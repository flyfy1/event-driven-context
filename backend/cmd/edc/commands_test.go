package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"event-driven-context/internal/mcpserver"
	"event-driven-context/internal/v2"
	"event-driven-context/internal/v2client"
)

func TestFileGetDoesNotPublishUnverifiedDownload(t *testing.T) {
	claimed := sha256.Sum256([]byte("different bytes"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-EDC-File-ID", "file_one")
		w.Header().Set("X-EDC-SHA256", hex.EncodeToString(claimed[:]))
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "6")
		_, _ = w.Write([]byte("actual"))
	}))
	t.Cleanup(server.Close)
	client, err := v2client.New(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "download.txt")
	var stdout, stderr bytes.Buffer
	a := &app{ctx: context.Background(), io: streams{in: strings.NewReader(""), out: &stdout, err: &stderr}, client: client, token: "test-token"}
	err = a.file([]string{"get", "--project", "prj_one", "-o", target, "file_one"})
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Lstat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unverified target exists: %v", statErr)
	}
	if leftovers, globErr := filepath.Glob(filepath.Join(dir, ".edc-download-*")); globErr != nil || len(leftovers) != 0 {
		t.Fatalf("temporary downloads = %v, error = %v", leftovers, globErr)
	}
}

func TestReadJSONLEventsRejectsTrailingJSON(t *testing.T) {
	_, err := readJSONLEvents(strings.NewReader(`{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"} {"id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}` + "\n"))
	if err == nil || !strings.Contains(err.Error(), "trailing data") {
		t.Fatalf("error = %v", err)
	}
}

func TestRemoteErrorPreservesPublicCode(t *testing.T) {
	err := remoteError(&v2client.APIError{Status: http.StatusConflict, Code: "state_version_mismatch", Message: "revision changed"})
	var v2Err *v2.Error
	if !errors.As(err, &v2Err) || v2Err.Code != "state_version_mismatch" || v2Err.Message != "revision changed" {
		t.Fatalf("error = %#v", err)
	}
}

func TestRemoteGetStateReconcilesLatestSequence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/prj_one/state":
			_ = json.NewEncoder(w).Encode(v2.StatesResult{States: []v2.State{}, LatestSequence: 10})
		case "/v1/projects/prj_one/state/project-brief/current":
			_ = json.NewEncoder(w).Encode(v2.State{ProjectID: "prj_one", Key: "project-brief/current", Version: 1, BasedOnSequence: 11, Lag: 0})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client, err := v2client.New(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	version := int64(1)
	out, err := (remoteMCPBackend{client: client}).GetState(context.Background(), mcpserver.V2GetStateInput{ProjectID: "prj_one", Keys: []string{"project-brief/current"}, Version: &version})
	if err != nil || out.LatestSequence != 11 || len(out.States) != 1 || out.States[0].Lag != 0 {
		t.Fatalf("result = %#v, error = %v", out, err)
	}
}
