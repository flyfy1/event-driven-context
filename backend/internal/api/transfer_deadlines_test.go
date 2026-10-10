package api

import (
	"net/http/httptest"
	"testing"
	"time"

	"event-driven-context/internal/core"
	"event-driven-context/internal/v2"
)

type deadlineWriter struct {
	*httptest.ResponseRecorder
	readDeadline  time.Time
	writeDeadline time.Time
}

func (w *deadlineWriter) SetReadDeadline(deadline time.Time) error {
	w.readDeadline = deadline
	return nil
}

func (w *deadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.writeDeadline = deadline
	return nil
}

func TestExtendTransferDeadlines(t *testing.T) {
	for _, tt := range []struct {
		name     string
		maxBytes int64
		upload   bool
		budget   time.Duration
	}{
		{"media upload", core.MaxMediaRequestBytes, true, 516 * time.Second},
		{"file upload", v2.MaxFileBytes + v2MultipartOverhead, true, 996 * time.Second},
		{"hub import", core.HubImportRequestMaxBytes, true, 404 * time.Second},
		{"media and runner download", core.MaxMediaBytes, false, 500 * time.Second},
		{"file catalog download", v2.MaxFileBytes, false, 980 * time.Second},
		{"round up partial second", 1, true, 181 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			writer := &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}
			// Exercise ResponseController through the production logging wrapper.
			logged := &statusWriter{ResponseWriter: writer}
			before := time.Now()
			extendTransferDeadlines(logged, tt.maxBytes, tt.upload)
			after := time.Now()
			assertDeadline := func(name string, got time.Time, budget time.Duration) {
				t.Helper()
				if got.Before(before.Add(budget)) || got.After(after.Add(budget)) {
					t.Fatalf("%s deadline %v outside expected budget %v", name, got, budget)
				}
			}
			writeBudget := tt.budget
			if tt.upload {
				assertDeadline("read", writer.readDeadline, tt.budget)
				writeBudget += 10 * time.Second
				if writer.writeDeadline.Sub(writer.readDeadline) != 10*time.Second {
					t.Fatal("upload response must outlast the read deadline by ten seconds")
				}
			} else if !writer.readDeadline.IsZero() {
				t.Fatal("downloads must preserve the server read deadline")
			}
			assertDeadline("write", writer.writeDeadline, writeBudget)
		})
	}
}

func TestExtendTransferDeadlinesUnsupported(t *testing.T) {
	// Recorders do not support deadlines; handlers must still work with them.
	w := httptest.NewRecorder()
	extendTransferDeadlines(w, core.MaxMediaRequestBytes, true)
	if w.Body.Len() != 0 {
		t.Fatal("unsupported deadlines must not write a response")
	}
}
