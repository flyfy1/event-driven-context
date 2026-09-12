package noteindexer

import (
	"context"
	"encoding/json"
	"event-driven-context/internal/notes"
	"event-driven-context/internal/v2"
	"event-driven-context/internal/v2client"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAttachmentToolsStayAtRunBoundaryAndValidateLinks(t *testing.T) {
	const fileID = "file_test"
	const eventID = "11111111-1111-4111-8111-111111111111"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.HasPrefix(r.URL.Path, "/v1/projects/prj_test/") {
			t.Error("project escaped")
		}
		if strings.Contains(r.URL.Path, "/files/") && r.URL.Query().Get("through_sequence") != "7" {
			t.Error("missing frozen boundary")
		}
		entry := v2.FileCatalogEntry{FileInfo: v2.FileInfo{ID: fileID, ProjectID: "prj_test", Filename: "record.txt", MediaType: "text/plain", SizeBytes: 5, SHA256: strings.Repeat("a", 64)}, ReferenceCount: 1, ReferencesComplete: true, References: []v2.FileReference{{EventID: eventID, Sequence: 3, Type: "note", Relation: "attachment"}}}
		switch {
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			json.NewEncoder(w).Encode(entry)
		case strings.HasSuffix(r.URL.Path, "/catalog"):
			if r.URL.Query().Get("limit") != "20" {
				t.Error("unbounded discovery")
			}
			json.NewEncoder(w).Encode(v2.FileCatalogPage{ProjectID: "prj_test", ThroughSequence: 7, Files: []v2.FileCatalogEntry{entry}})
		case strings.Contains(r.URL.Path, "/events/"):
			json.NewEncoder(w).Encode(v2.Event{ID: eventID, ProjectID: "prj_test", Sequence: 3, Type: "note", Content: v2.EventContent{Kind: "file", FileID: fileID}})
		default:
			http.Error(w, "unexpected download", 500)
		}
	}))
	defer server.Close()
	client, _ := v2client.New(server.URL, "test")
	d := &draft{client: client, project: "prj_test", run: notes.OrganizationRun{ThroughSequence: 7, Events: []notes.EventPreview{{ID: eventID, Sequence: 3, Kind: "file", Backfill: true}}}, files: map[string]string{}, read: map[string]bool{}, accounted: map[string]string{}}
	seedDefaults(d.files)
	ctx := context.Background()
	if _, err := d.call(ctx, "list_files", toolArgs{Limit: 1000}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.call(ctx, "file_metadata", toolArgs{FileID: fileID}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.call(ctx, "read_event", toolArgs{EventID: eventID}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.call(ctx, "account_event", toolArgs{EventID: eventID, Disposition: "used"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.call(ctx, "finish", toolArgs{}); err == nil {
		t.Fatal("missing file link accepted")
	}
	d.files["goals/priorities.md"] += fmt.Sprintf("\n[Attachment](edc-file://%s) [source](edc-event://%s)\n", fileID, eventID)
	if _, err := d.call(ctx, "finish", toolArgs{}); err != nil {
		t.Fatal(err)
	}
	d.files["goals/priorities.md"] += "\n[Foreign](edc-file://file_foreign)\n"
	if _, err := d.call(ctx, "finish", toolArgs{}); err == nil {
		t.Fatal("unknown file accepted")
	}
	if calls < 4 {
		t.Fatal("expected selective requests")
	}
}
