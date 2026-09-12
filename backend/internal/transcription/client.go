package transcription

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"strings"
	"time"
)

const MaxAudioBytes int64 = 25_000_000

type Options struct {
	Language string
	Prompt   string
}

type Result struct {
	Text  string
	Model string
}

type Transcriber interface {
	Transcribe(context.Context, string, string, int64, io.Reader, Options) (Result, error)
}

type Client struct {
	apiKey     string
	model      string
	endpoint   string
	httpClient *http.Client
}

func NewClient(apiKey, baseURL, model string, timeout time.Duration) (*Client, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required")
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid OpenAI base URL")
	}
	if model = strings.TrimSpace(model); model == "" {
		model = "gpt-transcribe"
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	u.Path = path.Join(u.Path, "audio/transcriptions")
	return &Client{apiKey: apiKey, model: model, endpoint: u.String(), httpClient: &http.Client{Timeout: timeout}}, nil
}

func (c *Client) Transcribe(ctx context.Context, filename, mediaType string, size int64, reader io.Reader, options Options) (Result, error) {
	if reader == nil || size < 1 || size > MaxAudioBytes {
		return Result{}, fmt.Errorf("audio must be 1 byte to 25 MB")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", c.model); err != nil {
		return Result{}, err
	}
	if language := strings.TrimSpace(options.Language); language != "" {
		if err := writer.WriteField("language", language); err != nil {
			return Result{}, err
		}
	}
	if prompt := strings.TrimSpace(options.Prompt); prompt != "" {
		if err := writer.WriteField("prompt", prompt); err != nil {
			return Result{}, err
		}
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": filename}))
	header.Set("Content-Type", mediaType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return Result{}, err
	}
	n, err := io.Copy(part, io.LimitReader(reader, MaxAudioBytes+1))
	if err != nil {
		return Result{}, err
	}
	if n != size {
		return Result{}, fmt.Errorf("audio size changed while reading")
	}
	if err = writer.Close(); err != nil {
		return Result{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, &body)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	res, err := c.httpClient.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("OpenAI transcription request failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		return Result{}, fmt.Errorf("OpenAI transcription returned HTTP %d", res.StatusCode)
	}
	var payload struct {
		Text string `json:"text"`
	}
	decoder := json.NewDecoder(io.LimitReader(res.Body, 2<<20))
	if err = decoder.Decode(&payload); err != nil {
		return Result{}, fmt.Errorf("OpenAI transcription returned invalid JSON")
	}
	payload.Text = strings.TrimSpace(payload.Text)
	if payload.Text == "" {
		return Result{}, fmt.Errorf("OpenAI transcription returned empty text")
	}
	return Result{Text: payload.Text, Model: c.model}, nil
}
