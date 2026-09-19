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

func graphFixture(t *testing.T, body string, inspect func(*http.Request)) *Client {
	t.Helper()
	return NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.Scheme != "https" || r.URL.Host != "graph.microsoft.com" || r.Header.Get("Authorization") != "Bearer graph-token" {
			t.Fatal("unexpected destination or auth")
		}
		if inspect != nil {
			inspect(r)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})})
}

func TestGraphRoutes(t *testing.T) {
	for _, tc := range []struct {
		p, o, path string
		args       map[string]any
	}{
		{"microsoft-calendar", "calendars.list", "/v1.0/me/calendars", nil},
		{"microsoft-calendar", "events.list", "/v1.0/me/calendars/AAMk+abc==/calendarView", map[string]any{"calendar_id": "AAMk+abc==", "time_min": "2026-09-19T08:00:00+08:00", "time_max": "2026-09-20T08:00:00+08:00"}},
		{"microsoft-todo", "tasklists.list", "/v1.0/me/todo/lists", nil},
		{"microsoft-todo", "tasks.list", "/v1.0/me/todo/lists/AAMk=abc/tasks", map[string]any{"tasklist_id": "AAMk=abc"}},
		{"onedrive", "files.list", "/v1.0/me/drive/root/children", nil},
		{"onedrive", "files.list", "/v1.0/me/drive/items/folder!ABC/children", map[string]any{"folder_id": "folder!ABC"}},
		{"onedrive", "files.get", "/v1.0/me/drive/items/file!ABC", map[string]any{"file_id": "file!ABC"}},
		{"outlook-mail", "messages.list", "/v1.0/me/messages", nil},
		{"outlook-mail", "messages.list", "/v1.0/me/mailFolders/inbox/messages", map[string]any{"folder_id": "inbox"}},
		{"outlook-mail", "messages.get", "/v1.0/me/messages/AAMk+abc==", map[string]any{"message_id": "AAMk+abc=="}},
	} {
		t.Run(tc.p+tc.o+tc.path, func(t *testing.T) {
			body := `{"id":"one","unknown":"drop"}`
			list := strings.HasSuffix(tc.o, ".list")
			if list {
				body = `{"value":[{"id":"one","unknown":"drop"}]}`
			}
			c := graphFixture(t, body, func(r *http.Request) {
				if r.URL.Path != tc.path {
					t.Fatalf("path %s", r.URL.Path)
				}
				q := r.URL.Query()
				if q.Get("$select") == "" || strings.Contains(q.Get("$select"), "downloadUrl") {
					t.Fatal("unsafe select")
				}
				if list && q.Get("$top") != "20" {
					t.Fatal("unbounded page")
				}
				if tc.o == "events.list" && (q.Get("startDateTime") != tc.args["time_min"] || q.Get("endDateTime") != tc.args["time_max"] || q.Get("$select") != "id,subject,start,end,isCancelled") {
					t.Fatal("invalid calendar range/projection")
				}
				if tc.p == "outlook-mail" && r.Header.Get("Prefer") != `outlook.body-content-type="text"` {
					t.Fatal("missing text preference")
				}
			})
			result, err := c.executeGraph(context.Background(), tc.p, tc.o, "graph-token", tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(result.Body), "unknown") || result.ContentType != "application/json" {
				t.Fatal("unsafe output")
			}
		})
	}
}

func TestGraphInputRejection(t *testing.T) {
	c := graphFixture(t, `{}`, func(*http.Request) { t.Fatal("invalid input reached network") })
	for _, id := range []string{"..", ".", "a/../b", "a/b", "a%2fb", "a?x=1", "a#x", "https://evil.test", "a\nb", "a\\b", strings.Repeat("a", 1025)} {
		if _, err := c.executeGraph(context.Background(), "onedrive", "files.get", "graph-token", map[string]any{"file_id": id}); err == nil {
			t.Errorf("accepted id %q", id)
		}
	}
	for _, args := range []map[string]any{{"page_url": "https://evil.test"}, {"limit": 101}, {"limit": 0}, {"limit": 1.5}, {"skiptoken": "x"}} {
		if _, err := c.executeGraph(context.Background(), "onedrive", "files.list", "graph-token", args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
	for _, args := range []map[string]any{
		{"time_min": "2026-09-19T00:00:00Z", "time_max": "2026-09-20T00:00:00Z"},
		{"calendar_id": "cal", "time_min": "2026-09-19"},
		{"calendar_id": "cal", "time_min": "2026-09-19", "time_max": "2026-09-20"},
		{"calendar_id": "cal", "time_min": "2026-09-19T00:00:00Z", "time_max": "2026-09-27T00:00:00Z"},
		{"calendar_id": "cal", "time_min": "2026-09-20T00:00:00Z", "time_max": "2026-09-19T00:00:00Z"},
	} {
		if _, err := c.executeGraph(context.Background(), "microsoft-calendar", "events.list", "graph-token", args); err == nil {
			t.Errorf("accepted range %v", args)
		}
	}
	if _, err := c.executeGraph(context.Background(), "outlook-mail", "messages.delete", "graph-token", nil); err == nil {
		t.Fatal("accepted write")
	}
	if _, err := c.executeGraph(context.Background(), "outlook-mail", "messages.get", "graph-token", nil); err == nil {
		t.Fatal("accepted missing ID")
	}
	for _, token := range []string{"", "token\r\nx: secret"} {
		if _, err := c.executeGraph(context.Background(), "onedrive", "files.list", token, nil); err == nil {
			t.Fatal("accepted invalid token")
		}
	}
}

func TestGraphPaginationAndDownloadURLFiltering(t *testing.T) {
	body := `{"@odata.nextLink":"https://evil.test/?token=SECRET","value":[{"id":"file","name":"Budget","@microsoft.graph.downloadUrl":"https://signed.test/SECRET","file":{"mimeType":"text/plain","@microsoft.graph.downloadUrl":"SECRET"},"parentReference":{"id":"parent","secret":"SECRET"}}]}`
	calls := 0
	c := graphFixture(t, body, func(*http.Request) { calls++ })
	result, err := c.executeGraph(context.Background(), "onedrive", "files.list", "graph-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if json.Unmarshal(result.Body, &out) != nil || out["has_more"] != true || out["incomplete"] != true || out["continuation_supported"] != false || calls != 1 {
		t.Fatalf("bad pagination %s", result.Body)
	}
	if strings.Contains(string(result.Body), "SECRET") || strings.Contains(string(result.Body), "downloadUrl") || strings.Contains(string(result.Body), "nextLink") {
		t.Fatal("credential URL leak")
	}
	c = graphFixture(t, `{"id":"file","@microsoft.graph.downloadUrl":"SECRET","file":{"mimeType":"text/plain","downloadUrl":"SECRET"}}`, nil)
	result, err = c.executeGraph(context.Background(), "onedrive", "files.get", "graph-token", map[string]any{"file_id": "file"})
	if err != nil || strings.Contains(string(result.Body), "SECRET") {
		t.Fatal("get URL leak")
	}
}

func TestGraphMalformedAndBoundedResponses(t *testing.T) {
	for _, tc := range []struct{ body, code string }{
		{strings.Repeat("x", graphMaxResponseBytes+1), "response_too_large"},
		{`null`, "invalid_upstream_json"}, {`{"value":null}`, "invalid_upstream_json"}, {`{"value":[null]}`, "invalid_upstream_json"}, {`{"value":[{"name":"no id"}]}`, "invalid_upstream_json"},
	} {
		_, err := graphFixture(t, tc.body, nil).executeGraph(context.Background(), "onedrive", "files.list", "graph-token", nil)
		var ce *Error
		if !errors.As(err, &ce) || ce.Code != tc.code {
			t.Fatalf("wanted %s got %v", tc.code, err)
		}
	}
	c := graphFixture(t, `{"value":[{"id":"1"},{"id":"2"}]}`, nil)
	result, err := c.executeGraph(context.Background(), "onedrive", "files.list", "graph-token", map[string]any{"limit": 1})
	if err != nil || !strings.Contains(string(result.Body), `"incomplete":true`) || strings.Contains(string(result.Body), `"id":"2"`) {
		t.Fatalf("page size not enforced %s %v", result.Body, err)
	}
}

func TestGraphCalendarDropsPrivateDetails(t *testing.T) {
	c := graphFixture(t, `{"value":[{"id":"event","subject":"Standup","start":{"dateTime":"2026-09-19T00:00:00","timeZone":"UTC","url":"SECRET"},"end":{"dateTime":"2026-09-19T00:30:00","timeZone":"UTC"},"isCancelled":false,"attendees":["SECRET"],"body":{"content":"SECRET"},"webLink":"SECRET","onlineMeeting":{"joinUrl":"SECRET"}}]}`, nil)
	result, err := c.executeGraph(context.Background(), "microsoft-calendar", "events.list", "graph-token", map[string]any{"calendar_id": "cal", "time_min": "2026-09-19T00:00:00Z", "time_max": "2026-09-26T00:00:00Z"})
	if err != nil || strings.Contains(string(result.Body), "SECRET") || !strings.Contains(string(result.Body), "Standup") {
		t.Fatalf("calendar projection %s %v", result.Body, err)
	}
}

func TestGraphRefusesRedirectAndSanitizesErrors(t *testing.T) {
	for _, status := range []int{302, 401, 403, 429, 500} {
		calls := 0
		c := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: status, Header: http.Header{"Location": []string{"https://evil.test/SECRET"}}, Body: io.NopCloser(strings.NewReader("SECRET")), Request: r}, nil
		})})
		_, err := c.executeGraph(context.Background(), "onedrive", "files.list", "graph-token", nil)
		if err == nil || strings.Contains(err.Error(), "SECRET") || calls != 1 {
			t.Fatalf("unsafe status %d %v calls %d", status, err, calls)
		}
	}
}
