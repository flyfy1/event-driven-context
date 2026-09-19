// Package hubpush sends generic authorization reminders through optional Android FCM.
package hubpush

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"
)

var ErrUnregistered = errors.New("push device is unregistered")
var errDelivery = errors.New("push delivery failed")

type Sender struct {
	client   *http.Client
	tokens   oauth2.TokenSource
	endpoint string
}

// FromEnv reads only the explicitly configured service account file. Neither credentials
// nor provider error bodies are returned or logged. Missing configuration disables push.
func FromEnv() (*Sender, error) {
	project, path := os.Getenv("EDC_HUB_FCM_PROJECT_ID"), os.Getenv("EDC_HUB_FCM_SERVICE_ACCOUNT_FILE")
	if project == "" && path == "" {
		return nil, nil
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9-]{4,61}[a-z0-9]$`).MatchString(project) || path == "" {
		return nil, errors.New("invalid FCM configuration")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("FCM credential file unavailable")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(b) > 65536 {
		return nil, errors.New("invalid FCM credential file")
	}
	var credential struct {
		Type     string `json:"type"`
		Email    string `json:"client_email"`
		Key      string `json:"private_key"`
		KeyID    string `json:"private_key_id"`
		TokenURI string `json:"token_uri"`
	}
	if json.Unmarshal(b, &credential) != nil || credential.Type != "service_account" || credential.Email == "" || credential.Key == "" || (credential.TokenURI != "" && credential.TokenURI != "https://oauth2.googleapis.com/token") {
		return nil, errors.New("invalid FCM service account")
	}
	block, _ := pem.Decode([]byte(credential.Key))
	if block == nil {
		return nil, errors.New("invalid FCM private key")
	}
	parsed, parseErr := x509.ParsePKCS8PrivateKey(block.Bytes)
	if parseErr != nil {
		parsed, parseErr = x509.ParsePKCS1PrivateKey(block.Bytes)
	}
	privateKey, isRSA := parsed.(*rsa.PrivateKey)
	if parseErr != nil || !isRSA || privateKey.N.BitLen() < 2048 {
		return nil, errors.New("invalid FCM private key")
	}
	// Token exchange uses a pinned endpoint and a bounded client; credentials cannot select a host.
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	cfg := jwt.Config{Email: credential.Email, PrivateKey: []byte(credential.Key), PrivateKeyID: credential.KeyID, Scopes: []string{"https://www.googleapis.com/auth/firebase.messaging"}, TokenURL: "https://oauth2.googleapis.com/token"}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, client)
	return &Sender{client: client, tokens: cfg.TokenSource(ctx), endpoint: "https://fcm.googleapis.com/v1/projects/" + project + "/messages:send"}, nil
}

// Send carries no request details, names, reasons, or provider content. The app must
// authenticate to retrieve pending authorizations before displaying a local notification.
func (s *Sender) Send(ctx context.Context, device string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tok, err := s.tokens.Token()
	if err != nil {
		return errDelivery
	}
	payload := map[string]any{"message": map[string]any{
		"token":   device,
		"data":    map[string]string{"type": "authorization_changed"},
		"android": map[string]any{"priority": "HIGH", "ttl": "300s", "collapse_key": "hub_authorizations"},
	}}
	b, err := json.Marshal(payload)
	if err != nil {
		return errDelivery
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(b))
	if err != nil {
		return errDelivery
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	resp, err := s.client.Do(req)
	if err != nil {
		return errDelivery
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	var body struct {
		Error struct {
			Details []struct {
				ErrorCode string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&body)
	for _, d := range body.Error.Details {
		if d.ErrorCode == "UNREGISTERED" {
			return ErrUnregistered
		}
	}
	return errDelivery
}
