package v2

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	"event-driven-context/internal/core"
)

type eventAuthority struct {
	actor  Actor
	plugin *Installation
}

func (s *Service) RecordEvents(ctx context.Context, projectID string, in RecordEventsInput) (RecordEventsResult, error) {
	if err := s.identity.RequireProjectMember(ctx, projectID); err != nil {
		return RecordEventsResult{}, err
	}
	u, err := s.identity.Me(ctx)
	if err != nil {
		return RecordEventsResult{}, err
	}
	return s.recordEvents(projectID, in, eventAuthority{actor: Actor{Type: "user", ID: u.ID, Username: u.Username}})
}

func (s *Service) RecordEventsAsPlugin(ctx context.Context, principal PluginPrincipal, in RecordEventsInput) (RecordEventsResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	installation, err := s.authorizePluginLocked(principal)
	if err != nil {
		return RecordEventsResult{}, err
	}
	return s.recordEventsLocked(principal.ProjectID, in, eventAuthority{actor: Actor{Type: "plugin", ID: installation.PluginID, OnBehalfOf: installation.ManagerUserID}, plugin: &installation})
}

func (s *Service) recordEvents(projectID string, in RecordEventsInput, auth eventAuthority) (RecordEventsResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recordEventsLocked(projectID, in, auth)
}

func (s *Service) recordEventsLocked(projectID string, in RecordEventsInput, auth eventAuthority) (RecordEventsResult, error) {
	if len(in.Events) < 1 || len(in.Events) > MaxBatchEvents {
		return RecordEventsResult{}, v2err("invalid_input", "events must contain 1 to 100 items")
	}
	out := RecordEventsResult{Results: make([]EventWriteResult, 0, len(in.Events))}
	for _, raw := range in.Events {
		item, err := normalizedInput(raw)
		if err == nil {
			channel := sourceChannel(item.Source)
			if auth.plugin == nil && (item.Type == "derived" || channel == "plugin") {
				err = v2err("forbidden", "derived and plugin source require plugin credentials")
			}
			if auth.plugin != nil {
				if channel != "plugin" {
					err = v2err("invalid_input", "plugin writes require source.channel=plugin")
				} else if !contains(auth.plugin.Permissions.WriteEvents, item.Type) {
					err = v2err("forbidden", "plugin cannot write event type %s", item.Type)
				}
			}
		}
		if err != nil {
			out.Results = append(out.Results, eventResult(raw.ID, "invalid", 0, err))
			continue
		}
		p := s.projectLocked(projectID)
		var existing *Event
		for i := range p.Events {
			if p.Events[i].ID == item.ID {
				existing = &p.Events[i]
				break
			}
		}
		if existing != nil {
			if sameInput(*existing, item) {
				out.Results = append(out.Results, eventResult(item.ID, "duplicate", existing.Sequence, nil))
			} else {
				out.Results = append(out.Results, eventResult(item.ID, "conflict", existing.Sequence, v2err("conflict", "event id already exists with different content")))
			}
			continue
		}
		refsOK := true
		for _, ref := range item.Refs {
			target, ok := eventByID(p, ref.ID)
			if !ok {
				out.Results = append(out.Results, eventResult(item.ID, "invalid", 0, v2err("invalid_ref", "referenced event does not exist in project")))
				refsOK = false
				break
			}
			if auth.plugin != nil && !contains(auth.plugin.Permissions.ReadEvents, target.Type) {
				out.Results = append(out.Results, eventResult(item.ID, "invalid", 0, v2err("invalid_ref", "plugin cannot read referenced event")))
				refsOK = false
				break
			}
		}
		if !refsOK {
			continue
		}
		if item.Content.Kind == "file" {
			f, ok := p.Files[item.Content.FileID]
			if !ok {
				out.Results = append(out.Results, eventResult(item.ID, "invalid", 0, v2err("invalid_ref", "file does not exist in project")))
				continue
			}
			if auth.plugin != nil {
				readable := false
				for _, prior := range p.Events {
					if prior.Content.Kind == "file" && prior.Content.FileID == f.ID && contains(auth.plugin.Permissions.ReadEvents, prior.Type) {
						readable = true
						break
					}
				}
				if !readable {
					out.Results = append(out.Results, eventResult(item.ID, "invalid", 0, v2err("invalid_ref", "plugin cannot access referenced file")))
					continue
				}
			}
			item.Content.MediaType, item.Content.Filename, item.Content.SizeBytes, item.Content.SHA256 = f.MediaType, f.Filename, f.SizeBytes, f.SHA256
		}
		candidate, err := cloneSnapshot(s.data)
		if err != nil {
			return out, err
		}
		cp := candidate.Projects[projectID]
		if cp == nil {
			cp = &projectData{}
			normalizeProjectData(cp)
			candidate.Projects[projectID] = cp
		}
		cp.LatestSequence++
		e := Event{ID: item.ID, ProjectID: projectID, Type: item.Type, Content: item.Content, Metadata: item.Metadata, Source: item.Source, Refs: item.Refs, OccurredAt: item.OccurredAt, Sequence: cp.LatestSequence, RecordedAt: nowUTC(), Actor: auth.actor}
		cp.Events = append(cp.Events, e)
		if e.Content.Kind == "file" {
			f := cp.Files[e.Content.FileID]
			f.Referenced = true
			cp.Files[f.ID] = f
		}
		if err = s.persistSnapshotLocked(candidate); err != nil {
			return out, err
		}
		s.data = candidate
		out.Results = append(out.Results, eventResult(e.ID, "created", e.Sequence, nil))
	}
	return out, nil
}

func eventResult(id, status string, seq int64, err error) EventWriteResult {
	r := EventWriteResult{ID: strings.ToLower(strings.TrimSpace(id)), Status: status, Sequence: seq}
	var e *Error
	if errors.As(err, &e) {
		r.Error = e
	}
	return r
}
func sourceChannel(source map[string]json.RawMessage) string {
	var v string
	_ = json.Unmarshal(source["channel"], &v)
	return v
}
func contains(items []string, v string) bool {
	for _, x := range items {
		if x == v {
			return true
		}
	}
	return false
}
func eventByID(p *projectData, id string) (Event, bool) {
	for _, e := range p.Events {
		if e.ID == id {
			return e, true
		}
	}
	return Event{}, false
}

func (s *Service) GetEvent(ctx context.Context, projectID, eventID string) (Event, error) {
	if err := s.identity.RequireProjectMember(ctx, projectID); err != nil {
		return Event{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.data.Projects[projectID]
	if p == nil {
		return Event{}, core.ErrNotFound
	}
	e, ok := eventByID(p, strings.ToLower(eventID))
	if !ok {
		return Event{}, core.ErrNotFound
	}
	return cloneEvent(e), nil
}
func (s *Service) GetEventAsPlugin(ctx context.Context, principal PluginPrincipal, eventID string) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, err := s.authorizePluginLocked(principal)
	if err != nil {
		return Event{}, err
	}
	p := s.data.Projects[principal.ProjectID]
	if p == nil {
		return Event{}, core.ErrNotFound
	}
	e, ok := eventByID(p, strings.ToLower(eventID))
	if !ok || !contains(in.Permissions.ReadEvents, e.Type) {
		return Event{}, core.ErrNotFound
	}
	return cloneEvent(e), nil
}

type eventCursor struct {
	ProjectID string `json:"p"`
	Snapshot  int64  `json:"s"`
	After     int64  `json:"a"`
	QueryHash string `json:"q"`
	Signature string `json:"h,omitempty"`
}

func (s *Service) QueryEvents(ctx context.Context, projectID string, in QueryEventsInput) (EventsPage, error) {
	if err := s.identity.RequireProjectMember(ctx, projectID); err != nil {
		return EventsPage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queryLocked(projectID, in, nil)
}
func (s *Service) QueryEventsAsPlugin(ctx context.Context, principal PluginPrincipal, in QueryEventsInput) (EventsPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	install, err := s.authorizePluginLocked(principal)
	if err != nil {
		return EventsPage{}, err
	}
	return s.queryLocked(principal.ProjectID, in, &install)
}

func (s *Service) queryLocked(projectID string, in QueryEventsInput, plugin *Installation) (EventsPage, error) {
	p := s.data.Projects[projectID]
	if p == nil {
		p = &projectData{}
		normalizeProjectData(p)
	}
	if in.Limit == 0 {
		in.Limit = 50
	}
	if in.Limit < 1 || in.Limit > 100 {
		return EventsPage{}, v2err("invalid_input", "limit must be 1 to 100")
	}
	if in.TimeField == "" {
		in.TimeField = "recorded_at"
	}
	if in.TimeField != "recorded_at" && in.TimeField != "occurred_at" {
		return EventsPage{}, v2err("invalid_input", "invalid time_field")
	}
	if in.AfterSequence < 0 || len(in.Cursor) > 2048 {
		return EventsPage{}, v2err("invalid_input", "invalid sequence or cursor")
	}
	if in.Order != "" && in.Order != "asc" && in.Order != "desc" {
		return EventsPage{}, v2err("invalid_input", "order must be asc or desc")
	}
	descending := in.Order == "desc"
	for _, typ := range in.Types {
		if typ != "log" && typ != "note" && typ != "derived" {
			return EventsPage{}, v2err("invalid_input", "invalid event type filter")
		}
	}
	in.Types = uniqueSortedStrings(in.Types)
	if in.RefsTo != "" {
		in.RefsTo = strings.ToLower(strings.TrimSpace(in.RefsTo))
		if !uuidPattern.MatchString(in.RefsTo) {
			return EventsPage{}, v2err("invalid_input", "refs_to must be a UUID")
		}
	}
	from, err := parseTimeFilter(in.From)
	if err != nil {
		return EventsPage{}, err
	}
	to, err := parseTimeFilter(in.To)
	if err != nil {
		return EventsPage{}, err
	}
	if !from.IsZero() && !to.IsZero() && !from.Before(to) {
		return EventsPage{}, v2err("invalid_input", "from must be before to")
	}
	metadata, err := canonicalObject(in.Metadata, MaxMetadata)
	if err != nil {
		return EventsPage{}, err
	}
	source, err := canonicalObject(in.Source, MaxMetadata)
	if err != nil {
		return EventsPage{}, err
	}
	in.Metadata, in.Source = metadata, source
	queryCopy := in
	queryCopy.Cursor = ""
	pluginScope := struct {
		InstallationID string   `json:"installation_id,omitempty"`
		ReadEvents     []string `json:"read_events,omitempty"`
	}{}
	if plugin != nil {
		pluginScope.InstallationID = plugin.ID
		pluginScope.ReadEvents = uniqueSortedStrings(plugin.Permissions.ReadEvents)
	}
	qraw, _ := json.Marshal(struct {
		Query QueryEventsInput `json:"query"`
		Scope any              `json:"scope"`
	}{queryCopy, pluginScope})
	sum := sha256.Sum256(qraw)
	qhash := hex.EncodeToString(sum[:])
	snapshotSeq, boundary := p.LatestSequence, in.AfterSequence
	if descending {
		boundary = snapshotSeq + 1
	}
	if in.Cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(in.Cursor)
		if e != nil {
			return EventsPage{}, v2err("invalid_input", "invalid cursor")
		}
		var c eventCursor
		if json.Unmarshal(raw, &c) != nil {
			return EventsPage{}, v2err("invalid_input", "invalid cursor")
		}
		invalidBoundary := c.After < in.AfterSequence || c.After > c.Snapshot
		if descending {
			invalidBoundary = c.After <= in.AfterSequence || c.After > c.Snapshot+1
		}
		if c.ProjectID != projectID || c.QueryHash != qhash || c.Snapshot < 0 || c.Snapshot > p.LatestSequence || invalidBoundary || !s.validCursor(c) {
			return EventsPage{}, v2err("invalid_input", "invalid cursor")
		}
		snapshotSeq, boundary = c.Snapshot, c.After
	}
	out := EventsPage{Events: []Event{}, LatestSequence: snapshotSeq}
	start, end, step := 0, len(p.Events), 1
	if descending {
		start, end, step = len(p.Events)-1, -1, -1
	}
	for index := start; index != end; index += step {
		e := p.Events[index]
		outsideWindow := e.Sequence <= boundary || e.Sequence > snapshotSeq
		if descending {
			outsideWindow = e.Sequence <= in.AfterSequence || e.Sequence >= boundary || e.Sequence > snapshotSeq
		}
		if outsideWindow {
			continue
		}
		if plugin != nil && !contains(plugin.Permissions.ReadEvents, e.Type) {
			continue
		}
		if len(in.Types) > 0 && !contains(in.Types, e.Type) {
			continue
		}
		if !objectMatches(e.Metadata, in.Metadata) || !objectMatches(e.Source, in.Source) {
			continue
		}
		if in.RefsTo != "" && !refsTo(e.Refs, in.RefsTo) {
			continue
		}
		var tv string
		if in.TimeField == "occurred_at" {
			tv = e.OccurredAt
		} else {
			tv = e.RecordedAt
		}
		if tv == "" {
			continue
		}
		t, _ := time.Parse(time.RFC3339Nano, tv)
		if !from.IsZero() && t.Before(from) {
			continue
		}
		if !to.IsZero() && !t.Before(to) {
			continue
		}
		if len(out.Events) == in.Limit {
			c := eventCursor{ProjectID: projectID, Snapshot: snapshotSeq, After: out.Events[len(out.Events)-1].Sequence, QueryHash: qhash}
			c.Signature = s.signCursor(c)
			raw, _ := json.Marshal(c)
			out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
			break
		}
		out.Events = append(out.Events, cloneEvent(e))
	}
	return out, nil
}
func (s *Service) signCursor(c eventCursor) string {
	c.Signature = ""
	raw, _ := json.Marshal(c)
	mac := hmac.New(sha256.New, []byte(s.data.CursorKey))
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}
func (s *Service) validCursor(c eventCursor) bool {
	got, err := hex.DecodeString(c.Signature)
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(s.signCursor(c))
	return err == nil && hmac.Equal(got, want)
}
func objectMatches(have, want map[string]json.RawMessage) bool {
	for k, v := range want {
		if !jsonEqual(have[k], v) {
			return false
		}
	}
	return true
}
func jsonEqual(a, b json.RawMessage) bool { return string(a) == string(b) }
func refsTo(refs []Ref, id string) bool {
	for _, r := range refs {
		if r.ID == id {
			return true
		}
	}
	return false
}

func uniqueSortedStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, value := range in {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Service) ListMetadata(ctx context.Context, projectID string, in MetadataInput) (MetadataResult, error) {
	if err := s.identity.RequireProjectMember(ctx, projectID); err != nil {
		return MetadataResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listMetadataLocked(s.data.Projects[projectID], in, nil), nil
}

func (s *Service) ListMetadataAsPlugin(ctx context.Context, principal PluginPrincipal, in MetadataInput) (MetadataResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	installation, err := s.authorizePluginLocked(principal)
	if err != nil {
		return MetadataResult{}, err
	}
	return s.listMetadataLocked(s.data.Projects[principal.ProjectID], in, &installation), nil
}

func (s *Service) listMetadataLocked(p *projectData, in MetadataInput, plugin *Installation) MetadataResult {
	if p == nil {
		return MetadataResult{Fields: []MetadataField{}}
	}
	if in.Key != "" {
		counts := map[string]int{}
		raws := map[string]json.RawMessage{}
		for _, e := range p.Events {
			if plugin != nil && !contains(plugin.Permissions.ReadEvents, e.Type) {
				continue
			}
			if v, ok := e.Metadata[in.Key]; ok {
				counts[string(v)]++
				raws[string(v)] = v
			}
		}
		keys := make([]string, 0, len(counts))
		for k := range counts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := MetadataResult{Values: []MetadataValue{}}
		for _, k := range keys {
			out.Values = append(out.Values, MetadataValue{cloneRawMessage(raws[k]), counts[k]})
		}
		return out
	}
	type agg struct {
		types map[string]bool
		n     int
	}
	m := map[string]*agg{}
	for _, e := range p.Events {
		if plugin != nil && !contains(plugin.Permissions.ReadEvents, e.Type) {
			continue
		}
		for k, v := range e.Metadata {
			a := m[k]
			if a == nil {
				a = &agg{types: map[string]bool{}}
				m[k] = a
			}
			a.n++
			var x any
			d := json.NewDecoder(strings.NewReader(string(v)))
			d.UseNumber()
			_ = d.Decode(&x)
			a.types[jsonType(x)] = true
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := MetadataResult{Fields: []MetadataField{}}
	for _, k := range keys {
		ts := make([]string, 0, len(m[k].types))
		for t := range m[k].types {
			ts = append(ts, t)
		}
		sort.Strings(ts)
		out.Fields = append(out.Fields, MetadataField{k, ts, m[k].n})
	}
	return out
}
func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case json.Number:
		return "number"
	case bool:
		return "boolean"
	case []any:
		return "array"
	default:
		return "object"
	}
}

func (s *Service) OpenFileAsPlugin(ctx context.Context, principal PluginPrincipal, fileID string) (FileInfo, io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	install, err := s.authorizePluginLocked(principal)
	if err != nil {
		return FileInfo{}, nil, err
	}
	p := s.data.Projects[principal.ProjectID]
	if p == nil {
		return FileInfo{}, nil, core.ErrNotFound
	}
	authorized := false
	for _, e := range p.Events {
		if e.Content.Kind == "file" && e.Content.FileID == fileID && contains(install.Permissions.ReadEvents, e.Type) {
			authorized = true
			break
		}
	}
	if !authorized {
		return FileInfo{}, nil, core.ErrNotFound
	}
	return s.openFileLocked(p, fileID)
}
