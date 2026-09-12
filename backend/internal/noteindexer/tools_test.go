package noteindexer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"event-driven-context/internal/notes"
	"event-driven-context/internal/v2"
	"event-driven-context/internal/v2client"
)

func testDraft() *draft {
	d := &draft{project: "prj_one", run: notes.OrganizationRun{ThroughSequence: 10, Snapshot: notes.Export{ProjectID: "prj_one", Files: []notes.File{}}}, files: map[string]string{}, read: map[string]bool{}, accounted: map[string]string{}}
	seedDefaults(d.files)
	return d
}
func callDraft(t *testing.T, d *draft, name string, in toolArgs) any {
	t.Helper()
	out, err := d.call(context.Background(), name, in)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}
func TestSearchNotesUsesOriginalUnicodeOffsets(t *testing.T) {
	d := testDraft()
	// U+023A is two UTF-8 bytes; its lowercase U+2C65 is three.
	d.files["topics/work/unicode.md"] = "ȺȺȺ NEEDLE 中文 end"
	out := callDraft(t, d, "search_notes", toolArgs{Query: "needle"}).([]map[string]string)
	if len(out) != 1 || out[0]["snippet"] != "NEEDLE 中文 end" || !utf8.ValidString(out[0]["snippet"]) {
		t.Fatalf("bad Unicode snippet: %+v", out)
	}
	d.files["topics/work/literal.md"] = "the literal [a.*] expression"
	out = callDraft(t, d, "search_notes", toolArgs{Query: "[a.*]"}).([]map[string]string)
	if len(out) != 1 || !strings.HasPrefix(out[0]["snippet"], "[a.*]") {
		t.Fatalf("query interpreted as regex: %+v", out)
	}
}
func TestDraftReadAndListingBounds(t *testing.T) {
	d := testDraft()
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = fmt.Sprintf("# Heading %03d", i+1)
	}
	d.files["topics/work/long.md"] = strings.Join(lines, "\n")
	out := callDraft(t, d, "read_note", toolArgs{Path: "topics/work/long.md", Start: 2, Limit: 1000}).(map[string]any)
	if len(strings.Split(out["text"].(string), "\n")) != 120 || out["next_start"] != 122 || out["has_more"] != true {
		t.Fatalf("read bounds: %+v", out)
	}
	outline := callDraft(t, d, "read_note", toolArgs{Path: "topics/work/long.md", Outline: true}).(map[string]any)
	if len(outline["headings"].([]string)) != 60 {
		t.Fatal("outline not bounded")
	}
	d.files["topics/work/large.md"] = strings.Repeat("界", 12001)
	if _, err := d.call(context.Background(), "read_note", toolArgs{Path: "topics/work/large.md"}); err == nil {
		t.Fatal("oversized note read accepted")
	}
	for i := 0; i < 30; i++ {
		d.run.Events = append(d.run.Events, notes.EventPreview{ID: fmt.Sprint(i)})
	}
	events := callDraft(t, d, "list_events", toolArgs{Start: -1, Limit: 100}).(map[string]any)
	if len(events["events"].([]notes.EventPreview)) != 20 || events["next_start"] != 20 {
		t.Fatalf("event bounds: %+v", events)
	}
	listing := callDraft(t, d, "list_notes", toolArgs{Folder: "topics"}).(map[string]any)
	for _, entry := range listing["entries"].([]map[string]string) {
		if strings.Count(entry["path"], "/") != 1 {
			t.Fatal("list_notes returned descendants")
		}
	}
}
func TestDraftReadsSourceBeforeUsingAndAccountsEveryEvent(t *testing.T) {
	d := testDraft()
	d.run.Events = []notes.EventPreview{{ID: "evt_one", Sequence: 4}, {ID: "evt_two", Sequence: 5}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e := v2.Event{ID: "evt_one", ProjectID: "prj_one", Sequence: 4, Type: "note", Content: v2.EventContent{Kind: "text", Text: strings.Repeat("界", 9000)}}
		if strings.HasSuffix(r.URL.Path, "evt_future") {
			e.ID = "evt_future"
			e.Sequence = 11
		}
		json.NewEncoder(w).Encode(e)
	}))
	defer server.Close()
	d.client, _ = v2client.New(server.URL, "plugin-token")
	if _, err := d.call(context.Background(), "account_event", toolArgs{EventID: "evt_one", Disposition: "used"}); err == nil {
		t.Fatal("used unread event")
	}
	out := callDraft(t, d, "read_event", toolArgs{EventID: "evt_one", Start: 1, Limit: 99999}).(map[string]any)
	if utf8.RuneCountInString(out["text"].(string)) != 8000 || out["next_start"] != 8001 || out["has_more"] != true {
		t.Fatalf("source bounds: %+v", out)
	}
	if _, err := d.call(context.Background(), "read_event", toolArgs{EventID: "evt_future"}); err == nil || d.read["evt_future"] {
		t.Fatal("read beyond source snapshot")
	}
	callDraft(t, d, "account_event", toolArgs{EventID: "evt_one", Disposition: "used"})
	if _, err := d.call(context.Background(), "finish", toolArgs{}); err == nil || d.finished {
		t.Fatal("finished without complete accounting")
	}
	if _, err := d.call(context.Background(), "account_event", toolArgs{EventID: "outside", Disposition: "irrelevant"}); err == nil {
		t.Fatal("accounted foreign event")
	}
	callDraft(t, d, "account_event", toolArgs{EventID: "evt_two", Disposition: "irrelevant"})
	callDraft(t, d, "finish", toolArgs{})
	if !d.finished {
		t.Fatal("valid draft not finished")
	}
	callDraft(t, d, "write_note", toolArgs{Path: "index.md", Content: d.files["index.md"] + "\nChanged.\n"})
	if d.finished {
		t.Fatal("mutation retained finished flag")
	}
}
func TestDraftFinishChecksLinksSourcesAndStableIDs(t *testing.T) {
	for _, tc := range []struct{ name, extra string }{
		{"unknown wiki", "[[note_missing]]"}, {"unread event", "[Source](edc-event://evt_unread)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := testDraft()
			d.files["index.md"] += "\n" + tc.extra
			if _, err := d.call(context.Background(), "finish", toolArgs{}); err == nil || d.finished {
				t.Fatal("invalid reference accepted")
			}
		})
	}
	d := testDraft()
	original := "---\nid: note_detail\ntitle: Detail\n---\n# Detail\n\n> A useful detail.\n"
	d.files["topics/work/detail.md"] = original
	d.run.Snapshot.Files = []notes.File{{Path: "topics/work/detail.md", Content: original}}
	d.files["index.md"] += "\n[[note_detail]]\n"
	callDraft(t, d, "move_note", toolArgs{From: "topics/work/detail.md", To: "topics/life/detail.md"})
	callDraft(t, d, "finish", toolArgs{})
	if d.files["topics/life/detail.md"] != original {
		t.Fatal("move changed ID/content")
	}
	callDraft(t, d, "write_note", toolArgs{Path: "topics/life/detail.md", Content: strings.Replace(original, "note_detail", "note_replacement", 1)})
	if _, err := d.call(context.Background(), "finish", toolArgs{}); err == nil {
		t.Fatal("stable identity removal accepted")
	}
}
