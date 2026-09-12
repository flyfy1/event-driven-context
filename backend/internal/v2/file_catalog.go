package v2

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"

	"event-driven-context/internal/core"
)

func (s *Service) ListFiles(ctx context.Context, projectID string, in FileCatalogInput) (FileCatalogPage, error) {
	if err := s.identity.RequireProjectMember(ctx, projectID); err != nil {
		return FileCatalogPage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listFilesLocked(projectID, in, nil, "user:"+core.UserID(ctx))
}
func (s *Service) ListFilesAsPlugin(ctx context.Context, p PluginPrincipal, in FileCatalogInput) (FileCatalogPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	installation, err := s.authorizePluginLocked(p)
	if err != nil {
		return FileCatalogPage{}, err
	}
	return s.listFilesLocked(p.ProjectID, in, &installation, catalogAuthority(installation))
}
func catalogAuthority(p Installation) string {
	return fmt.Sprintf("plugin:%s:%d:%v", p.ID, p.ConfigRevision, p.Permissions.ReadEvents)
}
func (s *Service) FileMetadata(ctx context.Context, projectID, fileID string, through *int64) (FileCatalogEntry, error) {
	if err := s.identity.RequireProjectMember(ctx, projectID); err != nil {
		return FileCatalogEntry{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fileMetadataLocked(projectID, fileID, through, nil)
}
func (s *Service) FileMetadataAsPlugin(ctx context.Context, p PluginPrincipal, fileID string, through *int64) (FileCatalogEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	installation, err := s.authorizePluginLocked(p)
	if err != nil {
		return FileCatalogEntry{}, err
	}
	return s.fileMetadataLocked(p.ProjectID, fileID, through, &installation)
}
func (s *Service) FileReferences(ctx context.Context, projectID, fileID string, in FileCatalogInput) (FileReferencesPage, error) {
	if err := s.identity.RequireProjectMember(ctx, projectID); err != nil {
		return FileReferencesPage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fileReferencesLocked(projectID, fileID, in, nil, "user:"+core.UserID(ctx))
}
func (s *Service) FileReferencesAsPlugin(ctx context.Context, p PluginPrincipal, fileID string, in FileCatalogInput) (FileReferencesPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	installation, err := s.authorizePluginLocked(p)
	if err != nil {
		return FileReferencesPage{}, err
	}
	return s.fileReferencesLocked(p.ProjectID, fileID, in, &installation, catalogAuthority(installation))
}

// The cursor pins the immutable Event boundary and is bound to the endpoint,
// project, and current authority. Revalidate access on every request.
func (s *Service) catalogWindow(projectID, kind, authority string, in FileCatalogInput) (eventCursor, int, error) {
	p := s.data.Projects[projectID]
	latest := int64(0)
	if p != nil {
		latest = p.LatestSequence
	}
	if in.Limit < 0 || in.Limit > 100 || len(in.Cursor) > 2048 {
		return eventCursor{}, 0, v2err("invalid_input", "invalid catalog limit or cursor")
	}
	limit := in.Limit
	if limit == 0 {
		limit = 25
	}
	c := eventCursor{ProjectID: projectID, Snapshot: latest, QueryHash: fmt.Sprintf("%x", sha256.Sum256([]byte("file-catalog-v1:"+kind+":"+authority)))}
	if in.ThroughSequence != nil {
		c.Snapshot = *in.ThroughSequence
	}
	if in.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		if err != nil {
			return c, 0, v2err("invalid_input", "invalid catalog cursor")
		}
		var prior eventCursor
		if json.Unmarshal(raw, &prior) != nil || prior.ProjectID != projectID || prior.QueryHash != c.QueryHash || !s.validCursor(prior) || prior.After < 0 || in.ThroughSequence != nil && prior.Snapshot != *in.ThroughSequence {
			return c, 0, v2err("invalid_input", "invalid catalog cursor")
		}
		c = prior
	}
	if c.Snapshot < 0 || c.Snapshot > latest {
		return c, 0, v2err("invalid_input", "catalog boundary exceeds project history")
	}
	return c, limit, nil
}
func (s *Service) catalogNext(c eventCursor, after int) string {
	c.After = int64(after)
	c.Signature = s.signCursor(c)
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func (s *Service) listFilesLocked(projectID string, in FileCatalogInput, p *Installation, authority string) (FileCatalogPage, error) {
	c, limit, err := s.catalogWindow(projectID, "files", authority, in)
	if err != nil {
		return FileCatalogPage{}, err
	}
	entries, _ := catalogEntries(s.data.Projects[projectID], c.Snapshot, p)
	out := FileCatalogPage{ProjectID: projectID, ThroughSequence: c.Snapshot, Files: []FileCatalogEntry{}}
	if c.After > int64(len(entries)) {
		return out, v2err("invalid_input", "catalog cursor offset is invalid")
	}
	end := min(int(c.After)+limit, len(entries))
	out.Files = entries[int(c.After):end]
	if end < len(entries) {
		out.NextCursor = s.catalogNext(c, end)
	}
	return out, nil
}
func (s *Service) fileMetadataLocked(projectID, fileID string, through *int64, p *Installation) (FileCatalogEntry, error) {
	c, _, err := s.catalogWindow(projectID, "metadata", "", FileCatalogInput{ThroughSequence: through})
	if err != nil {
		return FileCatalogEntry{}, err
	}
	entries, _ := catalogEntries(s.data.Projects[projectID], c.Snapshot, p)
	for _, f := range entries {
		if f.ID == fileID {
			return f, nil
		}
	}
	return FileCatalogEntry{}, core.ErrNotFound
}
func (s *Service) fileReferencesLocked(projectID, fileID string, in FileCatalogInput, p *Installation, authority string) (FileReferencesPage, error) {
	c, limit, err := s.catalogWindow(projectID, "references:"+fileID, authority, in)
	if err != nil {
		return FileReferencesPage{}, err
	}
	_, refs := catalogEntries(s.data.Projects[projectID], c.Snapshot, p)
	all, ok := refs[fileID]
	if !ok {
		return FileReferencesPage{}, core.ErrNotFound
	}
	out := FileReferencesPage{ProjectID: projectID, FileID: fileID, ThroughSequence: c.Snapshot, References: []FileReference{}}
	if c.After > int64(len(all)) {
		return out, v2err("invalid_input", "reference cursor offset is invalid")
	}
	end := min(int(c.After)+limit, len(all))
	out.References = all[int(c.After):end]
	if end < len(all) {
		out.NextCursor = s.catalogNext(c, end)
	}
	return out, nil
}

func catalogEntries(p *projectData, through int64, grant *Installation) ([]FileCatalogEntry, map[string][]FileReference) {
	out := []FileCatalogEntry{}
	refs := map[string][]FileReference{}
	if p == nil {
		return out, refs
	}
	readable := func(e Event) bool {
		return e.Sequence <= through && (grant == nil || contains(grant.Permissions.ReadEvents, e.Type))
	}
	sources := map[string]string{}
	for _, e := range p.Events {
		if readable(e) && e.Content.Kind == "file" {
			if _, ok := p.Files[e.Content.FileID]; ok {
				sources[e.ID] = e.Content.FileID
				refs[e.Content.FileID] = append(refs[e.Content.FileID], FileReference{EventID: e.ID, Sequence: e.Sequence, Type: e.Type, Relation: "attachment"})
			}
		}
	}
	for _, e := range p.Events {
		if !readable(e) || e.Type != "derived" {
			continue
		}
		seen := map[string]bool{}
		for _, r := range e.Refs {
			fileID := sources[r.ID]
			if r.Rel == "derived_from" && fileID != "" && !seen[fileID] {
				seen[fileID] = true
				refs[fileID] = append(refs[fileID], FileReference{EventID: e.ID, Sequence: e.Sequence, Type: e.Type, Relation: "derived_from"})
			}
		}
	}
	for fileID, associations := range refs {
		sort.Slice(associations, func(i, j int) bool {
			if associations[i].Sequence != associations[j].Sequence {
				return associations[i].Sequence < associations[j].Sequence
			}
			return associations[i].Relation < associations[j].Relation
		})
		preview := append([]FileReference{}, associations[max(0, len(associations)-5):]...)
		out = append(out, FileCatalogEntry{FileInfo: p.Files[fileID], ReferenceCount: len(associations), References: preview, ReferencesComplete: len(associations) <= 5})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, refs
}
