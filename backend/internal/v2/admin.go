package v2

import "sort"

// AdminPlugin deliberately omits installation config and token material.
type AdminPlugin struct {
	InstallationID string      `json:"installation_id"`
	ProjectID      string      `json:"project_id"`
	PluginID       string      `json:"plugin_id"`
	Name           string      `json:"name"`
	Version        string      `json:"version"`
	Status         string      `json:"status"`
	ManagerUserID  string      `json:"manager_user_id"`
	Permissions    Permissions `json:"permissions"`
	CreatedAt      string      `json:"created_at"`
	UpdatedAt      string      `json:"updated_at"`
}

type AdminProjectStats struct {
	ProjectID      string        `json:"project_id"`
	LatestSequence int64         `json:"latest_sequence"`
	EventCount     int           `json:"event_count"`
	FileCount      int           `json:"file_count"`
	FileBytes      int64         `json:"file_bytes"`
	StateCount     int           `json:"state_count"`
	PluginCount    int           `json:"plugin_count"`
	ManualRunCount int           `json:"manual_run_count"`
	LastActivityAt string        `json:"last_activity_at,omitempty"`
	Plugins        []AdminPlugin `json:"plugins"`
}

// AdminProjectStats returns a read-only aggregate for projects whose IDs have
// already passed the administrator boundary in the API package.
func (s *Service) AdminProjectStats(projectIDs []string) []AdminProjectStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AdminProjectStats, 0, len(projectIDs))
	for _, projectID := range projectIDs {
		stats := AdminProjectStats{ProjectID: projectID, Plugins: []AdminPlugin{}}
		project := s.data.Projects[projectID]
		if project == nil {
			out = append(out, stats)
			continue
		}
		stats.LatestSequence = project.LatestSequence
		stats.EventCount = len(project.Events)
		stats.FileCount = len(project.Files)
		stats.StateCount = len(project.States)
		stats.ManualRunCount = len(project.ManualRuns)
		for _, file := range project.Files {
			stats.FileBytes += file.SizeBytes
			stats.LastActivityAt = laterTimestamp(stats.LastActivityAt, file.UploadedAt)
		}
		for _, event := range project.Events {
			stats.LastActivityAt = laterTimestamp(stats.LastActivityAt, event.RecordedAt)
		}
		for _, versions := range project.States {
			if len(versions) > 0 {
				stats.LastActivityAt = laterTimestamp(stats.LastActivityAt, versions[len(versions)-1].UpdatedAt)
			}
		}
		for _, installation := range project.Installations {
			if installation.Status == "removed" {
				continue
			}
			stats.Plugins = append(stats.Plugins, AdminPlugin{
				InstallationID: installation.ID,
				ProjectID:      projectID,
				PluginID:       installation.PluginID,
				Name:           installation.Manifest.Name,
				Version:        installation.PluginVersion,
				Status:         installation.Status,
				ManagerUserID:  installation.ManagerUserID,
				Permissions:    clonePermissions(installation.Permissions),
				CreatedAt:      installation.CreatedAt,
				UpdatedAt:      installation.UpdatedAt,
			})
			stats.LastActivityAt = laterTimestamp(stats.LastActivityAt, installation.UpdatedAt)
		}
		stats.PluginCount = len(stats.Plugins)
		sort.Slice(stats.Plugins, func(i, j int) bool { return stats.Plugins[i].PluginID < stats.Plugins[j].PluginID })
		out = append(out, stats)
	}
	return out
}

func laterTimestamp(current, candidate string) string {
	if candidate > current {
		return candidate
	}
	return current
}
