package api

import (
	"net/http"
	"strings"
	"testing"

	"event-driven-context/internal/core"
	"event-driven-context/internal/notes"
)

func TestNotesExportHTTPIsReadOnlyAndProjectScoped(t *testing.T) {
	f := newV2APIFixture(t)
	base := "/v1/projects/" + f.project.ID + "/notes"
	for _, token := range []string{"", f.bobToken} {
		w := f.request(t, http.MethodGet, base+"/export", token, "", nil)
		if w.Code != http.StatusUnauthorized && w.Code != http.StatusNotFound && w.Code != http.StatusForbidden {
			t.Fatalf("unauthorized read %d %s", w.Code, w.Body.String())
		}
	}
	w := f.request(t, http.MethodGet, base+"/export", f.token, "", nil)
	if out := decodeV2Response[notes.Export](t, w); w.Code != 200 || out.Revision != 0 || len(out.Files) != 0 {
		t.Fatalf("empty %d %#v", w.Code, out)
	}
	// Seed through internal publication; no public write route is registered.
	revision := int64(0)
	_, err := f.service.SyncNotes(core.WithUser(t.Context(), f.alice.ID), f.project.ID, notes.SyncInput{ExpectedRevision: &revision, Files: []notes.WriteFile{{Path: "index.md", Content: "# Notes\n"}}})
	if err != nil {
		t.Fatal(err)
	}
	w = f.request(t, http.MethodGet, base+"/export", f.token, "", nil)
	if out := decodeV2Response[notes.Export](t, w); w.Code != 200 || out.Revision != 1 || len(out.Files) != 1 || out.Files[0].Content != "# Notes\n" {
		t.Fatalf("export %d %#v", w.Code, out)
	}
	w = f.request(t, http.MethodPost, base+"/sync", f.token, "application/json", strings.NewReader(`{"expected_revision":1,"files":[{"path":"index.md","content":"changed"}]}`))
	if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unexpected write route %d %s", w.Code, w.Body.String())
	}
	w = f.request(t, http.MethodGet, base+"/export", f.token, "", nil)
	if out := decodeV2Response[notes.Export](t, w); w.Code != 200 || out.Revision != 1 || out.Files[0].Content != "# Notes\n" {
		t.Fatalf("remote modified %d %#v", w.Code, out)
	}
}
