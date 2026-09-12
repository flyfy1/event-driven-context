package v2

import (
	"context"
	"encoding/json"
	"io"
)

const (
	MaxBatchEvents = 100
	MaxTextBytes   = 1 << 20
	MaxMetadata    = 32 << 10
	MaxRefs        = 32
	MaxFileBytes   = 50 << 20
	MaxStateBytes  = 256 << 10
)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

type Actor struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	Username   string `json:"username,omitempty"`
	OnBehalfOf string `json:"on_behalf_of,omitempty"`
}

type Ref struct {
	Rel string `json:"rel"`
	ID  string `json:"id"`
}

type EventContent struct {
	Kind       string `json:"kind"`
	Text       string `json:"text,omitempty"`
	FileID     string `json:"file_id,omitempty"`
	MediaType  string `json:"media_type,omitempty"`
	Filename   string `json:"filename,omitempty"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

type EventInput struct {
	ID         string                     `json:"id"`
	Type       string                     `json:"type"`
	Content    EventContent               `json:"content"`
	Metadata   map[string]json.RawMessage `json:"metadata,omitempty"`
	Source     map[string]json.RawMessage `json:"source"`
	Refs       []Ref                      `json:"refs,omitempty"`
	OccurredAt string                     `json:"occurred_at,omitempty"`
}

type Event struct {
	ID         string                     `json:"id"`
	ProjectID  string                     `json:"project_id"`
	Type       string                     `json:"type"`
	Content    EventContent               `json:"content"`
	Metadata   map[string]json.RawMessage `json:"metadata"`
	Source     map[string]json.RawMessage `json:"source"`
	Refs       []Ref                      `json:"refs"`
	OccurredAt string                     `json:"occurred_at,omitempty"`
	Sequence   int64                      `json:"sequence"`
	RecordedAt string                     `json:"recorded_at"`
	Actor      Actor                      `json:"actor"`
}

type RecordEventsInput struct {
	Events []EventInput `json:"events"`
}
type EventWriteResult struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Sequence int64  `json:"sequence,omitempty"`
	Error    *Error `json:"error,omitempty"`
}
type RecordEventsResult struct {
	Results []EventWriteResult `json:"results"`
}

type QueryEventsInput struct {
	Types         []string                   `json:"types,omitempty"`
	Metadata      map[string]json.RawMessage `json:"metadata,omitempty"`
	Source        map[string]json.RawMessage `json:"source,omitempty"`
	RefsTo        string                     `json:"refs_to,omitempty"`
	AfterSequence int64                      `json:"after_sequence,omitempty"`
	Order         string                     `json:"order,omitempty"`
	From          string                     `json:"from,omitempty"`
	To            string                     `json:"to,omitempty"`
	TimeField     string                     `json:"time_field,omitempty"`
	Limit         int                        `json:"limit,omitempty"`
	Cursor        string                     `json:"cursor,omitempty"`
}
type EventsPage struct {
	Events         []Event `json:"events"`
	NextCursor     string  `json:"next_cursor,omitempty"`
	LatestSequence int64   `json:"latest_sequence"`
}
type MetadataInput struct {
	Key string `json:"key,omitempty"`
}
type MetadataValue struct {
	Value      json.RawMessage `json:"value"`
	EventCount int             `json:"event_count"`
}
type MetadataField struct {
	Key        string   `json:"key"`
	Types      []string `json:"types"`
	EventCount int      `json:"event_count"`
}
type MetadataResult struct {
	Fields []MetadataField `json:"fields,omitempty"`
	Values []MetadataValue `json:"values,omitempty"`
}

type FileUpload struct {
	Filename  string
	MediaType string
	SHA256    string
	SizeBytes int64
	Reader    io.Reader
}
type FileInfo struct {
	ID         string `json:"file_id"`
	ProjectID  string `json:"project_id"`
	Filename   string `json:"filename"`
	MediaType  string `json:"media_type"`
	SizeBytes  int64  `json:"size_bytes"`
	SHA256     string `json:"sha256"`
	UploadedAt string `json:"uploaded_at"`
	Referenced bool   `json:"referenced"`
}
type CleanupResult struct {
	Removed int `json:"removed"`
}

type StateContent struct {
	Format string `json:"format"`
	Text   string `json:"text"`
}
type Producer struct {
	PluginID      string `json:"plugin_id"`
	PluginVersion string `json:"plugin_version"`
}
type State struct {
	ProjectID       string          `json:"project_id"`
	Key             string          `json:"key"`
	Version         int64           `json:"version"`
	Content         StateContent    `json:"content"`
	Data            json.RawMessage `json:"data,omitempty"`
	BasedOnSequence int64           `json:"based_on_sequence"`
	Refs            []string        `json:"refs"`
	Producer        Producer        `json:"producer"`
	UpdatedAt       string          `json:"updated_at"`
	Lag             int64           `json:"lag"`
}
type PutStateInput struct {
	Key             string          `json:"key"`
	ExpectedVersion *int64          `json:"expected_version,omitempty"`
	Content         StateContent    `json:"content"`
	Data            json.RawMessage `json:"data,omitempty"`
	BasedOnSequence int64           `json:"based_on_sequence"`
	Refs            []string        `json:"refs,omitempty"`
	AsPluginID      string          `json:"as_plugin_id,omitempty"`
}
type ListStateInput struct {
	Prefix string `json:"prefix,omitempty"`
}
type GetStateInput struct {
	Keys    []string `json:"keys"`
	Version *int64   `json:"version,omitempty"`
}
type StatesResult struct {
	States         []State `json:"states"`
	LatestSequence int64   `json:"latest_sequence"`
}

type Permissions struct {
	OrganizeNotes bool     `json:"organize_notes,omitempty" yaml:"organize_notes"`
	ReadEvents    []string `json:"read_events" yaml:"read_events"`
	WriteEvents   []string `json:"write_events" yaml:"write_events"`
	WriteState    []string `json:"write_state" yaml:"write_state"`
}
type Manifest struct {
	ID             string             `json:"id" yaml:"id"`
	Version        string             `json:"version" yaml:"version"`
	Name           string             `json:"name" yaml:"name"`
	Description    string             `json:"description,omitempty" yaml:"description"`
	Skills         []string           `json:"skills,omitempty" yaml:"skills"`
	State          []StateDeclaration `json:"state,omitempty" yaml:"state"`
	SessionContext []string           `json:"session_context,omitempty" yaml:"session_context"`
	// Processor and Config are declarative manifest data. Core stores and
	// returns them but never executes commands, schedules, or prompts.
	Processor    json.RawMessage `json:"processor,omitempty" yaml:"processor"`
	Config       json.RawMessage `json:"config,omitempty" yaml:"config"`
	ConfigFields []ConfigField   `json:"config_fields,omitempty" yaml:"config_fields"`
	Permissions  Permissions     `json:"permissions" yaml:"permissions"`
}
type ConfigField struct {
	Key         string `json:"key" yaml:"key"`
	Type        string `json:"type" yaml:"type"`
	Description string `json:"description" yaml:"description"`
}
type StateDeclaration struct {
	Key string `json:"key" yaml:"key"`
}
type Installation struct {
	ID              string                 `json:"id"`
	ProjectID       string                 `json:"project_id"`
	ProjectTimezone string                 `json:"project_timezone,omitempty"`
	PluginID        string                 `json:"plugin_id"`
	PluginVersion   string                 `json:"plugin_version"`
	Manifest        Manifest               `json:"manifest"`
	ManagerUserID   string                 `json:"manager_user_id"`
	Status          string                 `json:"status"`
	ConfigRevision  int64                  `json:"config_revision"`
	Config          json.RawMessage        `json:"config"`
	ConfigRevisions []PluginConfigRevision `json:"config_revisions"`
	Permissions     Permissions            `json:"permissions"`
	CreatedAt       string                 `json:"created_at"`
	UpdatedAt       string                 `json:"updated_at"`
}
type PluginConfigRevision struct {
	Revision        int64           `json:"revision"`
	Config          json.RawMessage `json:"config"`
	CreatedAt       string          `json:"created_at"`
	CreatedByUserID string          `json:"created_by_user_id"`
}
type InstallPluginInput struct {
	Manifest Manifest        `json:"manifest"`
	Config   json.RawMessage `json:"config,omitempty"`
}
type InstallPluginResult struct {
	Installation Installation `json:"installation"`
	Token        string       `json:"token"`
}
type RevisePluginInput struct {
	ExpectedRevision int64           `json:"expected_revision"`
	Config           json.RawMessage `json:"config"`
}
type PluginPrincipal struct {
	InstallationID string
	ProjectID      string
	PluginID       string
	Revision       int64
}

type ManualRunInput struct {
	RequestID      string   `json:"request_id"`
	SourceEventIDs []string `json:"source_event_ids,omitempty"`
}
type ManualRunRequest struct {
	RequestID      string   `json:"request_id"`
	ProjectID      string   `json:"project_id"`
	PluginID       string   `json:"plugin_id"`
	SourceEventIDs []string `json:"source_event_ids"`
	Status         string   `json:"status"`
	CreatedAt      string   `json:"created_at"`
}

// ServiceAPI is the adapter-facing contract. Plugin calls deliberately accept
// an authenticated principal rather than a caller-selected plugin id.
type ServiceAPI interface {
	AdminProjectStats([]string) []AdminProjectStats
	RecordEvents(context.Context, string, RecordEventsInput) (RecordEventsResult, error)
	QueryEvents(context.Context, string, QueryEventsInput) (EventsPage, error)
	GetEvent(context.Context, string, string) (Event, error)
	ListMetadata(context.Context, string, MetadataInput) (MetadataResult, error)
	PutFile(context.Context, string, FileUpload) (FileInfo, error)
	OpenFile(context.Context, string, string) (FileInfo, io.ReadCloser, error)
	ListState(context.Context, string, ListStateInput) (StatesResult, error)
	GetState(context.Context, string, GetStateInput) (StatesResult, error)
	PutState(context.Context, string, PutStateInput) (State, error)
	InstallPlugin(context.Context, string, InstallPluginInput) (InstallPluginResult, error)
	EnsureBuiltinPlugin(context.Context, string, Manifest, json.RawMessage) (PluginPrincipal, Installation, error)
	ListPlugins(context.Context, string) ([]Installation, error)
	RevisePlugin(context.Context, string, string, RevisePluginInput) (Installation, error)
	SetPluginStatus(context.Context, string, string, string) (Installation, error)
	RemovePlugin(context.Context, string, string) (Installation, error)
	RequestManualRun(context.Context, string, string, ManualRunInput) (ManualRunRequest, error)
	AuthenticatePlugin(string) (PluginPrincipal, error)
	GetPluginAsPlugin(context.Context, PluginPrincipal) (Installation, error)
	RecordEventsAsPlugin(context.Context, PluginPrincipal, RecordEventsInput) (RecordEventsResult, error)
	QueryEventsAsPlugin(context.Context, PluginPrincipal, QueryEventsInput) (EventsPage, error)
	GetEventAsPlugin(context.Context, PluginPrincipal, string) (Event, error)
	ListMetadataAsPlugin(context.Context, PluginPrincipal, MetadataInput) (MetadataResult, error)
	OpenFileAsPlugin(context.Context, PluginPrincipal, string) (FileInfo, io.ReadCloser, error)
	ListStateAsPlugin(context.Context, PluginPrincipal, ListStateInput) (StatesResult, error)
	GetStateAsPlugin(context.Context, PluginPrincipal, GetStateInput) (StatesResult, error)
	PutStateAsPlugin(context.Context, PluginPrincipal, PutStateInput) (State, error)
}
