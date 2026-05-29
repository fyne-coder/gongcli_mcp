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
)

var newDoctorHTTPClient = func(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

type mcpGatewayDoctorResponse struct {
	Target               string                 `json:"target"`
	GatewayBaseURL       string                 `json:"gateway_base_url"`
	MCPURL               string                 `json:"mcp_url"`
	ExpectedIssuer       string                 `json:"expected_issuer,omitempty"`
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
	requiredScope := fs.String("required-scope", "gongmcp/read", "required MCP OAuth scope expected in metadata")
	expectDCR := fs.Bool("expect-dcr", false, "require gateway-advertised DCR authorization-server metadata")
	tokenEnv := fs.String("token-env", "", "environment variable containing an optional access token for tools/list smoke")
	origin := fs.String("origin", "", "optional Origin header for CORS/preflight validation")
	timeout := fs.Duration("timeout", defaultHTTPTimeout, "HTTP timeout for gateway and metadata checks")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if fs.NArg() != 0 {
		return errUsage
	}

	client := newDoctorHTTPClient(*timeout)
	response := runMCPGatewayDoctor(ctx, client, mcpGatewayDoctorOptions{
		RawURL:         *rawURL,
		ExpectedIssuer: strings.TrimRight(strings.TrimSpace(*expectedIssuer), "/"),
		RequiredScope:  strings.TrimSpace(*requiredScope),
		ExpectDCR:      *expectDCR,
		TokenEnv:       strings.TrimSpace(*tokenEnv),
		Origin:         strings.TrimSpace(*origin),
	})
	return writeJSONValue(a.out, response)
}

type mcpGatewayDoctorOptions struct {
	RawURL         string
	ExpectedIssuer string
	RequiredScope  string
	ExpectDCR      bool
	TokenEnv       string
	Origin         string
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
		ExpectDCR:            opts.ExpectDCR,
		AuthenticatedCheck:   "skipped",
		SensitiveDataWarning: "This output contains gateway metadata, HTTP status checks, and sanitized evidence only. Do not paste bearer tokens or client secrets into support artifacts.",
	}
	baseURL, mcpURL, err := normalizeMCPGatewayURL(opts.RawURL)
	if err != nil {
		response.Checks = append(response.Checks, failCheck("gateway_url", "invalid_gateway_url", "gateway URL is invalid", err.Error(), "pass an absolute https gateway base URL or /mcp URL"))
		return response
	}
	response.GatewayBaseURL = baseURL
	response.MCPURL = mcpURL
	response.Target = mcpURL

	rootMetadataURL := baseURL + "/.well-known/oauth-protected-resource"
	endpointMetadataURL := baseURL + "/.well-known/oauth-protected-resource/mcp"
	var rootMetadata protectedResourceMetadata
	if fetchJSONCheck(ctx, client, rootMetadataURL, &rootMetadata, &response.Checks, "protected_resource_metadata") {
		response.Checks = append(response.Checks, validateProtectedResourceMetadata("protected_resource_metadata", rootMetadata, mcpURL, opts.ExpectedIssuer, baseURL, opts.RequiredScope, opts.ExpectDCR)...)
	}

	var endpointMetadata protectedResourceMetadata
	if fetchJSONCheck(ctx, client, endpointMetadataURL, &endpointMetadata, &response.Checks, "endpoint_protected_resource_metadata") {
		response.Checks = append(response.Checks, validateProtectedResourceMetadata("endpoint_protected_resource_metadata", endpointMetadata, mcpURL, opts.ExpectedIssuer, baseURL, opts.RequiredScope, opts.ExpectDCR)...)
	}

	if opts.Origin != "" {
		response.Checks = append(response.Checks, checkCORSPreflight(ctx, client, mcpURL, opts.Origin))
	}
	response.Checks = append(response.Checks, checkMCPChallenge(ctx, client, http.MethodGet, mcpURL, opts.Origin, opts.RequiredScope))
	response.Checks = append(response.Checks, checkMCPChallenge(ctx, client, http.MethodPost, mcpURL, opts.Origin, opts.RequiredScope))

	authServerURL := firstAuthorizationServer(rootMetadata, endpointMetadata)
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
			response.Checks = append(response.Checks, checkAuthenticatedToolsList(ctx, client, mcpURL, opts.Origin, opts.TokenEnv, token))
		}
	}

	return response
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
	if strings.HasSuffix(parsed.Path, "/mcp") {
		base = strings.TrimSuffix(trimmed, "/mcp")
	}
	return base, base + "/mcp", nil
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

func validateProtectedResourceMetadata(name string, metadata protectedResourceMetadata, expectedResource, expectedIssuer, baseURL, requiredScope string, expectDCR bool) []postgresdeploy.Check {
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
	wantAuthServer := expectedIssuer
	if expectDCR {
		wantAuthServer = baseURL
	}
	if authServer == "" {
		checks = append(checks, failCheck(name+"_authorization_server", "authorization_server_missing", "metadata is missing authorization_servers", "", "publish the OIDC issuer URL or gateway auth-server URL for DCR mode"))
	} else if err := requireHTTPSURL(authServer); err != nil {
		checks = append(checks, failCheck(name+"_authorization_server", "authorization_server_not_https", "authorization server must be an absolute https URL", "", "fix authorization_servers[0]"))
	} else if wantAuthServer != "" && authServer != strings.TrimRight(wantAuthServer, "/") {
		checks = append(checks, failCheck(name+"_authorization_server", "authorization_server_mismatch", "authorization server does not match expected value", fmt.Sprintf("authorization_server=%s", authServer), "verify --issuer, DCR mode, and gateway metadata configuration"))
	} else if !expectDCR && expectedIssuer == "" && authServer == baseURL {
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

func checkMCPChallenge(ctx context.Context, client *http.Client, method, mcpURL, origin, requiredScope string) postgresdeploy.Check {
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
	baseURL := strings.TrimSuffix(mcpURL, "/mcp")
	expectedMetadataURLs := []string{
		baseURL + "/.well-known/oauth-protected-resource",
		baseURL + "/.well-known/oauth-protected-resource/mcp",
	}
	if !validBearerChallenge(challenge, expectedMetadataURLs, requiredScope) {
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
		return failCheck("oidc_discovery", "issuer_not_https", "OIDC issuer must be an absolute https URL", "", "pass the public OIDC issuer URL, not a private or http URL")
	}
	discoveryURL := issuer + "/.well-known/openid-configuration"
	var metadata authorizationServerMetadata
	var checks []postgresdeploy.Check
	if !fetchJSONCheck(ctx, client, discoveryURL, &metadata, &checks, "oidc_discovery") {
		return checks[len(checks)-1]
	}
	if metadata.JWKSURI == "" {
		return failCheck("oidc_discovery", "jwks_uri_missing", "OIDC discovery metadata is missing jwks_uri", "", "verify the OIDC issuer URL")
	}
	if err := requireHTTPSURL(metadata.JWKSURI); err != nil {
		return failCheck("oidc_discovery", "jwks_uri_not_https", "OIDC jwks_uri must be an absolute https URL", "", "verify the OIDC issuer URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadata.JWKSURI, nil)
	if err != nil {
		return failCheck("oidc_jwks", "request_build_failed", "JWKS request could not be built", "", "verify jwks_uri")
	}
	resp, err := client.Do(req)
	if err != nil {
		return failCheck("oidc_jwks", "request_failed", "JWKS endpoint could not be reached", "", "verify OIDC JWKS reachability from the operator network")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return failCheck("oidc_jwks", "unexpected_http_status", "JWKS endpoint returned a non-200 status", fmt.Sprintf("status=%d", resp.StatusCode), "verify the OIDC issuer URL and network path")
	}
	var jwks struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&jwks); err != nil {
		return failCheck("oidc_jwks", "invalid_json", "JWKS endpoint did not return valid JSON", "", "verify OIDC JWKS endpoint")
	}
	if len(jwks.Keys) == 0 {
		return failCheck("oidc_jwks", "keys_missing", "JWKS endpoint returned no keys", "", "verify OIDC signing keys")
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
		return failCheck("dcr_authorization_server_metadata", "authorization_endpoint_invalid", "DCR metadata authorization_endpoint should point at the provider /oauth2/authorize endpoint", "", "verify provider authorization endpoint metadata")
	case tokenErr != nil || !strings.HasSuffix(tokenURL.Path, "/oauth2/token"):
		return failCheck("dcr_authorization_server_metadata", "token_endpoint_invalid", "DCR metadata token_endpoint should point at the provider /oauth2/token endpoint", "", "verify provider token endpoint metadata")
	case authURL.Host != tokenURL.Host:
		return failCheck("dcr_authorization_server_metadata", "provider_endpoint_host_mismatch", "DCR metadata authorization and token endpoints must share the same provider host", "", "verify provider endpoint metadata")
	case !containsString(metadata.CodeChallengeMethodsSupported, "S256"):
		return failCheck("dcr_authorization_server_metadata", "pkce_s256_missing", "DCR metadata must advertise PKCE S256", "", "verify gateway DCR metadata")
	case !containsString(metadata.TokenEndpointAuthMethodsSupported, "none"):
		return failCheck("dcr_authorization_server_metadata", "public_client_auth_missing", "DCR metadata must allow public-client token endpoint auth method none", "", "verify gateway DCR metadata")
	default:
		return passCheck("dcr_authorization_server_metadata", "DCR authorization-server metadata is gateway-shaped")
	}
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
		return postgresdeploy.Check{
			Name:        "authenticated_tools_list",
			Status:      postgresdeploy.CheckFail,
			ErrorKind:   "unexpected_http_status",
			Message:     "authenticated tools/list did not return 200",
			Remediation: "verify token issuer, client_id, scope, group/allowlist, and private gongmcp reachability",
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
		return failCheck("authenticated_tools_list", "tools_missing", "authenticated tools/list response did not include tools", "", "verify MCP upstream is gongmcp")
	}
	return postgresdeploy.Check{
		Name:     "authenticated_tools_list",
		Status:   postgresdeploy.CheckPass,
		Message:  "authenticated tools/list reached upstream",
		Evidence: []postgresdeploy.Evidence{{Key: "token_env", Value: tokenEnv}},
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

func validBearerChallenge(challenge string, expectedResourceMetadata []string, requiredScope string) bool {
	trimmed := strings.TrimSpace(challenge)
	if len(trimmed) <= len("Bearer ") || !strings.EqualFold(trimmed[:len("Bearer")], "Bearer") || trimmed[len("Bearer")] != ' ' {
		return false
	}
	params := parseAuthParams(strings.TrimSpace(trimmed[len("Bearer"):]))
	if !containsString(expectedResourceMetadata, params["resource_metadata"]) {
		return false
	}
	if strings.TrimSpace(requiredScope) == "" {
		return true
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
