package capture

import (
	"context"
	"encoding/json"
	"io/fs"
	"time"

	"event-driven-context/internal/v2"
)

const (
	ClaudeCode       = "claude-code"
	Codex            = "codex"
	MaxHookInput     = 1 << 20
	MaxHookField     = 16 << 10
	maxContextOutput = 64 << 10
)

// Sender is the public V2 surface used by capture. v2client.Client satisfies it.
// Keeping this interface transport-only prevents hooks from reaching into the
// service data directory or the legacy automation coordinator.
type Sender interface {
	RecordEvents(context.Context, string, v2.RecordEventsInput) (v2.RecordEventsResult, error)
	ListPlugins(context.Context, string) ([]v2.Installation, error)
	GetState(context.Context, string, string, *int64) (v2.State, error)
}

type Binding struct {
	Directory string `json:"directory"`
	ProjectID string `json:"project_id"`
	Server    string `json:"server"`
	AccountID string `json:"account_id"`
	LinkedAt  string `json:"linked_at"`
}

type Status struct {
	Binding       Binding         `json:"binding"`
	HooksEnabled  bool            `json:"hooks_enabled"`
	HookClients   map[string]bool `json:"hook_clients"`
	OutboxPending int             `json:"outbox_pending"`
}

type OutboxItem struct {
	Binding   Binding       `json:"binding"`
	Event     v2.EventInput `json:"event"`
	QueuedAt  string        `json:"queued_at"`
	Attempts  int           `json:"attempts"`
	LastError string        `json:"last_error,omitempty"`
}

type HookResult struct {
	EventID       string `json:"event_id,omitempty"`
	Queued        bool   `json:"queued"`
	Delivered     bool   `json:"delivered"`
	Pending       int    `json:"pending"`
	ContextLoaded int    `json:"context_loaded,omitempty"`
}

type HookInput struct {
	SessionID            string          `json:"session_id"`
	TurnID               string          `json:"turn_id,omitempty"`
	PromptID             string          `json:"prompt_id,omitempty"`
	TranscriptPath       string          `json:"transcript_path,omitempty"`
	CWD                  string          `json:"cwd"`
	PermissionMode       string          `json:"permission_mode,omitempty"`
	HookEventName        string          `json:"hook_event_name"`
	Prompt               string          `json:"prompt,omitempty"`
	Source               string          `json:"source,omitempty"`
	Model                string          `json:"model,omitempty"`
	AgentType            string          `json:"agent_type,omitempty"`
	SessionTitle         string          `json:"session_title,omitempty"`
	StopHookActive       bool            `json:"stop_hook_active,omitempty"`
	LastAssistantMessage string          `json:"last_assistant_message,omitempty"`
	Trigger              string          `json:"trigger,omitempty"`
	CustomInstructions   json.RawMessage `json:"custom_instructions,omitempty"`
	Reason               string          `json:"reason,omitempty"`
}

type normalizedHook struct {
	Input       HookInput
	Event       v2.EventInput
	StableID    bool
	Reminder    bool
	LoadContext bool
}

type hookOutput struct {
	HookSpecificOutput *hookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

type hookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext,omitempty"`
}

type FlushResult struct {
	Delivered int `json:"delivered"`
	Pending   int `json:"pending"`
}

type SetupOptions struct {
	Directory    string
	Client       string
	EDCPath      string
	ConfigPath   string
	Server       string
	AccountID    string
	DisableHooks bool
}

type FileChange struct {
	Root       string      `json:"-"`
	Path       string      `json:"path"`
	Before     []byte      `json:"-"`
	After      []byte      `json:"-"`
	BeforeHash string      `json:"before_sha256,omitempty"`
	Diff       string      `json:"diff"`
	Mode       fs.FileMode `json:"mode"`
}

type SetupPreview struct {
	Directory string       `json:"directory"`
	Client    string       `json:"client"`
	Command   string       `json:"command"`
	Changes   []FileChange `json:"changes"`
	Warnings  []string     `json:"warnings,omitempty"`
}

type bindingsFile struct {
	Version  int       `json:"version"`
	Bindings []Binding `json:"bindings"`
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
