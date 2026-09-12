package transcription

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientSendsOpenAITranscriptionRequest(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/audio/transcriptions" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("unexpected request: %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("model") != "gpt-transcribe" || r.FormValue("language") != "zh" || r.FormValue("prompt") != "Integ.Life" {
			t.Fatalf("unexpected fields: %#v", r.MultipartForm.Value)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		body, _ := io.ReadAll(file)
		if header.Filename != "voice.m4a" || string(body) != "audio" {
			t.Fatalf("unexpected file: %q %q", header.Filename, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"会议预算调整为三万元"}`)
	}))
	defer server.Close()

	client, err := NewClient("secret", server.URL+"/v1", "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Transcribe(context.Background(), "voice.m4a", "audio/mp4", 5, strings.NewReader("audio"), Options{Language: "zh", Prompt: "Integ.Life"})
	if err != nil || result.Text != "会议预算调整为三万元" || result.Model != "gpt-transcribe" || calls != 1 {
		t.Fatalf("result=%#v calls=%d err=%v", result, calls, err)
	}
}

func TestClientRejectsEmptyTranscriptAndSafeUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"do not expose this"}}`, http.StatusBadGateway)
	}))
	defer server.Close()
	client, err := NewClient("secret", server.URL, "gpt-transcribe", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Transcribe(context.Background(), "voice.wav", "audio/wav", 1, strings.NewReader("x"), Options{})
	if err == nil || strings.Contains(err.Error(), "do not expose") {
		t.Fatalf("unsafe or missing error: %v", err)
	}
}
