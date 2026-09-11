package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type storedEvent struct {
	Sequence       int64  `json:"sequence"`
	Event          Event  `json:"event"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	RequestHash    string `json:"request_hash"`
}

type eventCursor struct {
	After     int64  `json:"after"`
	Snapshot  int64  `json:"snapshot"`
	QueryHash string `json:"query"`
}

var syncDirectory = func(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *Store) eventsDir(projectID string) string {
	return filepath.Join(s.dataDir, "projects", projectID, "events")
}
func (s *Store) filesDir(projectID string) string {
	return filepath.Join(s.dataDir, "projects", projectID, "files")
}
func (s *Store) eventPath(projectID, eventID string) string {
	return filepath.Join(s.eventsDir(projectID), eventID+".json")
}
func (s *Store) filePath(projectID, fileID string) string {
	return filepath.Join(s.filesDir(projectID), fileID)
}

// writeImmutable publishes a fully-written file exactly once. Event manifests are
// the source of truth; raw file bytes without a manifest are unreachable.
func writeImmutable(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Link(tmp.Name(), path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func (s *Store) writeEvent(record storedEvent) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return writeImmutable(s.eventPath(record.Event.ProjectID, record.Event.ID), data)
}

func (s *Store) loadProjectEvents(projectID string) ([]storedEvent, error) {
	entries, err := os.ReadDir(s.eventsDir(projectID))
	if errors.Is(err, fs.ErrNotExist) {
		return []storedEvent{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]storedEvent, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.eventsDir(projectID), entry.Name()))
		if err != nil {
			return nil, err
		}
		var record storedEvent
		if err = json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("read event %s: %w", entry.Name(), err)
		}
		if record.Sequence < 1 || record.Event.ID == "" || record.Event.ProjectID != projectID || entry.Name() != record.Event.ID+".json" {
			return nil, fmt.Errorf("invalid event manifest %s", entry.Name())
		}
		if record.Event.Metadata == nil {
			record.Event.Metadata = map[string]json.RawMessage{}
		}
		if err = normalizeStoredEvent(&record); err != nil {
			return nil, fmt.Errorf("read event %s: %w", entry.Name(), err)
		}
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sequence == out[j].Sequence {
			return out[i].Event.ID < out[j].Event.ID
		}
		return out[i].Sequence < out[j].Sequence
	})
	for i := 1; i < len(out); i++ {
		if out[i-1].Sequence == out[i].Sequence {
			return nil, fmt.Errorf("duplicate event sequence %d in project %s", out[i].Sequence, projectID)
		}
	}
	return out, nil
}

func (s *Store) RecordEvent(ctx context.Context, in RecordInput) (Event, error) {
	if err := s.requireMember(ctx, in.ProjectID); err != nil {
		return Event{}, err
	}
	in, data, err := prepareRecord(in)
	if err != nil {
		return Event{}, err
	}
	requestBytes, err := json.Marshal(in)
	if err != nil {
		return Event{}, err
	}
	requestHash := digest(requestBytes)
	return s.appendEvent(ctx, in, data, requestHash)
}

func (s *Store) RecordMediaEvent(ctx context.Context, in MediaRecordInput) (Event, error) {
	if err := s.requireMember(ctx, in.ProjectID); err != nil {
		return Event{}, err
	}
	record, data, err := prepareMediaRecord(in)
	if err != nil {
		return Event{}, err
	}
	requestBytes, err := json.Marshal(struct {
		ProjectID  string                     `json:"project_id"`
		Filename   string                     `json:"filename"`
		MediaType  string                     `json:"media_type"`
		SizeBytes  int                        `json:"size_bytes"`
		SHA256     string                     `json:"sha256"`
		Metadata   map[string]json.RawMessage `json:"metadata"`
		OccurredAt string                     `json:"occurred_at"`
	}{record.ProjectID, record.Content.File.Filename, record.Content.File.MediaType, len(data), digest(data), record.Metadata, record.OccurredAt})
	if err != nil {
		return Event{}, err
	}
	return s.appendEvent(ctx, record, data, digest(requestBytes))
}

func (s *Store) appendEvent(ctx context.Context, in RecordInput, data []byte, requestHash string) (Event, error) {
	return s.appendEventWithSemantics(ctx, in, data, requestHash, nil, Relations{})
}

// RecordDerived is the internal boundary for coordinator-created context. It is
// intentionally absent from the public Backend interface and HTTP/MCP routes.
func (s *Store) RecordDerived(ctx context.Context, in DerivedRecordInput) (Event, error) {
	if err := s.requireMember(ctx, in.ProjectID); err != nil {
		return Event{}, err
	}
	if in.Provenance.Kind != ProvenanceTranscript && in.Provenance.Kind != ProvenanceSummary && in.Provenance.Kind != ProvenanceSuggestion {
		return Event{}, Invalid("derived provenance.kind must be transcript, summary, or suggestion")
	}
	if err := validateProvenanceLabels(in.Provenance); err != nil {
		return Event{}, err
	}
	var err error
	in.Provenance.SourceEventIDs, err = normalizeEventIDs(in.Provenance.SourceEventIDs)
	if err != nil {
		return Event{}, err
	}
	if len(in.Provenance.SourceEventIDs) == 0 {
		return Event{}, Invalid("derived events require source_event_ids")
	}
	in.Relations.SupersedesEventIDs, err = normalizeEventIDs(in.Relations.SupersedesEventIDs)
	if err != nil {
		return Event{}, err
	}
	if len(in.Provenance.SourceEventIDs)+len(in.Relations.SupersedesEventIDs) > 128 {
		return Event{}, Invalid("derived event max 128 event references")
	}
	record := RecordInput{ProjectID: in.ProjectID, Content: in.Content, Metadata: in.Metadata, OccurredAt: in.OccurredAt, IdempotencyKey: in.IdempotencyKey}
	record, data, err := prepareRecord(record)
	if err != nil {
		return Event{}, err
	}
	requestBytes, err := json.Marshal(struct {
		Domain     string      `json:"domain"`
		Record     RecordInput `json:"record"`
		Provenance Provenance  `json:"provenance"`
		Relations  Relations   `json:"relations"`
	}{"derived", record, in.Provenance, in.Relations})
	if err != nil {
		return Event{}, err
	}
	return s.appendEventWithSemantics(ctx, record, data, digest(requestBytes), &in.Provenance, in.Relations)
}

func (s *Store) appendEventWithSemantics(ctx context.Context, in RecordInput, data []byte, requestHash string, controlled *Provenance, controlledRelations Relations) (Event, error) {

	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadProjectEvents(in.ProjectID)
	if err != nil {
		return Event{}, err
	}
	for _, record := range records {
		if in.IdempotencyKey != "" && record.Event.ActorUserID == UserID(ctx) && record.IdempotencyKey == in.IdempotencyKey {
			if record.RequestHash != requestHash {
				return Event{}, ErrConflict
			}
			return record.Event, nil
		}
	}
	provenance, relations, err := resolveEventSemantics(ctx, in.Action, controlled, controlledRelations, records)
	if err != nil {
		return Event{}, err
	}
	var actorUsername string
	if err = s.db.QueryRowContext(ctx, "SELECT username FROM users WHERE id=?", UserID(ctx)).Scan(&actorUsername); err != nil {
		return Event{}, err
	}
	sequence := int64(1)
	if len(records) != 0 {
		sequence = records[len(records)-1].Sequence + 1
	}
	event := Event{
		ID:            newID("evt"),
		ProjectID:     in.ProjectID,
		ActorUserID:   UserID(ctx),
		ActorUsername: actorUsername,
		RecordedAt:    now(),
		OccurredAt:    in.OccurredAt,
		Content:       Content{Kind: in.Content.Kind, Text: in.Content.Text},
		Metadata:      in.Metadata,
		Provenance:    provenance,
		Relations:     relations,
	}
	if in.Content.File != nil {
		file := FileInfo{ID: newID("file"), Filename: in.Content.File.Filename, MediaType: in.Content.File.MediaType, SizeBytes: len(data), SHA256: digest(data)}
		event.Content.File = &file
		filePath := s.filePath(event.ProjectID, file.ID)
		if err = writeImmutable(filePath, data); err != nil {
			// A directory sync can fail after link(2) made the raw file visible.
			// With no manifest yet it remains unreachable, so cleanup is safe.
			if removeErr := os.Remove(filePath); removeErr == nil {
				_ = syncDirectory(filepath.Dir(filePath))
			}
			return Event{}, err
		}
	}
	record := storedEvent{Sequence: sequence, Event: event, IdempotencyKey: in.IdempotencyKey, RequestHash: requestHash}
	if err = s.writeEvent(record); err != nil {
		if event.Content.File != nil {
			// If link(2) published the manifest but its directory sync failed,
			// the manifest may already reference the file. Preserve the bytes.
			_, manifestErr := os.Stat(s.eventPath(event.ProjectID, event.ID))
			if errors.Is(manifestErr, fs.ErrNotExist) {
				filePath := s.filePath(event.ProjectID, event.Content.File.ID)
				if removeErr := os.Remove(filePath); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
					return Event{}, fmt.Errorf("write event: %w; remove unpublished file: %v", err, removeErr)
				}
				_ = syncDirectory(filepath.Dir(filePath))
			}
		}
		return Event{}, err
	}
	return event, nil
}

func (s *Store) findEvent(ctx context.Context, eventID string) (storedEvent, error) {
	if UserID(ctx) == "" {
		return storedEvent{}, ErrUnauthenticated
	}
	projects, err := s.ListProjects(ctx, Empty{})
	if err != nil {
		return storedEvent{}, err
	}
	for _, project := range projects.Projects {
		data, err := os.ReadFile(s.eventPath(project.ID, eventID))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return storedEvent{}, err
		}
		var record storedEvent
		if err = json.Unmarshal(data, &record); err != nil {
			return storedEvent{}, err
		}
		if err = normalizeStoredEvent(&record); err != nil {
			return storedEvent{}, err
		}
		if record.Event.ID == eventID && record.Event.ProjectID == project.ID {
			return record, nil
		}
	}
	return storedEvent{}, ErrNotFound
}

func (s *Store) GetEvent(ctx context.Context, in EventRef) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.findEvent(ctx, in.EventID)
	return record.Event, err
}

func normalizeStoredEvent(record *storedEvent) error {
	if record.Event.Provenance.Kind == "" {
		record.Event.Provenance.Kind = ProvenanceOriginal
	}
	if !validProvenanceKind(record.Event.Provenance.Kind) {
		return fmt.Errorf("invalid provenance kind %q", record.Event.Provenance.Kind)
	}
	if record.Event.Metadata == nil {
		record.Event.Metadata = map[string]json.RawMessage{}
	}
	return nil
}

func validProvenanceKind(kind string) bool {
	return kind == ProvenanceOriginal || kind == ProvenanceTranscript || kind == ProvenanceSummary || kind == ProvenanceSuggestion || kind == ProvenanceConfirmation
}

func validateProvenanceLabels(provenance Provenance) error {
	for name, value := range map[string]string{"run_id": provenance.RunID, "skill_id": provenance.SkillID, "skill_version": provenance.SkillVersion} {
		if len(value) > 256 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
			return Invalid("%s must be safe UTF-8, max 256 bytes", name)
		}
	}
	return nil
}

func resolveEventSemantics(ctx context.Context, action *ActionInput, controlled *Provenance, controlledRelations Relations, records []storedEvent) (Provenance, Relations, error) {
	byID := make(map[string]Event, len(records))
	for _, record := range records {
		byID[record.Event.ID] = record.Event
	}
	validateSources := func(ids []string) error {
		for _, id := range ids {
			if _, ok := byID[id]; !ok {
				return ErrNotFound
			}
		}
		return nil
	}
	validateSupersedes := func(ids []string) error {
		if err := validateSources(ids); err != nil {
			return err
		}
		for _, id := range ids {
			if byID[id].ActorUserID != UserID(ctx) {
				return ErrActionForbidden
			}
		}
		return nil
	}
	if controlled != nil {
		if err := validateSources(controlled.SourceEventIDs); err != nil {
			return Provenance{}, Relations{}, err
		}
		if err := validateSupersedes(controlledRelations.SupersedesEventIDs); err != nil {
			return Provenance{}, Relations{}, err
		}
		return *controlled, controlledRelations, nil
	}
	if action == nil {
		return Provenance{Kind: ProvenanceOriginal}, Relations{}, nil
	}
	if err := validateSources(action.SourceEventIDs); err != nil {
		return Provenance{}, Relations{}, err
	}
	if err := validateSupersedes(action.SupersedesEventIDs); err != nil {
		return Provenance{}, Relations{}, err
	}
	switch action.Kind {
	case "confirmation":
		for _, id := range action.SourceEventIDs {
			source := byID[id]
			if source.Provenance.Kind != ProvenanceSuggestion || source.ActorUserID != UserID(ctx) {
				return Provenance{}, Relations{}, ErrActionForbidden
			}
		}
		return Provenance{Kind: ProvenanceConfirmation, SourceEventIDs: action.SourceEventIDs}, Relations{SupersedesEventIDs: action.SupersedesEventIDs}, nil
	case "correction":
		return Provenance{Kind: ProvenanceOriginal, SourceEventIDs: action.SourceEventIDs}, Relations{SupersedesEventIDs: action.SupersedesEventIDs}, nil
	default:
		return Provenance{}, Relations{}, Invalid("action.kind must be confirmation or correction")
	}
}

func (s *Store) findFile(ctx context.Context, in FileRef) (string, FileInfo, error) {
	if UserID(ctx) == "" {
		return "", FileInfo{}, ErrUnauthenticated
	}
	projects, err := s.ListProjects(ctx, Empty{})
	if err != nil {
		return "", FileInfo{}, err
	}
	for _, project := range projects.Projects {
		records, err := s.loadProjectEvents(project.ID)
		if err != nil {
			return "", FileInfo{}, err
		}
		for _, record := range records {
			file := record.Event.Content.File
			if file == nil || file.ID != in.FileID {
				continue
			}
			return project.ID, *file, nil
		}
	}
	return "", FileInfo{}, ErrNotFound
}

func (s *Store) openVerifiedFile(projectID string, info FileInfo) (*os.File, error) {
	f, err := os.Open(s.filePath(projectID, info.ID))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("event file %s is missing: %w", info.ID, err)
	}
	if err != nil {
		return nil, err
	}
	valid := false
	defer func() {
		if !valid {
			f.Close()
		}
	}()
	hash := sha256.New()
	size, err := io.Copy(hash, f)
	if err != nil {
		return nil, err
	}
	if size != int64(info.SizeBytes) || fmt.Sprintf("%x", hash.Sum(nil)) != info.SHA256 {
		return nil, fmt.Errorf("event file %s integrity check failed", info.ID)
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	valid = true
	return f, nil
}

func (s *Store) GetFile(ctx context.Context, in FileRef) (FileResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID, info, err := s.findFile(ctx, in)
	if err != nil {
		return FileResult{}, err
	}
	if info.SizeBytes > MaxContentBytes {
		return FileResult{}, TooLarge("file exceeds the 1 MiB base64 API; use the raw content endpoint")
	}
	f, err := s.openVerifiedFile(projectID, info)
	if err != nil {
		return FileResult{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return FileResult{}, err
	}
	return FileResult{File: info, DataBase64: base64.StdEncoding.EncodeToString(data)}, nil
}

// OpenFileContent returns a verified immutable file for authenticated streaming.
// The caller owns the returned handle.
func (s *Store) OpenFileContent(ctx context.Context, in FileRef) (FileInfo, *os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID, info, err := s.findFile(ctx, in)
	if err != nil {
		return FileInfo{}, nil, err
	}
	f, err := s.openVerifiedFile(projectID, info)
	if err != nil {
		return FileInfo{}, nil, err
	}
	return info, f, nil
}

func queryHash(in QueryInput) string {
	b, _ := json.Marshal(struct {
		ProjectID      string                     `json:"project_id"`
		From           string                     `json:"from"`
		To             string                     `json:"to"`
		TimeField      string                     `json:"time_field"`
		Metadata       map[string]json.RawMessage `json:"metadata"`
		MetadataExists []string                   `json:"metadata_exists"`
	}{in.ProjectID, in.From, in.To, in.TimeField, in.Metadata, in.MetadataExists})
	return digest(b)
}

func (s *Store) QueryEvents(ctx context.Context, in QueryInput) (Events, error) {
	out := Events{Events: []Event{}}
	if err := s.requireMember(ctx, in.ProjectID); err != nil {
		return out, err
	}
	if in.Limit == 0 {
		in.Limit = 50
	}
	if in.Limit < 1 || in.Limit > 100 {
		return out, Invalid("limit must be 1-100")
	}
	if in.TimeField == "" {
		in.TimeField = "recorded_at"
	}
	if in.TimeField != "recorded_at" && in.TimeField != "occurred_at" {
		return out, Invalid("time_field must be recorded_at or occurred_at")
	}
	var err error
	for _, value := range []*string{&in.From, &in.To} {
		if *value != "" {
			*value, err = normalizedTime(*value)
			if err != nil {
				return out, err
			}
		}
	}
	if in.From != "" && in.To != "" && in.From >= in.To {
		return out, Invalid("from must be before to")
	}
	in.Metadata, err = normalizeMetadata(in.Metadata)
	if err != nil {
		return out, err
	}
	if len(in.MetadataExists) > 128 {
		return out, Invalid("metadata_exists max 128 keys")
	}
	for _, key := range in.MetadataExists {
		if len(key) == 0 || len(key) > 128 || !utf8.ValidString(key) {
			return out, Invalid("metadata key must be 1-128 bytes")
		}
	}
	query := queryHash(in)
	cursor := eventCursor{QueryHash: query}
	if in.Cursor != "" {
		data, decodeErr := base64.RawURLEncoding.DecodeString(in.Cursor)
		if decodeErr != nil || json.Unmarshal(data, &cursor) != nil || cursor.QueryHash != query || cursor.After < 0 || cursor.Snapshot < 0 {
			return out, Invalid("invalid cursor")
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadProjectEvents(in.ProjectID)
	if err != nil {
		return out, err
	}
	if in.Cursor == "" && len(records) != 0 {
		cursor.Snapshot = records[len(records)-1].Sequence
	}
	matched := make([]storedEvent, 0, len(records))
	for _, record := range records {
		if record.Sequence <= cursor.After || record.Sequence > cursor.Snapshot || !matchesEvent(record.Event, in) {
			continue
		}
		matched = append(matched, record)
	}
	for _, record := range matched {
		candidate := append(out.Events, record.Event)
		encoded, _ := json.Marshal(candidate)
		if len(out.Events) > 0 && len(encoded) > MaxQueryPageBytes {
			break
		}
		out.Events = candidate
		cursor.After = record.Sequence
		if len(out.Events) == in.Limit {
			break
		}
	}
	if len(out.Events) < len(matched) {
		data, _ := json.Marshal(cursor)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return out, nil
}

func matchesEvent(event Event, in QueryInput) bool {
	timestamp := event.RecordedAt
	if in.TimeField == "occurred_at" {
		timestamp = event.OccurredAt
	}
	if (in.From != "" && (timestamp == "" || timestamp < in.From)) || (in.To != "" && (timestamp == "" || timestamp >= in.To)) {
		return false
	}
	for key, wanted := range in.Metadata {
		got, ok := event.Metadata[key]
		if !ok || string(got) != string(wanted) {
			return false
		}
	}
	for _, key := range in.MetadataExists {
		if _, ok := event.Metadata[key]; !ok {
			return false
		}
	}
	return true
}

func (s *Store) ListMetadata(ctx context.Context, in MetadataInput) (MetadataResult, error) {
	out := MetadataResult{Fields: []MetadataField{}, Values: []MetadataValue{}}
	if err := s.requireMember(ctx, in.ProjectID); err != nil {
		return out, err
	}
	if in.Limit == 0 {
		in.Limit = 50
	}
	if in.Limit < 1 || in.Limit > 100 || in.Offset < 0 {
		return out, Invalid("limit must be 1-100; offset must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadProjectEvents(in.ProjectID)
	if err != nil {
		return out, err
	}
	if in.Key != nil {
		if len(*in.Key) == 0 || len(*in.Key) > 128 || !utf8.ValidString(*in.Key) {
			return out, Invalid("metadata key must be 1-128 bytes")
		}
		counts := map[string]int{}
		values := map[string]json.RawMessage{}
		for _, record := range records {
			if value, ok := record.Event.Metadata[*in.Key]; ok {
				counts[string(value)]++
				values[string(value)] = value
			}
		}
		keys := sortedKeys(counts)
		for _, key := range keys {
			out.Values = append(out.Values, MetadataValue{Value: values[key], EventCount: counts[key]})
		}
		if in.Offset < len(out.Values) {
			out.Values = out.Values[in.Offset:]
		} else {
			out.Values = out.Values[:0]
		}
		if len(out.Values) > in.Limit {
			out.HasMore = true
			out.Values = out.Values[:in.Limit]
		}
		return out, nil
	}
	types := map[string]map[string]bool{}
	counts := map[string]int{}
	for _, record := range records {
		for key, value := range record.Event.Metadata {
			typ, _, err := canonical(value)
			if err != nil {
				return out, err
			}
			if types[key] == nil {
				types[key] = map[string]bool{}
			}
			types[key][typ] = true
			counts[key]++
		}
	}
	for _, key := range sortedKeys(counts) {
		field := MetadataField{Key: key, EventCount: counts[key]}
		for typ := range types[key] {
			field.Types = append(field.Types, typ)
		}
		sort.Strings(field.Types)
		out.Fields = append(out.Fields, field)
	}
	if in.Offset < len(out.Fields) {
		out.Fields = out.Fields[in.Offset:]
	} else {
		out.Fields = out.Fields[:0]
	}
	if len(out.Fields) > in.Limit {
		out.HasMore = true
		out.Fields = out.Fields[:in.Limit]
	}
	return out, nil
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (s *Store) migrateLegacyEventStorage(ctx context.Context) error {
	var exists int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='events'").Scan(&exists); err != nil || exists == 0 {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.seq,e.id,e.project_id,e.actor_user_id,u.username,e.recorded_at,e.occurred_at,e.text_content,e.metadata,e.idempotency_key,e.request_hash,f.id,f.filename,f.media_type,f.size_bytes,f.sha256,f.data FROM events e JOIN users u ON u.id=e.actor_user_id LEFT JOIN files f ON f.id=e.file_id ORDER BY e.project_id,e.seq`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var record storedEvent
		var occurred, text, key sql.NullString
		var metadata string
		var fileID, filename, media, sha sql.NullString
		var size sql.NullInt64
		var data []byte
		if err = rows.Scan(&record.Sequence, &record.Event.ID, &record.Event.ProjectID, &record.Event.ActorUserID, &record.Event.ActorUsername, &record.Event.RecordedAt, &occurred, &text, &metadata, &key, &record.RequestHash, &fileID, &filename, &media, &size, &sha, &data); err != nil {
			return err
		}
		record.Event.OccurredAt = occurred.String
		record.Event.Metadata = map[string]json.RawMessage{}
		if err = json.Unmarshal([]byte(metadata), &record.Event.Metadata); err != nil {
			return err
		}
		record.IdempotencyKey = key.String
		if fileID.Valid {
			record.Event.Content = Content{Kind: "file", File: &FileInfo{ID: fileID.String, Filename: filename.String, MediaType: media.String, SizeBytes: int(size.Int64), SHA256: sha.String}}
			if err = writeImmutable(s.filePath(record.Event.ProjectID, fileID.String), data); err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
		} else {
			value := text.String
			record.Event.Content = Content{Kind: "text", Text: &value}
		}
		if err = s.writeEvent(record); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, statement := range []string{
		"DROP TRIGGER IF EXISTS metadata_no_delete", "DROP TRIGGER IF EXISTS metadata_no_update",
		"DROP TRIGGER IF EXISTS files_no_delete", "DROP TRIGGER IF EXISTS files_no_update",
		"DROP TRIGGER IF EXISTS events_no_delete", "DROP TRIGGER IF EXISTS events_no_update",
		"DROP TABLE IF EXISTS event_metadata", "DROP TABLE IF EXISTS events", "DROP TABLE IF EXISTS files",
	} {
		if _, err = s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

var _ Backend = (*Store)(nil)
