package v2

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"event-driven-context/internal/core"
	"golang.org/x/sys/unix"
)

const snapshotVersion = 1

type projectData struct {
	LatestSequence int64                       `json:"latest_sequence"`
	Events         []Event                     `json:"events"`
	Files          map[string]FileInfo         `json:"files"`
	States         map[string][]State          `json:"states"`
	Installations  map[string]Installation     `json:"installations"`
	ManualRuns     map[string]ManualRunRequest `json:"manual_runs"`
}

type snapshot struct {
	Version     int                     `json:"version"`
	CursorKey   string                  `json:"cursor_key"`
	Projects    map[string]*projectData `json:"projects"`
	TokenHashes map[string]string       `json:"token_hashes"` // sha256 -> installation id
}

type Service struct {
	identity *core.Store
	root     string
	mu       sync.Mutex
	data     snapshot
	lockFile *os.File
}

var _ ServiceAPI = (*Service)(nil)

func New(identity *core.Store, dataDir string) (*Service, error) {
	if identity == nil || strings.TrimSpace(dataDir) == "" || dataDir == ":memory:" {
		return nil, fmt.Errorf("v2 requires identity store and persistent data directory")
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(abs, "v2")
	if err = os.MkdirAll(filepath.Join(root, "files"), 0700); err != nil {
		return nil, err
	}
	if err = os.Chmod(root, 0700); err != nil {
		return nil, err
	}
	if err = os.Chmod(filepath.Join(root, "files"), 0700); err != nil {
		return nil, err
	}
	lockFile, err := acquireWriterLock(root)
	if err != nil {
		return nil, err
	}
	keepLock := false
	defer func() {
		if !keepLock {
			_ = unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
			_ = lockFile.Close()
		}
	}()
	cursorKey, err := randomID("")
	if err != nil {
		return nil, err
	}
	s := &Service{identity: identity, root: root, lockFile: lockFile, data: snapshot{Version: snapshotVersion, CursorKey: cursorKey, Projects: map[string]*projectData{}, TokenHashes: map[string]string{}}}
	raw, err := os.ReadFile(filepath.Join(root, "index.json"))
	if errors.Is(err, os.ErrNotExist) {
		if err = s.persistLocked(); err != nil {
			return nil, err
		}
		keepLock = true
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &s.data); err != nil {
		return nil, fmt.Errorf("read v2 index: %w", err)
	}
	if s.data.Version != snapshotVersion || s.data.CursorKey == "" || s.data.Projects == nil || s.data.TokenHashes == nil {
		return nil, fmt.Errorf("unsupported v2 index")
	}
	for _, p := range s.data.Projects {
		normalizeProjectData(p)
	}
	keepLock = true
	return s, nil
}

func acquireWriterLock(root string) (*os.File, error) {
	path := filepath.Join(root, ".writer.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("v2 data directory is already open by another service: %w", err)
	}
	return f, nil
}

// Close releases the single-writer lock. It is safe to call more than once.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lockFile == nil {
		return nil
	}
	f := s.lockFile
	s.lockFile = nil
	unlockErr := unix.Flock(int(f.Fd()), unix.LOCK_UN)
	closeErr := f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

func normalizeProjectData(p *projectData) {
	if p.Files == nil {
		p.Files = map[string]FileInfo{}
	}
	if p.States == nil {
		p.States = map[string][]State{}
	}
	if p.Installations == nil {
		p.Installations = map[string]Installation{}
	}
	if p.ManualRuns == nil {
		p.ManualRuns = map[string]ManualRunRequest{}
	}
	if p.Events == nil {
		p.Events = []Event{}
	}
}

func (s *Service) projectLocked(id string) *projectData {
	p := s.data.Projects[id]
	if p == nil {
		p = &projectData{}
		normalizeProjectData(p)
		s.data.Projects[id] = p
	}
	return p
}

func (s *Service) persistLocked() error {
	return s.persistSnapshotLocked(s.data)
}

func (s *Service) persistSnapshotLocked(candidate snapshot) error {
	if s.lockFile == nil {
		return errors.New("v2 service is closed")
	}
	raw, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.root, "index.json"), raw, 0600)
}

func cloneRawMessage(in json.RawMessage) json.RawMessage {
	if in == nil {
		return nil
	}
	return bytes.Clone(in)
}

func cloneRawMap(in map[string]json.RawMessage) map[string]json.RawMessage {
	if in == nil {
		return nil
	}
	out := make(map[string]json.RawMessage, len(in))
	for key, value := range in {
		out[key] = cloneRawMessage(value)
	}
	return out
}

func cloneEvent(in Event) Event {
	in.Metadata = cloneRawMap(in.Metadata)
	in.Source = cloneRawMap(in.Source)
	in.Refs = append([]Ref(nil), in.Refs...)
	return in
}

func cloneState(in State) State {
	in.Data = cloneRawMessage(in.Data)
	in.Refs = append([]string(nil), in.Refs...)
	return in
}

func clonePermissions(in Permissions) Permissions {
	in.ReadEvents = append([]string(nil), in.ReadEvents...)
	in.WriteEvents = append([]string(nil), in.WriteEvents...)
	in.WriteState = append([]string(nil), in.WriteState...)
	return in
}

func cloneManifest(in Manifest) Manifest {
	in.Skills = append([]string(nil), in.Skills...)
	in.State = append([]StateDeclaration(nil), in.State...)
	in.SessionContext = append([]string(nil), in.SessionContext...)
	in.Processor = cloneRawMessage(in.Processor)
	in.Config = cloneRawMessage(in.Config)
	in.Permissions = clonePermissions(in.Permissions)
	return in
}

func cloneInstallation(in Installation) Installation {
	in.Manifest = cloneManifest(in.Manifest)
	in.Config = cloneRawMessage(in.Config)
	in.Permissions = clonePermissions(in.Permissions)
	in.ConfigRevisions = append([]PluginConfigRevision(nil), in.ConfigRevisions...)
	for i := range in.ConfigRevisions {
		in.ConfigRevisions[i].Config = cloneRawMessage(in.ConfigRevisions[i].Config)
	}
	return in
}

func cloneManualRun(in ManualRunRequest) ManualRunRequest {
	in.SourceEventIDs = append([]string(nil), in.SourceEventIDs...)
	return in
}

func cloneSnapshot(in snapshot) (snapshot, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return snapshot{}, err
	}
	var out snapshot
	if err = json.Unmarshal(raw, &out); err != nil {
		return snapshot{}, err
	}
	for _, p := range out.Projects {
		normalizeProjectData(p)
	}
	return out, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	closeErr := d.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func tokenDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func randomID(prefix string) (string, error) {
	var b [18]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b[:]), nil
}

func pluginKey(projectID, pluginID string) string { return projectID + "\x00" + pluginID }

func sortedInstallations(p *projectData) []Installation {
	out := make([]Installation, 0, len(p.Installations))
	for _, v := range p.Installations {
		if v.Status != "removed" {
			out = append(out, cloneInstallation(v))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PluginID < out[j].PluginID })
	return out
}
