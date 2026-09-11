package v2

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"event-driven-context/internal/core"
)

func TestSingleWriterLockRejectsConcurrentServiceAndReleasesOnClose(t *testing.T) {
	f := newFixture(t)
	dataDir := filepath.Join(f.root, "data")
	if second, err := New(f.identity, dataDir); err == nil {
		_ = second.Close()
		t.Fatal("second service acquired the same data directory")
	} else if !strings.Contains(err.Error(), "already open") {
		t.Fatalf("unclear lock error: %v", err)
	}
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(f.identity, dataDir)
	if err != nil {
		t.Fatalf("lock was not released by Close: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
}

func TestReturnedValuesCannotMutateStoredDataOrPermissions(t *testing.T) {
	f := newFixture(t)
	first, err := f.service.RecordEvents(f.aliceCtx, f.project.ID, RecordEventsInput{Events: []EventInput{textInput(eventOne, "source")}})
	if err != nil {
		t.Fatal(err)
	}
	seq := created(t, first).Sequence

	page, err := f.service.QueryEvents(f.aliceCtx, f.project.ID, QueryEventsInput{})
	if err != nil {
		t.Fatal(err)
	}
	page.Events[0].Metadata["count"][0] = '1'
	page.Events[0].Source["channel"] = json.RawMessage(`"hook"`)
	page.Events[0].Refs = append(page.Events[0].Refs, Ref{Rel: "replies_to", ID: eventTwo})
	got, err := f.service.GetEvent(f.aliceCtx, f.project.ID, eventOne)
	if err != nil || string(got.Metadata["count"]) != "9007199254740993" || sourceChannel(got.Source) != "app" || len(got.Refs) != 0 {
		t.Fatalf("query result mutated stored event: %#v err=%v", got, err)
	}
	got.Metadata["count"][0] = '2'
	again, err := f.service.GetEvent(f.aliceCtx, f.project.ID, eventOne)
	if err != nil || string(again.Metadata["count"]) != "9007199254740993" {
		t.Fatalf("get result mutated stored event: %#v err=%v", again, err)
	}

	manifest := Manifest{
		ID:      "alias-check",
		Version: "0.1.0",
		Name:    "Alias check",
		State:   []StateDeclaration{{Key: "current"}},
		Permissions: Permissions{
			ReadEvents: []string{"note"},
			WriteState: []string{"current"},
		},
	}
	installed, err := f.service.InstallPlugin(f.aliceCtx, f.project.ID, InstallPluginInput{Manifest: manifest, Config: json.RawMessage(`{"prompt":"safe"}`)})
	if err != nil {
		t.Fatal(err)
	}
	manifest.Permissions.ReadEvents[0] = "log"
	installed.Installation.Permissions.ReadEvents[0] = "log"
	installed.Installation.Manifest.Permissions.ReadEvents[0] = "derived"
	installed.Installation.Config[11] = 'X'
	installed.Installation.ConfigRevisions[0].Config[11] = 'Y'
	listed, err := f.service.ListPlugins(f.aliceCtx, f.project.ID)
	if err != nil || len(listed) != 1 || listed[0].Permissions.ReadEvents[0] != "note" || listed[0].Manifest.Permissions.ReadEvents[0] != "note" || string(listed[0].Config) != `{"prompt":"safe"}` || string(listed[0].ConfigRevisions[0].Config) != `{"prompt":"safe"}` {
		t.Fatalf("installation alias changed stored authority: %#v err=%v", listed, err)
	}
	listed[0].Permissions.ReadEvents[0] = "log"
	relisted, err := f.service.ListPlugins(f.aliceCtx, f.project.ID)
	if err != nil || relisted[0].Permissions.ReadEvents[0] != "note" {
		t.Fatalf("list result mutated stored authority: %#v err=%v", relisted, err)
	}

	principal, err := f.service.AuthenticatePlugin(installed.Token)
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	state, err := f.service.PutStateAsPlugin(context.Background(), principal, PutStateInput{
		Key:             "alias-check/current",
		ExpectedVersion: &zero,
		Content:         StateContent{Format: "text", Text: "safe"},
		Data:            json.RawMessage(`{"count":9007199254740993}`),
		BasedOnSequence: seq,
		Refs:            []string{eventOne},
	})
	if err != nil {
		t.Fatal(err)
	}
	state.Data[9] = '1'
	state.Refs[0] = eventTwo
	states, err := f.service.GetState(f.aliceCtx, f.project.ID, GetStateInput{Keys: []string{"alias-check/current"}})
	if err != nil || len(states.States) != 1 || string(states.States[0].Data) != `{"count":9007199254740993}` || states.States[0].Refs[0] != eventOne {
		t.Fatalf("put result mutated stored state: %#v err=%v", states, err)
	}
	states.States[0].Data[9] = '2'
	states.States[0].Refs[0] = eventTwo
	states, err = f.service.GetState(f.aliceCtx, f.project.ID, GetStateInput{Keys: []string{"alias-check/current"}})
	if err != nil || string(states.States[0].Data) != `{"count":9007199254740993}` || states.States[0].Refs[0] != eventOne {
		t.Fatalf("get result mutated stored state: %#v err=%v", states, err)
	}
}

func TestFileReadsAndDedupeFailClosedAfterBlobCorruption(t *testing.T) {
	f := newFixture(t)
	original := []byte("original")
	info, err := f.service.PutFile(f.aliceCtx, f.project.ID, FileUpload{Filename: "x.txt", MediaType: "text/plain", SizeBytes: int64(len(original)), Reader: bytes.NewReader(original)})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(f.root, "data", "v2", "files", info.ID), []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, reader, openErr := f.service.OpenFile(f.aliceCtx, f.project.ID, info.ID); errorCode(openErr) != "conflict" {
		if reader != nil {
			_ = reader.Close()
		}
		t.Fatalf("corrupt blob was returned: %v", openErr)
	}
	if _, retryErr := f.service.PutFile(f.aliceCtx, f.project.ID, FileUpload{Filename: "x.txt", MediaType: "text/plain", SizeBytes: int64(len(original)), Reader: bytes.NewReader(original)}); errorCode(retryErr) != "conflict" {
		t.Fatalf("dedupe falsely confirmed corrupt blob: %v", retryErr)
	}
}

func TestImageValidationRequiresACompleteDecodablePayload(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	if !validMedia("image/png", encoded.Bytes()) {
		t.Fatal("valid PNG rejected")
	}
	corrupt := bytes.Clone(encoded.Bytes())
	for offset := 8; offset+12 <= len(corrupt); {
		n := int(binary.BigEndian.Uint32(corrupt[offset : offset+4]))
		if string(corrupt[offset+4:offset+8]) == "IDAT" {
			for i := offset + 8; i < offset+8+n; i++ {
				corrupt[i] = 0
			}
			binary.BigEndian.PutUint32(corrupt[offset+8+n:offset+12+n], crc32.ChecksumIEEE(corrupt[offset+4:offset+8+n]))
			break
		}
		offset += 12 + n
	}
	if validMedia("image/png", corrupt) {
		t.Fatal("CRC-valid but undecodable PNG accepted")
	}
}

func TestGetStateRejectsNonPositiveVersion(t *testing.T) {
	f := newFixture(t)
	zero := int64(0)
	_, err := f.service.GetState(f.aliceCtx, f.project.ID, GetStateInput{Keys: []string{"project-brief/current"}, Version: &zero})
	var appErr *Error
	if !errors.As(err, &appErr) || appErr.Code != "invalid_input" {
		t.Fatalf("zero state version accepted: %v", err)
	}
	if errors.Is(err, core.ErrNotFound) {
		t.Fatalf("invalid version was hidden as not found: %v", err)
	}
}
