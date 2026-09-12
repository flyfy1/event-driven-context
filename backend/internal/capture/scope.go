package capture

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type captureScope struct {
	Version int    `json:"version"`
	Client  string `json:"client"`
	Enabled bool   `json:"enabled"`
}

func (m *Manager) captureScopePath(binding Binding, client string) string {
	return filepath.Join(m.root, "capture-scopes", scopeKey(binding)+"-"+client+".json")
}

func (m *Manager) captureEnabled(binding Binding, client string) bool {
	b, err := os.ReadFile(m.captureScopePath(binding, client))
	if err != nil {
		return false
	}
	var state captureScope
	return json.Unmarshal(b, &state) == nil && state.Version == 1 && state.Client == client && state.Enabled
}
