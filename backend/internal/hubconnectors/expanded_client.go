package hubconnectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var expandedID = regexp.MustCompile(`^[A-Za-z0-9_+=!@:.\-]{1,256}$`)
var decimalID = regexp.MustCompile(`^[0-9]{1,30}$`)

func (c *Client) executeExpanded(ctx context.Context, provider, operation, token string, args map[string]any) (Result, error) {
	host, ok := expandedHosts[provider]
	if !ok {
		return Result{}, &Error{Code: "provider_unavailable"}
	}
	value := func(k string) string { s, _ := args[k].(string); return s }
	for _, k := range []string{"workspace_id", "project_id", "task_id", "base_id", "table_id", "team_id", "folder_id", "file_id", "guild_id", "channel_id", "chat_id", "document_id", "spreadsheet_id", "space_id", "page_id"} {
		if id := value(k); id != "" && (!expandedID.MatchString(id) || strings.Contains(id, "..")) {
			return Result{}, &Error{Code: "invalid_resource_id"}
		}
	}
	limit := integerArg(args, "limit", 20)
	cursor := value("cursor")
	q := url.Values{}
	headers := http.Header{"Authorization": {"Bearer " + token}}
	path := ""
	method := http.MethodGet
	contentType := "application/json"
	var body any
	switch provider {
	case "asana":
		path = "/api/1.0/"
		switch operation {
		case "workspaces.list":
			path += "workspaces"
		case "projects.list":
			path += "workspaces/" + value("workspace_id") + "/projects"
		case "tasks.list":
			path += "projects/" + value("project_id") + "/tasks"
		case "tasks.get":
			path += "tasks/" + value("task_id")
		}
		if operation != "tasks.get" {
			q.Set("limit", limit)
			if cursor != "" {
				q.Set("offset", cursor)
			}
		}
	case "airtable":
		if operation == "bases.list" {
			path = "/v0/meta/bases"
		} else {
			path = "/v0/" + value("base_id") + "/" + value("table_id")
			q.Set("pageSize", limit)
		}
		if cursor != "" {
			q.Set("offset", cursor)
		}
	case "linear":
		path = "/graphql"
		method = http.MethodPost
		headers.Set("Authorization", token)
		query := "query { viewer { id name email } }"
		vars := map[string]any{}
		if operation != "identity.get" {
			vars["first"], _ = strconv.Atoi(limit)
			vars["after"] = nil
			if cursor != "" {
				vars["after"] = cursor
			}
			query = "query ($first: Int!, $after: String) { teams(first: $first, after: $after) { nodes { id name key } pageInfo { hasNextPage endCursor } } }"
			if operation == "issues.list" {
				vars["team"] = value("team_id")
				query = "query ($first: Int!, $after: String, $team: String!) { issues(first: $first, after: $after, filter: {team: {id: {eq: $team}}}) { nodes { id identifier title description updatedAt url } pageInfo { hasNextPage endCursor } } }"
			}
		}
		body = map[string]any{"query": query, "variables": vars}
	case "gitlab":
		headers.Del("Authorization")
		headers.Set("PRIVATE-TOKEN", token)
		path = "/api/v4/projects"
		if operation == "issues.list" {
			path += "/" + url.PathEscape(value("project_id")) + "/issues"
		} else {
			q.Set("membership", "true")
		}
		q.Set("per_page", limit)
		q.Set("page", "1")
		if cursor != "" {
			n, e := strconv.Atoi(cursor)
			if e != nil || n < 1 || n > 1000000 {
				return Result{}, &Error{Code: "invalid_cursor"}
			}
			q.Set("page", cursor)
		}
	case "box":
		if operation == "files.list" {
			path = "/2.0/folders/" + value("folder_id") + "/items"
			q.Set("usemarker", "true")
			q.Set("limit", limit)
			if cursor != "" {
				q.Set("marker", cursor)
			}
		} else {
			path = "/2.0/files/" + value("file_id")
		}
		q.Set("fields", "id,type,name,size,modified_at,description")
	case "discord-bot":
		headers.Set("Authorization", "Bot "+token)
		path = "/api/v10"
		switch operation {
		case "identity.get":
			path += "/users/@me"
		case "channels.list":
			path += "/guilds/" + value("guild_id") + "/channels"
		case "messages.list":
			path += "/channels/" + value("channel_id") + "/messages"
			q.Set("limit", limit)
			if cursor != "" {
				if !decimalID.MatchString(cursor) {
					return Result{}, &Error{Code: "invalid_cursor"}
				}
				q.Set("before", cursor)
			}
		}
		for _, k := range []string{"guild_id", "channel_id"} {
			if id := value(k); id != "" && !decimalID.MatchString(id) {
				return Result{}, &Error{Code: "invalid_resource_id"}
			}
		}
	case "feishu", "lark":
		if n, _ := strconv.Atoi(limit); n > 50 {
			limit = "50"
		}
		path = "/open-apis/im/v1/"
		if operation == "chats.list" {
			path += "chats"
		} else {
			path += "messages"
			q.Set("container_id_type", "chat")
			q.Set("container_id", value("chat_id"))
		}
		q.Set("page_size", limit)
		if cursor != "" {
			q.Set("page_token", cursor)
		}
	case "google-docs":
		path = "/v1/documents/" + value("document_id")
		q.Set("includeTabsContent", "true")
	case "google-sheets":
		path = "/v4/spreadsheets/" + value("spreadsheet_id")
		if operation == "values.get" {
			path += "/values/" + url.PathEscape(value("range"))
		} else {
			q.Set("fields", "spreadsheetId,spreadsheetUrl,properties(title,locale,timeZone),sheets(properties)")
		}
	case "google-chat":
		path = "/v1/spaces"
		if operation == "messages.list" {
			path += "/" + value("space_id") + "/messages"
		}
		q.Set("pageSize", limit)
		if cursor != "" {
			q.Set("pageToken", cursor)
		}
	case "microsoft-contacts", "microsoft-onenote", "microsoft-teams":
		path = "/v1.0/me"
		switch provider {
		case "microsoft-contacts":
			path += "/contacts"
			q.Set("$select", "id,displayName,emailAddresses,mobilePhone,businessPhones")
		case "microsoft-onenote":
			switch operation {
			case "notebooks.list":
				path += "/onenote/notebooks"
			case "pages.list":
				path += "/onenote/pages"
			case "pages.content":
				path += "/onenote/pages/" + url.PathEscape(value("page_id")) + "/content"
				contentType = "text/html"
			}
		case "microsoft-teams":
			if n, _ := strconv.Atoi(limit); n > 50 {
				limit = "50"
			}
			switch operation {
			case "teams.list":
				path += "/joinedTeams"
			case "channels.list":
				path = "/v1.0/teams/" + url.PathEscape(value("team_id")) + "/channels"
			case "messages.list":
				path = "/v1.0/teams/" + url.PathEscape(value("team_id")) + "/channels/" + url.PathEscape(value("channel_id")) + "/messages"
			}
		}
		if _, has := args["limit"]; has || operation == "contacts.list" || operation == "notebooks.list" || operation == "pages.list" || operation == "messages.list" {
			q.Set("$top", limit)
		}
		if cursor != "" {
			q.Set("$skiptoken", cursor)
		}
	}
	if path == "" {
		return Result{}, &Error{Code: "operation_unavailable"}
	}
	endpoint := "https://" + host + path
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}
	result, err := c.saasRequest(ctx, provider, operation, method, endpoint, headers, body, contentType)
	if err != nil || contentType != "application/json" {
		return result, err
	}
	if provider == "linear" || provider == "feishu" || provider == "lark" {
		var response struct {
			Errors []json.RawMessage `json:"errors"`
			Code   int               `json:"code"`
		}
		if json.Unmarshal(result.Body, &response) != nil || len(response.Errors) > 0 || response.Code != 0 {
			return Result{}, &Error{Code: "provider_request_failed"}
		}
	}
	if strings.HasPrefix(provider, "microsoft-") {
		var payload map[string]json.RawMessage
		if json.Unmarshal(result.Body, &payload) != nil || payload == nil {
			return Result{}, &Error{Code: "invalid_upstream_json"}
		}
		if raw := payload["@odata.nextLink"]; raw != nil {
			var next string
			if json.Unmarshal(raw, &next) != nil {
				return Result{}, &Error{Code: "invalid_upstream_pagination"}
			}
			parsed, e := url.Parse(next)
			if e != nil || parsed.Scheme != "https" || parsed.Host != host || parsed.Path != path || parsed.User != nil {
				return Result{}, &Error{Code: "invalid_upstream_pagination"}
			}
			delete(payload, "@odata.nextLink")
			payload["incomplete"] = json.RawMessage("true")
			if nextCursor := parsed.Query().Get("$skiptoken"); nextCursor != "" {
				payload["next_cursor"], _ = json.Marshal(nextCursor)
			}
			result.Body, _ = json.Marshal(payload)
		}
	}
	return result, nil
}
