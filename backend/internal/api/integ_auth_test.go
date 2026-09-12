package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"event-driven-context/internal/core"
	"event-driven-context/internal/v2"
)

type integAuthFixture struct {
	store       *core.Store
	issuer      *httptest.Server
	api         *httptest.Server
	browser     *http.Client
	config      IntegAuthConfig
	challenge   string
	email       string
	tokenCalled bool
}

func newIntegAuthFixture(t *testing.T) *integAuthFixture {
	t.Helper()
	f := &integAuthFixture{email: "person@example.com"}
	f.issuer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			f.tokenCalled = true
			clientID, secret, ok := r.BasicAuth()
			if !ok || clientID != "event-context" || secret != "test-client-secret" {
				t.Errorf("token Basic auth = %q %q %v", clientID, secret, ok)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if err := r.ParseForm(); err != nil {
				t.Error(err)
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "test-code" ||
				r.Form.Get("redirect_uri") != f.config.RedirectURI || pkceChallenge(r.Form.Get("code_verifier")) != f.challenge {
				t.Errorf("invalid token form: %#v", r.Form)
				http.Error(w, "invalid grant", http.StatusBadRequest)
				return
			}
			respond(w, http.StatusOK, map[string]string{"access_token": "central-access-token", "token_type": "Bearer"})
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer central-access-token" {
				t.Errorf("userinfo Authorization = %q", r.Header.Get("Authorization"))
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			respond(w, http.StatusOK, map[string]any{"sub": "central-user-1", "email": f.email, "roles": []string{}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.issuer.Close)
	store, err := core.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	f.store = store
	t.Cleanup(func() { _ = store.Close() })
	service, err := v2.New(store, filepath.Join(t.TempDir(), "v2"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	f.config = IntegAuthConfig{
		Issuer:       f.issuer.URL,
		ClientID:     "event-context",
		ClientSecret: "test-client-secret",
		RedirectURI:  "http://context-api.test/v1/auth/integ/callback",
		WebBaseURL:   "http://context.test",
	}
	f.api = httptest.NewServer(V2HandlerWithConfig(store, service, Config{AllowedOrigins: []string{"http://context.test"}, PublicBaseURL: "http://context-api.test", IntegAuth: f.config}))
	t.Cleanup(f.api.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	f.browser = &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	return f
}

func (f *integAuthFixture) start(t *testing.T, returnTo string) (*http.Response, *url.URL) {
	t.Helper()
	endpoint := f.api.URL + "/v1/auth/integ/start?ui_locales=" + url.QueryEscape("zh-Hans en") + "&return_to=" + url.QueryEscape(returnTo)
	res, err := f.browser.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("start status = %d", res.StatusCode)
	}
	location, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return res, location
}

func TestIntegAuthPKCEStateCookieLoginAndReturnTo(t *testing.T) {
	f := newIntegAuthFixture(t)
	returnTo := "/workspace.html?project=project-1#events"
	startResponse, authorize := f.start(t, returnTo)
	query := authorize.Query()
	state := query.Get("state")
	f.challenge = query.Get("code_challenge")
	if authorize.Scheme+"://"+authorize.Host+authorize.Path != f.issuer.URL+"/authorize" ||
		query.Get("response_type") != "code" || query.Get("client_id") != "event-context" ||
		query.Get("redirect_uri") != f.config.RedirectURI || query.Get("code_challenge_method") != "S256" ||
		query.Get("ui_locales") != "zh-Hans en" || !validIntegAuthState(state) || f.challenge == "" {
		t.Fatalf("invalid authorize redirect: %s", authorize)
	}
	cookies := startResponse.Cookies()
	if len(cookies) != 1 || cookies[0].Name != localAuthCookiePrefix+state || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].MaxAge != int(integAuthLifetime.Seconds()) {
		t.Fatalf("invalid transaction cookie login transaction cookie: %#v", cookies)
	}

	callback, err := f.browser.Get(f.api.URL + "/v1/auth/integ/callback?code=test-code&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatal(err)
	}
	defer callback.Body.Close()
	if callback.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d", callback.StatusCode)
	}
	destination, err := url.Parse(callback.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if destination.Scheme+"://"+destination.Host != "http://context.test" || destination.Path != "/workspace.html" ||
		destination.Query().Get("project") != "project-1" || destination.Query().Get("auth") != "complete" || destination.Fragment != "events" {
		t.Fatalf("callback destination lost return_to: %s", destination)
	}
	var session *http.Cookie
	for _, cookie := range callback.Cookies() {
		if cookie.Name == localSessionCookieName {
			session = cookie
		}
	}
	if session == nil || session.Value == "" || !session.HttpOnly || session.SameSite != http.SameSiteLaxMode {
		t.Fatalf("missing product session cookie: %#v", callback.Cookies())
	}

	me, err := f.browser.Get(f.api.URL + "/v1/me")
	if err != nil {
		t.Fatal(err)
	}
	defer me.Body.Close()
	var user core.User
	if me.StatusCode != http.StatusOK || json.NewDecoder(me.Body).Decode(&user) != nil || user.Email != "person@example.com" {
		t.Fatalf("cookie-authenticated me status=%d user=%+v", me.StatusCode, user)
	}

	logoutRequest, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, f.api.URL+"/v1/auth/logout", nil)
	logout, err := f.browser.Do(logoutRequest)
	if err != nil {
		t.Fatal(err)
	}
	logout.Body.Close()
	if logout.StatusCode != http.StatusOK {
		t.Fatalf("logout status = %d", logout.StatusCode)
	}
	me, err = f.browser.Get(f.api.URL + "/v1/me")
	if err != nil {
		t.Fatal(err)
	}
	me.Body.Close()
	if me.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session remained valid after logout: %d", me.StatusCode)
	}
}

func TestIntegAuthTransactionsAreStateIsolated(t *testing.T) {
	f := newIntegAuthFixture(t)
	_, first := f.start(t, "/workspace.html?project=first#events")
	_, second := f.start(t, "/workspace.html?project=second#state")
	firstState, secondState := first.Query().Get("state"), second.Query().Get("state")
	if firstState == secondState {
		t.Fatal("parallel login starts reused state")
	}
	f.challenge = first.Query().Get("code_challenge")
	response, err := f.browser.Get(f.api.URL + "/v1/auth/integ/callback?code=test-code&state=" + url.QueryEscape(firstState))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusFound || !strings.Contains(response.Header.Get("Location"), "project=first") {
		t.Fatalf("first transaction was overwritten: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	apiURL, _ := url.Parse(f.api.URL)
	for _, cookie := range f.browser.Jar.Cookies(apiURL) {
		if cookie.Name == localAuthCookiePrefix+secondState {
			return
		}
	}
	t.Fatal("completing first login cleared the second transaction")
}

func TestIntegAuthFailureDoesNotIssueSession(t *testing.T) {
	f := newIntegAuthFixture(t)
	_, authorize := f.start(t, "/")
	f.challenge = authorize.Query().Get("code_challenge")
	f.email = ""
	response, err := f.browser.Get(f.api.URL + "/v1/auth/integ/callback?code=test-code&state=" + url.QueryEscape(authorize.Query().Get("state")))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadGateway || !f.tokenCalled {
		t.Fatalf("userinfo failure status=%d tokenCalled=%v", response.StatusCode, f.tokenCalled)
	}
	apiURL, _ := url.Parse(f.api.URL)
	for _, cookie := range f.browser.Jar.Cookies(apiURL) {
		if cookie.Name == localSessionCookieName || cookie.Name == sessionCookieName {
			t.Fatalf("failure issued a session cookie: %#v", cookie)
		}
	}
}

func TestIntegAuthRejectsCrossOriginReturnAndBadState(t *testing.T) {
	f := newIntegAuthFixture(t)
	response, err := f.browser.Get(f.api.URL + "/v1/auth/integ/start?return_to=" + url.QueryEscape("https://evil.example/"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("cross-origin return_to status = %d", response.StatusCode)
	}
	_, authorize := f.start(t, "/")
	response, err = f.browser.Get(f.api.URL + "/v1/auth/integ/callback?code=test-code&state=" + strings.Repeat("A", 43))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest || f.tokenCalled {
		t.Fatalf("bad state status=%d tokenCalled=%v authorize=%s", response.StatusCode, f.tokenCalled, authorize)
	}
}

func TestIntegAuthProductionCookiesUseHostPrefixAndSecure(t *testing.T) {
	config := IntegAuthConfig{
		Issuer:        "https://auth.integ.life",
		ClientID:      "event-context",
		ClientSecret:  "test-client-secret",
		RedirectURI:   "https://context-api.integ.life/v1/auth/integ/callback",
		WebBaseURL:    "https://context.integ.life",
		SecureCookies: true,
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/auth/integ/start?return_to=%2Fworkspace.html", nil)
	response := httptest.NewRecorder()
	integAuthStart(response, request, config, "https://context-api.integ.life")
	result := response.Result()
	defer result.Body.Close()
	state := mustParseURL(t, result.Header.Get("Location")).Query().Get("state")
	cookies := result.Cookies()
	if len(cookies) != 1 || cookies[0].Name != integAuthCookiePrefix+state || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Domain != "" || cookies[0].Path != "/" {
		t.Fatalf("invalid production transaction cookie: %#v", cookies)
	}

	response = httptest.NewRecorder()
	setSessionCookie(response, core.LoginResult{Token: "product-session", ExpiresAt: time.Now().Add(time.Hour)}, true)
	cookies = response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookieName || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Domain != "" || cookies[0].Path != "/" || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("invalid production session cookie: %#v", cookies)
	}
}

func TestIntegAuthReturnsMCPLoginToOriginalConsent(t *testing.T) {
	f := newIntegAuthFixture(t)
	registration := `{"client_name":"MCP Test","redirect_uris":["https://client.example/callback"],"grant_types":["authorization_code"],"response_types":["code"],"token_endpoint_auth_method":"none"}`
	response, err := f.browser.Post(f.api.URL+"/oauth/register", "application/json", strings.NewReader(registration))
	if err != nil {
		t.Fatal(err)
	}
	var registered struct {
		ClientID string `json:"client_id"`
	}
	if response.StatusCode != http.StatusCreated || json.NewDecoder(response.Body).Decode(&registered) != nil || registered.ClientID == "" {
		response.Body.Close()
		t.Fatalf("OAuth client registration failed: status=%d client=%+v", response.StatusCode, registered)
	}
	response.Body.Close()
	digest := sha256.Sum256([]byte(strings.Repeat("v", 64)))
	authorization := url.Values{
		"response_type":         {"code"},
		"client_id":             {registered.ClientID},
		"redirect_uri":          {"https://client.example/callback"},
		"state":                 {"mcp-client-state"},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(digest[:])},
		"code_challenge_method": {"S256"},
		"resource":              {"http://context-api.test/mcp"},
		"scope":                 {core.ScopeRead},
		"lang":                  {"zh-CN"},
	}
	response, err = f.browser.Get(f.api.URL + "/oauth/authorize?" + authorization.Encode())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("OAuth authorization start status = %d", response.StatusCode)
	}
	requestID := mustParseURL(t, response.Header.Get("Location")).Query().Get("request_id")
	if requestID == "" {
		t.Fatal("OAuth authorization did not create request_id")
	}

	returnTo := "/oauth/authorize?request_id=" + url.QueryEscape(requestID) + "&lang=zh-CN"
	_, centralAuthorize := f.start(t, returnTo)
	f.challenge = centralAuthorize.Query().Get("code_challenge")
	response, err = f.browser.Get(f.api.URL + "/v1/auth/integ/callback?code=test-code&state=" + url.QueryEscape(centralAuthorize.Query().Get("state")))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	returnLocation := mustParseURL(t, response.Header.Get("Location"))
	if response.StatusCode != http.StatusFound || returnLocation.Scheme+"://"+returnLocation.Host != "http://context-api.test" ||
		returnLocation.Path != "/oauth/authorize" || returnLocation.Query().Get("request_id") != requestID ||
		returnLocation.Query().Get("lang") != "zh-CN" || returnLocation.Query().Get("auth") != "" {
		t.Fatalf("central login returned to wrong consent URL: status=%d location=%s", response.StatusCode, returnLocation)
	}

	response, err = f.browser.Get(f.api.URL + returnLocation.RequestURI())
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(body), `name="decision" value="approve"`) || strings.Contains(string(body), `autocomplete="current-password"`) {
		t.Fatalf("returned consent did not recognize central session: status=%d body=%s err=%v", response.StatusCode, body, readErr)
	}

	response, err = f.browser.PostForm(f.api.URL+"/oauth/authorize", url.Values{"request_id": {requestID}, "lang": {"zh-CN"}, "decision": {"approve"}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	clientCallback := mustParseURL(t, response.Header.Get("Location"))
	if response.StatusCode != http.StatusFound || clientCallback.Host != "client.example" || clientCallback.Query().Get("code") == "" || clientCallback.Query().Get("state") != "mcp-client-state" {
		t.Fatalf("consent approval failed: status=%d location=%s", response.StatusCode, clientCallback)
	}
}

func TestIntegAuthRejectsMalformedInternalOAuthReturn(t *testing.T) {
	f := newIntegAuthFixture(t)
	for _, returnTo := range []string{
		"/oauth/authorize",
		"/oauth/authorize?request_id=wrong-prefix",
		"/oauth/authorize?request_id=oauth_request_valid123&next=https://evil.example",
		"/oauth/authorize?request_id=oauth_request_valid123#fragment",
	} {
		response, err := f.browser.Get(f.api.URL + "/v1/auth/integ/start?return_to=" + url.QueryEscape(returnTo))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("return_to %q status = %d", returnTo, response.StatusCode)
		}
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestCredentialedCORSAndBearerPriorityOverCookie(t *testing.T) {
	f := newIntegAuthFixture(t)
	credentials := core.Credentials{Username: "bearer-user", Email: "bearer@example.com", Password: "password-123-long"}
	if _, err := f.store.Register(context.Background(), credentials); err != nil {
		t.Fatal(err)
	}
	login, err := f.store.Login(context.Background(), credentials)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, f.api.URL+"/v1/me", nil)
	request.Header.Set("Origin", "http://context.test")
	request.Header.Set("Authorization", "Bearer "+login.Token)
	request.AddCookie(&http.Cookie{Name: localSessionCookieName, Value: "invalid-cookie-token"})
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Access-Control-Allow-Origin") != "http://context.test" || response.Header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("credentialed CORS/Bearer priority failed: status=%d headers=%#v", response.StatusCode, response.Header)
	}
}
