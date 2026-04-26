//go:build windows

package handlers

import (
	"os"
	"os/exec"
)

func enableChildProcessGroup(cmd *exec.Cmd) bool {
	return false
}

func interruptChildProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(os.Interrupt)
}

func killChildProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
