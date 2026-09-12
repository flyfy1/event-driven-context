package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"event-driven-context/internal/core"
	"event-driven-context/internal/transcription"
	"event-driven-context/internal/v2"
)

type fakeAudioTranscriber struct {
	calls int
}

func (f *fakeAudioTranscriber) Transcribe(_ context.Context, filename, mediaType string, size int64, reader io.Reader, _ transcription.Options) (transcription.Result, error) {
	f.calls++
	data, _ := io.ReadAll(reader)
	if filename != "meeting.wav" || mediaType != "audio/wav" || int64(len(data)) != size {
		return transcription.Result{}, &v2.Error{Code: "invalid_input", Message: "bad fixture"}
	}
	return transcription.Result{Text: "会议预算调整为三万元。", Model: "gpt-transcribe"}, nil
}

func TestV2AudioUploadTranscribesToSourceLinkedPluginEvent(t *testing.T) {
	f := newV2APIFixture(t)
	transcriber := &fakeAudioTranscriber{}
	f.handler = V2HandlerWithConfig(f.store, f.service, Config{PublicBaseURL: v2TestIssuer, AudioTranscriber: transcriber})
	ctx := core.WithUser(context.Background(), f.alice.ID)
	audio := apiWAV(256)
	file, err := f.service.PutFile(ctx, f.project.ID, v2.FileUpload{Filename: "meeting.wav", MediaType: "audio/wav", SizeBytes: int64(len(audio)), Reader: bytes.NewReader(audio)})
	if err != nil {
		t.Fatal(err)
	}
	sourceID := "55555555-5555-4555-8555-555555555555"
	written, err := f.service.RecordEvents(ctx, f.project.ID, v2.RecordEventsInput{Events: []v2.EventInput{{ID: sourceID, Type: "note", Content: v2.EventContent{Kind: "file", FileID: file.ID}, Source: map[string]json.RawMessage{"channel": json.RawMessage(`"web"`)}}}})
	if err != nil || written.Results[0].Status != "created" {
		t.Fatalf("source write: %#v %v", written, err)
	}
	path := "/v1/projects/" + f.project.ID + "/transcriptions"
	for attempt := 0; attempt < 2; attempt++ {
		w := f.request(t, http.MethodPost, path, f.token, "application/json", strings.NewReader(`{"source_event_id":"`+sourceID+`"}`))
		result := decodeV2Response[v2TranscriptionResult](t, w)
		if w.Code != http.StatusOK || result.TranscriptEvent.Content.Text != "会议预算调整为三万元。" || result.TranscriptEvent.Actor.ID != "audio-transcribe" {
			t.Fatalf("attempt %d: %d %#v body=%s", attempt, w.Code, result, w.Body.String())
		}
		if len(result.TranscriptEvent.Refs) != 1 || result.TranscriptEvent.Refs[0].Rel != "derived_from" || result.TranscriptEvent.Refs[0].ID != sourceID {
			t.Fatalf("source ref: %#v", result.TranscriptEvent.Refs)
		}
	}
	if transcriber.calls != 1 {
		t.Fatalf("idempotency called transcriber %d times", transcriber.calls)
	}
	plugins, err := f.service.ListPlugins(ctx, f.project.ID)
	if err != nil || len(plugins) != 1 || plugins[0].Manifest.ID != "audio-transcribe" {
		t.Fatalf("builtin plugin: %#v %v", plugins, err)
	}
}
