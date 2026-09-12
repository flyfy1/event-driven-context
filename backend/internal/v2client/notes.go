package v2client

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"event-driven-context/internal/notes"
)

const MaxNotesExportBytes = 16 << 20
const MaxNoteBytes = 1 << 20
const MaxNotesFiles = 10000

type NotesFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}
type NotesExport struct {
	ProjectID       string      `json:"project_id"`
	Revision        int64       `json:"revision"`
	ThroughSequence int64       `json:"through_sequence"`
	Files           []NotesFile `json:"files"`
}

// ValidateNotePath is intentionally portable: note paths cannot alias local
// metadata, contain hidden segments, or use Windows separators.
func ValidateNotePath(name string) error {
	if err := notes.ValidatePath(name); err != nil {
		return fmt.Errorf("invalid note path %q: %w", name, err)
	}
	return nil
}

func NoteHash(content string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(content))) }

func validateNotesExport(projectID string, out NotesExport) error {
	if out.ProjectID != projectID || out.Revision < 0 || out.ThroughSequence < 0 {
		return fmt.Errorf("server returned unexpected notes project or revision")
	}
	if len(out.Files) > MaxNotesFiles {
		return fmt.Errorf("server returned too many notes")
	}
	seen := make(map[string]string)
	files := make(map[string]bool)
	for _, file := range out.Files {
		files[strings.ToLower(file.Path)] = true
	}
	for _, file := range out.Files {
		if err := ValidateNotePath(file.Path); err != nil {
			return err
		}
		if len(file.Content) > MaxNoteBytes || !utf8.ValidString(file.Content) || strings.ContainsRune(file.Content, 0) || file.SHA256 != NoteHash(file.Content) {
			return fmt.Errorf("invalid note content or sha256 for %q", file.Path)
		}
		parts := strings.Split(file.Path, "/")
		for i := range parts {
			p := strings.Join(parts[:i+1], "/")
			k := strings.ToLower(p)
			if i < len(parts)-1 && files[k] {
				return fmt.Errorf("note file also used as directory: %q", p)
			}
			if prev, ok := seen[k]; ok && (prev != p || i == len(parts)-1) {
				return fmt.Errorf("duplicate or case-aliased note path %q", file.Path)
			}
			seen[k] = p
		}
	}
	return nil
}

func (c *Client) ExportNotes(ctx context.Context, projectID string) (out NotesExport, err error) {
	req, err := c.newRequest(ctx, http.MethodGet, projectPath(projectID, "/notes/export"), nil)
	if err != nil {
		return out, err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return out, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return out, c.responseError(res)
	}
	if err = decodeJSON(res.Body, MaxNotesExportBytes, &out); err == nil {
		err = validateNotesExport(projectID, out)
	}
	return
}
