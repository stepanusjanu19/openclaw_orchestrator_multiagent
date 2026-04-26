//go:build windows

package handlers

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
	probe := portInspection{Available: false, Error: err.Error()}
	out, cmdErr := exec.Command("netstat", "-ano", "-p", "tcp").Output()
	if cmdErr != nil {
		return probe
	}
	needle := ":" + strconv.Itoa(port)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || !strings.EqualFold(fields[0], "TCP") {
			continue
		}
		if !strings.Contains(fields[1], needle) || !strings.EqualFold(fields[3], "LISTENING") {
			continue
		}
		pid, err := strconv.Atoi(fields[4])
		if err != nil {
			continue
		}
		probe.PID = pid
		probe.Command = windowsCommandLine(pid)
		return probe
	}
	return probe
}

func windowsCommandLine(pid int) string {
	out, err := exec.Command("wmic", "process", "where", fmt.Sprintf("ProcessId=%d", pid), "get", "CommandLine", "/value").Output()
	if err != nil {
		return ""
	}
	out = bytes.TrimSpace(out)
	line := strings.TrimPrefix(string(out), "CommandLine=")
	return strings.TrimSpace(line)
}

func stopExternalProcess(pid int) error {
	if pid <= 0 || pid == os.Getpid() {
		return fmt.Errorf("PID tidak valid")
	}
	return exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
}
