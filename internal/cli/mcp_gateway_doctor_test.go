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
	} {
		if !hasDeployCheck(response.Checks, name, postgresdeploy.CheckPass) {
			t.Fatalf("missing passing check %s: %+v", name, response.Checks)
		}
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

func TestDoctorMCPGatewayAcceptsRootResourceMetadataChallenge(t *testing.T) {
	server := newMCPGatewayDoctorTestServer(t, mcpGatewayDoctorTestOptions{RootMetadataChallenge: true})
	withDoctorHTTPClient(t, server)

	response := runDoctorMCPGatewayForTest(t, server.URL, server.URL)
	if !hasDeployCheck(response.Checks, "unauthenticated_get_challenge", postgresdeploy.CheckPass) {
		t.Fatalf("missing GET challenge pass: %+v", response.Checks)
	}
	if !hasDeployCheck(response.Checks, "unauthenticated_post_challenge", postgresdeploy.CheckPass) {
		t.Fatalf("missing POST challenge pass: %+v", response.Checks)
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

type mcpGatewayDoctorTestOptions struct {
	ExpectDCR             bool
	WrongResource         bool
	MissingChallenge      bool
	FalseBearerChallenge  bool
	MissingRequiredScope  bool
	AuthenticatedTools    bool
	RootMetadataChallenge bool
}

func newMCPGatewayDoctorTestServer(t *testing.T, opts mcpGatewayDoctorTestOptions) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		baseURL := server.URL
		mcpURL := baseURL + "/mcp"
		resource := mcpURL
		if opts.WrongResource {
			resource = baseURL + "/wrong"
		}
		scopes := []string{"gongmcp/read"}
		if opts.MissingRequiredScope {
			scopes = []string{"openid"}
		}
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp":
			writeTestJSON(t, w, map[string]any{
				"resource":                 resource,
				"authorization_servers":    []string{baseURL},
				"scopes_supported":         scopes,
				"bearer_methods_supported": []string{"header"},
			})
		case "/.well-known/openid-configuration":
			writeTestJSON(t, w, map[string]any{
				"issuer":                 baseURL,
				"authorization_endpoint": baseURL + "/oauth2/authorize",
				"token_endpoint":         baseURL + "/oauth2/token",
				"jwks_uri":               baseURL + "/jwks",
			})
		case "/.well-known/oauth-authorization-server":
			writeTestJSON(t, w, map[string]any{
				"issuer":                                baseURL,
				"authorization_endpoint":                baseURL + "/oauth2/authorize",
				"token_endpoint":                        baseURL + "/oauth2/token",
				"registration_endpoint":                 baseURL + "/register",
				"jwks_uri":                              baseURL + "/jwks",
				"scopes_supported":                      []string{"openid", "email", "gongmcp/read"},
				"response_types_supported":              []string{"code"},
				"grant_types_supported":                 []string{"authorization_code"},
				"token_endpoint_auth_methods_supported": []string{"none"},
				"code_challenge_methods_supported":      []string{"S256"},
			})
		case "/jwks":
			writeTestJSON(t, w, map[string]any{"keys": []map[string]any{{"kid": "test-key"}}})
		case "/mcp":
			if opts.AuthenticatedTools && r.Header.Get("Authorization") == testBearerHeader("test-access-placeholder") {
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
				resourceMetadata := baseURL + "/.well-known/oauth-protected-resource/mcp"
				if opts.RootMetadataChallenge {
					resourceMetadata = baseURL + "/.well-known/oauth-protected-resource"
				}
				if opts.FalseBearerChallenge {
					w.Header().Set("WWW-Authenticate", fmt.Sprintf(`NotBearer resource_metadata="%s", scope="gongmcp/read"`, resourceMetadata))
				} else {
					w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s", scope="gongmcp/read"`, resourceMetadata))
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

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("json encode: %v", err)
	}
}
