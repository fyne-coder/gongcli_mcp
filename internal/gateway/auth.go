package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

type Principal struct {
	Subject string
	Email   string
	Groups  []string
	Scopes  []string
}

type Authorizer struct {
	cfg            Config
	keyFunc        jwt.Keyfunc
	clientVerifier ClientVerifier
}

const maxAudienceClientIDChecks = 8

type ClientVerifier interface {
	VerifyClientID(ctx context.Context, clientID string) error
}

type staticClientVerifier struct {
	clientID string
}

func (v staticClientVerifier) VerifyClientID(_ context.Context, clientID string) error {
	if clientID != v.clientID {
		return errors.New("client_id mismatch")
	}
	return nil
}

type cognitoClaims struct {
	jwt.RegisteredClaims
	ClientID string   `json:"client_id"`
	TokenUse string   `json:"token_use"`
	Scope    string   `json:"scope"`
	Email    string   `json:"email"`
	Username string   `json:"username"`
	Groups   []string `json:"cognito:groups"`
	raw      map[string]any
}

func (c *cognitoClaims) UnmarshalJSON(data []byte) error {
	type alias cognitoClaims
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*c = cognitoClaims(decoded)
	c.raw = raw
	return nil
}

func NewRemoteAuthorizer(ctx context.Context, cfg Config) (*Authorizer, error) {
	keys, err := keyfunc.NewDefaultCtx(ctx, []string{cfg.JWKSURL})
	if err != nil {
		return nil, err
	}
	return NewAuthorizer(cfg, keys.Keyfunc), nil
}

func NewAuthorizer(cfg Config, keyFunc jwt.Keyfunc) *Authorizer {
	return NewAuthorizerWithClientVerifier(cfg, keyFunc, staticClientVerifier{clientID: cfg.ClientID})
}

func NewAuthorizerWithClientVerifier(cfg Config, keyFunc jwt.Keyfunc, clientVerifier ClientVerifier) *Authorizer {
	if clientVerifier == nil {
		clientVerifier = staticClientVerifier{clientID: cfg.ClientID}
	}
	return &Authorizer{cfg: cfg, keyFunc: keyFunc, clientVerifier: clientVerifier}
}

func (a *Authorizer) KeyFunc() jwt.Keyfunc {
	return a.keyFunc
}

func (a *Authorizer) Authenticate(ctx context.Context, authorization string) (Principal, error) {
	const prefix = "bearer "
	auth := strings.TrimSpace(authorization)
	if !strings.HasPrefix(strings.ToLower(auth), prefix) {
		return Principal{}, errors.New("missing bearer token")
	}
	rawToken := strings.TrimSpace(auth[len(prefix):])
	if rawToken == "" {
		return Principal{}, errors.New("empty bearer token")
	}
	if a.cfg.MaxBearerBytes > 0 && len(rawToken) > a.cfg.MaxBearerBytes {
		return Principal{}, errors.New("bearer token is too large")
	}
	return a.VerifyAccessToken(ctx, rawToken)
}

func (a *Authorizer) VerifyAccessToken(ctx context.Context, rawToken string) (Principal, error) {
	claims := &cognitoClaims{}
	parser := jwt.NewParser(
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(a.cfg.AuthLeeway),
		jwt.WithValidMethods([]string{"RS256"}),
	)
	token, err := parser.ParseWithClaims(rawToken, claims, a.keyFunc)
	if err != nil {
		return Principal{}, fmt.Errorf("invalid access token: %w", err)
	}
	if !token.Valid {
		return Principal{}, errors.New("invalid access token")
	}
	if !issuerMatches(claims.Issuer, a.cfg.Issuer) {
		return Principal{}, errors.New("issuer mismatch")
	}
	if ctx.Err() != nil {
		return Principal{}, ctx.Err()
	}
	scopes := scopesFromClaims(claims, a.cfg.AuthProfile == AuthProfileDirectOIDC)
	if err := a.validateTokenProfile(ctx, claims); err != nil {
		return Principal{}, err
	}
	if !hasScope(scopes, a.cfg.RequiredScope) {
		return Principal{}, fmt.Errorf("required scope %q missing", a.cfg.RequiredScope)
	}
	groups := groupsFromClaims(claims, a.cfg.GroupClaim, a.cfg.AuthProfile == AuthProfileDirectOIDC)
	email := emailFromClaims(claims, a.cfg.AuthProfile == AuthProfileDirectOIDC)
	if err := a.authorizePrincipal(claims.Subject, email, groups); err != nil {
		return Principal{}, err
	}
	return Principal{
		Subject: claims.Subject,
		Email:   email,
		Groups:  groups,
		Scopes:  scopes,
	}, nil
}

func (a *Authorizer) validateTokenProfile(ctx context.Context, claims *cognitoClaims) error {
	switch a.cfg.AuthProfile {
	case "", AuthProfileCognito:
		return a.validateCognitoClaims(ctx, claims)
	case AuthProfileDirectOIDC:
		return a.validateDirectOIDCClaims(ctx, claims)
	default:
		return fmt.Errorf("unsupported auth profile %q", a.cfg.AuthProfile)
	}
}

func (a *Authorizer) validateCognitoClaims(ctx context.Context, claims *cognitoClaims) error {
	if claims.TokenUse != "access" {
		return errors.New("token_use is not access")
	}
	if err := a.clientVerifier.VerifyClientID(ctx, claims.ClientID); err != nil {
		return err
	}
	// Cognito access tokens may omit aud; when present, bind it to the MCP resource.
	if len(claims.Audience) > 0 && !audienceContains(claims.Audience, a.cfg.ResourceURL()) {
		return errors.New("audience/resource mismatch")
	}
	return nil
}

func (a *Authorizer) validateDirectOIDCClaims(ctx context.Context, claims *cognitoClaims) error {
	if claims.TokenUse != "" && claims.TokenUse != "access" {
		return errors.New("token_use is not access")
	}
	if claims.ClientID != "" {
		if err := a.clientVerifier.VerifyClientID(ctx, claims.ClientID); err != nil {
			return err
		}
	} else if err := a.verifyAudienceClientID(ctx, claims.Audience); err != nil {
		return err
	}
	if len(claims.Audience) > 0 &&
		!audienceContains(claims.Audience, a.cfg.ResourceURL()) &&
		!audienceClientIDAllowed(ctx, a.clientVerifier, claims.Audience) {
		return errors.New("audience/resource mismatch")
	}
	return nil
}

func (a *Authorizer) verifyAudienceClientID(ctx context.Context, audience jwt.ClaimStrings) error {
	for i, value := range audience {
		if i >= maxAudienceClientIDChecks {
			break
		}
		if err := a.clientVerifier.VerifyClientID(ctx, value); err == nil {
			return nil
		}
	}
	return errors.New("client_id missing and audience client binding not allowed")
}

func audienceClientIDAllowed(ctx context.Context, verifier ClientVerifier, audience jwt.ClaimStrings) bool {
	for i, value := range audience {
		if i >= maxAudienceClientIDChecks {
			break
		}
		if verifier.VerifyClientID(ctx, value) == nil {
			return true
		}
	}
	return false
}

func (a *Authorizer) authorizePrincipal(subject, email string, groups []string) error {
	if len(a.cfg.AllowedSubjects) > 0 {
		if _, ok := a.cfg.AllowedSubjects[strings.ToLower(subject)]; !ok {
			return errors.New("subject is not allowed")
		}
	}
	if len(a.cfg.AllowedEmails) > 0 {
		if _, ok := a.cfg.AllowedEmails[strings.ToLower(email)]; !ok {
			return errors.New("email is not allowed")
		}
	}
	requiredGroups := a.cfg.configuredRequiredGroups()
	if len(requiredGroups) > 0 {
		if hasAnyRequiredGroup(groups, requiredGroups) {
			return nil
		}
		if len(requiredGroups) == 1 {
			return fmt.Errorf("required group %q missing", requiredGroups[0])
		}
		return errors.New("required group membership missing")
	}
	return nil
}

func hasAnyRequiredGroup(groups, requiredGroups []string) bool {
	for _, group := range groups {
		for _, required := range requiredGroups {
			if group == required {
				return true
			}
		}
	}
	return false
}

func audienceContains(audience jwt.ClaimStrings, want string) bool {
	for _, value := range audience {
		if value == want {
			return true
		}
	}
	return false
}

func issuerMatches(got, want string) bool {
	got = strings.TrimRight(strings.TrimSpace(got), "/")
	want = strings.TrimRight(strings.TrimSpace(want), "/")
	return got != "" && got == want
}

func hasScope(scopeValues []string, want string) bool {
	for _, scope := range scopeValues {
		if scope == want {
			return true
		}
	}
	return false
}

func scopesFromClaims(claims *cognitoClaims, includeSCP bool) []string {
	scopes := splitList(claims.Scope)
	if includeSCP {
		scopes = append(scopes, claimStringList(claimValue(claims.raw, "scp", false))...)
	}
	return scopes
}

func groupsFromClaims(claims *cognitoClaims, groupClaim string, allowNested bool) []string {
	if groupClaim == "" || groupClaim == "cognito:groups" {
		return claims.Groups
	}
	return claimStringList(claimValue(claims.raw, groupClaim, allowNested))
}

func emailFromClaims(claims *cognitoClaims, allowNested bool) string {
	if claims.Email != "" {
		return claims.Email
	}
	if !allowNested {
		return ""
	}
	values := claimStringList(claimValue(claims.raw, "email", true))
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func claimValue(raw map[string]any, name string, allowNested bool) any {
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
		// Operator-supplied dotted names intentionally support nested provider
		// claims like ext.memberOf while preserving literal dotted keys above.
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

func claimStringList(value any) []string {
	switch typed := value.(type) {
	case string:
		return splitList(typed)
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
