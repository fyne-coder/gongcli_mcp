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
		jwt.WithIssuer(a.cfg.Issuer),
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
	if ctx.Err() != nil {
		return Principal{}, ctx.Err()
	}
	if claims.TokenUse != "access" {
		return Principal{}, errors.New("token_use is not access")
	}
	if err := a.clientVerifier.VerifyClientID(ctx, claims.ClientID); err != nil {
		return Principal{}, err
	}
	if !hasScope(claims.Scope, a.cfg.RequiredScope) {
		return Principal{}, fmt.Errorf("required scope %q missing", a.cfg.RequiredScope)
	}
	// Cognito access tokens may omit aud; when present, bind it to the MCP resource.
	if len(claims.Audience) > 0 && !audienceContains(claims.Audience, a.cfg.ResourceURL()) {
		return Principal{}, errors.New("audience/resource mismatch")
	}
	groups := groupsFromClaims(claims, a.cfg.GroupClaim)
	if err := a.authorizePrincipal(claims.Subject, claims.Email, groups); err != nil {
		return Principal{}, err
	}
	return Principal{
		Subject: claims.Subject,
		Email:   claims.Email,
		Groups:  groups,
		Scopes:  strings.Fields(claims.Scope),
	}, nil
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
	if a.cfg.RequiredGroup != "" {
		for _, group := range groups {
			if group == a.cfg.RequiredGroup {
				return nil
			}
		}
		return fmt.Errorf("required group %q missing", a.cfg.RequiredGroup)
	}
	return nil
}

func audienceContains(audience jwt.ClaimStrings, want string) bool {
	for _, value := range audience {
		if value == want {
			return true
		}
	}
	return false
}

func hasScope(scopeValue, want string) bool {
	for _, scope := range strings.Fields(scopeValue) {
		if scope == want {
			return true
		}
	}
	return false
}

func groupsFromClaims(claims *cognitoClaims, groupClaim string) []string {
	if groupClaim == "" || groupClaim == "cognito:groups" {
		return claims.Groups
	}
	value, ok := claims.raw[groupClaim]
	if !ok {
		return nil
	}
	return claimStringList(value)
}

func claimStringList(value any) []string {
	switch typed := value.(type) {
	case string:
		return splitList(typed)
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
