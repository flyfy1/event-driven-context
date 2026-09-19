package hubconnectors

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func saasResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestSaaSReadRequests(t *testing.T) {
	tests := []struct {
		p, o, method, host, path string
		args                     map[string]any
		query                    map[string]string
		body                     map[string]any
		response                 string
	}{
		{"todoist", "tasks.list", "GET", "api.todoist.com", "/api/v1/tasks", map[string]any{"project_id": "proj_1", "cursor": "opaque&token", "limit": 7}, map[string]string{"project_id": "proj_1", "cursor": "opaque&token", "limit": "7"}, nil, `{"results":[],"next_cursor":"more"}`},
		{"todoist", "tasks.get", "GET", "api.todoist.com", "/api/v1/tasks/task_1", map[string]any{"task_id": "task_1"}, nil, nil, `{"id":"task_1"}`},
		{"todoist", "projects.list", "GET", "api.todoist.com", "/api/v1/projects", nil, map[string]string{"limit": "20"}, nil, `{"results":[]}`},
		{"notion", "search", "POST", "api.notion.com", "/v1/search", map[string]any{"query": "design", "cursor": "next", "limit": 4}, nil, map[string]any{"query": "design", "start_cursor": "next", "page_size": float64(4)}, `{"results":[],"has_more":true,"next_cursor":"page2"}`},
		{"notion", "pages.get", "GET", "api.notion.com", "/v1/pages/page_1", map[string]any{"page_id": "page_1"}, nil, nil, `{"object":"page","id":"page_1"}`},
		{"notion", "blocks.children", "GET", "api.notion.com", "/v1/blocks/block_1/children", map[string]any{"block_id": "block_1", "cursor": "next"}, map[string]string{"start_cursor": "next", "page_size": "20"}, nil, `{"results":[]}`},
		{"dropbox", "files.list", "POST", "api.dropboxapi.com", "/2/files/list_folder", nil, nil, map[string]any{"path": "", "recursive": false, "limit": float64(20)}, `{"entries":[],"cursor":"opaque","has_more":true}`},
		{"dropbox", "files.continue", "POST", "api.dropboxapi.com", "/2/files/list_folder/continue", map[string]any{"cursor": "opaque"}, nil, map[string]any{"cursor": "opaque"}, `{"entries":[],"has_more":false}`},
		{"dropbox", "files.get", "POST", "api.dropboxapi.com", "/2/files/get_metadata", map[string]any{"path": "/Work/note.md"}, nil, map[string]any{"path": "/Work/note.md"}, `{"name":"note.md"}`},
		{"dropbox", "files.download", "POST", "content.dropboxapi.com", "/2/files/download", map[string]any{"path": "id:abc"}, nil, nil, "file bytes"},
		{"readwise-reader", "documents.list", "GET", "readwise.io", "/api/v3/list/", map[string]any{"cursor": "opaque", "location": "later", "updated_after": "2026-09-01T00:00:00Z"}, map[string]string{"pageCursor": "opaque", "location": "later", "updatedAfter": "2026-09-01T00:00:00Z", "limit": "20"}, nil, `{"results":[],"nextPageCursor":"next"}`},
		{"readwise-reader", "documents.get", "GET", "readwise.io", "/api/v3/list/", map[string]any{"document_id": "doc123"}, map[string]string{"id": "doc123", "withHtmlContent": "true", "limit": "1"}, nil, `{"results":[{"id":"doc123","html_content":"<p>Text</p>"}]}`},
		{"github", "repos.list", "GET", "api.github.com", "/user/repos", map[string]any{"cursor": "2", "limit": 5}, map[string]string{"page": "2", "per_page": "5"}, nil, `[{"name":"repo"}]`},
		{"github", "issues.list", "GET", "api.github.com", "/repos/team/repo/issues", map[string]any{"owner": "team", "repo": "repo", "state": "all"}, map[string]string{"state": "all", "page": "1", "per_page": "20"}, nil, `[{"number":1}]`},
		{"github", "contents.get", "GET", "api.github.com", "/repos/team/repo/contents/docs/file name.md", map[string]any{"owner": "team", "repo": "repo", "path": "docs/file name.md", "ref": "feature/read"}, map[string]string{"ref": "feature/read"}, nil, `{"encoding":"base64","content":"dGV4dA=="}`},
		{"slack", "conversations.list", "GET", "slack.com", "/api/conversations.list", map[string]any{"type": "private_channel", "cursor": "opaque"}, map[string]string{"types": "private_channel", "cursor": "opaque", "exclude_archived": "true", "limit": "20"}, nil, `{"ok":true,"channels":[],"response_metadata":{"next_cursor":"next"}}`},
		{"slack", "conversations.history", "GET", "slack.com", "/api/conversations.history", map[string]any{"channel_id": "C0123", "limit": 100}, map[string]string{"channel": "C0123", "limit": "15"}, nil, `{"ok":true,"messages":[],"has_more":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.p+"/"+tt.o, func(t *testing.T) {
			calls := 0
			client := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != tt.method || r.URL.Scheme != "https" || r.URL.Host != tt.host || r.URL.Path != tt.path {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
				}
				for k, v := range tt.query {
					if r.URL.Query().Get(k) != v {
						t.Errorf("query %s=%q want %q", k, r.URL.Query().Get(k), v)
					}
				}
				if tt.body != nil {
					var body map[string]any
					if r.Body == nil || json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Fatal("missing JSON body")
					}
					for k, v := range tt.body {
						if body[k] != v {
							t.Errorf("body %s=%v want %v", k, body[k], v)
						}
					}
				} else if r.Body != nil {
					t.Fatal("unexpected request body")
				}
				auth := "Bearer private-token"
				if tt.p == "readwise-reader" {
					auth = "Token private-token"
				}
				if r.Header.Get("Authorization") != auth {
					t.Error("wrong auth scheme")
				}
				if tt.p == "notion" && r.Header.Get("Notion-Version") != "2026-03-11" {
					t.Error("missing current Notion version")
				}
				if tt.p == "github" && (r.Header.Get("X-GitHub-Api-Version") != "2026-03-10" || r.Header.Get("User-Agent") == "") {
					t.Error("missing GitHub version/agent")
				}
				if tt.o == "files.download" && r.Header.Get("Dropbox-API-Arg") != `{"path":"id:abc"}` {
					t.Error("missing Dropbox argument header")
				}
				response := saasResponse(tt.response)
				if tt.p == "github" && tt.o != "contents.get" {
					response.Header.Set("Link", "<https://api.github.com"+tt.path+`?page=3>; rel="next"`)
				}
				return response, nil
			})})
			result, err := client.executeSaaS(context.Background(), tt.p, tt.o, "private-token", tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal("unexpected request count")
			}
			if tt.p == "github" && tt.o != "contents.get" {
				var out struct {
					Items      json.RawMessage `json:"items"`
					NextCursor string          `json:"next_cursor"`
				}
				if json.Unmarshal(result.Body, &out) != nil || out.NextCursor != "3" || string(out.Items) != tt.response {
					t.Fatal("lost GitHub pagination")
				}
			} else if string(result.Body) != tt.response {
				t.Fatal("response changed")
			}
			if tt.o == "files.download" && result.ContentType != "application/octet-stream" {
				t.Fatal("unsafe download type")
			}
		})
	}
}

func TestSaaSRejectsUnsafeOrUnsupportedInputs(t *testing.T) {
	client := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("network for invalid input"); return nil, nil })})
	tests := []struct {
		p, o string
		args map[string]any
	}{
		{"todoist", "tasks.get", map[string]any{"task_id": "../delete"}},
		{"todoist", "tasks.list", map[string]any{"limit": 101}},
		{"todoist", "tasks.list", map[string]any{"url": "https://evil.example"}},
		{"todoist", "tasks.delete", map[string]any{"task_id": "1"}},
		{"notion", "pages.get", map[string]any{"page_id": "abc/../x"}},
		{"notion", "search", map[string]any{"cursor": strings.Repeat("x", 4097)}},
		{"dropbox", "files.get", map[string]any{"path": "https://evil.example/file"}},
		{"dropbox", "files.download", map[string]any{"path": "/"}},
		{"dropbox", "files.continue", map[string]any{"cursor": "abc", "limit": 5}},
		{"readwise-reader", "documents.list", map[string]any{"updated_after": "yesterday"}},
		{"readwise-reader", "documents.list", map[string]any{"location": "secret"}},
		{"github", "repos.list", map[string]any{"cursor": "https://evil.example/next"}},
		{"github", "repos.list", map[string]any{"cursor": "0"}},
		{"github", "issues.list", map[string]any{"owner": "..", "repo": "repo"}},
		{"github", "contents.get", map[string]any{"owner": "team", "repo": "repo", "path": "../secret"}},
		{"github", "contents.get", map[string]any{"owner": "team", "repo": "repo", "path": "a//b"}},
		{"slack", "conversations.list", map[string]any{"type": "public_channel,private_channel"}},
		{"slack", "conversations.history", map[string]any{"channel_id": "../admin"}},
	}
	for i, tt := range tests {
		if _, err := client.executeSaaS(context.Background(), tt.p, tt.o, "token", tt.args); err == nil {
			t.Errorf("accepted invalid case %d", i)
		}
	}
}

func TestSaaSErrorRedactionBoundsAndPagination(t *testing.T) {
	for _, scenario := range []string{"status401", "status429", "slack_missing_scope", "slack_revoked", "invalid_json", "oversized", "evil_next_url", "transport_error", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			client := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				res := saasResponse(`{"ok":true}`)
				switch scenario {
				case "status401":
					res.StatusCode = 401
					res.Body = io.NopCloser(strings.NewReader("private-token private-user-data"))
				case "status429":
					res.StatusCode = 429
				case "slack_missing_scope":
					res = saasResponse(`{"ok":false,"error":"missing_scope","needed":"private-user-data"}`)
				case "slack_revoked":
					res = saasResponse(`{"ok":false,"error":"token_revoked"}`)
				case "invalid_json":
					res = saasResponse("invalid")
				case "oversized":
					res = saasResponse(strings.Repeat("x", MaxResponseBytes+1))
				case "evil_next_url":
					res = saasResponse(`[]`)
					res.Header.Set("Link", `<https://evil.example/user/repos?page=2>; rel="next"`)
				case "transport_error":
					return nil, errors.New("private-token private-user-data")
				case "redirect":
					res.StatusCode = 302
					res.Header.Set("Location", "https://evil.example")
				}
				return res, nil
			})})
			provider, operation := "slack", "conversations.list"
			if scenario == "evil_next_url" {
				provider, operation = "github", "repos.list"
			}
			_, err := client.executeSaaS(context.Background(), provider, operation, "private-token", nil)
			if err == nil {
				t.Fatal("expected failure")
			}
			if strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), "http") {
				t.Fatal("sensitive provider detail leaked")
			}
			if calls != 1 {
				t.Fatal("followed redirect or retried")
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatal("untyped adapter failure")
			}
			if scenario == "slack_missing_scope" && e.Code != "provider_permission_denied" {
				t.Fatal("missing scope not distinguished")
			}
			if scenario == "slack_revoked" && e.Code != "reauthorization_required" {
				t.Fatal("revoked token not distinguished")
			}
		})
	}
}

func TestSaaSDropboxUnicodeHeader(t *testing.T) {
	path := "/资料/📖.txt"
	raw, _ := json.Marshal(map[string]string{"path": path})
	escaped := saasASCIIJSON(raw)
	for _, r := range escaped {
		if r > 127 {
			t.Fatal("non-ASCII Dropbox header")
		}
	}
	var decoded map[string]string
	if json.Unmarshal([]byte(escaped), &decoded) != nil || decoded["path"] != path {
		t.Fatal("Unicode path lost in header encoding")
	}
}

func TestSaaSCatalogOnlyAdvertisesReads(t *testing.T) {
	providers := saasProviders()
	if len(providers) != 6 {
		t.Fatal("missing SaaS provider")
	}
	for _, p := range providers {
		if !p.MultipleAccounts || p.AuthMode != "owner_token" || len(p.Operations) == 0 {
			t.Fatal("invalid catalog provider", p.ID)
		}
		for _, op := range p.Operations {
			if !op.ReadOnly || op.InputSchema["additionalProperties"] != false {
				t.Fatal("unsafe operation contract", p.ID, op.ID)
			}
		}
	}
	for _, p := range providers {
		if p.ID == "todoist" && p.Operations[0].ScopeAlternatives[0] != "data:read" {
			t.Fatal("Todoist read scope missing")
		}
		if p.ID == "dropbox" && p.Operations[3].ScopeAlternatives[0] != "files.content.read" {
			t.Fatal("Dropbox download scope missing")
		}
	}
}
