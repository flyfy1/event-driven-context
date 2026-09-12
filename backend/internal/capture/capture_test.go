package capture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"event-driven-context/internal/v2"
	edcrecorder "event-driven-context/skills/edc-recorder"
)

type fakeSender struct {
	mu      sync.Mutex
	events  []v2.EventInput
	fail    error
	plugins []v2.Installation
	states  map[string]v2.State
}

func (f *fakeSender) RecordEvents(_ context.Context, _ string, in v2.RecordEventsInput) (v2.RecordEventsResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return v2.RecordEventsResult{}, f.fail
	}
	results := make([]v2.EventWriteResult, len(in.Events))
	for i, event := range in.Events {
		status := "created"
		for _, old := range f.events {
			if old.ID == event.ID {
				oldJSON, _ := json.Marshal(old)
				newJSON, _ := json.Marshal(event)
				if bytes.Equal(oldJSON, newJSON) {
					status = "duplicate"
				} else {
					status = "conflict"
				}
				break
			}
		}
		f.events = append(f.events, event)
		results[i] = v2.EventWriteResult{ID: event.ID, Status: status, Sequence: int64(len(f.events))}
		if status == "conflict" {
			results[i].Error = &v2.Error{Code: "conflict", Message: "id reused with different content"}
		}
	}
	return v2.RecordEventsResult{Results: results}, nil
}

func (f *fakeSender) ListPlugins(context.Context, string) ([]v2.Installation, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	return f.plugins, nil
}

func (f *fakeSender) GetState(_ context.Context, _ string, key string, _ *int64) (v2.State, error) {
	state, ok := f.states[key]
	if !ok {
		return v2.State{}, errors.New("not found")
	}
	return state, nil
}

func newTestManager(t *testing.T, sender Sender) *Manager {
	t.Helper()
	m, err := Open(filepath.Join(t.TempDir(), "capture"), sender)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNormalizeClaudeCodeStableIDRedactionAndUTF8Truncation(t *testing.T) {
	in := HookInput{SessionID: "session-1", PromptID: "550e8400-e29b-41d4-a716-446655440000", CWD: t.TempDir(), HookEventName: "UserPromptSubmit", Prompt: `deploy {"password":"value with spaces","api_key":"ordinary-value"} sk-proj-abcdefghijklmnop ` + strings.Repeat("界", MaxHookField)}
	one, err := NormalizeClaudeCode(in)
	if err != nil {
		t.Fatal(err)
	}
	two, err := NormalizeClaudeCode(in)
	if err != nil || one.Event.ID != two.Event.ID || !one.StableID {
		t.Fatalf("stable normalization failed: %v %s %s", err, one.Event.ID, two.Event.ID)
	}
	text := one.Event.Content.Text
	if strings.Contains(text, "value with spaces") || strings.Contains(text, "ordinary-value") || strings.Contains(text, "abcdefghijklmnop") {
		t.Fatalf("secret remained in normalized hook: %q", text[:min(len(text), 200)])
	}
	if !strings.Contains(text, "[REDACTED]") || len(text) > MaxHookField || !strings.HasSuffix(string([]rune(text)[len([]rune(text))-1:]), "界") {
		t.Fatalf("redaction/truncation invalid, bytes=%d", len(text))
	}
	if _, ok := one.Event.Metadata["truncated"]; !ok {
		t.Fatal("truncation metadata missing")
	}

	in.PromptID = ""
	three, _ := NormalizeClaudeCode(in)
	four, _ := NormalizeClaudeCode(in)
	if three.Event.ID == four.Event.ID || three.StableID || four.StableID {
		t.Fatal("hooks without a stable prompt identifier must get persisted UUIDv7 ids")
	}
}

func TestNormalizeCodexUsesTurnIDAndCurrentWireFields(t *testing.T) {
	dir := t.TempDir()
	in := HookInput{
		SessionID:     "session-1",
		TurnID:        "turn-1",
		CWD:           dir,
		HookEventName: "UserPromptSubmit",
		Prompt:        "ship with api_key=ordinary-secret-value",
		Model:         "gpt-test",
	}
	one, err := NormalizeCodex(in)
	if err != nil {
		t.Fatal(err)
	}
	two, err := NormalizeCodex(in)
	if err != nil || one.Event.ID != two.Event.ID || !one.StableID {
		t.Fatalf("stable Codex normalization failed: %v %#v %#v", err, one, two)
	}
	if one.Event.Content.Text != "ship with api_key= [REDACTED]" {
		t.Fatalf("Codex prompt was not redacted: %q", one.Event.Content.Text)
	}
	if string(one.Event.Source["client"]) != `"codex"` || string(one.Event.Metadata["kind"]) != `"user_message"` {
		t.Fatalf("Codex source or metadata mismatch: %#v %#v", one.Event.Source, one.Event.Metadata)
	}

	stop := in
	stop.HookEventName = "Stop"
	stop.Prompt = ""
	stop.LastAssistantMessage = "done"
	normalizedStop, err := NormalizeCodex(stop)
	if err != nil || normalizedStop.Event.ID == one.Event.ID || normalizedStop.Reminder {
		t.Fatalf("Codex Stop normalization failed: %v %#v", err, normalizedStop)
	}
	stop.LastAssistantMessage = ""
	missingMessage, err := NormalizeCodex(stop)
	if err != nil || string(missingMessage.Event.Metadata["message_missing"]) != "true" {
		t.Fatalf("Codex nullable Stop message failed: %v %#v", err, missingMessage)
	}

	start := HookInput{SessionID: "session-1", CWD: dir, HookEventName: "SessionStart", Source: "startup"}
	normalizedStart, err := NormalizeCodex(start)
	if err != nil || normalizedStart.StableID || !normalizedStart.LoadContext {
		t.Fatalf("Codex SessionStart normalization failed: %v %#v", err, normalizedStart)
	}
}

func TestDecodeCurrentClaudeHookToleratesDocumentedExtraFields(t *testing.T) {
	raw := `{"session_id":"s","prompt_id":"p","transcript_path":"/tmp/t","cwd":"/tmp","scratchpad_dir":"/tmp/s","permission_mode":"default","effort":{"level":"high"},"hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"done","background_tasks":[],"session_crons":[]}`
	if _, err := DecodeHookInput(strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestLinkUsesLongestDirectoryAndIsolatesAccounts(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "nested")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	m := newTestManager(t, &fakeSender{})
	ctx := context.Background()
	if _, err := m.Link(ctx, root, "project-a", "https://context.example", "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Link(ctx, child, "project-b", "https://context.example", "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Link(ctx, root, "project-c", "https://context.example", "bob"); err != nil {
		t.Fatal(err)
	}
	alice, err := m.CurrentLink(child, "https://context.example", "alice")
	if err != nil || alice.ProjectID != "project-b" {
		t.Fatalf("alice binding %#v err=%v", alice, err)
	}
	bob, err := m.CurrentLink(child, "https://context.example", "bob")
	if err != nil || bob.ProjectID != "project-c" {
		t.Fatalf("bob binding %#v err=%v", bob, err)
	}
}

func TestOutboxPersistsRetriesAndCorruptItemDoesNotBlockValidItem(t *testing.T) {
	sender := &fakeSender{fail: errors.New("offline")}
	m := newTestManager(t, sender)
	dir := t.TempDir()
	binding, err := m.Link(context.Background(), dir, "project", "https://context.example", "alice")
	if err != nil {
		t.Fatal(err)
	}
	event := v2.EventInput{ID: "11111111-1111-4111-8111-111111111111", Type: "log", Content: v2.EventContent{Kind: "text", Text: "kept"}, Source: map[string]json.RawMessage{"channel": json.RawMessage(`"hook"`)}}
	if _, err = m.enqueue(binding, event); err != nil {
		t.Fatal(err)
	}
	if result, flushErr := m.Flush(context.Background(), binding); flushErr == nil || result.Pending != 1 {
		t.Fatalf("offline flush %#v err=%v", result, flushErr)
	}

	// Simulate a damaged file from an older/interrupted writer. It is reported,
	// while the valid event is still delivered.
	outboxDir := filepath.Join(m.root, "outbox", scopeKey(binding))
	if err = os.WriteFile(filepath.Join(outboxDir, "22222222-2222-4222-8222-222222222222.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	sender.fail = nil
	result, flushErr := m.Flush(context.Background(), binding)
	if flushErr == nil || result.Delivered != 1 || result.Pending != 1 {
		t.Fatalf("corrupt isolation %#v err=%v", result, flushErr)
	}
	if len(sender.events) != 1 || sender.events[0].ID != event.ID {
		t.Fatalf("valid item not delivered: %#v", sender.events)
	}
}

func TestFlushPermanentFailureDoesNotBlockLaterItems(t *testing.T) {
	conflicting := v2.EventInput{ID: "44444444-4444-4444-8444-444444444444", Type: "log", Content: v2.EventContent{Kind: "text", Text: "server copy"}}
	sender := &fakeSender{events: []v2.EventInput{conflicting}}
	m := newTestManager(t, sender)
	binding := Binding{ProjectID: "project", Server: "https://context.example", AccountID: "alice"}
	first := conflicting
	first.Content.Text = "client copy"
	second := v2.EventInput{ID: "55555555-5555-4555-8555-555555555555", Type: "log", Content: v2.EventContent{Kind: "text", Text: "later valid"}}
	if _, err := m.Enqueue(binding, []v2.EventInput{first, second}); err != nil {
		t.Fatal(err)
	}
	result, err := m.Flush(context.Background(), binding)
	if err == nil || result.Delivered != 1 || result.Pending != 1 {
		t.Fatalf("flush result=%#v err=%v", result, err)
	}
	if sender.events[len(sender.events)-1].ID != second.ID {
		t.Fatalf("later item was blocked: %#v", sender.events)
	}
}

func TestEnqueueIsIdempotentAndAccountScoped(t *testing.T) {
	m := newTestManager(t, &fakeSender{})
	event := v2.EventInput{ID: "33333333-3333-4333-8333-333333333333", Type: "note", Content: v2.EventContent{Kind: "text", Text: "offline"}, Source: map[string]json.RawMessage{"channel": json.RawMessage(`"cli"`)}}
	alice := Binding{ProjectID: "project", Server: "https://context.example", AccountID: "alice"}
	bob := Binding{ProjectID: "project", Server: "https://context.example", AccountID: "bob"}
	if added, err := m.Enqueue(alice, []v2.EventInput{event}); err != nil || added != 1 {
		t.Fatalf("first enqueue added=%d err=%v", added, err)
	}
	if added, err := m.Enqueue(alice, []v2.EventInput{event}); err != nil || added != 0 {
		t.Fatalf("idempotent enqueue added=%d err=%v", added, err)
	}
	if added, err := m.Enqueue(bob, []v2.EventInput{event}); err != nil || added != 1 {
		t.Fatalf("account-scoped enqueue added=%d err=%v", added, err)
	}
	changed := event
	changed.Content.Text = "different"
	if _, err := m.Enqueue(alice, []v2.EventInput{changed}); err == nil {
		t.Fatal("same UUID with different content was accepted")
	}
	if aliceItems, _ := m.OutboxList(alice); len(aliceItems) != 1 {
		t.Fatalf("alice outbox=%d", len(aliceItems))
	}
	if bobItems, _ := m.OutboxList(bob); len(bobItems) != 1 {
		t.Fatalf("bob outbox=%d", len(bobItems))
	}
}

func TestHandleHookQueuesBeforeDeliveryDeduplicatesAndLoadsContext(t *testing.T) {
	sender := &fakeSender{
		plugins: []v2.Installation{{ProjectID: "project", PluginID: "project-brief", Status: "active", Manifest: v2.Manifest{SessionContext: []string{"current", "_cursor"}}}},
		states:  map[string]v2.State{"project-brief/current": {ProjectID: "project", Key: "project-brief/current", Version: 2, Content: v2.StateContent{Format: "markdown", Text: "## Todo\n- Ship"}, BasedOnSequence: 4, Lag: 1}},
	}
	m := newTestManager(t, sender)
	dir := t.TempDir()
	binding, err := m.Link(context.Background(), dir, "project", "https://context.example", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err = writeJSONAtomic(m.captureScopePath(binding, ClaudeCode), captureScope{Version: 1, Client: ClaudeCode, Enabled: true}, 0o600); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"session_id": "session", "cwd": dir, "hook_event_name": "SessionStart", "source": "startup", "model": "claude-sonnet"}
	raw, _ := json.Marshal(payload)
	var output bytes.Buffer
	first, err := m.HandleHook(context.Background(), ClaudeCode, "https://context.example", "alice", bytes.NewReader(raw), &output)
	if err != nil || !first.Delivered || first.ContextLoaded != 1 {
		t.Fatalf("first hook %#v err=%v", first, err)
	}
	if !strings.Contains(output.String(), "project-brief/current") || strings.Contains(output.String(), "_cursor") {
		t.Fatalf("bad SessionStart context: %s", output.String())
	}
	second, err := m.HandleHook(context.Background(), ClaudeCode, "https://context.example", "alice", bytes.NewReader(raw), &bytes.Buffer{})
	if err != nil || first.EventID != second.EventID || !second.Delivered {
		t.Fatalf("retry hook %#v err=%v", second, err)
	}
	if len(sender.events) != 2 || sender.events[0].ID != sender.events[1].ID {
		t.Fatalf("server retry did not reuse id: %#v", sender.events)
	}
}

func TestStopContinuationGetsDistinctStableIDAndOneReminder(t *testing.T) {
	sender := &fakeSender{}
	m := newTestManager(t, sender)
	dir := t.TempDir()
	binding, err := m.Link(context.Background(), dir, "project", "https://context.example", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err = writeJSONAtomic(m.captureScopePath(binding, ClaudeCode), captureScope{Version: 1, Client: ClaudeCode, Enabled: true}, 0o600); err != nil {
		t.Fatal(err)
	}
	makeInput := func(active bool, message string) []byte {
		b, _ := json.Marshal(map[string]any{"session_id": "session", "prompt_id": "550e8400-e29b-41d4-a716-446655440000", "cwd": dir, "hook_event_name": "Stop", "stop_hook_active": active, "last_assistant_message": message, "background_tasks": []any{}, "session_crons": []any{}})
		return b
	}
	var firstOut bytes.Buffer
	first, err := m.HandleHook(context.Background(), ClaudeCode, binding.Server, binding.AccountID, bytes.NewReader(makeInput(false, "first answer")), &firstOut)
	if err != nil || !strings.Contains(firstOut.String(), "edc-recorder") {
		t.Fatalf("first Stop %#v output=%s err=%v", first, firstOut.String(), err)
	}
	var secondOut bytes.Buffer
	second, err := m.HandleHook(context.Background(), ClaudeCode, binding.Server, binding.AccountID, bytes.NewReader(makeInput(true, "continued answer")), &secondOut)
	if err != nil || second.EventID == first.EventID || secondOut.Len() != 0 {
		t.Fatalf("continued Stop %#v output=%s err=%v", second, secondOut.String(), err)
	}
	retry, err := m.HandleHook(context.Background(), ClaudeCode, binding.Server, binding.AccountID, bytes.NewReader(makeInput(true, "continued answer")), &bytes.Buffer{})
	if err != nil || retry.EventID != second.EventID {
		t.Fatalf("continued retry %#v err=%v", retry, err)
	}
}

func TestSetupPreviewRequiresApprovalPreservesOtherSettingsAndDetectsStaleFile(t *testing.T) {
	project := t.TempDir()
	edc := filepath.Join(t.TempDir(), "edc")
	if err := os.WriteFile(edc, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(project, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"permissions":{"allow":["Read"]},"unrelated":{"Authorization":"Bearer synthetic-private-value","env":{"MY_API_KEY":"synthetic-env-secret","SLACK_BOT_TOKEN":"synthetic-slack-secret"}},"hooks":{"PostToolUse":[{"hooks":[{"type":"command","command":"fmt"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	mcpConfig := filepath.Join(project, ".mcp.json")
	if err := os.WriteFile(mcpConfig, []byte(`{"mcpServers":{"event-driven-context":{"type":"stdio","command":"/old/edc","args":["mcp"]},"keep-me":{"type":"http","url":"https://example.invalid/mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newTestManager(t, &fakeSender{})
	if _, err := m.Link(context.Background(), project, "project", "https://context.example", "alice"); err != nil {
		t.Fatal(err)
	}
	preview, err := m.SetupPreview(SetupOptions{Directory: project, Client: ClaudeCode, EDCPath: edc, ConfigPath: filepath.Join(t.TempDir(), "config.json"), Server: "https://context.example", AccountID: "alice"})
	if err != nil || len(preview.Changes) != 4 || !strings.Contains(preview.Changes[0].Diff, "SessionStart") {
		t.Fatalf("preview %#v err=%v", preview, err)
	}
	for _, change := range preview.Changes {
		if strings.Contains(change.Diff, "synthetic-private-value") || strings.Contains(change.Diff, "synthetic-env-secret") || strings.Contains(change.Diff, "synthetic-slack-secret") {
			t.Fatal("setup preview leaked unrelated credential")
		}
	}
	if err = m.ApplySetup(preview, false); err == nil {
		t.Fatal("unapproved setup applied")
	}
	before, _ := os.ReadFile(settings)
	if strings.Contains(string(before), "SessionStart") {
		t.Fatal("preview changed settings")
	}
	if err = m.ApplySetup(preview, true); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(settings)
	if !strings.Contains(string(after), `"PostToolUse"`) || !strings.Contains(string(after), `"SessionStart"`) || !strings.Contains(string(after), `"timeout": 5`) {
		t.Fatalf("settings were not merged: %s", after)
	}
	if !strings.Contains(string(after), "synthetic-private-value") || !strings.Contains(string(after), "synthetic-env-secret") || !strings.Contains(string(after), "synthetic-slack-secret") {
		t.Fatal("setup changed an unrelated credential field")
	}
	mcpAfter, _ := os.ReadFile(mcpConfig)
	if strings.Contains(string(mcpAfter), "event-driven-context") || !strings.Contains(string(mcpAfter), "keep-me") {
		t.Fatalf("legacy EDC MCP entry not removed safely: %s", mcpAfter)
	}
	skill, _ := os.ReadFile(filepath.Join(project, ".claude", "skills", "edc-recorder", "SKILL.md"))
	if !bytes.Equal(skill, edcrecorder.Content) {
		t.Fatal("installed skill differs from embedded recorder")
	}

	stale, err := m.SetupPreview(SetupOptions{Directory: project, Client: ClaudeCode, EDCPath: edc, ConfigPath: filepath.Join(t.TempDir(), "config.json"), Server: "https://context.example", AccountID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(settings, append(after, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = m.ApplySetup(stale, true); err == nil || !strings.Contains(err.Error(), "changed after preview") {
		t.Fatalf("stale preview accepted: %v", err)
	}
}

func TestSetupWithoutLegacyMCPDoesNotCreateMCPConfig(t *testing.T) {
	project := t.TempDir()
	edc := filepath.Join(t.TempDir(), "edc")
	if err := os.WriteFile(edc, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	m := newTestManager(t, &fakeSender{})
	if _, err := m.Link(context.Background(), project, "project", "https://context.example", "alice"); err != nil {
		t.Fatal(err)
	}
	preview, err := m.SetupPreview(SetupOptions{Directory: project, Client: ClaudeCode, EDCPath: edc, ConfigPath: filepath.Join(t.TempDir(), "config.json"), Server: "https://context.example", AccountID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Changes) != 3 {
		t.Fatalf("unexpected setup changes: %#v", preview.Changes)
	}
	for _, change := range preview.Changes {
		if filepath.Base(change.Path) == ".mcp.json" {
			t.Fatal("direct CLI setup attempted to create .mcp.json")
		}
	}
	if err = m.ApplySetup(preview, true); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(project, ".mcp.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("MCP config created: %v", err)
	}
}

func TestCodexSetupPreservesExistingHooksAndInstallsRecorder(t *testing.T) {
	project := t.TempDir()
	edc := filepath.Join(t.TempDir(), "edc")
	if err := os.WriteFile(edc, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(project, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hooksPath, []byte(`{"description":"keep","hooks":{"Stop":[{"hooks":[{"type":"command","command":"keep-me"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	m := newTestManager(t, &fakeSender{})
	binding, err := m.Link(context.Background(), project, "project", "https://context.example", "alice")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := m.SetupPreview(SetupOptions{Directory: project, Client: Codex, EDCPath: edc, ConfigPath: filepath.Join(t.TempDir(), "config.json"), Server: binding.Server, AccountID: binding.AccountID})
	firstPath := ""
	if len(preview.Changes) > 0 {
		firstPath = preview.Changes[0].Path
	}
	if err != nil || len(preview.Changes) != 3 || !strings.HasSuffix(firstPath, filepath.Join(".codex", "hooks.json")) {
		t.Fatalf("Codex preview changes=%d first=%q err=%v", len(preview.Changes), firstPath, err)
	}
	if err = m.ApplySetup(preview, true); err != nil {
		t.Fatal(err)
	}
	hooks, err := os.ReadFile(hooksPath)
	if err != nil || !bytes.Contains(hooks, []byte("keep-me")) || !bytes.Contains(hooks, []byte("hook codex")) || !bytes.Contains(hooks, []byte(`"timeout": 3`)) {
		t.Fatalf("Codex hooks were not merged safely: %s err=%v", hooks, err)
	}
	skill, err := os.ReadFile(filepath.Join(project, ".agents", "skills", "edc-recorder", "SKILL.md"))
	if err != nil || !bytes.Equal(skill, edcrecorder.Content) {
		t.Fatalf("Codex recorder install mismatch: %v", err)
	}
	status, err := m.Status(project, binding.Server, binding.AccountID)
	if err != nil || !status.HooksEnabled || !status.HookClients[Codex] || status.HookClients[ClaudeCode] {
		t.Fatalf("Codex status mismatch: %#v err=%v", status, err)
	}
}

func TestHandleCodexHookQueuesAndDelivers(t *testing.T) {
	sender := &fakeSender{}
	m := newTestManager(t, sender)
	dir := t.TempDir()
	binding, err := m.Link(context.Background(), dir, "project", "https://context.example", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err = writeJSONAtomic(m.captureScopePath(binding, Codex), captureScope{Version: 1, Client: Codex, Enabled: true}, 0o600); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{
		"session_id":      "session",
		"turn_id":         "turn",
		"cwd":             dir,
		"hook_event_name": "UserPromptSubmit",
		"prompt":          "Codex hook text",
		"permission_mode": "default",
	})
	result, err := m.HandleHook(context.Background(), Codex, binding.Server, binding.AccountID, bytes.NewReader(payload), &bytes.Buffer{})
	if err != nil || !result.Delivered || len(sender.events) != 1 || sender.events[0].Content.Text != "Codex hook text" {
		t.Fatalf("Codex hook result=%#v events=%#v err=%v", result, sender.events, err)
	}
}

func TestDisableHooksIsProjectAccountScopedEvenWhenOldCommandRemains(t *testing.T) {
	project := t.TempDir()
	makeEDC := func(name string) string {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte("binary"), 0o700); err != nil {
			t.Fatal(err)
		}
		return path
	}
	sender := &fakeSender{}
	m := newTestManager(t, sender)
	binding, err := m.Link(context.Background(), project, "project", "https://context.example", "alice")
	if err != nil {
		t.Fatal(err)
	}
	enable, err := m.SetupPreview(SetupOptions{Directory: project, Client: ClaudeCode, EDCPath: makeEDC("edc-a"), ConfigPath: filepath.Join(t.TempDir(), "a.json"), Server: binding.Server, AccountID: binding.AccountID})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.ApplySetup(enable, true); err != nil {
		t.Fatal(err)
	}
	disable, err := m.SetupPreview(SetupOptions{Directory: project, Client: ClaudeCode, EDCPath: makeEDC("edc-b"), ConfigPath: filepath.Join(t.TempDir(), "b.json"), Server: binding.Server, AccountID: binding.AccountID, DisableHooks: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.ApplySetup(disable, true); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"session_id": "s", "prompt_id": "p", "cwd": project, "hook_event_name": "UserPromptSubmit", "prompt": "must not capture"})
	result, err := m.HandleHook(context.Background(), ClaudeCode, binding.Server, binding.AccountID, bytes.NewReader(raw), &bytes.Buffer{})
	if err != nil || result.EventID != "" || len(sender.events) != 0 {
		t.Fatalf("disabled old hook captured %#v events=%d err=%v", result, len(sender.events), err)
	}
}
