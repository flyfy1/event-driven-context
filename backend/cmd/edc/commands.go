package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"event-driven-context/internal/capture"
	"event-driven-context/internal/core"
	"event-driven-context/internal/localcollection"
	"event-driven-context/internal/v2"
	"event-driven-context/internal/v2client"
	"github.com/google/uuid"
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func (a *app) project(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("project requires create, list, members or add-member")
	}
	command := args[0]
	f := a.flags("project " + command)
	switch command {
	case "list":
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		out, err := a.client.ListProjects(a.ctx)
		return a.result(out, err)
	case "create":
		name := f.String("name", "", "project name")
		description := f.String("description", "", "description")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if *name == "" {
			return fmt.Errorf("--name is required")
		}
		out, err := a.client.CreateProject(a.ctx, core.ProjectInput{Name: *name, Description: *description})
		return a.result(out, err)
	case "members":
		projectID := f.String("project", "", "project ID")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		out, err := a.client.ListMembers(a.ctx, *projectID)
		return a.result(out, err)
	case "add-member":
		projectID := f.String("project", "", "project ID")
		username := f.String("username", "", "registered username")
		email := f.String("email", "", "registered email")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		if (*username == "") == (*email == "") {
			return fmt.Errorf("exactly one of --username or --email is required")
		}
		if *email != "" {
			out, err := a.client.AddMemberByEmail(a.ctx, *projectID, *email)
			return a.result(out, err)
		}
		out, err := a.client.AddMember(a.ctx, *projectID, *username)
		return a.result(out, err)
	default:
		return fmt.Errorf("unknown project command %q", command)
	}
}

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

func decodeInputJSON(r io.Reader, limit int64, out any) error {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return err
	}
	if int64(len(b)) > limit {
		return fmt.Errorf("input exceeds %d bytes", limit)
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return fmt.Errorf("invalid JSON input: %w", err)
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("invalid JSON input: trailing data")
	}
	return nil
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

func objectWithPairs(raw string, pairs []string, jsonValues bool) (map[string]json.RawMessage, error) {
	var out map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return nil, fmt.Errorf("value must be a JSON object")
	}
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("expected KEY=VALUE, got %q", pair)
		}
		encoded, err := json.Marshal(value)
		if jsonValues && json.Valid([]byte(value)) {
			encoded = []byte(value)
			err = nil
		}
		if err != nil {
			return nil, err
		}
		out[key] = encoded
	}
	return out, nil
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

func openInputFile(stdin io.Reader, path string) (*os.File, func(), string, int64, string, error) {
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, "", 0, "", err
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, nil, "", 0, "", err
		}
		if !st.Mode().IsRegular() || st.Size() > v2.MaxFileBytes {
			f.Close()
			return nil, nil, "", 0, "", fmt.Errorf("file must be regular and at most %d bytes", v2.MaxFileBytes)
		}
		h := sha256.New()
		if _, err = io.Copy(h, f); err != nil {
			f.Close()
			return nil, nil, "", 0, "", err
		}
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			f.Close()
			return nil, nil, "", 0, "", err
		}
		return f, func() { _ = f.Close() }, filepath.Base(path), st.Size(), hex.EncodeToString(h.Sum(nil)), nil
	}
	tmp, err := os.CreateTemp("", "edc-push-*")
	if err != nil {
		return nil, nil, "", 0, "", err
	}
	cleanup := func() { name := tmp.Name(); _ = tmp.Close(); _ = os.Remove(name) }
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(stdin, v2.MaxFileBytes+1))
	if err != nil || n > v2.MaxFileBytes {
		cleanup()
		if err != nil {
			return nil, nil, "", 0, "", err
		}
		return nil, nil, "", 0, "", fmt.Errorf("file exceeds %d bytes", v2.MaxFileBytes)
	}
	if _, err = tmp.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, nil, "", 0, "", err
	}
	return tmp, cleanup, "stdin", n, hex.EncodeToString(h.Sum(nil)), nil
}

func (a *app) query(args []string) error {
	f := a.flags("query")
	projectID := f.String("project", "", "project ID")
	types := f.String("types", "", "comma-separated types")
	metadata := f.String("metadata", "{}", "metadata filter JSON")
	source := f.String("source", "{}", "source filter JSON")
	refsTo := f.String("refs-to", "", "referenced event UUID")
	after := f.Int64("after", 0, "after sequence")
	from := f.String("from", "", "inclusive RFC3339")
	to := f.String("to", "", "exclusive RFC3339")
	timeField := f.String("time-field", "recorded_at", "recorded_at or occurred_at")
	limit := f.Int("limit", 50, "page size")
	cursor := f.String("cursor", "", "snapshot cursor")
	if err := parse(f, args); err != nil {
		return err
	}
	if err := a.requiredProject(projectID); err != nil {
		return err
	}
	meta, err := objectWithPairs(*metadata, nil, true)
	if err != nil {
		return err
	}
	src, err := objectWithPairs(*source, nil, true)
	if err != nil {
		return err
	}
	var typeList []string
	if *types != "" {
		typeList = strings.Split(*types, ",")
	}
	out, err := a.client.QueryEvents(a.ctx, *projectID, v2.QueryEventsInput{Types: typeList, Metadata: meta, Source: src, RefsTo: *refsTo, AfterSequence: *after, From: *from, To: *to, TimeField: *timeField, Limit: *limit, Cursor: *cursor})
	return a.result(out, err)
}

func (a *app) get(args []string) error {
	f := a.flags("get")
	projectID := f.String("project", "", "project ID")
	if err := f.Parse(args); err != nil {
		return err
	}
	if err := a.requiredProject(projectID); err != nil {
		return err
	}
	if f.NArg() != 1 {
		return fmt.Errorf("get requires one EVENT_ID")
	}
	out, err := a.client.GetEvent(a.ctx, *projectID, f.Arg(0))
	return a.result(out, err)
}

func (a *app) metadata(args []string) error {
	f := a.flags("metadata")
	projectID := f.String("project", "", "project ID")
	key := f.String("key", "", "metadata key")
	if err := parse(f, args); err != nil {
		return err
	}
	if err := a.requiredProject(projectID); err != nil {
		return err
	}
	out, err := a.client.ListMetadata(a.ctx, *projectID, *key)
	return a.result(out, err)
}

func (a *app) file(args []string) error {
	if len(args) == 0 || args[0] != "get" {
		return fmt.Errorf("file requires get")
	}
	f := a.flags("file get")
	projectID := f.String("project", "", "project ID")
	output := f.String("o", "-", "output path or -")
	cache := f.String("cache", "", "collection directory for verified on-demand caching")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if err := a.requiredProject(projectID); err != nil {
		return err
	}
	if f.NArg() != 1 {
		return fmt.Errorf("file get requires one FILE_ID")
	}
	if *cache != "" {
		hasOutput := false
		f.Visit(func(value *flag.Flag) {
			if value.Name == "o" {
				hasOutput = true
			}
		})
		if hasOutput {
			return fmt.Errorf("--cache and -o are mutually exclusive")
		}
		collection, err := localcollection.Open(*cache, a.client.BaseURL, *projectID)
		if err != nil {
			return err
		}
		defer collection.Close()
		result, err := collection.GetFile(a.ctx, a.client, f.Arg(0))
		return a.result(result, err)
	}
	if *output == "-" {
		tmp, err := os.CreateTemp("", "edc-download-*")
		if err != nil {
			return err
		}
		name := tmp.Name()
		defer os.Remove(name)
		_, err = a.client.GetFile(a.ctx, *projectID, f.Arg(0), tmp)
		if closeErr := tmp.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		verified, err := os.Open(name)
		if err != nil {
			return err
		}
		defer verified.Close()
		_, err = io.Copy(a.io.out, verified)
		return err
	}
	dir := filepath.Dir(*output)
	publish, err := os.CreateTemp(dir, ".edc-download-*")
	if err != nil {
		return err
	}
	publishName := publish.Name()
	defer os.Remove(publishName)
	info, err := a.client.GetFile(a.ctx, *projectID, f.Arg(0), publish)
	if err == nil {
		err = publish.Chmod(0600)
	}
	if err == nil {
		err = publish.Sync()
	}
	if closeErr := publish.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// A hard link publishes the verified inode atomically and cannot replace an
	// existing path. The temporary file is in the destination directory, so the
	// operation stays on one filesystem.
	if err = os.Link(publishName, *output); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("output already exists: %s", *output)
		}
		return fmt.Errorf("publish download: %w", err)
	}
	return a.json(info)
}

func (a *app) state(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("state requires list, get or put")
	}
	command := args[0]
	f := a.flags("state " + command)
	projectID := f.String("project", "", "project ID")
	switch command {
	case "list":
		prefix := f.String("prefix", "", "key prefix")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		out, err := a.client.ListState(a.ctx, *projectID, *prefix)
		return a.result(out, err)
	case "get":
		versionRaw := f.String("version", "", "historical version")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		if f.NArg() != 1 {
			return fmt.Errorf("state get requires one KEY")
		}
		var version *int64
		if *versionRaw != "" {
			n, err := strconv.ParseInt(*versionRaw, 10, 64)
			if err != nil || n < 1 {
				return fmt.Errorf("--version must be positive")
			}
			version = &n
		}
		out, err := a.client.GetState(a.ctx, *projectID, f.Arg(0), version)
		return a.result(out, err)
	case "put":
		contentPath := f.String("content", "", "content file or -")
		format := f.String("format", "markdown", "markdown or text")
		dataRaw := f.String("data", "", "optional JSON data")
		based := f.Int64("based-on", 0, "based-on sequence")
		expectedRaw := f.String("expected-version", "", "expected current version")
		asPlugin := f.String("as-plugin", "", "installed plugin id")
		var refs stringList
		f.Var(&refs, "ref", "source event UUID, repeatable")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		if f.NArg() != 1 {
			return fmt.Errorf("state put requires one KEY")
		}
		if *contentPath == "" {
			return fmt.Errorf("--content is required")
		}
		content, err := readPathOrStdin(a.io.in, *contentPath, v2.MaxStateBytes)
		if err != nil {
			return err
		}
		var data json.RawMessage
		if *dataRaw != "" {
			if !json.Valid([]byte(*dataRaw)) {
				return fmt.Errorf("--data must be JSON")
			}
			data = json.RawMessage(*dataRaw)
		}
		var expected *int64
		if *expectedRaw != "" {
			n, e := strconv.ParseInt(*expectedRaw, 10, 64)
			if e != nil || n < 0 {
				return fmt.Errorf("--expected-version must be nonnegative")
			}
			expected = &n
		}
		in := v2.PutStateInput{Key: f.Arg(0), ExpectedVersion: expected, Content: v2.StateContent{Format: *format, Text: string(content)}, Data: data, BasedOnSequence: *based, Refs: refs, AsPluginID: *asPlugin}
		out, err := a.client.PutState(a.ctx, *projectID, in)
		return a.result(out, err)
	default:
		return fmt.Errorf("unknown state command %q", command)
	}
}

func readPathOrStdin(stdin io.Reader, path string, limit int) ([]byte, error) {
	var r io.Reader = stdin
	var f *os.File
	var err error
	if path != "-" {
		f, err = os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > limit {
		return nil, fmt.Errorf("input exceeds %d bytes", limit)
	}
	return b, nil
}

func (a *app) plugin(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("plugin requires install, list, config, pause, resume, rerun or remove")
	}
	command := args[0]
	f := a.flags("plugin " + command)
	projectID := f.String("project", "", "project ID")
	switch command {
	case "list":
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		out, err := a.client.ListPlugins(a.ctx, *projectID)
		return a.result(struct {
			Plugins []v2.Installation `json:"plugins"`
		}{out}, err)
	case "install":
		manifestPath := f.String("manifest", "", "manifest JSON path or -")
		configRaw := f.String("config", "", "optional config JSON")
		tokenFile := f.String("token-file", "", "new private file for the one-time plugin token")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		if *manifestPath == "" {
			return fmt.Errorf("--manifest is required")
		}
		raw, err := readPathOrStdin(a.io.in, *manifestPath, 1<<20)
		if err != nil {
			return err
		}
		var manifest v2.Manifest
		if err = json.Unmarshal(raw, &manifest); err != nil {
			return fmt.Errorf("manifest must be JSON: %w", err)
		}
		var config json.RawMessage
		if *configRaw != "" {
			if !json.Valid([]byte(*configRaw)) {
				return fmt.Errorf("--config must be JSON")
			}
			config = json.RawMessage(*configRaw)
		}
		out, err := a.client.InstallPlugin(a.ctx, *projectID, v2.InstallPluginInput{Manifest: manifest, Config: config})
		if err != nil {
			return err
		}
		if *tokenFile != "" {
			if err = writePrivateNewFile(*tokenFile, []byte(out.Token+"\n")); err != nil {
				return fmt.Errorf("save plugin token: %w", err)
			}
		}
		return a.json(struct {
			Installation  v2.Installation `json:"installation"`
			TokenReturned bool            `json:"token_returned"`
			TokenFile     string          `json:"token_file,omitempty"`
		}{out.Installation, out.Token != "", *tokenFile})
	case "config":
		pluginID := f.String("plugin", "", "plugin id")
		revision := f.Int64("expected-revision", 0, "expected config revision")
		configRaw := f.String("config", "", "config JSON")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		if *pluginID == "" || *revision < 1 || *configRaw == "" {
			return fmt.Errorf("--plugin, positive --expected-revision and --config are required")
		}
		if !json.Valid([]byte(*configRaw)) {
			return fmt.Errorf("--config must be JSON")
		}
		out, err := a.client.PatchPlugin(a.ctx, *projectID, *pluginID, v2client.PatchPluginInput{Action: "config", ExpectedRevision: revision, Config: json.RawMessage(*configRaw)})
		return a.result(out, err)
	case "pause", "resume":
		pluginID := f.String("plugin", "", "plugin id")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		if *pluginID == "" {
			return fmt.Errorf("--plugin is required")
		}
		out, err := a.client.PatchPlugin(a.ctx, *projectID, *pluginID, v2client.PatchPluginInput{Action: command})
		return a.result(out, err)
	case "rerun":
		pluginID := f.String("plugin", "", "plugin id")
		requestID := f.String("request-id", "", "stable request UUID")
		var sources stringList
		f.Var(&sources, "source-event", "source event UUID, repeatable")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		if *pluginID == "" {
			return fmt.Errorf("--plugin is required")
		}
		if *requestID == "" {
			id, e := uuid.NewV7()
			if e != nil {
				return e
			}
			*requestID = id.String()
		}
		if _, e := uuid.Parse(*requestID); e != nil {
			return fmt.Errorf("--request-id must be UUID")
		}
		out, err := a.client.RequestManualRun(a.ctx, *projectID, *pluginID, v2.ManualRunInput{RequestID: *requestID, SourceEventIDs: sources})
		return a.result(out, err)
	case "remove":
		pluginID := f.String("plugin", "", "plugin id")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		if *pluginID == "" {
			return fmt.Errorf("--plugin is required")
		}
		out, err := a.client.RemovePlugin(a.ctx, *projectID, *pluginID)
		return a.result(out, err)
	default:
		return fmt.Errorf("unknown plugin command %q", command)
	}
}

func (a *app) pull(args []string) error {
	f := a.flags("pull")
	projectID := f.String("project", "", "project ID")
	after := f.Int64("after", 0, "after sequence")
	follow := f.Bool("follow", false, "continue watching")
	if err := parse(f, args); err != nil {
		return err
	}
	if err := a.requiredProject(projectID); err != nil {
		return err
	}
	if *follow {
		return fmt.Errorf("pull --follow is not implemented in this V2 CLI build")
	}
	cursor := ""
	encoder := json.NewEncoder(a.io.out)
	for {
		page, err := a.client.QueryEvents(a.ctx, *projectID, v2.QueryEventsInput{AfterSequence: *after, Limit: 100, Cursor: cursor})
		if err != nil {
			return err
		}
		for _, event := range page.Events {
			if err := encoder.Encode(event); err != nil {
				return err
			}
		}
		if page.NextCursor == "" {
			return nil
		}
		cursor = page.NextCursor
	}
}

func (a *app) mcp(args []string) error { return runMCP(a, args) }

var _ flag.Value = (*stringList)(nil)
