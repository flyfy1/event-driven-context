package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"event-driven-context/internal/core"
)

func TestHubOwnerApprovalAndAgentBoundaries(t *testing.T) {
	t.Setenv("EDC_HUB_CREDENTIAL_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32)))
	f := newV2APIFixture(t)
	request := func(method, path, token, origin string, body any) *httptest.ResponseRecorder {
		if body == nil {
			return f.request(t, method, path, token, origin, nil)
		}
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		return f.request(t, method, path, token, "application/json", bytes.NewReader(raw))
	}
	w := request("GET", "/v1/hub/capabilities", "", "", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("POST", "/v1/hub/agents", "", "", map[string]string{"owner": f.alice.Username, "name": "Test Agent"})
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	agent := decodeV2Response[core.HubAgentRegistration](t, w)
	for _, path := range []string{"/v1/hub/owner", "/v1/me", "/v1/projects"} {
		w = request("GET", path, agent.Token, "", nil)
		if w.Code != 401 {
			t.Fatal("agent entered owner surface", path, w.Code)
		}
	}
	w = request("GET", "/v1/hub/sources", agent.Token, "", nil)
	if w.Code != 403 {
		t.Fatal("pending agent discovers accounts", w.Code)
	}
	w = request("POST", "/v1/hub/agents/"+agent.ID+"/decision", f.bobToken, "", map[string]string{"decision": "approve", "verification_code": agent.VerificationCode})
	if w.Code != 404 {
		t.Fatal("cross owner decision", w.Code)
	}
	w = request("POST", "/v1/hub/agents/"+agent.ID+"/decision", f.token, "", map[string]string{"decision": "approve", "verification_code": agent.VerificationCode})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("POST", "/v1/hub/connections", f.token, "", map[string]string{"provider_id": "gmail", "account_id": "work@example.invalid", "display_name": "Work", "credential": "fake-token-not-used"})
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	c := decodeV2Response[core.HubConnection](t, w)
	w = request("GET", "/v1/hub/sources", agent.Token, "", nil)
	if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte("fake-token")) {
		t.Fatal("source discovery", w.Code, w.Body.String())
	}
	w = request("POST", "/v1/hub/requests", agent.Token, "", map[string]any{"connection_id": c.ID, "operation": "messages.send", "reason": "Not supported", "duration_seconds": 3600})
	if w.Code != 400 {
		t.Fatal("unknown operation accepted", w.Code)
	}
	w = request("POST", "/v1/hub/requests", agent.Token, "", map[string]any{"connection_id": c.ID, "operation": "messages.list", "reason": "Find project mail", "duration_seconds": 3600})
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	out := decodeV2Response[struct {
		Request core.HubRequest `json:"request"`
	}](t, w)
	w = request("POST", "/v1/hub/requests/"+out.Request.ID+"/decision", agent.Token, "", map[string]string{"decision": "approve"})
	if w.Code != 401 {
		t.Fatal("agent self approved", w.Code)
	}
	w = request("POST", "/v1/hub/execute", agent.Token, "", map[string]any{"connection_id": c.ID, "operation": "messages.list", "args": map[string]any{}})
	if w.Code != 403 {
		t.Fatal("executed without grant", w.Code)
	}
	w = request("POST", "/v1/hub/requests/"+out.Request.ID+"/decision", f.token, "", map[string]string{"decision": "approve"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("POST", "/v1/hub/execute", agent.Token, "", map[string]any{"connection_id": c.ID, "operation": "messages.get", "args": map[string]any{"message_id": "123"}})
	if w.Code != 403 {
		t.Fatal("grant widened", w.Code)
	}
	w = request("POST", "/v1/hub/requests/"+out.Request.ID+"/decision", f.token, "", map[string]string{"decision": "revoke"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("POST", "/v1/hub/execute", agent.Token, "", map[string]any{"connection_id": c.ID, "operation": "messages.list", "args": map[string]any{}})
	if w.Code != 403 {
		t.Fatal("revoked execution", w.Code)
	}
}

func TestHubRegistrationResponseDoesNotDiscloseOwner(t *testing.T) {
	f := newV2APIFixture(t)
	// Fill the owner's queue without consuming the HTTP gate's independent IP budget.
	for i := 0; i < 9; i++ {
		if _, err := f.store.RegisterHubAgent(context.Background(), f.alice.Username, "Queued Agent"); err != nil {
			t.Fatal(err)
		}
	}
	var shape map[string]any
	for _, owner := range []string{f.alice.Username, "missing-owner", f.alice.Username} {
		start := time.Now()
		w := f.request(t, "POST", "/v1/hub/agents", "", "application/json", v2JSONBody(t, map[string]string{"owner": owner, "name": "Test Agent"}))
		if w.Code != 201 {
			t.Fatalf("registration: %d %s", w.Code, w.Body.String())
		}
		if time.Since(start) < 100*time.Millisecond {
			t.Fatal("registration bypassed timing floor")
		}
		out := decodeV2Response[map[string]any](t, w)
		if len(out) != 8 || out["status"] != "pending" || out["name"] != "Test Agent" || out["approval_url"] == "" {
			t.Fatalf("unexpected shape: %v", out)
		}
		if shape != nil {
			for key, value := range shape {
				if reflect.TypeOf(out[key]) != reflect.TypeOf(value) {
					t.Fatalf("field %s differs", key)
				}
			}
		}
		shape = out
	}
}
