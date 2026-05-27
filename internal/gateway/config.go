package gateway

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

type Config struct {
	Addr            string
	Upstream        *url.URL
	InternalToken   string
	PublicBaseURL   string
	Issuer          string
	JWKSURL         string
	ClientID        string
	RequiredScope   string
	ScopesSupported []string
	RequiredGroup   string
	GroupClaim      string
	AllowedSubjects map[string]struct{}
	AllowedEmails   map[string]struct{}
	AllowedOrigins  []string
	AuthLeeway      time.Duration
	MaxRequestBytes int64
	MaxBearerBytes  int
	UpstreamTimeout time.Duration

	DCREnabled             bool
	CognitoDomainURL       string
	CognitoUserPoolID      string
	DCRAllowedRedirectURIs []string
	DCRAllowedScopes       []string
	DCRIdentityProviders   []string
	DCRClientNamePrefix    string
	DCRAccessTokenMinutes  int32
	DCRClientCacheTTL      time.Duration
}

func LoadConfig() (Config, error) {
	upstream, err := url.Parse(envDefault("GATEWAY_UPSTREAM_URL", "http://gongmcp:8080"))
	if err != nil {
		return Config{}, fmt.Errorf("GATEWAY_UPSTREAM_URL: %w", err)
	}
	if upstream.Scheme == "" || upstream.Host == "" {
		return Config{}, errors.New("GATEWAY_UPSTREAM_URL must be absolute")
	}

	token, err := loadInternalToken()
	if err != nil {
		return Config{}, err
	}

	issuer := strings.TrimRight(strings.TrimSpace(envFirst("COGNITO_ISSUER_URL", "OIDC_ISSUER_URL")), "/")
	if issuer == "" {
		return Config{}, errors.New("COGNITO_ISSUER_URL or OIDC_ISSUER_URL is required")
	}
	if err := validateHTTPSURL("COGNITO_ISSUER_URL", issuer); err != nil {
		return Config{}, err
	}

	publicBaseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/")
	if publicBaseURL == "" {
		return Config{}, errors.New("PUBLIC_BASE_URL is required")
	}
	if err := validateHTTPSURL("PUBLIC_BASE_URL", publicBaseURL); err != nil {
		return Config{}, err
	}

	clientID := strings.TrimSpace(envFirst("COGNITO_CLIENT_ID", "OIDC_CLIENT_ID"))
	if clientID == "" {
		return Config{}, errors.New("COGNITO_CLIENT_ID or OIDC_CLIENT_ID is required")
	}

	requiredScope := strings.TrimSpace(envDefault("COGNITO_REQUIRED_SCOPE", "gongmcp/read"))
	scopesSupported := splitList(envDefault("COGNITO_SCOPES_SUPPORTED", requiredScope))
	if !contains(scopesSupported, requiredScope) {
		scopesSupported = append([]string{requiredScope}, scopesSupported...)
	}

	jwksURL := strings.TrimSpace(envDefault("COGNITO_JWKS_URL", issuer+"/.well-known/jwks.json"))
	if err := validateHTTPSURL("COGNITO_JWKS_URL", jwksURL); err != nil {
		return Config{}, err
	}

	requiredGroup := strings.TrimSpace(os.Getenv("COGNITO_REQUIRED_GROUP"))
	allowedSubjects := csvSet(os.Getenv("COGNITO_ALLOWED_SUBJECTS"))
	allowedEmails := csvSet(os.Getenv("COGNITO_ALLOWED_EMAILS"))
	if requiredGroup == "" && len(allowedSubjects) == 0 && len(allowedEmails) == 0 {
		return Config{}, errors.New("at least one of COGNITO_REQUIRED_GROUP, COGNITO_ALLOWED_SUBJECTS, or COGNITO_ALLOWED_EMAILS is required")
	}

	dcrEnabled := envBool("GATEWAY_DCR_ENABLED")
	cognitoDomainURL := strings.TrimRight(strings.TrimSpace(os.Getenv("COGNITO_DOMAIN_URL")), "/")
	cognitoUserPoolID := strings.TrimSpace(os.Getenv("COGNITO_USER_POOL_ID"))
	dcrAllowedRedirectURIs := splitList(os.Getenv("COGNITO_DCR_ALLOWED_REDIRECT_URIS"))
	dcrAllowedScopes := splitList(os.Getenv("COGNITO_DCR_ALLOWED_SCOPES"))
	dcrIdentityProviders := splitList(os.Getenv("COGNITO_DCR_IDENTITY_PROVIDERS"))
	dcrClientNamePrefix := strings.TrimSpace(envDefault("COGNITO_DCR_CLIENT_NAME_PREFIX", "gongmcp-dcr"))
	dcrAccessTokenMinutes := int32(envIntDefault("COGNITO_DCR_ACCESS_TOKEN_MINUTES", 60))
	if dcrEnabled {
		if cognitoDomainURL == "" {
			return Config{}, errors.New("COGNITO_DOMAIN_URL is required when GATEWAY_DCR_ENABLED is true")
		}
		if err := validateHTTPSURL("COGNITO_DOMAIN_URL", cognitoDomainURL); err != nil {
			return Config{}, err
		}
		if cognitoUserPoolID == "" {
			return Config{}, errors.New("COGNITO_USER_POOL_ID is required when GATEWAY_DCR_ENABLED is true")
		}
		if len(dcrAllowedRedirectURIs) == 0 {
			return Config{}, errors.New("COGNITO_DCR_ALLOWED_REDIRECT_URIS is required when GATEWAY_DCR_ENABLED is true")
		}
		for _, redirectURI := range dcrAllowedRedirectURIs {
			if err := validateHTTPSURL("COGNITO_DCR_ALLOWED_REDIRECT_URIS", redirectURI); err != nil {
				return Config{}, err
			}
		}
		if len(dcrAllowedScopes) == 0 {
			dcrAllowedScopes = []string{"openid", "email", requiredScope}
		}
		if !contains(dcrAllowedScopes, requiredScope) {
			return Config{}, fmt.Errorf("COGNITO_DCR_ALLOWED_SCOPES must include required scope %q", requiredScope)
		}
		if len(dcrIdentityProviders) == 0 {
			return Config{}, errors.New("COGNITO_DCR_IDENTITY_PROVIDERS is required when GATEWAY_DCR_ENABLED is true")
		}
		if dcrClientNamePrefix == "" {
			return Config{}, errors.New("COGNITO_DCR_CLIENT_NAME_PREFIX must not be empty")
		}
		if dcrAccessTokenMinutes < 5 || dcrAccessTokenMinutes > 1440 {
			return Config{}, errors.New("COGNITO_DCR_ACCESS_TOKEN_MINUTES must be between 5 and 1440")
		}
	}

	return Config{
		Addr:            envDefault("GATEWAY_ADDR", ":8090"),
		Upstream:        upstream,
		InternalToken:   token,
		PublicBaseURL:   publicBaseURL,
		Issuer:          issuer,
		JWKSURL:         jwksURL,
		ClientID:        clientID,
		RequiredScope:   requiredScope,
		ScopesSupported: scopesSupported,
		RequiredGroup:   requiredGroup,
		GroupClaim:      envDefault("COGNITO_GROUP_CLAIM", "cognito:groups"),
		AllowedSubjects: allowedSubjects,
		AllowedEmails:   allowedEmails,
		AllowedOrigins:  splitList(os.Getenv("GATEWAY_ALLOWED_ORIGINS")),
		AuthLeeway:      60 * time.Second,
		MaxRequestBytes: 8 << 20,
		MaxBearerBytes:  8 << 10,
		UpstreamTimeout: 90 * time.Second,

		DCREnabled:             dcrEnabled,
		CognitoDomainURL:       cognitoDomainURL,
		CognitoUserPoolID:      cognitoUserPoolID,
		DCRAllowedRedirectURIs: dcrAllowedRedirectURIs,
		DCRAllowedScopes:       dcrAllowedScopes,
		DCRIdentityProviders:   dcrIdentityProviders,
		DCRClientNamePrefix:    dcrClientNamePrefix,
		DCRAccessTokenMinutes:  dcrAccessTokenMinutes,
		DCRClientCacheTTL:      10 * time.Minute,
	}, nil
}

func (c Config) ResourceURL() string {
	return c.PublicBaseURL + "/mcp"
}

func (c Config) ResourceMetadataURL() string {
	return c.PublicBaseURL + "/.well-known/oauth-protected-resource/mcp"
}

func (c Config) AuthorizationServerURL() string {
	if c.DCREnabled {
		return c.PublicBaseURL
	}
	return c.Issuer
}

func (c Config) WWWAuthenticateChallenge() string {
	return fmt.Sprintf(`Bearer resource_metadata="%s", scope="%s"`, c.ResourceMetadataURL(), c.RequiredScope)
}

func loadInternalToken() (string, error) {
	if raw := strings.TrimSpace(os.Getenv("GATEWAY_INTERNAL_BEARER_TOKEN")); raw != "" {
		return raw, nil
	}
	tokenFile := strings.TrimSpace(envFirst("GATEWAY_INTERNAL_BEARER_TOKEN_FILE", "INTERNAL_BEARER_TOKEN_FILE"))
	if tokenFile == "" {
		return "", errors.New("GATEWAY_INTERNAL_BEARER_TOKEN or GATEWAY_INTERNAL_BEARER_TOKEN_FILE is required")
	}
	tokenRaw, err := os.ReadFile(tokenFile)
	if err != nil {
		return "", fmt.Errorf("read internal bearer token: %w", err)
	}
	token := strings.TrimSpace(string(tokenRaw))
	if token == "" {
		return "", errors.New("internal bearer token is empty")
	}
	return token, nil
}

func validateHTTPSURL(name, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s must be an absolute https URL: %w", name, err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("%s must be an absolute https URL", name)
	}
	return nil
}

func envFirst(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func envDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func envIntDefault(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed == 0 {
		return fallback
	}
	return parsed
}

func csvSet(value string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, field := range strings.Split(value, ",") {
		if trimmed := strings.ToLower(strings.TrimSpace(field)); trimmed != "" {
			out[trimmed] = struct{}{}
		}
	}
	return out
}

func splitList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == ' '
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
