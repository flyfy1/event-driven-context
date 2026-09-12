package api

import (
	"net/http"
	"strings"
	"testing"

	"event-driven-context/internal/core"
	"event-driven-context/internal/notes"
)

func TestNotesExportHTTPIsProjectScoped(t *testing.T) {
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
	// Seed the notes tree through the same service used by publication.
	revision := int64(0)
	_, err := f.service.SyncNotes(core.WithUser(t.Context(), f.alice.ID), f.project.ID, notes.SyncInput{ExpectedRevision: &revision, Files: []notes.WriteFile{{Path: "index.md", Content: "# Notes\n"}}})
	if err != nil {
		t.Fatal(err)
	}
	w = f.request(t, http.MethodGet, base+"/export", f.token, "", nil)
	if out := decodeV2Response[notes.Export](t, w); w.Code != 200 || out.Revision != 1 || len(out.Files) != 1 || out.Files[0].Content != "# Notes\n" {
		t.Fatalf("export %d %#v", w.Code, out)
	}
}

func TestNotesEditorSaveHTTP(t *testing.T) {
	f := newV2APIFixture(t)
	base := "/v1/projects/" + f.project.ID + "/notes"
	body := `{"expected_revision":0,"files":[{"path":"index.md","content":"# Notes\n"},{"path":"topics/a.md","content":"A"}]}`
	for _, token := range []string{"", f.bobToken} {
		w := f.request(t, http.MethodPost, base+"/sync", token, "application/json", strings.NewReader(body))
		if w.Code != http.StatusUnauthorized && w.Code != http.StatusNotFound && w.Code != http.StatusForbidden {
			t.Fatalf("unauthorized write %d %s", w.Code, w.Body.String())
		}
	}
	w := f.request(t, http.MethodPost, base+"/sync", f.token, "application/json", strings.NewReader(body))
	if out := decodeV2Response[notes.Export](t, w); w.Code != 200 || out.Revision != 1 || len(out.Files) != 2 {
		t.Fatalf("save %d %#v", w.Code, out)
	}
	w = f.request(t, http.MethodPost, base+"/sync", f.token, "application/json", strings.NewReader(body))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "notes_revision_mismatch") {
		t.Fatalf("stale write %d %s", w.Code, w.Body.String())
	}
	for _, invalid := range []string{
		`{"files":[{"path":"index.md","content":"missing revision"}]}`,
		`{"expected_revision":1,"files":[{"path":"../escape.md","content":"bad"}]}`,
	} {
		w = f.request(t, http.MethodPost, base+"/sync", f.token, "application/json", strings.NewReader(invalid))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid write %d %s", w.Code, w.Body.String())
		}
	}
	w = f.request(t, http.MethodPost, base+"/sync", f.token, "application/json", strings.NewReader(`{"expected_revision":1,"files":[{"path":"index.md","content":"edited"}]}`))
	if out := decodeV2Response[notes.Export](t, w); w.Code != 200 || out.Revision != 2 || len(out.Files) != 2 {
		t.Fatalf("patch %d %#v", w.Code, out)
	}
	w = f.request(t, http.MethodGet, base+"/export", f.token, "", nil)
	out := decodeV2Response[notes.Export](t, w)
	if w.Code != 200 || out.Revision != 2 || len(out.Files) != 2 || out.Files[0].Content != "edited" || out.Files[1].Content != "A" {
		t.Fatalf("read saved tree %d %#v", w.Code, out)
	}
}
