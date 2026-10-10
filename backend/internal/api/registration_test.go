package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"event-driven-context/internal/core"
)

func TestRegistrationPolicy(t *testing.T) {
	for _, version := range []string{"v1", "v2"} {
		for _, allow := range []bool{false, true} {
			name := version + "/disabled"
			if allow {
				name = version + "/enabled"
			}
			t.Run(name, func(t *testing.T) {
				f := newV2APIFixture(t)
				config := Config{}
				if allow {
					config.AllowRegistration = true
				}
				handler := HandlerWithConfig(f.store, config)
				if version == "v2" {
					handler = V2HandlerWithConfig(f.store, f.service, config)
				}
				server := httptest.NewServer(handler)
				defer server.Close()
				credentials := core.Credentials{Username: "new-user", Email: "new@example.com", Password: "registration-password-123"}
				body, _ := json.Marshal(credentials)
				response := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/v1/auth/register", strings.NewReader(string(body)))
				request.Header.Set("Content-Type", "application/json")
				handler.ServeHTTP(response, request)
				if allow {
					if response.Code != http.StatusCreated {
						t.Fatalf("registration: %d %s", response.Code, response.Body.String())
					}
				} else {
					if response.Code != http.StatusForbidden {
						t.Fatalf("registration: %d %s", response.Code, response.Body.String())
					}
					client, err := NewClient(server.URL, "")
					if err != nil {
						t.Fatal(err)
					}
					_, err = client.Register(context.Background(), credentials)
					var apiErr *core.Error
					if !errors.As(err, &apiErr) || apiErr.Code != "registration_disabled" || !strings.Contains(apiErr.Message, "Contact the server administrator") {
						t.Fatalf("client error: %v", err)
					}
				}
				_, err := f.store.Login(context.Background(), credentials)
				if allow && err != nil {
					t.Fatalf("enabled registration did not create account: %v", err)
				}
				if !allow && err == nil {
					t.Fatal("disabled registration created an account")
				}
				// Existing sessions remain valid when signup is disabled.
				client, _ := NewClient(server.URL, "")
				if _, err := client.Me(context.Background()); err == nil {
					t.Fatal("registration policy bypassed authentication")
				}
				client.Token = f.token
				if _, err := client.Me(context.Background()); err != nil {
					t.Fatalf("existing session: %v", err)
				}
			})
		}
	}
}
