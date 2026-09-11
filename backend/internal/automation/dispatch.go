package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"event-driven-context/internal/core"
	"event-driven-context/internal/runner"
)

// Dispatch evaluates due triggers before leasing at most one run to the
// authenticated runner. A nil claim is the HTTP 204 case.
func (c *Coordinator) Dispatch(ctx context.Context, token string) (*Claim, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := authenticateRunner(c.state, token)
	if err != nil {
		return nil, err
	}
	if err = c.recoverLocked(); err != nil {
		return nil, err
	}
	if err = c.evaluateDueLocked(ctx); err != nil {
		return nil, err
	}
	now := c.now()
	changed := []Run{}
	for _, run := range c.state.Runs {
		if run.RunnerID != r.ID || terminal(run.Status) || run.Status == RunCandidateSaved || strings.HasPrefix(run.Status, "blocked_") {
			continue
		}
		if (run.Status == RunLeased || run.Status == RunRunning) && run.CurrentAttempt != nil {
			expires, parseErr := parseStoredTime(run.CurrentAttempt.LeaseExpiresAt)
			if parseErr != nil {
				return nil, parseErr
			}
			if now.Before(expires) {
				continue
			}
			if run.AttemptCount >= 3 {
				run.Status = RunFailed
			} else {
				run.Status = RunRetryWait
			}
			run.CurrentAttempt = nil
			run.NextAttemptAt = now.Format(time.RFC3339Nano)
			run.FailureKind = "temporary"
			run.FailureCode = "lease_expired"
			run.UpdatedAt = c.nowString()
			changed = append(changed, run)
		}
	}
	if len(changed) != 0 {
		if err = c.appendJournal(journalEntry{Action: "runs.leases_expired", ActorType: "system", ActorID: "dispatcher", Runs: changed}); err != nil {
			return nil, err
		}
	}
	var selected *Run
	for _, value := range c.state.Runs {
		run := value
		if run.RunnerID != r.ID || (run.Status != RunQueued && run.Status != RunRetryWait) {
			continue
		}
		if run.AttemptCount >= 3 {
			continue
		}
		if run.Status == RunRetryWait && run.NextAttemptAt != "" {
			due, parseErr := parseStoredTime(run.NextAttemptAt)
			if parseErr != nil {
				return nil, parseErr
			}
			if now.Before(due) {
				continue
			}
		}
		if selected == nil || run.CreatedAt < selected.CreatedAt || (run.CreatedAt == selected.CreatedAt && run.ID < selected.ID) {
			copy := cloneRun(run)
			selected = &copy
		}
	}
	if selected == nil {
		return nil, nil
	}
	installation, revision, err := c.authorizeRunLocked(ctx, r, *selected, true)
	if err != nil {
		return nil, err
	}
	selected.AttemptCount++
	selected.FencingCounter++
	attempt := Attempt{
		ID: newID("att"), FencingToken: selected.FencingCounter, StartedAt: c.nowString(),
		LeaseExpiresAt: now.Add(c.leaseDuration).Format(time.RFC3339Nano),
	}
	selected.CurrentAttempt = &attempt
	selected.Status = RunLeased
	selected.NextAttemptAt = ""
	selected.FailureKind = ""
	selected.FailureCode = ""
	selected.UpdatedAt = c.nowString()
	claim, err := c.buildClaimLocked(ctx, *selected, revision, attempt)
	if err != nil {
		return nil, err
	}
	if err = c.appendJournal(journalEntry{Action: "run.leased", ActorType: "runner", ActorID: r.ID, Runs: []Run{*selected}}); err != nil {
		return nil, err
	}
	_ = installation
	claim.Run = *selected
	return &claim, nil
}

func (c *Coordinator) Heartbeat(ctx context.Context, token, runID string, credential AttemptCredential) (Run, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := authenticateRunner(c.state, token)
	if err != nil {
		return Run{}, err
	}
	run, err := c.requireAttemptLocked(ctx, r, runID, credential)
	if err != nil {
		return Run{}, err
	}
	run = cloneRun(run)
	started, _ := parseStoredTime(run.CurrentAttempt.StartedAt)
	ceiling := started.Add(c.maxRunTime)
	expires := c.now().Add(c.leaseDuration)
	if expires.After(ceiling) {
		expires = ceiling
	}
	if !expires.After(c.now()) {
		return Run{}, autoError("lease_expired", "attempt lease expired")
	}
	run.Status = RunRunning
	run.CurrentAttempt.LeaseExpiresAt = expires.Format(time.RFC3339Nano)
	run.UpdatedAt = c.nowString()
	if err = c.appendJournal(journalEntry{Action: "run.heartbeat", ActorType: "runner", ActorID: r.ID, Runs: []Run{run}}); err != nil {
		return Run{}, err
	}
	return run, nil
}

// OpenInputFile rechecks runner, installation, membership, lease, and the
// claim's immutable file whitelist. The caller owns the returned handle.
func (c *Coordinator) OpenInputFile(ctx context.Context, token, runID, fileID string, credential AttemptCredential) (core.FileInfo, *os.File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := authenticateRunner(c.state, token)
	if err != nil {
		return core.FileInfo{}, nil, err
	}
	run, err := c.requireAttemptLocked(ctx, r, runID, credential)
	if err != nil {
		return core.FileInfo{}, nil, err
	}
	allowed := false
	for _, input := range run.Inputs {
		if input.FileID == fileID && fileID != "" {
			allowed = true
			break
		}
	}
	if !allowed {
		return core.FileInfo{}, nil, core.ErrNotFound
	}
	ownerCtx := core.WithUser(ctx, run.OwnerUserID)
	info, file, err := c.store.OpenFileContent(ownerCtx, core.FileRef{FileID: fileID})
	if err != nil {
		return core.FileInfo{}, nil, err
	}
	return info, file, nil
}

func (c *Coordinator) requireAttemptLocked(ctx context.Context, r RegisteredRunner, runID string, credential AttemptCredential) (Run, error) {
	if !validAttemptID(credential.AttemptID) || credential.FencingToken == 0 {
		return Run{}, core.Invalid("invalid attempt credential")
	}
	run, ok := c.state.Runs[runID]
	if !ok || run.RunnerID != r.ID {
		return Run{}, core.ErrNotFound
	}
	if _, _, err := c.authorizeRunLocked(ctx, r, run, true); err != nil {
		return Run{}, err
	}
	if run.CurrentAttempt == nil || run.CurrentAttempt.ID != credential.AttemptID || run.CurrentAttempt.FencingToken != credential.FencingToken {
		return Run{}, autoError("stale_attempt", "attempt id or fencing token is stale")
	}
	expires, err := parseStoredTime(run.CurrentAttempt.LeaseExpiresAt)
	if err != nil {
		return Run{}, err
	}
	if !c.now().Before(expires) {
		return Run{}, autoError("lease_expired", "attempt lease expired")
	}
	if run.Status != RunLeased && run.Status != RunRunning {
		return Run{}, autoError("stale_attempt", "run no longer accepts this attempt")
	}
	return run, nil
}

func (c *Coordinator) authorizeRunLocked(ctx context.Context, r RegisteredRunner, run Run, activeRequired bool) (Installation, InstallationRevision, error) {
	installation, ok := c.state.Installations[run.InstallationID]
	if !ok || installation.OwnerUserID != run.OwnerUserID || installation.ProjectID != run.ProjectID || installation.RunnerID != r.ID {
		return Installation{}, InstallationRevision{}, core.ErrNotFound
	}
	if activeRequired && !installation.Enabled {
		return Installation{}, InstallationRevision{}, autoError("installation_paused", "installation is paused")
	}
	if !runnerSupports(r, run.SkillID) {
		return Installation{}, InstallationRevision{}, autoError("capability_unavailable", "runner capability is unavailable")
	}
	ownerCtx := core.WithUser(ctx, run.OwnerUserID)
	if _, err := c.store.AutomationSnapshot(ownerCtx, core.AutomationSnapshotInput{ProjectID: run.ProjectID, ThroughSequence: run.SnapshotSequence}); err != nil {
		return Installation{}, InstallationRevision{}, err
	}
	revision, ok := findRevision(installation, run.RevisionID)
	if !ok {
		return Installation{}, InstallationRevision{}, fmt.Errorf("run revision is missing")
	}
	return installation, revision, nil
}

func (c *Coordinator) buildClaimLocked(ctx context.Context, run Run, revision InstallationRevision, attempt Attempt) (Claim, error) {
	snapshot, err := c.store.AutomationSnapshot(core.WithUser(ctx, run.OwnerUserID), core.AutomationSnapshotInput{ProjectID: run.ProjectID, ThroughSequence: run.SnapshotSequence})
	if err != nil {
		return Claim{}, err
	}
	byID := map[string]core.Event{}
	for _, record := range snapshot.Records {
		byID[record.Event.ID] = record.Event
	}
	task := runner.Task{RunID: run.ID, ProjectID: run.ProjectID, SkillID: run.SkillID, SkillVersion: run.SkillVersion, SkillDigest: run.SkillDigest, UserPrompt: revision.UserPrompt, Language: revision.Language, Inputs: []runner.Input{}}
	files := []ClaimInputFile{}
	for _, input := range run.Inputs {
		event, ok := byID[input.EventID]
		if !ok {
			return Claim{}, fmt.Errorf("run source event disappeared")
		}
		runnerInput := runner.Input{EventID: event.ID}
		if input.FileID != "" {
			if event.Content.File == nil || event.Content.File.ID != input.FileID {
				return Claim{}, fmt.Errorf("run input file no longer matches event")
			}
			runnerInput.AudioSHA = event.Content.File.SHA256
			files = append(files, ClaimInputFile{EventID: event.ID, FileID: event.Content.File.ID, Filename: event.Content.File.Filename, MediaType: event.Content.File.MediaType, SizeBytes: event.Content.File.SizeBytes, SHA256: event.Content.File.SHA256})
		} else if event.Content.Text != nil {
			runnerInput.Text = *event.Content.Text
		} else {
			return Claim{}, fmt.Errorf("run text input no longer has text")
		}
		task.Inputs = append(task.Inputs, runnerInput)
	}
	return Claim{Run: run, AttemptID: attempt.ID, FencingToken: attempt.FencingToken, LeaseExpiresAt: attempt.LeaseExpiresAt, Task: task, InputFiles: files}, nil
}

func (c *Coordinator) evaluateDueLocked(ctx context.Context) error {
	installations := make([]Installation, 0, len(c.state.Installations))
	for _, installation := range c.state.Installations {
		installations = append(installations, installation)
	}
	sort.Slice(installations, func(i, j int) bool { return installations[i].ID < installations[j].ID })
	for _, installation := range installations {
		if !installation.Enabled {
			continue
		}
		r, ok := c.state.Runners[installation.RunnerID]
		if !ok || r.RevokedAt != "" || !runnerSupports(r, installation.SkillID) {
			continue
		}
		ownerCtx := core.WithUser(ctx, installation.OwnerUserID)
		snapshot, err := c.store.AutomationSnapshot(ownerCtx, core.AutomationSnapshotInput{ProjectID: installation.ProjectID})
		if err != nil {
			if errors.Is(err, core.ErrNotFound) {
				continue
			}
			return err
		}
		if installation.SkillID == AudioTranscribeSkill {
			if err = c.generateEventRunsLocked(installation, snapshot); err != nil {
				return err
			}
		} else if err = c.generateDailyRunsLocked(installation, snapshot); err != nil {
			return err
		}
	}
	return nil
}

func (c *Coordinator) generateEventRunsLocked(installation Installation, snapshot core.AutomationSnapshot) error {
	newRuns := []Run{}
	for _, record := range snapshot.Records {
		if !isOwnedRawAudio(record.Event, installation.OwnerUserID) {
			continue
		}
		revision, ok := revisionForSequence(installation, record.Sequence)
		if !ok || !metadataMatches(record.Event.Metadata, revision.Trigger.MetadataEquals) {
			continue
		}
		key := installation.ID + "\x00" + revision.ID + "\x00" + record.Event.ID
		if _, exists := c.state.Dedupe[dedupeKey("event", key)]; exists {
			continue
		}
		run := c.newRun(installation, revision, "event", 1, record.Sequence, []RunInput{{EventID: record.Event.ID, FileID: record.Event.Content.File.ID}})
		if err := writeJSONOnce(c.runRequestPath(run), run); err != nil {
			return err
		}
		if err := c.appendJournal(journalEntry{Action: "run.event_triggered", ActorType: "system", ActorID: "dispatcher", Runs: []Run{run}, DedupeKind: "event", DedupeKey: key, DedupeRunID: run.ID}); err != nil {
			return err
		}
		newRuns = append(newRuns, run)
	}
	_ = newRuns
	return nil
}

func revisionForSequence(installation Installation, sequence int64) (InstallationRevision, bool) {
	for _, revision := range installation.Revisions {
		if sequence > revision.ActivationSequence && (!revision.Closed || sequence <= revision.ActiveThroughSequence) {
			return revision, true
		}
	}
	return InstallationRevision{}, false
}

func metadataMatches(event map[string]json.RawMessage, required map[string]json.RawMessage) bool {
	for key, want := range required {
		got, ok := event[key]
		if !ok || !jsonValuesEqual(got, want) {
			return false
		}
	}
	return true
}

func jsonValuesEqual(a, b json.RawMessage) bool {
	canonical := func(raw json.RawMessage) ([]byte, bool) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return nil, false
		}
		out, err := json.Marshal(value)
		return out, err == nil
	}
	ca, oka := canonical(a)
	cb, okb := canonical(b)
	return oka && okb && bytes.Equal(ca, cb)
}

func eventsByID(snapshot core.AutomationSnapshot) map[string]core.Event {
	out := make(map[string]core.Event, len(snapshot.Records))
	for _, record := range snapshot.Records {
		out[record.Event.ID] = record.Event
	}
	return out
}

func copyAndHash(reader io.Reader) ([]byte, string, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", err
	}
	return data, hashBytes(data), nil
}
