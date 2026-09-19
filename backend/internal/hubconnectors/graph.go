package hubconnectors

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const graphMaxResponseBytes = 2 * 1024 * 1024

// Graph IDs can be opaque base64-derived strings or OneDrive IDs with '!'.
// Slashes, percent escapes, query delimiters, and traversal are never accepted.
var graphResourceID = regexp.MustCompile(`^[A-Za-z0-9_+=!.\-]+$`)

// nil means a JSON scalar; nested objects must have an explicit projection.
type graphProjection map[string]graphProjection

func graphFields(names string) graphProjection {
	result := graphProjection{}
	for _, name := range strings.Split(names, ",") {
		result[name] = nil
	}
	return result
}

func graphDateTime() graphProjection { return graphFields("dateTime,timeZone") }
func graphAddress() graphProjection {
	return graphProjection{"emailAddress": graphFields("name,address")}
}

func (c *Client) executeGraph(ctx context.Context, providerID, operationID, credential string, args map[string]any) (Result, error) {
	// Validate locally as well as in Execute so future internal callers cannot
	// accidentally bypass the operation schema or fixed destination rules.
	var operation *Operation
	for _, provider := range graphProviders() {
		if provider.ID != providerID {
			continue
		}
		for i := range provider.Operations {
			if provider.Operations[i].ID == operationID {
				operation = &provider.Operations[i]
				break
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
	for _, key := range []string{"calendar_id", "tasklist_id", "folder_id", "file_id", "message_id"} {
		if id, ok := args[key].(string); ok && (!graphResourceID.MatchString(id) || strings.Contains(id, "..") || id == ".") {
			return Result{}, &Error{Code: "invalid_resource_id"}
		}
	}
	path := "/v1.0/me"
	query := url.Values{}
	var selected string
	var projection graphProjection
	list := strings.HasSuffix(operationID, ".list")
	limit, _ := strconv.Atoi(integerArg(args, "limit", 20))
	if list {
		query.Set("$top", strconv.Itoa(limit))
	}
	switch providerID {
	case "microsoft-calendar":
		if operationID == "calendars.list" {
			path += "/calendars"
			selected = "id,name,color,isDefaultCalendar,owner"
			projection = graphFields("id,name,color,isDefaultCalendar")
			projection["owner"] = graphFields("name,address")
		} else {
			start, err1 := time.Parse(time.RFC3339, args["time_min"].(string))
			end, err2 := time.Parse(time.RFC3339, args["time_max"].(string))
			if err1 != nil || err2 != nil || !end.After(start) || end.Sub(start) > 7*24*time.Hour {
				return Result{}, &Error{Code: "invalid_time_range"}
			}
			path = "/v1.0/me/calendars/" + url.PathEscape(args["calendar_id"].(string)) + "/calendarView"
			query.Set("startDateTime", start.Format(time.RFC3339Nano))
			query.Set("endDateTime", end.Format(time.RFC3339Nano))
			selected = "id,subject,start,end,isCancelled"
			projection = graphFields("id,subject,isCancelled")
			projection["start"], projection["end"] = graphDateTime(), graphDateTime()
		}
	case "microsoft-todo":
		path += "/todo/lists"
		if operationID == "tasklists.list" {
			selected = "id,displayName,isOwner,isShared,wellknownListName"
			projection = graphFields(selected)
		} else {
			path += "/" + url.PathEscape(args["tasklist_id"].(string)) + "/tasks"
			selected = "id,title,status,importance,createdDateTime,lastModifiedDateTime,body,dueDateTime,completedDateTime"
			projection = graphFields("id,title,status,importance,createdDateTime,lastModifiedDateTime")
			projection["body"] = graphFields("content,contentType")
			projection["dueDateTime"], projection["completedDateTime"] = graphDateTime(), graphDateTime()
		}
	case "onedrive":
		path += "/drive"
		if operationID == "files.list" {
			if id, ok := args["folder_id"].(string); ok {
				path += "/items/" + url.PathEscape(id) + "/children"
			} else {
				path += "/root/children"
			}
		} else {
			path += "/items/" + url.PathEscape(args["file_id"].(string))
		}
		selected = "id,name,size,createdDateTime,lastModifiedDateTime,webUrl,description,file,folder,parentReference"
		projection = graphFields("id,name,size,createdDateTime,lastModifiedDateTime,webUrl,description")
		projection["file"], projection["folder"] = graphFields("mimeType"), graphFields("childCount")
		projection["parentReference"] = graphFields("driveId,id,path")
	case "outlook-mail":
		if operationID == "messages.list" {
			if id, ok := args["folder_id"].(string); ok {
				path += "/mailFolders/" + url.PathEscape(id)
			}
			path += "/messages"
		} else {
			path += "/messages/" + url.PathEscape(args["message_id"].(string))
		}
		selected = "id,subject,receivedDateTime,sentDateTime,isRead,importance,hasAttachments,webLink,from,sender,toRecipients"
		projection = graphFields("id,subject,receivedDateTime,sentDateTime,isRead,importance,hasAttachments,webLink")
		projection["from"], projection["sender"], projection["toRecipients"] = graphAddress(), graphAddress(), graphAddress()
		if operationID == "messages.get" {
			selected += ",body,bodyPreview"
			projection["body"], projection["bodyPreview"] = graphFields("content,contentType"), nil
		}
	}
	query.Set("$select", selected)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://graph.microsoft.com"+path+"?"+query.Encode(), nil)
	if err != nil {
		return Result{}, &Error{Code: "invalid_request"}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+credential)
	if providerID == "outlook-mail" {
		req.Header.Set("Prefer", `outlook.body-content-type="text"`)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return Result{}, &Error{Code: "upstream_unavailable"}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		code := "upstream_error"
		switch res.StatusCode {
		case 401:
			code = "reauthorization_required"
		case 403:
			code = "provider_permission_denied"
		case 429:
			code = "provider_rate_limited"
		}
		return Result{}, &Error{Code: code, StatusCode: res.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, graphMaxResponseBytes+1))
	if err != nil {
		return Result{}, &Error{Code: "upstream_read_failed"}
	}
	if len(body) > graphMaxResponseBytes {
		return Result{}, &Error{Code: "response_too_large"}
	}
	var raw map[string]any
	if json.Unmarshal(body, &raw) != nil || raw == nil {
		return Result{}, &Error{Code: "invalid_upstream_json"}
	}
	var output any
	if list {
		items, ok := raw["value"].([]any)
		if !ok {
			return Result{}, &Error{Code: "invalid_upstream_json"}
		}
		// Never expose or follow an upstream continuation URL, including malicious
		// URLs and signed parameters. The limitation is explicit even on empty pages.
		next, nextPresent := raw["@odata.nextLink"]
		hasMore := nextPresent && next != nil && next != ""
		if len(items) > limit {
			items = items[:limit]
			hasMore = true
		}
		values := make([]any, 0, len(items))
		for _, value := range items {
			item, ok := value.(map[string]any)
			if !ok || !graphHasID(item) {
				return Result{}, &Error{Code: "invalid_upstream_json"}
			}
			values = append(values, graphProject(item, projection))
		}
		output = map[string]any{"value": values, "has_more": hasMore, "incomplete": hasMore, "continuation_supported": false}
	} else {
		if !graphHasID(raw) {
			return Result{}, &Error{Code: "invalid_upstream_json"}
		}
		output = graphProject(raw, projection)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return Result{}, &Error{Code: "invalid_upstream_json"}
	}
	return Result{ContentType: "application/json", Body: encoded}, nil
}

func graphHasID(item map[string]any) bool { id, ok := item["id"].(string); return ok && id != "" }

// Field projection is also enforced on the response. $select alone is not a
// security boundary: Graph can add annotations such as preauthenticated URLs.
func graphProject(item map[string]any, projection graphProjection) map[string]any {
	out := map[string]any{}
	for key, nested := range projection {
		value, exists := item[key]
		if !exists {
			continue
		}
		if value == nil {
			out[key] = nil
			continue
		}
		if nested == nil {
			switch value.(type) {
			case string, bool, float64:
				out[key] = value
			}
			continue
		}
		switch value := value.(type) {
		case map[string]any:
			out[key] = graphProject(value, nested)
		case []any:
			projected := make([]any, 0, len(value))
			for _, entry := range value {
				if object, ok := entry.(map[string]any); ok {
					projected = append(projected, graphProject(object, nested))
				}
			}
			out[key] = projected
		}
	}
	return out
}
