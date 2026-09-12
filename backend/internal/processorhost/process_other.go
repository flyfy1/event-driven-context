//go:build !unix

package processorhost

import "os/exec"

func configureProcessGroup(*exec.Cmd) {}
func terminateProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
