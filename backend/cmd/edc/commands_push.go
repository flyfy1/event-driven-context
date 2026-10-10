package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"event-driven-context/internal/capture"
	"event-driven-context/internal/v2"
	"event-driven-context/internal/v2client"
	"github.com/google/uuid"
)

func (a *app) push(args []string) error {
	f := a.flags("push")
	projectID := f.String("project", "", "project ID")
	eventType := f.String("type", "note", "note or log")
	filePath := f.String("file", "", "file path or -")
	mediaType := f.String("media-type", "", "explicit MIME type")
	jsonInput := f.Bool("json", false, "read one Event JSON from stdin")
	jsonlInput := f.Bool("jsonl", false, "read Event JSONL from stdin")
	metadataJSON := f.String("metadata", "{}", "metadata JSON object")
	sourceJSON := f.String("source-json", `{"channel":"cli"}`, "source JSON object")
	occurredAt := f.String("occurred-at", "", "RFC3339 occurrence time")
	var metaPairs, sourcePairs stringList
	f.Var(&metaPairs, "meta", "metadata KEY=JSON or KEY=text, repeatable")
	f.Var(&sourcePairs, "source", "source KEY=VALUE, repeatable")
	if err := f.Parse(args); err != nil {
		return err
	}
	if err := a.requiredProject(projectID); err != nil {
		return err
	}
	modes := 0
	if *filePath != "" {
		modes++
	}
	if *jsonInput {
		modes++
	}
	if *jsonlInput {
		modes++
	}
	if modes > 1 {
		return fmt.Errorf("use only one of --file, --json or --jsonl")
	}
	if modes > 0 && f.NArg() != 0 {
		return fmt.Errorf("text cannot be combined with file or JSON input")
	}
	var events []v2.EventInput
	var err error
	switch {
	case *jsonInput:
		var event v2.EventInput
		err = decodeInputJSON(a.io.in, v2.MaxTextBytes+v2.MaxMetadata, &event)
		events = []v2.EventInput{event}
	case *jsonlInput:
		events, err = readJSONLEvents(a.io.in)
	case *filePath != "":
		if *mediaType == "" {
			return fmt.Errorf("--file requires --media-type")
		}
		events, err = a.fileEvent(*projectID, *filePath, *mediaType, *eventType, *metadataJSON, metaPairs, *sourceJSON, sourcePairs, *occurredAt)
	default:
		if f.NArg() > 1 {
			return fmt.Errorf("push accepts at most one text argument")
		}
		var text string
		if f.NArg() == 1 {
			text = f.Arg(0)
		} else {
			var b []byte
			b, err = io.ReadAll(io.LimitReader(a.io.in, v2.MaxTextBytes+1))
			text = string(b)
			if err == nil && len(b) > v2.MaxTextBytes {
				err = fmt.Errorf("text exceeds 1 MiB")
			}
		}
		if err == nil {
			var metadata, source map[string]json.RawMessage
			metadata, err = objectWithPairs(*metadataJSON, metaPairs, true)
			if err == nil {
				source, err = objectWithPairs(*sourceJSON, sourcePairs, false)
			}
			if err == nil {
				events = []v2.EventInput{{Type: *eventType, Content: v2.EventContent{Kind: "text", Text: text}, Metadata: metadata, Source: source, OccurredAt: *occurredAt}}
			}
		}
	}
	if err != nil {
		return err
	}
	for i := range events {
		if events[i].ID == "" {
			id, idErr := uuid.NewV7()
			if idErr != nil {
				return idErr
			}
			events[i].ID = id.String()
		}
		parsed, err := uuid.Parse(strings.TrimSpace(events[i].ID))
		if err != nil {
			return fmt.Errorf("event %d id must be a UUID", i+1)
		}
		events[i].ID = parsed.String()
	}
	var queueManager *capture.Manager
	var queueBinding capture.Binding
	if manager, binding, bindingErr := a.currentBinding(a.ctx); bindingErr == nil && binding.ProjectID == *projectID {
		queueManager, queueBinding = manager, binding
		// A new push is also an opportunity to deliver older durable items. A
		// pending conflict must not prevent this new batch from being attempted.
		_, _ = queueManager.Flush(a.ctx, queueBinding)
	}
	out, err := a.client.RecordEvents(a.ctx, *projectID, v2.RecordEventsInput{Events: events})
	if err != nil {
		queued := 0
		if queueManager != nil {
			unconfirmed := events
			var batchErr *v2client.BatchError
			if errors.As(err, &batchErr) && len(out.Results) == len(events) {
				unconfirmed = make([]v2.EventInput, 0, len(events))
				for i, item := range out.Results {
					if item.Status != "created" && item.Status != "duplicate" {
						unconfirmed = append(unconfirmed, events[i])
					}
				}
			}
			if len(unconfirmed) > 0 {
				var queueErr error
				queued, queueErr = queueManager.Enqueue(queueBinding, unconfirmed)
				if queueErr != nil {
					return errors.Join(err, fmt.Errorf("persist failed events in outbox: %w", queueErr))
				}
			}
		}
		var batchErr *v2client.BatchError
		if errors.As(err, &batchErr) {
			if printErr := a.json(out); printErr != nil {
				return printErr
			}
		}
		if queued > 0 {
			_, _ = fmt.Fprintf(a.io.err, "queued %d unconfirmed event(s) in the local outbox\n", queued)
		}
		return err
	}
	return a.json(out)
}

func readJSONLEvents(r io.Reader) ([]v2.EventInput, error) {
	s := bufio.NewScanner(io.LimitReader(r, 2<<20+1))
	s.Buffer(make([]byte, 64<<10), 2<<20)
	events := make([]v2.EventInput, 0)
	line := 0
	for s.Scan() {
		line++
		if len(bytes.TrimSpace(s.Bytes())) == 0 {
			continue
		}
		if len(events) == v2.MaxBatchEvents {
			return nil, fmt.Errorf("JSONL exceeds %d events", v2.MaxBatchEvents)
		}
		var event v2.EventInput
		if err := decodeInputJSON(bytes.NewReader(s.Bytes()), int64(len(s.Bytes())), &event); err != nil {
			return nil, fmt.Errorf("JSONL line %d: %w", line, err)
		}
		events = append(events, event)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("JSONL must contain at least one event")
	}
	return events, nil
}

func (a *app) fileEvent(projectID, path, mediaType, eventType, metadataRaw string, metaPairs []string, sourceRaw string, sourcePairs []string, occurredAt string) ([]v2.EventInput, error) {
	f, cleanup, name, size, hash, err := openInputFile(a.io.in, path)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	info, err := a.client.PutFile(a.ctx, projectID, v2.FileUpload{Filename: name, MediaType: mediaType, SHA256: hash, SizeBytes: size, Reader: f})
	if err != nil {
		return nil, err
	}
	metadata, err := objectWithPairs(metadataRaw, metaPairs, true)
	if err != nil {
		return nil, err
	}
	source, err := objectWithPairs(sourceRaw, sourcePairs, false)
	if err != nil {
		return nil, err
	}
	return []v2.EventInput{{Type: eventType, Content: v2.EventContent{Kind: "file", FileID: info.ID}, Metadata: metadata, Source: source, OccurredAt: occurredAt}}, nil
}
