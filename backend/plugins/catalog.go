package plugins

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"

	"event-driven-context/internal/v2"
)

// manifestFiles keeps the repository-provided plugin manifests inside the
// server binary, so every deployment exposes the exact catalog it can install.
//
//go:embed */manifest.json
var manifestFiles embed.FS

var systemCatalog = mustLoadCatalog()

// Catalog returns the trusted plugins shipped with this server build.
func Catalog() []v2.Manifest {
	out := make([]v2.Manifest, len(systemCatalog))
	copy(out, systemCatalog)
	return out
}

// Lookup returns a repository-provided plugin by its stable id.
func Lookup(id string) (v2.Manifest, bool) {
	for _, manifest := range systemCatalog {
		if manifest.ID == id {
			return manifest, true
		}
	}
	return v2.Manifest{}, false
}

func mustLoadCatalog() []v2.Manifest {
	paths, err := fs.Glob(manifestFiles, "*/manifest.json")
	if err != nil {
		panic(fmt.Sprintf("load system plugin catalog: %v", err))
	}
	seen := make(map[string]bool, len(paths))
	catalog := make([]v2.Manifest, 0, len(paths))
	for _, path := range paths {
		raw, readErr := manifestFiles.ReadFile(path)
		if readErr != nil {
			panic(fmt.Sprintf("read system plugin manifest %s: %v", path, readErr))
		}
		var manifest v2.Manifest
		if decodeErr := json.Unmarshal(raw, &manifest); decodeErr != nil {
			panic(fmt.Sprintf("decode system plugin manifest %s: %v", path, decodeErr))
		}
		if manifest.ID == "" || manifest.Version == "" || manifest.Name == "" || seen[manifest.ID] {
			panic(fmt.Sprintf("invalid or duplicate system plugin manifest %s", path))
		}
		seen[manifest.ID] = true
		catalog = append(catalog, manifest)
	}
	sort.Slice(catalog, func(i, j int) bool {
		if catalog[i].Name == catalog[j].Name {
			return catalog[i].ID < catalog[j].ID
		}
		return catalog[i].Name < catalog[j].Name
	})
	return catalog
}
