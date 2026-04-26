//go:build !windows

package handlers

import (
	"os/exec"
	"syscall"
)

func enableChildProcessGroup(cmd *exec.Cmd) bool {
	if cmd == nil {
		return false
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return true
}

func interruptChildProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
}

func killChildProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
