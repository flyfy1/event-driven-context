//go:build unix

package processorhost

import (
	"os/exec"
	"syscall"
)

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateProcessGroup(cmd *exec.Cmd) {
	signalProcessGroup(cmd, syscall.SIGTERM)
}

func killProcessGroup(cmd *exec.Cmd) {
	signalProcessGroup(cmd, syscall.SIGKILL)
}

func signalProcessGroup(cmd *exec.Cmd, signal syscall.Signal) {
	if cmd.Process == nil {
		return
	}
	// Setpgid makes the initially started PID the process-group ID. Keep using
	// that stable ID after the parent exits so a later SIGKILL still reaches
	// grandchildren that ignored SIGTERM and continue holding inherited pipes.
	_ = syscall.Kill(-cmd.Process.Pid, signal)
}
