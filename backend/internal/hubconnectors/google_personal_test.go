package hubconnectors

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestGooglePersonalReadsAndCalendarProjection(t *testing.T) {
	tests := []struct {
		p, op, host, path, response string
		args                        map[string]any
	}{
		{"google-calendar", "events.list", "www.googleapis.com", "/calendar/v3/calendars/primary/events", `{"timeZone":"Asia/Singapore","nextPageToken":"secret-pagination","items":[{"id":"inside","summary":"Meeting","description":"do not leak","attendees":[{"email":"private"}],"start":{"dateTime":"2026-09-21T10:00:00+08:00"},"end":{"dateTime":"2026-09-21T11:00:00+08:00"}},{"id":"outside","start":{"dateTime":"2026-09-24T10:00:00+08:00"},"end":{"dateTime":"2026-09-24T11:00:00+08:00"}}]}`, map[string]any{"calendar_id": "primary", "time_min": "2026-09-21T00:00:00+08:00", "time_max": "2026-09-22T00:00:00+08:00"}},
		{"google-calendar", "freebusy.query", "www.googleapis.com", "/calendar/v3/freeBusy", `{"calendars":{"primary":{"busy":[{"start":"2026-09-20T00:00:00Z","end":"2026-09-21T01:00:00Z"}]},"other":{"busy":[{"start":"private"}]}}}`, map[string]any{"calendar_id": "primary", "time_min": "2026-09-21T00:00:00Z", "time_max": "2026-09-22T00:00:00Z"}},
		{"google-calendar", "calendars.list", "www.googleapis.com", "/calendar/v3/users/me/calendarList", `{"items":[{"id":"primary"}],"nextPageToken":"next"}`, nil},
		{"google-tasks", "tasklists.list", "tasks.googleapis.com", "/tasks/v1/users/@me/lists", `{"items":[]}`, nil},
		{"google-tasks", "tasks.list", "tasks.googleapis.com", "/tasks/v1/lists/@default/tasks", `{"items":[]}`, map[string]any{"tasklist_id": "@default"}},
		{"google-contacts", "contacts.list", "people.googleapis.com", "/v1/people/me/connections", `{"connections":[]}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.p+tt.op, func(t *testing.T) {
			client := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != tt.host || r.URL.Path != tt.path || r.Header.Get("Authorization") != "Bearer token" {
					t.Errorf("unexpected route/auth %s", r.URL.Path)
				}
				if tt.op == "events.list" && (r.URL.Query().Get("singleEvents") != "true" || strings.Contains(r.URL.Query().Get("fields"), "description")) {
					t.Error("unsafe event fields")
				}
				if tt.op == "contacts.list" && r.URL.Query().Get("personFields") != "names,emailAddresses,phoneNumbers" {
					t.Error("contact fields not minimized")
				}
				if tt.op == "freebusy.query" {
					var input map[string]any
					json.NewDecoder(r.Body).Decode(&input)
					if r.Method != "POST" || len(input["items"].([]any)) != 1 {
						t.Error("busy request")
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tt.response)), Header: http.Header{}}, nil
			})})
			result, err := client.Execute(context.Background(), tt.p, tt.op, "token", tt.args)
			if err != nil {
				t.Fatal(err)
			}
			out := string(result.Body)
			if tt.op == "events.list" && (strings.Contains(out, "private") || strings.Contains(out, "outside") || strings.Contains(out, "secret-pagination") || !strings.Contains(out, `"incomplete":true`)) {
				t.Fatal("event boundary leaked", out)
			}
			if tt.op == "freebusy.query" && (strings.Contains(out, "other") || strings.Contains(out, "2026-09-20")) {
				t.Fatal("busy boundary leaked", out)
			}
		})
	}
}
func TestGooglePersonalRejectsScopeBypass(t *testing.T) {
	c := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected network"); return nil, nil })})
	for _, args := range []map[string]any{
		{"calendar_id": "../../private", "time_min": "2026-09-21T00:00:00Z", "time_max": "2026-09-22T00:00:00Z"},
		{"calendar_id": "primary", "time_min": "2026-09-21T00:00:00Z", "time_max": "2026-10-22T00:00:00Z"},
		{"calendar_id": "primary", "time_min": "2026-09-21T00:00:00Z", "time_max": "2026-09-22T00:00:00Z", "page_token": "outside-grant"},
		{"calendar_id": "primary", "time_min": "2026-09-21", "time_max": "2026-09-22"},
	} {
		if _, err := c.Execute(context.Background(), "google-calendar", "events.list", "token", args); err == nil {
			t.Fatal("unsafe input accepted")
		}
	}
}
func TestGoogleNewScopesAndVerifiedSubject(t *testing.T) {
	g := GoogleOAuth{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://hub.example/callback"}
	for _, provider := range []string{"google-calendar", "google-tasks", "google-contacts"} {
		link, err := g.AuthorizationURL(strings.Repeat("s", 32), strings.Repeat("v", 43), []string{provider})
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(link)
		scopes := u.Query().Get("scope")
		if !strings.Contains(scopes, "openid") || strings.Contains(scopes, "gmail") || strings.Contains(scopes, "drive") {
			t.Fatal("overbroad identity scope", scopes)
		}
		c := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host != "openidconnect.googleapis.com" || r.URL.Path != "/v1/userinfo" {
				t.Fatal("wrong identity endpoint")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"sub":"stable-subject","email":"mutable-label@example.invalid"}`))}, nil
		})})
		id, err := c.GoogleAccountID(context.Background(), provider, "token")
		if err != nil || id != "stable-subject" {
			t.Fatal("unverified subject", id, err)
		}
	}
}
