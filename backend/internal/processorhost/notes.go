package processorhost

import (
	"context"
	"fmt"
	"time"

	"event-driven-context/internal/noteindexer"
	"event-driven-context/internal/v2"
	"event-driven-context/internal/v2client"
)

const notesPluginID = "notes-indexer"

func runNotes(ctx context.Context, client *v2client.Client, opts Options, spec processorSpec, installation v2.Installation, result Result) (Result, error) {
	if spec.Entry.Type != "agent" || spec.Entry.Protocol != "edc-notes-v1" || spec.Entry.Skill != "" || len(spec.Entry.Command) != 0 || spec.Schedule.Time != "" {
		return result, fmt.Errorf("notes-indexer requires the built-in agent protocol edc-notes-v1")
	}
	var installed processorSpec
	if err := strictJSON(installation.Manifest.Processor, &installed); err != nil || installed.Entry.Type != "agent" || installed.Entry.Protocol != "edc-notes-v1" {
		return result, fmt.Errorf("installed notes-indexer does not declare edc-notes-v1")
	}
	if !installation.Permissions.OrganizeNotes {
		return result, fmt.Errorf("installed notes-indexer lacks organize_notes permission")
	}
	if opts.Command != "" {
		return result, fmt.Errorf("notes-indexer does not support --command")
	}
	if opts.Timeout == 0 {
		opts.Timeout = 5 * time.Minute
		if spec.Limits.TimeoutSeconds > 0 {
			opts.Timeout = time.Duration(spec.Limits.TimeoutSeconds) * time.Second
		}
	}
	if opts.Timeout < 0 || opts.Timeout > 10*time.Minute {
		return result, fmt.Errorf("notes-indexer timeout must not exceed 10m")
	}
	out, err := noteindexer.Run(ctx, client, noteindexer.Options{ProjectID: opts.ProjectID, CodexPath: opts.AgentCommand, Timeout: opts.Timeout})
	result.Noop, result.Reason = out.Noop, out.Reason
	result.ProcessedEvents, result.ThroughSequence = out.ProcessedEvents, out.ThroughSequence
	result.NotesRevision = out.Revision
	return result, err
}

func notesWatchBackoff(interval, previous time.Duration, failed bool) time.Duration {
	if !failed {
		return 0
	}
	delay := interval
	if previous > 0 {
		delay = previous * 2
	}
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	return delay
}
