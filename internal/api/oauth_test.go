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
	if err != nil || len(tools.Tools) != 9 {
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
