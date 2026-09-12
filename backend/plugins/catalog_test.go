package plugins

import "testing"

func TestCatalogListsEverySystemPluginInStableOrder(t *testing.T) {
	catalog := Catalog()
	if len(catalog) != 5 {
		t.Fatalf("catalog contains %d plugins, want 5", len(catalog))
	}
	seen := map[string]bool{}
	for i, manifest := range catalog {
		if manifest.ID == "" || manifest.Version == "" || manifest.Name == "" {
			t.Fatalf("invalid manifest at %d: %#v", i, manifest)
		}
		if seen[manifest.ID] {
			t.Fatalf("duplicate plugin id %q", manifest.ID)
		}
		seen[manifest.ID] = true
		if i > 0 && catalog[i-1].Name > manifest.Name {
			t.Fatalf("catalog is not sorted: %q before %q", catalog[i-1].Name, manifest.Name)
		}
	}
	for _, id := range []string{"audio-transcribe", "daily-review", "evidence", "notes-indexer", "project-brief"} {
		if _, ok := Lookup(id); !ok {
			t.Errorf("system plugin %q is missing", id)
		}
	}
	if _, ok := Lookup("not-a-system-plugin"); ok {
		t.Fatal("unknown plugin unexpectedly found")
	}
}
