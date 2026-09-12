package buildinfo

import "testing"

func TestCurrentUsesEmbeddedValues(t *testing.T) {
	info := Current()
	if info.Version == "" || info.Commit == "" || info.BuiltAt == "" {
		t.Fatalf("incomplete build info: %#v", info)
	}
}
