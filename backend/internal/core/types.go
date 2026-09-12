package core

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const MaxContentBytes = 1 << 20
const MaxMediaBytes = 20 << 20
const MaxMetadataBytes = 32 << 10
const MaxRequestBytes = 2 << 20
const MaxMediaRequestBytes = 21 << 20
const MaxQueryPageBytes = 4 << 20

const (
	ProvenanceOriginal     = "original"
	ProvenanceTranscript   = "transcript"
	ProvenanceSummary      = "summary"
	ProvenanceSuggestion   = "suggestion"
	ProvenanceConfirmation = "confirmation"
)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }
func Invalid(format string, args ...any) error {
	return &Error{"invalid_input", fmt.Sprintf(format, args...)}
}
func TooLarge(format string, args ...any) error {
	return &Error{"too_large", fmt.Sprintf(format, args...)}
}

var ErrNotFound = &Error{"not_found", "resource not found or access denied"}
var ErrUnauthenticated = &Error{"unauthenticated", "valid login token required"}
var ErrForbidden = &Error{"forbidden", "project owner permission required"}
var ErrLastOwner = &Error{"conflict", "project must retain at least one owner"}
var ErrActionForbidden = &Error{"forbidden", "event action is not allowed"}
var ErrConflict = &Error{"conflict", "resource already exists or idempotency key reused with different input"}

type userKey struct{}

func WithUser(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, userKey{}, id)
}
func UserID(ctx context.Context) string { id, _ := ctx.Value(userKey{}).(string); return id }

type User struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	CreatedAt string `json:"created_at"`
}
type Credentials struct {
	Username string `json:"username"`
	Email    string `json:"email,omitempty"`
	Password string `json:"password"`
}
type LoginResult struct {
	User      User      `json:"user"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}
type Empty struct{}
type ProjectInput struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Timezone    string `json:"timezone,omitempty"`
}
type ProjectUpdateInput struct {
	Name     *string `json:"name,omitempty"`
	Timezone *string `json:"timezone,omitempty"`
}
type Project struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Timezone     string   `json:"timezone"`
	OwnerUserID  string   `json:"owner_user_id"`
	OwnerUserIDs []string `json:"owner_user_ids"`
	CreatedAt    string   `json:"created_at"`
}
type Projects struct {
	Projects []Project `json:"projects"`
}
type ProjectRef struct {
	ProjectID string `json:"project_id"`
}
type MemberInput struct {
	ProjectID string `json:"project_id"`
	Username  string `json:"username,omitempty"`
	Email     string `json:"email,omitempty"`
}
type MemberRoleInput struct {
	ProjectID string `json:"project_id"`
	UserID    string `json:"user_id"`
	Role      string `json:"role"`
}
type ProjectMember struct {
	User
	Role string `json:"role"`
}
type Members struct {
	Members []ProjectMember `json:"members"`
}
type FileInput struct {
	Filename   string `json:"filename"`
	MediaType  string `json:"media_type"`
	DataBase64 string `json:"data_base64"`
}
type ContentInput struct {
	Kind string     `json:"kind"`
	Text *string    `json:"text,omitempty"`
	File *FileInput `json:"file,omitempty"`
}
type RecordInput struct {
	ProjectID      string                     `json:"project_id"`
	Content        ContentInput               `json:"content"`
	Metadata       map[string]json.RawMessage `json:"metadata,omitempty"`
	OccurredAt     string                     `json:"occurred_at,omitempty"`
	IdempotencyKey string                     `json:"idempotency_key,omitempty"`
	Action         *ActionInput               `json:"action,omitempty"`
}
type ActionInput struct {
	Kind               string   `json:"kind"`
	SourceEventIDs     []string `json:"source_event_ids,omitempty"`
	SupersedesEventIDs []string `json:"supersedes_event_ids,omitempty"`
}

// MediaRecordInput is used by the multipart HTTP endpoint. Keeping raw bytes
// outside RecordInput preserves the existing 1 MiB base64 event contract.
type MediaRecordInput struct {
	ProjectID      string
	Filename       string
	MediaType      string
	Data           []byte
	Metadata       map[string]json.RawMessage
	OccurredAt     string
	IdempotencyKey string
}
type FileInfo struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	MediaType string `json:"media_type"`
	SizeBytes int    `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}
type Content struct {
	Kind string    `json:"kind"`
	Text *string   `json:"text,omitempty"`
	File *FileInfo `json:"file,omitempty"`
}
type Provenance struct {
	Kind           string   `json:"kind"`
	SourceEventIDs []string `json:"source_event_ids,omitempty"`
	RunID          string   `json:"run_id,omitempty"`
	SkillID        string   `json:"skill_id,omitempty"`
	SkillVersion   string   `json:"skill_version,omitempty"`
}
type Relations struct {
	SupersedesEventIDs []string `json:"supersedes_event_ids,omitempty"`
}
type Event struct {
	ID            string                     `json:"id"`
	ProjectID     string                     `json:"project_id"`
	ActorUserID   string                     `json:"actor_user_id"`
	ActorUsername string                     `json:"actor_username"`
	RecordedAt    string                     `json:"recorded_at"`
	OccurredAt    string                     `json:"occurred_at,omitempty"`
	Content       Content                    `json:"content"`
	Metadata      map[string]json.RawMessage `json:"metadata"`
	Provenance    Provenance                 `json:"provenance"`
	Relations     Relations                  `json:"relations"`
}
type EventRef struct {
	EventID string `json:"event_id"`
}
type FileRef struct {
	FileID string `json:"file_id"`
}
type FileResult struct {
	File       FileInfo `json:"file"`
	DataBase64 string   `json:"data_base64"`
}
type QueryInput struct {
	ProjectID      string                     `json:"project_id"`
	From           string                     `json:"from,omitempty"`
	To             string                     `json:"to,omitempty"`
	TimeField      string                     `json:"time_field,omitempty"`
	Metadata       map[string]json.RawMessage `json:"metadata,omitempty"`
	MetadataExists []string                   `json:"metadata_exists,omitempty"`
	Limit          int                        `json:"limit,omitempty"`
	Cursor         string                     `json:"cursor,omitempty"`
}
type Events struct {
	Events     []Event `json:"events"`
	NextCursor string  `json:"next_cursor,omitempty"`
}
type MetadataInput struct {
	ProjectID string  `json:"project_id"`
	Key       *string `json:"key,omitempty"`
	Limit     int     `json:"limit,omitempty"`
	Offset    int     `json:"offset,omitempty"`
}
type MetadataField struct {
	Key        string   `json:"key"`
	Types      []string `json:"types"`
	EventCount int      `json:"event_count"`
}
type MetadataValue struct {
	Value      json.RawMessage `json:"value"`
	EventCount int             `json:"event_count"`
}
type MetadataResult struct {
	Fields  []MetadataField `json:"fields,omitempty"`
	Values  []MetadataValue `json:"values,omitempty"`
	HasMore bool            `json:"has_more"`
}
type DerivedRecordInput struct {
	ProjectID      string
	Content        ContentInput
	Metadata       map[string]json.RawMessage
	OccurredAt     string
	IdempotencyKey string
	Provenance     Provenance
	Relations      Relations
}
type ContextQueryInput struct {
	ProjectID          string `json:"project_id"`
	Query              string `json:"query"`
	MaxOutputBytes     int    `json:"max_output_bytes,omitempty"`
	IncludeSuggestions bool   `json:"include_suggestions,omitempty"`
}
type ContextSnapshot struct {
	Sequence int64 `json:"sequence"`
}
type ContextEvidence struct {
	EventID          string   `json:"event_id"`
	Excerpt          string   `json:"excerpt"`
	ExcerptTruncated bool     `json:"excerpt_truncated"`
	SourceEventIDs   []string `json:"source_event_ids"`
	Interpretation   string   `json:"interpretation"`
	State            string   `json:"state"`
	RetrievalReason  string   `json:"retrieval_reason"`
	RecordedAt       string   `json:"recorded_at"`
	OccurredAt       string   `json:"occurred_at,omitempty"`
}
type ContextConflict struct {
	Kind           string   `json:"kind"`
	TargetEventID  string   `json:"target_event_id"`
	UpdateEventIDs []string `json:"update_event_ids"`
}
type ContextCoverage struct {
	SnapshotSequence       int64  `json:"snapshot_sequence"`
	IndexedThroughSequence int64  `json:"indexed_through_sequence"`
	PendingProcessingCount int    `json:"pending_processing_count"`
	Truncated              bool   `json:"truncated"`
	ConflictDetection      string `json:"conflict_detection"`
}
type ContextQueryResult struct {
	ProjectID   string            `json:"project_id"`
	Snapshot    ContextSnapshot   `json:"snapshot"`
	Evidence    []ContextEvidence `json:"evidence"`
	Suggestions []ContextEvidence `json:"suggestions"`
	Conflicts   []ContextConflict `json:"conflicts"`
	Coverage    ContextCoverage   `json:"coverage"`
	Warnings    []string          `json:"warnings"`
}

// Backend is the shared application boundary for HTTP, MCP and its stdio proxy.
type Backend interface {
	CreateProject(context.Context, ProjectInput) (Project, error)
	ListProjects(context.Context, Empty) (Projects, error)
	AddMember(context.Context, MemberInput) (User, error)
	ListMembers(context.Context, ProjectRef) (Members, error)
	RecordEvent(context.Context, RecordInput) (Event, error)
	GetEvent(context.Context, EventRef) (Event, error)
	QueryEvents(context.Context, QueryInput) (Events, error)
	ListMetadata(context.Context, MetadataInput) (MetadataResult, error)
	GetFile(context.Context, FileRef) (FileResult, error)
	QueryContext(context.Context, ContextQueryInput) (ContextQueryResult, error)
}
