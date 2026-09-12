package notescheduler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"event-driven-context/internal/core"
	"event-driven-context/internal/noteindexer"
	"event-driven-context/internal/v2"
)

var manifestJSON = []byte(`{
  "id": "notes-indexer",
  "version": "0.1.0",
  "name": "Notes indexer",
  "description": "Organizes project events into source-linked daily, persons, topics, and goals notes using the built-in bounded agent runtime.",
  "skills": [],
  "state": [],
  "session_context": [],
  "processor": {
    "runs_in": "host",
    "entry": { "type": "agent", "protocol": "edc-notes-v1" },
    "input": { "types": ["note", "derived", "log"] },
    "limits": { "timeout_seconds": 300 }
  },
  "config": {},
  "permissions": {
    "read_events": ["note", "derived", "log"],
    "write_events": [],
    "write_state": [],
    "organize_notes": true
  }
}
`)

// Run uses one slot and round-robin discovery: one batch per project per sweep.
// Checkpoints survive restarts; failed projects back off without starving others.
func Run(ctx context.Context, store *core.Store, service *v2.Service, codex string) {
	runLoop(ctx, store, service, codex, 15*time.Second, noteindexer.Run)
}

func runLoop(ctx context.Context, store *core.Store, service *v2.Service, codex string, interval time.Duration, execute func(context.Context, noteindexer.Client, noteindexer.Options) (noteindexer.Result, error)) {
	var manifest v2.Manifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		panic(err)
	}
	type retry struct {
		until time.Time
		delay time.Duration
	}
	retries := map[string]retry{}
	for ctx.Err() == nil {
		targets, err := store.NotesTargets(ctx)
		if err != nil {
			slog.Error("notes discovery failed", "error", err)
		}
		for _, target := range targets {
			if ctx.Err() != nil {
				return
			}
			r := retries[target.ProjectID]
			if time.Now().Before(r.until) {
				continue
			}
			runCtx := core.WithUser(ctx, target.OwnerID)
			// An explicit pause/removal opts a project out of automatic indexing.
			installations, e := service.ListPlugins(runCtx, target.ProjectID)
			if e != nil {
				continue
			}
			skip := false
			for _, in := range installations {
				if in.PluginID == manifest.ID && in.Status != "active" {
					skip = true
				}
			}
			if skip {
				continue
			}
			principal, _, e := service.EnsureAutomaticPlugin(runCtx, target.ProjectID, manifest)
			var result noteindexer.Result
			if e == nil {
				result, e = execute(ctx, client{service, principal}, noteindexer.Options{ProjectID: target.ProjectID, CodexPath: codex})
			}
			if ctx.Err() != nil {
				return
			}
			if e != nil {
				var ve *v2.Error
				if errors.As(e, &ve) && (ve.Code == "plugin_paused" || ve.Code == "plugin_removed") {
					continue
				}
				if r.delay == 0 {
					r.delay = 15 * time.Second
				} else {
					r.delay *= 2
				}
				if r.delay > 5*time.Minute {
					r.delay = 5 * time.Minute
				}
				r.until = time.Now().Add(r.delay)
				retries[target.ProjectID] = r
				slog.Error("automatic notes failed", "project_id", target.ProjectID, "retry_after", r.delay, "error", e)
			} else {
				delete(retries, target.ProjectID)
				if !result.Noop {
					slog.Info("automatic notes published", "project_id", target.ProjectID, "processed_events", result.ProcessedEvents, "through_sequence", result.ThroughSequence, "notes_revision", result.Revision)
				}
			}
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
