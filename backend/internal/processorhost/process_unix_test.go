//go:build unix

package processorhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunProcessCancellationTerminatesProcessGroup(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "pids")
	started := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := runProcess(context.Background(), 250*time.Millisecond, "/bin/sh", []string{"-c", `(trap '' TERM; while :; do sleep 30; done) & child=$!; printf '%s %s' $$ $child > "$1"; wait`, "processor", pidPath}, "", dir)
		done <- err
	}()
	var raw []byte
	var err error
	for deadline := time.Now().Add(500 * time.Millisecond); time.Now().Before(deadline); {
		raw, err = os.ReadFile(pidPath)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(raw))
	if len(fields) != 2 {
		t.Fatalf("invalid process ids: %q", raw)
	}
	pids := make([]int, 2)
	for i, field := range fields {
		pids[i], err = strconv.Atoi(field)
		if err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(time.Until(started.Add(500 * time.Millisecond)))
	if processExists(pids[0]) {
		t.Fatalf("parent process %d did not exit on SIGTERM", pids[0])
	}
	if !processExists(pids[1]) {
		t.Fatalf("TERM-ignoring child %d exited before SIGKILL escalation", pids[1])
	}
	err = <-done
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runProcess error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("process group cancellation took %v", elapsed)
	}
	for _, pid := range pids {
		deadline := time.Now().Add(2 * time.Second)
		for processExists(pid) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if processExists(pid) {
			t.Fatalf("processor process %d survived cancellation", pid)
		}
	}
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
