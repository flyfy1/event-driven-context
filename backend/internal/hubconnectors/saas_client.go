package hubconnectors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

const notionAPIVersion = "2026-03-11"
const githubAPIVersion = "2026-03-10"

var githubSegment = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

func (c *Client) executeSaaS(ctx context.Context, providerID, operationID, credential string, args map[string]any) (Result, error) {
	var operation *Operation
	for _, p := range saasProviders() {
		if p.ID == providerID {
			for i := range p.Operations {
				if p.Operations[i].ID == operationID {
					operation = &p.Operations[i]
					break
				}
			}
		}
	}
	if operation == nil {
		return Result{}, &Error{Code: "operation_unavailable"}
	}
	if err := validateArgs(operation.InputSchema, args); err != nil {
		return Result{}, err
	}
	if len(credential) == 0 || len(credential) > 8192 || strings.ContainsAny(credential, " \t\r\n\x00") {
		return Result{}, &Error{Code: "invalid_credential"}
	}
	q := url.Values{}
	headers := http.Header{}
	method := http.MethodGet
	endpoint := ""
	contentType := "application/json"
	var body any
	headers.Set("Authorization", "Bearer "+credential)
	value := func(key string) string { v, _ := args[key].(string); return v }
	id := func(key string) (string, error) {
		v := value(key)
		if !resourceID.MatchString(v) {
			return "", &Error{Code: "invalid_resource_id"}
		}
		return v, nil
	}
	limit, _ := strconv.Atoi(integerArg(args, "limit", 20))
	switch providerID {
	case "todoist":
		endpoint = "https://api.todoist.com/api/v1/"
		switch operationID {
		case "tasks.list":
			endpoint += "tasks"
			copyString(q, "project_id", args, "project_id")
		case "projects.list":
			endpoint += "projects"
		case "tasks.get":
			v, e := id("task_id")
			if e != nil {
				return Result{}, e
			}
			endpoint += "tasks/" + v
		}
		if operationID != "tasks.get" {
			q.Set("limit", strconv.Itoa(limit))
			copyString(q, "cursor", args, "cursor")
		}
	case "notion":
		endpoint = "https://api.notion.com/v1/"
		headers.Set("Notion-Version", notionAPIVersion)
		switch operationID {
		case "search":
			endpoint += "search"
			method = http.MethodPost
			b := map[string]any{"page_size": limit}
			if value("query") != "" {
				b["query"] = value("query")
			}
			if value("cursor") != "" {
				b["start_cursor"] = value("cursor")
			}
			body = b
		case "pages.get":
			v, e := id("page_id")
			if e != nil {
				return Result{}, e
			}
			endpoint += "pages/" + v
		case "blocks.children":
			v, e := id("block_id")
			if e != nil {
				return Result{}, e
			}
			endpoint += "blocks/" + v + "/children"
			q.Set("page_size", strconv.Itoa(limit))
			copyString(q, "start_cursor", args, "cursor")
		}
	case "dropbox":
		endpoint = "https://api.dropboxapi.com/2/files/"
		method = http.MethodPost
		path := value("path")
		if path == "/" {
			path = ""
		}
		if operationID != "files.continue" && path != "" && !strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "id:") {
			return Result{}, &Error{Code: "invalid_resource_path"}
		}
		switch operationID {
		case "files.list":
			endpoint += "list_folder"
			body = map[string]any{"path": path, "recursive": false, "limit": limit}
		case "files.continue":
			endpoint += "list_folder/continue"
			body = map[string]any{"cursor": value("cursor")}
		case "files.get":
			if path == "" {
				return Result{}, &Error{Code: "invalid_resource_path"}
			}
			endpoint += "get_metadata"
			body = map[string]any{"path": path}
		case "files.download":
			if path == "" {
				return Result{}, &Error{Code: "invalid_resource_path"}
			}
			endpoint = "https://content.dropboxapi.com/2/files/download"
			arg, _ := json.Marshal(map[string]string{"path": path})
			headers.Set("Dropbox-API-Arg", saasASCIIJSON(arg))
			contentType = "application/octet-stream"
		}
	case "readwise-reader":
		endpoint = "https://readwise.io/api/v3/list/"
		headers.Set("Authorization", "Token "+credential)
		if operationID == "documents.get" {
			q.Set("id", value("document_id"))
			q.Set("withHtmlContent", "true")
			q.Set("limit", "1")
		} else {
			q.Set("limit", strconv.Itoa(limit))
			copyString(q, "pageCursor", args, "cursor")
			copyString(q, "location", args, "location")
			if since := value("updated_after"); since != "" {
				if _, err := time.Parse(time.RFC3339, since); err != nil {
					return Result{}, &Error{Code: "invalid_argument"}
				}
				q.Set("updatedAfter", since)
			}
		}
	case "github":
		endpoint = "https://api.github.com/"
		headers.Set("X-GitHub-Api-Version", githubAPIVersion)
		headers.Set("Accept", "application/vnd.github+json")
		headers.Set("User-Agent", "Event-driven-Context-Hub")
		if operationID == "repos.list" {
			endpoint += "user/repos"
		} else {
			owner, repo := value("owner"), value("repo")
			if !safeGitHubSegment(owner) || !safeGitHubSegment(repo) {
				return Result{}, &Error{Code: "invalid_resource_id"}
			}
			endpoint += "repos/" + owner + "/" + repo + "/"
			if operationID == "issues.list" {
				endpoint += "issues"
				q.Set("state", "open")
				copyString(q, "state", args, "state")
			} else {
				endpoint += "contents"
				if path := value("path"); path != "" {
					parts := strings.Split(path, "/")
					for i, part := range parts {
						if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\\r\n") {
							return Result{}, &Error{Code: "invalid_resource_path"}
						}
						parts[i] = url.PathEscape(part)
					}
					endpoint += "/" + strings.Join(parts, "/")
				}
				copyString(q, "ref", args, "ref")
			}
		}
		if operationID != "contents.get" {
			cursor := value("cursor")
			if cursor == "" {
				cursor = "1"
			}
			n, err := strconv.Atoi(cursor)
			if err != nil || n < 1 || n > 1000000 {
				return Result{}, &Error{Code: "invalid_cursor"}
			}
			q.Set("page", strconv.Itoa(n))
			q.Set("per_page", strconv.Itoa(limit))
		}
	case "slack":
		endpoint = "https://slack.com/api/" + operationID
		copyString(q, "cursor", args, "cursor")
		if operationID == "conversations.list" {
			q.Set("types", "public_channel")
			copyString(q, "types", args, "type")
			q.Set("exclude_archived", "true")
		} else {
			v, e := id("channel_id")
			if e != nil {
				return Result{}, e
			}
			q.Set("channel", v)
			if limit > 15 {
				limit = 15
			}
		}
		q.Set("limit", strconv.Itoa(limit))
	default:
		return Result{}, &Error{Code: "provider_unavailable"}
	}
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}
	return c.saasRequest(ctx, providerID, operationID, method, endpoint, headers, body, contentType)
}

func safeGitHubSegment(s string) bool { return s != "." && s != ".." && githubSegment.MatchString(s) }

// Dropbox transports download arguments in an HTTP header and requires
// non-ASCII path characters to use JSON unicode escapes.
func saasASCIIJSON(data []byte) string {
	var out strings.Builder
	for _, r := range string(data) {
		if r < 128 {
			out.WriteRune(r)
		} else if r <= 0xffff {
			fmt.Fprintf(&out, `\u%04x`, r)
		} else {
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&out, `\u%04x\u%04x`, hi, lo)
		}
	}
	return out.String()
}

func (c *Client) saasRequest(ctx context.Context, providerID, operationID, method, endpoint string, headers http.Header, body any, contentType string) (Result, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" {
		return Result{}, &Error{Code: "invalid_request"}
	}
	allowed := map[string][]string{"todoist": {"api.todoist.com"}, "notion": {"api.notion.com"}, "dropbox": {"api.dropboxapi.com", "content.dropboxapi.com"}, "readwise-reader": {"readwise.io"}, "github": {"api.github.com"}, "slack": {"slack.com"}}
	hostOK := false
	for _, host := range allowed[providerID] {
		if u.Host == host {
			hostOK = true
		}
	}
	if !hostOK {
		return Result{}, &Error{Code: "invalid_request"}
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return Result{}, &Error{Code: "invalid_request"}
		}
		reader = bytes.NewReader(b)
		headers.Set("Content-Type", "application/json")
	}
	if headers.Get("Accept") == "" {
		headers.Set("Accept", contentType)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return Result{}, &Error{Code: "invalid_request"}
	}
	req.Header = headers
	res, err := c.http.Do(req)
	if err != nil {
		return Result{}, &Error{Code: "upstream_unavailable"}
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		code := "upstream_error"
		switch res.StatusCode {
		case 401:
			code = "reauthorization_required"
		case 403:
			code = "provider_permission_denied"
		case 429:
			code = "provider_rate_limited"
		case 409:
			code = "provider_conflict"
		}
		return Result{}, &Error{Code: code, StatusCode: res.StatusCode}
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, MaxResponseBytes+1))
	if err != nil {
		return Result{}, &Error{Code: "upstream_read_failed"}
	}
	if len(b) > MaxResponseBytes {
		return Result{}, &Error{Code: "response_too_large"}
	}
	if contentType == "application/json" && !json.Valid(b) {
		return Result{}, &Error{Code: "invalid_upstream_json"}
	}
	if providerID == "slack" {
		var out struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &out) != nil || !out.OK {
			code := "upstream_error"
			switch out.Error {
			case "invalid_auth", "token_revoked", "token_expired", "not_authed":
				code = "reauthorization_required"
			case "missing_scope", "not_in_channel", "channel_not_found":
				code = "provider_permission_denied"
			case "ratelimited":
				code = "provider_rate_limited"
			}
			return Result{}, &Error{Code: code}
		}
	}
	if providerID == "github" && operationID != "contents.get" {
		cursor, err := githubNextCursor(res.Header.Get("Link"), u)
		if err != nil {
			return Result{}, err
		}
		b, err = json.Marshal(map[string]any{"items": json.RawMessage(b), "next_cursor": cursor})
		if err != nil {
			return Result{}, &Error{Code: "invalid_upstream_json"}
		}
	}
	return Result{ContentType: contentType, Body: b}, nil
}

// Extract a page number, never forward or fetch an upstream-provided next URL.
func githubNextCursor(link string, current *url.URL) (string, error) {
	for _, part := range strings.Split(link, ",") {
		segments := strings.Split(part, ";")
		next := false
		for _, segment := range segments[1:] {
			if strings.TrimSpace(segment) == `rel="next"` {
				next = true
			}
		}
		if !next {
			continue
		}
		raw := strings.TrimSpace(segments[0])
		if !strings.HasPrefix(raw, "<") || !strings.HasSuffix(raw, ">") {
			return "", &Error{Code: "invalid_upstream_pagination"}
		}
		u, err := url.Parse(strings.TrimSuffix(strings.TrimPrefix(raw, "<"), ">"))
		if err != nil || u.Scheme != "https" || u.Host != "api.github.com" || u.Path != current.Path || u.User != nil {
			return "", &Error{Code: "invalid_upstream_pagination"}
		}
		n, err := strconv.Atoi(u.Query().Get("page"))
		if err != nil || n < 1 || n > 1000000 {
			return "", &Error{Code: "invalid_upstream_pagination"}
		}
		return strconv.Itoa(n), nil
	}
	return "", nil
}
