package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
)

type DCRRegistrar interface {
	RegisterClient(ctx context.Context, req DCRClientRegistrationRequest) (DCRClientRegistrationResponse, error)
}

type DCRClientRegistrationRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	ClientName              string   `json:"client_name"`
	Scope                   string   `json:"scope"`
}

type DCRClientRegistrationResponse struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	ClientName              string   `json:"client_name,omitempty"`
	Scope                   string   `json:"scope"`
}

type CognitoClientStore struct {
	cfg    Config
	client cognitoUserPoolClientAPI
	now    func() time.Time

	mu       sync.Mutex
	verified map[string]time.Time
}

type cognitoUserPoolClientAPI interface {
	CreateUserPoolClient(ctx context.Context, params *cognitoidentityprovider.CreateUserPoolClientInput, optFns ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.CreateUserPoolClientOutput, error)
	DescribeUserPoolClient(ctx context.Context, params *cognitoidentityprovider.DescribeUserPoolClientInput, optFns ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.DescribeUserPoolClientOutput, error)
}

func NewCognitoClientStore(ctx context.Context, cfg Config) (*CognitoClientStore, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS config for Cognito DCR: %w", err)
	}
	return NewCognitoClientStoreWithClient(cfg, cognitoidentityprovider.NewFromConfig(awsCfg)), nil
}

func NewCognitoClientStoreWithClient(cfg Config, client cognitoUserPoolClientAPI) *CognitoClientStore {
	return &CognitoClientStore{
		cfg:      cfg,
		client:   client,
		now:      time.Now,
		verified: map[string]time.Time{},
	}
}

func (s *CognitoClientStore) RegisterClient(ctx context.Context, req DCRClientRegistrationRequest) (DCRClientRegistrationResponse, error) {
	name, err := dcrClientName(s.cfg.DCRClientNamePrefix, req.ClientName)
	if err != nil {
		return DCRClientRegistrationResponse{}, err
	}
	scopes := strings.Fields(req.Scope)
	input := &cognitoidentityprovider.CreateUserPoolClientInput{
		UserPoolId:                      aws.String(s.cfg.CognitoUserPoolID),
		ClientName:                      aws.String(name),
		GenerateSecret:                  false,
		CallbackURLs:                    req.RedirectURIs,
		AllowedOAuthFlowsUserPoolClient: true,
		AllowedOAuthFlows:               []types.OAuthFlowType{types.OAuthFlowTypeCode},
		AllowedOAuthScopes:              scopes,
		SupportedIdentityProviders:      s.cfg.DCRIdentityProviders,
		PreventUserExistenceErrors:      types.PreventUserExistenceErrorTypesEnabled,
		EnableTokenRevocation:           aws.Bool(true),
		AccessTokenValidity:             aws.Int32(s.cfg.DCRAccessTokenMinutes),
		TokenValidityUnits: &types.TokenValidityUnitsType{
			AccessToken: types.TimeUnitsTypeMinutes,
		},
	}
	out, err := s.client.CreateUserPoolClient(ctx, input)
	if err != nil {
		return DCRClientRegistrationResponse{}, fmt.Errorf("create Cognito app client: %w", err)
	}
	if out.UserPoolClient == nil || aws.ToString(out.UserPoolClient.ClientId) == "" {
		return DCRClientRegistrationResponse{}, errors.New("cognito did not return a client_id")
	}
	clientID := aws.ToString(out.UserPoolClient.ClientId)
	s.rememberVerified(clientID)
	return DCRClientRegistrationResponse{
		ClientID:                clientID,
		ClientIDIssuedAt:        s.now().Unix(),
		RedirectURIs:            append([]string(nil), req.RedirectURIs...),
		TokenEndpointAuthMethod: "none",
		GrantTypes:              []string{"authorization_code"},
		ResponseTypes:           []string{"code"},
		ClientName:              name,
		Scope:                   strings.Join(scopes, " "),
	}, nil
}

func (s *CognitoClientStore) VerifyClientID(ctx context.Context, clientID string) error {
	if clientID == s.cfg.ClientID {
		return nil
	}
	if !s.cfg.DCREnabled {
		return errors.New("client_id mismatch")
	}
	if s.isRemembered(clientID) {
		return nil
	}
	out, err := s.client.DescribeUserPoolClient(ctx, &cognitoidentityprovider.DescribeUserPoolClientInput{
		UserPoolId: aws.String(s.cfg.CognitoUserPoolID),
		ClientId:   aws.String(clientID),
	})
	if err != nil {
		return fmt.Errorf("client_id is not an allowed DCR client: %w", err)
	}
	if out.UserPoolClient == nil {
		return errors.New("client_id is not an allowed DCR client")
	}
	client := out.UserPoolClient
	name := aws.ToString(client.ClientName)
	if !strings.HasPrefix(name, s.cfg.DCRClientNamePrefix+"-") {
		return errors.New("client_id is not a gateway-created DCR client")
	}
	if client.AllowedOAuthFlowsUserPoolClient == nil || !aws.ToBool(client.AllowedOAuthFlowsUserPoolClient) {
		return errors.New("DCR client has OAuth flows disabled")
	}
	if !oauthFlowsContain(client.AllowedOAuthFlows, types.OAuthFlowTypeCode) {
		return errors.New("DCR client does not allow authorization code flow")
	}
	if !contains(client.AllowedOAuthScopes, s.cfg.RequiredScope) {
		return fmt.Errorf("DCR client is missing required scope %q", s.cfg.RequiredScope)
	}
	if len(intersectStrings(client.CallbackURLs, s.cfg.DCRAllowedRedirectURIs)) == 0 {
		return errors.New("DCR client has no allowed callback URL")
	}
	s.rememberVerified(clientID)
	return nil
}

func (s *CognitoClientStore) isRemembered(clientID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	expiry, ok := s.verified[clientID]
	if !ok {
		return false
	}
	if s.now().Before(expiry) {
		return true
	}
	delete(s.verified, clientID)
	return false
}

func (s *CognitoClientStore) rememberVerified(clientID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verified[clientID] = s.now().Add(s.cfg.DCRClientCacheTTL)
}

func dcrClientName(prefix, requested string) (string, error) {
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("generate DCR client suffix: %w", err)
	}
	requested = sanitizeClientName(requested)
	if requested != "" {
		requested = "-" + requested
	}
	return fmt.Sprintf("%s-%d-%s%s", prefix, time.Now().Unix(), hex.EncodeToString(suffix[:]), requested), nil
}

func sanitizeClientName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
		if b.Len() >= 40 {
			break
		}
	}
	return strings.Trim(b.String(), "-_")
}

func oauthFlowsContain(values []types.OAuthFlowType, want types.OAuthFlowType) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func intersectStrings(a, b []string) []string {
	set := map[string]struct{}{}
	for _, value := range b {
		set[value] = struct{}{}
	}
	out := []string{}
	for _, value := range a {
		if _, ok := set[value]; ok {
			out = append(out, value)
		}
	}
	return out
}

func dcrError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": description,
	})
}
