package automation

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"event-driven-context/internal/core"
)

func autoError(code, message string) error { return &core.Error{Code: code, Message: message} }

func (c *Coordinator) RegisterRunner(ctx context.Context, in RegisterRunnerInput) (RunnerRegistration, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	user, err := c.store.Me(ctx)
	if err != nil {
		return RunnerRegistration{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 100 || !utf8.ValidString(in.Name) || strings.ContainsRune(in.Name, 0) {
		return RunnerRegistration{}, core.Invalid("runner name must be UTF-8, 1-100 bytes")
	}
	capabilities, err := normalizeCapabilities(in.Capabilities)
	if err != nil {
		return RunnerRegistration{}, err
	}
	token := "edr_" + strings.TrimPrefix(newID("token"), "token_") + strings.TrimPrefix(newID("token"), "token_")
	r := RegisteredRunner{
		ID: newID("runr"), OwnerUserID: user.ID, Name: in.Name, Capabilities: capabilities,
		TokenHash: hashString(token), CreatedAt: c.nowString(),
	}
	artifact := struct {
		Runner    RegisteredRunner `json:"runner"`
		TokenHash string           `json:"token_hash"`
	}{r, r.TokenHash}
	if err = writeJSONOnce(c.runnerPath(r.ID), artifact); err != nil {
		return RunnerRegistration{}, err
	}
	if err = c.appendJournal(journalEntry{Action: "runner.registered", ActorType: "user", ActorID: user.ID, Runner: &r, RunnerTokenHash: r.TokenHash}); err != nil {
		return RunnerRegistration{}, err
	}
	return RunnerRegistration{Runner: r, Token: token}, nil
}

func (c *Coordinator) RevokeRunner(ctx context.Context, runnerID string, in RunnerActionInput) (RegisteredRunner, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if in.Action != "revoke" {
		return RegisteredRunner{}, core.Invalid("action must be revoke")
	}
	r, ok := c.state.Runners[runnerID]
	if !ok || r.OwnerUserID != core.UserID(ctx) {
		return RegisteredRunner{}, core.ErrNotFound
	}
	if r.RevokedAt != "" {
		return r, nil
	}
	r.RevokedAt = c.nowString()
	changed := []Run{}
	for _, run := range c.state.Runs {
		if run.RunnerID == runnerID && !terminal(run.Status) {
			run.Status = RunCancelled
			run.FailureCode = "runner_revoked"
			run.CurrentAttempt = nil
			run.UpdatedAt = c.nowString()
			changed = append(changed, run)
		}
	}
	if err := c.appendJournal(journalEntry{Action: "runner.revoked", ActorType: "user", ActorID: core.UserID(ctx), Runner: &r, RunnerTokenHash: r.TokenHash, Runs: changed}); err != nil {
		return RegisteredRunner{}, err
	}
	return r, nil
}

func (c *Coordinator) CreateInstallation(ctx context.Context, in CreateInstallationInput) (Installation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	owner := core.UserID(ctx)
	if owner == "" {
		return Installation{}, core.ErrUnauthenticated
	}
	snapshot, err := c.store.AutomationSnapshot(ctx, core.AutomationSnapshotInput{ProjectID: in.ProjectID})
	if err != nil {
		return Installation{}, err
	}
	r, ok := c.state.Runners[in.RunnerID]
	if !ok || r.OwnerUserID != owner || r.RevokedAt != "" {
		return Installation{}, core.ErrNotFound
	}
	skill, ok := c.skills[in.SkillID]
	if !ok || !runnerSupports(r, in.SkillID) {
		return Installation{}, autoError("capability_unavailable", "runner lacks the required fixed skill capability")
	}
	trigger, err := normalizeTrigger(in.SkillID, in.Trigger)
	if err != nil {
		return Installation{}, err
	}
	if err = validateConfig(in.Language, in.UserPrompt); err != nil {
		return Installation{}, err
	}
	now := c.nowString()
	revision := InstallationRevision{
		ID: newID("irev"), Number: 1, Language: in.Language, UserPrompt: in.UserPrompt,
		Trigger: trigger, ActivationSequence: snapshot.SnapshotSequence, ActivatedAt: now,
	}
	installation := Installation{
		ID: newID("ins"), OwnerUserID: owner, ProjectID: in.ProjectID, RunnerID: r.ID,
		SkillID: skill.ID, SkillVersion: skill.Version, SkillDigest: skill.Digest, Enabled: true,
		CurrentRevisionID: revision.ID, Revisions: []InstallationRevision{revision}, CreatedAt: now, UpdatedAt: now,
	}
	if err = writeJSONOnce(c.revisionPath(installation, revision), revision); err != nil {
		return Installation{}, err
	}
	if err = c.appendJournal(journalEntry{Action: "installation.created", ActorType: "user", ActorID: owner, Installation: &installation}); err != nil {
		return Installation{}, err
	}
	return installation, nil
}

func (c *Coordinator) ReviseInstallation(ctx context.Context, installationID string, in ReviseInstallationInput) (Installation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	installation, err := c.requireInstallationOwner(ctx, installationID)
	if err != nil {
		return Installation{}, err
	}
	if !installation.Enabled {
		return Installation{}, core.ErrConflict
	}
	trigger, err := normalizeTrigger(installation.SkillID, in.Trigger)
	if err != nil {
		return Installation{}, err
	}
	if err = validateConfig(in.Language, in.UserPrompt); err != nil {
		return Installation{}, err
	}
	snapshot, err := c.store.AutomationSnapshot(ctx, core.AutomationSnapshotInput{ProjectID: installation.ProjectID})
	if err != nil {
		return Installation{}, err
	}
	installation = cloneInstallation(installation)
	for i := range installation.Revisions {
		if installation.Revisions[i].ID == installation.CurrentRevisionID {
			installation.Revisions[i].ActiveThroughSequence = snapshot.SnapshotSequence
			installation.Revisions[i].Closed = true
		}
	}
	now := c.nowString()
	revision := InstallationRevision{
		ID: newID("irev"), Number: len(installation.Revisions) + 1, Language: in.Language,
		UserPrompt: in.UserPrompt, Trigger: trigger, ActivationSequence: snapshot.SnapshotSequence, ActivatedAt: now,
	}
	installation.Revisions = append(installation.Revisions, revision)
	installation.CurrentRevisionID = revision.ID
	installation.UpdatedAt = now
	if err = writeJSONOnce(c.revisionPath(installation, revision), revision); err != nil {
		return Installation{}, err
	}
	if err = c.appendJournal(journalEntry{Action: "installation.revised", ActorType: "user", ActorID: installation.OwnerUserID, Installation: &installation}); err != nil {
		return Installation{}, err
	}
	return installation, nil
}

func (c *Coordinator) SetInstallation(ctx context.Context, installationID string, in InstallationActionInput) (Installation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	installation, err := c.requireInstallationOwner(ctx, installationID)
	if err != nil {
		return Installation{}, err
	}
	switch in.Action {
	case "pause":
		if !installation.Enabled {
			return installation, nil
		}
		snapshot, snapshotErr := c.store.AutomationSnapshot(ctx, core.AutomationSnapshotInput{ProjectID: installation.ProjectID})
		if snapshotErr != nil {
			return Installation{}, snapshotErr
		}
		installation = cloneInstallation(installation)
		for i := range installation.Revisions {
			if installation.Revisions[i].ID == installation.CurrentRevisionID {
				installation.Revisions[i].ActiveThroughSequence = snapshot.SnapshotSequence
				installation.Revisions[i].Closed = true
			}
		}
		installation.Enabled = false
		installation.UpdatedAt = c.nowString()
		cancelled := c.cancelInstallationRuns(installation.ID, "installation_paused")
		if err = c.appendJournal(journalEntry{Action: "installation.paused", ActorType: "user", ActorID: installation.OwnerUserID, Installation: &installation, Runs: cancelled}); err != nil {
			return Installation{}, err
		}
	case "resume":
		if installation.Enabled {
			return installation, nil
		}
		installation = cloneInstallation(installation)
		snapshot, snapshotErr := c.store.AutomationSnapshot(ctx, core.AutomationSnapshotInput{ProjectID: installation.ProjectID})
		if snapshotErr != nil {
			return Installation{}, snapshotErr
		}
		prior, ok := findRevision(installation, installation.CurrentRevisionID)
		if !ok {
			return Installation{}, fmt.Errorf("current installation revision is missing")
		}
		now := c.nowString()
		revision := prior
		revision.ID = newID("irev")
		revision.Number = len(installation.Revisions) + 1
		revision.ActivationSequence = snapshot.SnapshotSequence
		revision.ActiveThroughSequence = 0
		revision.Closed = false
		revision.ActivatedAt = now
		installation.Revisions = append(installation.Revisions, revision)
		installation.CurrentRevisionID = revision.ID
		installation.Enabled = true
		installation.UpdatedAt = now
		if err = writeJSONOnce(c.revisionPath(installation, revision), revision); err != nil {
			return Installation{}, err
		}
		if err = c.appendJournal(journalEntry{Action: "installation.resumed", ActorType: "user", ActorID: installation.OwnerUserID, Installation: &installation}); err != nil {
			return Installation{}, err
		}
	default:
		return Installation{}, core.Invalid("action must be pause or resume")
	}
	return installation, nil
}

func (c *Coordinator) ListInstallations(ctx context.Context) ([]Installation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	userID := core.UserID(ctx)
	if userID == "" {
		return nil, core.ErrUnauthenticated
	}
	out := []Installation{}
	for _, installation := range c.state.Installations {
		if installation.OwnerUserID != userID {
			continue
		}
		if _, err := c.store.AutomationSnapshot(ctx, core.AutomationSnapshotInput{ProjectID: installation.ProjectID}); err == nil {
			out = append(out, installation)
		} else if !errors.Is(err, core.ErrNotFound) {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}

func (c *Coordinator) ListRunners(ctx context.Context) ([]RegisteredRunner, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	userID := core.UserID(ctx)
	if userID == "" {
		return nil, core.ErrUnauthenticated
	}
	out := []RegisteredRunner{}
	for _, registered := range c.state.Runners {
		if registered.OwnerUserID == userID {
			registered.TokenHash = ""
			out = append(out, registered)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}

func (c *Coordinator) RequestRun(ctx context.Context, in RequestRunInput) (Run, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	installation, err := c.requireInstallationOwner(ctx, in.InstallationID)
	if err != nil {
		return Run{}, err
	}
	if !installation.Enabled {
		return Run{}, core.ErrConflict
	}
	if !safeShort(in.RequestID, 128) {
		return Run{}, core.Invalid("request_id must be 1-128 safe bytes")
	}
	ids, err := normalizeIDs(in.SourceEventIDs)
	if err != nil {
		return Run{}, err
	}
	key := installation.OwnerUserID + "\x00" + installation.ID + "\x00" + in.RequestID
	if runID, ok := c.state.Dedupe[dedupeKey("manual", key)]; ok {
		run := c.state.Runs[runID]
		if equalRunInputs(run.Inputs, ids) {
			return run, nil
		}
		return Run{}, core.ErrConflict
	}
	snapshot, err := c.store.AutomationSnapshot(ctx, core.AutomationSnapshotInput{ProjectID: installation.ProjectID})
	if err != nil {
		return Run{}, err
	}
	if installation.SkillID == AudioTranscribeSkill {
		if err = c.generateEventRunsLocked(installation, snapshot); err != nil {
			return Run{}, err
		}
	}
	revision, ok := findRevision(installation, installation.CurrentRevisionID)
	if !ok {
		return Run{}, fmt.Errorf("current installation revision is missing")
	}
	inputs, err := selectManualInputs(installation, snapshot, ids)
	if err != nil {
		return Run{}, err
	}
	for _, existing := range c.state.Runs {
		if existing.InstallationID == installation.ID && !terminal(existing.Status) && !strings.HasPrefix(existing.Status, "blocked_") && equalInputRefs(existing.Inputs, inputs) {
			return Run{}, core.ErrConflict
		}
	}
	generation := c.nextGeneration(installation.ID, inputs)
	run := c.newRun(installation, revision, "manual", generation, snapshot.SnapshotSequence, inputs)
	if err = writeJSONOnce(c.runRequestPath(run), run); err != nil {
		return Run{}, err
	}
	entry := journalEntry{Action: "run.manual_requested", ActorType: "user", ActorID: installation.OwnerUserID,
		Runs: []Run{run}, DedupeKind: "manual", DedupeKey: key, DedupeRunID: run.ID}
	if err = c.appendJournal(entry); err != nil {
		return Run{}, err
	}
	return run, nil
}

func (c *Coordinator) ListRuns(ctx context.Context) ([]Run, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	userID := core.UserID(ctx)
	if userID == "" {
		return nil, core.ErrUnauthenticated
	}
	out := []Run{}
	for _, run := range c.state.Runs {
		if run.OwnerUserID != userID {
			continue
		}
		if _, err := c.store.AutomationSnapshot(ctx, core.AutomationSnapshotInput{ProjectID: run.ProjectID}); err == nil {
			out = append(out, run)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

func (c *Coordinator) GetRun(ctx context.Context, runID string) (Run, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	run, ok := c.state.Runs[runID]
	if !ok || run.OwnerUserID != core.UserID(ctx) {
		return Run{}, core.ErrNotFound
	}
	if _, err := c.store.AutomationSnapshot(ctx, core.AutomationSnapshotInput{ProjectID: run.ProjectID}); err != nil {
		return Run{}, err
	}
	return run, nil
}

func (c *Coordinator) requireInstallationOwner(ctx context.Context, id string) (Installation, error) {
	installation, ok := c.state.Installations[id]
	if !ok || installation.OwnerUserID != core.UserID(ctx) {
		return Installation{}, core.ErrNotFound
	}
	if _, err := c.store.AutomationSnapshot(ctx, core.AutomationSnapshotInput{ProjectID: installation.ProjectID}); err != nil {
		return Installation{}, err
	}
	return installation, nil
}

func (c *Coordinator) cancelInstallationRuns(installationID, reason string) []Run {
	changed := []Run{}
	for _, run := range c.state.Runs {
		if run.InstallationID == installationID && !terminal(run.Status) {
			run.Status = RunCancelled
			run.FailureCode = reason
			run.CurrentAttempt = nil
			run.UpdatedAt = c.nowString()
			changed = append(changed, run)
		}
	}
	return changed
}

func normalizeCapabilities(values []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "audio_transcription" && value != "daily_review" {
			return nil, core.Invalid("unsupported runner capability")
		}
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil, core.Invalid("at least one runner capability is required")
	}
	sort.Strings(out)
	return out, nil
}

func runnerSupports(r RegisteredRunner, skillID string) bool {
	want := "daily_review"
	if skillID == AudioTranscribeSkill {
		want = "audio_transcription"
	}
	for _, capability := range r.Capabilities {
		if capability == want {
			return true
		}
	}
	return false
}

func validateConfig(language, prompt string) error {
	if len(language) > 32 || !utf8.ValidString(language) || strings.ContainsRune(language, 0) {
		return core.Invalid("language must be safe UTF-8, max 32 bytes")
	}
	if len(prompt) > 8192 || !utf8.ValidString(prompt) || strings.ContainsRune(prompt, 0) {
		return core.Invalid("user_prompt must be safe UTF-8, max 8 KiB")
	}
	return nil
}

func normalizeTrigger(skillID string, trigger Trigger) (Trigger, error) {
	if skillID == AudioTranscribeSkill && trigger.Type != "event" {
		return Trigger{}, core.Invalid("audio-transcribe requires an event trigger")
	}
	if skillID == DailyReviewSkill && trigger.Type != "daily" {
		return Trigger{}, core.Invalid("daily-review requires a daily trigger")
	}
	switch trigger.Type {
	case "event":
		if trigger.LocalTime != "" || trigger.Timezone != "" {
			return Trigger{}, core.Invalid("event trigger cannot contain daily fields")
		}
		metadata, err := canonicalMetadata(trigger.MetadataEquals)
		if err != nil {
			return Trigger{}, err
		}
		trigger.MetadataEquals = metadata
	case "daily":
		if len(trigger.MetadataEquals) != 0 {
			return Trigger{}, core.Invalid("daily trigger cannot contain metadata_equals")
		}
		if len(trigger.LocalTime) != 5 || trigger.LocalTime[2] != ':' {
			return Trigger{}, core.Invalid("daily local_time must be HH:MM")
		}
		var hour, minute int
		if _, err := fmt.Sscanf(trigger.LocalTime, "%02d:%02d", &hour, &minute); err != nil || hour > 23 || minute > 59 {
			return Trigger{}, core.Invalid("daily local_time must be HH:MM")
		}
		if _, err := time.LoadLocation(trigger.Timezone); err != nil {
			return Trigger{}, core.Invalid("daily timezone must be an IANA timezone")
		}
	default:
		return Trigger{}, core.Invalid("trigger.type must be event or daily")
	}
	return trigger, nil
}

func canonicalMetadata(in map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	if len(in) > 128 {
		return nil, core.Invalid("metadata_equals max 128 fields")
	}
	out := map[string]json.RawMessage{}
	for key, raw := range in {
		if !safeShort(key, 128) || !json.Valid(raw) {
			return nil, core.Invalid("metadata_equals requires valid top-level JSON values")
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, core.Invalid("metadata_equals contains invalid JSON")
		}
		canonical, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		out[key] = canonical
	}
	data, _ := json.Marshal(out)
	if len(data) > core.MaxMetadataBytes {
		return nil, core.Invalid("metadata_equals max 32 KiB")
	}
	return out, nil
}

func normalizeIDs(ids []string) ([]string, error) {
	if len(ids) == 0 || len(ids) > 128 {
		return nil, core.Invalid("source_event_ids requires 1-128 IDs")
	}
	out := append([]string(nil), ids...)
	sort.Strings(out)
	for i, id := range out {
		if !safeShort(id, 128) || (i > 0 && out[i-1] == id) {
			return nil, core.Invalid("source_event_ids must be unique safe IDs")
		}
	}
	return out, nil
}

func equalRunInputs(inputs []RunInput, ids []string) bool {
	if len(inputs) != len(ids) {
		return false
	}
	got := make([]string, len(inputs))
	for i := range inputs {
		got[i] = inputs[i].EventID
	}
	sort.Strings(got)
	return strings.Join(got, "\x00") == strings.Join(ids, "\x00")
}

func selectManualInputs(installation Installation, snapshot core.AutomationSnapshot, ids []string) ([]RunInput, error) {
	byID := map[string]core.Event{}
	for _, record := range snapshot.Records {
		byID[record.Event.ID] = record.Event
	}
	inputs := []RunInput{}
	for _, id := range ids {
		event, ok := byID[id]
		if !ok {
			return nil, core.ErrNotFound
		}
		if installation.SkillID == AudioTranscribeSkill {
			if len(ids) != 1 || !isOwnedRawAudio(event, installation.OwnerUserID) {
				return nil, core.Invalid("audio rerun requires one owned original audio event")
			}
			inputs = append(inputs, RunInput{EventID: id, FileID: event.Content.File.ID})
		} else {
			if event.Content.Kind != "text" || event.Content.Text == nil || event.Provenance.SkillID == DailyReviewSkill {
				return nil, core.Invalid("daily rerun inputs must be non-review text events")
			}
			inputs = append(inputs, RunInput{EventID: id})
		}
	}
	return inputs, nil
}

func isOwnedRawAudio(event core.Event, owner string) bool {
	return event.ActorUserID == owner && event.Provenance.Kind == core.ProvenanceOriginal && event.Content.Kind == "file" &&
		event.Content.File != nil && strings.HasPrefix(event.Content.File.MediaType, "audio/")
}

func findRevision(installation Installation, id string) (InstallationRevision, bool) {
	for _, revision := range installation.Revisions {
		if revision.ID == id {
			return revision, true
		}
	}
	return InstallationRevision{}, false
}

func terminal(status string) bool {
	return status == RunSucceeded || status == RunFailed || status == RunCancelled || status == RunSkipped
}

func (c *Coordinator) nextGeneration(installationID string, inputs []RunInput) int {
	maxGeneration := 0
	for _, run := range c.state.Runs {
		if run.InstallationID == installationID && equalInputRefs(run.Inputs, inputs) && run.Generation > maxGeneration {
			maxGeneration = run.Generation
		}
	}
	return maxGeneration + 1
}

func equalInputRefs(a, b []RunInput) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (c *Coordinator) newRun(installation Installation, revision InstallationRevision, triggerType string, generation int, snapshot int64, inputs []RunInput) Run {
	now := c.nowString()
	run := Run{
		ID: newID("run"), InstallationID: installation.ID, RevisionID: revision.ID, OwnerUserID: installation.OwnerUserID,
		ProjectID: installation.ProjectID, RunnerID: installation.RunnerID, SkillID: installation.SkillID,
		SkillVersion: installation.SkillVersion, SkillDigest: installation.SkillDigest, TriggerType: triggerType,
		Generation: generation, SnapshotSequence: snapshot, Inputs: inputs, Status: RunQueued, CreatedAt: now, UpdatedAt: now,
	}
	if installation.SkillID == AudioTranscribeSkill && generation > 1 {
		var prior *Run
		for _, candidate := range c.state.Runs {
			if candidate.InstallationID != installation.ID || candidate.Status != RunSucceeded || candidate.OutputSlot != "transcript" || candidate.OutputEventID == "" || !equalInputRefs(candidate.Inputs, inputs) {
				continue
			}
			if prior == nil || candidate.Generation > prior.Generation {
				copy := candidate
				prior = &copy
			}
		}
		if prior != nil {
			run.SupersedesEventIDs = []string{prior.OutputEventID}
		}
	}
	return run
}

func authenticateRunner(state durableState, token string) (RegisteredRunner, error) {
	if len(token) < 20 || len(token) > 256 {
		return RegisteredRunner{}, core.ErrUnauthenticated
	}
	want := hashString(token)
	for _, r := range state.Runners {
		if subtle.ConstantTimeCompare([]byte(r.TokenHash), []byte(want)) == 1 {
			if r.RevokedAt != "" {
				return RegisteredRunner{}, core.ErrUnauthenticated
			}
			return r, nil
		}
	}
	return RegisteredRunner{}, core.ErrUnauthenticated
}
