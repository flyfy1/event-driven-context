package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"event-driven-context/internal/core"
)

const (
	integAuthLifetime       = 10 * time.Minute
	integAuthCookiePrefix   = "__Host-edc_integ_auth_"
	localAuthCookiePrefix   = "edc_integ_auth_"
	sessionCookieName       = "__Host-edc_session"
	localSessionCookieName  = "edc_session"
	integAuthResponseLimit  = 64 << 10
	integAuthRequestTimeout = 10 * time.Second
)

// IntegAuthConfig connects the Context browser session to the central
// Integ.Life OAuth Authorization Code + PKCE service.
type IntegAuthConfig struct {
	Issuer        string
	ClientID      string
	ClientSecret  string
	RedirectURI   string
	WebBaseURL    string
	SecureCookies bool
	HTTPClient    *http.Client
}

type integAuthTransaction struct {
	State     string `json:"state"`
	Verifier  string `json:"verifier"`
	ReturnTo  string `json:"return_to"`
	ExpiresAt int64  `json:"expires_at"`
}

type integTokenResponse struct {
	AccessToken string `json:"access_token"`
}

type integUserInfo struct {
	Subject string `json:"sub"`
	Email   string `json:"email"`
}

func registerIntegAuthHandlers(mux *http.ServeMux, store *core.Store, config IntegAuthConfig, apiBaseURL string) {
	mux.HandleFunc("GET /v1/auth/integ/start", func(w http.ResponseWriter, r *http.Request) {
		integAuthStart(w, r, config, apiBaseURL)
	})
	mux.HandleFunc("GET /v1/auth/integ/callback", func(w http.ResponseWriter, r *http.Request) {
		integAuthCallback(w, r, store, config, apiBaseURL)
	})
}

func integAuthStart(w http.ResponseWriter, r *http.Request, config IntegAuthConfig, apiBaseURL string) {
	if err := validateIntegAuthConfig(config); err != nil {
		http.Error(w, "central login is unavailable", http.StatusServiceUnavailable)
		return
	}
	returnTo, err := validReturnTo(r.URL.Query().Get("return_to"), apiBaseURL != "")
	if err != nil {
		fail(w, core.Invalid("return_to must be a same-product absolute path"))
		return
	}
	state, err := randomURLToken(32)
	if err != nil {
		http.Error(w, "could not start login", http.StatusInternalServerError)
		return
	}
	verifier, err := randomURLToken(32)
	if err != nil {
		http.Error(w, "could not start login", http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC()
	tx := integAuthTransaction{State: state, Verifier: verifier, ReturnTo: returnTo, ExpiresAt: now.Add(integAuthLifetime).Unix()}
	value, err := encodeIntegAuthTransaction(tx, config.ClientSecret)
	if err != nil {
		http.Error(w, "could not start login", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     integAuthCookieName(config.SecureCookies, state),
		Value:    value,
		Path:     "/",
		Expires:  now.Add(integAuthLifetime),
		MaxAge:   int(integAuthLifetime.Seconds()),
		HttpOnly: true,
		Secure:   config.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})

	authorize, _ := url.Parse(strings.TrimRight(config.Issuer, "/") + "/authorize")
	query := authorize.Query()
	query.Set("response_type", "code")
	query.Set("client_id", config.ClientID)
	query.Set("redirect_uri", config.RedirectURI)
	query.Set("state", state)
	query.Set("code_challenge", pkceChallenge(verifier))
	query.Set("code_challenge_method", "S256")
	if locales := strings.TrimSpace(r.URL.Query().Get("ui_locales")); locales != "" {
		query.Set("ui_locales", locales)
	}
	authorize.RawQuery = query.Encode()
	http.Redirect(w, r, authorize.String(), http.StatusFound)
}

func integAuthCallback(w http.ResponseWriter, r *http.Request, store *core.Store, config IntegAuthConfig, apiBaseURL string) {
	if err := validateIntegAuthConfig(config); err != nil {
		http.Error(w, "central login is unavailable", http.StatusServiceUnavailable)
		return
	}
	state := r.URL.Query().Get("state")
	if !validIntegAuthState(state) {
		http.Error(w, "invalid or expired login state", http.StatusBadRequest)
		return
	}
	cookieName := integAuthCookieName(config.SecureCookies, state)
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		http.Error(w, "invalid or expired login state", http.StatusBadRequest)
		return
	}
	clearCookie(w, cookieName, config.SecureCookies)
	tx, err := decodeIntegAuthTransaction(cookie.Value, config.ClientSecret)
	if err != nil || time.Now().UTC().Unix() >= tx.ExpiresAt || !hmac.Equal([]byte(tx.State), []byte(state)) {
		http.Error(w, "invalid or expired login state", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" || r.URL.Query().Get("error") != "" {
		http.Error(w, "central login was not completed", http.StatusBadRequest)
		return
	}
	accessToken, err := exchangeIntegCode(r, config, code, tx.Verifier)
	if err != nil {
		http.Error(w, "central login token exchange failed", http.StatusBadGateway)
		return
	}
	identity, err := fetchIntegUserInfo(r, config, accessToken)
	if err != nil {
		http.Error(w, "central login identity lookup failed", http.StatusBadGateway)
		return
	}
	login, err := store.LoginInteg(r.Context(), strings.TrimRight(config.Issuer, "/"), identity.Subject, identity.Email)
	if err != nil {
		fail(w, err)
		return
	}
	setSessionCookie(w, login, config.SecureCookies)
	destination, err := integAuthReturnURL(config.WebBaseURL, apiBaseURL, tx.ReturnTo)
	if err != nil {
		http.Error(w, "central login return URL is invalid", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, destination, http.StatusFound)
}

func exchangeIntegCode(r *http.Request, config IntegAuthConfig, code, verifier string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {config.RedirectURI},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, strings.TrimRight(config.Issuer, "/")+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(config.ClientID, config.ClientSecret)
	var response integTokenResponse
	if err = doIntegJSON(config, req, &response); err != nil {
		return "", err
	}
	if response.AccessToken == "" {
		return "", errors.New("token response did not contain access_token")
	}
	return response.AccessToken, nil
}

func fetchIntegUserInfo(r *http.Request, config IntegAuthConfig, accessToken string) (integUserInfo, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, strings.TrimRight(config.Issuer, "/")+"/userinfo", nil)
	if err != nil {
		return integUserInfo{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	var identity integUserInfo
	if err = doIntegJSON(config, req, &identity); err != nil {
		return integUserInfo{}, err
	}
	identity.Subject = strings.TrimSpace(identity.Subject)
	identity.Email = strings.TrimSpace(identity.Email)
	if identity.Subject == "" || identity.Email == "" {
		return integUserInfo{}, errors.New("userinfo did not contain subject and email")
	}
	return identity, nil
}

func doIntegJSON(config IntegAuthConfig, req *http.Request, out any) error {
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: integAuthRequestTimeout}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, integAuthResponseLimit))
		return fmt.Errorf("central auth returned HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, integAuthResponseLimit+1))
	if err != nil {
		return err
	}
	if len(body) > integAuthResponseLimit {
		return errors.New("central auth response is too large")
	}
	if err = json.Unmarshal(body, out); err != nil {
		return err
	}
	return nil
}

func encodeIntegAuthTransaction(tx integAuthTransaction, secret string) (string, error) {
	payload, err := json.Marshal(tx)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func decodeIntegAuthTransaction(value, secret string) (integAuthTransaction, error) {
	var tx integAuthTransaction
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return tx, errors.New("invalid transaction")
	}
	providedMAC, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return tx, errors.New("invalid transaction")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(providedMAC, mac.Sum(nil)) {
		return tx, errors.New("invalid transaction")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(payload, &tx) != nil || !validIntegAuthState(tx.State) || tx.Verifier == "" || tx.ReturnTo == "" {
		return integAuthTransaction{}, errors.New("invalid transaction")
	}
	return tx, nil
}

func randomURLToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func pkceChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func validIntegAuthState(state string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(state)
	return err == nil && len(decoded) == 32 && base64.RawURLEncoding.EncodeToString(decoded) == state
}

func validReturnTo(raw string, allowOAuthReturn bool) (string, error) {
	if raw == "" {
		raw = "/"
	}
	decoded, decodeErr := url.PathUnescape(raw)
	if len(raw) > 2048 || decodeErr != nil || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.Contains(decoded, "\\") || hasURLControl(decoded) {
		return "", errors.New("invalid return path")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil {
		return "", errors.New("invalid return path")
	}
	if parsed.Path == "/oauth/authorize" && (!allowOAuthReturn || !validOAuthReturnQuery(parsed)) {
		return "", errors.New("invalid OAuth return path")
	}
	return parsed.String(), nil
}

func validOAuthReturnQuery(returnTo *url.URL) bool {
	if returnTo.Fragment != "" {
		return false
	}
	query := returnTo.Query()
	for name, values := range query {
		if (name != "request_id" && name != "lang") || len(values) != 1 {
			return false
		}
	}
	requestID := query.Get("request_id")
	if !strings.HasPrefix(requestID, "oauth_request_") || !validRequestID(requestID) {
		return false
	}
	if language := query.Get("lang"); language != "" && normalizeOAuthLanguage(language) == "" {
		return false
	}
	return true
}

func hasURLControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func integAuthReturnURL(webBase, apiBase, returnTo string) (string, error) {
	reference, err := url.Parse(returnTo)
	if err != nil {
		return "", err
	}
	baseRaw := webBase
	isOAuthReturn := reference.Path == "/oauth/authorize"
	if isOAuthReturn {
		if !validOAuthReturnQuery(reference) {
			return "", errors.New("invalid OAuth return path")
		}
		baseRaw = apiBase
	}
	base, err := url.Parse(baseRaw)
	if err != nil || base.Scheme == "" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return "", errors.New("invalid web base URL")
	}
	destination := base.ResolveReference(reference)
	if destination.Scheme != base.Scheme || destination.Host != base.Host {
		return "", errors.New("return path escaped web origin")
	}
	if !isOAuthReturn {
		query := destination.Query()
		query.Set("auth", "complete")
		destination.RawQuery = query.Encode()
	}
	return destination.String(), nil
}

func validateIntegAuthConfig(config IntegAuthConfig) error {
	if strings.TrimSpace(config.ClientID) == "" || config.ClientSecret == "" || config.RedirectURI == "" {
		return errors.New("central auth client is incomplete")
	}
	for _, raw := range []string{config.Issuer, config.RedirectURI, config.WebBaseURL} {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
			return errors.New("central auth URL is invalid")
		}
		if config.SecureCookies && parsed.Scheme != "https" {
			return errors.New("central auth production URLs must use HTTPS")
		}
	}
	return nil
}

func integAuthCookieName(secure bool, state string) string {
	if secure {
		return integAuthCookiePrefix + state
	}
	return localAuthCookiePrefix + state
}

func configuredSessionCookieName(secure bool) string {
	if secure {
		return sessionCookieName
	}
	return localSessionCookieName
}

func setSessionCookie(w http.ResponseWriter, login core.LoginResult, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     configuredSessionCookieName(secure),
		Value:    login.Token,
		Path:     "/",
		Expires:  login.ExpiresAt,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookies(w http.ResponseWriter) {
	clearCookie(w, sessionCookieName, true)
	clearCookie(w, localSessionCookieName, false)
}

func clearCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func requestSessionToken(r *http.Request) string {
	if token := bearer(r); token != "" {
		return token
	}
	for _, name := range []string{sessionCookieName, localSessionCookieName} {
		if cookie, err := r.Cookie(name); err == nil && cookie.Value != "" {
			return cookie.Value
		}
	}
	return ""
}
