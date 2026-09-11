package api

import (
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"event-driven-context/internal/automation"
	"event-driven-context/internal/core"
)

const (
	attemptIDHeader = "X-EDC-Attempt-ID"
	fencingHeader   = "X-EDC-Fencing-Token"
)

// RegisterAutomationHandlers wires the P1 fixed-skill user and runner APIs.
// The caller owns mux integration so automation can be landed independently of
// the existing server assembly.
func RegisterAutomationHandlers(mux *http.ServeMux, store *core.Store, coordinator *automation.Coordinator) {
	user := func(handler http.Handler) http.Handler { return authenticated(store, handler) }
	mux.Handle("POST /v1/automation/runners", user(jsonEndpoint(201, coordinator.RegisterRunner)))
	mux.Handle("GET /v1/automation/runners", user(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items, err := coordinator.ListRunners(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, map[string]any{"runners": items})
	})))
	mux.Handle("POST /v1/automation/runners/{id}/actions", user(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in automation.RunnerActionInput
		if err := decode(r, &in); err != nil {
			fail(w, err)
			return
		}
		out, err := coordinator.RevokeRunner(r.Context(), r.PathValue("id"), in)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, out)
	})))
	mux.Handle("POST /v1/automation/installations", user(jsonEndpoint(201, coordinator.CreateInstallation)))
	mux.Handle("GET /v1/automation/installations", user(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items, err := coordinator.ListInstallations(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, map[string]any{"installations": items})
	})))
	mux.Handle("POST /v1/automation/installations/{id}/revisions", user(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in automation.ReviseInstallationInput
		if err := decode(r, &in); err != nil {
			fail(w, err)
			return
		}
		out, err := coordinator.ReviseInstallation(r.Context(), r.PathValue("id"), in)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, out)
	})))
	mux.Handle("POST /v1/automation/installations/{id}/actions", user(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in automation.InstallationActionInput
		if err := decode(r, &in); err != nil {
			fail(w, err)
			return
		}
		out, err := coordinator.SetInstallation(r.Context(), r.PathValue("id"), in)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, out)
	})))
	mux.Handle("GET /v1/automation/runs", user(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items, err := coordinator.ListRuns(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		filtered := make([]automation.Run, 0, len(items))
		for _, item := range items {
			if value := r.URL.Query().Get("project_id"); value != "" && item.ProjectID != value {
				continue
			}
			if value := r.URL.Query().Get("installation_id"); value != "" && item.InstallationID != value {
				continue
			}
			if value := r.URL.Query().Get("status"); value != "" && item.Status != value {
				continue
			}
			filtered = append(filtered, item)
		}
		respond(w, http.StatusOK, automation.Runs{Runs: filtered})
	})))
	mux.Handle("GET /v1/automation/runs/{id}", user(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out, err := coordinator.GetRun(r.Context(), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, out)
	})))
	mux.Handle("POST /v1/automation/runs", user(jsonEndpoint(201, coordinator.RequestRun)))
	mux.Handle("GET /v1/inbox", user(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out, err := coordinator.ListInbox(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, out)
	})))
	mux.Handle("POST /v1/inbox/{id}/read", user(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out, err := coordinator.ReadInbox(r.Context(), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, out)
	})))

	mux.HandleFunc("POST /v1/runner/dispatch", func(w http.ResponseWriter, r *http.Request) {
		claim, err := coordinator.Dispatch(r.Context(), bearer(r))
		if err != nil {
			runnerFail(w, err)
			return
		}
		if claim == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		respond(w, http.StatusOK, claim)
	})
	mux.HandleFunc("POST /v1/runner/runs/{id}/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		credential, err := attemptCredential(r)
		if err != nil {
			runnerFail(w, err)
			return
		}
		out, err := coordinator.Heartbeat(r.Context(), bearer(r), r.PathValue("id"), credential)
		if err != nil {
			runnerFail(w, err)
			return
		}
		respond(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /v1/runner/runs/{id}/input-files/{file_id}/content", func(w http.ResponseWriter, r *http.Request) {
		credential, err := attemptCredential(r)
		if err != nil {
			runnerFail(w, err)
			return
		}
		info, file, err := coordinator.OpenInputFile(r.Context(), bearer(r), r.PathValue("id"), r.PathValue("file_id"), credential)
		if err != nil {
			runnerFail(w, err)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", info.MediaType)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": info.Filename}))
		w.Header().Set("Content-Length", strconv.Itoa(info.SizeBytes))
		w.Header().Set("X-Content-SHA256", info.SHA256)
		w.WriteHeader(http.StatusOK)
		if _, err = io.Copy(w, file); err != nil {
			slog.Debug("runner input response write failed", "file_id", info.ID, "error", err)
		}
	})
	mux.HandleFunc("POST /v1/runner/runs/{id}/submit", func(w http.ResponseWriter, r *http.Request) {
		credential, err := attemptCredential(r)
		if err != nil {
			runnerFail(w, err)
			return
		}
		var in automation.SubmitInput
		if err = decode(r, &in); err != nil {
			runnerFail(w, err)
			return
		}
		out, err := coordinator.Submit(r.Context(), bearer(r), r.PathValue("id"), credential, in)
		if err != nil {
			runnerFail(w, err)
			return
		}
		respond(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /v1/runner/runs/{id}/fail", func(w http.ResponseWriter, r *http.Request) {
		credential, err := attemptCredential(r)
		if err != nil {
			runnerFail(w, err)
			return
		}
		var in automation.FailureInput
		if err = decode(r, &in); err != nil {
			runnerFail(w, err)
			return
		}
		out, err := coordinator.Fail(r.Context(), bearer(r), r.PathValue("id"), credential, in)
		if err != nil {
			runnerFail(w, err)
			return
		}
		respond(w, http.StatusOK, out)
	})
}

func attemptCredential(r *http.Request) (automation.AttemptCredential, error) {
	ids := r.Header.Values(attemptIDHeader)
	fences := r.Header.Values(fencingHeader)
	if len(ids) != 1 || len(fences) != 1 || !validAttemptHeader(strings.TrimSpace(ids[0])) {
		return automation.AttemptCredential{}, core.Invalid("runner attempt headers are required exactly once")
	}
	fence, err := strconv.ParseUint(strings.TrimSpace(fences[0]), 10, 64)
	if err != nil || fence == 0 {
		return automation.AttemptCredential{}, core.Invalid("X-EDC-Fencing-Token must be a positive base-10 integer")
	}
	return automation.AttemptCredential{AttemptID: strings.TrimSpace(ids[0]), FencingToken: fence}, nil
}

func validAttemptHeader(value string) bool {
	if len(value) != 36 || !strings.HasPrefix(value, "att_") {
		return false
	}
	for _, character := range value[4:] {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func runnerFail(w http.ResponseWriter, err error) {
	var typed *core.Error
	if errors.As(err, &typed) {
		switch typed.Code {
		case "stale_attempt", "lease_expired", "installation_paused", "capability_unavailable":
			respond(w, http.StatusConflict, map[string]any{"error": typed})
			return
		}
	}
	fail(w, err)
}
