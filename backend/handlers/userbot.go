// handlers/userbot.go
// ──────────────────────────────────────────────────────────────────────────────
// UserBot Handler — mengontrol bots/userbot/cli.js via stdin/stdout (JSON IPC).
//
// Endpoints:
//   POST /api/userbot/start          — hidupkan proses userbot CLI
//   POST /api/userbot/stop           — matikan proses userbot CLI
//   GET  /api/userbot/status         — cek apakah userbot sedang berjalan & terautentikasi
//   POST /api/userbot/auth/sendCode  — kirim kode OTP ke nomor Telegram user
//   POST /api/userbot/auth/signIn    — masukkan kode OTP untuk login
//   POST /api/userbot/auth/signIn2FA — masukkan password 2FA jika diperlukan
//   POST /api/userbot/botfather/create  — buat bot baru via @BotFather
//   POST /api/userbot/botfather/list    — list bot yang dimiliki
//   POST /api/userbot/botfather/delete  — hapus bot via @BotFather
//   POST /api/userbot/botfather/revoke  — revoke & regenerate token bot
// ──────────────────────────────────────────────────────────────────────────────

package handlers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ── Process State ──────────────────────────────────────────────────────────────

type userBotProcess struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	ready  bool
	authed bool
}

var ubProc = &userBotProcess{}
var botUsernamePattern = regexp.MustCompile(`^@?[A-Za-z0-9_]{5,32}$`)
var botCommandPattern = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

func normalizeBotUsername(username string) (string, error) {
	username = strings.TrimSpace(username)
	if !botUsernamePattern.MatchString(username) {
		return "", fmt.Errorf("format username bot tidak valid")
	}
	if strings.HasPrefix(username, "@") {
		return username, nil
	}
	return "@" + username, nil
}

func validateTelegramText(val string, maxLen int, field string) (string, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return "", fmt.Errorf("field %q wajib diisi", field)
	}
	if len(val) > maxLen || strings.ContainsAny(val, "\x00") {
		return "", fmt.Errorf("field %q tidak valid", field)
	}
	return val, nil
}

// ── IPC Helpers ────────────────────────────────────────────────────────────────

// ipcRequest mengirim JSON command ke stdin userbot dan menunggu respons JSON dari stdout.
func (u *userBotProcess) ipcRequest(payload map[string]interface{}) (map[string]interface{}, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.cmd == nil || u.stdin == nil {
		return nil, fmt.Errorf("UserBot tidak berjalan. Jalankan dulu via /api/userbot/start")
	}

	data, _ := json.Marshal(payload)
	if _, err := fmt.Fprintf(u.stdin, "%s\n", data); err != nil {
		return nil, fmt.Errorf("gagal menulis ke stdin userbot: %w", err)
	}

	// Baca satu baris respons dengan timeout
	done := make(chan map[string]interface{}, 1)
	errCh := make(chan error, 1)
	go func() {
		if u.stdout.Scan() {
			var resp map[string]interface{}
			if err := json.Unmarshal([]byte(u.stdout.Text()), &resp); err != nil {
				errCh <- fmt.Errorf("gagal parse respons userbot: %w", err)
			} else {
				done <- resp
			}
		} else {
			errCh <- fmt.Errorf("stdout userbot tertutup")
		}
	}()

	select {
	case resp := <-done:
		return resp, nil
	case err := <-errCh:
		return nil, err
	case <-time.After(20 * time.Second):
		return nil, fmt.Errorf("timeout menunggu respons userbot (>20s). BotFather mungkin lambat merespons")
	}
}

// ── Start / Stop ───────────────────────────────────────────────────────────────

// UserBotHandler menampung dependency.
type UserBotHandler struct {
	App *App
}

// StartUserBot menyalakan proses userbot CLI.
func (h *UserBotHandler) StartUserBot(w http.ResponseWriter, r *http.Request) {
	ubProc.mu.Lock()
	defer ubProc.mu.Unlock()

	if ubProc.cmd != nil {
		jsonOK(w, map[string]interface{}{"message": "UserBot sudah berjalan", "running": true})
		return
	}

	cliPath := filepath.Join(h.App.Cfg.BotsDir, "userbot", "cli.js")
	if _, err := os.Stat(cliPath); os.IsNotExist(err) {
		jsonError(w, http.StatusInternalServerError, "userbot/cli.js tidak ditemukan di "+cliPath)
		return
	}

	nodeBin := h.App.Cfg.NodeBin
	if nodeBin == "" {
		nodeBin = "node"
	}

	cmd := exec.Command(nodeBin, cliPath)
	cmd.Dir = filepath.Dir(cliPath)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "gagal buat stdin pipe: "+err.Error())
		return
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "gagal buat stdout pipe: "+err.Error())
		return
	}

	if err := cmd.Start(); err != nil {
		jsonError(w, http.StatusInternalServerError, "gagal start userbot: "+err.Error())
		return
	}

	ubProc.cmd    = cmd
	ubProc.stdin  = stdin
	ubProc.stdout = bufio.NewScanner(stdoutPipe)

	// Baca pesan "ready" pertama dari userbot
	readyCh := make(chan map[string]interface{}, 1)
	go func() {
		if ubProc.stdout.Scan() {
			var resp map[string]interface{}
			json.Unmarshal([]byte(ubProc.stdout.Text()), &resp)
			readyCh <- resp
		}
	}()

	var readyResp map[string]interface{}
	select {
	case readyResp = <-readyCh:
	case <-time.After(25 * time.Second): // Telegram MTProto butuh 5-8 detik untuk koneksi awal
		cmd.Process.Kill()
		ubProc.cmd = nil
		jsonError(w, http.StatusInternalServerError, "userbot gagal siap dalam 25 detik — cek jaringan/credentials")
		return
	}

	// [CRITICAL] Jika cli.js mengembalikan ok:false saat startup (mis. API_ID kosong),
	// kita harus batalkan dan tampilkan error yang bermakna ke user — BUKAN mark sebagai running.
	if readyResp["ok"] != true {
		errMsg := "UserBot gagal start"
		if errVal, ok := readyResp["error"]; ok {
			errMsg = fmt.Sprintf("%v", errVal)
		}
		cmd.Process.Kill()
		ubProc.cmd    = nil
		ubProc.stdin  = nil
		ubProc.stdout = nil
		jsonError(w, http.StatusInternalServerError, errMsg)
		return
	}

	ubProc.ready = true
	if data, ok := readyResp["data"].(map[string]interface{}); ok {
		ubProc.authed = data["authenticated"] == true
	}

	// Watch process exit di background
	go func() {
		cmd.Wait()
		ubProc.mu.Lock()
		ubProc.cmd    = nil
		ubProc.stdin  = nil
		ubProc.stdout = nil
		ubProc.ready  = false
		ubProc.authed = false
		ubProc.mu.Unlock()
		fmt.Println("[UserBot] Process exited.")
	}()

	jsonOK(w, map[string]interface{}{
		"message":       "UserBot berhasil dijalankan",
		"running":       true,
		"authenticated": ubProc.authed,
	})
}

// StopUserBot mematikan proses userbot CLI.
func (h *UserBotHandler) StopUserBot(w http.ResponseWriter, r *http.Request) {
	ubProc.mu.Lock()
	defer ubProc.mu.Unlock()

	if ubProc.cmd == nil {
		jsonOK(w, map[string]string{"message": "UserBot tidak sedang berjalan"})
		return
	}

	fmt.Fprintf(ubProc.stdin, "%s\n", `{"cmd":"quit"}`)
	time.Sleep(500 * time.Millisecond)
	ubProc.cmd.Process.Kill()
	ubProc.cmd    = nil
	ubProc.stdin  = nil
	ubProc.stdout = nil
	ubProc.ready  = false
	ubProc.authed = false

	jsonOK(w, map[string]string{"message": "UserBot dihentikan"})
}

// GetUserBotStatus mengembalikan status proses dan auth saat ini.
func (h *UserBotHandler) GetUserBotStatus(w http.ResponseWriter, r *http.Request) {
	// Baca state tanpa blocking IPC (untuk menghindari deadlock saat ipcRequest juga butuh lock)
	ubProc.mu.Lock()
	running := ubProc.cmd != nil
	authed  := ubProc.authed
	ubProc.mu.Unlock()

	if !running {
		jsonOK(w, map[string]interface{}{"running": false, "authenticated": false})
		return
	}

	if !authed {
		// Belum login, tidak perlu ping (menghindari interferensi dengan alur OTP)
		jsonOK(w, map[string]interface{}{"running": true, "authenticated": false})
		return
	}

	// Sudah login: ping untuk mendapatkan info user terkini
	resp, err := ubProc.ipcRequest(map[string]interface{}{"cmd": "getCurrentUser"})
	if err != nil {
		// Proses mungkin sudah mati — kembalikan status jujur
		jsonOK(w, map[string]interface{}{"running": false, "authenticated": false, "error": err.Error()})
		return
	}
	jsonOK(w, map[string]interface{}{"running": true, "authenticated": resp["ok"] == true, "data": resp["data"]})
}

// ── Auth Endpoints ─────────────────────────────────────────────────────────────

func (h *UserBotHandler) SendCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Phone string `json:"phone"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Phone == "" {
		jsonError(w, http.StatusBadRequest, "Field \"phone\" wajib diisi (format: +628...)")
		return
	}
	body.Phone = strings.TrimSpace(body.Phone)
	if len(body.Phone) > 24 || !strings.HasPrefix(body.Phone, "+") {
		jsonError(w, http.StatusBadRequest, "Format phone tidak valid")
		return
	}

	resp, err := ubProc.ipcRequest(map[string]interface{}{"cmd": "sendCode", "phone": body.Phone})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resp["ok"] != true {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("%v", resp["error"]))
		return
	}
	jsonOK(w, resp["data"])
}

func (h *UserBotHandler) SignIn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code          string `json:"code"`
		PhoneCodeHash string `json:"phoneCodeHash"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Code == "" {
		jsonError(w, http.StatusBadRequest, "Field \"code\" wajib diisi")
		return
	}
	if len(strings.TrimSpace(body.Code)) > 16 {
		jsonError(w, http.StatusBadRequest, "Field \"code\" tidak valid")
		return
	}

	resp, err := ubProc.ipcRequest(map[string]interface{}{
		"cmd":           "signIn",
		"code":          body.Code,
		"phoneCodeHash": body.PhoneCodeHash,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resp["ok"] != true {
		code := http.StatusUnauthorized
		jsonError(w, code, fmt.Sprintf("%v", resp["error"]))
		return
	}
	ubProc.mu.Lock()
	ubProc.authed = true
	ubProc.mu.Unlock()
	jsonOK(w, resp["data"])
}

func (h *UserBotHandler) SignIn2FA(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Password == "" {
		jsonError(w, http.StatusBadRequest, "Field \"password\" wajib diisi")
		return
	}
	if len(body.Password) > 256 {
		jsonError(w, http.StatusBadRequest, "Field \"password\" terlalu panjang")
		return
	}

	resp, err := ubProc.ipcRequest(map[string]interface{}{"cmd": "signIn2FA", "password": body.Password})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resp["ok"] != true {
		jsonError(w, http.StatusUnauthorized, fmt.Sprintf("%v", resp["error"]))
		return
	}
	ubProc.mu.Lock()
	ubProc.authed = true
	ubProc.mu.Unlock()
	jsonOK(w, resp["data"])
}

// ── BotFather Endpoints ────────────────────────────────────────────────────────

func (h *UserBotHandler) CreateBot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Username string `json:"username"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Name == "" || body.Username == "" {
		jsonError(w, http.StatusBadRequest, "Field \"name\" dan \"username\" wajib diisi")
		return
	}
	name, err := validateTelegramText(body.Name, 64, "name")
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	username, err := normalizeBotUsername(body.Username)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := ubProc.ipcRequest(map[string]interface{}{
		"cmd":      "createBot",
		"name":     name,
		"username": username,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resp["ok"] != true {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("%v", resp["error"]))
		return
	}
	jsonOK(w, resp["data"])
}

func (h *UserBotHandler) ListBots(w http.ResponseWriter, r *http.Request) {
	resp, err := ubProc.ipcRequest(map[string]interface{}{"cmd": "listBots"})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resp["ok"] != true {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("%v", resp["error"]))
		return
	}
	jsonOK(w, resp["data"])
}

func (h *UserBotHandler) DeleteBot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Username == "" {
		jsonError(w, http.StatusBadRequest, "Field \"username\" wajib diisi")
		return
	}
	username, err := normalizeBotUsername(body.Username)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := ubProc.ipcRequest(map[string]interface{}{"cmd": "deleteBot", "username": username})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resp["ok"] != true {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("%v", resp["error"]))
		return
	}
	jsonOK(w, resp["data"])
}

func (h *UserBotHandler) RevokeToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Username == "" {
		jsonError(w, http.StatusBadRequest, "Field \"username\" wajib diisi")
		return
	}
	username, err := normalizeBotUsername(body.Username)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := ubProc.ipcRequest(map[string]interface{}{
		"cmd":        "setToken",
		"username":   username,
		"regenerate": true,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resp["ok"] != true {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("%v", resp["error"]))
		return
	}
	jsonOK(w, resp["data"])
}

// ── Extended BotFather Management ─────────────────────────────────────────────

func (h *UserBotHandler) GetBotInfo(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Username == "" {
		jsonError(w, http.StatusBadRequest, "Field \"username\" wajib diisi")
		return
	}
	username, err := normalizeBotUsername(body.Username)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := ubProc.ipcRequest(map[string]interface{}{"cmd": "getBotInfo", "username": username})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resp["ok"] != true {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("%v", resp["error"]))
		return
	}
	jsonOK(w, resp["data"])
}

func (h *UserBotHandler) GetBotToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Username == "" {
		jsonError(w, http.StatusBadRequest, "Field \"username\" wajib diisi")
		return
	}
	username, err := normalizeBotUsername(body.Username)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := ubProc.ipcRequest(map[string]interface{}{"cmd": "getBotToken", "username": username})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resp["ok"] != true {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("%v", resp["error"]))
		return
	}
	jsonOK(w, resp["data"])
}

func (h *UserBotHandler) SetBotName(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Username == "" || body.Name == "" {
		jsonError(w, http.StatusBadRequest, "Field \"username\" dan \"name\" wajib diisi")
		return
	}
	username, err := normalizeBotUsername(body.Username)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	name, err := validateTelegramText(body.Name, 64, "name")
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := ubProc.ipcRequest(map[string]interface{}{"cmd": "setBotName", "username": username, "name": name})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resp["ok"] != true {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("%v", resp["error"]))
		return
	}
	jsonOK(w, resp["data"])
}

func (h *UserBotHandler) SetBotDescription(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username    string `json:"username"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Username == "" || body.Description == "" {
		jsonError(w, http.StatusBadRequest, "Field \"username\" dan \"description\" wajib diisi")
		return
	}
	username, err := normalizeBotUsername(body.Username)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	description, err := validateTelegramText(body.Description, 512, "description")
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := ubProc.ipcRequest(map[string]interface{}{"cmd": "setBotDescription", "username": username, "description": description})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resp["ok"] != true {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("%v", resp["error"]))
		return
	}
	jsonOK(w, resp["data"])
}

func (h *UserBotHandler) SetBotAbout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		About    string `json:"about"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Username == "" || body.About == "" {
		jsonError(w, http.StatusBadRequest, "Field \"username\" dan \"about\" wajib diisi")
		return
	}
	username, err := normalizeBotUsername(body.Username)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	about, err := validateTelegramText(body.About, 256, "about")
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := ubProc.ipcRequest(map[string]interface{}{"cmd": "setBotAbout", "username": username, "about": about})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resp["ok"] != true {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("%v", resp["error"]))
		return
	}
	jsonOK(w, resp["data"])
}

func (h *UserBotHandler) SetBotCommands(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string              `json:"username"`
		Commands []map[string]string `json:"commands"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Username == "" || len(body.Commands) == 0 {
		jsonError(w, http.StatusBadRequest, "Field \"username\" dan \"commands\" (array) wajib diisi")
		return
	}
	username, err := normalizeBotUsername(body.Username)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(body.Commands) > 100 {
		jsonError(w, http.StatusBadRequest, "commands terlalu banyak")
		return
	}
	for _, cmd := range body.Commands {
		name := strings.TrimSpace(cmd["cmd"])
		desc := strings.TrimSpace(cmd["desc"])
		if !botCommandPattern.MatchString(name) || desc == "" || len(desc) > 256 {
			jsonError(w, http.StatusBadRequest, "format command tidak valid")
			return
		}
	}
	resp, err := ubProc.ipcRequest(map[string]interface{}{"cmd": "setBotCommands", "username": username, "commands": body.Commands})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resp["ok"] != true {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("%v", resp["error"]))
		return
	}
	jsonOK(w, resp["data"])
}

// POST /api/userbot/send
// Mengirim pesan sebagai UserBot (akun MTProto biasa, bukan bot) ke chatId target.
// Digunakan oleh sub-agent untuk melaporkan error snapshot tanpa menggunakan persona bot.
func (h *UserBotHandler) SendMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.ChatID == "" || body.Text == "" {
		jsonError(w, http.StatusBadRequest, "Field \"chat_id\" dan \"text\" wajib diisi")
		return
	}
	if len(strings.TrimSpace(body.ChatID)) > 128 || len(strings.TrimSpace(body.Text)) > 4000 {
		jsonError(w, http.StatusBadRequest, "chat_id atau text tidak valid")
		return
	}

	ubProc.mu.Lock()
	running := ubProc.ready
	ubProc.mu.Unlock()

	if !running {
		jsonError(w, http.StatusServiceUnavailable, "UserBot tidak aktif. Hidupkan UserBot terlebih dahulu.")
		return
	}

	resp, err := ubProc.ipcRequest(map[string]interface{}{
		"cmd":    "sendMessage",
		"chatId": body.ChatID,
		"text":   body.Text,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resp["ok"] != true {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("%v", resp["error"]))
		return
	}
	jsonOK(w, resp["data"])
}
