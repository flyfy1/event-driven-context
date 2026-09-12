// Package notes stores versioned Markdown trees. Callers provide authorization
// and serialize operations; the V2 service owns the process-wide writer lock.
package notes

const (
	MaxFileBytes   = 1 << 20
	MaxExportBytes = 16 << 20
	MaxFiles       = 10000
)

type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}

type Export struct {
	ProjectID string `json:"project_id"`
	Revision  int64  `json:"revision"`
	Files     []File `json:"files"`
}

type WriteFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type SyncInput struct {
	ExpectedRevision *int64      `json:"expected_revision"`
	Files            []WriteFile `json:"files"`
	Replace          bool        `json:"-"`
	Checkpoint       *Checkpoint `json:"-"`
}

type Checkpoint struct {
	AfterSequence int64  `json:"after_sequence"`
	PromptVersion string `json:"prompt_version"`
	PolicyHash    string `json:"policy_hash"`
	RunID         string `json:"run_id"`
}

type Error struct{ Code, Message string }

func (e *Error) Error() string      { return e.Message }
func invalid(message string) error  { return &Error{"invalid_input", message} }
func tooLarge(message string) error { return &Error{"too_large", message} }
