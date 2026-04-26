//go:build !linux && !windows

package handlers

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

type portInspection struct {
	Available      bool
	PID            int
	Command        string
	Executable     string
	UID            int
	UIDKnown       bool
	RouterTool     string
	OpenClawRouter bool
	Error          string
}

func inspectPort(host string, port int) portInspection {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err == nil {
		_ = ln.Close()
		return portInspection{Available: true}
	}
	return portInspection{Available: false, Error: err.Error()}
}

func stopExternalProcess(pid int) error {
	if pid <= 0 || pid == os.Getpid() {
		return fmt.Errorf("PID tidak valid")
	}
	return fmt.Errorf("stop external process tidak didukung pada OS ini")
}
