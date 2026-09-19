package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func importFixtureInput() HubImportInput {
	return HubImportInput{ProviderID: "whatsapp-import", AccountID: "personal-chat", DisplayName: "Chat export", Filename: "chat.txt", Format: "whatsapp-text", Content: "19/09/2026, 10:12 - Alice: First line\r\nsecond line\r\n[9/19/26, 10:13:04 PM] Bob: Reply\n"}
}
func importSecret(t *testing.T, s *Store, ctx context.Context, c HubConnection, key []byte) string {
	t.Helper()
	value, err := s.HubCredential(ctx, c, key)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestHubImportWhatsAppPreservesOriginalAndPagination(t *testing.T) {
	s, owner, _, _, key := hubFixture(t)
	in := importFixtureInput()
	result, err := s.ImportHubSnapshot(owner, in, key)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Import.Snapshot || result.Import.Live || result.Import.RecordCount != 2 {
		t.Fatalf("wrong snapshot metadata: %+v", result.Import)
	}
	page, err := s.ExecuteHubImport(owner, result.Connection, importSecret(t, s, owner, result.Connection, key), map[string]any{"limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	first := page.Records[0]
	if first.Sender != "Alice" || first.Text != "First line\nsecond line" || !first.TimestampUncertain || first.TimestampText != "19/09/2026, 10:12" || page.NextOffset == nil || *page.NextOffset != 1 {
		t.Fatalf("bad first record %+v", first)
	}
	next, err := s.ExecuteHubImport(owner, result.Connection, importSecret(t, s, owner, result.Connection, key), map[string]any{"offset": 1, "limit": 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Records) != 1 || next.Records[0].Sender != "Bob" || next.NextOffset != nil || first.Raw+next.Records[0].Raw != in.Content {
		t.Fatal("original text or pagination lost")
	}
	raw, err := os.ReadFile(filepath.Join(s.dataDir, "hub-imports", result.Import.ID+".source"))
	if err != nil || string(raw) != in.Content {
		t.Fatal("source bytes not preserved", err)
	}
	for _, name := range []string{"", result.Import.ID + ".source", result.Import.ID + ".json"} {
		info, err := os.Stat(filepath.Join(s.dataDir, "hub-imports", name))
		if err != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatal("import permissions not private", err)
		}
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "First line") || strings.Contains(string(encoded), s.dataDir) || strings.Contains(string(encoded), "hub_import") {
		t.Fatal("discovery exposed content, reference or storage path")
	}
}
func TestHubImportReconnectIsAppendOnlyAndRevokesGrants(t *testing.T) {
	s, owner, _, registration, key := hubFixture(t)
	if err := s.DecideHubAgent(owner, registration.ID, "approve", registration.VerificationCode); err != nil {
		t.Fatal(err)
	}
	a, err := s.HubAgent(owner, registration.Token, true)
	if err != nil {
		t.Fatal(err)
	}
	in := importFixtureInput()
	one, err := s.ImportHubSnapshot(owner, in, key)
	if err != nil {
		t.Fatal(err)
	}
	request, err := s.RequestHubAccess(owner, a, one.Connection.ID, "records.list", "Read chat snapshot", 3600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DecideHubRequest(owner, request.ID, "approve"); err != nil {
		t.Fatal(err)
	}
	in.Content = "19/09/2026, 11:00 - Alice: New snapshot\n"
	two, err := s.ImportHubSnapshot(owner, in, key)
	if err != nil {
		t.Fatal(err)
	}
	if one.Import.ID == two.Import.ID || one.Connection.ID != two.Connection.ID {
		t.Fatal("snapshot identity or account identity wrong")
	}
	if err = s.CheckHubAccess(owner, a, two.Connection.ID, "records.list"); err == nil {
		t.Fatal("replacement snapshot inherited old grant")
	}
	original, err := os.ReadFile(filepath.Join(s.dataDir, "hub-imports", one.Import.ID+".source"))
	if err != nil || string(original) != importFixtureInput().Content {
		t.Fatal("prior snapshot overwritten")
	}
	page, err := s.ExecuteHubImport(owner, two.Connection, importSecret(t, s, owner, two.Connection, key), nil)
	if err != nil || len(page.Records) != 1 || !strings.Contains(page.Records[0].Raw, "New snapshot") {
		t.Fatal("new snapshot not selected", err)
	}
}
func TestHubImportCrossOwnerProviderAndReferenceIsolation(t *testing.T) {
	s, owner, other, _, key := hubFixture(t)
	imported, err := s.ImportHubSnapshot(owner, importFixtureInput(), key)
	if err != nil {
		t.Fatal(err)
	}
	secret := importSecret(t, s, owner, imported.Connection, key)
	otherImport, err := s.ImportHubSnapshot(other, importFixtureInput(), key)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []HubConnection{otherImport.Connection, {ID: imported.Connection.ID, OwnerID: imported.Connection.OwnerID, ProviderID: "calendar-import", AccountID: imported.Connection.AccountID, Status: "configured"}, {ID: imported.Connection.ID, OwnerID: imported.Connection.OwnerID, ProviderID: imported.Connection.ProviderID, AccountID: "different", Status: "configured"}} {
		if _, err = s.ExecuteHubImport(owner, c, secret, nil); err == nil {
			t.Fatal("mismatched account/provider/owner read original")
		}
	}
	var ref hubImportReference
	_ = json.Unmarshal([]byte(secret), &ref)
	ref.ID = "../../secret"
	traversal, _ := json.Marshal(ref)
	if _, err = s.ExecuteHubImport(owner, imported.Connection, string(traversal), nil); err == nil {
		t.Fatal("path traversal accepted")
	}
	if err = s.DisconnectHubConnection(owner, imported.Connection.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ExecuteHubImport(owner, imported.Connection, secret, nil); err == nil {
		t.Fatal("disconnected reference still readable")
	}
}
func TestHubImportICSAndMarkdownDoNotInventCalendarSemantics(t *testing.T) {
	s, owner, _, _, key := hubFixture(t)
	source := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:demo\r\nDTSTART;TZID=Asia/Singapore:20260919T100000\r\nSUMMARY:Original\r\n continuation\r\nRRULE:FREQ=DAILY;COUNT=3\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	result, err := s.ImportHubSnapshot(owner, HubImportInput{ProviderID: "calendar-import", AccountID: "calendar", DisplayName: "Calendar export", Filename: "calendar.ics", Format: "ics", Content: source}, key)
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.ExecuteHubImport(owner, result.Connection, importSecret(t, s, owner, result.Connection, key), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 3 || page.Records[1].Kind != "ical_event" || page.Records[1].Fields["DTSTART;TZID=Asia/Singapore"] != "20260919T100000" || page.Records[1].Fields["SUMMARY"] != "Originalcontinuation" {
		t.Fatalf("bad components %+v", page.Records)
	}
	joined := ""
	for _, r := range page.Records {
		joined += r.Raw
	}
	if joined != source {
		t.Fatal("ICS source not preserved")
	}
	if !strings.Contains(strings.Join(page.Import.Limitations, " "), "Recurrences are not expanded") {
		t.Fatal("calendar parsing claims missing")
	}
	markdown, err := s.ImportHubSnapshot(owner, HubImportInput{ProviderID: "markdown-import", AccountID: "notes", DisplayName: "Notes", Filename: "note.md", Format: "markdown", Content: "# Title\n\nOriginal paragraph.\n"}, key)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.ExecuteHubImport(owner, markdown.Connection, importSecret(t, s, owner, markdown.Connection, key), nil)
	if err != nil || len(p.Records) != 1 || p.Records[0].Kind != "markdown" {
		t.Fatal("markdown snapshot failed", err)
	}
}
func TestHubImportRejectsInvalidInputAndPaginationAndTampering(t *testing.T) {
	s, owner, _, _, key := hubFixture(t)
	if _, err := s.ImportHubSnapshot(context.Background(), importFixtureInput(), key); err == nil {
		t.Fatal("anonymous import accepted")
	}
	mutations := []func(*HubImportInput){func(i *HubImportInput) { i.Content = strings.Repeat("x", HubImportMaxBytes+1) }, func(i *HubImportInput) { i.Content = string([]byte{0xff}) }, func(i *HubImportInput) { i.Filename = "../../secrets" }, func(i *HubImportInput) { i.ProviderID = "gmail" }, func(i *HubImportInput) { i.Content = "\x00" }}
	for _, mutate := range mutations {
		in := importFixtureInput()
		mutate(&in)
		if _, err := s.ImportHubSnapshot(owner, in, key); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	result, err := s.ImportHubSnapshot(owner, importFixtureInput(), key)
	if err != nil {
		t.Fatal(err)
	}
	secret := importSecret(t, s, owner, result.Connection, key)
	for _, args := range []map[string]any{{"limit": 101}, {"limit": 0}, {"offset": -1}, {"offset": 0.5}, {"offset": 100}, {"path": "../file"}} {
		if _, err = s.ExecuteHubImport(owner, result.Connection, secret, args); err == nil {
			t.Fatal("invalid pagination accepted", args)
		}
	}
	if err = os.WriteFile(filepath.Join(s.dataDir, "hub-imports", result.Import.ID+".source"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ExecuteHubImport(owner, result.Connection, secret, nil); err == nil {
		t.Fatal("source hash mismatch ignored")
	}
}
