package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type HardenTask struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type HardenPreviewResponse struct {
	OS         string       `json:"os"`
	IsRHEL     bool         `json:"is_rhel"`
	TargetUser string       `json:"target_user"`
	TargetHome string       `json:"target_home"`
	Tasks      []HardenTask `json:"tasks"`
}

type HardenExecuteRequest struct {
	SelectedTasks []string `json:"selected_tasks"`
}

type localUserInfo struct {
	Username string
	HomeDir  string
	UID      string
}

type hardenCommand struct {
	Label    string
	Command  string
	Args     []string
	Stdin    string
	Optional bool
}

type taskRunner struct {
	logs     []string
	warnings bool
}

var hardenTaskIDPattern = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)
var numericPortPattern = regexp.MustCompile(`^[0-9]{1,5}$`)

func osDetect() (bool, string) {
	isRHEL := false
	osName := "Ubuntu/Debian"
	osRelease, err := os.ReadFile("/etc/os-release")
	if err == nil {
		releaseStr := strings.ToLower(string(osRelease))
		if strings.Contains(releaseStr, "id=\"almalinux\"") || strings.Contains(releaseStr, "id=\"centos\"") || strings.Contains(releaseStr, "id=\"rhel\"") || strings.Contains(releaseStr, "id=almalinux") {
			isRHEL = true
			osName = "AlmaLinux / RHEL"
		} else if strings.Contains(releaseStr, "id=ubuntu") || strings.Contains(releaseStr, "id=debian") {
			isRHEL = false
			osName = "Ubuntu / Debian"
		}
	} else {
		isRHEL = true
		osName = "AlmaLinux / RHEL (Fallback)"
	}
	return isRHEL, osName
}

func getUser() localUserInfo {
	info := localUserInfo{
		Username: "lenvo_alma",
		HomeDir:  "/home/lenvo_alma",
		UID:      "1000",
	}
	usr, _ := user.Current()
	if usr != nil {
		info.Username = usr.Username
		info.HomeDir = usr.HomeDir
		info.UID = usr.Uid
	}
	return info
}

type SystemHardenHandler struct {
	App *App
}

func (h *SystemHardenHandler) HardenPreviewHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	isRHEL, osName := osDetect()
	info := getUser()

	tasks := []HardenTask{
		{ID: "port", Title: "Port Security", Description: "Batasi AnyDesk & matikan socket admin publik yang tidak perlu"},
		{ID: "firewall", Title: "Konfigurasi Firewall", Description: "Kunci zona public dan allow hanya port lokal yang dibutuhkan"},
		{ID: "ssh", Title: "Hardening SSH", Description: "Disable root login dan batasi akses ke user saat ini"},
		{ID: "perms", Title: "Credential Access", Description: "Set permission ketat untuk file credential OpenClaw"},
		{ID: "systemd", Title: "Process Isolation Gateway", Description: "Tambah sandbox systemd drop-in untuk OpenClaw"},
		{ID: "cloudflared", Title: "Install Cloudflared Tunnel", Description: "Install cloudflared via package manager/installer resmi"},
	}

	if isRHEL {
		tasks = append(tasks, HardenTask{
			ID:          "selinux",
			Title:       "SELinux MAC Advanced",
			Description: "Label port/file context dan tulis helper script audit SELinux",
		})
	}

	json.NewEncoder(w).Encode(HardenPreviewResponse{
		OS:         osName,
		IsRHEL:     isRHEL,
		TargetUser: info.Username,
		TargetHome: info.HomeDir,
		Tasks:      tasks,
	})
}

func (h *SystemHardenHandler) HardenExecuteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req HardenExecuteRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}
	if len(req.SelectedTasks) == 0 {
		jsonError(w, http.StatusBadRequest, "selected_tasks wajib diisi")
		return
	}
	for _, task := range req.SelectedTasks {
		if !hardenTaskIDPattern.MatchString(task) {
			jsonError(w, http.StatusBadRequest, "ID task hardening tidak valid")
			return
		}
	}
	if err := ensureSudoReady(); err != nil {
		jsonError(w, http.StatusPreconditionFailed, err.Error())
		return
	}

	isRHEL, _ := osDetect()
	info := getUser()
	apiPort := validatedPort(h.App.Cfg.APIPort, "8080")
	gwPort := extractPort(h.App.Cfg.GatewayURL, "14000")
	ollamaPort := extractPort(h.App.Cfg.OllamaDirectURL, "11434")
	fePort := extractPort(h.App.Cfg.FrontendOrigin, "5173")

	runner := &taskRunner{
		logs: []string{
			"🚀 Structured hardening execution started",
			"Mode: sudo -n allowlist executor",
			"Selected tasks: " + strings.Join(req.SelectedTasks, ", "),
		},
	}

	for _, taskID := range req.SelectedTasks {
		switch taskID {
		case "port":
			runner.runTask("Port Security", buildPortSecurityCommands())
		case "firewall":
			runner.runTask("Firewall", buildFirewallCommands(isRHEL, gwPort, apiPort, fePort, ollamaPort))
		case "ssh":
			runner.runTask("SSH Hardening", buildSSHTask(info.Username))
		case "perms":
			runner.runTask("Credential Access", buildPermissionCommands(info.HomeDir))
		case "systemd":
			runner.runTask("Systemd Sandbox", buildSystemdCommands(info))
		case "cloudflared":
			runner.runTask("Cloudflared", buildCloudflaredCommands(isRHEL, info.HomeDir))
		case "selinux":
			if isRHEL {
				runner.runSELinuxTask(info, gwPort, apiPort, fePort, ollamaPort)
			}
		default:
			runner.warn("Task tidak dikenali dan di-skip: " + taskID)
		}
	}

	message := "Eksekusi hardening selesai."
	if runner.warnings {
		message = "Eksekusi hardening selesai dengan warning. Lihat log detail."
	}
	jsonOK(w, map[string]interface{}{
		"message": message,
		"logs":    strings.Join(runner.logs, "\n"),
	})
}

func ensureSudoReady() error {
	cmd := exec.Command("sudo", "-n", "true")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("Sudo non-interactive belum siap. Jalankan 'sudo -v' di terminal server lalu ulangi. Detail: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func validatedPort(raw, fallback string) string {
	val := strings.TrimSpace(raw)
	if !numericPortPattern.MatchString(val) {
		return fallback
	}
	n, err := strconv.Atoi(val)
	if err != nil || n <= 0 || n > 65535 {
		return fallback
	}
	return strconv.Itoa(n)
}

func extractPort(rawURL, fallback string) string {
	if rawURL == "" {
		return fallback
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Port() == "" {
		return fallback
	}
	return validatedPort(u.Port(), fallback)
}

func (r *taskRunner) runTask(name string, commands []hardenCommand) {
	r.logs = append(r.logs, "", ">> Task: "+name)
	for _, step := range commands {
		if out, err := runStructuredCommand(step); err != nil {
			r.warn(fmt.Sprintf("%s gagal: %v", step.Label, err))
			if out != "" {
				r.logs = append(r.logs, out)
			}
			if !step.Optional {
				continue
			}
		} else {
			r.logs = append(r.logs, "✅ "+step.Label)
			if out != "" {
				r.logs = append(r.logs, out)
			}
		}
	}
}

func (r *taskRunner) runSELinuxTask(info localUserInfo, gwPort, apiPort, fePort, ollamaPort string) {
	r.logs = append(r.logs, "", ">> Task: SELinux")
	out, err := exec.Command("getenforce").CombinedOutput()
	status := strings.TrimSpace(string(out))
	if status == "" {
		status = "unknown"
	}
	r.logs = append(r.logs, "SELinux status: "+status)
	if err != nil || status == "Disabled" {
		r.warn("SELinux tidak aktif. Skip semanage/restorecon.")
		return
	}

	for _, cmd := range buildSELinuxCommands(info, gwPort, apiPort, fePort, ollamaPort) {
		stepOut, stepErr := runStructuredCommand(cmd)
		if stepErr != nil {
			r.warn(fmt.Sprintf("%s gagal: %v", cmd.Label, stepErr))
		} else {
			r.logs = append(r.logs, "✅ "+cmd.Label)
		}
		if stepOut != "" {
			r.logs = append(r.logs, stepOut)
		}
	}

	scriptPath := filepath.Join(info.HomeDir, ".openclaw", "selinux-autofix.sh")
	script := "#!/bin/bash\n" +
		"echo \"[autofix] Scanning recent AVC denials...\"\n" +
		"sudo ausearch -m AVC -ts recent 2>/dev/null | grep -iE 'node|openclaw|multiagent|cloudflared|anydesk' > /tmp/oc_selinux.log\n" +
		"if [ -s /tmp/oc_selinux.log ]; then\n" +
		"  echo \"[autofix] Found denials. Generating hotfix policy...\"\n" +
		"  cat /tmp/oc_selinux.log | sudo audit2allow -M openclaw_hotfix\n" +
		"  sudo semodule -i openclaw_hotfix.pp\n" +
		"  echo \"Hotfix SELinux openclaw_hotfix applied\"\n" +
		"else\n" +
		"  echo \"[autofix] No AVC denials found. System is clean.\"\n" +
		"fi\n" +
		"rm -f openclaw_hotfix.* /tmp/oc_selinux.log\n"
	if err := writeRootFile(scriptPath, script, "0755"); err != nil {
		r.warn("Gagal menulis selinux-autofix.sh: " + err.Error())
	} else {
		r.logs = append(r.logs, "✅ SELinux autofix script ditulis ke "+scriptPath)
	}
}

func (r *taskRunner) warn(msg string) {
	r.warnings = true
	r.logs = append(r.logs, "⚠️ "+msg)
}

func runStructuredCommand(cmd hardenCommand) (string, error) {
	argv := append([]string{"-n", cmd.Command}, cmd.Args...)
	ex := exec.Command("sudo", argv...)
	if cmd.Stdin != "" {
		ex.Stdin = strings.NewReader(cmd.Stdin)
	}
	out, err := ex.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if cmd.Optional {
			return text, nil
		}
		return text, err
	}
	return text, nil
}

func writeRootFile(path, content, perm string) error {
	if _, err := runStructuredCommand(hardenCommand{
		Label:   "Write " + path,
		Command: "tee",
		Args:    []string{path},
		Stdin:   content,
	}); err != nil {
		return err
	}
	_, err := runStructuredCommand(hardenCommand{
		Label:   "chmod " + perm + " " + path,
		Command: "chmod",
		Args:    []string{perm, path},
	})
	return err
}

func buildPortSecurityCommands() []hardenCommand {
	cmds := []hardenCommand{
		{Label: "Stop AnyDesk", Command: "systemctl", Args: []string{"stop", "anydesk"}, Optional: true},
		{Label: "Disable cockpit.socket", Command: "systemctl", Args: []string{"disable", "--now", "cockpit.socket"}, Optional: true},
		{Label: "Disable systemd-journal-remote.socket", Command: "systemctl", Args: []string{"disable", "--now", "systemd-journal-remote.socket"}, Optional: true},
	}
	anydeskPath := "/etc/anydesk/system.conf"
	line := "ad.listen.interface=127.0.0.1\n"
	if data, err := exec.Command("sudo", "-n", "cat", anydeskPath).CombinedOutput(); err == nil {
		current := string(data)
		if !strings.Contains(current, "ad.listen.interface") {
			cmds = append(cmds, hardenCommand{
				Label:   "Update AnyDesk listen interface",
				Command: "tee",
				Args:    []string{anydeskPath},
				Stdin:   current + "\n" + line,
				Optional: true,
			})
		}
	}
	cmds = append(cmds, hardenCommand{Label: "Start AnyDesk", Command: "systemctl", Args: []string{"start", "anydesk"}, Optional: true})
	return cmds
}

func buildFirewallCommands(isRHEL bool, gwPort, apiPort, fePort, ollamaPort string) []hardenCommand {
	if isRHEL {
		return []hardenCommand{
			{Label: "firewalld public target", Command: "firewall-cmd", Args: []string{"--permanent", "--zone=public", "--set-target=default"}, Optional: true},
			{Label: "remove cockpit", Command: "firewall-cmd", Args: []string{"--permanent", "--zone=public", "--remove-service=cockpit"}, Optional: true},
			{Label: "remove 9090", Command: "firewall-cmd", Args: []string{"--permanent", "--zone=public", "--remove-port=9090/tcp"}, Optional: true},
			{Label: "remove 7070", Command: "firewall-cmd", Args: []string{"--permanent", "--zone=public", "--remove-port=7070/tcp"}, Optional: true},
			{Label: "allow ssh", Command: "firewall-cmd", Args: []string{"--permanent", "--zone=public", "--add-service=ssh"}, Optional: true},
			{Label: "trusted source localhost", Command: "firewall-cmd", Args: []string{"--permanent", "--zone=trusted", "--add-source=127.0.0.1/32"}, Optional: true},
			{Label: "trusted gw port", Command: "firewall-cmd", Args: []string{"--permanent", "--zone=trusted", "--add-port=" + gwPort + "/tcp"}, Optional: true},
			{Label: "trusted internal port", Command: "firewall-cmd", Args: []string{"--permanent", "--zone=trusted", "--add-port=14002/tcp"}, Optional: true},
			{Label: "trusted api port", Command: "firewall-cmd", Args: []string{"--permanent", "--zone=trusted", "--add-port=" + apiPort + "/tcp"}, Optional: true},
			{Label: "trusted fe port", Command: "firewall-cmd", Args: []string{"--permanent", "--zone=trusted", "--add-port=" + fePort + "/tcp"}, Optional: true},
			{Label: "trusted ollama port", Command: "firewall-cmd", Args: []string{"--permanent", "--zone=trusted", "--add-port=" + ollamaPort + "/tcp"}, Optional: true},
			{Label: "reload firewalld", Command: "firewall-cmd", Args: []string{"--reload"}, Optional: true},
		}
	}
	return []hardenCommand{
		{Label: "ufw default deny incoming", Command: "ufw", Args: []string{"default", "deny", "incoming"}, Optional: true},
		{Label: "ufw default allow outgoing", Command: "ufw", Args: []string{"default", "allow", "outgoing"}, Optional: true},
		{Label: "ufw allow ssh", Command: "ufw", Args: []string{"allow", "ssh"}, Optional: true},
		{Label: "ufw allow gateway localhost", Command: "ufw", Args: []string{"allow", "from", "127.0.0.1", "to", "any", "port", gwPort}, Optional: true},
		{Label: "ufw allow internal localhost", Command: "ufw", Args: []string{"allow", "from", "127.0.0.1", "to", "any", "port", "14002"}, Optional: true},
		{Label: "ufw allow api localhost", Command: "ufw", Args: []string{"allow", "from", "127.0.0.1", "to", "any", "port", apiPort}, Optional: true},
		{Label: "ufw allow fe localhost", Command: "ufw", Args: []string{"allow", "from", "127.0.0.1", "to", "any", "port", fePort}, Optional: true},
		{Label: "ufw allow ollama localhost", Command: "ufw", Args: []string{"allow", "from", "127.0.0.1", "to", "any", "port", ollamaPort}, Optional: true},
		{Label: "enable ufw", Command: "ufw", Args: []string{"--force", "enable"}, Optional: true},
	}
}

func buildSSHTask(username string) []hardenCommand {
	config := "PermitRootLogin no\nPasswordAuthentication no\nMaxAuthTries 3\nLoginGraceTime 30\nAllowUsers " + username + "\nX11Forwarding no\n"
	return []hardenCommand{
		{Label: "Backup sshd_config", Command: "cp", Args: []string{"/etc/ssh/sshd_config", "/etc/ssh/sshd_config.bak"}, Optional: true},
		{Label: "Write hardening.conf", Command: "tee", Args: []string{"/etc/ssh/sshd_config.d/hardening.conf"}, Stdin: config},
		{Label: "Restart sshd", Command: "systemctl", Args: []string{"restart", "sshd"}, Optional: true},
		{Label: "Restart ssh", Command: "systemctl", Args: []string{"restart", "ssh"}, Optional: true},
	}
}

func buildPermissionCommands(homeDir string) []hardenCommand {
	return []hardenCommand{
		{Label: "chmod ~/.openclaw", Command: "chmod", Args: []string{"700", filepath.Join(homeDir, ".openclaw")}, Optional: true},
		{Label: "chmod ~/.openclaw/*.json", Command: "find", Args: []string{filepath.Join(homeDir, ".openclaw"), "-maxdepth", "1", "-name", "*.json", "-exec", "chmod", "600", "{}", "+"}, Optional: true},
		{Label: "chmod ~/.openclaw/credentials", Command: "chmod", Args: []string{"700", filepath.Join(homeDir, ".openclaw", "credentials")}, Optional: true},
		{Label: "chmod credentials/*.env", Command: "find", Args: []string{filepath.Join(homeDir, ".openclaw", "credentials"), "-maxdepth", "1", "-name", "*.env", "-exec", "chmod", "600", "{}", "+"}, Optional: true},
	}
}

func buildSystemdCommands(info localUserInfo) []hardenCommand {
	dropIn := `[Service]
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=` + info.HomeDir + `/.openclaw /tmp
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
RestrictRealtime=true
`
	path := filepath.Join(info.HomeDir, ".config", "systemd", "user", "openclaw-gateway-harden.conf")
	return []hardenCommand{
		{Label: "create systemd user dir", Command: "install", Args: []string{"-d", "-m", "0755", filepath.Dir(path)}},
		{Label: "write systemd harden drop-in", Command: "tee", Args: []string{path}, Stdin: dropIn},
		{Label: "daemon-reload user systemd", Command: "runuser", Args: []string{"-u", info.Username, "--", "env", "XDG_RUNTIME_DIR=/run/user/" + info.UID, "systemctl", "--user", "daemon-reload"}, Optional: true},
	}
}

func buildCloudflaredCommands(isRHEL bool, homeDir string) []hardenCommand {
	cmds := []hardenCommand{}
	if _, err := exec.LookPath("cloudflared"); err != nil {
		if isRHEL {
			cmds = append(cmds, hardenCommand{
				Label:   "install cloudflared rpm",
				Command: "dnf",
				Args:    []string{"install", "-y", "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-x86_64.rpm"},
				Optional: true,
			})
		} else {
			cmds = append(cmds,
				hardenCommand{Label: "download cloudflared deb", Command: "curl", Args: []string{"-fsSL", "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb", "-o", "/tmp/cloudflared.deb"}, Optional: true},
				hardenCommand{Label: "install cloudflared deb", Command: "dpkg", Args: []string{"-i", "/tmp/cloudflared.deb"}, Optional: true},
				hardenCommand{Label: "cleanup cloudflared deb", Command: "rm", Args: []string{"-f", "/tmp/cloudflared.deb"}, Optional: true},
			)
		}
	}
	cmds = append(cmds, hardenCommand{Label: "create ~/.cloudflared", Command: "install", Args: []string{"-d", "-m", "0755", filepath.Join(homeDir, ".cloudflared")}, Optional: true})
	if isRHEL {
		cmds = append(cmds,
			hardenCommand{Label: "semanage cloudflared bin label", Command: "semanage", Args: []string{"fcontext", "-a", "-t", "bin_t", "/usr/bin/cloudflared"}, Optional: true},
			hardenCommand{Label: "restorecon cloudflared", Command: "restorecon", Args: []string{"-v", "/usr/bin/cloudflared"}, Optional: true},
		)
	}
	return cmds
}

func buildSELinuxCommands(info localUserInfo, gwPort, apiPort, fePort, ollamaPort string) []hardenCommand {
	paths := []hardenCommand{
		{Label: "label gw port", Command: "semanage", Args: []string{"port", "-a", "-t", "http_port_t", "-p", "tcp", gwPort}, Optional: true},
		{Label: "label internal port", Command: "semanage", Args: []string{"port", "-a", "-t", "http_port_t", "-p", "tcp", "14002"}, Optional: true},
		{Label: "label api port", Command: "semanage", Args: []string{"port", "-a", "-t", "http_port_t", "-p", "tcp", apiPort}, Optional: true},
		{Label: "label fe port", Command: "semanage", Args: []string{"port", "-a", "-t", "http_port_t", "-p", "tcp", fePort}, Optional: true},
		{Label: "label ollama port", Command: "semanage", Args: []string{"port", "-a", "-t", "http_port_t", "-p", "tcp", ollamaPort}, Optional: true},
		{Label: "label ~/.openclaw", Command: "semanage", Args: []string{"fcontext", "-a", "-t", "user_home_t", info.HomeDir + "/.openclaw(/.*)?"}, Optional: true},
		{Label: "label ~/.ssh", Command: "semanage", Args: []string{"fcontext", "-a", "-t", "ssh_home_t", info.HomeDir + "/.ssh(/.*)?"}, Optional: true},
		{Label: "restorecon ~/.openclaw", Command: "restorecon", Args: []string{"-Rv", info.HomeDir + "/.openclaw/"}, Optional: true},
		{Label: "restorecon ~/.ssh", Command: "restorecon", Args: []string{"-Rv", info.HomeDir + "/.ssh/"}, Optional: true},
		{Label: "restorecon ~/.cloudflared", Command: "restorecon", Args: []string{"-Rv", info.HomeDir + "/.cloudflared/"}, Optional: true},
	}
	return paths
}
