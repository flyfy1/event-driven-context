package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"event-driven-context/internal/automation"
	"event-driven-context/internal/core"
	"event-driven-context/internal/runner"
)

type tickResult struct {
	Status string `json:"status"`
	RunID  string `json:"run_id,omitempty"`
}

func tick(ctx context.Context, client *runnerClient, state *localState, opts options) (tickResult, error) {
	retried, err := retryPending(ctx, client, state)
	if err != nil {
		return tickResult{}, err
	}
	if retried != "" {
		return tickResult{Status: "candidate_submitted", RunID: retried}, nil
	}
	claim, err := client.dispatch(ctx)
	if err != nil {
		return tickResult{}, err
	}
	if claim == nil {
		return tickResult{Status: "idle"}, nil
	}
	if err = validateClaim(*claim); err != nil {
		return tickResult{}, fmt.Errorf("invalid claim: %w", err)
	}

	attemptCtx, cancelAttempt := context.WithCancel(ctx)
	var leaseDone chan struct{}
	defer func() {
		cancelAttempt()
		if leaseDone != nil {
			<-leaseDone
		}
	}()
	leaseUpdates := make(chan error, 1)
	claimExpiry, _ := time.Parse(time.RFC3339Nano, claim.LeaseExpiresAt)
	initialHeartbeatCtx, cancelInitialHeartbeat := context.WithDeadline(attemptCtx, claimExpiry)
	initialRun, err := client.heartbeat(initialHeartbeatCtx, claim.Run.ID, claim.AttemptID, claim.FencingToken)
	cancelInitialHeartbeat()
	if err != nil {
		return tickResult{}, err
	}
	leaseExpiresAt, err := leaseExpiry(initialRun, claim.LeaseExpiresAt)
	if err != nil {
		return tickResult{}, err
	}
	leaseDone = make(chan struct{})
	go func() {
		defer close(leaseDone)
		maintainLease(attemptCtx, cancelAttempt, client, *claim, leaseExpiresAt, opts.heartbeatInterval, leaseUpdates)
	}()

	taskDir, err := os.MkdirTemp(state.tempDir, "attempt-")
	if err != nil {
		return tickResult{}, fmt.Errorf("create attempt directory: %w", err)
	}
	if err = os.Chmod(taskDir, 0o700); err != nil {
		_ = os.RemoveAll(taskDir)
		return tickResult{}, fmt.Errorf("secure attempt directory: %w", err)
	}
	defer os.RemoveAll(taskDir)
	if err = materializeInputs(attemptCtx, client, claim, taskDir); err != nil {
		if leaseErr := receivedLeaseError(leaseUpdates); leaseErr != nil {
			return tickResult{}, leaseErr
		}
		return tickResult{}, err
	}

	candidate, err := runner.Execute(attemptCtx, claim.Task, runnerConfig(opts, taskDir))
	if err != nil {
		if leaseErr := receivedLeaseError(leaseUpdates); leaseErr != nil {
			return tickResult{}, leaseErr
		}
		if ctx.Err() != nil {
			return tickResult{}, ctx.Err()
		}
		kind, code := classifyExecutionError(err)
		failCtx, cancel := context.WithTimeout(ctx, opts.requestTimeout)
		defer cancel()
		if reportErr := client.fail(failCtx, claim.Run.ID, claim.AttemptID, claim.FencingToken, automation.FailureInput{Kind: kind, Code: code}); reportErr != nil {
			return tickResult{}, fmt.Errorf("execution failed (%s); failure report failed: %w", code, reportErr)
		}
		return tickResult{Status: "run_failed", RunID: claim.Run.ID}, nil
	}
	pending := pendingCandidate{
		Server: client.baseURL, RunnerFingerprint: state.fingerprint,
		RunID: claim.Run.ID, AttemptID: claim.AttemptID, FencingToken: claim.FencingToken,
		Candidate: candidate,
	}
	pendingPath, err := state.savePending(pending)
	if err != nil {
		return tickResult{}, fmt.Errorf("save candidate before submit: %w", err)
	}
	if err = client.submit(attemptCtx, pending); err != nil {
		if definitiveAttemptError(err) {
			_ = removePending(pendingPath, state.pendingDir)
		}
		return tickResult{}, err
	}
	if err = removePending(pendingPath, state.pendingDir); err != nil {
		return tickResult{}, fmt.Errorf("remove submitted candidate: %w", err)
	}
	return tickResult{Status: "candidate_submitted", RunID: claim.Run.ID}, nil
}

func retryPending(ctx context.Context, client *runnerClient, state *localState) (string, error) {
	paths, err := state.pending()
	if err != nil {
		return "", fmt.Errorf("list pending candidates: %w", err)
	}
	for _, path := range paths {
		value, err := readPending(path)
		if err != nil {
			return "", err
		}
		if value.Server != client.baseURL || value.RunnerFingerprint != state.fingerprint {
			return "", fmt.Errorf("pending candidate belongs to a different runner identity")
		}
		if err = client.submit(ctx, value); err != nil {
			if definitiveAttemptError(err) {
				if removeErr := removePending(path, state.pendingDir); removeErr != nil {
					return "", removeErr
				}
				continue
			}
			return "", err
		}
		if err = removePending(path, state.pendingDir); err != nil {
			return "", err
		}
		return value.RunID, nil
	}
	return "", nil
}

func validateClaim(claim automation.Claim) error {
	if claim.Run.ID == "" || claim.AttemptID == "" || claim.FencingToken == 0 {
		return fmt.Errorf("run and attempt identity are required")
	}
	if claim.Task.RunID != claim.Run.ID || claim.Task.ProjectID != claim.Run.ProjectID || claim.Task.SkillID != claim.Run.SkillID || claim.Task.SkillVersion != claim.Run.SkillVersion || !strings.EqualFold(claim.Task.SkillDigest, claim.Run.SkillDigest) {
		return fmt.Errorf("task does not match its fixed run")
	}
	if claim.Run.CurrentAttempt != nil && (claim.Run.CurrentAttempt.ID != claim.AttemptID || claim.Run.CurrentAttempt.FencingToken != claim.FencingToken) {
		return fmt.Errorf("attempt does not match run")
	}
	expires, err := time.Parse(time.RFC3339Nano, claim.LeaseExpiresAt)
	if err != nil || !expires.After(time.Now()) {
		return fmt.Errorf("lease expiry is invalid")
	}
	byEvent := map[string]automation.ClaimInputFile{}
	for _, file := range claim.InputFiles {
		if file.EventID == "" || file.FileID == "" || byEvent[file.EventID].FileID != "" || file.SizeBytes <= 0 || file.SizeBytes > core.MaxMediaBytes {
			return fmt.Errorf("input file whitelist is invalid")
		}
		if !validSHA256(file.SHA256) || audioExtension(file.MediaType) == "" {
			return fmt.Errorf("input file integrity metadata is invalid")
		}
		byEvent[file.EventID] = file
	}
	seenEvents := map[string]bool{}
	runInputs := map[string]string{}
	for _, input := range claim.Run.Inputs {
		if input.EventID == "" {
			return fmt.Errorf("fixed run input event is required")
		}
		if _, exists := runInputs[input.EventID]; exists {
			return fmt.Errorf("fixed run inputs contain duplicates")
		}
		runInputs[input.EventID] = input.FileID
	}
	if len(runInputs) != len(claim.Task.Inputs) {
		return fmt.Errorf("task inputs do not match fixed run inputs")
	}
	for _, input := range claim.Task.Inputs {
		if input.EventID == "" || seenEvents[input.EventID] {
			return fmt.Errorf("task inputs are invalid")
		}
		seenEvents[input.EventID] = true
		runFileID, inRun := runInputs[input.EventID]
		if !inRun {
			return fmt.Errorf("task input is outside the fixed run")
		}
		file, hasFile := byEvent[input.EventID]
		if hasFile {
			if runFileID != file.FileID || input.Text != "" || input.AudioPath != "" || !strings.EqualFold(input.AudioSHA, file.SHA256) {
				return fmt.Errorf("audio task input does not match its file grant")
			}
			delete(byEvent, input.EventID)
		} else if runFileID != "" || input.Text == "" || input.AudioPath != "" || input.AudioSHA != "" {
			return fmt.Errorf("text task input is invalid")
		}
	}
	if len(byEvent) != 0 {
		return fmt.Errorf("claim contains an unused file grant")
	}
	return nil
}

func materializeInputs(ctx context.Context, client *runnerClient, claim *automation.Claim, taskDir string) error {
	files := map[string]automation.ClaimInputFile{}
	for _, file := range claim.InputFiles {
		files[file.EventID] = file
	}
	for index := range claim.Task.Inputs {
		input := &claim.Task.Inputs[index]
		file, ok := files[input.EventID]
		if !ok {
			continue
		}
		path := filepath.Join(taskDir, fmt.Sprintf("input-%d%s", index, audioExtension(file.MediaType)))
		if err := downloadVerified(ctx, client, *claim, file, path); err != nil {
			return err
		}
		input.AudioPath = path
	}
	return nil
}

func downloadVerified(ctx context.Context, client *runnerClient, claim automation.Claim, file automation.ClaimInputFile, path string) error {
	body, contentLength, err := client.download(ctx, claim.Run.ID, claim.AttemptID, claim.FencingToken, file.FileID)
	if err != nil {
		return err
	}
	defer body.Close()
	if contentLength >= 0 && contentLength != int64(file.SizeBytes) {
		return fmt.Errorf("input file length does not match claim")
	}
	destination, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(destination, hash), io.LimitReader(body, int64(file.SizeBytes)+1))
	closeErr := destination.Close()
	if copyErr != nil || closeErr != nil || written != int64(file.SizeBytes) {
		return fmt.Errorf("input file download is incomplete")
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), file.SHA256) {
		return fmt.Errorf("input file sha256 does not match claim")
	}
	return nil
}

func maintainLease(ctx context.Context, cancel context.CancelFunc, client *runnerClient, claim automation.Claim, expiry time.Time, interval time.Duration, result chan<- error) {
	for {
		wait := interval
		if until := time.Until(expiry); until < wait {
			wait = until
		}
		if wait <= 0 {
			select {
			case result <- fmt.Errorf("attempt lease expired"):
			default:
			}
			cancel()
			return
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		heartbeatCtx, cancelHeartbeat := context.WithDeadline(ctx, expiry)
		run, err := client.heartbeat(heartbeatCtx, claim.Run.ID, claim.AttemptID, claim.FencingToken)
		cancelHeartbeat()
		if err != nil {
			select {
			case result <- err:
			default:
			}
			cancel()
			return
		}
		expiry, err = leaseExpiry(run, "")
		if err != nil {
			select {
			case result <- err:
			default:
			}
			cancel()
			return
		}
	}
}

func leaseExpiry(run automation.Run, fallback string) (time.Time, error) {
	value := fallback
	if run.CurrentAttempt != nil && run.CurrentAttempt.LeaseExpiresAt != "" {
		value = run.CurrentAttempt.LeaseExpiresAt
	}
	expires, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || !expires.After(time.Now()) {
		return time.Time{}, fmt.Errorf("server returned an expired lease")
	}
	return expires, nil
}

func receivedLeaseError(channel <-chan error) error {
	select {
	case err := <-channel:
		return err
	default:
		return nil
	}
}

func classifyExecutionError(err error) (kind, code string) {
	switch {
	case runner.IsKind(err, runner.CapabilityError):
		return "capability", "capability_unavailable"
	case runner.IsKind(err, runner.InvalidTask):
		return "invalid_output", "invalid_task"
	case runner.IsKind(err, runner.IntegrityError):
		return "invalid_output", "input_integrity"
	case runner.IsKind(err, runner.InvalidOutput):
		return "invalid_output", "invalid_output"
	case runner.IsKind(err, runner.TimeoutError):
		return "temporary", "execution_timeout"
	default:
		return "temporary", "execution_failed"
	}
}

func definitiveAttemptError(err error) bool {
	var remote *remoteError
	if !errors.As(err, &remote) {
		return false
	}
	if remote.Status == http.StatusNotFound {
		return true
	}
	switch remote.Code {
	case "stale_attempt", "lease_expired", "installation_paused", "runner_revoked":
		return true
	default:
		return false
	}
}

func temporary(err error) bool {
	var remote *remoteError
	if errors.As(err, &remote) {
		if remote.Code == "stale_attempt" || remote.Code == "lease_expired" {
			return true
		}
		return remote.Status >= 500 || remote.Status == http.StatusTooManyRequests || remote.Status == http.StatusRequestTimeout
	}
	var network net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network)
}

func stableErrorCode(err error) string {
	var remote *remoteError
	if errors.As(err, &remote) && remote.Code != "" {
		return remote.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "temporary_failure"
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func audioExtension(mediaType string) string {
	base, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return ""
	}
	switch strings.ToLower(base) {
	case "audio/mp4", "audio/x-m4a", "audio/m4a":
		return ".m4a"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return ".wav"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	default:
		return ""
	}
}
