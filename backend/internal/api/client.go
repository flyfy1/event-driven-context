package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"event-driven-context/internal/core"
)

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func NewClient(base, token string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("server must be an absolute HTTP(S) origin URL")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return nil, fmt.Errorf("remote servers require HTTPS; HTTP is allowed only on loopback")
	}
	return &Client{BaseURL: strings.TrimRight(base, "/"), Token: token, HTTP: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	r, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		r.Header.Set("Authorization", "Bearer "+c.Token)
	}
	res, err := c.HTTP.Do(r)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var envelope struct {
			Error json.RawMessage `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(res.Body, 16<<10)).Decode(&envelope) == nil {
			var e core.Error
			if json.Unmarshal(envelope.Error, &e) == nil && e.Code != "" {
				return &e
			}
		}
		return fmt.Errorf("server returned HTTP %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 128<<20)).Decode(out)
}
func (c *Client) Register(ctx context.Context, in core.Credentials) (out core.User, err error) {
	err = c.Do(ctx, "POST", "/v1/auth/register", in, &out)
	return
}
func (c *Client) Login(ctx context.Context, in core.Credentials) (out core.LoginResult, err error) {
	err = c.Do(ctx, "POST", "/v1/auth/login", in, &out)
	return
}
func (c *Client) Me(ctx context.Context) (out core.User, err error) {
	err = c.Do(ctx, "GET", "/v1/me", nil, &out)
	return
}
func (c *Client) Logout(ctx context.Context) error {
	return c.Do(ctx, "POST", "/v1/auth/logout", core.Empty{}, &core.Empty{})
}
func (c *Client) CreateProject(ctx context.Context, in core.ProjectInput) (out core.Project, err error) {
	err = c.Do(ctx, "POST", "/v1/projects", in, &out)
	return
}
func (c *Client) ListProjects(ctx context.Context, _ core.Empty) (out core.Projects, err error) {
	err = c.Do(ctx, "GET", "/v1/projects", nil, &out)
	return
}
func (c *Client) AddMember(ctx context.Context, in core.MemberInput) (out core.User, err error) {
	err = c.Do(ctx, "POST", "/v1/members", in, &out)
	return
}
func (c *Client) ListMembers(ctx context.Context, in core.ProjectRef) (out core.Members, err error) {
	err = c.Do(ctx, "POST", "/v1/members/query", in, &out)
	return
}
func (c *Client) RecordEvent(ctx context.Context, in core.RecordInput) (out core.Event, err error) {
	err = c.Do(ctx, "POST", "/v1/events", in, &out)
	return
}
func (c *Client) GetEvent(ctx context.Context, in core.EventRef) (out core.Event, err error) {
	err = c.Do(ctx, "GET", "/v1/events/"+url.PathEscape(in.EventID), nil, &out)
	return
}
func (c *Client) QueryEvents(ctx context.Context, in core.QueryInput) (out core.Events, err error) {
	err = c.Do(ctx, "POST", "/v1/events/query", in, &out)
	return
}
func (c *Client) QueryContext(ctx context.Context, in core.ContextQueryInput) (out core.ContextQueryResult, err error) {
	err = c.Do(ctx, "POST", "/v1/context/query", in, &out)
	return
}
func (c *Client) ListMetadata(ctx context.Context, in core.MetadataInput) (out core.MetadataResult, err error) {
	err = c.Do(ctx, "POST", "/v1/metadata/query", in, &out)
	return
}
func (c *Client) GetFile(ctx context.Context, in core.FileRef) (out core.FileResult, err error) {
	err = c.Do(ctx, "GET", "/v1/files/"+url.PathEscape(in.FileID), nil, &out)
	return
}

var _ core.Backend = (*Client)(nil)
