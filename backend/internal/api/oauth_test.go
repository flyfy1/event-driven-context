package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"event-driven-context/internal/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const testOAuthIssuer = "https://context-api.integ.life"

type oauthFixture struct {
	store    *core.Store
	server   *httptest.Server
	client   *http.Client
	clientID string
}

func newOAuthFixture(t *testing.T, ttl time.Duration) *oauthFixture {
	t.Helper()
	store, err := core.Open(t.TempDir() + "/context.db")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Register(context.Background(), core.Credentials{Username: "alice", Password: "integration-password-123"}); err != nil {
		t.Fatal(err)
	}
	h := httptest.NewTLSServer(HandlerWithConfig(store, Config{PublicBaseURL: testOAuthIssuer, OAuthAccessTokenTTL: ttl}))
	jar, _ := cookiejar.New(nil)
	client := h.Client()
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	t.Cleanup(func() { h.Close(); store.Close() })
	f := &oauthFixture{store: store, server: h, client: client}
	f.clientID = f.registerClient(t)
	return f
}

func (f *oauthFixture) registerClient(t *testing.T) string {
	t.Helper()
	body := `{"client_name":"ChatGPT Test","redirect_uris":["https://client.example/callback"],"grant_types":["authorization_code"],"response_types":["code"],"token_endpoint_auth_method":"none","application_type":"web"}`
	res := f.do(t, http.MethodPost, "/oauth/register", "application/json", body)
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("register: %d %s", res.StatusCode, b)
	}
	var out struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil || out.ClientID == "" {
		t.Fatalf("register response: %+v %v", out, err)
	}
	return out.ClientID
}

func TestOAuthDynamicRegistrationAllowsOptionalClientName(t *testing.T) {
	store, err := core.Open(t.TempDir() + "/context.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	h := httptest.NewServer(HandlerWithConfig(store, Config{PublicBaseURL: testOAuthIssuer}))
	t.Cleanup(h.Close)
	body := `{"redirect_uris":["https://chatgpt.com/connector/oauth/test"],"grant_types":["authorization_code","refresh_token"],"response_types":["code"],"token_endpoint_auth_method":"none"}`
	res, err := http.Post(h.URL+"/oauth/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("register without client_name: %d %s", res.StatusCode, b)
	}
	var out struct {
		ClientID   string   `json:"client_id"`
		ClientName string   `json:"client_name"`
		GrantTypes []string `json:"grant_types"`
	}
	if err = json.NewDecoder(res.Body).Decode(&out); err != nil || out.ClientID == "" || out.ClientName != "OAuth client" || len(out.GrantTypes) != 1 || out.GrantTypes[0] != "authorization_code" {
		t.Fatalf("registration response: %+v %v", out, err)
	}
}

func TestOAuthPageRegistrationContinuesAuthorization(t *testing.T) {
	f := newOAuthFixture(t, time.Hour)
	verifier := strings.Repeat("r", 64)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type": {"code"}, "client_id": {f.clientID}, "redirect_uri": {"https://client.example/callback"},
		"state": {"registration-state"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"}, "resource": {testOAuthIssuer + "/mcp"}, "scope": {core.ScopeRead}, "lang": {"zh-CN"},
	}
	res := f.do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), "", "")
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("authorization start: %d", res.StatusCode)
	}
	requestLocation, _ := url.Parse(res.Header.Get("Location"))
	requestID := requestLocation.Query().Get("request_id")
	res = f.do(t, http.MethodGet, res.Header.Get("Location"), "", "")
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Language") != "zh-CN" || !bytes.Contains(page, []byte("创建账号")) {
		t.Fatalf("registration option missing: %d %s", res.StatusCode, page)
	}

	form := url.Values{"request_id": {requestID}, "lang": {"zh-CN"}, "decision": {"register"}, "username": {"new-user"}, "password": {"new-user-password-123"}}
	res = f.do(t, http.MethodPost, "/oauth/authorize", "application/x-www-form-urlencoded", form.Encode())
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("register: %d", res.StatusCode)
	}
	registeredLocation, _ := url.Parse(res.Header.Get("Location"))
	if registeredLocation.Query().Get("lang") != "zh-CN" {
		t.Fatalf("registration lost language: %s", res.Header.Get("Location"))
	}
	res = f.do(t, http.MethodGet, res.Header.Get("Location"), "", "")
	page, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !bytes.Contains(page, []byte("当前登录用户 <strong>new-user</strong>")) || !bytes.Contains(page, []byte(core.ScopeRead)) {
		t.Fatalf("registration did not continue to consent: %d %s", res.StatusCode, page)
	}

	form = url.Values{"request_id": {requestID}, "decision": {"approve"}}
	res = f.do(t, http.MethodPost, "/oauth/authorize", "application/x-www-form-urlencoded", form.Encode())
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("consent: %d", res.StatusCode)
	}
	callback, _ := url.Parse(res.Header.Get("Location"))
	tokenForm := url.Values{
		"grant_type": {"authorization_code"}, "client_id": {f.clientID}, "redirect_uri": {"https://client.example/callback"},
		"code": {callback.Query().Get("code")}, "code_verifier": {verifier}, "resource": {testOAuthIssuer + "/mcp"},
	}
	res = f.do(t, http.MethodPost, "/oauth/token", "application/x-www-form-urlencoded", tokenForm.Encode())
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("token exchange after registration: %d %s", res.StatusCode, b)
	}
}

func TestOAuthPageRegistrationAllowsOpenAIFormOrigin(t *testing.T) {
	f := newOAuthFixture(t, time.Hour)
	verifier := strings.Repeat("o", 64)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type": {"code"}, "client_id": {f.clientID}, "redirect_uri": {"https://client.example/callback"},
		"state": {"openai-origin-state"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"}, "resource": {testOAuthIssuer + "/mcp"}, "scope": {core.ScopeRead},
	}
	res := f.do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), "", "")
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("authorization start: %d", res.StatusCode)
	}
	requestLocation, _ := url.Parse(res.Header.Get("Location"))
	requestID := requestLocation.Query().Get("request_id")
	res = f.do(t, http.MethodGet, res.Header.Get("Location"), "", "")
	res.Body.Close()

	form := url.Values{"request_id": {requestID}, "decision": {"register"}, "username": {"openai-user"}, "password": {"openai-user-password-123"}}
	req, err := http.NewRequest(http.MethodPost, f.server.URL+"/oauth/authorize", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://chatgpt.com")
	res, err = f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("OpenAI-origin registration: got %d want %d", res.StatusCode, http.StatusSeeOther)
	}

	res = f.do(t, http.MethodGet, res.Header.Get("Location"), "", "")
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !bytes.Contains(page, []byte("Signed in as <strong>openai-user</strong>")) {
		t.Fatalf("registration did not continue to consent: %d %s", res.StatusCode, page)
	}
}

func TestOAuthAuthorizationFormStillRequiresCSRFSession(t *testing.T) {
	f := newOAuthFixture(t, time.Hour)
	form := url.Values{"request_id": {"oauth_request_missing"}, "decision": {"register"}, "username": {"attacker"}, "password": {"attacker-password-123"}}
	req, err := http.NewRequest(http.MethodPost, f.server.URL+"/oauth/authorize", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	res, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("cross-origin form without CSRF: got %d want %d", res.StatusCode, http.StatusBadRequest)
	}
}

func TestOAuthConcurrentAuthorizationRequestsRemainBrowserBound(t *testing.T) {
	f := newOAuthFixture(t, time.Hour)
	requestA, cookieA := f.startAuthorization(t, "state-a", strings.Repeat("a", 64))
	requestB, cookieB := f.startAuthorization(t, "state-b", strings.Repeat("b", 64))
	if cookieA == cookieB || !strings.HasPrefix(cookieA, "edc_oauth_csrf_") || !strings.HasPrefix(cookieB, "edc_oauth_csrf_") {
		t.Fatalf("authorization requests did not receive distinct transaction cookies: %q %q", cookieA, cookieB)
	}

	res := f.do(t, http.MethodGet, oauthRequestLocation(requestA, "en"), "", "")
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !bytes.Contains(page, []byte("Sign in")) {
		t.Fatalf("older authorization request lost after newer start: %d %s", res.StatusCode, page)
	}

	form := url.Values{"request_id": {requestA}, "decision": {"login"}, "username": {"alice"}, "password": {"integration-password-123"}}
	res = f.do(t, http.MethodPost, "/oauth/authorize", "application/x-www-form-urlencoded", form.Encode())
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("login on older request: %d", res.StatusCode)
	}

	codeA := f.approveAuthorization(t, requestA, "state-a")
	codeB := f.approveAuthorization(t, requestB, "state-b")
	f.exchangeAuthorizationCode(t, codeA, strings.Repeat("a", 64))
	f.exchangeAuthorizationCode(t, codeB, strings.Repeat("b", 64))
}

func TestOAuthDuplicateAuthorizationSubmissionShowsRestartGuidance(t *testing.T) {
	f := newOAuthFixture(t, time.Hour)
	requestID, _ := f.startAuthorization(t, "duplicate-state", strings.Repeat("d", 64))
	form := url.Values{"request_id": {requestID}, "decision": {"login"}, "username": {"alice"}, "password": {"integration-password-123"}}
	res := f.do(t, http.MethodPost, "/oauth/authorize", "application/x-www-form-urlencoded", form.Encode())
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", res.StatusCode)
	}
	f.approveAuthorization(t, requestID, "duplicate-state")

	form = url.Values{"request_id": {requestID}, "decision": {"approve"}}
	res = f.do(t, http.MethodPost, "/oauth/authorize", "application/x-www-form-urlencoded", form.Encode())
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") || !bytes.Contains(page, []byte("start the connection again")) || bytes.Contains(page, []byte(`"error":"invalid_request"`)) {
		t.Fatalf("duplicate submission response: %d %q %s", res.StatusCode, res.Header.Get("Content-Type"), page)
	}
}

func TestOAuthExpiredAuthorizationRequestShowsRestartGuidance(t *testing.T) {
	f := newOAuthFixture(t, time.Hour)
	requestID, csrf := "oauth_request_expired", "oauth_csrf_expired"
	verifier := strings.Repeat("e", 64)
	sum := sha256.Sum256([]byte(verifier))
	request := core.OAuthRequest{
		ClientID: f.clientID, RedirectURI: "https://client.example/callback", State: "expired-state",
		CodeChallenge: base64.RawURLEncoding.EncodeToString(sum[:]), Scope: core.ScopeRead, Resource: testOAuthIssuer + "/mcp",
	}
	if err := f.store.CreateOAuthRequest(context.Background(), requestID, csrf, request, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	cookieURL, _ := url.Parse(f.server.URL + "/oauth/authorize")
	f.client.Jar.SetCookies(cookieURL, []*http.Cookie{{Name: oauthCSRFCookieName(requestID), Value: csrf, Path: "/oauth/authorize", Secure: true}})

	res := f.do(t, http.MethodGet, oauthRequestLocation(requestID, "en"), "", "")
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") || !bytes.Contains(page, []byte("Authorization request expired")) || !bytes.Contains(page, []byte("start the connection again")) || bytes.Contains(page, []byte(`"error":"invalid_request"`)) {
		t.Fatalf("expired request response: %d %q %s", res.StatusCode, res.Header.Get("Content-Type"), page)
	}
}

func TestOAuthAuthorizationCSPAllowsOnlyValidatedCallbackOrigin(t *testing.T) {
	f := newOAuthFixture(t, time.Hour)
	requestID, _ := f.startAuthorization(t, "callback-csp", strings.Repeat("k", 64))
	res := f.do(t, http.MethodGet, oauthRequestLocation(requestID, "en"), "", "")
	res.Body.Close()
	if got, want := res.Header.Get("Content-Security-Policy"), "default-src 'none'; style-src 'unsafe-inline'; form-action 'self' https://client.example; frame-ancestors 'none'; base-uri 'none'"; got != want {
		t.Fatalf("authorization CSP: got %q want %q", got, want)
	}

	for _, tc := range []struct {
		redirectURI, want string
	}{
		{"https://client.example:8443/callback?fixed=1", "https://client.example:8443"},
		{"http://127.0.0.1:60474/oauth/callback", "http://127.0.0.1:60474"},
		{"http://[::1]:60474/oauth/callback", "http://[::1]:60474"},
		{"https://*.example/callback", ""},
		{"https://client.example/%25/callback", "https://client.example"},
		{"https://client.example/callback with space", "https://client.example"},
		{"https://client.example;evil/callback", ""},
		{"https://client.example,evil/callback", ""},
	} {
		if got := oauthRedirectCSPSource(tc.redirectURI); got != tc.want {
			t.Errorf("CSP source for %q: got %q want %q", tc.redirectURI, got, tc.want)
		}
	}
}

func TestOAuthPageLanguageNegotiation(t *testing.T) {
	f := newOAuthFixture(t, time.Hour)
	verifier := strings.Repeat("l", 64)
	sum := sha256.Sum256([]byte(verifier))
	base := url.Values{
		"response_type": {"code"}, "client_id": {f.clientID}, "redirect_uri": {"https://client.example/callback"},
		"state": {"language-state"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"}, "resource": {testOAuthIssuer + "/mcp"}, "scope": {core.ScopeRead},
	}
	for _, tc := range []struct {
		header, language, text string
	}{
		{"ms-MY,ms;q=0.9,en;q=0.8", "ms", "Cipta akaun"},
		{"hi-IN,hi;q=0.9,en;q=0.8", "hi", "खाता बनाएँ"},
		{"en-US,en;q=0.9", "en", "Create account"},
	} {
		req, err := http.NewRequest(http.MethodGet, f.server.URL+"/oauth/authorize?"+base.Encode(), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Accept-Language", tc.header)
		res, err := f.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		location, _ := url.Parse(res.Header.Get("Location"))
		if res.StatusCode != http.StatusSeeOther || location.Query().Get("lang") != tc.language {
			t.Fatalf("language negotiation %q: %d %s", tc.header, res.StatusCode, res.Header.Get("Location"))
		}
		res = f.do(t, http.MethodGet, res.Header.Get("Location"), "", "")
		page, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || res.Header.Get("Content-Language") != tc.language || !bytes.Contains(page, []byte(tc.text)) {
			t.Fatalf("language page %s: %d %s", tc.language, res.StatusCode, page)
		}
	}
}

func TestOAuthPageUsesSharedLocaleCookieBeforeBrowserLanguage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize", nil)
	req.AddCookie(&http.Cookie{Name: oauthLocaleCookie, Value: "ms"})
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	if got := oauthLanguage(req); got != "ms" {
		t.Fatalf("shared locale cookie: got %q want ms", got)
	}

	recorder := httptest.NewRecorder()
	setOAuthLanguageCookie(recorder, testOAuthIssuer, "hi")
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != oauthLocaleCookie || cookies[0].Value != "hi" || cookies[0].MaxAge <= 0 || !cookies[0].Secure {
		t.Fatalf("shared locale cookie attributes: %+v", cookies)
	}
}

func (f *oauthFixture) do(t *testing.T, method, path, contentType, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, f.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if method == http.MethodPost && strings.HasPrefix(path, "/oauth/authorize") {
		req.Header.Set("Origin", testOAuthIssuer)
	}
	res, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (f *oauthFixture) startAuthorization(t *testing.T, state, verifier string) (requestID, csrfCookieName string) {
	t.Helper()
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type": {"code"}, "client_id": {f.clientID}, "redirect_uri": {"https://client.example/callback"},
		"state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"}, "resource": {testOAuthIssuer + "/mcp"}, "scope": {core.ScopeRead},
	}
	res := f.do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), "", "")
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("authorization start: %d", res.StatusCode)
	}
	location, _ := url.Parse(res.Header.Get("Location"))
	requestID = location.Query().Get("request_id")
	if requestID == "" {
		t.Fatal("authorization request id missing")
	}
	for _, cookie := range res.Cookies() {
		if strings.HasPrefix(cookie.Name, "edc_oauth_csrf_") {
			csrfCookieName = cookie.Name
			break
		}
	}
	if csrfCookieName == "" {
		t.Fatal("authorization transaction cookie missing")
	}
	return requestID, csrfCookieName
}

func (f *oauthFixture) approveAuthorization(t *testing.T, requestID, state string) string {
	t.Helper()
	form := url.Values{"request_id": {requestID}, "decision": {"approve"}}
	res := f.do(t, http.MethodPost, "/oauth/authorize", "application/x-www-form-urlencoded", form.Encode())
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("approve %s: %d", state, res.StatusCode)
	}
	callback, _ := url.Parse(res.Header.Get("Location"))
	if callback.Query().Get("state") != state || callback.Query().Get("iss") != testOAuthIssuer || callback.Query().Get("code") == "" {
		t.Fatalf("callback binding for %s: %s", state, callback)
	}
	return callback.Query().Get("code")
}

func (f *oauthFixture) exchangeAuthorizationCode(t *testing.T, code, verifier string) {
	t.Helper()
	form := url.Values{
		"grant_type": {"authorization_code"}, "client_id": {f.clientID}, "redirect_uri": {"https://client.example/callback"},
		"code": {code}, "code_verifier": {verifier}, "resource": {testOAuthIssuer + "/mcp"},
	}
	res := f.do(t, http.MethodPost, "/oauth/token", "application/x-www-form-urlencoded", form.Encode())
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("token exchange: %d %s", res.StatusCode, body)
	}
}

func (f *oauthFixture) authorize(t *testing.T, scope string) (accessToken string) {
	t.Helper()
	verifier := strings.Repeat("v", 64)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type": {"code"}, "client_id": {f.clientID}, "redirect_uri": {"https://client.example/callback"},
		"state": {"client-state"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"}, "resource": {testOAuthIssuer + "/mcp"}, "scope": {scope},
	}
	res := f.do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), "", "")
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("authorization start: %d", res.StatusCode)
	}
	requestLocation, _ := url.Parse(res.Header.Get("Location"))
	requestID := requestLocation.Query().Get("request_id")
	if requestID == "" {
		t.Fatal("authorization request id missing")
	}
	res = f.do(t, http.MethodGet, res.Header.Get("Location"), "", "")
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !bytes.Contains(page, []byte("Sign in")) {
		t.Fatalf("login page: %d %s", res.StatusCode, page)
	}
	form := url.Values{"request_id": {requestID}, "decision": {"login"}, "username": {"alice"}, "password": {"integration-password-123"}}
	res = f.do(t, http.MethodPost, "/oauth/authorize", "application/x-www-form-urlencoded", form.Encode())
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", res.StatusCode)
	}
	res = f.do(t, http.MethodGet, res.Header.Get("Location"), "", "")
	page, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !bytes.Contains(page, []byte("Authorize")) || !bytes.Contains(page, []byte(scope)) {
		t.Fatalf("consent page: %d %s", res.StatusCode, page)
	}
	form = url.Values{"request_id": {requestID}, "decision": {"approve"}}
	res = f.do(t, http.MethodPost, "/oauth/authorize", "application/x-www-form-urlencoded", form.Encode())
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("consent: %d", res.StatusCode)
	}
	callback, _ := url.Parse(res.Header.Get("Location"))
	if callback.Query().Get("state") != "client-state" || callback.Query().Get("iss") != testOAuthIssuer {
		t.Fatalf("callback binding missing: %s", callback)
	}
	code := callback.Query().Get("code")
	wrongResource := url.Values{"grant_type": {"authorization_code"}, "client_id": {f.clientID}, "redirect_uri": {"https://client.example/callback"}, "code": {code}, "code_verifier": {verifier}, "resource": {"https://wrong.example/mcp"}}
	res = f.do(t, http.MethodPost, "/oauth/token", "application/x-www-form-urlencoded", wrongResource.Encode())
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong token resource accepted: %d", res.StatusCode)
	}
	wrongVerifier := wrongResource
	wrongVerifier.Set("resource", testOAuthIssuer+"/mcp")
	wrongVerifier.Set("code_verifier", strings.Repeat("x", 64))
	res = f.do(t, http.MethodPost, "/oauth/token", "application/x-www-form-urlencoded", wrongVerifier.Encode())
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong PKCE verifier accepted: %d", res.StatusCode)
	}
	tokenForm := wrongVerifier
	tokenForm.Set("code_verifier", verifier)
	res = f.do(t, http.MethodPost, "/oauth/token", "application/x-www-form-urlencoded", tokenForm.Encode())
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		t.Fatalf("token exchange: %d %s", res.StatusCode, b)
	}
	var token struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
	}
	if err := json.NewDecoder(res.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if token.AccessToken == "" || token.Scope != scope {
		t.Fatalf("token response: %+v", token)
	}
	res = f.do(t, http.MethodPost, "/oauth/token", "application/x-www-form-urlencoded", tokenForm.Encode())
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("authorization code reused: %d", res.StatusCode)
	}
	return token.AccessToken
}

type oauthBearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t oauthBearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

func TestOAuthDiscoveryPKCEAndMCP(t *testing.T) {
	f := newOAuthFixture(t, 2*time.Second)
	for path, expected := range map[string]string{
		"/.well-known/oauth-protected-resource":     `"resource":"` + testOAuthIssuer + `/mcp"`,
		"/.well-known/oauth-protected-resource/mcp": `"authorization_servers":["` + testOAuthIssuer + `"]`,
		"/.well-known/oauth-authorization-server":   `"code_challenge_methods_supported":["S256"]`,
	} {
		res := f.do(t, http.MethodGet, path, "", "")
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || !bytes.Contains(b, []byte(expected)) {
			t.Fatalf("discovery %s: %d %s", path, res.StatusCode, b)
		}
	}
	res := f.do(t, http.MethodPost, "/mcp", "application/json", `{}`)
	res.Body.Close()
	challenge := res.Header.Get("WWW-Authenticate")
	if res.StatusCode != http.StatusUnauthorized || !strings.Contains(challenge, `resource_metadata="`+testOAuthIssuer+`/.well-known/oauth-protected-resource/mcp"`) || !strings.Contains(challenge, `scope="context:read context:write"`) {
		t.Fatalf("MCP challenge: %d %q", res.StatusCode, challenge)
	}

	readToken := f.authorize(t, core.ScopeRead)
	if _, err := f.store.AuthenticateOAuth(context.Background(), readToken, "https://wrong.example/mcp"); err == nil {
		t.Fatal("OAuth access token accepted for wrong audience")
	}
	httpClient := &http.Client{Transport: oauthBearerTransport{base: f.server.Client().Transport, token: readToken}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "oauth-integration", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: f.server.URL + "/mcp", HTTPClient: httpClient}, nil)
	if err != nil {
		t.Fatal("OAuth MCP initialize:", err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 10 {
		t.Fatalf("OAuth tools/list: %d %v", len(tools.Tools), err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_projects", Arguments: core.Empty{}})
	if err != nil || result.IsError {
		t.Fatal("read scope tool failed", err)
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "create_project", Arguments: core.ProjectInput{Name: "not allowed"}})
	if err != nil || !result.IsError {
		t.Fatal("read-only OAuth token used write tool", err)
	}
	time.Sleep(2100 * time.Millisecond)
	res = f.doWithBearer(t, readToken)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired OAuth token accepted: %d", res.StatusCode)
	}
}

func (f *oauthFixture) doWithBearer(t *testing.T, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, f.server.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"expiry","version":"1"}}}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestOAuthRejectsUnsupportedScopeAndResource(t *testing.T) {
	f := newOAuthFixture(t, time.Hour)
	verifier := strings.Repeat("v", 64)
	sum := sha256.Sum256([]byte(verifier))
	base := url.Values{"response_type": {"code"}, "client_id": {f.clientID}, "redirect_uri": {"https://client.example/callback"}, "state": {"s"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "resource": {testOAuthIssuer + "/mcp"}}
	badScope := url.Values{}
	for k, values := range base {
		badScope[k] = append([]string(nil), values...)
	}
	badScope.Set("scope", "admin")
	res := f.do(t, http.MethodGet, "/oauth/authorize?"+badScope.Encode(), "", "")
	res.Body.Close()
	if res.StatusCode != http.StatusFound || !strings.Contains(res.Header.Get("Location"), "error=invalid_scope") {
		t.Fatalf("unsupported scope accepted: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	base.Set("resource", "https://wrong.example/mcp")
	res = f.do(t, http.MethodGet, "/oauth/authorize?"+base.Encode(), "", "")
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong authorization resource accepted: %d", res.StatusCode)
	}
}
