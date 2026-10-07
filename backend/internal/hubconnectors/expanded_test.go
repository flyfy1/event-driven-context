package hubconnectors

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// These fixtures check the documented wire contracts, without provider accounts.
func TestExpandedReadContracts(t *testing.T) {
	tests := []struct {
		p, o, host, path string
		args             map[string]any
		query            map[string]string
	}{
		{"asana", "workspaces.list", "app.asana.com", "/api/1.0/workspaces", nil, map[string]string{"limit": "20"}},
		{"asana", "projects.list", "app.asana.com", "/api/1.0/workspaces/123/projects", map[string]any{"workspace_id": "123"}, nil},
		{"asana", "tasks.list", "app.asana.com", "/api/1.0/projects/123/tasks", map[string]any{"project_id": "123", "cursor": "opaque&next"}, map[string]string{"offset": "opaque&next"}},
		{"asana", "tasks.get", "app.asana.com", "/api/1.0/tasks/123", map[string]any{"task_id": "123"}, nil},
		{"airtable", "bases.list", "api.airtable.com", "/v0/meta/bases", nil, nil},
		{"airtable", "records.list", "api.airtable.com", "/v0/app123/tbl123", map[string]any{"base_id": "app123", "table_id": "tbl123", "limit": 7}, map[string]string{"pageSize": "7"}},
		{"linear", "identity.get", "api.linear.app", "/graphql", nil, nil},
		{"linear", "teams.list", "api.linear.app", "/graphql", map[string]any{"cursor": "opaque&next", "limit": 7}, nil},
		{"linear", "issues.list", "api.linear.app", "/graphql", map[string]any{"team_id": "team-123"}, nil},
		{"gitlab", "projects.list", "gitlab.com", "/api/v4/projects", nil, map[string]string{"membership": "true", "page": "1"}},
		{"gitlab", "issues.list", "gitlab.com", "/api/v4/projects/123/issues", map[string]any{"project_id": "123", "cursor": "2"}, map[string]string{"page": "2"}},
		{"box", "files.list", "api.box.com", "/2.0/folders/0/items", map[string]any{"folder_id": "0", "cursor": "next"}, map[string]string{"usemarker": "true", "marker": "next"}},
		{"box", "files.get", "api.box.com", "/2.0/files/123", map[string]any{"file_id": "123"}, nil},
		{"discord-bot", "identity.get", "discord.com", "/api/v10/users/@me", nil, nil},
		{"discord-bot", "channels.list", "discord.com", "/api/v10/guilds/123/channels", map[string]any{"guild_id": "123"}, nil},
		{"discord-bot", "messages.list", "discord.com", "/api/v10/channels/123/messages", map[string]any{"channel_id": "123", "cursor": "456"}, map[string]string{"before": "456"}},
		{"feishu", "chats.list", "open.feishu.cn", "/open-apis/im/v1/chats", map[string]any{"limit": 100}, map[string]string{"page_size": "50"}},
		{"feishu", "messages.list", "open.feishu.cn", "/open-apis/im/v1/messages", map[string]any{"chat_id": "oc_123"}, map[string]string{"container_id_type": "chat", "container_id": "oc_123"}},
		{"lark", "chats.list", "open.larksuite.com", "/open-apis/im/v1/chats", nil, nil},
		{"lark", "messages.list", "open.larksuite.com", "/open-apis/im/v1/messages", map[string]any{"chat_id": "oc_123"}, nil},
		{"google-docs", "documents.get", "docs.googleapis.com", "/v1/documents/doc123", map[string]any{"document_id": "doc123"}, map[string]string{"includeTabsContent": "true"}},
		{"google-sheets", "spreadsheets.get", "sheets.googleapis.com", "/v4/spreadsheets/sheet123", map[string]any{"spreadsheet_id": "sheet123"}, nil},
		{"google-sheets", "values.get", "sheets.googleapis.com", "/v4/spreadsheets/sheet123/values/'Work sheet'!A1:C7", map[string]any{"spreadsheet_id": "sheet123", "range": "'Work sheet'!A1:C7"}, nil},
		{"google-chat", "spaces.list", "chat.googleapis.com", "/v1/spaces", nil, nil},
		{"google-chat", "messages.list", "chat.googleapis.com", "/v1/spaces/AAA123/messages", map[string]any{"space_id": "AAA123", "cursor": "next"}, map[string]string{"pageToken": "next"}},
		{"microsoft-contacts", "contacts.list", "graph.microsoft.com", "/v1.0/me/contacts", nil, map[string]string{"$top": "20"}},
		{"microsoft-onenote", "notebooks.list", "graph.microsoft.com", "/v1.0/me/onenote/notebooks", nil, nil},
		{"microsoft-onenote", "pages.list", "graph.microsoft.com", "/v1.0/me/onenote/pages", nil, nil},
		{"microsoft-onenote", "pages.content", "graph.microsoft.com", "/v1.0/me/onenote/pages/page!123/content", map[string]any{"page_id": "page!123"}, nil},
		{"microsoft-teams", "teams.list", "graph.microsoft.com", "/v1.0/me/joinedTeams", nil, nil},
		{"microsoft-teams", "channels.list", "graph.microsoft.com", "/v1.0/teams/team123/channels", map[string]any{"team_id": "team123"}, nil},
		{"microsoft-teams", "messages.list", "graph.microsoft.com", "/v1.0/teams/team123/channels/19:abc@thread.tacv2/messages", map[string]any{"team_id": "team123", "channel_id": "19:abc@thread.tacv2", "limit": 100}, map[string]string{"$top": "50"}},
		{"gmail", "attachments.get", "gmail.googleapis.com", "/gmail/v1/users/me/messages/mail123/attachments/attachment123", map[string]any{"message_id": "mail123", "attachment_id": "attachment123"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.p+"/"+tt.o, func(t *testing.T) {
			calls := 0
			client := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				method := "GET"
				if tt.p == "linear" {
					method = "POST"
				}
				if r.Method != method || r.URL.Scheme != "https" || r.URL.Host != tt.host || r.URL.Path != tt.path {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL)
				}
				for k, v := range tt.query {
					if r.URL.Query().Get(k) != v {
						t.Errorf("query %s=%q want %q", k, r.URL.Query().Get(k), v)
					}
				}
				auth := "Bearer fixture-token"
				if tt.p == "linear" {
					auth = "fixture-token"
				}
				if tt.p == "discord-bot" {
					auth = "Bot fixture-token"
				}
				if tt.p == "gitlab" {
					auth = ""
					if r.Header.Get("PRIVATE-TOKEN") != "fixture-token" {
						t.Fatal("GitLab auth")
					}
				}
				if r.Header.Get("Authorization") != auth || strings.Contains(r.URL.String(), "fixture-token") {
					t.Fatal("credential contract")
				}
				if tt.p == "linear" {
					var b struct {
						Query     string
						Variables map[string]any
					}
					if json.NewDecoder(r.Body).Decode(&b) != nil || !strings.HasPrefix(b.Query, "query ") || strings.Contains(b.Query, "mutation") {
						t.Fatal("read GraphQL contract")
					}
					if tt.o == "issues.list" && b.Variables["team"] != "team-123" {
						t.Fatal("team filter")
					}
					if tt.o == "teams.list" && b.Variables["after"] != "opaque&next" {
						t.Fatal("cursor")
					}
				}
				res := saasResponse(`{"data":{},"code":0}`)
				if tt.p == "gitlab" {
					res = saasResponse(`[]`)
					res.Header.Set("X-Next-Page", "3")
				}
				if tt.o == "pages.content" {
					res = saasResponse(`<p>Untrusted note</p>`)
				}
				return res, nil
			})})
			res, err := client.Execute(context.Background(), tt.p, tt.o, "fixture-token", tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal("request count")
			}
			if tt.p == "gitlab" && !strings.Contains(string(res.Body), `"next_cursor":"3"`) {
				t.Fatal("pagination lost")
			}
			if tt.o == "pages.content" && res.ContentType != "text/html" {
				t.Fatal("HTML content contract")
			}
		})
	}
}
func TestExpandedInputAndUpstreamFailures(t *testing.T) {
	client := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("invalid input reached network"); return nil, nil })})
	for _, tt := range []struct {
		p, o string
		args map[string]any
	}{
		{"asana", "tasks.get", map[string]any{"task_id": "../secret"}},
		{"asana", "tasks.get", map[string]any{"task_id": "123", "url": "https://evil.test"}},
		{"discord-bot", "messages.list", map[string]any{"channel_id": "text"}},
		{"gitlab", "projects.list", map[string]any{"cursor": "https://evil.test"}},
		{"google-sheets", "values.get", map[string]any{"spreadsheet_id": "x/../y", "range": "A1"}},
		{"linear", "issues.delete", map[string]any{}},
	} {
		if _, err := client.Execute(context.Background(), tt.p, tt.o, "fixture-token", tt.args); err == nil {
			t.Fatal("invalid accepted", tt.p, tt.o)
		}
	}
	for _, tt := range []struct{ p, o, body string }{
		{"linear", "teams.list", `{"errors":[{"message":"private token"}]}`},
		{"feishu", "chats.list", `{"code":99991663,"msg":"private token"}`},
		{"microsoft-contacts", "contacts.list", `{"value":[],"@odata.nextLink":"https://evil.test/v1.0/me/contacts"}`},
	} {
		c := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return saasResponse(tt.body), nil })})
		_, err := c.Execute(context.Background(), tt.p, tt.o, "fixture-token", nil)
		if err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal("unsafe upstream failure", err)
		}
	}
	c := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return saasResponse(`{"value":[],"@odata.nextLink":"https://graph.microsoft.com/v1.0/me/contacts?$skiptoken=opaque%26next"}`), nil
	})})
	res, err := c.Execute(context.Background(), "microsoft-contacts", "contacts.list", "fixture-token", nil)
	if err != nil || !strings.Contains(string(res.Body), `"incomplete":true`) || !strings.Contains(string(res.Body), `"next_cursor":"opaque\u0026next"`) || strings.Contains(string(res.Body), "@odata.nextLink") {
		t.Fatal("bounded pagination", string(res.Body), err)
	}
}
