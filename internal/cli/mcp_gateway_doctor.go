package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	postgresdeploy "github.com/fyne-coder/gongcli_mcp/internal/deploy/postgres"
	"github.com/golang-jwt/jwt/v5"
)

var newDoctorHTTPClient = func(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

type mcpGatewayDoctorResponse struct {
	Target               string                 `json:"target"`
	GatewayBaseURL       string                 `json:"gateway_base_url"`
	MCPURL               string                 `json:"mcp_url"`
	ExpectedIssuer       string                 `json:"expected_issuer,omitempty"`
	ExpectedAuthServer   string                 `json:"expected_authorization_server,omitempty"`
	DoctorProfile        string                 `json:"doctor_profile,omitempty"`
	ExpectDCR            bool                   `json:"expect_dcr"`
	AuthenticatedCheck   string                 `json:"authenticated_check"`
	Checks               []postgresdeploy.Check `json:"checks"`
	SensitiveDataWarning string                 `json:"sensitive_data_warning"`
}

func (a *app) doctorMCPGateway(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor mcp-gateway", flag.ContinueOnError)
	fs.SetOutput(a.err)
	rawURL := fs.String("url", "", "public MCP gateway base URL or /mcp URL")
	expectedIssuer := fs.String("issuer", "", "expected OIDC issuer URL")
	expectedAuthServer := fs.String("authorization-server", "", "expected OAuth authorization server URL advertised by protected-resource metadata; defaults to --issuer, or gateway base URL with --expect-dcr")
	requiredScope := fs.String("required-scope", "gongmcp/read", "required MCP OAuth scope expected in metadata")
	profile := fs.String("profile", "cognito", "token-shape diagnostic profile: cognito or direct-oidc (jumpcloud aliases direct-oidc)")
	groupClaim := fs.String("group-claim", "", "configured access-token group claim for token-shape diagnostics")
	clientID := fs.String("client-id", "", "expected OAuth client ID for token-shape client-binding diagnostics")
	requiredGroup := fs.String("required-group", "", "required group value for token-shape policy diagnostics without printing token group names")
	expectDCR := fs.Bool("expect-dcr", false, "require gateway-advertised DCR authorization-server metadata")
	tokenEnv := fs.String("token-env", "", "environment variable containing an optional access token for untrusted token-shape diagnostics and tools/list smoke")
	origin := fs.String("origin", "", "optional Origin header for CORS/preflight validation")
	timeout := fs.Duration("timeout", defaultHTTPTimeout, "HTTP timeout for gateway and metadata checks")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if fs.NArg() != 0 {
		return errUsage
	}

	normalizedProfile, err := normalizeDoctorProfile(*profile)
	if err != nil {
		return err
	}

	client := newDoctorHTTPClient(*timeout)
	response := runMCPGatewayDoctor(ctx, client, mcpGatewayDoctorOptions{
		RawURL:                      *rawURL,
		ExpectedIssuer:              strings.TrimRight(strings.TrimSpace(*expectedIssuer), "/"),
		ExpectedAuthorizationServer: strings.TrimRight(strings.TrimSpace(*expectedAuthServer), "/"),
		RequiredScope:               strings.TrimSpace(*requiredScope),
		DoctorProfile:               normalizedProfile,
		GroupClaim:                  strings.TrimSpace(*groupClaim),
		ClientID:                    strings.TrimSpace(*clientID),
		RequiredGroup:               strings.TrimSpace(*requiredGroup),
		ExpectDCR:                   *expectDCR,
		TokenEnv:                    strings.TrimSpace(*tokenEnv),
		Origin:                      strings.TrimSpace(*origin),
	})
	return writeJSONValue(a.out, response)
}

type mcpGatewayDoctorOptions struct {
	RawURL                      string
	ExpectedIssuer              string
	ExpectedAuthorizationServer string
	RequiredScope               string
	DoctorProfile               string
	GroupClaim                  string
	ClientID                    string
	RequiredGroup               string
	ExpectDCR                   bool
	TokenEnv                    string
	Origin                      string
}

type protectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
}

type authorizationServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint"`
	ClientIDMetadataDocumentSupported bool     `json:"client_id_metadata_document_supported"`
	JWKSURI                           string   `json:"jwks_uri"`
	ScopesSupported                   []string `json:"scopes_supported"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
}

func runMCPGatewayDoctor(ctx context.Context, client *http.Client, opts mcpGatewayDoctorOptions) mcpGatewayDoctorResponse {
	response := mcpGatewayDoctorResponse{
		ExpectedIssuer:       opts.ExpectedIssuer,
		DoctorProfile:        opts.DoctorProfile,
		ExpectDCR:            opts.ExpectDCR,
		AuthenticatedCheck:   "skipped",
		SensitiveDataWarning: "This output contains gateway metadata, HTTP status checks, and sanitized token-shape evidence only. Do not paste bearer tokens, client secrets, or decoded JWT payloads into support artifacts.",
	}
	baseURL, mcpURL, err := normalizeMCPGatewayURL(opts.RawURL)
	if err != nil {
		response.Checks = append(response.Checks, failCheck("gateway_url", "invalid_gateway_url", "gateway URL is invalid", "invalid_url", "pass an absolute https gateway base URL or /mcp URL"))
		return response
	}
	response.GatewayBaseURL = baseURL
	response.MCPURL = mcpURL
	response.Target = mcpURL
	expectedAuthServer := expectedDoctorAuthorizationServer(opts, baseURL)
	response.ExpectedAuthServer = expectedAuthServer

	rootMetadataURL := baseURL + "/.well-known/oauth-protected-resource"
	endpointMetadataURL := endpointProtectedResourceMetadataURL(baseURL, mcpURL)
	var rootMetadata protectedResourceMetadata
	if fetchJSONCheck(ctx, client, rootMetadataURL, &rootMetadata, &response.Checks, "protected_resource_metadata") {
		if mcpURL == baseURL+"/mcp" {
			response.Checks = append(response.Checks, validateProtectedResourceMetadata("protected_resource_metadata", rootMetadata, mcpURL, expectedAuthServer, baseURL, opts.RequiredScope, opts.ExpectDCR)...)
		} else {
			response.Checks = append(response.Checks, postgresdeploy.Check{
				Name:    "protected_resource_metadata_resource",
				Status:  postgresdeploy.CheckWarn,
				Message: "root protected-resource metadata was fetched but resource validation was skipped for an alternate MCP path",
				Evidence: []postgresdeploy.Evidence{
					{Key: "target_mcp_path", Value: mcpURL},
				},
			})
		}
	}

	var endpointMetadata protectedResourceMetadata
	if fetchJSONCheck(ctx, client, endpointMetadataURL, &endpointMetadata, &response.Checks, "endpoint_protected_resource_metadata") {
		response.Checks = append(response.Checks, validateProtectedResourceMetadata("endpoint_protected_resource_metadata", endpointMetadata, mcpURL, expectedAuthServer, baseURL, opts.RequiredScope, opts.ExpectDCR)...)
	}

	if opts.Origin != "" {
		response.Checks = append(response.Checks, checkCORSPreflight(ctx, client, mcpURL, opts.Origin))
	}
	response.Checks = append(response.Checks, checkMCPChallenge(ctx, client, http.MethodGet, mcpURL, endpointMetadataURL, opts.Origin, opts.RequiredScope))
	response.Checks = append(response.Checks, checkMCPChallenge(ctx, client, http.MethodPost, mcpURL, endpointMetadataURL, opts.Origin, opts.RequiredScope))

	authServerURL := firstAuthorizationServer(endpointMetadata, rootMetadata)
	if authServerURL == "" {
		authServerURL = expectedAuthServer
	}
	discoveryBase := authServerURL
	if opts.ExpectedIssuer != "" {
		discoveryBase = opts.ExpectedIssuer
	}
	if discoveryBase != "" {
		response.Checks = append(response.Checks, checkOIDCDiscovery(ctx, client, strings.TrimRight(discoveryBase, "/")))
	} else {
		response.Checks = append(response.Checks, failCheck("oidc_discovery", "authorization_server_missing", "OIDC discovery could not be checked because no authorization server was advertised", "", "fix protected-resource metadata authorization_servers or pass --issuer"))
	}

	if opts.ExpectDCR {
		response.Checks = append(response.Checks, checkDCRAuthorizationServer(ctx, client, baseURL))
	}
	if authServerURL != "" {
		response.Checks = append(response.Checks, checkOAuthAuthorizationServerMetadata(ctx, client, authServerURL, opts.RequiredScope))
	}

	if opts.TokenEnv != "" {
		token := strings.TrimSpace(os.Getenv(opts.TokenEnv))
		if token == "" {
			response.AuthenticatedCheck = "skipped_missing_token_env"
			response.Checks = append(response.Checks, postgresdeploy.Check{
				Name:        "authenticated_tools_list",
				Status:      postgresdeploy.CheckWarn,
				ErrorKind:   "token_env_missing",
				Message:     "authenticated tools/list smoke skipped because token env var is unset",
				Remediation: "set the token env var only in the operator shell, then rerun; do not paste tokens into shared logs",
				Evidence:    []postgresdeploy.Evidence{{Key: "token_env", Value: opts.TokenEnv}},
			})
		} else {
			response.AuthenticatedCheck = "attempted"
			response.Checks = append(response.Checks, tokenShapeDiagnostics(opts, token)...)
			response.Checks = append(response.Checks, checkAuthenticatedToolsList(ctx, client, mcpURL, opts.Origin, opts.TokenEnv, token))
		}
	}

	return response
}

func normalizeDoctorProfile(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "cognito":
		return "cognito", nil
	case "direct-oidc", "direct_oidc", "directoidc", "oidc", "jumpcloud":
		return "direct-oidc", nil
	default:
		return "", fmt.Errorf("doctor mcp-gateway: --profile must be cognito or direct-oidc")
	}
}

func expectedDoctorAuthorizationServer(opts mcpGatewayDoctorOptions, baseURL string) string {
	switch {
	case opts.ExpectedAuthorizationServer != "":
		return opts.ExpectedAuthorizationServer
	case opts.ExpectDCR:
		return baseURL
	case opts.ExpectedIssuer != "":
		return opts.ExpectedIssuer
	default:
		return ""
	}
}

func normalizeMCPGatewayURL(raw string) (string, string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return "", "", fmt.Errorf("--url is required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", "", fmt.Errorf("URL could not be parsed")
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return "", "", fmt.Errorf("URL must be absolute https")
	}
	if parsed.User != nil {
		return "", "", fmt.Errorf("URL must not include userinfo")
	}
	if parsed.Fragment != "" {
		return "", "", fmt.Errorf("URL must not include a fragment")
	}
	base := trimmed
	mcpURL := ""
	if strings.HasSuffix(parsed.Path, "/mcp") {
		base = strings.TrimSuffix(trimmed, "/mcp")
		mcpURL = trimmed
	}
	if isAlternateMCPPath(parsed.Path) {
		base = parsed.Scheme + "://" + parsed.Host
		mcpURL = trimmed
	}
	if mcpURL == "" {
		mcpURL = base + "/mcp"
	}
	return base, mcpURL, nil
}

func isAlternateMCPPath(path string) bool {
	segment := strings.Trim(strings.TrimSpace(path), "/")
	return strings.HasPrefix(segment, "mcp-") || strings.HasPrefix(segment, "mcp_")
}

func endpointProtectedResourceMetadataURL(baseURL, mcpURL string) string {
	baseParsed, baseErr := url.Parse(baseURL)
	mcpParsed, mcpErr := url.Parse(mcpURL)
	if baseErr != nil || mcpErr != nil {
		return strings.TrimRight(baseURL, "/") + "/.well-known/oauth-protected-resource/mcp"
	}
	mcpPath := strings.Trim(mcpParsed.EscapedPath(), "/")
	basePath := strings.Trim(baseParsed.EscapedPath(), "/")
	if basePath != "" && strings.HasPrefix(mcpPath, basePath+"/") {
		mcpPath = strings.TrimPrefix(mcpPath, basePath+"/")
	}
	if mcpPath == "" {
		mcpPath = "mcp"
	}
	return strings.TrimRight(baseURL, "/") + "/.well-known/oauth-protected-resource/" + mcpPath
}

func fetchJSONCheck(ctx context.Context, client *http.Client, rawURL string, target any, checks *[]postgresdeploy.Check, name string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		*checks = append(*checks, failCheck(name, "request_build_failed", "metadata request could not be built", "", "verify the gateway URL"))
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		*checks = append(*checks, failCheck(name, "request_failed", "metadata endpoint could not be reached", "", "verify DNS, TLS, WAF, ingress, and gateway logs"))
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		*checks = append(*checks, failCheck(name, "unexpected_http_status", "metadata endpoint returned a non-200 status", fmt.Sprintf("status=%d", resp.StatusCode), "route metadata paths to gongmcp-gateway"))
		return false
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := dec.Decode(target); err != nil {
		*checks = append(*checks, failCheck(name, "invalid_json", "metadata endpoint did not return valid JSON", "", "inspect gateway metadata handler output"))
		return false
	}
	*checks = append(*checks, postgresdeploy.Check{
		Name:     name,
		Status:   postgresdeploy.CheckPass,
		Message:  "metadata endpoint returned JSON",
		Evidence: []postgresdeploy.Evidence{{Key: "url", Value: rawURL}},
	})
	return true
}

func validateProtectedResourceMetadata(name string, metadata protectedResourceMetadata, expectedResource, expectedAuthServer, baseURL, requiredScope string, expectDCR bool) []postgresdeploy.Check {
	checks := []postgresdeploy.Check{}
	if metadata.Resource != expectedResource {
		checks = append(checks, failCheck(name+"_resource", "resource_mismatch", "protected-resource metadata resource does not match /mcp URL", fmt.Sprintf("resource=%s", metadata.Resource), "set metadata resource to the exact public MCP URL entered in Claude"))
	} else {
		checks = append(checks, passCheck(name+"_resource", "metadata resource matches public /mcp URL"))
	}
	authServer := ""
	if len(metadata.AuthorizationServers) > 0 {
		authServer = strings.TrimRight(metadata.AuthorizationServers[0], "/")
	}
	wantAuthServer := expectedAuthServer
	if authServer == "" {
		checks = append(checks, failCheck(name+"_authorization_server", "authorization_server_missing", "metadata is missing authorization_servers", "", "publish the Cognito issuer URL or gateway auth-server URL for DCR mode"))
	} else if err := requireHTTPSURL(authServer); err != nil {
		checks = append(checks, failCheck(name+"_authorization_server", "authorization_server_not_https", "authorization server must be an absolute https URL", "", "fix authorization_servers[0]"))
	} else if wantAuthServer != "" && authServer != strings.TrimRight(wantAuthServer, "/") {
		checks = append(checks, failCheck(name+"_authorization_server", "authorization_server_mismatch", "authorization server does not match expected value", fmt.Sprintf("authorization_server=%s", authServer), "verify --authorization-server, --issuer, DCR mode, and gateway metadata configuration"))
	} else if !expectDCR && expectedAuthServer == "" && authServer == baseURL {
		checks = append(checks, postgresdeploy.Check{
			Name:        name + "_authorization_server",
			Status:      postgresdeploy.CheckWarn,
			ErrorKind:   "dcr_metadata_not_requested",
			Message:     "authorization server points at the gateway; DCR may be enabled but --expect-dcr was not set",
			Remediation: "rerun with --expect-dcr if the gateway DCR fallback is enabled",
		})
	} else {
		checks = append(checks, passCheck(name+"_authorization_server", "authorization server metadata is present and expected"))
	}
	if !containsString(metadata.BearerMethodsSupported, "header") {
		checks = append(checks, failCheck(name+"_bearer_method", "bearer_header_missing", "bearer_methods_supported must include header", "", "advertise bearer token transport in the Authorization header"))
	} else {
		checks = append(checks, passCheck(name+"_bearer_method", "bearer_methods_supported includes header"))
	}
	if len(metadata.ScopesSupported) == 0 {
		checks = append(checks, failCheck(name+"_scopes", "scopes_missing", "scopes_supported is empty", "", "advertise the required MCP read scope"))
	} else if requiredScope != "" && !containsString(metadata.ScopesSupported, requiredScope) {
		checks = append(checks, failCheck(name+"_scopes", "required_scope_missing", "scopes_supported does not include the required MCP scope", fmt.Sprintf("required_scope=%s", requiredScope), "advertise the required MCP read scope in protected-resource metadata"))
	} else {
		checks = append(checks, postgresdeploy.Check{
			Name:    name + "_scopes",
			Status:  postgresdeploy.CheckPass,
			Message: "scopes_supported is populated",
			Evidence: []postgresdeploy.Evidence{
				{Key: "scope_count", Value: fmt.Sprintf("%d", len(metadata.ScopesSupported))},
			},
		})
	}
	return checks
}

func checkMCPChallenge(ctx context.Context, client *http.Client, method, mcpURL, expectedMetadataURL, origin, requiredScope string) postgresdeploy.Check {
	var body io.Reader = http.NoBody
	if method == http.MethodPost {
		body = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	}
	req, err := http.NewRequestWithContext(ctx, method, mcpURL, body)
	if err != nil {
		return failCheck("unauthenticated_"+strings.ToLower(method)+"_challenge", "request_build_failed", "challenge request could not be built", "", "verify the MCP URL")
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := client.Do(req)
	if err != nil {
		return failCheck("unauthenticated_"+strings.ToLower(method)+"_challenge", "request_failed", "unauthenticated /mcp request failed before response", "", "verify gateway reachability")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusUnauthorized {
		return failCheck("unauthenticated_"+strings.ToLower(method)+"_challenge", "expected_401", "unauthenticated /mcp must return 401", fmt.Sprintf("status=%d", resp.StatusCode), "route /mcp to gongmcp-gateway and verify auth middleware runs before upstream proxying")
	}
	challenge := resp.Header.Get("WWW-Authenticate")
	if !validBearerChallenge(challenge, expectedMetadataURL, requiredScope) {
		return failCheck("unauthenticated_"+strings.ToLower(method)+"_challenge", "invalid_www_authenticate", "401 response is missing Bearer resource_metadata and scope challenge", "", "set WWW-Authenticate to Bearer with resource_metadata and scope")
	}
	return passCheck("unauthenticated_"+strings.ToLower(method)+"_challenge", "unauthenticated /mcp returns a Bearer challenge")
}

func checkCORSPreflight(ctx context.Context, client *http.Client, mcpURL, origin string) postgresdeploy.Check {
	req, err := http.NewRequestWithContext(ctx, http.MethodOptions, mcpURL, nil)
	if err != nil {
		return failCheck("cors_preflight", "request_build_failed", "CORS preflight request could not be built", "", "verify the MCP URL")
	}
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
	resp, err := client.Do(req)
	if err != nil {
		return failCheck("cors_preflight", "request_failed", "CORS preflight request failed before response", "", "verify gateway reachability")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusNoContent {
		return failCheck("cors_preflight", "unexpected_http_status", "CORS preflight did not return 204", fmt.Sprintf("status=%d", resp.StatusCode), "verify gateway allowed origins")
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != origin {
		return failCheck("cors_preflight", "origin_not_allowed", "CORS preflight did not echo the expected origin", "", "configure GATEWAY_ALLOWED_ORIGINS for browser-based clients that send Origin")
	}
	return passCheck("cors_preflight", "CORS preflight allowed expected origin")
}

func checkOIDCDiscovery(ctx context.Context, client *http.Client, issuer string) postgresdeploy.Check {
	if err := requireHTTPSURL(issuer); err != nil {
		return failCheck("oidc_discovery", "issuer_not_https", "OIDC issuer must be an absolute https URL", "", "pass the Cognito issuer URL, not a private or http URL")
	}
	discoveryURL := issuer + "/.well-known/openid-configuration"
	var metadata authorizationServerMetadata
	var checks []postgresdeploy.Check
	if !fetchJSONCheck(ctx, client, discoveryURL, &metadata, &checks, "oidc_discovery") {
		return checks[len(checks)-1]
	}
	if strings.TrimRight(metadata.Issuer, "/") != issuer {
		return failCheck("oidc_discovery", "issuer_mismatch", "OIDC discovery issuer does not match requested issuer", "", "verify --issuer points at the same provider issuer used by the gateway")
	}
	if metadata.JWKSURI == "" {
		return failCheck("oidc_discovery", "jwks_uri_missing", "OIDC discovery metadata is missing jwks_uri", "", "verify the Cognito issuer URL")
	}
	if err := requireHTTPSURL(metadata.JWKSURI); err != nil {
		return failCheck("oidc_discovery", "jwks_uri_not_https", "OIDC jwks_uri must be an absolute https URL", "", "verify the Cognito issuer URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadata.JWKSURI, nil)
	if err != nil {
		return failCheck("oidc_jwks", "request_build_failed", "JWKS request could not be built", "", "verify jwks_uri")
	}
	resp, err := client.Do(req)
	if err != nil {
		return failCheck("oidc_jwks", "request_failed", "JWKS endpoint could not be reached", "", "verify Cognito JWKS reachability from the operator network")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return failCheck("oidc_jwks", "unexpected_http_status", "JWKS endpoint returned a non-200 status", fmt.Sprintf("status=%d", resp.StatusCode), "verify the Cognito issuer URL and network path")
	}
	var jwks struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&jwks); err != nil {
		return failCheck("oidc_jwks", "invalid_json", "JWKS endpoint did not return valid JSON", "", "verify Cognito JWKS endpoint")
	}
	if len(jwks.Keys) == 0 {
		return failCheck("oidc_jwks", "keys_missing", "JWKS endpoint returned no keys", "", "verify Cognito user pool signing keys")
	}
	return postgresdeploy.Check{
		Name:    "oidc_jwks",
		Status:  postgresdeploy.CheckPass,
		Message: "OIDC discovery and JWKS are reachable",
		Evidence: []postgresdeploy.Evidence{
			{Key: "issuer", Value: issuer},
			{Key: "jwks_key_count", Value: fmt.Sprintf("%d", len(jwks.Keys))},
		},
	}
}

func checkDCRAuthorizationServer(ctx context.Context, client *http.Client, baseURL string) postgresdeploy.Check {
	var metadata authorizationServerMetadata
	var checks []postgresdeploy.Check
	if !fetchJSONCheck(ctx, client, baseURL+"/.well-known/oauth-authorization-server", &metadata, &checks, "dcr_authorization_server_metadata") {
		return checks[len(checks)-1]
	}
	authURL, authErr := parseHTTPSMetadataEndpoint(metadata.AuthorizationEndpoint)
	tokenURL, tokenErr := parseHTTPSMetadataEndpoint(metadata.TokenEndpoint)
	switch {
	case metadata.RegistrationEndpoint != baseURL+"/register":
		return failCheck("dcr_authorization_server_metadata", "registration_endpoint_mismatch", "DCR metadata registration_endpoint does not match gateway /register", "", "verify GATEWAY_DCR_ENABLED and PUBLIC_BASE_URL")
	case authErr != nil || !strings.HasSuffix(authURL.Path, "/oauth2/authorize"):
		return failCheck("dcr_authorization_server_metadata", "authorization_endpoint_invalid", "DCR metadata authorization_endpoint should point at Cognito /oauth2/authorize", "", "verify COGNITO_DOMAIN_URL")
	case tokenErr != nil || !strings.HasSuffix(tokenURL.Path, "/oauth2/token"):
		return failCheck("dcr_authorization_server_metadata", "token_endpoint_invalid", "DCR metadata token_endpoint should point at Cognito /oauth2/token", "", "verify COGNITO_DOMAIN_URL")
	case authURL.Host != tokenURL.Host:
		return failCheck("dcr_authorization_server_metadata", "cognito_endpoint_host_mismatch", "DCR metadata authorization and token endpoints must share the same Cognito host", "", "verify COGNITO_DOMAIN_URL")
	case !containsString(metadata.CodeChallengeMethodsSupported, "S256"):
		return failCheck("dcr_authorization_server_metadata", "pkce_s256_missing", "DCR metadata must advertise PKCE S256", "", "verify gateway DCR metadata")
	case !containsString(metadata.TokenEndpointAuthMethodsSupported, "none"):
		return failCheck("dcr_authorization_server_metadata", "public_client_auth_missing", "DCR metadata must allow public-client token endpoint auth method none", "", "verify gateway DCR metadata")
	default:
		return passCheck("dcr_authorization_server_metadata", "DCR authorization-server metadata is gateway-shaped")
	}
}

func checkOAuthAuthorizationServerMetadata(ctx context.Context, client *http.Client, authServerURL, requiredScope string) postgresdeploy.Check {
	authServerURL = strings.TrimRight(strings.TrimSpace(authServerURL), "/")
	if err := requireHTTPSURL(authServerURL); err != nil {
		return failCheck("oauth_authorization_server_metadata", "authorization_server_not_https", "authorization server must be an absolute https URL", "", "fix protected-resource metadata authorization_servers")
	}
	candidates, err := oauthAuthorizationServerMetadataURLs(authServerURL)
	if err != nil {
		return failCheck("oauth_authorization_server_metadata", "authorization_server_invalid", "authorization server URL could not be converted to metadata paths", "", "fix protected-resource metadata authorization_servers")
	}
	var metadata authorizationServerMetadata
	selectedURL, failureEvidence, ok := fetchOAuthAuthorizationServerMetadata(ctx, client, candidates, &metadata)
	if !ok {
		return failCheck("oauth_authorization_server_metadata", "metadata_unreachable", "OAuth authorization-server metadata was not reachable at the RFC 8414 metadata paths", failureEvidence, "publish /.well-known/oauth-authorization-server metadata for the advertised authorization server, or advertise an authorization server that provides it")
	}

	issuer := strings.TrimRight(metadata.Issuer, "/")
	authEndpoint, authErr := parseHTTPSMetadataEndpoint(metadata.AuthorizationEndpoint)
	tokenEndpoint, tokenErr := parseHTTPSMetadataEndpoint(metadata.TokenEndpoint)
	switch {
	case issuer == "":
		return failCheck("oauth_authorization_server_metadata", "issuer_missing", "OAuth authorization-server metadata is missing issuer", fmt.Sprintf("url=%s", selectedURL), "set issuer to the advertised authorization server URL")
	case issuer != authServerURL:
		return failCheck("oauth_authorization_server_metadata", "issuer_mismatch", "OAuth authorization-server metadata issuer does not match the advertised authorization server", fmt.Sprintf("issuer=%s", issuer), "set issuer to the advertised authorization server URL or advertise the provider issuer directly")
	case authErr != nil || authEndpoint.Path == "":
		return failCheck("oauth_authorization_server_metadata", "authorization_endpoint_invalid", "OAuth authorization-server metadata authorization_endpoint is invalid", "", "publish an absolute https authorization endpoint")
	case tokenErr != nil || tokenEndpoint.Path == "":
		return failCheck("oauth_authorization_server_metadata", "token_endpoint_invalid", "OAuth authorization-server metadata token_endpoint is invalid", "", "publish an absolute https token endpoint")
	case !containsString(metadata.ResponseTypesSupported, "code"):
		return failCheck("oauth_authorization_server_metadata", "authorization_code_response_missing", "OAuth authorization-server metadata must advertise response_types_supported containing code", "", "enable authorization-code flow for the client used by Claude")
	case len(metadata.GrantTypesSupported) > 0 && !containsString(metadata.GrantTypesSupported, "authorization_code"):
		return failCheck("oauth_authorization_server_metadata", "authorization_code_grant_missing", "OAuth authorization-server metadata grant_types_supported does not include authorization_code", "", "enable authorization-code grant for the client used by Claude")
	case len(metadata.CodeChallengeMethodsSupported) > 0 && !containsString(metadata.CodeChallengeMethodsSupported, "S256"):
		return failCheck("oauth_authorization_server_metadata", "pkce_s256_missing", "OAuth authorization-server metadata does not advertise PKCE S256", "", "enable PKCE S256 or omit the field only if the provider does not advertise PKCE methods")
	case !hasCompatibleTokenEndpointAuthMethod(metadata.TokenEndpointAuthMethodsSupported):
		return failCheck("oauth_authorization_server_metadata", "token_endpoint_auth_method_missing", "OAuth authorization-server metadata does not advertise a Claude-compatible token endpoint auth method", "", "advertise client_secret_post, client_secret_basic, or none for the configured client")
	case requiredScope != "" && len(metadata.ScopesSupported) > 0 && !containsString(metadata.ScopesSupported, requiredScope):
		return failCheck("oauth_authorization_server_metadata", "required_scope_missing", "OAuth authorization-server metadata scopes_supported does not include the required MCP scope", fmt.Sprintf("required_scope=%s", requiredScope), "advertise the same MCP scope in authorization-server and protected-resource metadata")
	case metadata.RegistrationEndpoint == "" && !supportsClientIDMetadata(metadata):
		return postgresdeploy.Check{
			Name:        "oauth_authorization_server_metadata",
			Status:      postgresdeploy.CheckWarn,
			Message:     "OAuth authorization-server metadata is static-client only",
			Remediation: "for hosted Claude, verify custom connector OAuth Client ID/secret token exchange evidence, use Anthropic-held credentials, or put a DCR/CIMD-capable broker in front of this IdP",
			Evidence: []postgresdeploy.Evidence{
				{Key: "url", Value: selectedURL},
				{Key: "registration_endpoint", Value: "absent"},
				{Key: "client_id_metadata_document_supported", Value: fmt.Sprintf("%t", metadata.ClientIDMetadataDocumentSupported)},
			},
		}
	default:
		return postgresdeploy.Check{
			Name:    "oauth_authorization_server_metadata",
			Status:  postgresdeploy.CheckPass,
			Message: "OAuth authorization-server metadata is reachable and authorization-code shaped",
			Evidence: []postgresdeploy.Evidence{
				{Key: "url", Value: selectedURL},
				{Key: "authorization_endpoint_host", Value: authEndpoint.Host},
				{Key: "token_endpoint_host", Value: tokenEndpoint.Host},
			},
		}
	}
}

func oauthAuthorizationServerMetadataURLs(authServerURL string) ([]string, error) {
	parsed, err := url.Parse(authServerURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return nil, fmt.Errorf("authorization server must be absolute https without userinfo, query, or fragment")
	}
	origin := parsed.Scheme + "://" + parsed.Host
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	if path == "" {
		return []string{
			origin + "/.well-known/oauth-authorization-server",
			origin + "/.well-known/openid-configuration",
		}, nil
	}
	return []string{
		origin + "/.well-known/oauth-authorization-server" + path,
		origin + path + "/.well-known/oauth-authorization-server",
		origin + "/.well-known/openid-configuration" + path,
		origin + path + "/.well-known/openid-configuration",
	}, nil
}

func fetchOAuthAuthorizationServerMetadata(ctx context.Context, client *http.Client, urls []string, target *authorizationServerMetadata) (selectedURL, failureEvidence string, ok bool) {
	var attempts []string
	for _, rawURL := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			attempts = append(attempts, "request_build_failed")
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			attempts = append(attempts, "request_failed")
			continue
		}
		if resp.StatusCode != http.StatusOK {
			attempts = append(attempts, fmt.Sprintf("status=%d", resp.StatusCode))
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			continue
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(target)
		resp.Body.Close()
		if err != nil {
			attempts = append(attempts, "invalid_json")
			continue
		}
		return rawURL, "", true
	}
	return "", strings.Join(attempts, ","), false
}

func hasCompatibleTokenEndpointAuthMethod(methods []string) bool {
	if len(methods) == 0 {
		return false
	}
	for _, method := range methods {
		switch method {
		case "client_secret_post", "client_secret_basic", "none":
			return true
		}
	}
	return false
}

func supportsClientIDMetadata(metadata authorizationServerMetadata) bool {
	return metadata.ClientIDMetadataDocumentSupported && containsString(metadata.TokenEndpointAuthMethodsSupported, "none")
}

func parseHTTPSMetadataEndpoint(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, fmt.Errorf("endpoint must be absolute https without userinfo or fragment")
	}
	return parsed, nil
}

func checkAuthenticatedToolsList(ctx context.Context, client *http.Client, mcpURL, origin, tokenEnv, token string) postgresdeploy.Check {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	if err != nil {
		return failCheck("authenticated_tools_list", "request_build_failed", "authenticated tools/list request could not be built", "", "verify the MCP URL")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := client.Do(req)
	if err != nil {
		return failCheck("authenticated_tools_list", "request_failed", "authenticated tools/list request failed before response", "", "verify gateway reachability and token validity")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		kind, message, remediation := authenticatedToolsListFailure(resp.StatusCode)
		return postgresdeploy.Check{
			Name:        "authenticated_tools_list",
			Status:      postgresdeploy.CheckFail,
			ErrorKind:   kind,
			Message:     message,
			Remediation: remediation,
			Evidence: []postgresdeploy.Evidence{
				{Key: "status", Value: fmt.Sprintf("%d", resp.StatusCode)},
				{Key: "token_env", Value: tokenEnv},
			},
		}
	}
	var decoded map[string]any
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&decoded); err != nil {
		return failCheck("authenticated_tools_list", "invalid_json", "authenticated tools/list did not return JSON", "", "verify private gongmcp upstream response")
	}
	if _, ok := decoded["tools"]; !ok {
		if result, ok := decoded["result"].(map[string]any); ok {
			_, ok = result["tools"]
			if ok {
				return postgresdeploy.Check{
					Name:     "authenticated_tools_list",
					Status:   postgresdeploy.CheckPass,
					Message:  "authenticated tools/list reached upstream",
					Evidence: []postgresdeploy.Evidence{{Key: "token_env", Value: tokenEnv}},
				}
			}
		}
		return failCheck("authenticated_tools_list", "unexpected_mcp_shape", "authenticated tools/list returned 200 but did not include tools", "", "verify MCP upstream is gongmcp and returns JSON-RPC tools/list shape")
	}
	return postgresdeploy.Check{
		Name:     "authenticated_tools_list",
		Status:   postgresdeploy.CheckPass,
		Message:  "authenticated tools/list reached upstream",
		Evidence: []postgresdeploy.Evidence{{Key: "token_env", Value: tokenEnv}},
	}
}

func authenticatedToolsListFailure(status int) (kind, message, remediation string) {
	switch status {
	case http.StatusUnauthorized:
		return "auth_rejected", "authenticated tools/list was rejected before gateway authorization completed", "verify bearer token presence, issuer, signature, expiry, and gateway auth profile before group or email policy"
	case http.StatusForbidden:
		return "authorization_denied", "authenticated tools/list reached gateway auth but was denied by policy", "verify required scope, configured group claim, required group membership, and email allowlist"
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return "upstream_unreachable", "authenticated tools/list could not reach private gongmcp", "verify GATEWAY_UPSTREAM_URL, private gongmcp health, service routing, and network policy"
	default:
		return "unexpected_http_status", "authenticated tools/list did not return 200", "verify token issuer, client binding, scope, group/allowlist, and private gongmcp reachability"
	}
}

func tokenShapeDiagnostics(opts mcpGatewayDoctorOptions, rawToken string) []postgresdeploy.Check {
	claims, err := parseUnverifiedDoctorClaims(rawToken)
	if err != nil {
		// Intentionally do not include parser errors; malformed JWT errors can
		// expose user-provided token fragments in future library versions.
		return []postgresdeploy.Check{postgresdeploy.Check{
			Name:        "token_shape_parse",
			Status:      postgresdeploy.CheckWarn,
			ErrorKind:   "jwt_payload_unreadable",
			Message:     "token env var is set but JWT payload could not be parsed for local shape diagnostics",
			Remediation: "verify the env var contains a JWT access token; diagnostics are untrusted and do not replace gateway verification",
			Evidence: []postgresdeploy.Evidence{
				{Key: "doctor_profile", Value: opts.DoctorProfile},
				{Key: "token_env", Value: opts.TokenEnv},
			},
		}}
	}
	directOIDC := opts.DoctorProfile == "direct-oidc"
	checks := []postgresdeploy.Check{
		checkTokenShapeTokenUse(claims, directOIDC),
		checkTokenShapeScope(claims, opts.RequiredScope, directOIDC),
		checkTokenShapeClientBinding(claims, opts.ClientID, directOIDC),
		checkTokenShapeGroupClaim(claims, opts.GroupClaim, opts.RequiredGroup, directOIDC),
		checkTokenShapeEmailClaim(claims, directOIDC),
		checkTokenShapeExpiry(claims),
	}
	return checks
}

func parseUnverifiedDoctorClaims(rawToken string) (jwt.MapClaims, error) {
	parser := jwt.NewParser()
	token, _, err := parser.ParseUnverified(rawToken, jwt.MapClaims{})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("claims are not map claims")
	}
	return claims, nil
}

func checkTokenShapeTokenUse(claims jwt.MapClaims, directOIDC bool) postgresdeploy.Check {
	raw, ok := claims["token_use"]
	status := "missing"
	if ok {
		switch typed := raw.(type) {
		case string:
			switch strings.TrimSpace(typed) {
			case "":
				status = "missing"
			case "access":
				status = "access"
			default:
				status = "non_access"
			}
		default:
			status = "non_access"
		}
	}
	evidence := []postgresdeploy.Evidence{{Key: "token_use_status", Value: status}}
	switch {
	case directOIDC && status == "non_access":
		return postgresdeploy.Check{
			Name:        "token_shape_token_use",
			Status:      postgresdeploy.CheckFail,
			ErrorKind:   "token_use_not_access",
			Message:     "token_use is present but not access",
			Remediation: "use an access token for MCP bearer auth; diagnostics are untrusted and do not replace gateway verification",
			Evidence:    evidence,
		}
	case !directOIDC && status != "access":
		return postgresdeploy.Check{
			Name:        "token_shape_token_use",
			Status:      postgresdeploy.CheckFail,
			ErrorKind:   "token_use_missing_or_not_access",
			Message:     "Cognito profile expects token_use=access",
			Remediation: "exchange or select a Cognito access token, not an ID token",
			Evidence:    evidence,
		}
	case directOIDC && status == "missing":
		return postgresdeploy.Check{
			Name:     "token_shape_token_use",
			Status:   postgresdeploy.CheckPass,
			Message:  "token_use is absent, which is expected for some direct-OIDC providers; unverified shape only",
			Evidence: evidence,
		}
	default:
		return postgresdeploy.Check{
			Name:     "token_shape_token_use",
			Status:   postgresdeploy.CheckPass,
			Message:  "token_use shape matches the selected doctor profile; unverified shape only",
			Evidence: evidence,
		}
	}
}

func checkTokenShapeScope(claims jwt.MapClaims, requiredScope string, directOIDC bool) postgresdeploy.Check {
	inScope, inSCP := requiredScopePresence(claims, requiredScope)
	evidence := []postgresdeploy.Evidence{
		{Key: "scope_claim", Value: scopeClaimStatus(claims["scope"])},
		{Key: "scp_claim", Value: claimPresenceStatus(doctorClaimValue(claims, "scp", false))},
	}
	if requiredScope != "" {
		evidence = append(evidence,
			postgresdeploy.Evidence{Key: "required_scope_in_scope", Value: fmt.Sprintf("%t", inScope)},
			postgresdeploy.Evidence{Key: "required_scope_in_scp", Value: fmt.Sprintf("%t", inSCP)},
		)
	}
	switch {
	case requiredScope == "":
		return postgresdeploy.Check{
			Name:     "token_shape_scope",
			Status:   postgresdeploy.CheckPass,
			Message:  "required scope was not configured for token-shape diagnostics",
			Evidence: evidence,
		}
	case directOIDC && (inScope || inSCP):
		return postgresdeploy.Check{
			Name:     "token_shape_scope",
			Status:   postgresdeploy.CheckPass,
			Message:  "required scope appears in scope or scp; unverified shape only",
			Evidence: evidence,
		}
	case !directOIDC && inScope:
		return postgresdeploy.Check{
			Name:     "token_shape_scope",
			Status:   postgresdeploy.CheckPass,
			Message:  "required scope appears in scope; unverified shape only",
			Evidence: evidence,
		}
	case !directOIDC && !inScope && inSCP:
		return postgresdeploy.Check{
			Name:        "token_shape_scope",
			Status:      postgresdeploy.CheckFail,
			ErrorKind:   "scope_scp_not_sufficient_for_cognito",
			Message:     "Cognito profile requires scope; scp alone is not sufficient",
			Remediation: "ensure the Cognito access token includes the required scope in the scope claim",
			Evidence:    evidence,
		}
	default:
		return postgresdeploy.Check{
			Name:        "token_shape_scope",
			Status:      postgresdeploy.CheckFail,
			ErrorKind:   "required_scope_missing",
			Message:     "required scope is missing from the expected token-shape claims",
			Remediation: "verify provider scope grants and gateway OIDC_REQUIRED_SCOPE",
			Evidence:    evidence,
		}
	}
}

func checkTokenShapeClientBinding(claims jwt.MapClaims, expectedClientID string, directOIDC bool) postgresdeploy.Check {
	clientID := strings.TrimSpace(stringClaim(claims["client_id"]))
	audience := audienceClaimValues(claims["aud"])
	binding := "missing"
	switch {
	case clientID != "":
		binding = "client_id"
	case audienceMatchesExpected(audience, expectedClientID):
		binding = "aud"
	case len(audience) > 0:
		binding = "aud_present"
	}
	evidence := []postgresdeploy.Evidence{
		{Key: "client_id_claim", Value: claimPresenceStatus(clientID)},
		{Key: "aud_claim", Value: claimPresenceStatus(len(audience) > 0)},
		{Key: "aud_matches_client_id", Value: fmt.Sprintf("%t", expectedClientID != "" && audienceMatchesExpected(audience, expectedClientID))},
		{Key: "client_binding", Value: binding},
	}
	if expectedClientID == "" {
		return postgresdeploy.Check{
			Name:     "token_shape_client_binding",
			Status:   postgresdeploy.CheckPass,
			Message:  "client binding diagnostics skipped because --client-id was not set",
			Evidence: evidence,
		}
	}
	switch {
	case binding == "client_id" && clientID == expectedClientID:
		return postgresdeploy.Check{
			Name:     "token_shape_client_binding",
			Status:   postgresdeploy.CheckPass,
			Message:  "token payload includes expected client_id binding; unverified shape only",
			Evidence: evidence,
		}
	case binding == "client_id":
		return postgresdeploy.Check{
			Name:        "token_shape_client_binding",
			Status:      postgresdeploy.CheckFail,
			ErrorKind:   "client_id_mismatch",
			Message:     "token includes client_id but it does not match --client-id",
			Remediation: "verify the Claude connector client ID and gateway OIDC_CLIENT_ID",
			Evidence:    evidence,
		}
	case binding == "aud" || (directOIDC && binding == "aud_present"):
		if directOIDC && binding == "aud_present" && !audienceMatchesExpected(audience, expectedClientID) {
			return postgresdeploy.Check{
				Name:        "token_shape_client_binding",
				Status:      postgresdeploy.CheckWarn,
				ErrorKind:   "aud_client_binding_unverified",
				Message:     "aud is present but does not match --client-id; gateway may still accept other audience bindings",
				Remediation: "verify OIDC client ID and audience/resource binding against gateway policy",
				Evidence:    evidence,
			}
		}
		return postgresdeploy.Check{
			Name:     "token_shape_client_binding",
			Status:   postgresdeploy.CheckPass,
			Message:  "token payload appears to bind client identity through aud; unverified shape only",
			Evidence: evidence,
		}
	case !directOIDC:
		kind := "client_id_missing"
		message := "Cognito profile expects client_id in the access token"
		if len(audience) > 0 {
			kind = "client_id_missing_aud_not_sufficient"
			message = "Cognito profile expects client_id; aud alone is not sufficient"
		}
		return postgresdeploy.Check{
			Name:        "token_shape_client_binding",
			Status:      postgresdeploy.CheckFail,
			ErrorKind:   kind,
			Message:     message,
			Remediation: "verify the Cognito app client and access-token grant",
			Evidence:    evidence,
		}
	default:
		return postgresdeploy.Check{
			Name:        "token_shape_client_binding",
			Status:      postgresdeploy.CheckFail,
			ErrorKind:   "client_binding_missing",
			Message:     "token is missing client_id and aud client binding",
			Remediation: "verify provider access-token format and gateway OIDC_CLIENT_ID",
			Evidence:    evidence,
		}
	}
}

func checkTokenShapeGroupClaim(claims jwt.MapClaims, groupClaim, requiredGroup string, directOIDC bool) postgresdeploy.Check {
	if groupClaim == "" {
		return postgresdeploy.Check{
			Name:    "token_shape_group_claim",
			Status:  postgresdeploy.CheckPass,
			Message: "group claim diagnostics skipped because --group-claim was not set",
		}
	}
	topLevel := doctorClaimValue(claims, groupClaim, false)
	nested := doctorClaimValue(claims, groupClaim, true)
	location := "absent"
	switch {
	case topLevel != nil:
		location = "top_level"
	case nested != nil && nested != topLevel:
		location = "nested_ext"
	}
	populated := len(doctorClaimStringList(topLevel)) > 0 || len(doctorClaimStringList(nested)) > 0
	requiredPresent := true
	if requiredGroup != "" {
		requiredPresent = claimListContains(doctorClaimStringList(topLevel), requiredGroup) ||
			claimListContains(doctorClaimStringList(nested), requiredGroup)
	}
	evidence := []postgresdeploy.Evidence{
		{Key: "group_claim", Value: groupClaim},
		{Key: "group_claim_location", Value: location},
		{Key: "group_claim_populated", Value: fmt.Sprintf("%t", populated)},
	}
	if requiredGroup != "" {
		evidence = append(evidence, postgresdeploy.Evidence{Key: "required_group_present", Value: fmt.Sprintf("%t", requiredPresent)})
	}
	switch {
	case !populated:
		status := postgresdeploy.CheckFail
		if directOIDC && location == "nested_ext" {
			status = postgresdeploy.CheckWarn
		}
		return postgresdeploy.Check{
			Name:        "token_shape_group_claim",
			Status:      status,
			ErrorKind:   "group_claim_empty",
			Message:     "configured group claim is absent or empty in the token payload",
			Remediation: "verify IdP group mapping into the configured access-token claim",
			Evidence:    evidence,
		}
	case requiredGroup != "" && !requiredPresent:
		return postgresdeploy.Check{
			Name:        "token_shape_group_claim",
			Status:      postgresdeploy.CheckFail,
			ErrorKind:   "required_group_missing",
			Message:     "configured required group is not present in the token group claim",
			Remediation: "verify dedicated MCP group membership and exact group-name case in the IdP",
			Evidence:    evidence,
		}
	default:
		return postgresdeploy.Check{
			Name:     "token_shape_group_claim",
			Status:   postgresdeploy.CheckPass,
			Message:  "configured group claim is populated; unverified shape only",
			Evidence: evidence,
		}
	}
}

func checkTokenShapeEmailClaim(claims jwt.MapClaims, directOIDC bool) postgresdeploy.Check {
	topLevel := strings.TrimSpace(stringClaim(claims["email"]))
	location := "absent"
	switch {
	case topLevel != "":
		location = "top_level"
	case doctorClaimValue(claims, "email", true) != nil:
		location = "nested_ext"
	}
	evidence := []postgresdeploy.Evidence{{Key: "email_claim_location", Value: location}}
	if location == "absent" {
		if !directOIDC {
			return postgresdeploy.Check{
				Name:     "token_shape_email_claim",
				Status:   postgresdeploy.CheckPass,
				Message:  "email claim is absent, which is expected for the Cognito access-token profile",
				Evidence: evidence,
			}
		}
		return postgresdeploy.Check{
			Name:     "token_shape_email_claim",
			Status:   postgresdeploy.CheckWarn,
			Message:  "email claim is absent from top-level and nested ext token-shape locations",
			Evidence: evidence,
		}
	}
	return postgresdeploy.Check{
		Name:     "token_shape_email_claim",
		Status:   postgresdeploy.CheckPass,
		Message:  "email claim is present in an expected token-shape location; unverified shape only",
		Evidence: evidence,
	}
}

func checkTokenShapeExpiry(claims jwt.MapClaims) postgresdeploy.Check {
	raw, ok := claims["exp"]
	if !ok {
		return postgresdeploy.Check{
			Name:        "token_shape_expiry",
			Status:      postgresdeploy.CheckWarn,
			ErrorKind:   "exp_missing",
			Message:     "exp claim is missing from token-shape diagnostics",
			Remediation: "verify provider access-token lifetime settings",
			Evidence:    []postgresdeploy.Evidence{{Key: "exp_status", Value: "missing"}},
		}
	}
	exp, err := numericDateClaim(raw)
	if err != nil {
		return postgresdeploy.Check{
			Name:      "token_shape_expiry",
			Status:    postgresdeploy.CheckWarn,
			ErrorKind: "exp_unreadable",
			Message:   "exp claim is present but could not be interpreted for diagnostics",
			Evidence:  []postgresdeploy.Evidence{{Key: "exp_status", Value: "unreadable"}},
		}
	}
	now := time.Now()
	status := "valid"
	check := postgresdeploy.Check{
		Name:     "token_shape_expiry",
		Status:   postgresdeploy.CheckPass,
		Message:  "exp claim indicates the token is not expired for local diagnostics",
		Evidence: []postgresdeploy.Evidence{{Key: "exp_status", Value: status}},
	}
	if now.After(exp.Time) {
		check.Status = postgresdeploy.CheckFail
		check.ErrorKind = "token_expired"
		check.Message = "exp claim indicates the token is already expired"
		check.Remediation = "refresh or reissue the access token before retrying authenticated /mcp"
		check.Evidence = []postgresdeploy.Evidence{{Key: "exp_status", Value: "expired"}}
	}
	return check
}

func requiredScopePresence(claims jwt.MapClaims, requiredScope string) (inScope, inSCP bool) {
	if requiredScope == "" {
		return true, true
	}
	scopeValue, _ := claims["scope"].(string)
	inScope = containsString(strings.Fields(scopeValue), requiredScope)
	inSCP = containsString(doctorClaimStringList(doctorClaimValue(claims, "scp", false)), requiredScope)
	return inScope, inSCP
}

func scopeClaimStatus(raw any) string {
	if strings.TrimSpace(stringClaim(raw)) == "" {
		return "absent"
	}
	return "present"
}

func claimPresenceStatus(present any) string {
	switch typed := present.(type) {
	case bool:
		if typed {
			return "present"
		}
		return "absent"
	case string:
		if strings.TrimSpace(typed) == "" || typed == "false" {
			return "absent"
		}
		return "present"
	default:
		if present == nil {
			return "absent"
		}
		return "present"
	}
}

func stringClaim(raw any) string {
	value, ok := raw.(string)
	if !ok {
		return ""
	}
	return value
}

func audienceClaimValues(raw any) []string {
	switch typed := raw.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{typed}
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
				out = append(out, value)
			}
		}
		return out
	default:
		return nil
	}
}

func audienceMatchesExpected(audience []string, expectedClientID string) bool {
	if expectedClientID == "" {
		return len(audience) > 0
	}
	return containsString(audience, expectedClientID)
}

func doctorClaimValue(raw jwt.MapClaims, name string, allowNested bool) any {
	if raw == nil || name == "" {
		return nil
	}
	if value, ok := raw[name]; ok {
		return value
	}
	if !allowNested {
		return nil
	}
	if strings.Contains(name, ".") {
		parts := strings.Split(name, ".")
		var current any = raw
		for _, part := range parts {
			next, ok := current.(map[string]any)
			if !ok {
				return nil
			}
			current, ok = next[part]
			if !ok {
				return nil
			}
		}
		return current
	}
	ext, ok := raw["ext"].(map[string]any)
	if !ok {
		return nil
	}
	return ext[name]
}

func doctorClaimStringList(value any) []string {
	switch typed := value.(type) {
	case string:
		return strings.FieldsFunc(typed, func(r rune) bool {
			return r == ',' || r == ' ' || r == ';'
		})
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if strings.TrimSpace(item) != "" {
				out = append(out, strings.TrimSpace(item))
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	default:
		return nil
	}
}

func claimListContains(values []string, want string) bool {
	return containsString(values, want)
}

func numericDateClaim(raw any) (*jwt.NumericDate, error) {
	switch typed := raw.(type) {
	case float64:
		return jwt.NewNumericDate(time.Unix(int64(typed), 0)), nil
	case json.Number:
		unix, err := typed.Int64()
		if err != nil {
			return nil, err
		}
		return jwt.NewNumericDate(time.Unix(unix, 0)), nil
	default:
		return nil, fmt.Errorf("unsupported exp claim type")
	}
}

func firstAuthorizationServer(values ...protectedResourceMetadata) string {
	for _, value := range values {
		if len(value.AuthorizationServers) > 0 {
			return strings.TrimRight(value.AuthorizationServers[0], "/")
		}
	}
	return ""
}

func validBearerChallenge(challenge, expectedResourceMetadata, requiredScope string) bool {
	trimmed := strings.TrimSpace(challenge)
	if len(trimmed) <= len("Bearer ") || !strings.EqualFold(trimmed[:len("Bearer")], "Bearer") || trimmed[len("Bearer")] != ' ' {
		return false
	}
	params := parseAuthParams(strings.TrimSpace(trimmed[len("Bearer"):]))
	if params["resource_metadata"] != expectedResourceMetadata {
		return false
	}
	return containsString(strings.Fields(params["scope"]), requiredScope)
}

func parseAuthParams(raw string) map[string]string {
	params := map[string]string{}
	for _, part := range splitAuthParamParts(raw) {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}
		params[key] = value
	}
	return params
}

func splitAuthParamParts(raw string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false
	escaped := false
	for _, r := range raw {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\' && inQuote:
			current.WriteRune(r)
			escaped = true
		case r == '"':
			current.WriteRune(r)
			inQuote = !inQuote
		case r == ',' && !inQuote:
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	parts = append(parts, current.String())
	return parts
}

func requireHTTPSURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("not absolute https")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("userinfo or fragment not allowed")
	}
	return nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func passCheck(name, message string) postgresdeploy.Check {
	return postgresdeploy.Check{Name: name, Status: postgresdeploy.CheckPass, Message: message}
}

func failCheck(name, kind, message, evidence, remediation string) postgresdeploy.Check {
	check := postgresdeploy.Check{
		Name:        name,
		Status:      postgresdeploy.CheckFail,
		ErrorKind:   kind,
		Message:     message,
		Remediation: remediation,
	}
	if evidence != "" {
		check.Evidence = []postgresdeploy.Evidence{{Key: "detail", Value: evidence}}
	}
	return check
}
