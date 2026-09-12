package notes

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Caller has validated Export and holds the service lock. The metadata lives
// beside the exact immutable tree selected by the same publication pointer.
func (s Store) Checkpoint() (Checkpoint, error) {
	var out struct {
		Checkpoint Checkpoint `json:"checkpoint"`
	}
	target, err := os.Readlink(filepath.Join(s.Root, "notes"))
	if errors.Is(err, os.ErrNotExist) {
		return out.Checkpoint, nil
	}
	if err != nil {
		return out.Checkpoint, err
	}
	name := filepath.Join(s.Root, filepath.Dir(target), "publication.json")
	info, err := os.Lstat(name)
	if err != nil {
		return out.Checkpoint, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16384 {
		return out.Checkpoint, fmt.Errorf("invalid notes publication metadata")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return out.Checkpoint, err
	}
	err = json.Unmarshal(data, &out)
	return out.Checkpoint, err
}
