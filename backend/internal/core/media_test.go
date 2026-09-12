package core

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func wavFixture(totalBytes int) []byte {
	if totalBytes < 45 {
		totalBytes = 45
	}
	data := make([]byte, totalBytes)
	copy(data[:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(totalBytes-8))
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], 8000)
	binary.LittleEndian.PutUint32(data[28:32], 16000)
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], uint32(totalBytes-44))
	return data
}

func TestMediaTypeValidationUsesBoundedStructure(t *testing.T) {
	realM4A, err := os.ReadFile("testdata/fixture.m4a")
	if err != nil {
		t.Fatal(err)
	}
	if mediaType, err := validateMedia("audio/mp4", realM4A); err != nil || mediaType != "audio/mp4" {
		t.Fatalf("real AAC M4A rejected: %q %v", mediaType, err)
	}

	// ftyp plus attacker-controlled bytes containing the codec name is not a
	// valid audio track/sample description and must not pass the structural gate.
	fake := append([]byte{0, 0, 0, 16}, []byte("ftypM4A \x00\x00\x00\x00")...)
	fake = append(fake, []byte("arbitrary HTML <script>mp4a</script>")...)
	if isAACMP4(fake) {
		t.Fatal("unstructured mp4a marker accepted")
	}

	for _, data := range [][]byte{nil, {0xff}, {0xff, 0xfb}, {0xff, 0xfb, 0x90}, []byte("ID3\x04\x00\x00\x00\x00\x00\x20")} {
		if isMP3(data) {
			t.Fatalf("truncated MP3 accepted: %x", data)
		}
	}
	if !isMP3([]byte{0xff, 0xfb, 0x90, 0x64}) {
		t.Fatal("valid MP3 frame header rejected")
	}
	for _, mediaType := range []string{"audio/mp4", "audio/mpeg", "audio/wav", "text/html"} {
		if _, err = validateMedia(mediaType, []byte("<!doctype html><script>mp4a</script>")); err == nil {
			t.Fatalf("HTML accepted as %s", mediaType)
		}
	}
}

func FuzzMP3HeaderNeverPanics(f *testing.F) {
	for _, seed := range [][]byte{nil, {0xff}, {0xff, 0xfb}, {0xff, 0xfb, 0x90}, []byte("ID3\x04\x00\x00\x00\x00\x00\x01x")} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_ = isMP3(data)
	})
}

func TestMediaRecordLimitsAndIdempotency(t *testing.T) {
	s := openTest(t)
	ctx := user(t, s, "alice")
	p, err := s.CreateProject(ctx, ProjectInput{Name: "media"})
	if err != nil {
		t.Fatal(err)
	}
	data := wavFixture(MaxMediaBytes)
	in := MediaRecordInput{ProjectID: p.ID, Filename: "voice.wav", MediaType: "audio/wav", Data: data, IdempotencyKey: "capture-1"}
	event, err := s.RecordMediaEvent(ctx, in)
	if err != nil || event.Content.File == nil || event.Content.File.SizeBytes != MaxMediaBytes {
		t.Fatalf("20 MiB media rejected: %+v %v", event, err)
	}
	again, err := s.RecordMediaEvent(ctx, in)
	if err != nil || again.ID != event.ID {
		t.Fatalf("identical retry duplicated: %+v %v", again, err)
	}
	changed := in
	changed.Data = bytes.Clone(data)
	changed.Data[len(changed.Data)-1] = 1
	_, err = s.RecordMediaEvent(ctx, changed)
	requireError(t, err, ErrConflict)

	tooLarge := in
	tooLarge.IdempotencyKey = "capture-2"
	tooLarge.Data = append(data, 0)
	_, err = s.RecordMediaEvent(ctx, tooLarge)
	var appErr *Error
	if !errors.As(err, &appErr) || appErr.Code != "too_large" {
		t.Fatalf("oversize media error: %v", err)
	}
	records, err := s.loadProjectEvents(p.ID)
	if err != nil || len(records) != 1 {
		t.Fatalf("failed media left visible event: %d %v", len(records), err)
	}
}

func TestManifestSyncFailurePreservesReferencedMedia(t *testing.T) {
	s := openTest(t)
	ctx := user(t, s, "alice")
	p, err := s.CreateProject(ctx, ProjectInput{Name: "durability"})
	if err != nil {
		t.Fatal(err)
	}
	originalSync := syncDirectory
	syncDirectory = func(path string) error {
		if filepath.Base(path) == "events" {
			return errors.New("injected event directory sync failure")
		}
		return originalSync(path)
	}
	defer func() { syncDirectory = originalSync }()

	in := MediaRecordInput{ProjectID: p.ID, Filename: "voice.wav", MediaType: "audio/wav", Data: wavFixture(128), IdempotencyKey: "durable-retry"}
	if _, err = s.RecordMediaEvent(ctx, in); err == nil {
		t.Fatal("injected sync failure was not reported")
	}
	records, err := s.loadProjectEvents(p.ID)
	if err != nil || len(records) != 1 || records[0].Event.Content.File == nil {
		t.Fatalf("linked manifest missing after uncertain sync: %+v %v", records, err)
	}
	fileID := records[0].Event.Content.File.ID
	if _, err = os.Stat(s.filePath(p.ID, fileID)); err != nil {
		t.Fatalf("manifest references deleted media: %v", err)
	}
	syncDirectory = originalSync
	again, err := s.RecordMediaEvent(ctx, in)
	if err != nil || again.ID != records[0].Event.ID {
		t.Fatalf("idempotent retry did not resolve published manifest: %+v %v", again, err)
	}
}
