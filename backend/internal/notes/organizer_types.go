package notes

type EventPreview struct {
	ID         string `json:"id"`
	Sequence   int64  `json:"sequence"`
	Type       string `json:"type"`
	Kind       string `json:"kind"`
	RecordedAt string `json:"recorded_at"`
	Preview    string `json:"preview"`
	Backfill   bool   `json:"backfill,omitempty"`
}

type BeginInput struct {
	PromptVersion string `json:"prompt_version"`
}
type OrganizationRun struct {
	Timezone        string         `json:"timezone"`
	Noop            bool           `json:"noop"`
	Reason          string         `json:"reason,omitempty"`
	RunID           string         `json:"run_id,omitempty"`
	AfterSequence   int64          `json:"after_sequence"`
	ThroughSequence int64          `json:"through_sequence"`
	Snapshot        Export         `json:"snapshot"`
	Events          []EventPreview `json:"events"`
}
type AccountedEvent struct {
	ID          string `json:"id"`
	Disposition string `json:"disposition"`
}
type PublishInput struct {
	RunID     string           `json:"run_id"`
	Files     []WriteFile      `json:"files"`
	Removed   []string         `json:"removed"`
	Accounted []AccountedEvent `json:"accounted"`
}
type PublishResult struct {
	Revision        int64 `json:"revision"`
	ThroughSequence int64 `json:"through_sequence"`
}
