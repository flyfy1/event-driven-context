package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"event-driven-context/internal/localcollection"
	"event-driven-context/internal/v2"
	"event-driven-context/internal/v2client"
)

func TestAggregateSyncAndCacheGetRemainPullOnly(t *testing.T) {
	noteText := "# Notes\n"
	catalogCalls, downloads := 0, 0
	entry := v2.FileCatalogEntry{FileInfo: v2.FileInfo{ID: "file_one", ProjectID: "prj_one", Filename: "note.txt", MediaType: "text/plain", SizeBytes: 5, SHA256: v2client.NoteHash("hello"), Referenced: true}, ReferenceCount: 1, References: []v2.FileReference{{EventID: "evt_one", Sequence: 5, Type: "note", Relation: "attachment"}}, ReferencesComplete: true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("sync attempted mutation: %s", r.Method)
		}
		switch r.URL.Path {
		case "/v1/projects/prj_one/notes/export":
			json.NewEncoder(w).Encode(v2client.NotesExport{ProjectID: "prj_one", Revision: 2, ThroughSequence: 3, Files: []v2client.NotesFile{{Path: "index.md", Content: noteText, SHA256: v2client.NoteHash(noteText)}}})
		case "/v1/projects/prj_one/files/catalog":
			catalogCalls++
			json.NewEncoder(w).Encode(v2.FileCatalogPage{ProjectID: "prj_one", ThroughSequence: 7, Files: []v2.FileCatalogEntry{entry}})
		case "/v1/projects/prj_one/files/file_one/metadata":
			json.NewEncoder(w).Encode(entry)
		case "/v1/projects/prj_one/files/file_one":
			downloads++
			w.Header().Set("X-EDC-File-ID", entry.ID)
			w.Header().Set("X-EDC-SHA256", entry.SHA256)
			w.Header().Set("Content-Length", strconv.Itoa(5))
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "hello")
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := v2client.New(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	a := &app{ctx: context.Background(), client: client, token: "token", io: streams{in: strings.NewReader(""), out: io.Discard, err: io.Discard}}
	dir := t.TempDir()
	if err = a.dispatch("sync", []string{"--project", "prj_one", "--output", dir}); err != nil {
		t.Fatal(err)
	}
	if downloads != 0 {
		t.Fatal("default sync downloaded bytes")
	}
	if got := readNotesTestFile(t, dir, "notes/index.md"); got != noteText {
		t.Fatal("notes not in collection")
	}
	c, err := localcollection.Open(dir, server.URL, "prj_one")
	if err != nil {
		t.Fatal(err)
	}
	manifest := c.Manifest()
	c.Close()
	if manifest.Notes.Revision != 2 || manifest.Notes.ThroughSequence != 3 || manifest.Catalog.ThroughSequence != 7 || !manifest.Notes.Complete || !manifest.Catalog.Complete || manifest.Bytes.Complete {
		t.Fatalf("wrong independent coverage: %+v", manifest)
	}
	if err = a.file([]string{"get", "--project", "prj_one", "--cache", dir, "file_one"}); err != nil {
		t.Fatal(err)
	}
	if downloads != 1 {
		t.Fatal("cache get did not download once")
	}
	if err = a.file([]string{"get", "--project", "prj_one", "--cache", dir, "file_one"}); err != nil || downloads != 1 {
		t.Fatalf("cached bytes not reused: %v", err)
	}
	before := catalogCalls
	if err = a.file([]string{"get", "--project", "prj_one", "--cache", dir, "-o", "-", "file_one"}); err == nil || !strings.Contains(err.Error(), "mutually exclusive") || catalogCalls != before {
		t.Fatalf("cache/-o not rejected: %v", err)
	}
	if err = a.dispatch("sync", []string{"--project", "prj_one", "--output", dir, "--files", "all"}); err != nil || downloads != 1 {
		t.Fatalf("all sync did not reuse cache: %v", err)
	}
	if err = os.WriteFile(filepath.Join(dir, "notes/index.md"), []byte("local edit"), 0600); err != nil {
		t.Fatal(err)
	}
	before = catalogCalls
	if err = a.dispatch("sync", []string{"--project", "prj_one", "--output", dir}); err == nil || !strings.Contains(err.Error(), "conflicts") || catalogCalls != before {
		t.Fatalf("notes conflict did not preflight collection: %v", err)
	}
	if got := readNotesTestFile(t, dir, "notes/index.md"); got != "local edit" {
		t.Fatal("local notes overwritten")
	}
}
