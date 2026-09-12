package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"event-driven-context/internal/automation"
)

const maxAPIResponseBytes = 4 << 20

type runnerClient struct {
	baseURL        string
	token          string
	http           *http.Client
	requestTimeout time.Duration
}

type remoteError struct {
	Status int
	Code   string
}

func (e *remoteError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("server returned HTTP %d", e.Status)
	}
	return fmt.Sprintf("server returned %s (HTTP %d)", e.Code, e.Status)
}

func newRunnerClient(base, token string, requestTimeout time.Duration) (*runnerClient, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("server must be an absolute HTTP(S) origin URL")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return nil, fmt.Errorf("remote servers require HTTPS; HTTP is allowed only on loopback")
	}
	if token == "" {
		return nil, fmt.Errorf("runner token is required")
	}
	return &runnerClient{
		baseURL: strings.TrimRight(base, "/"), token: token, requestTimeout: requestTimeout,
		http: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

func (c *runnerClient) dispatch(ctx context.Context) (*automation.Claim, error) {
	requestCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()
	request, err := c.request(requestCtx, http.MethodPost, "/v1/runner/dispatch", nil, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
		return nil, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, decodeRemoteError(response)
	}
	var claim automation.Claim
	if err = decodeBoundedJSON(response.Body, maxAPIResponseBytes, &claim); err != nil {
		return nil, fmt.Errorf("decode dispatch response: %w", err)
	}
	return &claim, nil
}

func (c *runnerClient) heartbeat(ctx context.Context, runID, attemptID string, fencing uint64) (automation.Run, error) {
	var run automation.Run
	err := c.doJSON(ctx, http.MethodPost, runPath(runID, "heartbeat"), attemptID, fencing, nil, &run)
	return run, err
}

func (c *runnerClient) submit(ctx context.Context, value pendingCandidate) error {
	input := automation.SubmitInput{Candidate: &value.Candidate}
	return c.doJSON(ctx, http.MethodPost, runPath(value.RunID, "submit"), value.AttemptID, value.FencingToken, input, &automation.Run{})
}

func (c *runnerClient) fail(ctx context.Context, runID, attemptID string, fencing uint64, input automation.FailureInput) error {
	return c.doJSON(ctx, http.MethodPost, runPath(runID, "fail"), attemptID, fencing, input, &automation.Run{})
}

func (c *runnerClient) download(ctx context.Context, runID, attemptID string, fencing uint64, fileID string) (io.ReadCloser, int64, error) {
	requestCtx, cancel := context.WithTimeout(ctx, maxDuration(c.requestTimeout, 2*time.Minute))
	request, err := c.request(requestCtx, http.MethodGet, "/v1/runner/runs/"+url.PathEscape(runID)+"/input-files/"+url.PathEscape(fileID)+"/content", nil, attemptHeaders(attemptID, fencing))
	if err != nil {
		cancel()
		return nil, 0, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		cancel()
		return nil, 0, err
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		err = decodeRemoteError(response)
		cancel()
		return nil, 0, err
	}
	return &cancelReadCloser{ReadCloser: response.Body, cancel: cancel}, response.ContentLength, nil
}

func (c *runnerClient) doJSON(ctx context.Context, method, path, attemptID string, fencing uint64, input, output any) error {
	requestCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()
	request, err := c.request(requestCtx, method, path, input, attemptHeaders(attemptID, fencing))
	if err != nil {
		return err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return decodeRemoteError(response)
	}
	return decodeBoundedJSON(response.Body, maxAPIResponseBytes, output)
}

func (c *runnerClient) request(ctx context.Context, method, path string, input any, headers http.Header) (*http.Request, error) {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	return request, nil
}

func decodeRemoteError(response *http.Response) error {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&envelope)
	return &remoteError{Status: response.StatusCode, Code: envelope.Error.Code}
}

func decodeBoundedJSON(body io.Reader, limit int64, output any) error {
	limited := io.LimitReader(body, limit+1)
	contents, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if int64(len(contents)) > limit {
		return fmt.Errorf("response exceeds %d bytes", limit)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(output); err != nil {
		return err
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("response contains trailing data")
	}
	return nil
}

func attemptHeaders(attemptID string, fencing uint64) http.Header {
	headers := make(http.Header)
	headers.Set("X-EDC-Attempt-ID", attemptID)
	headers.Set("X-EDC-Fencing-Token", strconv.FormatUint(fencing, 10))
	return headers
}

func runPath(runID, action string) string {
	return "/v1/runner/runs/" + url.PathEscape(runID) + "/" + action
}

type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r *cancelReadCloser) Close() error {
	err := r.ReadCloser.Close()
	r.cancel()
	return err
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
