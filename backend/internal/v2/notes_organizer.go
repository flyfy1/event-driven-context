package v2

import (
	"context"
	"sort"
	"time"

	"event-driven-context/internal/notes"
)

type noteLease struct {
	ID, InstallationID, PromptVersion            string
	ConfigRevision, BaseRevision, After, Through int64
	Expires                                      time.Time
	Events                                       []notes.EventPreview
}

func (s *Service) BeginNotesOrganization(ctx context.Context, p PluginPrincipal, in notes.BeginInput) (notes.OrganizationRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	installation, err := s.authorizePluginLocked(p)
	if err != nil {
		return notes.OrganizationRun{}, err
	}
	if !installation.Permissions.OrganizeNotes {
		return notes.OrganizationRun{}, v2err("forbidden", "plugin cannot organize notes")
	}
	if in.PromptVersion == "" || len(in.PromptVersion) > 64 {
		return notes.OrganizationRun{}, v2err("invalid_input", "prompt_version required")
	}
	for _, typ := range []string{"log", "note", "derived"} {
		if !contains(installation.Permissions.ReadEvents, typ) {
			return notes.OrganizationRun{}, v2err("forbidden", "organizer requires all project event types")
		}
	}
	if lease, ok := s.noteLeases[p.ProjectID]; ok && time.Now().Before(lease.Expires) {
		return notes.OrganizationRun{Noop: true, Reason: "already_running"}, nil
	}
	store, err := s.noteStore(p.ProjectID)
	if err != nil {
		return notes.OrganizationRun{}, err
	}
	snapshot, err := store.Export()
	if err != nil {
		return notes.OrganizationRun{}, notesError(err)
	}
	cp, err := store.Checkpoint()
	if err != nil {
		return notes.OrganizationRun{}, err
	}
	project := s.data.Projects[p.ProjectID]
	if cp.AfterSequence > project.LatestSequence {
		return notes.OrganizationRun{}, v2err("conflict", "notes checkpoint is ahead of project events")
	}
	timezone, err := s.identity.ProjectTimezone(ctx, p.ProjectID)
	if err != nil {
		return notes.OrganizationRun{}, err
	}
	out := notes.OrganizationRun{Timezone: timezone, AfterSequence: cp.AfterSequence, ThroughSequence: cp.AfterSequence, Snapshot: snapshot, Events: []notes.EventPreview{}}
	for _, e := range project.Events {
		if e.Sequence <= cp.AfterSequence {
			continue
		}
		text := []rune(e.Content.Text)
		if len(text) > 240 {
			text = append(text[:240], []rune(" …")...)
		}
		out.Events = append(out.Events, notes.EventPreview{ID: e.ID, Sequence: e.Sequence, Type: e.Type, Kind: e.Content.Kind, RecordedAt: e.RecordedAt, Preview: string(text)})
		out.ThroughSequence = e.Sequence
		if len(out.Events) == 20 {
			break
		}
	}
	if len(out.Events) == 0 && (snapshot.Revision == 0 || cp.PromptVersion == in.PromptVersion && cp.PolicyHash == notes.PolicyHash(snapshot.Files)) {
		out.Noop = true
		out.Reason = "up_to_date"
		return out, nil
	}
	id, err := randomID("nrun_")
	if err != nil {
		return out, err
	}
	out.RunID = id
	if s.noteLeases == nil {
		s.noteLeases = map[string]noteLease{}
	}
	s.noteLeases[p.ProjectID] = noteLease{ID: id, InstallationID: p.InstallationID, PromptVersion: in.PromptVersion, ConfigRevision: installation.ConfigRevision, BaseRevision: snapshot.Revision, After: out.AfterSequence, Through: out.ThroughSequence, Expires: time.Now().Add(12 * time.Minute), Events: out.Events}
	return out, nil
}

func (s *Service) PublishNotesOrganization(ctx context.Context, p PluginPrincipal, in notes.PublishInput) (notes.PublishResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	installation, err := s.authorizePluginLocked(p)
	if err != nil {
		return notes.PublishResult{}, err
	}
	lease, ok := s.noteLeases[p.ProjectID]
	if !installation.Permissions.OrganizeNotes || !ok || lease.ID != in.RunID || lease.InstallationID != p.InstallationID || lease.ConfigRevision != installation.ConfigRevision || !time.Now().Before(lease.Expires) {
		return notes.PublishResult{}, v2err("conflict", "organizer lease expired or changed")
	}
	expected := map[string]bool{}
	for _, e := range lease.Events {
		expected[e.ID] = true
	}
	for _, a := range in.Accounted {
		if !expected[a.ID] || (a.Disposition != "used" && a.Disposition != "irrelevant") {
			return notes.PublishResult{}, v2err("invalid_input", "account every batch event exactly once")
		}
		delete(expected, a.ID)
	}
	if len(expected) != 0 {
		return notes.PublishResult{}, v2err("invalid_input", "unaccounted events remain")
	}
	store, err := s.noteStore(p.ProjectID)
	if err != nil {
		return notes.PublishResult{}, err
	}
	snapshot, err := store.Export()
	if err != nil {
		return notes.PublishResult{}, err
	}
	if snapshot.Revision != lease.BaseRevision {
		return notes.PublishResult{}, v2err("conflict", "notes changed while organizing")
	}
	files := map[string]notes.WriteFile{}
	for _, f := range snapshot.Files {
		files[f.Path] = notes.WriteFile{Path: f.Path, Content: f.Content}
	}
	seen := map[string]bool{}
	for _, name := range in.Removed {
		if err := notes.ValidatePath(name); err != nil {
			return notes.PublishResult{}, notesError(err)
		}
		if seen[name] {
			return notes.PublishResult{}, v2err("invalid_input", "duplicate change path")
		}
		seen[name] = true
		delete(files, name)
	}
	for _, f := range in.Files {
		if seen[f.Path] {
			return notes.PublishResult{}, v2err("invalid_input", "duplicate change path")
		}
		seen[f.Path] = true
		files[f.Path] = f
	}
	all := make([]notes.WriteFile, 0, len(files))
	for _, f := range files {
		all = append(all, f)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Path < all[j].Path })
	sources := map[string]bool{}
	for _, e := range s.data.Projects[p.ProjectID].Events {
		if e.Sequence <= lease.Through && contains(installation.Permissions.ReadEvents, e.Type) {
			sources[e.ID] = true
		}
	}
	if err = notes.ValidateOrganizedTree(all, snapshot.Files, sources); err != nil {
		return notes.PublishResult{}, v2err("invalid_input", "invalid notes: %s", err)
	}
	for _, f := range all {
		if len(f.Content) > notes.MaxFileBytes {
			return notes.PublishResult{}, v2err("too_large", "note too large")
		}
	}
	forHash := make([]notes.File, 0, len(all))
	for _, f := range all {
		forHash = append(forHash, notes.File{Path: f.Path, Content: f.Content})
	}
	cp := notes.Checkpoint{AfterSequence: lease.Through, PromptVersion: lease.PromptVersion, PolicyHash: notes.PolicyHash(forHash), RunID: lease.ID}
	written, err := store.Sync(notes.SyncInput{ExpectedRevision: &lease.BaseRevision, Files: all, Replace: true, Checkpoint: &cp}, "plugin:"+installation.PluginID)
	if err != nil {
		return notes.PublishResult{}, notesError(err)
	}
	delete(s.noteLeases, p.ProjectID)
	return notes.PublishResult{Revision: written.Revision, ThroughSequence: lease.Through}, nil
}

func (s *Service) CancelNotesOrganization(ctx context.Context, p PluginPrincipal, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.authorizePluginLocked(p); err != nil {
		return err
	}
	if lease, ok := s.noteLeases[p.ProjectID]; ok && lease.ID == runID && lease.InstallationID == p.InstallationID {
		delete(s.noteLeases, p.ProjectID)
	}
	return nil
}
