package v2

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"

	"event-driven-context/internal/core"
)

func validateManifest(m Manifest) (Manifest, error) {
	m = cloneManifest(m)
	m.ID = strings.TrimSpace(m.ID)
	m.Version = strings.TrimSpace(m.Version)
	m.Name = strings.TrimSpace(m.Name)
	if !validPluginID(m.ID) || m.Version == "" || len(m.Version) > 64 || m.Name == "" || len(m.Name) > 200 {
		return m, v2err("invalid_input", "invalid plugin manifest identity")
	}
	var err error
	if m.Processor, err = canonicalJSON(m.Processor); err != nil {
		return m, err
	}
	if m.Config, err = canonicalJSON(m.Config); err != nil {
		return m, err
	}
	if len(m.Processor) > MaxStateBytes || len(m.Config) > MaxStateBytes {
		return m, v2err("too_large", "plugin manifest data too large")
	}
	configFields := make(map[string]bool, len(m.ConfigFields))
	for i := range m.ConfigFields {
		field := &m.ConfigFields[i]
		field.Key = strings.TrimSpace(field.Key)
		field.Type = strings.TrimSpace(field.Type)
		field.Description = strings.TrimSpace(field.Description)
		if field.Key == "" || len(field.Key) > 64 || field.Type == "" || len(field.Type) > 64 || field.Description == "" || len(field.Description) > 1000 || configFields[field.Key] {
			return m, v2err("invalid_input", "invalid plugin config field documentation")
		}
		configFields[field.Key] = true
	}
	for _, t := range append(append([]string{}, m.Permissions.ReadEvents...), m.Permissions.WriteEvents...) {
		if t != "log" && t != "note" && t != "derived" {
			return m, v2err("invalid_input", "invalid event permission")
		}
	}
	for _, t := range m.Permissions.WriteEvents {
		if t != "derived" {
			return m, v2err("invalid_input", "plugins may only write derived events")
		}
	}
	for _, name := range m.Permissions.WriteState {
		if !validStateNamePattern(name) {
			return m, v2err("invalid_input", "invalid state permission")
		}
	}
	for _, d := range m.State {
		if !validStateNamePattern(d.Key) {
			return m, v2err("invalid_input", "invalid state declaration")
		}
	}
	for _, name := range m.SessionContext {
		if !validStateNamePattern(name) || privateStateName(name) {
			return m, v2err("invalid_input", "invalid session context state")
		}
	}
	return m, nil
}

func (s *Service) InstallPlugin(ctx context.Context, projectID string, in InstallPluginInput) (InstallPluginResult, error) {
	if err := s.identity.RequireProjectOwner(ctx, projectID); err != nil {
		return InstallPluginResult{}, err
	}
	u, err := s.identity.Me(ctx)
	if err != nil {
		return InstallPluginResult{}, err
	}
	m, err := validateManifest(in.Manifest)
	if err != nil {
		return InstallPluginResult{}, err
	}
	config, err := canonicalJSON(in.Config)
	if err != nil {
		return InstallPluginResult{}, err
	}
	if len(config) == 0 {
		config = m.Config
	}
	if len(config) == 0 {
		config = json.RawMessage(`{}`)
	}
	if len(config) > MaxStateBytes {
		return InstallPluginResult{}, v2err("too_large", "plugin config too large")
	}
	var tokenBytes [32]byte
	if _, err = rand.Read(tokenBytes[:]); err != nil {
		return InstallPluginResult{}, err
	}
	token := "edcp_" + base64.RawURLEncoding.EncodeToString(tokenBytes[:])
	id, err := randomID("pin_")
	if err != nil {
		return InstallPluginResult{}, err
	}
	now := nowUTC()
	installation := Installation{ID: id, ProjectID: projectID, PluginID: m.ID, PluginVersion: m.Version, Manifest: m, ManagerUserID: u.ID, Status: "active", ConfigRevision: 1, Config: config, ConfigRevisions: []PluginConfigRevision{{Revision: 1, Config: config, CreatedAt: now, CreatedByUserID: u.ID}}, Permissions: m.Permissions, CreatedAt: now, UpdatedAt: now}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.projectLocked(projectID)
	if old, ok := p.Installations[m.ID]; ok && old.Status != "removed" {
		return InstallPluginResult{}, v2err("conflict", "plugin is already installed")
	}
	candidate, err := cloneSnapshot(s.data)
	if err != nil {
		return InstallPluginResult{}, err
	}
	cp := candidate.Projects[projectID]
	if cp == nil {
		cp = &projectData{}
		normalizeProjectData(cp)
		candidate.Projects[projectID] = cp
	}
	cp.Installations[m.ID] = cloneInstallation(installation)
	candidate.TokenHashes[tokenDigest(token)] = id
	if err = s.persistSnapshotLocked(candidate); err != nil {
		return InstallPluginResult{}, err
	}
	s.data = candidate
	return InstallPluginResult{cloneInstallation(installation), token}, nil
}

// EnsureBuiltinPlugin authorizes an active server-managed plugin, installing it
// on first use when the caller owns the project. Built-ins do not receive a
// bearer token: only code running inside this server can obtain their principal.
func (s *Service) EnsureBuiltinPlugin(ctx context.Context, projectID string, manifest Manifest, config json.RawMessage) (PluginPrincipal, Installation, error) {
	if err := s.identity.RequireProjectMember(ctx, projectID); err != nil {
		return PluginPrincipal{}, Installation{}, err
	}
	manifest, err := validateManifest(manifest)
	if err != nil {
		return PluginPrincipal{}, Installation{}, err
	}
	config, err = canonicalJSON(config)
	if err != nil {
		return PluginPrincipal{}, Installation{}, err
	}
	if len(config) == 0 {
		config = manifest.Config
	}
	if len(config) == 0 {
		config = json.RawMessage(`{}`)
	}
	if len(config) > MaxStateBytes {
		return PluginPrincipal{}, Installation{}, v2err("too_large", "plugin config too large")
	}

	s.mu.Lock()
	p := s.data.Projects[projectID]
	if p != nil {
		if current, ok := p.Installations[manifest.ID]; ok && current.Status != "removed" {
			s.mu.Unlock()
			if current.Status == "paused" {
				return PluginPrincipal{}, Installation{}, v2err("plugin_paused", "plugin is paused")
			}
			if current.PluginVersion != manifest.Version || !jsonEqual(current.Manifest.Processor, manifest.Processor) {
				return PluginPrincipal{}, Installation{}, v2err("conflict", "plugin id is already installed by a different processor")
			}
			current = cloneInstallation(current)
			return PluginPrincipal{InstallationID: current.ID, ProjectID: projectID, PluginID: current.PluginID, Revision: current.ConfigRevision}, current, nil
		}
	}
	s.mu.Unlock()

	if err = s.identity.RequireProjectOwner(ctx, projectID); err != nil {
		return PluginPrincipal{}, Installation{}, err
	}
	user, err := s.identity.Me(ctx)
	if err != nil {
		return PluginPrincipal{}, Installation{}, err
	}
	id, err := randomID("pin_")
	if err != nil {
		return PluginPrincipal{}, Installation{}, err
	}
	now := nowUTC()
	installation := Installation{ID: id, ProjectID: projectID, PluginID: manifest.ID, PluginVersion: manifest.Version, Manifest: manifest, ManagerUserID: user.ID, Status: "active", ConfigRevision: 1, Config: config, ConfigRevisions: []PluginConfigRevision{{Revision: 1, Config: config, CreatedAt: now, CreatedByUserID: user.ID}}, Permissions: manifest.Permissions, CreatedAt: now, UpdatedAt: now}

	s.mu.Lock()
	defer s.mu.Unlock()
	p = s.projectLocked(projectID)
	if current, ok := p.Installations[manifest.ID]; ok && current.Status != "removed" {
		if current.Status == "paused" {
			return PluginPrincipal{}, Installation{}, v2err("plugin_paused", "plugin is paused")
		}
		if current.PluginVersion != manifest.Version || !jsonEqual(current.Manifest.Processor, manifest.Processor) {
			return PluginPrincipal{}, Installation{}, v2err("conflict", "plugin id is already installed by a different processor")
		}
		current = cloneInstallation(current)
		return PluginPrincipal{InstallationID: current.ID, ProjectID: projectID, PluginID: current.PluginID, Revision: current.ConfigRevision}, current, nil
	}
	candidate, err := cloneSnapshot(s.data)
	if err != nil {
		return PluginPrincipal{}, Installation{}, err
	}
	cp := candidate.Projects[projectID]
	if cp == nil {
		cp = &projectData{}
		normalizeProjectData(cp)
		candidate.Projects[projectID] = cp
	}
	cp.Installations[manifest.ID] = cloneInstallation(installation)
	if err = s.persistSnapshotLocked(candidate); err != nil {
		return PluginPrincipal{}, Installation{}, err
	}
	s.data = candidate
	return PluginPrincipal{InstallationID: installation.ID, ProjectID: projectID, PluginID: installation.PluginID, Revision: installation.ConfigRevision}, cloneInstallation(installation), nil
}

func (s *Service) ListPlugins(ctx context.Context, projectID string) ([]Installation, error) {
	if err := s.identity.RequireProjectMember(ctx, projectID); err != nil {
		return nil, err
	}
	timezone, err := s.identity.ProjectTimezone(ctx, projectID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.data.Projects[projectID]
	if p == nil {
		return []Installation{}, nil
	}
	installations := sortedInstallations(p)
	for i := range installations {
		installations[i].ProjectTimezone = timezone
	}
	return installations, nil
}

func (s *Service) managedPlugin(ctx context.Context, projectID, pluginID string) (Installation, error) {
	if err := s.identity.RequireProjectMember(ctx, projectID); err != nil {
		return Installation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.data.Projects[projectID]
	if p == nil {
		return Installation{}, core.ErrNotFound
	}
	in, ok := p.Installations[pluginID]
	if !ok || in.Status == "removed" {
		return Installation{}, core.ErrNotFound
	}
	if in.ManagerUserID != core.UserID(ctx) {
		return Installation{}, core.ErrNotFound
	}
	return cloneInstallation(in), nil
}

func (s *Service) RevisePlugin(ctx context.Context, projectID, pluginID string, input RevisePluginInput) (Installation, error) {
	if _, err := s.managedPlugin(ctx, projectID, pluginID); err != nil {
		return Installation{}, err
	}
	config, err := canonicalJSON(input.Config)
	if err != nil {
		return Installation{}, err
	}
	if len(config) > MaxStateBytes {
		return Installation{}, v2err("too_large", "plugin config too large")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate, err := cloneSnapshot(s.data)
	if err != nil {
		return Installation{}, err
	}
	in := candidate.Projects[projectID].Installations[pluginID]
	if in.Status == "removed" || in.ManagerUserID != core.UserID(ctx) {
		return Installation{}, core.ErrNotFound
	}
	if input.ExpectedRevision != in.ConfigRevision {
		return Installation{}, v2err("conflict", "plugin config revision mismatch")
	}
	in.ConfigRevision++
	in.Config = config
	in.UpdatedAt = nowUTC()
	in.ConfigRevisions = append(in.ConfigRevisions, PluginConfigRevision{Revision: in.ConfigRevision, Config: config, CreatedAt: in.UpdatedAt, CreatedByUserID: core.UserID(ctx)})
	candidate.Projects[projectID].Installations[pluginID] = in
	if err = s.persistSnapshotLocked(candidate); err != nil {
		return Installation{}, err
	}
	s.data = candidate
	return cloneInstallation(in), nil
}

func (s *Service) SetPluginStatus(ctx context.Context, projectID, pluginID, status string) (Installation, error) {
	if status != "active" && status != "paused" {
		return Installation{}, v2err("invalid_input", "status must be active or paused")
	}
	if _, err := s.managedPlugin(ctx, projectID, pluginID); err != nil {
		return Installation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate, err := cloneSnapshot(s.data)
	if err != nil {
		return Installation{}, err
	}
	in := candidate.Projects[projectID].Installations[pluginID]
	if in.ManagerUserID != core.UserID(ctx) || in.Status == "removed" {
		return Installation{}, core.ErrNotFound
	}
	in.Status = status
	in.UpdatedAt = nowUTC()
	candidate.Projects[projectID].Installations[pluginID] = in
	if err = s.persistSnapshotLocked(candidate); err != nil {
		return Installation{}, err
	}
	s.data = candidate
	return cloneInstallation(in), nil
}

func (s *Service) RemovePlugin(ctx context.Context, projectID, pluginID string) (Installation, error) {
	if _, err := s.managedPlugin(ctx, projectID, pluginID); err != nil {
		return Installation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate, err := cloneSnapshot(s.data)
	if err != nil {
		return Installation{}, err
	}
	in := candidate.Projects[projectID].Installations[pluginID]
	if in.ManagerUserID != core.UserID(ctx) || in.Status == "removed" {
		return Installation{}, core.ErrNotFound
	}
	in.Status = "removed"
	in.UpdatedAt = nowUTC()
	candidate.Projects[projectID].Installations[pluginID] = in
	for hash, id := range candidate.TokenHashes {
		if id == in.ID {
			delete(candidate.TokenHashes, hash)
		}
	}
	if err = s.persistSnapshotLocked(candidate); err != nil {
		return Installation{}, err
	}
	s.data = candidate
	return cloneInstallation(in), nil
}

func (s *Service) AuthenticatePlugin(token string) (PluginPrincipal, error) {
	if len(token) < 16 || len(token) > 256 {
		return PluginPrincipal{}, core.ErrUnauthenticated
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.data.TokenHashes[tokenDigest(token)]
	if !ok {
		return PluginPrincipal{}, core.ErrUnauthenticated
	}
	for _, p := range s.data.Projects {
		for _, in := range p.Installations {
			if in.ID == id {
				if in.Status == "paused" {
					return PluginPrincipal{}, v2err("plugin_paused", "plugin is paused")
				}
				if in.Status != "active" {
					return PluginPrincipal{}, core.ErrUnauthenticated
				}
				return PluginPrincipal{in.ID, in.ProjectID, in.PluginID, in.ConfigRevision}, nil
			}
		}
	}
	return PluginPrincipal{}, core.ErrUnauthenticated
}

// GetPluginAsPlugin lets a processor refresh its own immutable manifest and
// current configuration without granting visibility into other installations.
func (s *Service) GetPluginAsPlugin(ctx context.Context, principal PluginPrincipal) (Installation, error) {
	s.mu.Lock()
	in, err := s.authorizePluginLocked(principal)
	s.mu.Unlock()
	if err != nil {
		return Installation{}, err
	}
	timezone, err := s.identity.ProjectTimezone(ctx, principal.ProjectID)
	if err != nil {
		return Installation{}, err
	}
	in = cloneInstallation(in)
	in.ProjectTimezone = timezone
	return in, nil
}

func (s *Service) authorizePluginLocked(principal PluginPrincipal) (Installation, error) {
	p := s.data.Projects[principal.ProjectID]
	if p == nil {
		return Installation{}, core.ErrUnauthenticated
	}
	in, ok := p.Installations[principal.PluginID]
	if !ok || in.ID != principal.InstallationID {
		return Installation{}, core.ErrUnauthenticated
	}
	if in.Status == "paused" {
		return Installation{}, v2err("plugin_paused", "plugin is paused")
	}
	if in.Status != "active" {
		return Installation{}, core.ErrUnauthenticated
	}
	return in, nil
}
