package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"event-driven-context/internal/core"
)

func authRequest(h http.Handler, peer, username string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(fmt.Sprintf(`{"username":%q,"password":"wrong"}`, username)))
	r.RemoteAddr = peer
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAuthGateIPIsolation(t *testing.T) {
	for name, h := range map[string]http.Handler{
		"v1": HandlerWithConfig(nil, Config{AllowRegistration: true}),
		"v2": V2HandlerWithConfig(nil, nil, Config{AllowRegistration: true}),
	} {
		t.Run(name, func(t *testing.T) {
			// Invalid JSON avoids calling the store but still spends IP tokens.
			request := func(ip, path string) int {
				r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{"))
				r.RemoteAddr = ip
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w.Code
			}
			for i := 0; i < authBurst; i++ {
				if got := request(fmt.Sprintf("192.0.2.1:%d", 1000+i), "/v1/auth/login"); got != 400 {
					t.Fatalf("attempt %d: %d", i, got)
				}
			}
			if got := request("192.0.2.1:9000", "/v1/auth/register"); got != 429 {
				t.Fatalf("same IP register: %d", got)
			}
			if got := request("192.0.2.2:9000", "/v1/auth/login"); got != 400 {
				t.Fatalf("other IP login: %d", got)
			}
			if name == "v2" {
				for i := 0; i < authBurst; i++ {
					if got := request("192.0.2.3:9000", "/v1/hub/agents"); got != 400 {
						t.Fatalf("hub attempt %d: %d", i, got)
					}
				}
				if got := request("192.0.2.3:9000", "/v1/hub/agents"); got != 429 {
					t.Fatalf("same IP hub: %d", got)
				}
				if got := request("192.0.2.4:9000", "/v1/hub/agents"); got != 400 {
					t.Fatalf("other IP hub: %d", got)
				}
			}
		})
	}
}

func TestAuthGateUsernameAcrossIPs(t *testing.T) {
	calls := 0
	h := newAuthGate(nil).login(func(_ context.Context, in core.Credentials) (core.LoginResult, error) {
		calls++
		return core.LoginResult{}, &core.Error{Code: "unauthenticated", Message: "invalid credentials"}
	})
	for i := 0; i < authBurst; i++ {
		name := "alice"
		if i%2 == 0 {
			name = " ALICE "
		}
		if w := authRequest(h, fmt.Sprintf("192.0.2.%d:9000", i+1), name); w.Code != 401 {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
	if w := authRequest(h, "198.51.100.1:9000", "Alice"); w.Code != 429 || w.Header().Get("Retry-After") != "2" {
		t.Fatalf("username limit: %d", w.Code)
	}
	if calls != authBurst {
		t.Fatalf("limited request reached login: %d calls", calls)
	}
	if w := authRequest(h, "198.51.100.2:9000", "bob"); w.Code != 401 {
		t.Fatalf("other username: %d", w.Code)
	}
}

func TestAuthGateClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	for _, tt := range []struct {
		name, peer, forwarded, real, want string
		trust                             bool
	}{
		{"default", "127.0.0.1:90", "192.0.2.1", "192.0.2.2", "127.0.0.1", false},
		{"untrusted", "198.51.100.1:90", "192.0.2.1", "192.0.2.2", "198.51.100.1", true},
		{"chain", "127.0.0.1:90", "192.0.2.99, 198.51.100.1, 10.1.2.3", "192.0.2.2", "198.51.100.1", true},
		{"real fallback", "127.0.0.1:90", "", "192.0.2.2", "192.0.2.2", true},
		{"malformed", "127.0.0.1:90", "192.0.2.1, garbage", "192.0.2.2", "127.0.0.1", true},
		{"ipv6", "[::1]:90", "2001:db8::1", "", "2001:db8::1", true},
		{"mapped ipv4", "[::ffff:192.0.2.1]:90", "198.51.100.1", "", "192.0.2.1", true},
		{"no headers", "127.0.0.1:90", "", "", "127.0.0.1", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := newAuthGate(nil)
			if tt.trust {
				g.trustedProxies = trusted
			}
			r := httptest.NewRequest("POST", "/", nil)
			r.RemoteAddr = tt.peer
			if tt.forwarded != "" {
				r.Header.Set("X-Forwarded-For", tt.forwarded)
			}
			if tt.real != "" {
				r.Header.Set("X-Real-IP", tt.real)
			}
			if got := g.clientIP(r).String(); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestAuthGateSpoofedForwardingCannotBypassLimit(t *testing.T) {
	g := newAuthGate([]netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")})
	h := g.wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	for i := 0; i <= authBurst; i++ {
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = "192.0.2.1:9000"
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1))
		r.Header.Set("X-Real-IP", fmt.Sprintf("198.51.100.%d", i+1))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 204
		if i == authBurst {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("attempt %d: %d, want %d", i, w.Code, want)
		}
	}
}

func TestAuthGateRefillAndEviction(t *testing.T) {
	g := newAuthGate(nil)
	now := time.Now()
	for i := 0; i < authBurst; i++ {
		if !g.allow("hot", now) {
			t.Fatal("early limit")
		}
	}
	if g.allow("hot", now.Add(time.Second)) {
		t.Fatal("early refill")
	}
	if !g.allow("hot", now.Add(2*time.Second)) {
		t.Fatal("missing refill")
	}
	now = now.Add(3 * time.Second)
	for i := 0; i < authMaxKeys; i++ {
		g.allow(fmt.Sprint(i), now)
	}
	if len(g.buckets) != authMaxKeys || g.recent.Len() != authMaxKeys {
		t.Fatal("unbounded keys")
	}
	if _, ok := g.buckets["hot"]; ok {
		t.Fatal("oldest key not evicted")
	}
	g.allow("fresh", now.Add(authIdleTTL))
	if len(g.buckets) != 1 || g.recent.Len() != 1 {
		t.Fatal("idle keys not evicted")
	}
}

func TestAuthGateGlobalConcurrency(t *testing.T) {
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	done := make(chan struct{}, 4)
	defer func() {
		close(release)
		for i := 0; i < 4; i++ {
			<-done
		}
	}()
	for i := 0; i < 4; i++ {
		h := newAuthGate(nil).wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			entered <- struct{}{}
			<-release
		}))
		go func() { authRequest(h, "192.0.2.1:90", "alice"); done <- struct{}{} }()
	}
	for i := 0; i < 4; i++ {
		<-entered
	}
	h := newAuthGate(nil).wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("concurrency cap bypassed") }))
	if w := authRequest(h, "192.0.2.2:90", "bob"); w.Code != 429 {
		t.Fatalf("busy status: %d", w.Code)
	}
}
