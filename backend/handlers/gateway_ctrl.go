// handlers/gateway_ctrl.go
// Kontrol daemon OpenClaw Gateway — status, restart, stop, reload config.
// Strategi deteksi: systemctl → pgrep → HTTP probe (berlapis).

package handlers

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// OpenClawCtrlHandler mengelola lifecycle daemon OpenClaw.
type OpenClawCtrlHandler struct {
	App *App
}

// DaemonStatus menyimpan info status daemon.
type DaemonStatus struct {
	Running     bool   `json:"running"`
	PID         int    `json:"pid,omitempty"`
	Method      string `json:"method"` // "systemctl" | "pgrep" | "probe"
	ActiveSince string `json:"active_since,omitempty"`
	Error       string `json:"error,omitempty"`
}

func openClawGatewayEnv(base []string, token string) []string {
	env := append([]string{}, base...)
	if strings.TrimSpace(token) != "" {
		env = append(env,
			"OPENCLAW_GW_TOKEN="+token,
			"OPENCLAW_GATEWAY_TOKEN="+token,
		)
	}
	return env
}

func setOpenClawSystemdTokenAlias(token string) {
	if strings.TrimSpace(token) == "" {
		return
	}
	_ = exec.Command(
		"systemctl",
		"--user",
		"set-environment",
		"OPENCLAW_GW_TOKEN="+token,
		"OPENCLAW_GATEWAY_TOKEN="+token,
	).Run()
}

// detectDaemon mencoba mendeteksi status daemon OpenClaw secara berlapis.
func detectDaemon(gatewayURL string) DaemonStatus {
	// ── Lapis 1: systemctl ────────────────────────────────────────────────────
	if out, err := exec.Command("systemctl", "--user", "is-active", "openclaw").Output(); err == nil {
		active := strings.TrimSpace(string(out))
		if active == "active" {
			// Ambil PID dari systemctl
			pidOut, _ := exec.Command("systemctl", "--user", "show", "-p", "MainPID", "openclaw").Output()
			pid := 0
			if pidStr := strings.TrimPrefix(strings.TrimSpace(string(pidOut)), "MainPID="); pidStr != "" {
				pid, _ = strconv.Atoi(pidStr)
			}
			// Ambil ActiveEnterTimestamp
			sinceOut, _ := exec.Command("systemctl", "--user", "show", "-p", "ActiveEnterTimestamp", "openclaw").Output()
			since := strings.TrimPrefix(strings.TrimSpace(string(sinceOut)), "ActiveEnterTimestamp=")
			return DaemonStatus{Running: true, PID: pid, Method: "systemctl", ActiveSince: since}
		}
		return DaemonStatus{Running: false, Method: "systemctl"}
	}

	// ── Lapis 2: pgrep ────────────────────────────────────────────────────────
	// Gunakan pencarian untuk "openclaw-gateway" (nama binary yang berjalan di background)
	if out, err := exec.Command("pgrep", "-f", "openclaw-gateway").Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		// Ambil PID pertama yang muncul
		if len(lines) > 0 {
			pid, _ := strconv.Atoi(lines[0])
			if pid > 0 {
				return DaemonStatus{Running: true, PID: pid, Method: "pgrep"}
			}
		}
	}

	// ── Lapis 3: HTTP probe ke Gateway URL ───────────────────────────────────
	if gatewayURL != "" {
		client := &http.Client{Timeout: 2 * time.Second}
		if resp, err := client.Get(gatewayURL); err == nil {
			resp.Body.Close()
			return DaemonStatus{Running: true, Method: "probe"}
		}
	}

	return DaemonStatus{Running: false, Method: "pgrep"}
}

// GetDaemonStatus — GET /api/openclaw/daemon
func (h *OpenClawCtrlHandler) GetDaemonStatus(w http.ResponseWriter, r *http.Request) {
	status := detectDaemon(h.App.Cfg.GatewayURL)
	jsonOK(w, status)
}

// RestartDaemon — POST /api/openclaw/daemon/restart
func (h *OpenClawCtrlHandler) RestartDaemon(w http.ResponseWriter, r *http.Request) {
	// Coba systemctl restart terlebih dahulu
	setOpenClawSystemdTokenAlias(h.App.Cfg.GatewayToken)
	if err := exec.Command("systemctl", "--user", "restart", "openclaw").Run(); err == nil {
		time.Sleep(1 * time.Second) // beri waktu daemon naik
		status := detectDaemon(h.App.Cfg.GatewayURL)
		jsonOK(w, map[string]interface{}{
			"message": "OpenClaw daemon berhasil di-restart via systemctl",
			"status":  status,
		})
		return
	}

	// Fallback: kill + respawn openclaw
	exec.Command("pkill", "-f", "openclaw-gateway").Run()
	time.Sleep(500 * time.Millisecond)

	// Spawn kembali sebagai background process
	cmd := exec.Command("openclaw", "gateway")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Env = append(openClawGatewayEnv(os.Environ(), h.App.Cfg.GatewayToken), "OPENCLAW_DETACH=1")
	if err := cmd.Start(); err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("Gagal restart daemon: %v", err))
		return
	}
	time.Sleep(1 * time.Second)
	status := detectDaemon(h.App.Cfg.GatewayURL)
	jsonOK(w, map[string]interface{}{
		"message": "OpenClaw daemon di-respawn (fallback mode)",
		"status":  status,
	})
}

// StopDaemon — POST /api/openclaw/daemon/stop
func (h *OpenClawCtrlHandler) StopDaemon(w http.ResponseWriter, r *http.Request) {
	// Coba systemctl stop
	if err := exec.Command("systemctl", "--user", "stop", "openclaw").Run(); err == nil {
		jsonOK(w, map[string]interface{}{
			"message": "OpenClaw daemon dihentikan via systemctl",
			"status":  DaemonStatus{Running: false, Method: "systemctl"},
		})
		return
	}
	// Fallback: pkill
	exec.Command("pkill", "-f", "openclaw-gateway").Run()
	jsonOK(w, map[string]interface{}{
		"message": "OpenClaw daemon dihentikan via pkill",
		"status":  DaemonStatus{Running: false, Method: "pgrep"},
	})
}

// ReloadConfig — POST /api/openclaw/daemon/reload
// Kirim SIGHUP ke daemon agar hot-reload openclaw.json tanpa downtime.
func (h *OpenClawCtrlHandler) ReloadConfig(w http.ResponseWriter, r *http.Request) {
	status := detectDaemon(h.App.Cfg.GatewayURL)
	if !status.Running {
		jsonError(w, http.StatusConflict, "Daemon tidak running — tidak ada yang perlu di-reload")
		return
	}

	// Coba reload via systemctl
	if err := exec.Command("systemctl", "--user", "reload", "openclaw").Run(); err == nil {
		jsonOK(w, map[string]interface{}{
			"message": "OpenClaw config di-reload via systemctl (SIGHUP)",
		})
		return
	}

	// Fallback: kirim SIGHUP ke PID
	if status.PID > 0 {
		if err := exec.Command("kill", "-HUP", strconv.Itoa(status.PID)).Run(); err == nil {
			jsonOK(w, map[string]interface{}{
				"message": fmt.Sprintf("SIGHUP dikirim ke PID %d", status.PID),
			})
			return
		}
	}

	// Fallback 2: HTTP POST ke /api/admin/reload jika daemon mengeksposnya
	if h.App.Cfg.GatewayURL != "" {
		reloadURL := strings.TrimRight(h.App.Cfg.GatewayURL, "/v1/chat/completions") + "/api/admin/reload"
		resp, err := http.Post(reloadURL, "application/json", nil)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				jsonOK(w, map[string]interface{}{
					"message": "OpenClaw config di-reload via REST API",
				})
				return
			}
		}
	}

	jsonError(w, http.StatusInternalServerError, "Gagal reload: tidak bisa kirim sinyal SIGHUP atau REST API ke daemon")
}
