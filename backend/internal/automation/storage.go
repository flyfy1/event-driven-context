package automation

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"event-driven-context/internal/core"
)

const journalVersion = 1

type durableState struct {
	Sequence      uint64
	Runners       map[string]RegisteredRunner
	Installations map[string]Installation
	Runs          map[string]Run
	Inbox         map[string]InboxEntry
	Dedupe        map[string]string
}

type journalEntry struct {
	Version         int               `json:"version"`
	Sequence        uint64            `json:"sequence"`
	Timestamp       string            `json:"timestamp"`
	Action          string            `json:"action"`
	ActorType       string            `json:"actor_type"`
	ActorID         string            `json:"actor_id"`
	Runner          *RegisteredRunner `json:"runner,omitempty"`
	RunnerTokenHash string            `json:"runner_token_hash,omitempty"`
	Installation    *Installation     `json:"installation,omitempty"`
	Runs            []Run             `json:"runs,omitempty"`
	Inbox           *InboxEntry       `json:"inbox,omitempty"`
	DedupeKind      string            `json:"dedupe_kind,omitempty"`
	DedupeKey       string            `json:"dedupe_key,omitempty"`
	DedupeRunID     string            `json:"dedupe_run_id,omitempty"`
}

func newDurableState() durableState {
	return durableState{
		Runners: map[string]RegisteredRunner{}, Installations: map[string]Installation{},
		Runs: map[string]Run{}, Inbox: map[string]InboxEntry{}, Dedupe: map[string]string{},
	}
}

func (s *durableState) apply(entry journalEntry) {
	s.Sequence = entry.Sequence
	if entry.Runner != nil {
		r := *entry.Runner
		if entry.RunnerTokenHash != "" {
			r.TokenHash = entry.RunnerTokenHash
		} else if prior, ok := s.Runners[r.ID]; ok {
			r.TokenHash = prior.TokenHash
		}
		s.Runners[r.ID] = r
	}
	if entry.Installation != nil {
		s.Installations[entry.Installation.ID] = cloneInstallation(*entry.Installation)
	}
	for _, run := range entry.Runs {
		s.Runs[run.ID] = cloneRun(run)
	}
	if entry.Inbox != nil {
		s.Inbox[entry.Inbox.ID] = cloneInbox(*entry.Inbox)
	}
	if entry.DedupeKind != "" {
		s.Dedupe[entry.DedupeKind+"\x00"+entry.DedupeKey] = entry.DedupeRunID
	}
}

func cloneInstallation(in Installation) Installation {
	out := in
	out.Revisions = append([]InstallationRevision(nil), in.Revisions...)
	for i := range out.Revisions {
		if in.Revisions[i].Trigger.MetadataEquals != nil {
			out.Revisions[i].Trigger.MetadataEquals = make(map[string]json.RawMessage, len(in.Revisions[i].Trigger.MetadataEquals))
			for key, value := range in.Revisions[i].Trigger.MetadataEquals {
				out.Revisions[i].Trigger.MetadataEquals[key] = append(json.RawMessage(nil), value...)
			}
		}
	}
	return out
}

func cloneRun(in Run) Run {
	out := in
	out.Inputs = append([]RunInput(nil), in.Inputs...)
	out.SupersedesEventIDs = append([]string(nil), in.SupersedesEventIDs...)
	out.OutputEventIDs = append([]string(nil), in.OutputEventIDs...)
	if in.CurrentAttempt != nil {
		attempt := *in.CurrentAttempt
		out.CurrentAttempt = &attempt
	}
	return out
}

type failureHooks struct {
	AfterCandidate bool
	AfterEvent     bool
	AfterCommit    bool
}

type Option func(*Coordinator)

func WithClock(clock Clock) Option { return func(c *Coordinator) { c.clock = clock } }

type Coordinator struct {
	mu            sync.Mutex
	store         *core.Store
	dataDir       string
	clock         Clock
	leaseDuration time.Duration
	maxRunTime    time.Duration
	skills        map[string]SkillDescriptor
	state         durableState
	poisoned      error
	hooks         failureHooks
}

func New(store *core.Store, dataDir string, skills []SkillDescriptor, options ...Option) (*Coordinator, error) {
	if store == nil || strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("automation requires store and data directory")
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	c := &Coordinator{
		store: store, dataDir: abs, clock: time.Now, leaseDuration: 120 * time.Second,
		maxRunTime: 10 * time.Minute, skills: map[string]SkillDescriptor{}, state: newDurableState(),
	}
	for _, option := range options {
		option(c)
	}
	for _, skill := range skills {
		if (skill.ID != AudioTranscribeSkill && skill.ID != DailyReviewSkill) || skill.Version != FixedSkillVersion || len(skill.Digest) != 64 {
			return nil, fmt.Errorf("invalid fixed skill descriptor")
		}
		c.skills[skill.ID] = skill
	}
	if len(c.skills) != 2 {
		return nil, fmt.Errorf("both fixed P1 skills are required")
	}
	state, err := loadJournal(abs)
	if err != nil {
		return nil, err
	}
	c.state = state
	if err = c.recoverLocked(); err != nil {
		return nil, err
	}
	return c, nil
}

func loadJournal(dataDir string) (durableState, error) {
	state := newDurableState()
	dir := filepath.Join(dataDir, "automation", "journal")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	names := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	for i, name := range names {
		want := uint64(i + 1)
		if name != fmt.Sprintf("%020d.json", want) {
			return state, fmt.Errorf("automation journal gap or unexpected file %q", name)
		}
		data, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil {
			return state, readErr
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		var item journalEntry
		if decodeErr := decoder.Decode(&item); decodeErr != nil {
			return state, fmt.Errorf("decode automation journal %s: %w", name, decodeErr)
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF || item.Version != journalVersion || item.Sequence != want {
			return state, fmt.Errorf("invalid automation journal %s", name)
		}
		state.apply(item)
	}
	return state, nil
}

func (c *Coordinator) appendJournal(entry journalEntry) error {
	if c.poisoned != nil {
		return fmt.Errorf("automation coordinator requires restart: %w", c.poisoned)
	}
	entry.Version = journalVersion
	entry.Sequence = c.state.Sequence + 1
	entry.Timestamp = c.nowString()
	data, err := json.Marshal(entry)
	if err == nil {
		err = writeOnce(c.journalPath(entry.Sequence), data)
	}
	if err != nil {
		c.poisoned = err
		return err
	}
	c.state.apply(entry)
	return nil
}

func (c *Coordinator) journalPath(sequence uint64) string {
	return filepath.Join(c.dataDir, "automation", "journal", fmt.Sprintf("%020d.json", sequence))
}

func (c *Coordinator) runnerPath(id string) string {
	return filepath.Join(c.dataDir, "automation", "runners", id+".json")
}

func (c *Coordinator) revisionPath(installation Installation, revision InstallationRevision) string {
	return filepath.Join(c.dataDir, "projects", installation.ProjectID, "automation", "installations", installation.ID, "revisions", revision.ID+".json")
}

func (c *Coordinator) runRequestPath(run Run) string {
	return filepath.Join(c.dataDir, "projects", run.ProjectID, "automation", "runs", run.ID, "request.json")
}

func (c *Coordinator) candidatePath(run Run, attemptID string) string {
	return filepath.Join(c.dataDir, "projects", run.ProjectID, "automation", "runs", run.ID, "attempts", attemptID, "candidate.json")
}

func (c *Coordinator) commitPath(run Run) string {
	return filepath.Join(c.dataDir, "projects", run.ProjectID, "automation", "runs", run.ID, "commit.json")
}

func (c *Coordinator) inboxPath(entry InboxEntry) string {
	return filepath.Join(c.dataDir, "users", entry.RecipientUserID, "inbox", entry.ID+".json")
}

func writeJSONOnce(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeOnce(path, data)
}

func writeOnce(path string, data []byte) error {
	if existing, err := os.ReadFile(path); err == nil {
		if bytes.Equal(existing, data) {
			return nil
		}
		return core.ErrConflict
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Link(tmp.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func newID(prefix string) string {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(data)
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func deterministicID(prefix string, values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return prefix + "_" + hex.EncodeToString(sum[:12])
}

func (c *Coordinator) now() time.Time    { return c.clock().UTC() }
func (c *Coordinator) nowString() string { return c.now().Format(time.RFC3339Nano) }

func parseStoredTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func safeShort(value string, max int) bool {
	return value != "" && len(value) <= max && !strings.ContainsAny(value, "/\\\x00\r\n")
}

func validAttemptID(value string) bool {
	if len(value) != len("att_")+32 || !strings.HasPrefix(value, "att_") {
		return false
	}
	for _, character := range value[len("att_"):] {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func dedupeKey(kind, key string) string { return kind + "\x00" + key }

func parseFence(value string) (uint64, error) { return strconv.ParseUint(value, 10, 64) }

// Close is intentionally idempotent. Every transition is synchronously
// fsynced, so there is no buffered writer to flush during shutdown.
func (c *Coordinator) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.poisoned
}
