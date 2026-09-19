package hubconnectors

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// GoogleOAuth contains deployment settings, never agent-controlled request
// parameters. Store client secrets and refresh tokens outside source control.
type GoogleOAuth struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	HTTPClient   *http.Client
}

// GoogleToken must remain in encrypted server-side credential storage. Never
// return this structure to an agent, include it in errors, or log its contents.
type GoogleToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope,omitempty"`
	TokenType    string `json:"token_type"`
}

var pkceValue = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

func (g GoogleOAuth) validate() error {
	if strings.TrimSpace(g.ClientID) == "" || strings.TrimSpace(g.ClientSecret) == "" {
		return &Error{Code: "oauth_not_configured"}
	}
	u, err := url.Parse(g.RedirectURL)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return &Error{Code: "invalid_redirect_uri"}
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "[::1]" || u.Hostname() == "::1" || u.Hostname() == "localhost")) {
		return &Error{Code: "invalid_redirect_uri"}
	}
	return nil
}

// AuthorizationURL uses PKCE S256 and offline access. The host must generate
// random state and verifier values, store a short-lived one-use state record
// bound to the signed-in owner, connection and requested scopes, and validate
// that state on callback before ExchangeCode. This function does not persist it.
func (g GoogleOAuth) AuthorizationURL(state, verifier string, providerIDs []string) (string, error) {
	if err := g.validate(); err != nil {
		return "", err
	}
	if !pkceValue.MatchString(verifier) || len(state) < 32 || len(state) > 512 || strings.ContainsAny(state, "\r\n\x00") {
		return "", &Error{Code: "invalid_oauth_state_or_verifier"}
	}
	scopes := []string{}
	seen := map[string]bool{}
	for _, p := range providerIDs {
		requested := GoogleScopes(p)
		if len(requested) == 0 {
			return "", &Error{Code: "invalid_oauth_provider"}
		}
		for _, scope := range requested {
			if !seen[scope] {
				scopes = append(scopes, scope)
				seen[scope] = true
			}
		}
	}
	if len(scopes) == 0 {
		return "", &Error{Code: "invalid_oauth_provider"}
	}
	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {g.ClientID}, "redirect_uri": {g.RedirectURL}, "response_type": {"code"}, "scope": {strings.Join(scopes, " ")}, "state": {state}, "access_type": {"offline"}, "prompt": {"consent"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"}}
	return "https://accounts.google.com/o/oauth2/v2/auth?" + q.Encode(), nil
}

func (g GoogleOAuth) ExchangeCode(ctx context.Context, code, verifier string) (GoogleToken, error) {
	if err := g.validate(); err != nil {
		return GoogleToken{}, err
	}
	if code == "" || len(code) > 8192 || !pkceValue.MatchString(verifier) {
		return GoogleToken{}, &Error{Code: "invalid_oauth_code_or_verifier"}
	}
	return g.token(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {g.RedirectURL}})
}

// Refresh does not replace the stored refresh token when Google omits it from
// a successful response. The caller merges the returned token atomically.
func (g GoogleOAuth) Refresh(ctx context.Context, refreshToken string) (GoogleToken, error) {
	if err := g.validate(); err != nil {
		return GoogleToken{}, err
	}
	if refreshToken == "" || len(refreshToken) > 8192 {
		return GoogleToken{}, &Error{Code: "invalid_refresh_token"}
	}
	return g.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}})
}

func (g GoogleOAuth) token(ctx context.Context, q url.Values) (GoogleToken, error) {
	q.Set("client_id", g.ClientID)
	q.Set("client_secret", g.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(q.Encode()))
	if err != nil {
		return GoogleToken{}, &Error{Code: "invalid_oauth_request"}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := NewClient(g.HTTPClient).http.Do(req)
	if err != nil {
		return GoogleToken{}, &Error{Code: "oauth_upstream_unavailable"}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return GoogleToken{}, &Error{Code: "oauth_exchange_failed", StatusCode: res.StatusCode}
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 65537))
	if err != nil || len(b) > 65536 {
		return GoogleToken{}, &Error{Code: "invalid_oauth_response"}
	}
	var token GoogleToken
	if json.Unmarshal(b, &token) != nil || token.AccessToken == "" || token.ExpiresIn <= 0 || token.ExpiresIn > 7*24*3600 || !strings.EqualFold(token.TokenType, "Bearer") {
		return GoogleToken{}, &Error{Code: "invalid_oauth_response"}
	}
	return token, nil
}

// GoogleAccountID verifies the credential against the provider's account
// endpoint. It never accepts an identity supplied by a browser or agent.
func (c *Client) GoogleAccountID(ctx context.Context, providerID, accessToken string) (string, error) {
	endpoint := ""
	switch providerID {
	case "google-drive":
		endpoint = "https://www.googleapis.com/drive/v3/about?fields=user(emailAddress,permissionId)"
	case "gmail":
		endpoint = "https://gmail.googleapis.com/gmail/v1/users/me/profile"
	default:
		if !googlePersonal(providerID) {
			return "", &Error{Code: "invalid_oauth_provider"}
		}
		endpoint = "https://openidconnect.googleapis.com/v1/userinfo"
	}
	if len(accessToken) == 0 || len(accessToken) > 8192 || strings.ContainsAny(accessToken, " \t\r\n\x00") {
		return "", &Error{Code: "invalid_credential"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", &Error{Code: "invalid_request"}
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return "", &Error{Code: "upstream_unavailable"}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", &Error{Code: "account_verification_failed", StatusCode: res.StatusCode}
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 65537))
	if err != nil || len(b) > 65536 {
		return "", &Error{Code: "invalid_account_response"}
	}
	var profile struct {
		Sub          string `json:"sub"`
		EmailAddress string `json:"emailAddress"`
		User         struct {
			EmailAddress string `json:"emailAddress"`
			PermissionID string `json:"permissionId"`
		} `json:"user"`
	}
	if json.Unmarshal(b, &profile) != nil {
		return "", &Error{Code: "invalid_account_response"}
	}
	id := profile.EmailAddress
	if googlePersonal(providerID) {
		id = profile.Sub
	}
	if providerID == "google-drive" {
		id = profile.User.PermissionID
		if id == "" {
			id = profile.User.EmailAddress
		}
	}
	if len(id) == 0 || len(id) > 254 || strings.ContainsAny(id, "\r\n\x00") {
		return "", &Error{Code: "invalid_account_response"}
	}
	return id, nil
}
