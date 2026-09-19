package hubconnectors

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Rewrite only in tests; public constructors do not accept endpoint overrides.
func serverClient(t *testing.T, handler http.HandlerFunc, inspect func(*http.Request)) *http.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	base, _ := url.Parse(srv.URL)
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if inspect != nil {
			inspect(r)
		}
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = base.Scheme
		u.Host = base.Host
		clone.URL = &u
		return http.DefaultTransport.RoundTrip(clone)
	})}
}

func TestProviderReads(t *testing.T) {
	tests := []struct {
		provider, operation, path, host string
		args                            map[string]any
		response, contentType           string
	}{
		{"google-drive", "files.list", "/drive/v3/files", "www.googleapis.com", map[string]any{"query": "name contains 'Budget'", "page_token": "next?&", "limit": float64(7)}, `{"files":[{"id":"file_1"}],"nextPageToken":"next-page"}`, "application/json"},
		{"google-drive", "files.get", "/drive/v3/files/file_1", "www.googleapis.com", map[string]any{"file_id": "file_1"}, `{"id":"file_1","name":"Budget"}`, "application/json"},
		{"google-drive", "files.export", "/drive/v3/files/file_1/export", "www.googleapis.com", map[string]any{"file_id": "file_1", "mime_type": "text/plain"}, "Budget content", "text/plain"},
		{"gmail", "messages.list", "/gmail/v1/users/me/messages", "gmail.googleapis.com", map[string]any{"query": "label:work", "page_token": "page2", "limit": json.Number("8")}, `{"messages":[{"id":"message1"}],"nextPageToken":"page3"}`, "application/json"},
		{"gmail", "messages.get", "/gmail/v1/users/me/messages/message1", "gmail.googleapis.com", map[string]any{"message_id": "message1"}, `{"id":"message1","payload":{"body":{"data":"aGk="}}}`, "application/json"},
		{"telegram-bot", "identity.get", "/bot12345:test-token/getMe", "api.telegram.org", nil, `{"ok":true,"result":{"id":12345}}`, "application/json"},
		{"telegram-bot", "updates.peek", "/bot12345:test-token/getUpdates", "api.telegram.org", map[string]any{"limit": 3}, `{"ok":true,"result":[{"update_id":42}]}`, "application/json"},
	}
	for _, tt := range tests {
		t.Run(tt.provider+"/"+tt.operation, func(t *testing.T) {
			client := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != tt.path {
					t.Errorf("request %s %s", r.Method, r.URL.Path)
				}
				q := r.URL.Query()
				if tt.provider == "telegram-bot" {
					if r.Header.Get("Authorization") != "" {
						t.Error("unexpected authorization header")
					}
					if tt.operation == "updates.peek" && (q.Get("timeout") != "0" || q.Get("limit") != "3" || q.Has("offset") || q.Has("allowed_updates")) {
						t.Errorf("unsafe Telegram query: %v", q)
					}
				} else if r.Header.Get("Authorization") != "Bearer access-token" {
					t.Error("missing bearer credential")
				}
				if tt.operation == "files.list" && (q.Get("q") != "name contains 'Budget'" || q.Get("pageToken") != "next?&" || q.Get("pageSize") != "7" || !strings.Contains(q.Get("fields"), "incompleteSearch")) {
					t.Errorf("bad Drive query: %v", q)
				}
				if tt.operation == "messages.list" && (q.Get("q") != "label:work" || q.Get("pageToken") != "page2" || q.Get("maxResults") != "8") {
					t.Errorf("bad Gmail query: %v", q)
				}
				if tt.operation == "messages.get" && q.Get("format") != "full" {
					t.Error("missing full message format")
				}
				if tt.operation == "files.export" && q.Get("mimeType") != "text/plain" {
					t.Error("missing export MIME")
				}
				_, _ = io.WriteString(w, tt.response)
			}, func(r *http.Request) {
				if r.URL.Scheme != "https" || r.URL.Host != tt.host {
					t.Errorf("unexpected origin %s", r.URL.Host)
				}
			})
			credential := "access-token"
			if tt.provider == "telegram-bot" {
				credential = "12345:test-token"
			}
			got, err := NewClient(client).Execute(context.Background(), tt.provider, tt.operation, credential, tt.args)
			if err != nil || string(got.Body) != tt.response || got.ContentType != tt.contentType {
				t.Fatalf("result=%v err=%v", got, err)
			}
		})
	}
}

func TestRejectUnsafeArgumentsBeforeNetwork(t *testing.T) {
	client := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("network called for invalid input")
		return nil, nil
	})})
	tests := []struct {
		p, o, c string
		a       map[string]any
	}{
		{"google-drive", "files.get", "token", map[string]any{"file_id": "../secrets"}},
		{"google-drive", "files.get", "token", map[string]any{"file_id": "https://evil.example"}},
		{"google-drive", "files.get", "token", map[string]any{}},
		{"google-drive", "files.list", "token", map[string]any{"url": "https://evil.example"}},
		{"google-drive", "files.list", "token", map[string]any{"limit": 101}},
		{"google-drive", "files.list", "token", map[string]any{"limit": 1.5}},
		{"google-drive", "files.list", "token", map[string]any{"query": strings.Repeat("x", 4097)}},
		{"google-drive", "files.export", "token", map[string]any{"file_id": "valid", "mime_type": "text/html"}},
		{"telegram-bot", "updates.peek", "123:token", map[string]any{"offset": -1}},
		{"telegram-bot", "updates.peek", "123:token", map[string]any{"allowed_updates": []string{"message"}}},
		{"telegram-bot", "identity.get", "123:token/../../other", nil},
		{"gmail", "messages.list", "token\r\nInjected: yes", nil},
		{"gmail", "messages.delete", "token", map[string]any{"message_id": "x"}},
		{"whatsapp-business", "messages.list", "token", nil},
	}
	for i, tt := range tests {
		if _, err := client.Execute(context.Background(), tt.p, tt.o, tt.c, tt.a); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestRedirectBodyBoundsAndSanitizedFailures(t *testing.T) {
	for _, scenario := range []string{"redirect", "unauthorized", "too_large", "invalid_json", "telegram_error", "transport_error"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			client := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch scenario {
				case "redirect":
					w.Header().Set("Location", "https://attacker.example/stolen")
					w.WriteHeader(http.StatusFound)
				case "unauthorized":
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = io.WriteString(w, `{"error":"secret-user-body"}`)
				case "too_large":
					_, _ = io.WriteString(w, strings.Repeat(" ", MaxResponseBytes+1))
				case "invalid_json":
					_, _ = io.WriteString(w, "bad")
				case "telegram_error":
					_, _ = io.WriteString(w, `{"ok":false,"description":"secret-user-body"}`)
				}
			}, nil)
			if scenario == "transport_error" {
				client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) { return nil, errors.New("leak " + r.URL.String()) })
			}
			_, err := NewClient(client).Execute(context.Background(), "telegram-bot", "identity.get", "12345:test-token", nil)
			if err == nil {
				t.Fatal("expected error")
			}
			if strings.Contains(err.Error(), "test-token") || strings.Contains(err.Error(), "secret-user-body") || strings.Contains(err.Error(), "http") {
				t.Fatalf("sensitive error: %s", err)
			}
			if scenario == "redirect" && calls != 1 {
				t.Fatalf("followed redirect: %d requests", calls)
			}
		})
	}
}

func TestConnectionAndCatalogAreCredentialFree(t *testing.T) {
	for _, id := range []string{"conn_work", "conn_personal"} {
		c := Connection{ID: id, OwnerID: "owner", ProviderID: "gmail", AccountID: id + "_account", DisplayName: id, Status: "configured"}
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if err := (Connection{ID: "a", OwnerID: "", ProviderID: "gmail", AccountID: "b", DisplayName: "x", Status: "configured"}).Validate(); err == nil {
		t.Fatal("missing owner accepted")
	}
	p, _ := Lookup("gmail")
	p.Operations[0].InputSchema["polluted"] = true
	p2, _ := Lookup("gmail")
	if _, ok := p2.Operations[0].InputSchema["polluted"]; ok {
		t.Fatal("catalog mutation persisted")
	}
	for _, p := range Catalog() {
		if !p.MultipleAccounts {
			t.Fatalf("%s lacks multiple account contract", p.ID)
		}
		if p.ImplementationStatus == "not_implemented" && len(p.Operations) > 0 {
			t.Fatalf("unimplemented provider advertises executable operations: %s", p.ID)
		}
	}
}
