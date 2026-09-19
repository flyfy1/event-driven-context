package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"event-driven-context/internal/core"
	"event-driven-context/internal/hubconnectors"
)

type hubGoogleTransport func(*http.Request) (*http.Response, error)

func (f hubGoogleTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func googleTestResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestHubGoogleOwnerFlowAndCallbackBinding(t *testing.T) {
	f := newV2APIFixture(t)
	key := bytes.Repeat([]byte{51}, 32)
	calls := 0
	g := hubconnectors.GoogleOAuth{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://hub.example/v1/hub/google/callback", HTTPClient: &http.Client{Transport: hubGoogleTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		switch r.URL.Host + r.URL.Path {
		case "oauth2.googleapis.com/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("code_verifier") == "" || r.Form.Get("code") != "owner-code" {
				t.Fatal("missing PKCE")
			}
			return googleTestResponse(`{"access_token":"private-access","refresh_token":"private-refresh","expires_in":3600,"token_type":"Bearer","scope":"https://www.googleapis.com/auth/gmail.readonly"}`), nil
		case "gmail.googleapis.com/gmail/v1/users/me/profile":
			return googleTestResponse(`{"emailAddress":"verified@example.invalid"}`), nil
		default:
			t.Fatal("unexpected provider request")
			return nil, nil
		}
	})}}
	mux := http.NewServeMux()
	registerHubGoogleWithConfig(mux, f.store, g, key, "https://frontend.example/hub.html")
	start := func(token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/hub/google/start", strings.NewReader(`{"provider_id":"gmail","display_name":"Work Gmail"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if got := start(""); got.Code != 401 {
		t.Fatal("anonymous OAuth start accepted", got.Code)
	}
	w := start(f.token)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		AuthorizationURL string `json:"authorization_url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(out.AuthorizationURL)
	state := u.Query().Get("state")
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatal("missing callback browser binding")
	}
	callback := func(cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/hub/google/callback?state="+url.QueryEscape(state)+"&code=owner-code", nil)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if got := callback(nil); got.Code != 400 || calls != 0 {
		t.Fatal("callback accepted without originating browser")
	}
	result := callback(cookies[0])
	if result.Code != 200 {
		t.Fatal(result.Code, result.Body.String())
	}
	if strings.Contains(result.Body.String(), "private-") {
		t.Fatal("callback leaked token")
	}
	if !strings.Contains(result.Body.String(), `href="https://frontend.example/hub.html"`) {
		t.Fatal("callback link did not use configured frontend")
	}
	if result.Header().Get("Referrer-Policy") != "no-referrer" || result.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("callback leaks URL or caches")
	}
	connections, err := f.store.HubConnections(context.Background(), f.alice.ID)
	if err != nil || len(connections) != 1 || connections[0].AccountID != "verified@example.invalid" {
		t.Fatal("verified account not persisted", err)
	}
	others, err := f.store.HubConnections(context.Background(), f.bob.ID)
	if err != nil || len(others) != 0 {
		t.Fatal("wrong owner account")
	}
	if got := callback(cookies[0]); got.Code != 400 || calls != 2 {
		t.Fatal("callback replay performed provider call")
	}
}

func TestHubGoogleRefreshPreservesTokenAndDisconnect(t *testing.T) {
	f := newV2APIFixture(t)
	key := bytes.Repeat([]byte{52}, 32)
	owner := core.WithUser(context.Background(), f.alice.ID)
	credential := hubGoogleCredential{Kind: "google_oauth", Token: hubconnectors.GoogleToken{AccessToken: "expired", RefreshToken: "keep-refresh", Scope: "https://www.googleapis.com/auth/gmail.readonly", TokenType: "Bearer", ExpiresIn: 3600}, ExpiresAt: time.Now().Add(-time.Hour).Unix()}
	b, _ := json.Marshal(credential)
	c, err := f.store.SaveHubGoogleConnection(owner, core.HubConnection{ProviderID: "gmail", AccountID: "account@example.invalid", DisplayName: "Work"}, string(b), key)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	g := hubconnectors.GoogleOAuth{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://hub.example/callback", HTTPClient: &http.Client{Transport: hubGoogleTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("refresh_token") != "keep-refresh" {
			t.Fatal("lost refresh token")
		}
		return googleTestResponse(`{"access_token":"fresh","expires_in":3600,"token_type":"Bearer"}`), nil
	})}}
	access, err := hubProviderCredentialWithConfig(owner, f.store, c, key, g)
	if err != nil || access != "fresh" || calls != 1 {
		t.Fatal("refresh failed", err)
	}
	secret, err := f.store.HubCredential(owner, c, key)
	if err != nil {
		t.Fatal(err)
	}
	var stored hubGoogleCredential
	if json.Unmarshal([]byte(secret), &stored) != nil || stored.Token.RefreshToken != "keep-refresh" {
		t.Fatal("refresh token not preserved")
	}
	if _, err = hubProviderCredentialWithConfig(owner, f.store, c, key, g); err != nil || calls != 1 {
		t.Fatal("unnecessary refresh", err)
	}
	if err = f.store.DisconnectHubConnection(owner, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = hubProviderCredentialWithConfig(owner, f.store, c, key, g); err == nil || calls != 1 {
		t.Fatal("disconnected credential refreshed")
	}
}
