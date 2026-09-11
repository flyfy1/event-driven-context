package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const processOutputLimit = 1 << 20

type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (w *limitedBuffer) Write(input []byte) (int, error) {
	original := len(input)
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		if len(input) > remaining {
			_, _ = w.buffer.Write(input[:remaining])
		} else {
			_, _ = w.buffer.Write(input)
		}
	}
	if original > remaining {
		w.overflow = true
	}
	return original, nil
}

func safeEnvironment() []string {
	keys := []string{"HOME", "CODEX_HOME", "PATH", "TMPDIR", "LANG", "LC_ALL", "SSL_CERT_FILE", "SSL_CERT_DIR"}
	environment := make([]string, 0, len(keys))
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			environment = append(environment, key+"="+value)
		}
	}
	return environment
}

func runProcess(ctx context.Context, timeout time.Duration, path string, args []string, stdin string, dir string) ([]byte, error) {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.Command(path, args...)
	command.Dir = dir
	command.Env = safeEnvironment()
	command.Stdin = strings.NewReader(stdin)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout := &limitedBuffer{limit: processOutputLimit}
	stderr := &limitedBuffer{limit: processOutputLimit}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return nil, &RunnerError{Kind: CapabilityError, Err: fmt.Errorf("start executable: %w", err)}
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		// The direct process may leave children in its process group. Reap the
		// parent first, then kill any survivors before returning on every path.
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if stdout.overflow || stderr.overflow {
			return nil, &RunnerError{Kind: InvalidOutput, Err: fmt.Errorf("process output exceeded limit")}
		}
		if err != nil {
			return nil, &RunnerError{Kind: ExecutionError, Err: fmt.Errorf("process exited unsuccessfully")}
		}
		return stdout.buffer.Bytes(), nil
	case <-runCtx.Done():
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		<-done
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return nil, &RunnerError{Kind: TimeoutError, Err: fmt.Errorf("process exceeded %s", timeout)}
		}
		return nil, runCtx.Err()
	}
}
