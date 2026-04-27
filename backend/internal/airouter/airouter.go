package airouter

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"multiagent-platform/internal/config"
)

type Validation struct {
	ChatURL  string
	BaseURL  string
	Masked   string
	OK       bool
	Errors   []string
	Warnings []string
}

type Status struct {
	Enabled          bool      `json:"enabled"`
	Provider         string    `json:"provider"`
	BaseURL          string    `json:"base_url"`
	MaskedBaseURL    string    `json:"masked_base_url"`
	ChatURL          string    `json:"chat_url,omitempty"`
	RequireLoopback  bool      `json:"require_loopback"`
	AllowCloud       bool      `json:"allow_cloud"`
	TokenConfigured  bool      `json:"token_configured"`
	ToolCallsAllowed bool      `json:"tool_calls_allowed"`
	RuntimeTool      string    `json:"runtime_tool,omitempty"`
	RuntimeActive    bool      `json:"runtime_active"`
	AgentRouting     bool      `json:"agent_routing_enabled"`
	BinaryTrusted    bool      `json:"binary_trusted"`
	BinaryPath       string    `json:"binary_resolved_path,omitempty"`
	RuntimeIsolated  bool      `json:"runtime_isolated"`
	RuntimeDir       string    `json:"runtime_dir,omitempty"`
	LoopbackEnforced bool      `json:"loopback_enforced"`
	ProcessGroup     bool      `json:"process_group_enabled"`
	RouteTarget      string    `json:"route_target"`
	RuntimeSupported bool      `json:"runtime_supported"`
	SecurityWarnings []string  `json:"security_warnings,omitempty"`
	SecurityNotices  []string  `json:"security_notices,omitempty"`
	Reachable        bool      `json:"reachable"`
	ModelCount       int       `json:"model_count"`
	Models           []string  `json:"models,omitempty"`
	Warnings         []string  `json:"warnings,omitempty"`
	Error            string    `json:"error,omitempty"`
	CheckedAt        time.Time `json:"checked_at"`
}

type modelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func Validate(cfg *config.Config) Validation {
	base := strings.TrimSpace(cfg.AIRouterBaseURL)
	v := Validation{BaseURL: base, Masked: MaskURL(base)}
	if !cfg.AIRouterEnabled {
		v.OK = true
		return v
	}
	if base == "" {
		v.Errors = append(v.Errors, "AI_ROUTER_BASE_URL kosong")
		return v
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		v.Errors = append(v.Errors, "AI_ROUTER_BASE_URL invalid")
		return v
	}
	if u.User != nil {
		v.Errors = append(v.Errors, "AI_ROUTER_BASE_URL tidak boleh memuat credential")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		v.Errors = append(v.Errors, "AI_ROUTER_BASE_URL harus http atau https")
	}
	host := strings.Trim(u.Hostname(), "[]")
	if host == "" {
		v.Errors = append(v.Errors, "AI_ROUTER_BASE_URL host kosong")
	}
	if host == "0.0.0.0" || host == "::" {
		v.Errors = append(v.Errors, "router tidak boleh diarahkan ke 0.0.0.0/::")
	}
	loopback := IsLoopbackHost(host)
	if cfg.AIRouterRequireLoopback && !loopback {
		v.Errors = append(v.Errors, "router non-loopback ditolak; set AI_ROUTER_REQUIRE_LOOPBACK=false hanya untuk deployment terkontrol")
	}
	if !loopback && !cfg.AIRouterAllowCloud {
		v.Errors = append(v.Errors, "router cloud/public ditolak; set AI_ROUTER_ALLOW_CLOUD=true hanya jika risikonya diterima")
	}
	if !loopback && u.Scheme != "https" {
		v.Errors = append(v.Errors, "router non-loopback harus memakai HTTPS")
	}
	if cfg.AIRouterProvider != "openclaw" && strings.TrimSpace(cfg.AIRouterToken) == "" && !loopback {
		v.Warnings = append(v.Warnings, "AI_ROUTER_TOKEN kosong; pastikan router tidak menerima request dari jaringan lain")
	}
	if strings.EqualFold(strings.TrimSpace(cfg.AIRouterToken), "9router-default-secret-change-me") {
		v.Errors = append(v.Errors, "AI_ROUTER_TOKEN memakai default secret 9Router yang tidak aman")
	}
	v.ChatURL = ChatCompletionsURL(base)
	v.OK = len(v.Errors) == 0
	return v
}

func ProbeStatus(cfg *config.Config) Status {
	return probeStatus(cfg, cfg.AIRouterEnabled, "", false)
}

func ProbeRuntimeStatus(baseCfg *config.Config, provider, baseURL string) Status {
	cfg := *baseCfg
	cfg.AIRouterEnabled = true
	cfg.AIRouterProvider = provider
	cfg.AIRouterBaseURL = baseURL
	cfg.AIRouterToken = ""
	cfg.AIRouterRequireLoopback = true
	cfg.AIRouterAllowCloud = false
	cfg.AIRouterAllowToolCalls = false
	return probeStatus(&cfg, baseCfg.AIRouterEnabled, provider, true)
}

func probeStatus(cfg *config.Config, agentRoutingEnabled bool, runtimeTool string, runtimeActive bool) Status {
	validation := Validate(cfg)
	status := Status{
		Enabled:          cfg.AIRouterEnabled,
		Provider:         cfg.AIRouterProvider,
		BaseURL:          validation.Masked,
		MaskedBaseURL:    validation.Masked,
		ChatURL:          validation.ChatURL,
		RequireLoopback:  cfg.AIRouterRequireLoopback,
		AllowCloud:       cfg.AIRouterAllowCloud,
		TokenConfigured:  strings.TrimSpace(cfg.AIRouterToken) != "",
		ToolCallsAllowed: cfg.AIRouterAllowToolCalls,
		RuntimeTool:      runtimeTool,
		RuntimeActive:    runtimeActive,
		AgentRouting:     agentRoutingEnabled,
		RouteTarget:      routeTarget(cfg.AIRouterEnabled),
		RuntimeSupported: validation.OK,
		Warnings:         append([]string{}, validation.Warnings...),
		CheckedAt:        time.Now(),
	}
	if !cfg.AIRouterEnabled {
		return status
	}
	if !validation.OK {
		status.Warnings = append(status.Warnings, validation.Errors...)
		status.Error = "router config ditolak"
		status.RuntimeSupported = false
		return status
	}
	modelsURL := ModelsURL(validation.BaseURL)
	req, err := http.NewRequest(http.MethodGet, modelsURL, nil)
	if err != nil {
		status.Error = "router URL invalid"
		status.RuntimeSupported = false
		return status
	}
	if token := strings.TrimSpace(cfg.AIRouterToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	hc := &http.Client{Timeout: time.Duration(cfg.AIRouterTimeoutMS) * time.Millisecond}
	res, err := hc.Do(req)
	if err != nil {
		status.Error = sanitizeError(err)
		status.RuntimeSupported = false
		return status
	}
	defer res.Body.Close()
	status.Reachable = res.StatusCode >= 200 && res.StatusCode < 500
	if res.StatusCode != http.StatusOK {
		status.Error = fmt.Sprintf("router /v1/models HTTP %d", res.StatusCode)
		status.RuntimeSupported = false
		return status
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		status.Error = "gagal membaca response router"
		status.RuntimeSupported = false
		return status
	}
	var mr modelsResponse
	if err := json.Unmarshal(body, &mr); err != nil {
		status.Error = "format /v1/models tidak dikenali"
		status.RuntimeSupported = false
		return status
	}
	for _, item := range mr.Data {
		if item.ID != "" {
			status.Models = append(status.Models, item.ID)
		}
	}
	status.ModelCount = len(status.Models)
	status.RuntimeSupported = true
	return status
}

func routeTarget(aiRouterEnabled bool) string {
	if aiRouterEnabled {
		return "ai_router"
	}
	return "openclaw_gateway"
}

func ChatCompletionsURL(raw string) string {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	if strings.HasSuffix(u, "/chat/completions") {
		return u
	}
	if strings.HasSuffix(u, "/v1") {
		return u + "/chat/completions"
	}
	return u + "/v1/chat/completions"
}

func ModelsURL(raw string) string {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	if strings.HasSuffix(u, "/chat/completions") {
		u = strings.TrimSuffix(u, "/chat/completions")
	}
	if strings.HasSuffix(u, "/v1") {
		return u + "/models"
	}
	return u + "/v1/models"
}

func MaskURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "[invalid-url]"
	}
	u.User = nil
	return u.String()
}

func IsLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	msg = strings.ReplaceAll(msg, "\n", " ")
	msg = strings.ReplaceAll(msg, "\r", " ")
	if len(msg) > 180 {
		return msg[:180] + "..."
	}
	return msg
}
