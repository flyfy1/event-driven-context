package v2

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"

	"event-driven-context/internal/core"
)

func splitStateKey(key string) (string, string, bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 2 || !validPluginID(parts[0]) || parts[1] == "" || len(parts[1]) > 128 {
		return "", "", false
	}
	for _, r := range parts[1] {
		if !(r == '_' || r == '-' || r == '.' || r == '{' || r == '}' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return "", "", false
		}
	}
	return parts[0], parts[1], true
}
func validStateNamePattern(name string) bool {
	_, n, ok := splitStateKey("plugin-id/" + name)
	return ok && n == name
}
func privateStateName(name string) bool { return strings.HasPrefix(name, "_") }
func matchesStatePermission(pattern, name string) bool {
	if pattern == name {
		return true
	}
	if pattern == "{date}" {
		if len(name) != 10 {
			return false
		}
		for i, c := range name {
			if i == 4 || i == 7 {
				if c != '-' {
					return false
				}
			} else if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}
	return false
}

func validateStateInput(p *projectData, in PutStateInput, installation Installation) (PutStateInput, error) {
	pluginID, name, ok := splitStateKey(in.Key)
	if !ok || pluginID != installation.PluginID {
		return in, v2err("forbidden_namespace", "plugin may only write its namespace")
	}
	allowed := false
	for _, pattern := range installation.Permissions.WriteState {
		if matchesStatePermission(pattern, name) {
			allowed = true
			break
		}
	}
	if !allowed {
		return in, v2err("forbidden_namespace", "state name is not granted")
	}
	if (in.Content.Format != "markdown" && in.Content.Format != "text") || !utf8.ValidString(in.Content.Text) {
		return in, v2err("invalid_input", "state content format must be markdown or text")
	}
	if len(in.Content.Text) > MaxStateBytes {
		return in, v2err("too_large", "state content exceeds 256 KiB")
	}
	data, err := canonicalJSON(in.Data)
	if err != nil {
		return in, err
	}
	if len(data) > MaxStateBytes {
		return in, v2err("too_large", "state data exceeds 256 KiB")
	}
	in.Data = data
	if in.BasedOnSequence < 0 || in.BasedOnSequence > p.LatestSequence {
		return in, v2err("invalid_input", "based_on_sequence exceeds project history")
	}
	if len(in.Refs) > MaxRefs {
		return in, v2err("too_large", "state refs exceed 32")
	}
	in.Refs = append([]string(nil), in.Refs...)
	seen := map[string]bool{}
	for i, rawID := range in.Refs {
		id := strings.ToLower(strings.TrimSpace(rawID))
		if !uuidPattern.MatchString(id) {
			return in, v2err("invalid_ref", "state ref must be a UUID")
		}
		in.Refs[i] = id
		if seen[id] {
			return in, v2err("invalid_ref", "duplicate state ref")
		}
		seen[id] = true
		e, ok := eventByID(p, id)
		if !ok || !contains(installation.Permissions.ReadEvents, e.Type) {
			return in, v2err("invalid_ref", "state ref is missing or unreadable")
		}
		if e.Sequence > in.BasedOnSequence {
			return in, v2err("invalid_ref", "state ref is newer than based_on_sequence")
		}
	}
	sort.Strings(in.Refs)
	return in, nil
}

func (s *Service) PutState(ctx context.Context, projectID string, in PutStateInput) (State, error) {
	if err := s.identity.RequireProjectMember(ctx, projectID); err != nil {
		return State{}, err
	}
	if in.AsPluginID == "" {
		return State{}, v2err("forbidden_namespace", "as_plugin_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.data.Projects[projectID]
	if p == nil {
		return State{}, core.ErrNotFound
	}
	installation, ok := p.Installations[in.AsPluginID]
	if !ok || installation.Status == "removed" || installation.ManagerUserID != core.UserID(ctx) {
		return State{}, core.ErrNotFound
	}
	if installation.Status == "paused" {
		return State{}, v2err("plugin_paused", "plugin is paused")
	}
	return s.putStateLocked(projectID, in, installation)
}
func (s *Service) PutStateAsPlugin(ctx context.Context, principal PluginPrincipal, in PutStateInput) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	installation, err := s.authorizePluginLocked(principal)
	if err != nil {
		return State{}, err
	}
	in.AsPluginID = ""
	return s.putStateLocked(principal.ProjectID, in, installation)
}

func (s *Service) putStateLocked(projectID string, in PutStateInput, installation Installation) (State, error) {
	p := s.data.Projects[projectID]
	if p == nil {
		return State{}, core.ErrNotFound
	}
	normalized, err := validateStateInput(p, in, installation)
	if err != nil {
		return State{}, err
	}
	versions := p.States[in.Key]
	current := int64(0)
	if len(versions) > 0 {
		current = versions[len(versions)-1].Version
	}
	if normalized.ExpectedVersion != nil && *normalized.ExpectedVersion != current {
		return State{}, v2err("state_version_mismatch", "expected state version does not match")
	}
	state := State{ProjectID: projectID, Key: in.Key, Version: current + 1, Content: normalized.Content, Data: normalized.Data, BasedOnSequence: normalized.BasedOnSequence, Refs: normalized.Refs, Producer: Producer{installation.PluginID, installation.PluginVersion}, UpdatedAt: nowUTC(), Lag: p.LatestSequence - normalized.BasedOnSequence}
	candidate, err := cloneSnapshot(s.data)
	if err != nil {
		return State{}, err
	}
	candidate.Projects[projectID].States[in.Key] = append(candidate.Projects[projectID].States[in.Key], state)
	if err = s.persistSnapshotLocked(candidate); err != nil {
		return State{}, err
	}
	s.data = candidate
	return cloneState(state), nil
}

func (s *Service) ListState(ctx context.Context, projectID string, in ListStateInput) (StatesResult, error) {
	if err := s.identity.RequireProjectMember(ctx, projectID); err != nil {
		return StatesResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listStateLocked(projectID, in, nil)
}
func (s *Service) ListStateAsPlugin(ctx context.Context, principal PluginPrincipal, in ListStateInput) (StatesResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	installation, err := s.authorizePluginLocked(principal)
	if err != nil {
		return StatesResult{}, err
	}
	return s.listStateLocked(principal.ProjectID, in, &installation)
}
func (s *Service) listStateLocked(projectID string, in ListStateInput, plugin *Installation) (StatesResult, error) {
	p := s.data.Projects[projectID]
	if p == nil {
		return StatesResult{States: []State{}}, nil
	}
	keys := make([]string, 0, len(p.States))
	for key := range p.States {
		if strings.HasPrefix(key, in.Prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	out := StatesResult{States: []State{}, LatestSequence: p.LatestSequence}
	for _, key := range keys {
		pid, name, ok := splitStateKey(key)
		if !ok || privateStateName(name) && (plugin == nil || plugin.PluginID != pid) {
			continue
		}
		versions := p.States[key]
		if len(versions) > 0 {
			st := versions[len(versions)-1]
			st.Lag = p.LatestSequence - st.BasedOnSequence
			out.States = append(out.States, cloneState(st))
		}
	}
	return out, nil
}

func (s *Service) GetState(ctx context.Context, projectID string, in GetStateInput) (StatesResult, error) {
	if err := s.identity.RequireProjectMember(ctx, projectID); err != nil {
		return StatesResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getStateLocked(projectID, in, nil)
}
func (s *Service) GetStateAsPlugin(ctx context.Context, principal PluginPrincipal, in GetStateInput) (StatesResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	installation, err := s.authorizePluginLocked(principal)
	if err != nil {
		return StatesResult{}, err
	}
	return s.getStateLocked(principal.ProjectID, in, &installation)
}
func (s *Service) getStateLocked(projectID string, in GetStateInput, plugin *Installation) (StatesResult, error) {
	if len(in.Keys) < 1 || len(in.Keys) > 100 {
		return StatesResult{}, v2err("invalid_input", "keys must contain 1 to 100 items")
	}
	if in.Version != nil && *in.Version <= 0 {
		return StatesResult{}, v2err("invalid_input", "state version must be positive")
	}
	p := s.data.Projects[projectID]
	if p == nil {
		return StatesResult{States: []State{}}, nil
	}
	out := StatesResult{States: []State{}, LatestSequence: p.LatestSequence}
	for _, key := range in.Keys {
		pid, name, ok := splitStateKey(key)
		if !ok {
			return StatesResult{}, v2err("invalid_input", "invalid state key")
		}
		if privateStateName(name) && (plugin == nil || plugin.PluginID != pid) {
			return StatesResult{}, v2err("forbidden_namespace", "private state is visible only to its plugin")
		}
		versions := p.States[key]
		if len(versions) == 0 {
			continue
		}
		var found *State
		if in.Version == nil {
			v := versions[len(versions)-1]
			found = &v
		} else {
			for _, v := range versions {
				if v.Version == *in.Version {
					vv := v
					found = &vv
					break
				}
			}
		}
		if found != nil {
			found.Lag = p.LatestSequence - found.BasedOnSequence
			out.States = append(out.States, cloneState(*found))
		}
	}
	return out, nil
}

func (s *Service) RequestManualRun(ctx context.Context, projectID, pluginID string, in ManualRunInput) (ManualRunRequest, error) {
	if _, err := s.managedPlugin(ctx, projectID, pluginID); err != nil {
		return ManualRunRequest{}, err
	}
	in.RequestID = strings.TrimSpace(in.RequestID)
	if in.RequestID == "" || len(in.RequestID) > 128 || len(in.SourceEventIDs) > MaxRefs {
		return ManualRunRequest{}, v2err("invalid_input", "invalid manual run request")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.data.Projects[projectID]
	if p == nil {
		return ManualRunRequest{}, core.ErrNotFound
	}
	installation, ok := p.Installations[pluginID]
	if !ok || installation.Status == "removed" || installation.ManagerUserID != core.UserID(ctx) {
		return ManualRunRequest{}, core.ErrNotFound
	}
	if installation.Status == "paused" {
		return ManualRunRequest{}, v2err("plugin_paused", "plugin is paused")
	}
	if installation.Status != "active" {
		return ManualRunRequest{}, core.ErrNotFound
	}
	in.SourceEventIDs = append([]string(nil), in.SourceEventIDs...)
	for i, rawID := range in.SourceEventIDs {
		id := strings.ToLower(strings.TrimSpace(rawID))
		if !uuidPattern.MatchString(id) {
			return ManualRunRequest{}, v2err("invalid_ref", "manual run source must be a UUID")
		}
		in.SourceEventIDs[i] = id
	}
	sort.Strings(in.SourceEventIDs)
	for i, id := range in.SourceEventIDs {
		if i > 0 && id == in.SourceEventIDs[i-1] {
			return ManualRunRequest{}, v2err("invalid_ref", "duplicate manual run source")
		}
		e, ok := eventByID(p, id)
		if !ok || !contains(installation.Permissions.ReadEvents, e.Type) {
			return ManualRunRequest{}, v2err("invalid_ref", "manual run source is missing or unreadable")
		}
	}
	mapKey := pluginID + "\x00" + in.RequestID
	if old, ok := p.ManualRuns[mapKey]; ok {
		if strings.Join(old.SourceEventIDs, "\x00") == strings.Join(in.SourceEventIDs, "\x00") {
			return cloneManualRun(old), nil
		}
		return ManualRunRequest{}, v2err("conflict", "request id reused with different inputs")
	}
	req := ManualRunRequest{RequestID: in.RequestID, ProjectID: projectID, PluginID: pluginID, SourceEventIDs: in.SourceEventIDs, Status: "accepted", CreatedAt: nowUTC()}
	candidate, err := cloneSnapshot(s.data)
	if err != nil {
		return ManualRunRequest{}, err
	}
	candidate.Projects[projectID].ManualRuns[mapKey] = req
	// Publish an authenticated private mailbox version for the processor host.
	items := make([]ManualRunRequest, 0)
	for key, v := range candidate.Projects[projectID].ManualRuns {
		if strings.HasPrefix(key, pluginID+"\x00") {
			items = append(items, v)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt < items[j].CreatedAt })
	if len(items) > 1000 {
		items = items[len(items)-1000:]
	}
	data, _ := json.Marshal(items)
	key := pluginID + "/_requests"
	versions := candidate.Projects[projectID].States[key]
	version := int64(1)
	if len(versions) > 0 {
		version = versions[len(versions)-1].Version + 1
	}
	refSet := map[string]bool{}
	for _, item := range items {
		for _, id := range item.SourceEventIDs {
			refSet[id] = true
		}
	}
	refs := make([]string, 0, len(refSet))
	for id := range refSet {
		refs = append(refs, id)
	}
	sort.Strings(refs)
	state := State{ProjectID: projectID, Key: key, Version: version, Content: StateContent{Format: "text", Text: "manual processing requests"}, Data: data, BasedOnSequence: candidate.Projects[projectID].LatestSequence, Refs: refs, Producer: Producer{pluginID, installation.PluginVersion}, UpdatedAt: nowUTC(), Lag: 0}
	candidate.Projects[projectID].States[key] = append(versions, state)
	if err = s.persistSnapshotLocked(candidate); err != nil {
		return ManualRunRequest{}, err
	}
	s.data = candidate
	return cloneManualRun(req), nil
}
