// handlers/gateway.go
// REST API handlers untuk Gateway Manager dan penerima Webhook publik

package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"multiagent-platform/internal/gateway"
)

type GatewayHandler struct {
	App        *App
	GwManager  *gateway.Manager
	PublicURL  string // Diambil dari env atau config, fallback ke localhost jika kosong
	seenMu     sync.Mutex
	seenEvents map[string]time.Time
}

var gatewayLabelPattern = regexp.MustCompile(`^[\p{L}\p{N} _\-.]{1,64}$`)
var senderPattern = regexp.MustCompile(`^[\p{L}\p{N}@+._:\-]{1,128}$`)

// ── CRUD Endpoints (Secured by Token) ────────────────────────────────────────

func (h *GatewayHandler) GetGateways(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	list := h.GwManager.List(agentID)

	response := make([]map[string]interface{}, 0, len(list))
	for _, g := range list {
		response = append(response, map[string]interface{}{
			"id":          g.ID,
			"agent_id":    g.AgentID,
			"platform":    g.Platform,
			"label":       g.Label,
			"webhook_url": g.WebhookURL(h.PublicURL),
			"active":      g.Active,
			"created_at":  g.CreatedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	jsonOK(w, response)
}

func (h *GatewayHandler) CreateGateway(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AgentID  string            `json:"agent_id"`
		Platform string            `json:"platform"`
		Label    string            `json:"label"`
		Config   map[string]string `json:"config"`
	}

	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "Invalid payload")
		return
	}

	if req.AgentID == "" || req.Platform == "" || req.Label == "" {
		jsonError(w, http.StatusBadRequest, "AgentID, Platform, dan Label wajib diisi")
		return
	}
	if !gatewayLabelPattern.MatchString(req.Label) {
		jsonError(w, http.StatusBadRequest, "Label gateway hanya boleh huruf/angka/spasi/-/_/. dan maksimal 64 karakter")
		return
	}
	req.Platform = strings.ToLower(strings.TrimSpace(req.Platform))
	if req.Config == nil {
		req.Config = map[string]string{}
	}
	for k, v := range req.Config {
		req.Config[k] = strings.TrimSpace(v)
		if len(v) > 2048 {
			jsonError(w, http.StatusBadRequest, "Konfigurasi gateway terlalu panjang: "+k)
			return
		}
	}
	switch req.Platform {
	case gateway.PlatformHTTP:
		if tok := req.Config["auth_token"]; tok != "" && len(tok) > 256 {
			jsonError(w, http.StatusBadRequest, "auth_token terlalu panjang")
			return
		}
	case gateway.PlatformWhatsApp:
		required := []string{"phone_number_id", "access_token", "verify_token", "app_secret", "relay_mode"}
		for _, key := range required {
			if strings.TrimSpace(req.Config[key]) == "" {
				jsonError(w, http.StatusBadRequest, "Konfigurasi WhatsApp belum lengkap: "+key)
				return
			}
		}
		if req.Config["relay_mode"] != "agent_engine" {
			jsonError(w, http.StatusBadRequest, "relay_mode WhatsApp harus 'agent_engine'")
			return
		}
	default:
		jsonError(w, http.StatusBadRequest, "Platform tidak disupport")
		return
	}

	g, err := h.GwManager.Add(req.AgentID, req.Platform, req.Label, req.Config)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Gagal menyimpan gateway: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	jsonOK(w, map[string]interface{}{
		"message":     "Gateway berhasil dibuat",
		"id":          g.ID,
		"webhook_url": g.WebhookURL(h.PublicURL),
		"note":        "Rahasia gateway disimpan aman di backend dan tidak ditampilkan kembali.",
	})
}

func (h *GatewayHandler) DeleteGateway(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/gateway/")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "ID gateway diperlukan")
		return
	}

	if err := h.GwManager.Delete(id); err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}

	jsonOK(w, map[string]string{"message": "Gateway berhasil dihapus"})
}

// ── Webhook Endpoint (Publicly Accessible) ───────────────────────────────────

// HandleWebhook menerima POST request dari platform eksternal (WA, HTTP, dll)
// Endpoint format: /api/webhook/{agentID}/{platform}
func (h *GatewayHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/webhook/"), "/")
	if len(parts) < 2 {
		http.Error(w, "Invalid webhook URL", http.StatusBadRequest)
		return
	}
	agentID := parts[0]
	platform := parts[1]

	verifiedGateway, ok := h.GwManager.FindActive(agentID, platform)
	if !ok {
		http.Error(w, "Gateway tidak ditemukan atau tidak aktif untuk agen ini", http.StatusNotFound)
		return
	}

	switch platform {
	case gateway.PlatformHTTP:
		h.handleHTTPGenericWebhook(w, r, agentID, verifiedGateway)
	case gateway.PlatformWhatsApp:
		if r.Method == http.MethodGet {
			h.handleWhatsAppVerification(w, r, verifiedGateway)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h.handleWhatsAppWebhook(w, r, agentID, verifiedGateway)
	default:
		http.Error(w, "Platform tidak disupport", http.StatusBadRequest)
	}
}

func (h *GatewayHandler) handleHTTPGenericWebhook(w http.ResponseWriter, r *http.Request, agentID string, gw *gateway.Gateway) {
	rawBody, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "Gagal membaca request body", http.StatusBadRequest)
		return
	}
	if authToken := strings.TrimSpace(gw.Config["auth_token"]); authToken != "" {
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(bearer), []byte(authToken)) != 1 {
			http.Error(w, "Invalid bearer token", http.StatusUnauthorized)
			return
		}
	}

	var payload struct {
		Message string `json:"message"`
		Sender  string `json:"sender"`
	}
	if err := json.NewDecoder(bytes.NewReader(rawBody)).Decode(&payload); err != nil {
		http.Error(w, "Invalid body: expects {message: string, sender: string}", http.StatusBadRequest)
		return
	}
	payload.Message = strings.TrimSpace(payload.Message)
	payload.Sender = strings.TrimSpace(payload.Sender)
	if payload.Message == "" || len(payload.Message) > 4000 {
		http.Error(w, "Invalid message", http.StatusBadRequest)
		return
	}
	if payload.Sender == "" || !senderPattern.MatchString(payload.Sender) {
		http.Error(w, "Invalid sender", http.StatusBadRequest)
		return
	}

	response, err := h.sendToAgentEngine(agentID, "http", payload.Sender, payload.Message)
	if err != nil {
		fmt.Printf("[Gateway][HTTP][%s] upstream error: %v\n", agentID, err)
		http.Error(w, "Agent sedang gagal memproses pesan", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	jsonOK(w, map[string]string{
		"agent_reply": response,
		"status":      "processed",
	})
}

func (h *GatewayHandler) handleWhatsAppVerification(w http.ResponseWriter, r *http.Request, gw *gateway.Gateway) {
	mode := r.URL.Query().Get("hub.mode")
	challenge := r.URL.Query().Get("hub.challenge")
	verifyToken := r.URL.Query().Get("hub.verify_token")
	if mode != "subscribe" || challenge == "" {
		http.Error(w, "invalid verification request", http.StatusBadRequest)
		return
	}
	if subtle.ConstantTimeCompare([]byte(verifyToken), []byte(gw.Config["verify_token"])) != 1 {
		http.Error(w, "verification token mismatch", http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(challenge))
}

func (h *GatewayHandler) handleWhatsAppWebhook(w http.ResponseWriter, r *http.Request, agentID string, gw *gateway.Gateway) {
	rawBody, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "Gagal membaca webhook", http.StatusBadRequest)
		return
	}

	if err := verifyMetaSignature(r.Header.Get("X-Hub-Signature-256"), gw.Config["app_secret"], rawBody); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	var payload struct {
		Entry []struct {
			Changes []struct {
				Value struct {
					Messages []struct {
						ID   string `json:"id"`
						From string `json:"from"`
						Type string `json:"type"`
						Text struct {
							Body string `json:"body"`
						} `json:"text"`
					} `json:"messages"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		http.Error(w, "payload WhatsApp tidak valid", http.StatusBadRequest)
		return
	}

	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			for _, message := range change.Value.Messages {
				if message.ID == "" || message.From == "" || message.Type != "text" || strings.TrimSpace(message.Text.Body) == "" {
					continue
				}
				if len(message.Text.Body) > 4000 || !senderPattern.MatchString(strings.TrimSpace(message.From)) {
					continue
				}
				if h.isDuplicateEvent(message.ID) {
					continue
				}
				reply, err := h.sendToAgentEngine(agentID, "whatsapp", message.From, message.Text.Body)
				if err != nil {
					fmt.Printf("[Gateway][WA][%s] agent engine error: %v\n", agentID, err)
					http.Error(w, "agent engine gagal memproses pesan", http.StatusBadGateway)
					return
				}
				if err := h.sendWhatsAppReply(gw, message.From, reply); err != nil {
					fmt.Printf("[Gateway][WA][%s] send reply error: %v\n", agentID, err)
					http.Error(w, "gagal mengirim balasan WhatsApp", http.StatusBadGateway)
					return
				}
			}
		}
	}
	jsonOK(w, map[string]string{"status": "accepted"})
}

func (h *GatewayHandler) sendToAgentEngine(agentID, channel, senderID, message string) (string, error) {
	ingressURL, ok := h.App.Manager.GetIngressURL(agentID)
	if !ok {
		return "", fmt.Errorf("agent '%s' tidak sedang berjalan", agentID)
	}
	payload := map[string]string{
		"channel":    channel,
		"session_id": senderID,
		"sender_id":  senderID,
		"message":    message,
	}
	bodyData, _ := json.Marshal(payload)

	req, err := http.NewRequest(http.MethodPost, ingressURL+"/message", bytes.NewBuffer(bodyData))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.App.InternalRPCToken)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("agent ingress status %d: %s", resp.StatusCode, string(body))
	}
	var result struct {
		OK    bool   `json:"ok"`
		Reply string `json:"reply"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("invalid agent ingress response: %w", err)
	}
	if !result.OK && result.Error != "" {
		return "", fmt.Errorf("%s", result.Error)
	}
	return result.Reply, nil
}

func verifyMetaSignature(headerValue, appSecret string, rawBody []byte) error {
	if appSecret == "" {
		return fmt.Errorf("app secret tidak dikonfigurasi")
	}
	const prefix = "sha256="
	if !strings.HasPrefix(headerValue, prefix) {
		return fmt.Errorf("missing or invalid X-Hub-Signature-256")
	}
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(rawBody)
	expected := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(expected), []byte(strings.TrimPrefix(headerValue, prefix))) != 1 {
		return fmt.Errorf("invalid webhook signature")
	}
	return nil
}

func (h *GatewayHandler) isDuplicateEvent(eventID string) bool {
	if eventID == "" {
		return false
	}
	h.seenMu.Lock()
	defer h.seenMu.Unlock()
	if h.seenEvents == nil {
		h.seenEvents = make(map[string]time.Time)
	}
	now := time.Now()
	for id, ts := range h.seenEvents {
		if now.Sub(ts) > 10*time.Minute {
			delete(h.seenEvents, id)
		}
	}
	if _, ok := h.seenEvents[eventID]; ok {
		return true
	}
	h.seenEvents[eventID] = now
	return false
}

func (h *GatewayHandler) sendWhatsAppReply(gw *gateway.Gateway, recipientID, text string) error {
	payload := map[string]interface{}{
		"messaging_product": "whatsapp",
		"to":                recipientID,
		"type":              "text",
		"text": map[string]string{
			"body": text,
		},
	}
	bodyData, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://graph.facebook.com/v22.0/%s/messages", gw.Config["phone_number_id"])
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(bodyData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gw.Config["access_token"])
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("meta api status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
