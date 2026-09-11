package automation

import (
	"encoding/json"
	"time"

	"event-driven-context/internal/runner"
)

const (
	AudioTranscribeSkill = runner.AudioTranscribeSkill
	DailyReviewSkill     = runner.DailyReviewSkill
	FixedSkillVersion    = runner.FixedSkillVersion

	RunQueued            = "queued"
	RunLeased            = "leased"
	RunRunning           = "running"
	RunCandidateSaved    = "candidate_saved"
	RunRetryWait         = "retry_wait"
	RunBlockedAuth       = "blocked_auth"
	RunBlockedCapability = "blocked_capability"
	RunFailed            = "failed"
	RunCancelled         = "cancelled"
	RunSkipped           = "skipped"
	RunSucceeded         = "succeeded"
)

type RegisterRunnerInput struct {
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
}

type RegisteredRunner struct {
	ID           string   `json:"id"`
	OwnerUserID  string   `json:"owner_user_id"`
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
	TokenHash    string   `json:"-"`
	CreatedAt    string   `json:"created_at"`
	RevokedAt    string   `json:"revoked_at,omitempty"`
}

type RunnerRegistration struct {
	Runner RegisteredRunner `json:"runner"`
	Token  string           `json:"token"`
}

type RunnerActionInput struct {
	Action string `json:"action"`
}

type Trigger struct {
	Type           string                     `json:"type"`
	MetadataEquals map[string]json.RawMessage `json:"metadata_equals,omitempty"`
	LocalTime      string                     `json:"local_time,omitempty"`
	Timezone       string                     `json:"timezone,omitempty"`
}

type CreateInstallationInput struct {
	ProjectID  string  `json:"project_id"`
	RunnerID   string  `json:"runner_id"`
	SkillID    string  `json:"skill_id"`
	Language   string  `json:"language,omitempty"`
	UserPrompt string  `json:"user_prompt,omitempty"`
	Trigger    Trigger `json:"trigger"`
}

type ReviseInstallationInput struct {
	Language   string  `json:"language,omitempty"`
	UserPrompt string  `json:"user_prompt,omitempty"`
	Trigger    Trigger `json:"trigger"`
}

type InstallationActionInput struct {
	Action string `json:"action"`
}

type InstallationRevision struct {
	ID                    string  `json:"id"`
	Number                int     `json:"number"`
	Language              string  `json:"language,omitempty"`
	UserPrompt            string  `json:"user_prompt,omitempty"`
	Trigger               Trigger `json:"trigger"`
	ActivationSequence    int64   `json:"activation_sequence"`
	ActiveThroughSequence int64   `json:"active_through_sequence,omitempty"`
	Closed                bool    `json:"closed,omitempty"`
	ActivatedAt           string  `json:"activated_at"`
}

type Installation struct {
	ID                string                 `json:"id"`
	OwnerUserID       string                 `json:"owner_user_id"`
	ProjectID         string                 `json:"project_id"`
	RunnerID          string                 `json:"runner_id"`
	SkillID           string                 `json:"skill_id"`
	SkillVersion      string                 `json:"skill_version"`
	SkillDigest       string                 `json:"skill_digest"`
	Enabled           bool                   `json:"enabled"`
	CurrentRevisionID string                 `json:"current_revision_id"`
	Revisions         []InstallationRevision `json:"revisions"`
	LastDailySlot     string                 `json:"last_daily_slot,omitempty"`
	CreatedAt         string                 `json:"created_at"`
	UpdatedAt         string                 `json:"updated_at"`
}

type RunInput struct {
	EventID string `json:"event_id"`
	FileID  string `json:"file_id,omitempty"`
}

type Attempt struct {
	ID             string `json:"id"`
	FencingToken   uint64 `json:"fencing_token"`
	StartedAt      string `json:"started_at"`
	LeaseExpiresAt string `json:"lease_expires_at"`
}

type Run struct {
	ID                 string     `json:"id"`
	InstallationID     string     `json:"installation_id"`
	RevisionID         string     `json:"revision_id"`
	OwnerUserID        string     `json:"owner_user_id"`
	ProjectID          string     `json:"project_id"`
	RunnerID           string     `json:"runner_id"`
	SkillID            string     `json:"skill_id"`
	SkillVersion       string     `json:"skill_version"`
	SkillDigest        string     `json:"skill_digest"`
	TriggerType        string     `json:"trigger_type"`
	Generation         int        `json:"generation"`
	SnapshotSequence   int64      `json:"snapshot_sequence"`
	Inputs             []RunInput `json:"inputs"`
	SupersedesEventIDs []string   `json:"supersedes_event_ids,omitempty"`
	EligibleInputCount int        `json:"eligible_input_count,omitempty"`
	OmittedInputCount  int        `json:"omitted_input_count,omitempty"`
	InputsTruncated    bool       `json:"inputs_truncated,omitempty"`
	WindowStart        string     `json:"window_start,omitempty"`
	WindowEnd          string     `json:"window_end,omitempty"`
	Slot               string     `json:"slot,omitempty"`
	Status             string     `json:"status"`
	AttemptCount       int        `json:"attempt_count"`
	FencingCounter     uint64     `json:"fencing_counter"`
	CurrentAttempt     *Attempt   `json:"current_attempt,omitempty"`
	NextAttemptAt      string     `json:"next_attempt_at,omitempty"`
	CandidateDigest    string     `json:"candidate_digest,omitempty"`
	OutputSlot         string     `json:"output_slot,omitempty"`
	OutputEventID      string     `json:"output_event_id,omitempty"`
	OutputEventIDs     []string   `json:"output_event_ids,omitempty"`
	InboxEntryID       string     `json:"inbox_entry_id,omitempty"`
	FailureKind        string     `json:"failure_kind,omitempty"`
	FailureCode        string     `json:"failure_code,omitempty"`
	NoOutputReason     string     `json:"no_output_reason,omitempty"`
	CreatedAt          string     `json:"created_at"`
	UpdatedAt          string     `json:"updated_at"`
}

type RequestRunInput struct {
	InstallationID string   `json:"installation_id"`
	RequestID      string   `json:"request_id"`
	SourceEventIDs []string `json:"source_event_ids"`
}

type ClaimInputFile struct {
	EventID   string `json:"event_id"`
	FileID    string `json:"file_id"`
	Filename  string `json:"filename"`
	MediaType string `json:"media_type"`
	SizeBytes int    `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

type Claim struct {
	Run            Run              `json:"run"`
	AttemptID      string           `json:"attempt_id"`
	FencingToken   uint64           `json:"fencing_token"`
	LeaseExpiresAt string           `json:"lease_expires_at"`
	Task           runner.Task      `json:"task"`
	InputFiles     []ClaimInputFile `json:"input_files"`
}

type AttemptCredential struct {
	AttemptID    string `json:"attempt_id"`
	FencingToken uint64 `json:"fencing_token"`
}

type SubmitInput struct {
	Candidate      *runner.Candidate `json:"candidate,omitempty"`
	NoOutputReason string            `json:"no_output_reason,omitempty"`
}

type FailureInput struct {
	Kind string `json:"kind"`
	Code string `json:"code"`
}

type InboxEntry struct {
	ID              string           `json:"id"`
	RecipientUserID string           `json:"recipient_user_id"`
	ProjectID       string           `json:"project_id"`
	InstallationID  string           `json:"installation_id"`
	RunID           string           `json:"run_id"`
	OutputEventID   string           `json:"output_event_id,omitempty"`
	OutputEventIDs  []string         `json:"output_event_ids,omitempty"`
	CandidateDigest string           `json:"candidate_digest"`
	Candidate       runner.Candidate `json:"candidate"`
	CreatedAt       string           `json:"created_at"`
	ReadAt          string           `json:"read_at,omitempty"`
}

type CommittedOutput struct {
	OutputSlot string `json:"output_slot"`
	Kind       string `json:"kind"`
	EventID    string `json:"event_id"`
}

type CommitRecord struct {
	RunID           string            `json:"run_id"`
	CandidateDigest string            `json:"candidate_digest"`
	Outputs         []CommittedOutput `json:"outputs"`
	InboxEntryID    string            `json:"inbox_entry_id"`
	CommittedAt     string            `json:"committed_at"`
}

type DispatchResult struct {
	Claim *Claim `json:"claim,omitempty"`
}

type Clock func() time.Time

type SkillDescriptor struct {
	ID      string
	Version string
	Digest  string
}
