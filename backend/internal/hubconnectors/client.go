package hubconnectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const MaxResponseBytes = 10 * 1024 * 1024

type Result struct {
	ContentType string `json:"content_type"`
	Body        []byte `json:"body"`
}

// Error is deliberately free of upstream URLs, credentials and response bodies.
// In particular Telegram's token is part of its URL and must never be logged.
type Error struct {
	Code       string `json:"code"`
	StatusCode int    `json:"status_code,omitempty"`
}

func (e *Error) Error() string { return "connector: " + e.Code }

type Client struct{ http *http.Client }

// NewClient copies the caller's client, refuses every redirect and imposes a
// maximum timeout. A custom transport is trusted server configuration; it must
// not log request URLs or Authorization headers. No endpoint is caller supplied.
func NewClient(client *http.Client) *Client {
	var c http.Client
	if client != nil {
		c = *client
	}
	c.Jar = nil
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if c.Timeout <= 0 || c.Timeout > 30*time.Second {
		c.Timeout = 30 * time.Second
	}
	return &Client{http: &c}
}

var resourceID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
var botToken = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)

// Execute performs one allowlisted provider operation. The caller must check
// the connection's owner and current grant before this call, obtain the secret
// from server-controlled storage and enforce any narrower resource constraints.
// Args are never treated as a URL or merged into an arbitrary query string.
func (c *Client) Execute(ctx context.Context, providerID, operationID, credential string, args map[string]any) (Result, error) {
	provider, ok := Lookup(providerID)
	if !ok || provider.ImplementationStatus != "adapter_available" {
		return Result{}, &Error{Code: "provider_unavailable"}
	}
	var operation *Operation
	for i := range provider.Operations {
		if provider.Operations[i].ID == operationID {
			operation = &provider.Operations[i]
			break
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
	endpoint, expectedType := "", "application/json"
	switch providerID {
	case "google-drive":
		endpoint = "https://www.googleapis.com/drive/v3/files"
		switch operationID {
		case "files.list":
			q.Set("pageSize", integerArg(args, "limit", 20))
			copyString(q, "q", args, "query")
			copyString(q, "pageToken", args, "page_token")
			q.Set("fields", "nextPageToken,incompleteSearch,files(id,name,mimeType,modifiedTime,webViewLink,size,description)")
		case "files.get", "files.export":
			id := args["file_id"].(string)
			if !resourceID.MatchString(id) {
				return Result{}, &Error{Code: "invalid_resource_id"}
			}
			endpoint += "/" + id
			if operationID == "files.get" {
				q.Set("fields", "id,name,mimeType,modifiedTime,webViewLink,size,description")
			} else {
				endpoint += "/export"
				expectedType = args["mime_type"].(string)
				q.Set("mimeType", expectedType)
			}
		}
	case "gmail":
		endpoint = "https://gmail.googleapis.com/gmail/v1/users/me/messages"
		if operationID == "messages.list" {
			q.Set("maxResults", integerArg(args, "limit", 20))
			copyString(q, "q", args, "query")
			copyString(q, "pageToken", args, "page_token")
		} else {
			id := args["message_id"].(string)
			if !resourceID.MatchString(id) {
				return Result{}, &Error{Code: "invalid_resource_id"}
			}
			endpoint += "/" + id
			q.Set("format", "full")
		}
	case "telegram-bot":
		if !botToken.MatchString(credential) {
			return Result{}, &Error{Code: "invalid_credential"}
		}
		endpoint = "https://api.telegram.org/bot" + credential
		if operationID == "identity.get" {
			endpoint += "/getMe"
		} else {
			endpoint += "/getUpdates"
			q.Set("limit", integerArg(args, "limit", 20))
			q.Set("timeout", "0")
		}
	}
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Result{}, &Error{Code: "invalid_request"}
	}
	req.Header.Set("Accept", expectedType)
	if providerID != "telegram-bot" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
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
	body, err := io.ReadAll(io.LimitReader(res.Body, MaxResponseBytes+1))
	if err != nil {
		return Result{}, &Error{Code: "upstream_read_failed"}
	}
	if len(body) > MaxResponseBytes {
		return Result{}, &Error{Code: "response_too_large"}
	}
	if expectedType == "application/json" {
		if !json.Valid(body) {
			return Result{}, &Error{Code: "invalid_upstream_json"}
		}
		if providerID == "telegram-bot" {
			var envelope struct {
				OK     bool            `json:"ok"`
				Result json.RawMessage `json:"result"`
			}
			if json.Unmarshal(body, &envelope) != nil || !envelope.OK || len(envelope.Result) == 0 {
				return Result{}, &Error{Code: "upstream_error"}
			}
		}
	}
	// Do not forward upstream Content-Type or other headers to a browser. The
	// selected operation fixes the output type; the host chooses safe disposition.
	return Result{ContentType: expectedType, Body: body}, nil
}

func copyString(q url.Values, to string, args map[string]any, from string) {
	if v, ok := args[from].(string); ok {
		q.Set(to, v)
	}
}
func integerArg(args map[string]any, key string, fallback int) string {
	if value, ok := args[key]; ok {
		n, _ := number(value)
		return strconv.Itoa(n)
	}
	return strconv.Itoa(fallback)
}
func number(v any) (int, error) {
	var f float64
	switch n := v.(type) {
	case int:
		f = float64(n)
	case float64:
		f = n
	case json.Number:
		var err error
		f, err = n.Float64()
		if err != nil {
			return 0, err
		}
	default:
		return 0, errors.New("not an integer")
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || f < 1 || f > 100 {
		return 0, errors.New("integer outside range")
	}
	return int(f), nil
}
func validateArgs(s map[string]any, args map[string]any) error {
	properties := s["properties"].(map[string]any)
	if required, ok := s["required"].([]string); ok {
		for _, k := range required {
			if _, found := args[k]; !found {
				return &Error{Code: "missing_argument"}
			}
		}
	}
	for key, value := range args {
		definition, ok := properties[key]
		if !ok {
			return &Error{Code: "unknown_argument"}
		}
		p := definition.(map[string]any)
		switch p["type"] {
		case "string":
			v, ok := value.(string)
			if !ok || len(v) == 0 || strings.ContainsRune(v, '\x00') {
				return &Error{Code: "invalid_argument"}
			}
			if max, ok := p["maxLength"].(int); ok && len(v) > max {
				return &Error{Code: "invalid_argument"}
			}
			if values, ok := p["enum"].([]string); ok {
				found := false
				for _, a := range values {
					if v == a {
						found = true
					}
				}
				if !found {
					return &Error{Code: "invalid_argument"}
				}
			}
		case "integer":
			if _, err := number(value); err != nil {
				return &Error{Code: "invalid_argument"}
			}
		default:
			return fmt.Errorf("connector: unsupported schema")
		}
	}
	return nil
}
