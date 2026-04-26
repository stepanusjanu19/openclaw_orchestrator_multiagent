//go:build linux

package handlers

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
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
	inode := findListeningSocketInode(port)
	if inode == "" {
		return probe
	}
	pid := findPIDBySocketInode(inode)
	if pid == 0 {
		return probe
	}
	probe.PID = pid
	probe.Command = readProcCmdline(pid)
	probe.Executable = readProcLink(pid, "exe")
	probe.UID, probe.UIDKnown = readProcUID(pid)
	probe.OpenClawRouter, probe.RouterTool = readProcRouterEnv(pid)
	return probe
}

func findListeningSocketInode(port int) string {
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		if inode := findListeningSocketInodeInFile(path, port); inode != "" {
			return inode
		}
	}
	return ""
}

func findListeningSocketInodeInFile(path string, port int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 || fields[0] == "sl" {
			continue
		}
		local := fields[1]
		state := fields[3]
		if state != "0A" {
			continue
		}
		_, rawPort, ok := strings.Cut(local, ":")
		if !ok {
			continue
		}
		parsedPort, err := strconv.ParseInt(rawPort, 16, 32)
		if err == nil && int(parsedPort) == port {
			return fields[9]
		}
	}
	return ""
}

func findPIDBySocketInode(inode string) int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	socketNeedle := "socket:[" + inode + "]"
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		fdDir := filepath.Join("/proc", entry.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err == nil && target == socketNeedle {
				return pid
			}
		}
	}
	return 0
}

func readProcCmdline(pid int) string {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil || len(data) == 0 {
		return ""
	}
	parts := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
	return strings.Join(parts, " ")
}

func readProcLink(pid int, name string) string {
	target, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), name))
	if err != nil {
		return ""
	}
	return target
}

func readProcUID(pid int) (int, bool) {
	info, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
	if err != nil {
		return 0, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(stat.Uid), true
}

func readProcRouterEnv(pid int) (bool, string) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
	if err != nil {
		return false, ""
	}
	openclawRouter := false
	routerTool := ""
	for _, item := range strings.Split(string(data), "\x00") {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		switch key {
		case "OPENCLAW_ROUTER_RUNTIME":
			openclawRouter = strings.EqualFold(value, "true")
		case "OPENCLAW_ROUTER_TOOL":
			routerTool = strings.TrimSpace(value)
		}
	}
	return openclawRouter, routerTool
}

func stopExternalProcess(pid int) error {
	if pid <= 0 || pid == os.Getpid() {
		return fmt.Errorf("PID tidak valid")
	}
	if err := syscall.Kill(pid, syscall.SIGINT); err != nil {
		return err
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return syscall.Kill(pid, syscall.SIGKILL)
}
