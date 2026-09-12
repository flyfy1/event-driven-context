package v2client

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestNotesExportRejectsInvalidSnapshots(t *testing.T) {
	for _, test := range []struct {
		name    string
		project string
		paths   []string
		corrupt bool
	}{
		{"foreign project", "prj_other", []string{"index.md"}, false},
		{"bad digest", "prj_one", []string{"index.md"}, true},
		{"traversal", "prj_one", []string{"../escape.md"}, false},
		{"file then directory", "prj_one", []string{"topics/a.md", "topics/a.md/child.md"}, false},
		{"directory then file", "prj_one", []string{"topics/a.md/child.md", "topics/a.md"}, false},
		{"case alias", "prj_one", []string{"topics/Work/a.md", "topics/work/b.md"}, false},
		{"duplicate", "prj_one", []string{"index.md", "index.md"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				out := NotesExport{ProjectID: test.project, Revision: 1, Files: []NotesFile{}}
				for _, p := range test.paths {
					hash := NoteHash("content")
					if test.corrupt {
						hash = NoteHash("other")
					}
					out.Files = append(out.Files, NotesFile{Path: p, Content: "content", SHA256: hash})
				}
				json.NewEncoder(w).Encode(out)
			})
			if _, err := c.ExportNotes(context.Background(), "prj_one"); err == nil {
				t.Fatal("invalid snapshot accepted")
			}
		})
	}
}
func TestNotesExportBoundsResponse(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method %s", r.Method)
		}
		w.Write([]byte(strings.Repeat(" ", MaxNotesExportBytes+1)))
	})
	_, err := c.ExportNotes(context.Background(), "prj_one")
	if err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("%v", err)
	}
}
