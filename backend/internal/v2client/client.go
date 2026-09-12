package v2client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	"event-driven-context/internal/core"
	"event-driven-context/internal/v2"
)

const maxJSONResponse = 128 << 20

// APIError is a structured non-2xx response. Error never includes credentials.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("server returned HTTP %d (%s): %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("server returned HTTP %d", e.Status)
}

// BatchError reports item failures while preserving the complete result returned
// by RecordEvents. Created and duplicate items are successful.
type BatchError struct {
	Failed []v2.EventWriteResult
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("event batch has %d failed item(s)", len(e.Failed))
}

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(base, token string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("server must be an absolute HTTP(S) origin URL")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return nil, fmt.Errorf("remote servers require HTTPS; HTTP is allowed only on loopback")
	}
	return &Client{
		BaseURL: strings.TrimRight(base, "/"),
		Token:   token,
		HTTP: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	r, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		r.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return r, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		if len(b) > 2<<20 {
			return fmt.Errorf("request exceeds 2 MiB JSON limit")
		}
		body = bytes.NewReader(b)
	}
	r, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	if in != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(r)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return c.responseError(res)
	}
	if out == nil || res.StatusCode == http.StatusNoContent {
		_, err = io.Copy(io.Discard, io.LimitReader(res.Body, 16<<10))
		return err
	}
	return decodeJSON(res.Body, maxJSONResponse, out)
}

func decodeJSON(r io.Reader, limit int64, out any) error {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return err
	}
	if int64(len(b)) > limit {
		return fmt.Errorf("server response exceeds %d byte limit", limit)
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return fmt.Errorf("invalid server JSON: %w", err)
	}
	var trailing any
	if err := d.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("invalid server JSON: trailing data")
	}
	return nil
}

func (c *Client) responseError(res *http.Response) error {
	var env struct {
		Error v2.Error `json:"error"`
	}
	if decodeJSON(io.LimitReader(res.Body, 16<<10), 16<<10, &env) == nil && env.Error.Code != "" {
		message := env.Error.Message
		if c.Token != "" {
			message = strings.ReplaceAll(message, c.Token, "[redacted]")
		}
		return &APIError{Status: res.StatusCode, Code: env.Error.Code, Message: message}
	}
	return &APIError{Status: res.StatusCode}
}

func projectPath(projectID, suffix string) string {
	return "/v1/projects/" + url.PathEscape(projectID) + suffix
}

func (c *Client) Register(ctx context.Context, in core.Credentials) (out core.User, err error) {
	err = c.doJSON(ctx, http.MethodPost, "/v1/auth/register", in, &out)
	return
}

func (c *Client) Login(ctx context.Context, in core.Credentials) (out core.LoginResult, err error) {
	err = c.doJSON(ctx, http.MethodPost, "/v1/auth/login", in, &out)
	return
}

func (c *Client) Me(ctx context.Context) (out core.User, err error) {
	err = c.doJSON(ctx, http.MethodGet, "/v1/me", nil, &out)
	return
}

func (c *Client) Logout(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodPost, "/v1/auth/logout", core.Empty{}, &core.Empty{})
}

func (c *Client) CreateProject(ctx context.Context, in core.ProjectInput) (out core.Project, err error) {
	err = c.doJSON(ctx, http.MethodPost, "/v1/projects", in, &out)
	return
}

func (c *Client) ListProjects(ctx context.Context) (out core.Projects, err error) {
	err = c.doJSON(ctx, http.MethodGet, "/v1/projects", nil, &out)
	return
}

func (c *Client) AddMember(ctx context.Context, projectID, username string) (out core.User, err error) {
	err = c.doJSON(ctx, http.MethodPost, projectPath(projectID, "/members"), struct {
		Username string `json:"username"`
	}{username}, &out)
	return
}

func (c *Client) ListMembers(ctx context.Context, projectID string) (out core.Members, err error) {
	err = c.doJSON(ctx, http.MethodGet, projectPath(projectID, "/members"), nil, &out)
	return
}

func (c *Client) RecordEvents(ctx context.Context, projectID string, in v2.RecordEventsInput) (out v2.RecordEventsResult, err error) {
	if len(in.Events) == 0 || len(in.Events) > v2.MaxBatchEvents {
		return out, fmt.Errorf("event batch must contain 1-%d items", v2.MaxBatchEvents)
	}
	err = c.doJSON(ctx, http.MethodPost, projectPath(projectID, "/events"), in, &out)
	if err != nil {
		return out, err
	}
	if len(out.Results) != len(in.Events) {
		return out, fmt.Errorf("server returned %d event results for %d inputs", len(out.Results), len(in.Events))
	}
	failed := make([]v2.EventWriteResult, 0)
	for i, item := range out.Results {
		if canonicalEventID(item.ID) != canonicalEventID(in.Events[i].ID) {
			return out, fmt.Errorf("server event result %d has unexpected id", i)
		}
		switch item.Status {
		case "created", "duplicate":
		case "conflict", "invalid":
			failed = append(failed, item)
		default:
			return out, fmt.Errorf("server event result %d has unknown status %q", i, item.Status)
		}
	}
	if len(failed) > 0 {
		return out, &BatchError{Failed: failed}
	}
	return out, nil
}

func canonicalEventID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

func (c *Client) QueryEvents(ctx context.Context, projectID string, in v2.QueryEventsInput) (out v2.EventsPage, err error) {
	err = c.doJSON(ctx, http.MethodPost, projectPath(projectID, "/events/query"), in, &out)
	if err == nil {
		err = validateEventProjects(projectID, out.Events)
	}
	return
}

func (c *Client) GetEvent(ctx context.Context, projectID, eventID string) (out v2.Event, err error) {
	err = c.doJSON(ctx, http.MethodGet, projectPath(projectID, "/events/"+url.PathEscape(eventID)), nil, &out)
	if err == nil && out.ProjectID != projectID {
		err = fmt.Errorf("server returned event from an unexpected project")
	}
	return
}

func validateEventProjects(projectID string, events []v2.Event) error {
	for _, event := range events {
		if event.ProjectID != projectID {
			return fmt.Errorf("server returned event from an unexpected project")
		}
	}
	return nil
}

func (c *Client) ListMetadata(ctx context.Context, projectID, key string) (out v2.MetadataResult, err error) {
	path := projectPath(projectID, "/metadata")
	if key != "" {
		path += "?key=" + url.QueryEscape(key)
	}
	err = c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return
}

func (c *Client) PutFile(ctx context.Context, projectID string, in v2.FileUpload) (out v2.FileInfo, err error) {
	if in.Reader == nil || in.SizeBytes < 0 || in.SizeBytes > v2.MaxFileBytes {
		return out, fmt.Errorf("file must be 0-%d bytes", v2.MaxFileBytes)
	}
	if len(in.SHA256) != sha256.Size*2 {
		return out, fmt.Errorf("sha256 must be 64 hexadecimal characters")
	}
	if _, err = hex.DecodeString(in.SHA256); err != nil {
		return out, fmt.Errorf("sha256 must be 64 hexadecimal characters")
	}
	digest := strings.ToLower(in.SHA256)
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		var writeErr error
		defer func() { _ = pw.CloseWithError(writeErr) }()
		if writeErr = mw.WriteField("sha256", digest); writeErr != nil {
			return
		}
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, in.Filename))
		h.Set("Content-Type", in.MediaType)
		var part io.Writer
		part, writeErr = mw.CreatePart(h)
		if writeErr != nil {
			return
		}
		var n int64
		n, writeErr = io.Copy(part, io.LimitReader(in.Reader, in.SizeBytes+1))
		if writeErr == nil && n != in.SizeBytes {
			writeErr = fmt.Errorf("file size changed while uploading: expected %d, read %d", in.SizeBytes, n)
		}
		if writeErr == nil {
			writeErr = mw.Close()
		}
	}()
	r, err := c.newRequest(ctx, http.MethodPost, projectPath(projectID, "/files"), pr)
	if err != nil {
		_ = pr.Close()
		return out, err
	}
	r.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := c.HTTP.Do(r)
	if err != nil {
		_ = pr.CloseWithError(err)
		return out, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return out, c.responseError(res)
	}
	if err = decodeJSON(res.Body, 1<<20, &out); err != nil {
		return out, err
	}
	if out.ProjectID != projectID {
		return out, fmt.Errorf("server returned file from an unexpected project")
	}
	if !strings.EqualFold(out.SHA256, digest) || out.SizeBytes != in.SizeBytes {
		return out, fmt.Errorf("server returned file metadata that does not match the upload")
	}
	return out, nil
}

type DownloadInfo struct {
	FileID    string `json:"file_id"`
	Filename  string `json:"filename,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

func (c *Client) GetFile(ctx context.Context, projectID, fileID string, dst io.Writer) (out DownloadInfo, err error) {
	if dst == nil {
		return out, fmt.Errorf("file destination is required")
	}
	r, err := c.newRequest(ctx, http.MethodGet, projectPath(projectID, "/files/"+url.PathEscape(fileID)), nil)
	if err != nil {
		return out, err
	}
	res, err := c.HTTP.Do(r)
	if err != nil {
		return out, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return out, c.responseError(res)
	}
	out.FileID = res.Header.Get("X-EDC-File-ID")
	out.SHA256 = res.Header.Get("X-EDC-SHA256")
	out.MediaType = res.Header.Get("Content-Type")
	if out.FileID != fileID {
		return out, fmt.Errorf("server returned an unexpected file id")
	}
	if len(out.SHA256) != sha256.Size*2 || out.SHA256 != strings.ToLower(out.SHA256) {
		return out, fmt.Errorf("server returned an invalid file sha256")
	}
	if _, err = hex.DecodeString(out.SHA256); err != nil {
		return out, fmt.Errorf("server returned an invalid file sha256")
	}
	if raw := res.Header.Get("Content-Length"); raw != "" {
		out.SizeBytes, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || out.SizeBytes < 0 || out.SizeBytes > v2.MaxFileBytes {
			return out, fmt.Errorf("server returned an invalid file size")
		}
	} else {
		return out, fmt.Errorf("server omitted file size")
	}
	_, params, _ := mime.ParseMediaType(res.Header.Get("Content-Disposition"))
	out.Filename = params["filename"]
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), io.LimitReader(res.Body, out.SizeBytes+1))
	if err != nil {
		return out, err
	}
	if n != out.SizeBytes {
		return out, fmt.Errorf("downloaded file size mismatch: expected %d, got %d", out.SizeBytes, n)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != out.SHA256 {
		return out, fmt.Errorf("downloaded file sha256 mismatch")
	}
	return out, nil
}

func (c *Client) ListState(ctx context.Context, projectID, prefix string) (out v2.StatesResult, err error) {
	path := projectPath(projectID, "/state")
	if prefix != "" {
		path += "?prefix=" + url.QueryEscape(prefix)
	}
	err = c.doJSON(ctx, http.MethodGet, path, nil, &out)
	if err == nil {
		err = validateStateProjects(projectID, out.States)
	}
	return
}

func statePath(projectID, key string) (string, error) {
	parts := strings.SplitN(key, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("state key must be <plugin_id>/<name>")
	}
	return projectPath(projectID, "/state/"+url.PathEscape(parts[0])+"/"+url.PathEscape(parts[1])), nil
}

func (c *Client) GetState(ctx context.Context, projectID, key string, version *int64) (out v2.State, err error) {
	path, err := statePath(projectID, key)
	if err != nil {
		return out, err
	}
	if version != nil {
		path += "?version=" + strconv.FormatInt(*version, 10)
	}
	err = c.doJSON(ctx, http.MethodGet, path, nil, &out)
	if err == nil {
		err = validateStateProjects(projectID, []v2.State{out})
	}
	return
}

func (c *Client) PutState(ctx context.Context, projectID string, in v2.PutStateInput) (out v2.State, err error) {
	path, err := statePath(projectID, in.Key)
	if err != nil {
		return out, err
	}
	err = c.doJSON(ctx, http.MethodPut, path, in, &out)
	if err == nil {
		err = validateStateProjects(projectID, []v2.State{out})
	}
	return
}

func validateStateProjects(projectID string, states []v2.State) error {
	for _, state := range states {
		if state.ProjectID != projectID {
			return fmt.Errorf("server returned state from an unexpected project")
		}
	}
	return nil
}

func (c *Client) ListPlugins(ctx context.Context, projectID string) (out []v2.Installation, err error) {
	var env struct {
		Plugins []v2.Installation `json:"plugins"`
	}
	err = c.doJSON(ctx, http.MethodGet, projectPath(projectID, "/plugins"), nil, &env)
	if err == nil {
		out = env.Plugins
		for _, p := range out {
			if p.ProjectID != projectID {
				return nil, fmt.Errorf("server returned plugin from an unexpected project")
			}
		}
	}
	return
}

func (c *Client) InstallPlugin(ctx context.Context, projectID string, in v2.InstallPluginInput) (out v2.InstallPluginResult, err error) {
	err = c.doJSON(ctx, http.MethodPost, projectPath(projectID, "/plugins"), in, &out)
	if err == nil && out.Installation.ProjectID != projectID {
		err = fmt.Errorf("server returned plugin from an unexpected project")
	}
	return
}

type PatchPluginInput struct {
	Action           string          `json:"action"`
	ExpectedRevision *int64          `json:"expected_revision,omitempty"`
	Config           json.RawMessage `json:"config,omitempty"`
}

func (c *Client) PatchPlugin(ctx context.Context, projectID, pluginID string, in PatchPluginInput) (out v2.Installation, err error) {
	err = c.doJSON(ctx, http.MethodPatch, projectPath(projectID, "/plugins/"+url.PathEscape(pluginID)), in, &out)
	if err == nil && (out.ProjectID != projectID || out.PluginID != pluginID) {
		err = fmt.Errorf("server returned an unexpected plugin")
	}
	return
}

func (c *Client) RemovePlugin(ctx context.Context, projectID, pluginID string) (out v2.Installation, err error) {
	err = c.doJSON(ctx, http.MethodDelete, projectPath(projectID, "/plugins/"+url.PathEscape(pluginID)), nil, &out)
	if err == nil && (out.ProjectID != projectID || out.PluginID != pluginID) {
		err = fmt.Errorf("server returned an unexpected plugin")
	}
	return
}

func (c *Client) RequestManualRun(ctx context.Context, projectID, pluginID string, in v2.ManualRunInput) (out v2.ManualRunRequest, err error) {
	err = c.doJSON(ctx, http.MethodPost, projectPath(projectID, "/plugins/"+url.PathEscape(pluginID)+"/runs"), in, &out)
	if err == nil && (out.ProjectID != projectID || out.PluginID != pluginID) {
		err = fmt.Errorf("server returned an unexpected plugin run")
	}
	return
}
