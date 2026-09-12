package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"event-driven-context/internal/core"
	"event-driven-context/internal/runner"
)

const maxCandidateBytes = 64 << 10

type candidateArtifact struct {
	RunID        string           `json:"run_id"`
	AttemptID    string           `json:"attempt_id"`
	FencingToken uint64           `json:"fencing_token"`
	Candidate    runner.Candidate `json:"candidate"`
	Digest       string           `json:"digest"`
}

func (c *Coordinator) Submit(ctx context.Context, token, runID string, credential AttemptCredential, in SubmitInput) (Run, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := authenticateRunner(c.state, token)
	if err != nil {
		return Run{}, err
	}
	if !validAttemptID(credential.AttemptID) || credential.FencingToken == 0 {
		return Run{}, core.Invalid("invalid attempt credential")
	}
	if existing, ok := c.state.Runs[runID]; ok && existing.RunnerID == r.ID && existing.Status == RunSucceeded && in.Candidate == nil && existing.NoOutputReason != "" && existing.NoOutputReason == in.NoOutputReason {
		if _, _, err = c.authorizeRunLocked(ctx, r, existing, true); err != nil {
			return Run{}, err
		}
		return cloneRun(existing), nil
	}
	if existing, handled, resumeErr := c.resumeSubmittedCandidateLocked(ctx, r, runID, credential, in); handled {
		return existing, resumeErr
	}
	run, err := c.requireAttemptLocked(ctx, r, runID, credential)
	if err != nil {
		return Run{}, err
	}
	if (in.Candidate == nil) == (in.NoOutputReason == "") {
		return Run{}, core.Invalid("submit requires exactly one candidate or no_output_reason")
	}
	if in.NoOutputReason != "" {
		if in.NoOutputReason != "no_relevant_records" {
			return Run{}, core.Invalid("unsupported no_output_reason")
		}
		run = cloneRun(run)
		run.Status = RunSucceeded
		run.NoOutputReason = in.NoOutputReason
		run.CurrentAttempt = nil
		run.UpdatedAt = c.nowString()
		if err = c.appendJournal(journalEntry{Action: "run.no_output", ActorType: "runner", ActorID: r.ID, Runs: []Run{run}}); err != nil {
			return Run{}, err
		}
		return run, nil
	}
	encoded, err := validateCandidate(run, *in.Candidate)
	if err != nil {
		return Run{}, err
	}
	digest := hashBytes(encoded)
	artifact := candidateArtifact{RunID: run.ID, AttemptID: credential.AttemptID, FencingToken: credential.FencingToken, Candidate: *in.Candidate, Digest: digest}
	if err = writeJSONOnce(c.candidatePath(run, credential.AttemptID), artifact); err != nil {
		return Run{}, err
	}
	if c.hooks.AfterCandidate {
		return Run{}, errors.New("injected crash after candidate")
	}
	run = cloneRun(run)
	run.Status = RunCandidateSaved
	run.CandidateDigest = digest
	run.OutputSlot = in.Candidate.OutputSlot
	run.UpdatedAt = c.nowString()
	if err = c.appendJournal(journalEntry{Action: "run.candidate_saved", ActorType: "runner", ActorID: r.ID, Runs: []Run{run}}); err != nil {
		return Run{}, err
	}
	return c.commitCandidateLocked(ctx, run, artifact)
}

func (c *Coordinator) resumeSubmittedCandidateLocked(ctx context.Context, registered RegisteredRunner, runID string, credential AttemptCredential, in SubmitInput) (Run, bool, error) {
	run, ok := c.state.Runs[runID]
	if !ok || run.RunnerID != registered.ID || in.Candidate == nil {
		return Run{}, false, nil
	}
	path := c.candidatePath(run, credential.AttemptID)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, true, err
	}
	if _, _, err = c.authorizeRunLocked(ctx, registered, run, true); err != nil {
		return Run{}, true, err
	}
	var artifact candidateArtifact
	if err = strictJSON(data, &artifact); err != nil {
		return Run{}, true, err
	}
	if artifact.RunID != run.ID || artifact.AttemptID != credential.AttemptID || artifact.FencingToken != credential.FencingToken {
		return Run{}, true, autoError("stale_attempt", "candidate belongs to a different attempt")
	}
	encoded, err := validateCandidate(run, *in.Candidate)
	if err != nil || hashBytes(encoded) != artifact.Digest {
		if err != nil {
			return Run{}, true, err
		}
		return Run{}, true, core.ErrConflict
	}
	if run.Status == RunSucceeded {
		return cloneRun(run), true, nil
	}
	if terminal(run.Status) {
		return Run{}, true, autoError("stale_attempt", "run no longer accepts this attempt")
	}
	if run.Status != RunCandidateSaved || run.CandidateDigest != artifact.Digest {
		run = cloneRun(run)
		run.Status = RunCandidateSaved
		run.CandidateDigest = artifact.Digest
		run.OutputSlot = artifact.Candidate.OutputSlot
		run.UpdatedAt = c.nowString()
		if err = c.appendJournal(journalEntry{Action: "run.candidate_recovered", ActorType: "runner", ActorID: registered.ID, Runs: []Run{run}}); err != nil {
			return Run{}, true, err
		}
	}
	committed, err := c.commitCandidateLocked(ctx, run, artifact)
	return committed, true, err
}

func (c *Coordinator) Fail(ctx context.Context, token, runID string, credential AttemptCredential, in FailureInput) (Run, error) {
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
	if !safeShort(in.Code, 128) {
		return Run{}, core.Invalid("failure code must be 1-128 safe bytes")
	}
	run = cloneRun(run)
	run.FailureKind = in.Kind
	run.FailureCode = in.Code
	run.CurrentAttempt = nil
	run.UpdatedAt = c.nowString()
	switch in.Kind {
	case "auth":
		run.Status = RunBlockedAuth
	case "capability":
		run.Status = RunBlockedCapability
	case "temporary", "invalid_output":
		if run.AttemptCount >= 3 {
			run.Status = RunFailed
		} else {
			run.Status = RunRetryWait
			delay := time.Minute * time.Duration(1<<(run.AttemptCount-1))
			run.NextAttemptAt = c.now().Add(delay).Format(time.RFC3339Nano)
		}
	default:
		return Run{}, core.Invalid("failure kind must be auth, capability, temporary, or invalid_output")
	}
	if err = c.appendJournal(journalEntry{Action: "run.failed_attempt", ActorType: "runner", ActorID: r.ID, Runs: []Run{run}}); err != nil {
		return Run{}, err
	}
	return run, nil
}

func validateCandidate(run Run, candidate runner.Candidate) ([]byte, error) {
	if candidate.SchemaVersion != 1 || candidate.Outcome != "output" || !utf8.ValidString(candidate.Text) || strings.TrimSpace(candidate.Text) == "" {
		return nil, core.Invalid("candidate envelope or text is invalid")
	}
	allowed := map[string]bool{}
	for _, input := range run.Inputs {
		allowed[input.EventID] = true
	}
	validateSources := func(ids []string) error {
		if len(ids) == 0 || len(ids) > 128 {
			return core.Invalid("candidate source_event_ids are required")
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if !allowed[id] || seen[id] {
				return core.Invalid("candidate source_event_ids must be an authorized unique subset")
			}
			seen[id] = true
		}
		return nil
	}
	if err := validateSources(candidate.SourceEventIDs); err != nil {
		return nil, err
	}
	if run.SkillID == AudioTranscribeSkill {
		if candidate.Kind != "transcript" || candidate.OutputSlot != "transcript" || len(candidate.Items) != 0 {
			return nil, core.Invalid("transcript candidate has invalid kind or output slot")
		}
	} else {
		if candidate.Kind != "summary" || candidate.OutputSlot != "daily_review" || len(candidate.Items) == 0 {
			return nil, core.Invalid("daily candidate requires classified items")
		}
		for _, item := range candidate.Items {
			if item.Kind != "progress" && item.Kind != "decision" && item.Kind != "open_question" && item.Kind != "suggestion" {
				return nil, core.Invalid("daily candidate item kind is invalid")
			}
			if !utf8.ValidString(item.Text) || strings.TrimSpace(item.Text) == "" {
				return nil, core.Invalid("daily candidate item text is invalid")
			}
			if err := validateSources(item.SourceEventIDs); err != nil {
				return nil, err
			}
		}
	}
	encoded, err := json.Marshal(candidate)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxCandidateBytes {
		return nil, core.TooLarge("candidate max 64 KiB")
	}
	return encoded, nil
}

type derivedOutput struct {
	slot      string
	kind      string
	text      string
	sourceIDs []string
}

func candidateOutputs(run Run, candidate runner.Candidate) []derivedOutput {
	if run.SkillID == AudioTranscribeSkill {
		return []derivedOutput{{slot: "transcript", kind: core.ProvenanceTranscript, text: candidate.Text, sourceIDs: append([]string(nil), candidate.SourceEventIDs...)}}
	}
	nonSuggestions := []runner.CandidateItem{}
	outputs := []derivedOutput{}
	for index, item := range candidate.Items {
		if item.Kind == "suggestion" {
			outputs = append(outputs, derivedOutput{slot: fmt.Sprintf("suggestion:%04d", index), kind: core.ProvenanceSuggestion, text: item.Text, sourceIDs: append([]string(nil), item.SourceEventIDs...)})
		} else {
			nonSuggestions = append(nonSuggestions, item)
		}
	}
	if len(nonSuggestions) != 0 {
		lines := make([]string, 0, len(nonSuggestions))
		sourceSet := map[string]bool{}
		for _, item := range nonSuggestions {
			lines = append(lines, item.Kind+": "+item.Text)
			for _, id := range item.SourceEventIDs {
				sourceSet[id] = true
			}
		}
		sources := make([]string, 0, len(sourceSet))
		for id := range sourceSet {
			sources = append(sources, id)
		}
		sort.Strings(sources)
		outputs = append([]derivedOutput{{slot: "daily_review", kind: core.ProvenanceSummary, text: strings.Join(lines, "\n"), sourceIDs: sources}}, outputs...)
	}
	return outputs
}

func (c *Coordinator) commitCandidateLocked(ctx context.Context, run Run, artifact candidateArtifact) (Run, error) {
	if commit, err := c.readCommit(run); err == nil {
		return c.finishCommitLocked(run, artifact, commit)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Run{}, err
	}
	outputs := candidateOutputs(run, artifact.Candidate)
	if len(outputs) == 0 {
		return Run{}, core.Invalid("candidate produced no traceable outputs")
	}
	committed := make([]CommittedOutput, 0, len(outputs))
	ownerCtx := core.WithUser(ctx, run.OwnerUserID)
	for _, output := range outputs {
		metadata := map[string]json.RawMessage{}
		putMetadata(metadata, "installation_id", run.InstallationID)
		putMetadata(metadata, "output_slot", output.slot)
		metadata["generation"] = json.RawMessage(fmt.Sprintf("%d", run.Generation))
		event, err := c.store.RecordDerived(ownerCtx, core.DerivedRecordInput{
			ProjectID:      run.ProjectID,
			Content:        core.ContentInput{Kind: "text", Text: &output.text},
			Metadata:       metadata,
			IdempotencyKey: "automation:" + run.ID + ":" + output.slot,
			Provenance:     core.Provenance{Kind: output.kind, SourceEventIDs: output.sourceIDs, RunID: run.ID, SkillID: run.SkillID, SkillVersion: run.SkillVersion},
			Relations:      core.Relations{SupersedesEventIDs: supersedesForOutput(run, output)},
		})
		if err != nil {
			return Run{}, err
		}
		committed = append(committed, CommittedOutput{OutputSlot: output.slot, Kind: output.kind, EventID: event.ID})
	}
	if c.hooks.AfterEvent {
		return Run{}, errors.New("injected crash after event")
	}
	inboxID := deterministicID("inb", run.ID, artifact.Candidate.OutputSlot, run.OwnerUserID)
	commit := CommitRecord{RunID: run.ID, CandidateDigest: artifact.Digest, Outputs: committed, InboxEntryID: inboxID, CommittedAt: c.nowString()}
	if err := writeJSONOnce(c.commitPath(run), commit); err != nil {
		return Run{}, err
	}
	if c.hooks.AfterCommit {
		return Run{}, errors.New("injected crash after commit")
	}
	return c.finishCommitLocked(run, artifact, commit)
}

func supersedesForOutput(run Run, output derivedOutput) []string {
	if output.slot == "transcript" {
		return append([]string(nil), run.SupersedesEventIDs...)
	}
	return nil
}

func (c *Coordinator) finishCommitLocked(run Run, artifact candidateArtifact, commit CommitRecord) (Run, error) {
	if commit.RunID != run.ID || commit.CandidateDigest != artifact.Digest || len(commit.Outputs) == 0 {
		return Run{}, fmt.Errorf("automation commit does not match immutable candidate")
	}
	eventIDs := make([]string, 0, len(commit.Outputs))
	snapshot, err := c.store.AutomationSnapshot(core.WithUser(context.Background(), run.OwnerUserID), core.AutomationSnapshotInput{ProjectID: run.ProjectID})
	if err != nil {
		return Run{}, err
	}
	byID := eventsByID(snapshot)
	expected := map[string]derivedOutput{}
	for _, output := range candidateOutputs(run, artifact.Candidate) {
		expected[output.slot] = output
	}
	for _, output := range commit.Outputs {
		if output.EventID == "" || output.OutputSlot == "" {
			return Run{}, fmt.Errorf("automation commit is incomplete")
		}
		want, expectedOutput := expected[output.OutputSlot]
		event, ok := byID[output.EventID]
		if !ok || event.ProjectID != run.ProjectID || event.ActorUserID != run.OwnerUserID || event.Provenance.RunID != run.ID || event.Provenance.SkillID != run.SkillID || event.Provenance.SkillVersion != run.SkillVersion || event.Provenance.Kind != output.Kind {
			return Run{}, fmt.Errorf("automation commit event verification failed")
		}
		if !expectedOutput || want.kind != output.Kind || event.Content.Text == nil || *event.Content.Text != want.text || !sameStrings(event.Provenance.SourceEventIDs, want.sourceIDs) || !sameStrings(event.Relations.SupersedesEventIDs, supersedesForOutput(run, want)) {
			return Run{}, fmt.Errorf("automation commit output verification failed")
		}
		if !jsonStringEquals(event.Metadata["installation_id"], run.InstallationID) || !jsonStringEquals(event.Metadata["output_slot"], output.OutputSlot) || string(event.Metadata["generation"]) != fmt.Sprintf("%d", run.Generation) {
			return Run{}, fmt.Errorf("automation commit metadata verification failed")
		}
		eventIDs = append(eventIDs, output.EventID)
	}
	if len(commit.Outputs) != len(expected) {
		return Run{}, fmt.Errorf("automation commit output count mismatch")
	}
	entry := InboxEntry{
		ID: commit.InboxEntryID, RecipientUserID: run.OwnerUserID, ProjectID: run.ProjectID,
		InstallationID: run.InstallationID, RunID: run.ID, OutputEventID: eventIDs[0], OutputEventIDs: eventIDs,
		CandidateDigest: artifact.Digest, Candidate: artifact.Candidate, CreatedAt: commit.CommittedAt,
	}
	if err := writeJSONOnce(c.inboxPath(entry), entry); err != nil {
		return Run{}, err
	}
	run = cloneRun(run)
	run.Status = RunSucceeded
	run.CurrentAttempt = nil
	run.OutputEventID = eventIDs[0]
	run.OutputEventIDs = eventIDs
	run.InboxEntryID = entry.ID
	run.UpdatedAt = c.nowString()
	if err := c.appendJournal(journalEntry{Action: "run.committed", ActorType: "system", ActorID: "coordinator", Runs: []Run{run}, Inbox: &entry}); err != nil {
		return Run{}, err
	}
	return run, nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa := append([]string(nil), a...)
	bb := append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	return strings.Join(aa, "\x00") == strings.Join(bb, "\x00")
}

func jsonStringEquals(raw json.RawMessage, want string) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil && value == want
}

func putMetadata(metadata map[string]json.RawMessage, key, value string) {
	encoded, _ := json.Marshal(value)
	metadata[key] = encoded
}

func (c *Coordinator) readCommit(run Run) (CommitRecord, error) {
	var commit CommitRecord
	data, err := os.ReadFile(c.commitPath(run))
	if err != nil {
		return commit, err
	}
	if err = strictJSON(data, &commit); err != nil {
		return commit, err
	}
	return commit, nil
}

func (c *Coordinator) recoverLocked() error {
	runIDs := make([]string, 0, len(c.state.Runs))
	for id := range c.state.Runs {
		runIDs = append(runIDs, id)
	}
	sort.Strings(runIDs)
	for _, id := range runIDs {
		run := c.state.Runs[id]
		if terminal(run.Status) {
			continue
		}
		artifact, found, err := c.findCandidate(run)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		if run.CurrentAttempt == nil || run.CurrentAttempt.ID != artifact.AttemptID || run.CurrentAttempt.FencingToken != artifact.FencingToken {
			return fmt.Errorf("candidate attempt does not match durable run fence for %s", run.ID)
		}
		encoded, err := validateCandidate(run, artifact.Candidate)
		if err != nil || hashBytes(encoded) != artifact.Digest {
			return fmt.Errorf("invalid immutable candidate for %s", run.ID)
		}
		if run.Status != RunCandidateSaved || run.CandidateDigest != artifact.Digest {
			run = cloneRun(run)
			run.Status = RunCandidateSaved
			run.CandidateDigest = artifact.Digest
			run.OutputSlot = artifact.Candidate.OutputSlot
			run.UpdatedAt = c.nowString()
			if err = c.appendJournal(journalEntry{Action: "run.candidate_recovered", ActorType: "system", ActorID: "recovery", Runs: []Run{run}}); err != nil {
				return err
			}
		}
		if _, err = c.commitCandidateLocked(context.Background(), run, artifact); err != nil {
			var coreErr *core.Error
			if errors.As(err, &coreErr) && (coreErr.Code == "not_found" || coreErr.Code == "forbidden") {
				run.Status = RunBlockedAuth
				run.FailureCode = "membership_revoked"
				run.CurrentAttempt = nil
				run.UpdatedAt = c.nowString()
				if journalErr := c.appendJournal(journalEntry{Action: "run.recovery_blocked", ActorType: "system", ActorID: "recovery", Runs: []Run{run}}); journalErr != nil {
					return journalErr
				}
				continue
			}
			return err
		}
	}
	return nil
}

func (c *Coordinator) findCandidate(run Run) (candidateArtifact, bool, error) {
	root := filepath.Join(c.dataDir, "projects", run.ProjectID, "automation", "runs", run.ID, "attempts")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return candidateArtifact{}, false, nil
	}
	if err != nil {
		return candidateArtifact{}, false, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var found *candidateArtifact
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if !validAttemptID(entry.Name()) {
			return candidateArtifact{}, false, fmt.Errorf("invalid candidate attempt directory")
		}
		data, readErr := os.ReadFile(filepath.Join(root, entry.Name(), "candidate.json"))
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return candidateArtifact{}, false, readErr
		}
		var artifact candidateArtifact
		if err = strictJSON(data, &artifact); err != nil {
			return candidateArtifact{}, false, err
		}
		if artifact.RunID != run.ID || artifact.AttemptID != entry.Name() || artifact.FencingToken == 0 {
			return candidateArtifact{}, false, fmt.Errorf("candidate identity mismatch")
		}
		if found != nil && found.Digest != artifact.Digest {
			return candidateArtifact{}, false, fmt.Errorf("multiple different candidates for one run")
		}
		copy := artifact
		found = &copy
	}
	if found == nil {
		return candidateArtifact{}, false, nil
	}
	return *found, true, nil
}

func strictJSON(data []byte, out any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("unexpected trailing JSON")
}
