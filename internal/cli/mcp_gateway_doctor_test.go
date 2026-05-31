package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	postgresdeploy "github.com/fyne-coder/gongcli_mcp/internal/deploy/postgres"
	"github.com/golang-jwt/jwt/v5"
)

func TestDoctorMCPGatewayHappyPathWithDCR(t *testing.T) {
	server := newMCPGatewayDoctorTestServer(t, mcpGatewayDoctorTestOptions{ExpectDCR: true})
	withDoctorHTTPClient(t, server)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"doctor", "mcp-gateway", "--url", server.URL + "/mcp", "--issuer", server.URL, "--expect-dcr"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(doctor mcp-gateway) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var response mcpGatewayDoctorResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v\n%s", err, stdout.String())
	}
	if response.MCPURL != server.URL+"/mcp" || !response.ExpectDCR {
		t.Fatalf("unexpected response target: %+v", response)
	}
	for _, name := range []string{
		"protected_resource_metadata",
		"endpoint_protected_resource_metadata",
		"unauthenticated_get_challenge",
		"unauthenticated_post_challenge",
		"oidc_jwks",
		"dcr_authorization_server_metadata",
		"oauth_authorization_server_metadata",
	} {
		if !hasDeployCheck(response.Checks, name, postgresdeploy.CheckPass) {
			t.Fatalf("missing passing check %s: %+v", name, response.Checks)
		}
	}
}

func TestDoctorMCPGatewayAllowsSeparateAuthorizationServer(t *testing.T) {
	server := newMCPGatewayDoctorTestServer(t, mcpGatewayDoctorTestOptions{AuthServerPath: "/jumpcloud-as"})
	withDoctorHTTPClient(t, server)

	response := runDoctorMCPGatewayWithTokenForTest(t, doctorMCPGatewayCLIFlags{
		URL:                 server.URL,
		Issuer:              server.URL,
		AuthorizationServer: server.URL + "/jumpcloud-as",
	})
	if response.ExpectedIssuer != server.URL {
		t.Fatalf("expected_issuer=%q", response.ExpectedIssuer)
	}
	if response.ExpectedAuthServer != server.URL+"/jumpcloud-as" {
		t.Fatalf("expected_authorization_server=%q", response.ExpectedAuthServer)
	}
	if !hasDeployCheck(response.Checks, "protected_resource_metadata_authorization_server", postgresdeploy.CheckPass) {
		t.Fatalf("missing protected-resource auth-server pass: %+v", response.Checks)
	}
	if !hasDeployCheck(response.Checks, "oauth_authorization_server_metadata", postgresdeploy.CheckPass) {
		t.Fatalf("missing OAuth AS metadata pass: %+v", response.Checks)
	}
}

func TestDoctorMCPGatewaySupportsAlternateMCPPath(t *testing.T) {
	server := newMCPGatewayDoctorTestServer(t, mcpGatewayDoctorTestOptions{MCPPath: "/mcp-jc-as-1c3d7f"})
	withDoctorHTTPClient(t, server)

	response := runDoctorMCPGatewayForTest(t, server.URL+"/mcp-jc-as-1c3d7f", server.URL)
	if response.GatewayBaseURL != server.URL {
		t.Fatalf("gateway_base_url=%q", response.GatewayBaseURL)
	}
	if response.MCPURL != server.URL+"/mcp-jc-as-1c3d7f" {
		t.Fatalf("mcp_url=%q", response.MCPURL)
	}
	if !hasDeployCheck(response.Checks, "endpoint_protected_resource_metadata", postgresdeploy.CheckPass) {
		t.Fatalf("missing alternate endpoint metadata pass: %+v", response.Checks)
	}
	if !hasDeployCheck(response.Checks, "unauthenticated_get_challenge", postgresdeploy.CheckPass) {
		t.Fatalf("missing alternate challenge pass: %+v", response.Checks)
	}
}

func TestDoctorMCPGatewayReportsMissingAuthorizationServerMetadata(t *testing.T) {
	server := newMCPGatewayDoctorTestServer(t, mcpGatewayDoctorTestOptions{MissingOAuthASMetadata: true, MissingOIDCDiscovery: true})
	withDoctorHTTPClient(t, server)

	response := runDoctorMCPGatewayForTest(t, server.URL, server.URL)
	check := findDeployCheck(response.Checks, "oauth_authorization_server_metadata")
	if check == nil || check.Status != postgresdeploy.CheckFail || check.ErrorKind != "metadata_unreachable" {
		t.Fatalf("oauth_authorization_server_metadata=%+v", check)
	}
}

func TestDoctorMCPGatewayFallsBackToOpenIDConfigurationForAuthorizationServer(t *testing.T) {
	server := newMCPGatewayDoctorTestServer(t, mcpGatewayDoctorTestOptions{
		MissingOAuthASMetadata:      true,
		MissingRegistrationEndpoint: true,
	})
	withDoctorHTTPClient(t, server)

	response := runDoctorMCPGatewayForTest(t, server.URL, server.URL)
	check := findDeployCheck(response.Checks, "oauth_authorization_server_metadata")
	if check == nil || check.Status != postgresdeploy.CheckWarn || check.Message != "OAuth authorization-server metadata is static-client only" {
		t.Fatalf("oauth_authorization_server_metadata=%+v", check)
	}
}

func TestDoctorMCPGatewayWarnsForStaticClientOnlyAuthorizationServer(t *testing.T) {
	server := newMCPGatewayDoctorTestServer(t, mcpGatewayDoctorTestOptions{MissingRegistrationEndpoint: true})
	withDoctorHTTPClient(t, server)

	response := runDoctorMCPGatewayForTest(t, server.URL, server.URL)
	check := findDeployCheck(response.Checks, "oauth_authorization_server_metadata")
	if check == nil || check.Status != postgresdeploy.CheckWarn || check.Message != "OAuth authorization-server metadata is static-client only" {
		t.Fatalf("oauth_authorization_server_metadata=%+v", check)
	}
	if !checkHasEvidence(check, "registration_endpoint", "absent") {
		t.Fatalf("missing registration_endpoint evidence: %+v", check)
	}
}

func TestDoctorMCPGatewayReportsWrongResource(t *testing.T) {
	server := newMCPGatewayDoctorTestServer(t, mcpGatewayDoctorTestOptions{WrongResource: true})
	withDoctorHTTPClient(t, server)

	response := runDoctorMCPGatewayForTest(t, server.URL, server.URL)
	if !hasDeployCheck(response.Checks, "protected_resource_metadata_resource", postgresdeploy.CheckFail) {
		t.Fatalf("missing root resource failure: %+v", response.Checks)
	}
	if !hasDeployCheck(response.Checks, "endpoint_protected_resource_metadata_resource", postgresdeploy.CheckFail) {
		t.Fatalf("missing endpoint resource failure: %+v", response.Checks)
	}
}

func TestDoctorMCPGatewayReportsMissingChallenge(t *testing.T) {
	server := newMCPGatewayDoctorTestServer(t, mcpGatewayDoctorTestOptions{MissingChallenge: true})
	withDoctorHTTPClient(t, server)

	response := runDoctorMCPGatewayForTest(t, server.URL, server.URL)
	if !hasDeployCheck(response.Checks, "unauthenticated_get_challenge", postgresdeploy.CheckFail) {
		t.Fatalf("missing GET challenge failure: %+v", response.Checks)
	}
	if !hasDeployCheck(response.Checks, "unauthenticated_post_challenge", postgresdeploy.CheckFail) {
		t.Fatalf("missing POST challenge failure: %+v", response.Checks)
	}
}

func TestDoctorMCPGatewayRejectsFalseBearerChallenge(t *testing.T) {
	server := newMCPGatewayDoctorTestServer(t, mcpGatewayDoctorTestOptions{FalseBearerChallenge: true})
	withDoctorHTTPClient(t, server)

	response := runDoctorMCPGatewayForTest(t, server.URL, server.URL)
	if !hasDeployCheck(response.Checks, "unauthenticated_get_challenge", postgresdeploy.CheckFail) {
		t.Fatalf("missing GET challenge failure: %+v", response.Checks)
	}
}

func TestDoctorMCPGatewayReportsMissingRequiredScope(t *testing.T) {
	server := newMCPGatewayDoctorTestServer(t, mcpGatewayDoctorTestOptions{MissingRequiredScope: true})
	withDoctorHTTPClient(t, server)

	response := runDoctorMCPGatewayForTest(t, server.URL, server.URL)
	if !hasDeployCheck(response.Checks, "protected_resource_metadata_scopes", postgresdeploy.CheckFail) {
		t.Fatalf("missing root scope failure: %+v", response.Checks)
	}
	if !hasDeployCheck(response.Checks, "endpoint_protected_resource_metadata_scopes", postgresdeploy.CheckFail) {
		t.Fatalf("missing endpoint scope failure: %+v", response.Checks)
	}
}

func TestDoctorMCPGatewayReportsDiscoveryIssuerMismatch(t *testing.T) {
	server := newMCPGatewayDoctorTestServer(t, mcpGatewayDoctorTestOptions{WrongDiscoveryIssuer: true})
	withDoctorHTTPClient(t, server)

	response := runDoctorMCPGatewayForTest(t, server.URL, server.URL)
	check := findDeployCheck(response.Checks, "oidc_discovery")
	if check == nil || check.Status != postgresdeploy.CheckFail || check.ErrorKind != "issuer_mismatch" {
		t.Fatalf("oidc_discovery=%+v", check)
	}
}

func TestDoctorMCPGatewayRejectsURLUserinfoWithoutLeaking(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"doctor", "mcp-gateway", "--url", "https://user:secret@example.com/mcp"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(doctor mcp-gateway) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "user:secret") || strings.Contains(stdout.String()+stderr.String(), "secret@example.com") {
		t.Fatalf("doctor output leaked URL userinfo: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	var response mcpGatewayDoctorResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !hasDeployCheck(response.Checks, "gateway_url", postgresdeploy.CheckFail) {
		t.Fatalf("missing gateway URL failure: %+v", response.Checks)
	}
}

func TestDoctorMCPGatewayDirectOIDCTokenShapeDoesNotLeakSensitiveValues(t *testing.T) {
	token := doctorTestUnsignedJWT(t, jwt.MapClaims{
		"scp": []string{"openid", "gongmcp/read"},
		"aud": []string{"claude-client"},
		"exp": time.Now().Add(time.Hour).Unix(),
		"ext": map[string]any{
			"email":    "operator@example.test",
			"memberOf": []any{"GongMCP-Users", "Other"},
		},
	})
	t.Setenv("GONGMCP_TEST_ACCESS_TOKEN", token)

	response := runDoctorMCPGatewayWithTokenForTest(t, doctorMCPGatewayCLIFlags{
		Profile:       "direct-oidc",
		GroupClaim:    "memberOf",
		ClientID:      "claude-client",
		RequiredGroup: "GongMCP-Users",
		TokenEnv:      "GONGMCP_TEST_ACCESS_TOKEN",
	})
	if response.DoctorProfile != "direct-oidc" {
		t.Fatalf("doctor_profile=%q", response.DoctorProfile)
	}
	combined, _ := json.Marshal(response)
	output := string(combined)
	for _, forbidden := range []string{
		token,
		"operator@example.test",
		"GongMCP-Users",
		"claude-client",
		"Other",
	} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("doctor output leaked %q:\n%s", forbidden, output)
		}
	}
	if !hasDeployCheck(response.Checks, "token_shape_group_claim", postgresdeploy.CheckPass) {
		t.Fatalf("missing direct-OIDC group pass: %+v", response.Checks)
	}
	if !hasDeployCheck(response.Checks, "token_shape_scope", postgresdeploy.CheckPass) {
		t.Fatalf("missing direct-OIDC scope pass: %+v", response.Checks)
	}
	if !hasDeployCheck(response.Checks, "token_shape_token_use", postgresdeploy.CheckPass) {
		t.Fatalf("missing direct-OIDC missing token_use pass: %+v", response.Checks)
	}
	if !hasDeployCheck(response.Checks, "token_shape_client_binding", postgresdeploy.CheckPass) {
		t.Fatalf("missing direct-OIDC aud client binding pass: %+v", response.Checks)
	}
	if !hasDeployCheck(response.Checks, "token_shape_email_claim", postgresdeploy.CheckPass) {
		t.Fatalf("missing direct-OIDC nested email pass: %+v", response.Checks)
	}
}

func TestDoctorMCPGatewayCognitoProfileRejectsSCPOnlyScope(t *testing.T) {
	token := doctorTestUnsignedJWT(t, jwt.MapClaims{
		"token_use": "access",
		"client_id": "cognito-client",
		"scp":       []string{"gongmcp/read"},
		"exp":       time.Now().Add(time.Hour).Unix(),
	})
	t.Setenv("GONGMCP_TEST_ACCESS_TOKEN", token)

	response := runDoctorMCPGatewayWithTokenForTest(t, doctorMCPGatewayCLIFlags{
		Profile:  "cognito",
		ClientID: "cognito-client",
		TokenEnv: "GONGMCP_TEST_ACCESS_TOKEN",
	})
	if !hasDeployCheck(response.Checks, "token_shape_scope", postgresdeploy.CheckFail) {
		t.Fatalf("expected Cognito scp-only scope failure: %+v", response.Checks)
	}
}

func TestDoctorMCPGatewayCognitoProfileRejectsMissingTokenUse(t *testing.T) {
	token := doctorTestUnsignedJWT(t, jwt.MapClaims{
		"scope":     "gongmcp/read",
		"client_id": "cognito-client",
		"exp":       time.Now().Add(time.Hour).Unix(),
	})
	t.Setenv("GONGMCP_TEST_ACCESS_TOKEN", token)

	response := runDoctorMCPGatewayWithTokenForTest(t, doctorMCPGatewayCLIFlags{
		Profile:  "cognito",
		ClientID: "cognito-client",
		TokenEnv: "GONGMCP_TEST_ACCESS_TOKEN",
	})
	check := findDeployCheck(response.Checks, "token_shape_token_use")
	if check == nil || check.Status != postgresdeploy.CheckFail || check.ErrorKind != "token_use_missing_or_not_access" {
		t.Fatalf("token_shape_token_use=%+v", check)
	}
}

func TestDoctorMCPGatewayAuthenticatedToolsListStatusRemediation(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		errorKind string
	}{
		{name: "401", status: http.StatusUnauthorized, errorKind: "auth_rejected"},
		{name: "403", status: http.StatusForbidden, errorKind: "authorization_denied"},
		{name: "502", status: http.StatusBadGateway, errorKind: "upstream_unreachable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newMCPGatewayDoctorTestServer(t, mcpGatewayDoctorTestOptions{
				AuthenticatedTools:  true,
				AuthenticatedStatus: tt.status,
			})
			withDoctorHTTPClient(t, server)
			t.Setenv("GONGMCP_TEST_ACCESS_TOKEN", doctorTestUnsignedJWT(t, jwt.MapClaims{
				"token_use": "access",
				"scope":     "gongmcp/read",
				"client_id": "test-client",
				"exp":       time.Now().Add(time.Hour).Unix(),
			}))

			response := runDoctorMCPGatewayWithTokenForTest(t, doctorMCPGatewayCLIFlags{
				URL:      server.URL,
				Issuer:   server.URL,
				TokenEnv: "GONGMCP_TEST_ACCESS_TOKEN",
			})
			check := findDeployCheck(response.Checks, "authenticated_tools_list")
			if check == nil || check.ErrorKind != tt.errorKind {
				t.Fatalf("authenticated_tools_list=%+v want error_kind=%s", check, tt.errorKind)
			}
		})
	}
}

func TestDoctorMCPGatewayTokenEnvDoesNotLeakTokenOrBody(t *testing.T) {
	server := newMCPGatewayDoctorTestServer(t, mcpGatewayDoctorTestOptions{AuthenticatedTools: true})
	withDoctorHTTPClient(t, server)
	t.Setenv("GONGMCP_TEST_ACCESS_TOKEN", "test-access-placeholder")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"doctor", "mcp-gateway", "--url", server.URL, "--issuer", server.URL, "--token-env", "GONGMCP_TEST_ACCESS_TOKEN"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(doctor mcp-gateway) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	for _, forbidden := range []string{"test-access-placeholder", "Sensitive Tool Name", "sensitive_tool"} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("doctor output leaked %q:\n%s", forbidden, combined)
		}
	}
	var response mcpGatewayDoctorResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if response.AuthenticatedCheck != "attempted" {
		t.Fatalf("authenticated_check=%q", response.AuthenticatedCheck)
	}
	if !hasDeployCheck(response.Checks, "authenticated_tools_list", postgresdeploy.CheckPass) {
		t.Fatalf("missing authenticated pass: %+v", response.Checks)
	}
}

func runDoctorMCPGatewayForTest(t *testing.T, rawURL, issuer string) mcpGatewayDoctorResponse {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"doctor", "mcp-gateway", "--url", rawURL, "--issuer", issuer}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(doctor mcp-gateway) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var response mcpGatewayDoctorResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v\n%s", err, stdout.String())
	}
	return response
}

type doctorMCPGatewayCLIFlags struct {
	URL                 string
	Issuer              string
	AuthorizationServer string
	Profile             string
	GroupClaim          string
	ClientID            string
	RequiredGroup       string
	TokenEnv            string
}

type mcpGatewayDoctorTestOptions struct {
	ExpectDCR                   bool
	MCPPath                     string
	AuthServerPath              string
	WrongResource               bool
	MissingChallenge            bool
	FalseBearerChallenge        bool
	MissingRequiredScope        bool
	WrongDiscoveryIssuer        bool
	MissingOAuthASMetadata      bool
	MissingOIDCDiscovery        bool
	MissingRegistrationEndpoint bool
	AuthenticatedTools          bool
	AuthenticatedStatus         int
}

func newMCPGatewayDoctorTestServer(t *testing.T, opts mcpGatewayDoctorTestOptions) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		baseURL := server.URL
		mcpPath := opts.MCPPath
		if mcpPath == "" {
			mcpPath = "/mcp"
		}
		mcpURL := baseURL + mcpPath
		authServerURL := baseURL
		if opts.AuthServerPath != "" {
			authServerURL = baseURL + opts.AuthServerPath
		}
		resource := mcpURL
		if opts.WrongResource {
			resource = baseURL + "/wrong"
		}
		scopes := []string{"gongmcp/read"}
		if opts.MissingRequiredScope {
			scopes = []string{"openid"}
		}
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource/mcp-jc-as-1c3d7f":
			writeTestJSON(t, w, map[string]any{
				"resource":                 resource,
				"authorization_servers":    []string{authServerURL},
				"scopes_supported":         scopes,
				"bearer_methods_supported": []string{"header"},
			})
		case "/.well-known/openid-configuration":
			if opts.MissingOIDCDiscovery {
				http.NotFound(w, r)
				return
			}
			issuer := baseURL
			if opts.WrongDiscoveryIssuer {
				issuer = baseURL + "/wrong-issuer"
			}
			oidcMetadata := map[string]any{
				"issuer":                                issuer,
				"authorization_endpoint":                baseURL + "/oauth2/authorize",
				"token_endpoint":                        baseURL + "/oauth2/token",
				"jwks_uri":                              baseURL + "/jwks",
				"scopes_supported":                      []string{"openid", "email", "gongmcp/read"},
				"response_types_supported":              []string{"code"},
				"grant_types_supported":                 []string{"authorization_code"},
				"token_endpoint_auth_methods_supported": []string{"none"},
				"code_challenge_methods_supported":      []string{"S256"},
			}
			if !opts.MissingRegistrationEndpoint {
				oidcMetadata["registration_endpoint"] = baseURL + "/register"
			}
			writeTestJSON(t, w, oidcMetadata)
		case "/.well-known/oauth-authorization-server", "/.well-known/oauth-authorization-server/jumpcloud-as", "/jumpcloud-as/.well-known/oauth-authorization-server":
			if opts.MissingOAuthASMetadata {
				http.NotFound(w, r)
				return
			}
			issuer := baseURL
			if opts.AuthServerPath != "" {
				issuer = baseURL + opts.AuthServerPath
			}
			asMetadata := map[string]any{
				"issuer":                                issuer,
				"authorization_endpoint":                baseURL + "/oauth2/authorize",
				"token_endpoint":                        baseURL + "/oauth2/token",
				"jwks_uri":                              baseURL + "/jwks",
				"scopes_supported":                      []string{"openid", "email", "gongmcp/read"},
				"response_types_supported":              []string{"code"},
				"grant_types_supported":                 []string{"authorization_code"},
				"token_endpoint_auth_methods_supported": []string{"none"},
				"code_challenge_methods_supported":      []string{"S256"},
			}
			if !opts.MissingRegistrationEndpoint {
				asMetadata["registration_endpoint"] = baseURL + "/register"
			}
			writeTestJSON(t, w, asMetadata)
		case "/jwks":
			writeTestJSON(t, w, map[string]any{"keys": []map[string]any{{"kid": "test-key"}}})
		case "/mcp", "/mcp-jc-as-1c3d7f":
			if opts.AuthenticatedTools && strings.HasPrefix(r.Header.Get("Authorization"), testBearerHeader("")) {
				status := opts.AuthenticatedStatus
				if status == 0 {
					status = http.StatusOK
				}
				if status != http.StatusOK {
					http.Error(w, "authenticated", status)
					return
				}
				writeTestJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      1,
					"result": map[string]any{
						"tools": []map[string]any{{"name": "sensitive_tool", "description": "Sensitive Tool Name"}},
					},
				})
				return
			}
			if !opts.MissingChallenge {
				if opts.FalseBearerChallenge {
					w.Header().Set("WWW-Authenticate", fmt.Sprintf(`NotBearer resource_metadata="%s/.well-known/oauth-protected-resource%s", scope="gongmcp/read"`, baseURL, mcpPath))
				} else {
					w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource%s", scope="gongmcp/read"`, baseURL, mcpPath))
				}
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	})
	server = httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	return server
}

func withDoctorHTTPClient(t *testing.T, server *httptest.Server) {
	t.Helper()
	original := newDoctorHTTPClient
	newDoctorHTTPClient = func(timeout time.Duration) *http.Client {
		client := server.Client()
		client.Timeout = timeout
		return client
	}
	t.Cleanup(func() {
		newDoctorHTTPClient = original
	})
}

func testBearerHeader(value string) string {
	return "Bear" + "er " + value
}

func runDoctorMCPGatewayWithTokenForTest(t *testing.T, flags doctorMCPGatewayCLIFlags) mcpGatewayDoctorResponse {
	t.Helper()
	if flags.URL == "" || flags.Issuer == "" {
		server := newMCPGatewayDoctorTestServer(t, mcpGatewayDoctorTestOptions{})
		withDoctorHTTPClient(t, server)
		flags.URL = server.URL
		flags.Issuer = server.URL
	}
	args := []string{"doctor", "mcp-gateway", "--url", flags.URL, "--issuer", flags.Issuer}
	if flags.AuthorizationServer != "" {
		args = append(args, "--authorization-server", flags.AuthorizationServer)
	}
	if flags.Profile != "" {
		args = append(args, "--profile", flags.Profile)
	}
	if flags.GroupClaim != "" {
		args = append(args, "--group-claim", flags.GroupClaim)
	}
	if flags.ClientID != "" {
		args = append(args, "--client-id", flags.ClientID)
	}
	if flags.RequiredGroup != "" {
		args = append(args, "--required-group", flags.RequiredGroup)
	}
	if flags.TokenEnv != "" {
		args = append(args, "--token-env", flags.TokenEnv)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), args, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(doctor mcp-gateway) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var response mcpGatewayDoctorResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v\n%s", err, stdout.String())
	}
	return response
}

func doctorTestUnsignedJWT(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	return signed
}

func findDeployCheck(checks []postgresdeploy.Check, name string) *postgresdeploy.Check {
	for i := range checks {
		if checks[i].Name == name {
			return &checks[i]
		}
	}
	return nil
}

func checkHasEvidence(check *postgresdeploy.Check, key, value string) bool {
	if check == nil {
		return false
	}
	for _, evidence := range check.Evidence {
		if evidence.Key == key && evidence.Value == value {
			return true
		}
	}
	return false
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("json encode: %v", err)
	}
}
