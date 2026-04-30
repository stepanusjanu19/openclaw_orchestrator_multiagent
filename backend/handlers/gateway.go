// handlers/gateway.go
// REST API handlers untuk Gateway Manager dan penerima Webhook publik

package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"multiagent-platform/internal/config"
	"multiagent-platform/internal/gateway"
	"multiagent-platform/internal/security"
)

type GatewayHandler struct {
	App          *App
	GwManager    *gateway.Manager
	PublicURL    string // Diambil dari env atau config, fallback ke localhost jika kosong
	CredsGetter  func() map[string]*config.AgentCred
	seenMu       sync.Mutex
	seenEvents   map[string]time.Time
	inflightMu   sync.Mutex
	inflightByID map[string]int
}

type gatewayAgentAvailability struct {
	Registered bool
	Running    bool
	Reserved   bool
}

var gatewayLabelPattern = regexp.MustCompile(`^[\p{L}\p{N} _\-.]{1,64}$`)
var senderPattern = regexp.MustCompile(`^[\p{L}\p{N}@+._:\-]{1,128}$`)
var webhookPathPattern = regexp.MustCompile(`^[a-f0-9]{32,64}$`)

const (
	defaultWebhookTTLDays = 90
	maxWebhookTTLDays     = 365
	webhookSignatureSkew  = 5 * time.Minute
)

// ── CRUD Endpoints (Secured by Token) ────────────────────────────────────────

func (h *GatewayHandler) GetGateways(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	list := h.GwManager.List(agentID)

	response := make([]map[string]interface{}, 0, len(list))
	now := time.Now()
	creds := h.credsSnapshot()
	for _, g := range list {
		expired := g.Expired(now)
		availability := h.gatewayAvailability(g, expired, creds)
		response = append(response, map[string]interface{}{
			"id":                  g.ID,
			"agent_id":            g.AgentID,
			"platform":            g.Platform,
			"label":               g.Label,
			"webhook_url":         g.WebhookURL(h.PublicURL),
			"active":              g.Active,
			"expires_at":          g.ExpiresAt,
			"expired":             expired,
			"public":              strings.EqualFold(g.Config["public"], "true"),
			"auth_mode":           gatewayAuthMode(g),
			"created_at":          g.CreatedAt,
			"agent_registered":    availability.Registered,
			"agent_running":       availability.Running,
			"runtime_available":   availability.Available,
			"availability_reason": availability.Reason,
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
	req.AgentID = strings.ToLower(strings.TrimSpace(req.AgentID))
	req.Platform = strings.ToLower(strings.TrimSpace(req.Platform))
	req.Label = strings.TrimSpace(req.Label)
	if !validAgentID.MatchString(req.AgentID) {
		jsonError(w, http.StatusBadRequest, "Agent ID tidak valid")
		return
	}
	if isReservedGatewayAgent(req.AgentID) {
		jsonError(w, http.StatusBadRequest, "Main Agent tidak bisa dipakai untuk Gateway")
		return
	}
	agentStatus := h.agentAvailability(req.AgentID)
	if !agentStatus.Registered {
		jsonError(w, http.StatusNotFound, "agent tidak terdaftar: "+req.AgentID)
		return
	}
	if !agentStatus.Running {
		jsonError(w, http.StatusConflict, "agent harus running sebelum membuat gateway: "+req.AgentID)
		return
	}
	if !gatewayLabelPattern.MatchString(req.Label) {
		jsonError(w, http.StatusBadRequest, "Label gateway hanya boleh huruf/angka/spasi/-/_/. dan maksimal 64 karakter")
		return
	}
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
	expiresAt, ttlDays, err := parseGatewayTTL(req.Config["ttl_days"])
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Config["ttl_days"] = strconv.Itoa(ttlDays)
	switch req.Platform {
	case gateway.PlatformHTTP:
		if existing, ok := h.GwManager.ActiveByAgentPlatform(req.AgentID, gateway.PlatformHTTP); ok {
			jsonError(w, http.StatusConflict, "agent sudah punya HTTP gateway aktif: "+existing.ID)
			return
		}
		publicMode := strings.EqualFold(req.Config["public"], "true")
		signatureRaw, signatureProvided := req.Config["signature_required"]
		signatureRequired := strings.TrimSpace(signatureRaw)
		if publicMode && signatureProvided && strings.EqualFold(signatureRequired, "true") {
			jsonError(w, http.StatusBadRequest, "public webhook tidak boleh memakai signature_required=true")
			return
		}
		if publicMode {
			req.Config["public"] = "true"
			req.Config["signature_required"] = "false"
		} else {
			req.Config["public"] = "false"
			req.Config["signature_required"] = "true"
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

	g, err := h.GwManager.Add(req.AgentID, req.Platform, req.Label, req.Config, expiresAt)
	if err != nil {
		if strings.Contains(err.Error(), "HTTP gateway aktif") {
			jsonError(w, http.StatusConflict, err.Error())
			return
		}
		jsonError(w, http.StatusInternalServerError, "Gagal menyimpan gateway: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	jsonOK(w, map[string]interface{}{
		"message":        "Gateway berhasil dibuat",
		"id":             g.ID,
		"webhook_url":    g.WebhookURL(h.PublicURL),
		"expires_at":     g.ExpiresAt,
		"auth_mode":      gatewayAuthMode(g),
		"webhook_secret": oneTimeWebhookSecret(g),
		"note":           "Webhook secret hanya ditampilkan sekali saat pembuatan. Simpan seperti Personal Access Token.",
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
// Endpoint format: /api/webhook/{agentID}/{platform}/{webhookPath}
func (h *GatewayHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/webhook/"), "/")
	if len(parts) < 2 {
		http.Error(w, "Invalid webhook URL", http.StatusBadRequest)
		return
	}
	agentID := parts[0]
	platform := parts[1]
	webhookPath := ""
	if len(parts) >= 3 {
		webhookPath = parts[2]
	}
	if webhookPath == "" {
		http.Error(w, "Gateway tidak ditemukan atau tidak aktif untuk agen ini", http.StatusNotFound)
		return
	}
	if !webhookPathPattern.MatchString(webhookPath) {
		http.Error(w, "Invalid webhook URL", http.StatusNotFound)
		return
	}

	var verifiedGateway *gateway.Gateway
	var ok bool
	if found, foundOK := h.GwManager.FindByPath(agentID, platform, webhookPath); foundOK {
		if found.Expired(time.Now()) {
			ctx := buildGatewayRequestContext(r, h.App.Cfg, found, nil, "", strings.EqualFold(found.Config["public"], "true"), nil)
			h.auditGatewayWithMetadata(agentID, platform, "", "", "expired", errors.New("gateway expired"), ctx, found)
			http.Error(w, "Gateway tidak ditemukan atau tidak aktif untuk agen ini", http.StatusNotFound)
			return
		}
		if !found.Active {
			ctx := buildGatewayRequestContext(r, h.App.Cfg, found, nil, "", strings.EqualFold(found.Config["public"], "true"), nil)
			h.auditGatewayWithMetadata(agentID, platform, "", "", "blocked", errors.New("gateway inactive"), ctx, found)
			http.Error(w, "Gateway tidak ditemukan atau tidak aktif untuk agen ini", http.StatusNotFound)
			return
		}
	}
	verifiedGateway, ok = h.GwManager.FindActiveByPath(agentID, platform, webhookPath)
	if !ok {
		http.Error(w, "Gateway tidak ditemukan atau tidak aktif untuk agen ini", http.StatusNotFound)
		return
	}
	if !h.agentAvailability(agentID).Running {
		ctx := buildGatewayRequestContext(r, h.App.Cfg, verifiedGateway, nil, "", strings.EqualFold(verifiedGateway.Config["public"], "true"), nil)
		h.auditGatewayWithMetadata(agentID, platform, "", "", "agent_off", errors.New("agent offline"), ctx, verifiedGateway)
		http.Error(w, "Agent offline. Start agent sebelum memakai gateway ini.", http.StatusServiceUnavailable)
		return
	}

	if platform == gateway.PlatformHTTP && len(parts) >= 4 {
		action := parts[3]
		if len(parts) > 4 {
			http.Error(w, "Invalid webhook URL", http.StatusNotFound)
			return
		}
		switch action {
		case "visit":
			http.Redirect(w, r, strings.TrimRight(verifiedGateway.WebhookURL(h.PublicURL), "/"), http.StatusFound)
		case "visit.js":
			h.handleHTTPVisitScript(w, r, agentID, verifiedGateway)
		case "location":
			h.handleHTTPLocationCapture(w, r, agentID, verifiedGateway)
		default:
			http.Error(w, "Invalid webhook URL", http.StatusNotFound)
		}
		return
	}

	switch platform {
	case gateway.PlatformHTTP:
		if r.Method == http.MethodGet && strings.EqualFold(verifiedGateway.Config["public"], "true") {
			h.handleHTTPVisitPage(w, r, agentID, verifiedGateway)
			return
		}
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

type gatewayAvailabilityInfo struct {
	Registered bool
	Running    bool
	Available  bool
	Reason     string
}

func (h *GatewayHandler) agentAvailability(agentID string) gatewayAgentAvailability {
	return h.agentAvailabilityFromCreds(agentID, h.credsSnapshot())
}

func (h *GatewayHandler) credsSnapshot() map[string]*config.AgentCred {
	if h.CredsGetter == nil {
		return nil
	}
	return h.CredsGetter()
}

func (h *GatewayHandler) agentAvailabilityFromCreds(agentID string, creds map[string]*config.AgentCred) gatewayAgentAvailability {
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	reserved := isReservedGatewayAgent(agentID)
	registered := true
	if creds != nil {
		_, registered = creds[agentID]
	}
	running := false
	if registered && !reserved && h.App != nil && h.App.Manager != nil {
		running = h.App.Manager.GetStatus(agentID).Running
	}
	return gatewayAgentAvailability{Registered: registered, Running: running, Reserved: reserved}
}

func (h *GatewayHandler) gatewayAvailability(g *gateway.Gateway, expired bool, creds map[string]*config.AgentCred) gatewayAvailabilityInfo {
	agent := h.agentAvailabilityFromCreds(g.AgentID, creds)
	info := gatewayAvailabilityInfo{
		Registered: agent.Registered,
		Running:    agent.Running,
		Available:  g.Active && !expired && agent.Registered && agent.Running && !agent.Reserved,
		Reason:     "available",
	}
	switch {
	case expired:
		info.Reason = "expired"
	case !g.Active:
		info.Reason = "inactive"
	case agent.Reserved:
		info.Reason = "reserved_main"
	case !agent.Registered:
		info.Reason = "agent_unregistered"
	case !agent.Running:
		info.Reason = "agent_stopped"
	}
	return info
}

func isReservedGatewayAgent(agentID string) bool {
	return strings.EqualFold(strings.TrimSpace(agentID), "main")
}

func (h *GatewayHandler) handleHTTPGenericWebhook(w http.ResponseWriter, r *http.Request, agentID string, gw *gateway.Gateway) {
	publicMode := strings.EqualFold(gw.Config["public"], "true")
	if r.Method != http.MethodPost {
		ctx := buildGatewayRequestContext(r, h.App.Cfg, gw, nil, "", publicMode, nil)
		h.auditGatewayWithMetadata(agentID, "http", "", "", "blocked", errors.New("method not allowed"), ctx, gw)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if publicMode && !isJSONContentType(r.Header.Get("Content-Type")) {
		ctx := buildGatewayRequestContext(r, h.App.Cfg, gw, nil, "", true, nil)
		h.auditGatewayWithMetadata(agentID, "http", "", "", "blocked", errors.New("invalid content type"), ctx, gw)
		http.Error(w, "Unsupported media type", http.StatusUnsupportedMediaType)
		return
	}
	bodyLimit := signedWebhookBodyLimit
	if publicMode {
		bodyLimit = publicWebhookBodyLimit
	}
	rawBody, err := readWebhookBody(r, bodyLimit)
	if err != nil {
		ctx := buildGatewayRequestContext(r, h.App.Cfg, gw, nil, "", publicMode, nil)
		h.auditGatewayWithMetadata(agentID, "http", "", "", "blocked", err, ctx, gw)
		http.Error(w, "Gagal membaca request body", http.StatusBadRequest)
		return
	}
	if !publicMode {
		if err := h.verifyOpenClawSignature(r, gw, rawBody); err != nil {
			ctx := buildGatewayRequestContext(r, h.App.Cfg, gw, rawBody, "", false, nil)
			h.auditGatewayWithMetadata(agentID, "http", "", "", "invalid_signature", err, ctx, gw)
			http.Error(w, "Webhook signature invalid", http.StatusUnauthorized)
			return
		}
	} else if !strings.EqualFold(gw.Config["signature_required"], "false") {
		ctx := buildGatewayRequestContext(r, h.App.Cfg, gw, rawBody, "", true, nil)
		h.auditGatewayWithMetadata(agentID, "http", "", "", "invalid_signature", errors.New("signature required"), ctx, gw)
		http.Error(w, "Webhook signature required", http.StatusUnauthorized)
		return
	}

	var payload struct {
		Message         string                  `json:"message"`
		Sender          string                  `json:"sender"`
		VisitorLocation *visitorLocationPayload `json:"visitor_location,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(rawBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		ctx := buildGatewayRequestContext(r, h.App.Cfg, gw, rawBody, "", publicMode, nil)
		h.auditGatewayWithMetadata(agentID, "http", "", "", "blocked", errors.New("invalid body shape"), ctx, gw)
		http.Error(w, "Invalid body: expects {message: string, sender: string, visitor_location?: object}", http.StatusBadRequest)
		return
	}
	visitorLocation, err := normalizeVisitorLocation(payload.VisitorLocation, time.Now())
	if err != nil {
		ctx := buildGatewayRequestContext(r, h.App.Cfg, gw, rawBody, "", publicMode, nil)
		h.auditGatewayWithMetadata(agentID, "http", "", "", "blocked", err, ctx, gw)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	payload.Message = strings.TrimSpace(payload.Message)
	payload.Sender = strings.TrimSpace(payload.Sender)
	if payload.Message == "" || len(payload.Message) > 4000 {
		ctx := buildGatewayRequestContext(r, h.App.Cfg, gw, rawBody, payload.Message, publicMode, visitorLocation)
		h.auditGatewayWithMetadata(agentID, "http", payload.Message, "", "blocked", errors.New("invalid message"), ctx, gw)
		http.Error(w, "Invalid message", http.StatusBadRequest)
		return
	}
	if payload.Sender == "" || !senderPattern.MatchString(payload.Sender) {
		ctx := buildGatewayRequestContext(r, h.App.Cfg, gw, rawBody, payload.Message, publicMode, visitorLocation)
		h.auditGatewayWithMetadata(agentID, "http", payload.Message, "", "blocked", errors.New("invalid sender"), ctx, gw)
		http.Error(w, "Invalid sender", http.StatusBadRequest)
		return
	}

	ctx := buildGatewayRequestContext(r, h.App.Cfg, gw, rawBody, payload.Message, publicMode, visitorLocation)
	w.Header().Set("X-OpenClaw-Request-ID", ctx.RequestID)
	if ctx.Risk.Decision == "block" {
		h.auditGatewayWithMetadata(agentID, "http", payload.Message, "", "blocked", errors.New(strings.Join(ctx.Risk.Reasons, ",")), ctx, gw)
		http.Error(w, "Request blocked by gateway policy", http.StatusForbidden)
		return
	}
	if !h.tryAcquireGatewaySlot(gw.ID) {
		ctx.Risk.Decision = "block"
		ctx.Risk.Reasons = append(ctx.Risk.Reasons, "gateway_concurrency_limit")
		ctx.Risk.Score += 80
		h.auditGatewayWithMetadata(agentID, "http", payload.Message, "", "rate_limited", errors.New("gateway concurrency limit"), ctx, gw)
		http.Error(w, "429 Too Many Requests - Gateway throttled", http.StatusTooManyRequests)
		return
	}
	defer h.releaseGatewaySlot(gw.ID)

	agentMessage := secureEnvelope(payload.Message, ctx.Risk)
	response, err := h.sendToAgentEngine(agentID, "http", payload.Sender, agentMessage)
	if err != nil {
		fmt.Printf("[Gateway][HTTP][%s] upstream error: %v\n", agentID, security.MaskKnownSecrets(err.Error()))
		status := "error"
		if isGatewayTimeout(err) {
			status = "upstream_timeout"
		}
		h.auditGatewayWithMetadata(agentID, "http", payload.Message, "", status, err, ctx, gw)
		http.Error(w, "Agent sedang gagal memproses pesan", http.StatusInternalServerError)
		return
	}
	response, err = h.applyOutputPolicy(agentID, "http", payload.Sender, response, r.URL.Query().Get("template_id"))
	if err != nil {
		h.auditGatewayWithMetadata(agentID, "http", payload.Message, response, "blocked", err, ctx, gw)
		http.Error(w, "Output diblokir oleh masking policy", http.StatusForbidden)
		return
	}
	h.auditGatewayWithMetadata(agentID, "http", payload.Message, response, "success", nil, ctx, gw)

	w.Header().Set("Content-Type", "application/json")
	jsonOK(w, map[string]string{
		"agent_reply": response,
		"status":      "processed",
	})
}

func (h *GatewayHandler) handleHTTPVisitPage(w http.ResponseWriter, r *http.Request, agentID string, gw *gateway.Gateway) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !strings.EqualFold(gw.Config["public"], "true") {
		http.Error(w, "Gateway tidak ditemukan atau tidak aktif untuk agen ini", http.StatusNotFound)
		return
	}
	ctx := buildGatewayRequestContext(r, h.App.Cfg, gw, nil, "", true, nil)
	h.auditGatewayWithMetadata(agentID, "http", "", "", "visit_loaded", nil, ctx, gw)
	locationToken := h.locationSubmitToken(gw, time.Now())
	scriptPath := html.EscapeString(strings.TrimRight(r.URL.EscapedPath(), "/") + "/visit.js?t=" + url.QueryEscape(locationToken))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="id">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>OpenClaw Visitor Check-in</title>
</head>
<body>
  <main>
    <h1>OpenClaw Visitor Check-in</h1>
    <p id="status">Meminta izin lokasi perangkat untuk check-in visitor.</p>
    <noscript>JavaScript diperlukan untuk mengirim koordinat GPS.</noscript>
  </main>
  <script src="%s" defer></script>
</body>
</html>`, scriptPath)
	_ = agentID
}

func (h *GatewayHandler) handleHTTPVisitScript(w http.ResponseWriter, r *http.Request, agentID string, gw *gateway.Gateway) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !strings.EqualFold(gw.Config["public"], "true") {
		http.Error(w, "Gateway tidak ditemukan atau tidak aktif untuk agen ini", http.StatusNotFound)
		return
	}
	locationToken := strings.TrimSpace(r.URL.Query().Get("t"))
	if !h.validLocationSubmitToken(gw, locationToken, time.Now()) {
		http.Error(w, "Invalid location token", http.StatusForbidden)
		return
	}
	tokenJSON, _ := json.Marshal(locationToken)
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = fmt.Fprintf(w, `(function(){
  var locationToken = %s;
  var statusEl = document.getElementById('status');
  function setStatus(text){ if (statusEl) statusEl.textContent = text; }
  function postStatus(status){
    return fetch(window.location.pathname.replace(/\/visit\.js$/, '').replace(/\/$/, '') + '/location', {
      method: 'POST',
      headers: {'Content-Type': 'application/json', 'X-OpenClaw-Location-Token': locationToken},
      credentials: 'same-origin',
      body: JSON.stringify({
        consent: false,
        status: status,
        captured_at: new Date().toISOString()
      })
    });
  }
  function postLocation(pos){
    var coords = pos.coords || {};
    return fetch(window.location.pathname.replace(/\/visit\.js$/, '').replace(/\/$/, '') + '/location', {
      method: 'POST',
      headers: {'Content-Type': 'application/json', 'X-OpenClaw-Location-Token': locationToken},
      credentials: 'same-origin',
      body: JSON.stringify({
        consent: true,
        latitude: coords.latitude,
        longitude: coords.longitude,
        accuracy_m: coords.accuracy || 0,
        captured_at: new Date(pos.timestamp || Date.now()).toISOString()
      })
    }).then(function(resp){
      if (!resp.ok) throw new Error('location rejected');
      setStatus('Lokasi visitor tersimpan.');
    });
  }
  if (!('geolocation' in navigator)) {
    setStatus('Browser tidak mendukung geolocation.');
    postStatus('gps_unavailable').catch(function(){});
    return;
  }
  navigator.geolocation.getCurrentPosition(function(pos){
    postLocation(pos).catch(function(){ setStatus('Gagal mengirim lokasi visitor.'); });
  }, function(err){
    var status = err && err.code === 1 ? 'gps_denied' : 'gps_unavailable';
    postStatus(status).catch(function(){});
    setStatus('Izin lokasi tidak diberikan. Visitor tetap dicatat dengan IP geolocation.');
  }, {enableHighAccuracy: true, timeout: 12000, maximumAge: 30000});
})();`, string(tokenJSON))
	_ = agentID
}

func (h *GatewayHandler) handleHTTPLocationCapture(w http.ResponseWriter, r *http.Request, agentID string, gw *gateway.Gateway) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !strings.EqualFold(gw.Config["public"], "true") {
		http.Error(w, "Gateway tidak ditemukan atau tidak aktif untuk agen ini", http.StatusNotFound)
		return
	}
	if !h.validLocationSubmitToken(gw, r.Header.Get("X-OpenClaw-Location-Token"), time.Now()) {
		ctx := buildGatewayRequestContext(r, h.App.Cfg, gw, nil, "", true, nil)
		h.auditGatewayWithMetadata(agentID, "http", "", "", "blocked", errors.New("invalid location submit token"), ctx, gw)
		http.Error(w, "Invalid location token", http.StatusForbidden)
		return
	}
	if !sameOriginBrowserSubmit(r) {
		ctx := buildGatewayRequestContext(r, h.App.Cfg, gw, nil, "", true, nil)
		h.auditGatewayWithMetadata(agentID, "http", "", "", "blocked", errors.New("cross-origin location submit"), ctx, gw)
		http.Error(w, "Forbidden origin", http.StatusForbidden)
		return
	}
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		ctx := buildGatewayRequestContext(r, h.App.Cfg, gw, nil, "", true, nil)
		h.auditGatewayWithMetadata(agentID, "http", "", "", "blocked", errors.New("invalid content type"), ctx, gw)
		http.Error(w, "Unsupported media type", http.StatusUnsupportedMediaType)
		return
	}
	rawBody, err := readWebhookBody(r, 4096)
	if err != nil {
		ctx := buildGatewayRequestContext(r, h.App.Cfg, gw, nil, "", true, nil)
		h.auditGatewayWithMetadata(agentID, "http", "", "", "blocked", err, ctx, gw)
		http.Error(w, "Gagal membaca request body", http.StatusBadRequest)
		return
	}
	var loc visitorLocationPayload
	dec := json.NewDecoder(bytes.NewReader(rawBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&loc); err != nil {
		ctx := buildGatewayRequestContext(r, h.App.Cfg, gw, rawBody, "", true, nil)
		h.auditGatewayWithMetadata(agentID, "http", "", "", "blocked", errors.New("invalid location body"), ctx, gw)
		http.Error(w, "Invalid body: expects {consent, latitude, longitude, accuracy_m, captured_at}", http.StatusBadRequest)
		return
	}
	if !loc.Consent {
		status, err := normalizeGPSStatus(loc.Status)
		if err != nil {
			ctx := buildGatewayRequestContext(r, h.App.Cfg, gw, rawBody, "", true, nil)
			h.auditGatewayWithMetadata(agentID, "http", "", "", "blocked", err, ctx, gw)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ctx := buildGatewayRequestContext(r, h.App.Cfg, gw, rawBody, "", true, nil)
		ctx.Visitor.Location["gps_status"] = status
		ctx.Visitor.Location["consent"] = false
		w.Header().Set("X-OpenClaw-Request-ID", ctx.RequestID)
		if ctx.Risk.Decision == "block" {
			h.auditGatewayWithMetadata(agentID, "http", "", "", "blocked", errors.New(strings.Join(ctx.Risk.Reasons, ",")), ctx, gw)
			http.Error(w, "Request blocked by gateway policy", http.StatusForbidden)
			return
		}
		h.auditGatewayWithMetadata(agentID, "http", "", "", status, nil, ctx, gw)
		w.Header().Set("Content-Type", "application/json")
		jsonOK(w, map[string]string{
			"request_id": ctx.RequestID,
			"status":     status,
		})
		return
	}
	visitorLocation, err := normalizeVisitorLocation(&loc, time.Now())
	if err != nil {
		ctx := buildGatewayRequestContext(r, h.App.Cfg, gw, rawBody, "", true, nil)
		h.auditGatewayWithMetadata(agentID, "http", "", "", "blocked", err, ctx, gw)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := buildGatewayRequestContext(r, h.App.Cfg, gw, rawBody, "", true, visitorLocation)
	w.Header().Set("X-OpenClaw-Request-ID", ctx.RequestID)
	if ctx.Risk.Decision == "block" {
		h.auditGatewayWithMetadata(agentID, "http", "", "", "blocked", errors.New(strings.Join(ctx.Risk.Reasons, ",")), ctx, gw)
		http.Error(w, "Request blocked by gateway policy", http.StatusForbidden)
		return
	}
	h.persistGatewayVisitor(agentID, "http", "location_captured", ctx, gw)
	w.Header().Set("Content-Type", "application/json")
	jsonOK(w, map[string]string{
		"request_id": ctx.RequestID,
		"status":     "location_captured",
	})
}

func sameOriginBrowserSubmit(r *http.Request) bool {
	host := canonicalHost(r.Host)
	if host == "" {
		return false
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		return requestOriginHost(origin) == host
	}
	if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		return requestOriginHost(referer) == host
	}
	return true
}

func requestOriginHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return canonicalHost(u.Host)
}

func canonicalHost(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return ""
	}
	u, err := url.Parse("//" + raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}

func normalizeGPSStatus(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "gps_denied":
		return "gps_denied", nil
	case "gps_unavailable", "gps_timeout":
		return "gps_unavailable", nil
	default:
		return "", errors.New("invalid gps status")
	}
}

func (h *GatewayHandler) locationSubmitToken(gw *gateway.Gateway, issuedAt time.Time) string {
	if gw == nil || gw.WebhookSecret == "" {
		return ""
	}
	payload := fmt.Sprintf("%s:%d", gw.ID, issuedAt.Unix())
	mac := hmac.New(sha256.New, []byte(gw.WebhookSecret))
	_, _ = mac.Write([]byte(payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil))
}

func (h *GatewayHandler) validLocationSubmitToken(gw *gateway.Gateway, token string, now time.Time) bool {
	token = strings.TrimSpace(token)
	if gw == nil || gw.WebhookSecret == "" || token == "" {
		return false
	}
	payload, signature, ok := strings.Cut(token, ".")
	if !ok || payload == "" || signature == "" {
		return false
	}
	gatewayID, issuedRaw, ok := strings.Cut(payload, ":")
	if !ok || gatewayID != gw.ID {
		return false
	}
	issuedUnix, err := strconv.ParseInt(issuedRaw, 10, 64)
	if err != nil {
		return false
	}
	issuedAt := time.Unix(issuedUnix, 0)
	if now.Sub(issuedAt) > 15*time.Minute || issuedAt.Sub(now) > 2*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, []byte(gw.WebhookSecret))
	_, _ = mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))
	if len(signature) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(signature), []byte(expected)) == 1
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
					fmt.Printf("[Gateway][WA][%s] agent engine error: %v\n", agentID, security.MaskKnownSecrets(err.Error()))
					h.auditGateway(agentID, "whatsapp", message.Text.Body, "", "error", err)
					http.Error(w, "agent engine gagal memproses pesan", http.StatusBadGateway)
					return
				}
				reply, err = h.applyOutputPolicy(agentID, "whatsapp", message.From, reply, gw.Config["template_id"])
				if err != nil {
					h.auditGateway(agentID, "whatsapp", message.Text.Body, reply, "blocked", err)
					http.Error(w, "output diblokir oleh masking policy", http.StatusForbidden)
					return
				}
				if err := h.sendWhatsAppReply(gw, message.From, reply); err != nil {
					fmt.Printf("[Gateway][WA][%s] send reply error: %v\n", agentID, security.MaskKnownSecrets(err.Error()))
					h.auditGateway(agentID, "whatsapp", message.Text.Body, reply, "error", err)
					http.Error(w, "gagal mengirim balasan WhatsApp", http.StatusBadGateway)
					return
				}
				h.auditGateway(agentID, "whatsapp", message.Text.Body, reply, "success", nil)
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

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("agent ingress status %d", resp.StatusCode)
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
		return "", fmt.Errorf("%s", security.MaskKnownSecrets(result.Error))
	}
	return result.Reply, nil
}

func (h *GatewayHandler) applyOutputPolicy(agentID, channel, senderID, reply, templateID string) (string, error) {
	out := reply
	if h.App.Security != nil && templateID != "" {
		rendered, err := h.App.Security.RenderTemplate(templateID, map[string]interface{}{
			"agent":     map[string]interface{}{"id": agentID, "reply": reply},
			"channel":   channel,
			"session":   map[string]interface{}{"id": senderID},
			"timestamp": time.Now().Format(time.RFC3339),
			"usage":     map[string]interface{}{"prompt_tokens": 0, "completion_tokens": 0},
		})
		if err != nil {
			return "", err
		}
		out = rendered
	}
	if h.App.Security == nil {
		return out, nil
	}
	masked := h.App.Security.Mask(out, "")
	if masked.Blocked {
		return masked.Text, errors.New(masked.Error)
	}
	return masked.Text, nil
}

func (h *GatewayHandler) auditGateway(agentID, channel, prompt, output, status string, err error) {
	if h.App.Security == nil {
		return
	}
	event := security.AuditEvent{
		Level:       "INFO",
		AgentID:     agentID,
		Channel:     channel,
		PromptHash:  security.HashText(prompt),
		OutputHash:  security.HashText(output),
		MaskingMode: h.App.Security.GetPolicy().MaskingMode,
		Status:      status,
		Message:     "gateway message processed",
	}
	if err != nil {
		event.Level = "ERROR"
		event.ErrorClass = security.MaskKnownSecrets(err.Error())
	}
	h.App.Security.EnqueueAudit(event)
}

func (h *GatewayHandler) auditGatewayWithMetadata(agentID, channel, prompt, output, status string, err error, ctx gatewayRequestContext, gw *gateway.Gateway) {
	if h.App == nil || gw == nil {
		return
	}
	h.persistGatewayVisitor(agentID, channel, status, ctx, gw)
	if h.App.Security == nil {
		return
	}
	event := security.AuditEvent{
		RequestID:   ctx.RequestID,
		Level:       "INFO",
		AgentID:     agentID,
		Channel:     channel,
		PromptHash:  security.HashText(prompt),
		OutputHash:  security.HashText(output),
		MaskingMode: h.App.Security.GetPolicy().MaskingMode,
		Status:      status,
		Message:     "gateway message processed",
		Metadata:    gatewayAuditMetadata(ctx, gw),
	}
	if err != nil {
		event.Level = "ERROR"
		if status == "blocked" || status == "rate_limited" || status == "invalid_signature" {
			event.Level = "SECURITY"
		}
		event.ErrorClass = security.MaskKnownSecrets(err.Error())
	}
	h.App.Security.EnqueueAudit(event)
}

func (h *GatewayHandler) persistGatewayVisitor(agentID, channel, status string, ctx gatewayRequestContext, gw *gateway.Gateway) {
	if h.App == nil || h.App.Visitors == nil || !h.App.Visitors.Enabled() || gw == nil {
		return
	}
	rec := gatewayVisitorRecord(ctx, gw, agentID, channel, status)
	if err := h.App.Visitors.Insert(context.Background(), rec); err != nil {
		fmt.Printf("[Gateway][Visitor] store error: %v\n", security.MaskKnownSecrets(err.Error()))
	}
}

func readWebhookBody(r *http.Request, maxBytes int) ([]byte, error) {
	rawBody, err := io.ReadAll(io.LimitReader(r.Body, int64(maxBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(rawBody) > maxBytes {
		return nil, errors.New(tooLargeMessage(maxBytes))
	}
	return rawBody, nil
}

func isGatewayTimeout(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "awaiting headers")
}

func (h *GatewayHandler) tryAcquireGatewaySlot(gatewayID string) bool {
	h.inflightMu.Lock()
	defer h.inflightMu.Unlock()
	if h.inflightByID == nil {
		h.inflightByID = map[string]int{}
	}
	if h.inflightByID[gatewayID] >= gatewayConcurrencyMax {
		return false
	}
	h.inflightByID[gatewayID]++
	return true
}

func (h *GatewayHandler) releaseGatewaySlot(gatewayID string) {
	h.inflightMu.Lock()
	defer h.inflightMu.Unlock()
	if h.inflightByID == nil || h.inflightByID[gatewayID] == 0 {
		return
	}
	h.inflightByID[gatewayID]--
	if h.inflightByID[gatewayID] <= 0 {
		delete(h.inflightByID, gatewayID)
	}
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

func parseGatewayTTL(raw string) (*time.Time, int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = strconv.Itoa(defaultWebhookTTLDays)
	}
	days, err := strconv.Atoi(raw)
	if err != nil {
		return nil, 0, fmt.Errorf("ttl_days tidak valid")
	}
	if days < 1 || days > maxWebhookTTLDays {
		return nil, 0, fmt.Errorf("ttl_days harus 1-%d hari", maxWebhookTTLDays)
	}
	expiresAt := time.Now().Add(time.Duration(days) * 24 * time.Hour).UTC()
	return &expiresAt, days, nil
}

func gatewayAuthMode(g *gateway.Gateway) string {
	if strings.EqualFold(g.Config["public"], "true") {
		return "public-restricted"
	}
	if g.Platform == gateway.PlatformHTTP {
		return "hmac-sha256"
	}
	if g.Platform == gateway.PlatformWhatsApp {
		return "meta-signature"
	}
	return "platform"
}

func oneTimeWebhookSecret(g *gateway.Gateway) string {
	if g.Platform == gateway.PlatformHTTP && !strings.EqualFold(g.Config["public"], "true") {
		return g.WebhookSecret
	}
	return ""
}

func (h *GatewayHandler) verifyOpenClawSignature(r *http.Request, gw *gateway.Gateway, rawBody []byte) error {
	if gw.WebhookSecret == "" {
		return fmt.Errorf("webhook signature secret missing")
	}
	timestampRaw := strings.TrimSpace(r.Header.Get("X-OpenClaw-Timestamp"))
	nonce := strings.TrimSpace(r.Header.Get("X-OpenClaw-Nonce"))
	signature := strings.TrimSpace(r.Header.Get("X-OpenClaw-Signature"))
	if timestampRaw == "" || nonce == "" || signature == "" {
		return fmt.Errorf("missing OpenClaw webhook signature headers")
	}
	if len(nonce) < 12 || len(nonce) > 96 || strings.ContainsAny(nonce, "\r\n\t ") {
		return fmt.Errorf("invalid OpenClaw webhook nonce")
	}
	ts, err := parseWebhookTimestamp(timestampRaw)
	if err != nil {
		return fmt.Errorf("invalid OpenClaw webhook timestamp")
	}
	if d := time.Since(ts); d > webhookSignatureSkew || d < -webhookSignatureSkew {
		return fmt.Errorf("OpenClaw webhook timestamp outside allowed window")
	}
	target := canonicalWebhookTarget(r.URL.EscapedPath(), r.URL.RawQuery)
	expected := computeOpenClawSignature(gw.WebhookSecret, r.Method, target, timestampRaw, nonce, rawBody)
	if !constantTimeSignatureEqual(signature, expected) {
		return fmt.Errorf("invalid OpenClaw webhook signature")
	}
	if !h.markUniqueEvent("hmac:" + gw.ID + ":" + nonce) {
		return fmt.Errorf("OpenClaw webhook nonce replay detected")
	}
	return nil
}

func parseWebhookTimestamp(raw string) (time.Time, error) {
	if unix, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if unix > 1_000_000_000_000 {
			return time.UnixMilli(unix), nil
		}
		return time.Unix(unix, 0), nil
	}
	return time.Parse(time.RFC3339, raw)
}

func computeOpenClawSignature(secret, method, path, timestamp, nonce string, rawBody []byte) string {
	bodyHash := sha256.Sum256(rawBody)
	canonical := strings.Join([]string{
		strings.ToUpper(method),
		path,
		timestamp,
		nonce,
		hex.EncodeToString(bodyHash[:]),
	}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(canonical))
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func canonicalWebhookTarget(path, rawQuery string) string {
	if rawQuery == "" {
		return path
	}
	return path + "?" + rawQuery
}

func constantTimeSignatureEqual(provided, expected string) bool {
	provided = strings.TrimSpace(provided)
	if strings.HasPrefix(provided, "sha256=") {
		provided = "v1=" + strings.TrimPrefix(provided, "sha256=")
	}
	if len(provided) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func (h *GatewayHandler) isDuplicateEvent(eventID string) bool {
	return !h.markUniqueEvent(eventID)
}

func (h *GatewayHandler) markUniqueEvent(eventID string) bool {
	if eventID == "" {
		return true
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
		return false
	}
	h.seenEvents[eventID] = now
	return true
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
		return fmt.Errorf("meta api status %d", resp.StatusCode)
	}
	return nil
}
