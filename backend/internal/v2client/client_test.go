package v2client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"event-driven-context/internal/v2"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	c, err := New(s.URL, "secret-token")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestStructuredErrorDoesNotExposeToken(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Errorf("authorization = %q", got)
		}
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
			"code": "forbidden", "message": "rejected secret-token",
		}})
	})
	_, err := c.ListProjects(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "forbidden" || apiErr.Status != http.StatusForbidden {
		t.Fatalf("error = %#v", err)
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("token leaked in error: %v", err)
	}
}

func TestRecordEventsReturnsPartialResultAndError(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(v2.RecordEventsResult{Results: []v2.EventWriteResult{
			{ID: "01991dc8-cdf9-7d10-873a-021604814fa6", Status: "created", Sequence: 1},
			{ID: "01991dc8-cdfa-734a-b16c-145bcc1533ac", Status: "conflict", Error: &v2.Error{Code: "conflict", Message: "different input"}},
		}})
	})
	in := v2.RecordEventsInput{Events: []v2.EventInput{
		{ID: "01991dc8-cdf9-7d10-873a-021604814fa6"},
		{ID: "01991dc8-cdfa-734a-b16c-145bcc1533ac"},
	}}
	out, err := c.RecordEvents(context.Background(), "prj_one", in)
	var batchErr *BatchError
	if !errors.As(err, &batchErr) || len(batchErr.Failed) != 1 {
		t.Fatalf("error = %#v", err)
	}
	if len(out.Results) != 2 || out.Results[0].Sequence != 1 {
		t.Fatalf("result = %#v", out)
	}
}

func TestRecordEventsAcceptsServerCanonicalizedUUID(t *testing.T) {
	const upper = "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(v2.RecordEventsResult{Results: []v2.EventWriteResult{{
			ID: strings.ToLower(upper), Status: "created", Sequence: 1,
		}}})
	})
	out, err := c.RecordEvents(context.Background(), "prj_one", v2.RecordEventsInput{Events: []v2.EventInput{{ID: "  " + upper + "\n"}}})
	if err != nil || len(out.Results) != 1 || out.Results[0].Status != "created" {
		t.Fatalf("result = %#v, error = %v", out, err)
	}
}

func TestQueryPreservesLargeJSONInteger(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"events":[{"id":"event","project_id":"prj_one","type":"note","content":{"kind":"text","text":"x"},"metadata":{"count":9007199254740993},"source":{"channel":"cli"},"refs":[],"sequence":1,"recorded_at":"2026-09-12T00:00:00Z","actor":{"type":"user","id":"u"}}],"latest_sequence":1}`)
	})
	out, err := c.QueryEvents(context.Background(), "prj_one", v2.QueryEventsInput{})
	if err != nil || len(out.Events) != 1 || string(out.Events[0].Metadata["count"]) != "9007199254740993" {
		t.Fatalf("count = %s, error = %v", out.Events[0].Metadata["count"], err)
	}
}

func TestQueryRejectsCrossProjectResponse(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(v2.EventsPage{Events: []v2.Event{{ID: "event", ProjectID: "prj_other"}}})
	})
	_, err := c.QueryEvents(context.Background(), "prj_expected", v2.QueryEventsInput{})
	if err == nil || !strings.Contains(err.Error(), "unexpected project") {
		t.Fatalf("error = %v", err)
	}
}

func TestGetFileRejectsHashMismatch(t *testing.T) {
	claimed := sha256.Sum256([]byte("expected"))
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-EDC-File-ID", "file_one")
		w.Header().Set("X-EDC-SHA256", hex.EncodeToString(claimed[:]))
		w.Header().Set("Content-Length", "6")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("actual"))
	})
	var dst bytes.Buffer
	_, err := c.GetFile(context.Background(), "prj_one", "file_one", &dst)
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("error = %v", err)
	}
}

func TestPutFileValidatesReturnedIdentity(t *testing.T) {
	data := []byte("hello")
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		var got bytes.Buffer
		_, _ = got.ReadFrom(f)
		if got.String() != "hello" || r.FormValue("sha256") != hash {
			t.Errorf("upload data=%q hash=%q", got.String(), r.FormValue("sha256"))
		}
		_ = json.NewEncoder(w).Encode(v2.FileInfo{ID: "file_one", ProjectID: "prj_other", SizeBytes: 5, SHA256: hash})
	})
	_, err := c.PutFile(context.Background(), "prj_one", v2.FileUpload{
		Filename: "hello.txt", MediaType: "text/plain", SizeBytes: int64(len(data)), SHA256: hash, Reader: bytes.NewReader(data),
	})
	if err == nil || !strings.Contains(err.Error(), "unexpected project") {
		t.Fatalf("error = %v", err)
	}
}
