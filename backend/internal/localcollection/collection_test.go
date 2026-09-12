package localcollection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"event-driven-context/internal/v2"
	"event-driven-context/internal/v2client"
)

type catalogFixture struct {
	client     *v2client.Client
	server     *httptest.Server
	entries    []v2.FileCatalogEntry
	bodies     map[string]string
	downloads  map[string]int
	latest     int64
	badFile    string
	onDownload func(string)
	boundaries []string
}

func newCatalogFixture(t *testing.T) *catalogFixture {
	t.Helper()
	f := &catalogFixture{latest: 7, bodies: map[string]string{"file_a": "alpha", "file_b": "beta"}, downloads: map[string]int{}}
	for i, id := range []string{"file_a", "file_b"} {
		name := id + ".txt"
		if i == 0 {
			name = "../../metadata.json"
		}
		f.entries = append(f.entries, v2.FileCatalogEntry{FileInfo: v2.FileInfo{ID: id, ProjectID: "prj_one", Filename: name, MediaType: "text/plain", SizeBytes: int64(len(f.bodies[id])), SHA256: digest([]byte(f.bodies[id])), Referenced: true}, ReferenceCount: 1, References: []v2.FileReference{{EventID: "evt_" + id, Sequence: int64(i + 2), Type: "note", Relation: "attachment"}}, ReferencesComplete: true})
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected method or auth %s", r.Method)
		}
		prefix := "/v1/projects/prj_one/files/"
		endpoint := strings.TrimPrefix(r.URL.Path, prefix)
		if endpoint == "catalog" {
			boundary := f.latest
			if value := r.URL.Query().Get("through_sequence"); value != "" {
				var err error
				boundary, err = strconv.ParseInt(value, 10, 64)
				if err != nil {
					t.Error(err)
				}
			}
			f.boundaries = append(f.boundaries, r.URL.Query().Get("through_sequence"))
			page := v2.FileCatalogPage{ProjectID: "prj_one", ThroughSequence: boundary, Files: f.entries[:1], NextCursor: "page2"}
			if r.URL.Query().Get("cursor") == "page2" {
				page.Files = f.entries[1:]
				page.NextCursor = ""
			}
			json.NewEncoder(w).Encode(page)
			return
		}
		if strings.HasSuffix(endpoint, "/metadata") {
			id := strings.TrimSuffix(endpoint, "/metadata")
			for _, entry := range f.entries {
				if entry.ID == id {
					json.NewEncoder(w).Encode(entry)
					return
				}
			}
			http.NotFound(w, r)
			return
		}
		body, ok := f.bodies[endpoint]
		if !ok {
			http.NotFound(w, r)
			return
		}
		f.downloads[endpoint]++
		if f.onDownload != nil {
			f.onDownload(endpoint)
		}
		if f.badFile == endpoint {
			body = "wrong bytes with honest response hash"
		}
		w.Header().Set("X-EDC-File-ID", endpoint)
		w.Header().Set("X-EDC-SHA256", digest([]byte(body)))
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		fmt.Fprint(w, body)
	}))
	t.Cleanup(f.server.Close)
	var err error
	f.client, err = v2client.New(f.server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func openTestCollection(t *testing.T, dir string, f *catalogFixture) *Collection {
	t.Helper()
	c, err := Open(dir, f.server.URL, "prj_one")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func TestMetadataOnDemandAndAllBytesHaveIndependentCoverage(t *testing.T) {
	f := newCatalogFixture(t)
	dir := t.TempDir()
	c := openTestCollection(t, dir, f)
	if err := c.CompleteNotes(3, 2); err != nil {
		t.Fatal(err)
	}
	result, err := c.SyncFiles(context.Background(), f.client, false)
	if err != nil || result.Metadata != 2 || result.Pending != 2 || !result.Manifest.Catalog.Complete || result.Manifest.Catalog.ThroughSequence != 7 || result.Manifest.Bytes.Complete || result.Manifest.Notes.ThroughSequence != 2 {
		t.Fatalf("%+v %v", result, err)
	}
	if len(f.downloads) != 0 {
		t.Fatal("metadata sync downloaded bytes")
	}
	var metadata FileMetadata
	exists, err := c.readRecord("files/file_a/metadata.json", &metadata)
	if err != nil || !exists || metadata.Downloaded || metadata.File.Filename != "../../metadata.json" || metadata.LocalFilename != "original-metadata.json" {
		t.Fatalf("%+v %v", metadata, err)
	}
	f.latest = 10
	one, err := c.GetFile(context.Background(), f.client, "file_a")
	if err != nil || !one.Downloaded || c.Manifest().Catalog.ThroughSequence != 7 || c.Manifest().Bytes.Complete {
		t.Fatalf("%+v %v", one, err)
	}
	if bytes, err := os.ReadFile(one.LocalPath); err != nil || string(bytes) != "alpha" {
		t.Fatalf("%q %v", bytes, err)
	}
	one, err = c.GetFile(context.Background(), f.client, "file_a")
	if err != nil || !one.Cached || one.Downloaded || f.downloads["file_a"] != 1 {
		t.Fatalf("%+v %v", one, err)
	}
	result, err = c.SyncFiles(context.Background(), f.client, true)
	if err != nil || result.Cached != 1 || result.Downloaded != 1 || !result.Manifest.Bytes.Complete || result.Manifest.Bytes.ThroughSequence != 10 || result.Manifest.Notes.ThroughSequence != 2 {
		t.Fatalf("%+v %v", result, err)
	}
	result, err = c.SyncFiles(context.Background(), f.client, true)
	if err != nil || result.Cached != 2 || result.Downloaded != 0 || f.downloads["file_a"] != 1 || f.downloads["file_b"] != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	for _, value := range f.boundaries {
		if value != "" && value != "7" && value != "10" {
			t.Fatal("unexpected catalog boundary")
		}
	}
}
func TestChecksumFailureLeavesResumableIncompleteCollection(t *testing.T) {
	f := newCatalogFixture(t)
	dir := t.TempDir()
	c := openTestCollection(t, dir, f)
	f.badFile = "file_b"
	result, err := c.SyncFiles(context.Background(), f.client, true)
	if err == nil || !strings.Contains(err.Error(), "catalog checksum") || result.Downloaded != 1 || result.Unavailable != 1 || result.Manifest.Catalog.Complete || result.Manifest.Bytes.Complete || result.Manifest.Catalog.Cursor != "page2" {
		t.Fatalf("%+v %v", result, err)
	}
	var metadata FileMetadata
	if _, err := c.readRecord("files/file_b/metadata.json", &metadata); err != nil || metadata.Downloaded {
		t.Fatalf("failed download registered: %+v %v", metadata, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "files/file_b", SafeFilename(f.entries[1].Filename))); !os.IsNotExist(err) {
		t.Fatal("unverified bytes published")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	f.badFile = ""
	f.latest = 20
	c = openTestCollection(t, dir, f)
	result, err = c.SyncFiles(context.Background(), f.client, true)
	if err != nil || result.Cached != 1 || result.Downloaded != 1 || !result.Manifest.Bytes.Complete || result.Manifest.Bytes.ThroughSequence != 7 || f.downloads["file_a"] != 1 {
		t.Fatalf("retry did not resume frozen snapshot: %+v %v", result, err)
	}
}
func TestCollectionPreservesLocalBytesAndMetadataEdits(t *testing.T) {
	for _, kind := range []string{"bytes", "metadata", "cache path"} {
		t.Run(kind, func(t *testing.T) {
			f := newCatalogFixture(t)
			dir := t.TempDir()
			c := openTestCollection(t, dir, f)
			if _, err := c.SyncFiles(context.Background(), f.client, true); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(dir, "files/file_a", SafeFilename(f.entries[0].Filename))
			edited := []byte("other")
			if kind != "bytes" {
				target = filepath.Join(dir, "files/file_a/metadata.json")
				raw, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "metadata" {
					edited = []byte(strings.Replace(string(raw), "../../metadata.json", "local-name", 1))
				} else {
					var envelope record
					json.Unmarshal(raw, &envelope)
					var metadata FileMetadata
					json.Unmarshal(envelope.Data, &metadata)
					metadata.LocalFilename = "../../outside"
					envelope.Data, _ = json.Marshal(metadata)
					envelope.SHA256 = digest(envelope.Data)
					edited, _ = json.Marshal(envelope)
				}
			}
			if err := os.WriteFile(target, edited, 0600); err != nil {
				t.Fatal(err)
			}
			result, err := c.SyncFiles(context.Background(), f.client, true)
			var conflict *ConflictError
			if err == nil || !errors.As(err, &conflict) || result.Conflicting != 1 || result.Manifest.Bytes.Complete {
				t.Fatalf("%+v %v", result, err)
			}
			actual, err := os.ReadFile(target)
			if err != nil || string(actual) != string(edited) {
				t.Fatal("local edit was overwritten")
			}
			if f.downloads["file_a"] != 1 {
				t.Fatal("conflict attempted overwrite download")
			}
		})
	}
}
func TestCollectionRejectsForeignBindingSymlinksAndConcurrentOperations(t *testing.T) {
	f := newCatalogFixture(t)
	dir := t.TempDir()
	c := openTestCollection(t, dir, f)
	if other, err := Open(dir, f.server.URL, "prj_one"); err == nil {
		other.Close()
		t.Fatal("concurrent collection operation admitted")
	}
	c.Close()
	if other, err := Open(dir, f.server.URL, "prj_other"); err == nil {
		other.Close()
		t.Fatal("foreign project admitted")
	}
	if other, err := Open(dir, "https://other.example", "prj_one"); err == nil {
		other.Close()
		t.Fatal("foreign origin admitted")
	}
	c = openTestCollection(t, dir, f)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "files")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetFile(context.Background(), f.client, "file_a"); err == nil {
		t.Fatal("symlink accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("escaped collection")
	}
}
func TestConcurrentMetadataEditIsNotOverwrittenByDownloadRegistration(t *testing.T) {
	f := newCatalogFixture(t)
	dir := t.TempDir()
	c := openTestCollection(t, dir, f)
	metadataPath := filepath.Join(dir, "files/file_a/metadata.json")
	f.onDownload = func(id string) {
		if id == "file_a" {
			if err := os.WriteFile(metadataPath, []byte("user metadata edit"), 0600); err != nil {
				t.Error(err)
			}
		}
	}
	_, err := c.GetFile(context.Background(), f.client, "file_a")
	var conflict *ConflictError
	if err == nil || !errors.As(err, &conflict) {
		t.Fatalf("%v", err)
	}
	raw, err := os.ReadFile(metadataPath)
	if err != nil || string(raw) != "user metadata edit" {
		t.Fatal("concurrent metadata edit overwritten")
	}
	if c.Manifest().Bytes.Complete {
		t.Fatal("single failed cache marked collection complete")
	}
}
func TestSafeFilenameCannotCollideWithMetadataOrEscape(t *testing.T) {
	for _, display := range []string{"metadata.json", "../escape", "..\\escape", "CON", "/", strings.Repeat("x", 1000), "你好.png"} {
		name := SafeFilename(display)
		if filepath.Base(name) != name || strings.ContainsAny(name, "/\\") || name == "metadata.json" || strings.HasPrefix(name, ".") || len(name) > 129 {
			t.Fatalf("unsafe result %q from %q", name, display)
		}
	}
}

func TestNewerNotesRestartStaleIncompleteCatalog(t *testing.T) {
	f := newCatalogFixture(t)
	dir := t.TempDir()
	c := openTestCollection(t, dir, f)
	f.badFile = "file_b"
	if _, err := c.SyncFiles(context.Background(), f.client, true); err == nil {
		t.Fatal("expected interrupted snapshot")
	}
	if err := c.CompleteNotes(4, 15); err != nil {
		t.Fatal(err)
	}
	f.badFile = ""
	f.latest = 20
	f.boundaries = nil
	result, err := c.SyncFiles(context.Background(), f.client, true)
	if err != nil || result.Manifest.Catalog.ThroughSequence != 20 || result.Manifest.Bytes.ThroughSequence != 20 || len(f.boundaries) == 0 || f.boundaries[0] != "" {
		t.Fatalf("did not restart obsolete catalog: %+v %v %v", result, f.boundaries, err)
	}
	f.latest = 10
	if _, err := c.SyncFiles(context.Background(), f.client, false); err == nil || !strings.Contains(err.Error(), "older than synced notes") {
		t.Fatalf("accepted catalog behind notes: %v", err)
	}
}
func TestMetadataOnlyDetectsMissingOrEditedCachedBytes(t *testing.T) {
	for _, mode := range []string{"missing", "edited"} {
		t.Run(mode, func(t *testing.T) {
			f := newCatalogFixture(t)
			dir := t.TempDir()
			c := openTestCollection(t, dir, f)
			if _, err := c.SyncFiles(context.Background(), f.client, true); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(dir, "files/file_a", SafeFilename(f.entries[0].Filename))
			if mode == "missing" {
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(name, []byte("other"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			result, err := c.SyncFiles(context.Background(), f.client, false)
			if result.Manifest.Bytes.Complete {
				t.Fatal("stale bytes completion survived detection")
			}
			if mode == "missing" && (err != nil || result.Pending != 1) {
				t.Fatalf("%+v %v", result, err)
			}
			if mode == "edited" && err == nil {
				t.Fatal("edited cache silently accepted")
			}
			if f.downloads["file_a"] != 1 {
				t.Fatal("metadata-only sync downloaded bytes")
			}
		})
	}
}
func TestTruncatedFilenamePreservesExtensionAndDistinguishesNames(t *testing.T) {
	a := SafeFilename(strings.Repeat("x", 200) + "a.png")
	b := SafeFilename(strings.Repeat("x", 200) + "b.png")
	if a == b || !strings.HasSuffix(a, ".png") || !strings.HasSuffix(b, ".png") || len(a) > 129 {
		t.Fatalf("invalid truncated filenames: %q %q", a, b)
	}
}
