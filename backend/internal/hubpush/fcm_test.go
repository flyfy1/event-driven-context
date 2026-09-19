package hubpush

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestSendContainsOnlyGenericDataAndHandlesUnregistered(t *testing.T) {
	status := 200
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer fake-access" {
			t.Error("invalid authenticated request")
		}
		var in struct {
			Message struct {
				Token        string            `json:"token"`
				Data         map[string]string `json:"data"`
				Notification json.RawMessage   `json:"notification"`
			} `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Fatal(err)
		}
		if in.Message.Token != "fake-device" || in.Message.Data["type"] != "authorization_changed" || len(in.Message.Data) != 1 || len(in.Message.Notification) != 0 {
			t.Error("push leaked extra content or notification payload")
		}
		w.WriteHeader(status)
		if status == 404 {
			_, _ = w.Write([]byte(`{"error":{"details":[{"errorCode":"UNREGISTERED"}]}}`))
		}
		if status == 403 {
			_, _ = w.Write([]byte(`{"error":{"message":"private provider contents"}}`))
		}
	})
	sender := &Sender{client: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Result(), nil
	})}, tokens: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "fake-access"}), endpoint: "https://fcm.googleapis.com/v1/projects/test-project/messages:send"}
	if err := sender.Send(context.Background(), "fake-device"); err != nil {
		t.Fatal(err)
	}
	status = 404
	if err := sender.Send(context.Background(), "fake-device"); !errors.Is(err, ErrUnregistered) {
		t.Fatalf("unregistered = %v", err)
	}
	status = 403
	if err := sender.Send(context.Background(), "fake-device"); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("provider error leaked")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	if err := sender.Send(ctx, "fake-device"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline = %v", err)
	}
}

func TestFromEnvDisabledAndIncomplete(t *testing.T) {
	t.Setenv("EDC_HUB_FCM_PROJECT_ID", "")
	t.Setenv("EDC_HUB_FCM_SERVICE_ACCOUNT_FILE", "")
	if s, err := FromEnv(); s != nil || err != nil {
		t.Fatal("default should disable push")
	}
	t.Setenv("EDC_HUB_FCM_PROJECT_ID", "invalid/project")
	if s, err := FromEnv(); s != nil || err == nil {
		t.Fatal("partial configuration allowed")
	}
}

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
