package hubconnectors

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// GoogleScopes is shared by onboarding and callback validation. Provider scopes
// authorize this server; agents still require independent operation grants.
func GoogleScopes(provider string) []string {
	const base = "https://www.googleapis.com/auth/"
	switch provider {
	case "google-drive":
		return []string{base + "drive.readonly"}
	case "gmail":
		return []string{base + "gmail.readonly"}
	case "google-calendar":
		return []string{"openid", base + "calendar.calendarlist.readonly", base + "calendar.events.readonly", base + "calendar.events.freebusy"}
	case "google-tasks":
		return []string{"openid", base + "tasks.readonly"}
	case "google-contacts":
		return []string{"openid", base + "contacts.readonly"}
	}
	return nil
}

func googlePersonalProviders() []Provider {
	list := func() map[string]any { return map[string]any{"limit": limitParam(), "page_token": textParam(4096)} }
	calendar := func() map[string]any {
		return map[string]any{"calendar_id": textParam(256), "time_min": textParam(64), "time_max": textParam(64)}
	}
	events := calendar()
	events["limit"] = limitParam()
	tasks := list()
	tasks["tasklist_id"] = textParam(256)
	return []Provider{
		{ID: "google-calendar", Name: "Google Calendar", AuthMode: "oauth2", ImplementationStatus: "adapter_available", MultipleAccounts: true,
			Requirements: []string{"Google OAuth app and enabled Calendar API, or owner-supplied access token"},
			Limitations:  []string{"Provider consent reads calendars account-wide; Agent event and availability grants require an exact calendar and date window", "Event results contain basic fields only; no writes or persistent sync", "Event pages report incompleteness; continuation is not supported yet"},
			Operations: []Operation{
				{ID: "calendars.list", Description: "List calendar names and IDs for this account", ReadOnly: true, ScopeAlternatives: []string{GoogleScopes("google-calendar")[1]}, InputSchema: schema(list())},
				{ID: "events.list", Description: "Read basic events overlapping a required calendar/time window (up to seven days)", ReadOnly: true, ScopeAlternatives: []string{GoogleScopes("google-calendar")[2]}, InputSchema: schema(events, "calendar_id", "time_min", "time_max")},
				{ID: "freebusy.query", Description: "Read busy intervals for one calendar in a required time window (up to seven days)", ReadOnly: true, ScopeAlternatives: []string{GoogleScopes("google-calendar")[3]}, InputSchema: schema(calendar(), "calendar_id", "time_min", "time_max")},
			}},
		{ID: "google-tasks", Name: "Google Tasks", AuthMode: "oauth2", ImplementationStatus: "adapter_available", MultipleAccounts: true,
			Requirements: []string{"Google OAuth app and enabled Tasks API, or owner-supplied access token"}, Limitations: []string{"Read-only, one page per call; task scheduled dates are not appointment times"},
			Operations: []Operation{
				{ID: "tasklists.list", Description: "Read one page of task lists", ReadOnly: true, ScopeAlternatives: GoogleScopes("google-tasks")[1:], InputSchema: schema(list())},
				{ID: "tasks.list", Description: "Read one page of tasks in one list", ReadOnly: true, ScopeAlternatives: GoogleScopes("google-tasks")[1:], InputSchema: schema(tasks, "tasklist_id")},
			}},
		{ID: "google-contacts", Name: "Google Contacts", AuthMode: "oauth2", ImplementationStatus: "adapter_available", MultipleAccounts: true,
			Requirements: []string{"Google OAuth app and enabled People API, or owner-supplied access token"}, Limitations: []string{"Personal contacts only; not an organization directory", "Only names, email addresses and phone numbers are requested"},
			Operations: []Operation{{ID: "contacts.list", Description: "Read one page of personal contacts", ReadOnly: true, ScopeAlternatives: GoogleScopes("google-contacts")[1:], InputSchema: schema(list())}}},
	}
}

var calendarIdentifier = regexp.MustCompile(`^[A-Za-z0-9_@.+#-]{1,256}$`)

func (c *Client) executeGooglePersonal(ctx context.Context, provider, operation, credential string, args map[string]any) (Result, error) {
	method, endpoint := http.MethodGet, ""
	q := url.Values{}
	var body any
	switch provider {
	case "google-calendar":
		if operation == "calendars.list" {
			endpoint = "https://www.googleapis.com/calendar/v3/users/me/calendarList"
			q.Set("maxResults", integerArg(args, "limit", 20))
			copyString(q, "pageToken", args, "page_token")
			q.Set("fields", "nextPageToken,items(id,summary,timeZone,accessRole,primary)")
		} else {
			id := args["calendar_id"].(string)
			if !calendarIdentifier.MatchString(id) {
				return Result{}, &Error{Code: "invalid_calendar_id"}
			}
			start, e1 := time.Parse(time.RFC3339, args["time_min"].(string))
			end, e2 := time.Parse(time.RFC3339, args["time_max"].(string))
			if e1 != nil || e2 != nil || !end.After(start) || end.Sub(start) > 7*24*time.Hour {
				return Result{}, &Error{Code: "invalid_time_window"}
			}
			if operation == "freebusy.query" {
				method = http.MethodPost
				endpoint = "https://www.googleapis.com/calendar/v3/freeBusy"
				body = map[string]any{"timeMin": start.Format(time.RFC3339Nano), "timeMax": end.Format(time.RFC3339Nano), "items": []map[string]string{{"id": id}}}
			} else {
				endpoint = "https://www.googleapis.com/calendar/v3/calendars/" + url.PathEscape(id) + "/events"
				q.Set("timeMin", start.Format(time.RFC3339Nano))
				q.Set("timeMax", end.Format(time.RFC3339Nano))
				q.Set("singleEvents", "true")
				q.Set("orderBy", "startTime")
				q.Set("maxResults", integerArg(args, "limit", 20))
				q.Set("fields", "nextPageToken,timeZone,items(id,summary,start,end,status)")
			}
		}
	case "google-tasks":
		endpoint = "https://tasks.googleapis.com/tasks/v1/users/@me/lists"
		if operation == "tasks.list" {
			id := args["tasklist_id"].(string)
			if !resourceID.MatchString(id) && id != "@default" {
				return Result{}, &Error{Code: "invalid_resource_id"}
			}
			endpoint = "https://tasks.googleapis.com/tasks/v1/lists/" + url.PathEscape(id) + "/tasks"
		}
		q.Set("maxResults", integerArg(args, "limit", 20))
		copyString(q, "pageToken", args, "page_token")
	case "google-contacts":
		endpoint = "https://people.googleapis.com/v1/people/me/connections"
		q.Set("personFields", "names,emailAddresses,phoneNumbers")
		q.Set("pageSize", integerArg(args, "limit", 20))
		copyString(q, "pageToken", args, "page_token")
	}
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}
	result, err := c.personalJSON(ctx, method, endpoint, credential, body)
	if err != nil {
		return result, err
	}
	if provider == "google-calendar" {
		if operation == "events.list" {
			return projectGoogleEvents(result, args)
		}
		if operation == "freebusy.query" {
			return projectGoogleBusy(result, args)
		}
	}
	return result, nil
}

func (c *Client) personalJSON(ctx context.Context, method, endpoint, credential string, body any) (Result, error) {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(raw))
	if err != nil {
		return Result{}, &Error{Code: "invalid_request"}
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return Result{}, &Error{Code: "upstream_unavailable"}
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return Result{}, &Error{Code: "upstream_error", StatusCode: res.StatusCode}
	}
	raw, err = io.ReadAll(io.LimitReader(res.Body, MaxResponseBytes+1))
	if err != nil || len(raw) > MaxResponseBytes {
		return Result{}, &Error{Code: "response_too_large"}
	}
	if !json.Valid(raw) {
		return Result{}, &Error{Code: "invalid_upstream_json"}
	}
	return Result{ContentType: "application/json", Body: raw}, nil
}

type googleEventTime struct {
	Date     string `json:"date,omitempty"`
	DateTime string `json:"dateTime,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
}
type googleBasicEvent struct {
	ID      string          `json:"id"`
	Summary string          `json:"summary,omitempty"`
	Start   googleEventTime `json:"start"`
	End     googleEventTime `json:"end"`
	Status  string          `json:"status,omitempty"`
}

func projectGoogleEvents(result Result, args map[string]any) (Result, error) {
	var input struct {
		Items    []googleBasicEvent `json:"items"`
		TimeZone string             `json:"timeZone"`
		Next     string             `json:"nextPageToken"`
	}
	if json.Unmarshal(result.Body, &input) != nil || input.Items == nil {
		return Result{}, &Error{Code: "invalid_upstream_json"}
	}
	// No raw page token is exposed: a provider token must not bypass a Hub time constraint.
	start, _ := time.Parse(time.RFC3339, args["time_min"].(string))
	end, _ := time.Parse(time.RFC3339, args["time_max"].(string))
	out := []googleBasicEvent{}
	for _, item := range input.Items {
		if item.Status == "cancelled" {
			continue
		}
		from, e1 := parseGoogleEventTime(item.Start, input.TimeZone)
		to, e2 := parseGoogleEventTime(item.End, input.TimeZone)
		if e1 != nil || e2 != nil {
			return Result{}, &Error{Code: "invalid_event_time"}
		}
		if from.Before(end) && to.After(start) {
			out = append(out, item)
		}
	}
	result.Body, _ = json.Marshal(map[string]any{"items": out, "timeZone": input.TimeZone, "incomplete": input.Next != "", "continuation_supported": false})
	return result, nil
}
func parseGoogleEventTime(v googleEventTime, fallback string) (time.Time, error) {
	if v.DateTime != "" {
		return time.Parse(time.RFC3339, v.DateTime)
	}
	zone := v.TimeZone
	if zone == "" {
		zone = fallback
	}
	if zone == "" {
		zone = "UTC"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, err
	}
	return time.ParseInLocation("2006-01-02", v.Date, loc)
}
func projectGoogleBusy(result Result, args map[string]any) (Result, error) {
	type interval struct {
		Start string `json:"start"`
		End   string `json:"end"`
	}
	type entry struct {
		Busy   []interval `json:"busy"`
		Errors []struct {
			Domain string `json:"domain"`
			Reason string `json:"reason"`
		} `json:"errors,omitempty"`
	}
	var input struct {
		Calendars map[string]entry `json:"calendars"`
	}
	id := args["calendar_id"].(string)
	if json.Unmarshal(result.Body, &input) != nil {
		return Result{}, &Error{Code: "invalid_upstream_json"}
	}
	value, ok := input.Calendars[id]
	if !ok {
		return Result{}, &Error{Code: "missing_calendar_response"}
	}
	if len(value.Errors) > 0 {
		return Result{}, &Error{Code: "calendar_availability_unknown"}
	}
	start, _ := time.Parse(time.RFC3339, args["time_min"].(string))
	end, _ := time.Parse(time.RFC3339, args["time_max"].(string))
	busy := []interval{}
	for _, v := range value.Busy {
		a, e1 := time.Parse(time.RFC3339, v.Start)
		b, e2 := time.Parse(time.RFC3339, v.End)
		if e1 != nil || e2 != nil || !b.After(a) {
			return Result{}, &Error{Code: "invalid_event_time"}
		}
		if a.Before(start) {
			a = start
		}
		if b.After(end) {
			b = end
		}
		if b.After(a) {
			busy = append(busy, interval{a.Format(time.RFC3339Nano), b.Format(time.RFC3339Nano)})
		}
	}
	result.Body, _ = json.Marshal(map[string]any{"calendar_id": id, "busy": busy})
	return result, nil
}

func googlePersonal(provider string) bool {
	return strings.HasPrefix(provider, "google-") && provider != "google-drive" && len(GoogleScopes(provider)) > 0
}
