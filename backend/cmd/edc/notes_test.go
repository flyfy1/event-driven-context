package main

import (
	"context"
	"encoding/json"
	"event-driven-context/internal/v2client"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type notesFixture struct {
	app       *app
	files     map[string]string
	revision  int64
	posts     int
	server    *httptest.Server
	beforeGet func()
}

func newNotesFixture(t *testing.T, files map[string]string) *notesFixture {
	t.Helper()
	n := &notesFixture{files: files, revision: 1}
	n.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing token")
		}
		if r.URL.Path != "/v1/projects/prj_one/notes/export" || r.Method != http.MethodGet {
			n.posts++
			t.Errorf("download sync attempted %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if n.beforeGet != nil {
			n.beforeGet()
		}
		out := v2client.NotesExport{ProjectID: "prj_one", Revision: n.revision, Files: []v2client.NotesFile{}}
		for name, content := range n.files {
			out.Files = append(out.Files, v2client.NotesFile{Path: name, Content: content, SHA256: v2client.NoteHash(content)})
		}
		json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(n.server.Close)
	client, err := v2client.New(n.server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	n.app = &app{ctx: context.Background(), client: client, token: "test-token", io: streams{in: strings.NewReader(""), out: io.Discard, err: io.Discard}}
	return n
}
func writeNotesTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	file := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func readNotesTestFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNotesSyncDownloadsAndIsIdempotent(t *testing.T) {
	n := newNotesFixture(t, map[string]string{"index.md": "remote", "goals/index.md": "goals"})
	dir := t.TempDir()
	writeNotesTestFile(t, dir, "topics/work/local.md", "preserve local only")
	if err := n.app.dispatch("notes", []string{"sync", "--project", "prj_one", "--output", dir}); err != nil {
		t.Fatal(err)
	}
	baseline := readNotesTestFile(t, dir, notesBaselineName)
	result, err := n.app.syncNotes("prj_one", dir)
	if err != nil || result.Downloaded != 0 || len(result.PreservedLocal) != 1 || n.posts != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	if readNotesTestFile(t, dir, notesBaselineName) != baseline {
		t.Fatal("repeat changed baseline")
	}
	n.files["goals/index.md"] = "remote edit"
	n.revision++
	result, err = n.app.syncNotes("prj_one", dir)
	if err != nil || result.Downloaded != 1 || n.posts != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	if readNotesTestFile(t, dir, "goals/index.md") != "remote edit" || readNotesTestFile(t, dir, "topics/work/local.md") != "preserve local only" {
		t.Fatal("incorrect files")
	}
	if _, ok := n.files["topics/work/local.md"]; ok {
		t.Fatal("local content uploaded")
	}
}
func TestNotesSyncConflictPreflightsWholeRun(t *testing.T) {
	n := newNotesFixture(t, map[string]string{"index.md": "base"})
	dir := t.TempDir()
	if _, err := n.app.syncNotes("prj_one", dir); err != nil {
		t.Fatal(err)
	}
	baseline := readNotesTestFile(t, dir, notesBaselineName)
	writeNotesTestFile(t, dir, "index.md", "local")
	writeNotesTestFile(t, dir, "goals/new.md", "upload")
	n.files["index.md"] = "remote"
	n.files["daily/new.md"] = "download"
	n.revision++
	if _, err := n.app.syncNotes("prj_one", dir); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("%v", err)
	}
	if n.posts != 0 || readNotesTestFile(t, dir, "index.md") != "local" || readNotesTestFile(t, dir, notesBaselineName) != baseline {
		t.Fatal("conflict mutated state")
	}
	if _, err := os.Stat(filepath.Join(dir, "daily/new.md")); !os.IsNotExist(err) {
		t.Fatal("download before preflight")
	}
}
func TestNotesSyncFirstRunAdoption(t *testing.T) {
	n := newNotesFixture(t, map[string]string{"index.md": "remote"})
	dir := t.TempDir()
	writeNotesTestFile(t, dir, "index.md", "untracked")
	if _, err := n.app.syncNotes("prj_one", dir); err == nil {
		t.Fatal("untracked overwritten")
	}
	if n.posts != 0 || readNotesTestFile(t, dir, "index.md") != "untracked" {
		t.Fatal("untracked mutated")
	}
	writeNotesTestFile(t, dir, "index.md", "remote")
	if _, err := n.app.syncNotes("prj_one", dir); err != nil {
		t.Fatal(err)
	}
}
func TestNotesSyncRestoresMissingFiles(t *testing.T) {
	n := newNotesFixture(t, map[string]string{"index.md": "root", "goals/index.md": "goal"})
	dir := t.TempDir()
	if _, err := n.app.syncNotes("prj_one", dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "index.md")); err != nil {
		t.Fatal(err)
	}
	delete(n.files, "goals/index.md")
	n.revision++
	result, err := n.app.syncNotes("prj_one", dir)
	if err != nil || len(result.Restored) != 1 || len(result.PreservedLocal) != 1 || result.Downloaded != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	if n.files["goals/index.md"] != "" || readNotesTestFile(t, dir, "goals/index.md") != "goal" || readNotesTestFile(t, dir, "index.md") != "root" {
		t.Fatal("not restored")
	}
}
func TestNotesSyncRejectsForeignBinding(t *testing.T) {
	n := newNotesFixture(t, map[string]string{})
	dir := t.TempDir()
	if _, err := n.app.syncNotes("prj_one", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := n.app.syncNotes("prj_other", dir); err == nil || !strings.Contains(err.Error(), "bound") {
		t.Fatalf("%v", err)
	}
	other := newNotesFixture(t, map[string]string{})
	if _, err := other.app.syncNotes("prj_one", dir); err == nil || !strings.Contains(err.Error(), "bound") {
		t.Fatalf("%v", err)
	}
}
func TestNotesSyncRejectsUnsafePaths(t *testing.T) {
	for _, name := range []string{"../escape.md", "topics/../escape.md", ".edc-notes-sync.json", "topics/a.md/child.md"} {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{name: "bad"}
			if name == "topics/a.md/child.md" {
				files["topics/a.md"] = "file parent"
			}
			n := newNotesFixture(t, files)
			if _, err := n.app.syncNotes("prj_one", t.TempDir()); err == nil {
				t.Fatal("unsafe path accepted")
			}
			if n.posts != 0 {
				t.Fatal("unsafe post")
			}
		})
	}
	t.Run("symlink", func(t *testing.T) {
		n := newNotesFixture(t, map[string]string{"topics/work.md": "remote"})
		dir := t.TempDir()
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(dir, "topics")); err != nil {
			t.Fatal(err)
		}
		if _, err := n.app.syncNotes("prj_one", dir); err == nil {
			t.Fatal("symlink accepted")
		}
		if _, err := os.Stat(filepath.Join(outside, "work.md")); !os.IsNotExist(err) {
			t.Fatal("escaped root")
		}
	})
	t.Run("case alias", func(t *testing.T) {
		n := newNotesFixture(t, map[string]string{"topics/Work/one.md": "one", "topics/work/two.md": "two"})
		if _, err := n.app.syncNotes("prj_one", t.TempDir()); err == nil {
			t.Fatal("case alias accepted")
		}
	})
}
func TestNotesSyncLocalEditConflictsEvenWithUnchangedRemote(t *testing.T) {
	n := newNotesFixture(t, map[string]string{"index.md": "base"})
	dir := t.TempDir()
	if _, err := n.app.syncNotes("prj_one", dir); err != nil {
		t.Fatal(err)
	}
	baseline := readNotesTestFile(t, dir, notesBaselineName)
	writeNotesTestFile(t, dir, "index.md", "local edit")
	if _, err := n.app.syncNotes("prj_one", dir); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("%v", err)
	}
	if n.posts != 0 || n.files["index.md"] != "base" || readNotesTestFile(t, dir, "index.md") != "local edit" || readNotesTestFile(t, dir, notesBaselineName) != baseline {
		t.Fatal("local edit or remote mutated")
	}
}
func TestNotesSyncDetectsConcurrentLocalEdit(t *testing.T) {
	n := newNotesFixture(t, map[string]string{"index.md": "base"})
	dir := t.TempDir()
	if _, err := n.app.syncNotes("prj_one", dir); err != nil {
		t.Fatal(err)
	}
	baseline := readNotesTestFile(t, dir, notesBaselineName)
	n.files["index.md"] = "new remote"
	n.revision++
	n.beforeGet = func() { writeNotesTestFile(t, dir, "index.md", "new local edit") }
	if _, err := n.app.syncNotes("prj_one", dir); err == nil || !strings.Contains(err.Error(), "local notes changed") {
		t.Fatalf("%v", err)
	}
	if readNotesTestFile(t, dir, notesBaselineName) != baseline || readNotesTestFile(t, dir, "index.md") != "new local edit" {
		t.Fatal("concurrent edit lost")
	}
}
