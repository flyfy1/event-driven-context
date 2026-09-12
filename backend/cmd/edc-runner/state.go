package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"event-driven-context/internal/runner"
)

type localState struct {
	dir         string
	pendingDir  string
	tempDir     string
	fingerprint string
}

type pendingCandidate struct {
	Server            string           `json:"server"`
	RunnerFingerprint string           `json:"runner_fingerprint"`
	RunID             string           `json:"run_id"`
	AttemptID         string           `json:"attempt_id"`
	FencingToken      uint64           `json:"fencing_token"`
	Candidate         runner.Candidate `json:"candidate"`
}

func openState(root, server, token string) (*localState, func(), error) {
	if root == "" {
		return nil, nil, fmt.Errorf("state directory is required")
	}
	if err := ensurePrivateDir(root); err != nil {
		return nil, nil, err
	}
	identityHash := sha256.Sum256([]byte(server + "\x00" + token))
	fingerprint := hex.EncodeToString(identityHash[:])
	dir := filepath.Join(root, fingerprint[:32])
	if err := ensurePrivateDir(dir); err != nil {
		return nil, nil, err
	}
	state := &localState{dir: dir, pendingDir: filepath.Join(dir, "pending"), tempDir: filepath.Join(dir, "tmp"), fingerprint: fingerprint}
	if err := ensurePrivateDir(state.pendingDir); err != nil {
		return nil, nil, err
	}
	if err := ensurePrivateDir(state.tempDir); err != nil {
		return nil, nil, err
	}
	lockPath := filepath.Join(dir, "runner.lock")
	lock, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open runner lock: %w", err)
	}
	if err = lock.Chmod(0o600); err != nil {
		lock.Close()
		return nil, nil, fmt.Errorf("secure runner lock: %w", err)
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, nil, fmt.Errorf("another runner is already active for this server")
		}
		return nil, nil, fmt.Errorf("lock runner state: %w", err)
	}
	release := func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
	}
	return state, release, nil
}

func ensurePrivateDir(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err = os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create private directory: %w", err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect private directory: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("runner state directories must be private (mode 0700)")
	}
	return nil
}

func (s *localState) savePending(value pendingCandidate) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	nameHash := sha256.Sum256([]byte(value.RunID + "\x00" + value.AttemptID))
	path := filepath.Join(s.pendingDir, hex.EncodeToString(nameHash[:])+".json")
	temporary, err := os.CreateTemp(s.pendingDir, ".candidate-")
	if err != nil {
		return "", fmt.Errorf("create pending candidate: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err = temporary.Chmod(0o600); err != nil {
		return "", err
	}
	if _, err = temporary.Write(encoded); err != nil {
		return "", err
	}
	if err = temporary.Sync(); err != nil {
		return "", err
	}
	if err = temporary.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(temporaryPath, path); err != nil {
		return "", err
	}
	committed = true
	if err = syncDirectory(s.pendingDir); err != nil {
		return "", err
	}
	return path, nil
}

func (s *localState) pending() ([]string, error) {
	entries, err := os.ReadDir(s.pendingDir)
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for _, entry := range entries {
		if entry.Type().IsRegular() && filepath.Ext(entry.Name()) == ".json" {
			paths = append(paths, filepath.Join(s.pendingDir, entry.Name()))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func readPending(path string) (pendingCandidate, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return pendingCandidate{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 1<<20 {
		return pendingCandidate{}, fmt.Errorf("pending candidate file is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return pendingCandidate{}, err
	}
	defer file.Close()
	var value pendingCandidate
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&value); err != nil {
		return pendingCandidate{}, fmt.Errorf("decode pending candidate: %w", err)
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return pendingCandidate{}, fmt.Errorf("pending candidate has trailing data")
	}
	if value.Server == "" || value.RunnerFingerprint == "" || value.RunID == "" || value.AttemptID == "" || value.FencingToken == 0 {
		return pendingCandidate{}, fmt.Errorf("pending candidate envelope is incomplete")
	}
	return value, nil
}

func removePending(path, dir string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncDirectory(dir)
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
