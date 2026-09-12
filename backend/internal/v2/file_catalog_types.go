package v2

// Catalog reads freeze the Event boundary, independently of note coverage.
type FileCatalogInput struct {
	Limit           int    `json:"limit,omitempty"`
	Cursor          string `json:"cursor,omitempty"`
	ThroughSequence *int64 `json:"through_sequence,omitempty"`
}
type FileReference struct {
	EventID  string `json:"event_id"`
	Sequence int64  `json:"sequence"`
	Type     string `json:"type"`
	Relation string `json:"relation"` // attachment or derived_from
}
type FileCatalogEntry struct {
	FileInfo
	ReferenceCount     int             `json:"reference_count"`
	References         []FileReference `json:"references"` // at most five previews
	ReferencesComplete bool            `json:"references_complete"`
}
type FileCatalogPage struct {
	ProjectID       string             `json:"project_id"`
	ThroughSequence int64              `json:"through_sequence"`
	Files           []FileCatalogEntry `json:"files"`
	NextCursor      string             `json:"next_cursor,omitempty"`
}
type FileReferencesPage struct {
	ProjectID       string          `json:"project_id"`
	FileID          string          `json:"file_id"`
	ThroughSequence int64           `json:"through_sequence"`
	References      []FileReference `json:"references"`
	NextCursor      string          `json:"next_cursor,omitempty"`
}
