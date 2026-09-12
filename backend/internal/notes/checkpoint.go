package notes

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type publicationMetadata struct {
	Revision   int64      `json:"revision"`
	Checkpoint Checkpoint `json:"checkpoint"`
}

func readPublication(name string) (publicationMetadata, error) {
	var out publicationMetadata
	info, err := os.Lstat(name)
	if err != nil {
		return out, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16384 {
		return out, fmt.Errorf("invalid notes publication metadata")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	if out.Checkpoint.AfterSequence < 0 || out.Checkpoint.AttachmentBackfillThroughSequence < 0 || out.Checkpoint.AttachmentBackfillThroughSequence > out.Checkpoint.AfterSequence {
		return out, fmt.Errorf("invalid notes checkpoint")
	}
	return out, nil
}

// Caller validates Export and holds the service writer lock.
func (s Store) Checkpoint() (Checkpoint, error) {
	target, err := os.Readlink(filepath.Join(s.Root, "notes"))
	if errors.Is(err, os.ErrNotExist) {
		return Checkpoint{}, nil
	}
	if err != nil {
		return Checkpoint{}, err
	}
	out, err := readPublication(filepath.Join(s.Root, filepath.Dir(target), "publication.json"))
	return out.Checkpoint, err
}
