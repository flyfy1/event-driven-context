package v2client

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"event-driven-context/internal/v2"
)

func testCatalogEntry() v2.FileCatalogEntry {
	return v2.FileCatalogEntry{FileInfo: v2.FileInfo{ID: "file_one", ProjectID: "prj_one", Filename: "a.txt", MediaType: "text/plain", SizeBytes: 3, SHA256: NoteHash("abc"), Referenced: true}, ReferenceCount: 1, References: []v2.FileReference{{EventID: "evt_one", Sequence: 2, Type: "note", Relation: "attachment"}}, ReferencesComplete: true}
}
func TestFileCatalogClientValidatesScopeAndBoundedPreview(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*v2.FileCatalogPage)
	}{
		{"foreign project", func(p *v2.FileCatalogPage) { p.ProjectID = "prj_other" }},
		{"foreign file", func(p *v2.FileCatalogPage) { p.Files[0].ProjectID = "prj_other" }},
		{"wrong boundary", func(p *v2.FileCatalogPage) { p.ThroughSequence = 8 }},
		{"unsafe ID", func(p *v2.FileCatalogPage) { p.Files[0].ID = "../../escape" }},
		{"checksum", func(p *v2.FileCatalogPage) { p.Files[0].SHA256 = "bad" }},
		{"preview overflow", func(p *v2.FileCatalogPage) {
			for i := 0; i < 5; i++ {
				p.Files[0].References = append(p.Files[0].References, p.Files[0].References[0])
			}
			p.Files[0].ReferenceCount = 6
		}},
		{"future reference", func(p *v2.FileCatalogPage) { p.Files[0].References[0].Sequence = 9 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				p := v2.FileCatalogPage{ProjectID: "prj_one", ThroughSequence: 7, Files: []v2.FileCatalogEntry{testCatalogEntry()}}
				test.mutate(&p)
				json.NewEncoder(w).Encode(p)
			})
			through := int64(7)
			if _, err := c.ListFiles(context.Background(), "prj_one", v2.FileCatalogInput{ThroughSequence: &through}); err == nil {
				t.Fatal("invalid catalog accepted")
			}
		})
	}
}
func TestFileCatalogClientRoutesAndFreezesQueries(t *testing.T) {
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Query().Get("through_sequence") != "7" {
			t.Errorf("unexpected request: %s", r.URL)
		}
		switch r.URL.Path {
		case "/v1/projects/prj_one/files/catalog":
			if r.URL.Query().Get("cursor") != "a+b?" || r.URL.Query().Get("limit") != "1" {
				t.Error("catalog query lost")
			}
			json.NewEncoder(w).Encode(v2.FileCatalogPage{ProjectID: "prj_one", ThroughSequence: 7, Files: []v2.FileCatalogEntry{testCatalogEntry()}})
		case "/v1/projects/prj_one/files/file_one/metadata":
			json.NewEncoder(w).Encode(testCatalogEntry())
		case "/v1/projects/prj_one/files/file_one/references":
			json.NewEncoder(w).Encode(v2.FileReferencesPage{ProjectID: "prj_one", FileID: "file_one", ThroughSequence: 7, References: testCatalogEntry().References})
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
		}
	})
	through := int64(7)
	ctx := context.Background()
	if _, err := c.ListFiles(ctx, "prj_one", v2.FileCatalogInput{Limit: 1, Cursor: "a+b?", ThroughSequence: &through}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.FileMetadata(ctx, "prj_one", "file_one", &through); err != nil {
		t.Fatal(err)
	}
	if _, err := c.FileReferences(ctx, "prj_one", "file_one", v2.FileCatalogInput{ThroughSequence: &through}); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("missing requests")
	}
	if _, err := c.FileMetadata(ctx, "prj_one", "../escape", nil); err == nil || calls != 3 {
		t.Fatal("unsafe ID requested")
	}
}
func TestFileMetadataRejectsUnexpectedIdentity(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		entry := testCatalogEntry()
		entry.ID = "file_other"
		json.NewEncoder(w).Encode(entry)
	})
	if _, err := c.FileMetadata(context.Background(), "prj_one", "file_one", nil); err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("%v", err)
	}
}
