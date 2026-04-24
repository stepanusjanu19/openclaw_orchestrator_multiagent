// handlers/provider_config.go
// Mengelola konfigurasi provider AI (OpenRouter, Anthropic) di openclaw.json.

package handlers

import (
	"encoding/json"
	"io"
	"multiagent-platform/internal/openclaw"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ProviderConfigHandler mengelola penyimpanan API key provider ke openclaw.json.
type ProviderConfigHandler struct {
	App *App
}

// providerStatus merepresentasikan status satu provider.
type providerStatus struct {
	Name        string `json:"name"`
	Configured  bool   `json:"configured"`         // true jika API key tersedia dan bukan placeholder
	KeyHint     string `json:"key_hint,omitempty"` // misal: "sk-or-****...abcd"
	Source      string `json:"source"`             // "env" | "providers" | "none"
	RequiresKey bool   `json:"requires_key"`
	ModelCount  int    `json:"model_count"`
}

// GetProviders — GET /api/providers
// Mengembalikan status semua provider yang dikenali.
func (h *ProviderConfigHandler) GetProviders(w http.ResponseWriter, r *http.Request) {
	cfg := readRawOCJson(h.App.Cfg.OpenClawWorkspaceDir)
	counts := buildProviderModelCounts(h.App.Cfg.OpenClawWorkspaceDir)

	statuses := []providerStatus{
		buildProviderStatus("ollama", cfg, counts["ollama"]),
		buildProviderStatus("openrouter", cfg, counts["openrouter"]),
		buildProviderStatus("anthropic", cfg, counts["anthropic"]),
		buildProviderStatus("google", cfg, counts["google"]),
		buildProviderStatus("openai", cfg, counts["openai"]),
	}
	jsonOK(w, statuses)
}

func (h *ProviderConfigHandler) GetProviderModels(w http.ResponseWriter, r *http.Request, providerName string) {
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	allowed := map[string]bool{"openrouter": true, "anthropic": true, "google": true, "openai": true, "ollama": true}
	if !allowed[providerName] {
		jsonError(w, http.StatusBadRequest, "Provider tidak dikenal: "+providerName)
		return
	}
	models, err := openclaw.GetProviderModels(h.App.Cfg.OpenClawWorkspaceDir, providerName)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Gagal memuat model provider: "+err.Error())
		return
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].Label < models[j].Label
	})
	jsonOK(w, map[string]interface{}{
		"provider": providerName,
		"models":   models,
		"count":    len(models),
	})
}

// SaveProviderKey — POST /api/providers/{name}
// Body JSON: {"api_key": "sk-or-..."}
func (h *ProviderConfigHandler) SaveProviderKey(w http.ResponseWriter, r *http.Request, providerName string) {
	// Sanitize: only known provider names (allowlist), prevent injection
	allowed := map[string]bool{"openrouter": true, "anthropic": true, "google": true, "openai": true, "ollama": true}
	if !allowed[providerName] {
		jsonError(w, http.StatusBadRequest, "Provider tidak dikenal: "+providerName)
		return
	}

	var body struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.APIKey == "" {
		jsonError(w, http.StatusBadRequest, "api_key diperlukan di body JSON")
		return
	}

	// Validate API key length to prevent oversized payloads stored on disk
	body.APIKey = strings.TrimSpace(body.APIKey)
	if len(body.APIKey) > 500 || strings.ContainsAny(body.APIKey, "\r\n\x00") {
		jsonError(w, http.StatusBadRequest, "api_key terlalu panjang")
		return
	}

	configPath := filepath.Join(h.App.Cfg.OpenClawWorkspaceDir, "openclaw.json")

	// Baca raw JSON agar tidak kehilangan field yang tidak kita ketahui
	raw, err := os.ReadFile(configPath)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Gagal baca openclaw.json: "+err.Error())
		return
	}

	var doc map[string]interface{}
	if err := openclaw.DecodeConfigJSON(raw, &doc); err != nil {
		jsonError(w, http.StatusInternalServerError, "Gagal parse openclaw.json: "+err.Error())
		return
	}

	// Simpan ke env section (cara resmi OpenRouter & Anthropic)
	envKey := providerEnvKeyName(providerName)
	envSection, _ := doc["env"].(map[string]interface{})
	if envSection == nil {
		envSection = map[string]interface{}{}
	}
	envSection[envKey] = body.APIKey
	doc["env"] = envSection

	out, _ := json.MarshalIndent(doc, "", "  ")
	if err := writeFileAtomic(configPath, out, 0600); err != nil {
		jsonError(w, http.StatusInternalServerError, "Gagal tulis openclaw.json: "+err.Error())
		return
	}

	jsonOK(w, map[string]interface{}{
		"message":  "API key provider " + providerName + " berhasil disimpan",
		"provider": providerName,
		"key_hint": maskKey(body.APIKey),
	})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func providerEnvKeyName(name string) string {
	switch name {
	case "openrouter":
		return "OPENROUTER_API_KEY"
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "google":
		return "GOOGLE_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "ollama":
		return "OLLAMA_API_KEY"
	default:
		return ""
	}
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

type rawOCDoc struct {
	Env    map[string]string `json:"env"`
	Models struct {
		Providers map[string]struct {
			APIKey json.RawMessage `json:"apiKey"`
		} `json:"providers"`
	} `json:"models"`
}

func readRawOCJson(ocDir string) rawOCDoc {
	var doc rawOCDoc
	data, err := os.ReadFile(filepath.Join(ocDir, "openclaw.json"))
	if err != nil {
		return doc
	}
	openclaw.DecodeConfigJSON(data, &doc)
	return doc
}

func rawSecretString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var val string
	if err := json.Unmarshal(raw, &val); err == nil {
		return val
	}
	var ref struct {
		Source string `json:"source"`
		ID     string `json:"id"`
	}
	if err := json.Unmarshal(raw, &ref); err == nil && ref.Source == "env" && ref.ID != "" {
		return os.Getenv(ref.ID)
	}
	return ""
}

func providerRequiresKey(name string) bool {
	return name != "ollama"
}

func buildProviderModelCounts(ocDir string) map[string]int {
	counts := map[string]int{}
	models, _, err := openclaw.GetAvailableModels(ocDir)
	if err != nil {
		return counts
	}
	for _, model := range models {
		counts[strings.ToLower(model.Provider)]++
	}
	return counts
}

func buildProviderStatus(name string, cfg rawOCDoc, modelCount int) providerStatus {
	envKey := providerEnvKeyName(name)

	// Cek env section
	if envKey != "" {
		if val, ok := cfg.Env[envKey]; ok && val != "" && !isPlaceholder(val) {
			return providerStatus{
				Name:        name,
				Configured:  true,
				KeyHint:     maskKey(val),
				Source:      "env",
				RequiresKey: providerRequiresKey(name),
				ModelCount:  modelCount,
			}
		}
	}

	// Cek models.providers.{name}.apiKey
	if prov, ok := cfg.Models.Providers[name]; ok {
		apiKey := rawSecretString(prov.APIKey)
		if apiKey != "" && !isPlaceholder(apiKey) {
			return providerStatus{
				Name:        name,
				Configured:  true,
				KeyHint:     maskKey(apiKey),
				Source:      "providers",
				RequiresKey: providerRequiresKey(name),
				ModelCount:  modelCount,
			}
		}
	}

	// Jika Ollama belum di-config API Key, secara default dia asumsikan local (tanpa token)
	if name == "ollama" {
		return providerStatus{Name: "ollama", Configured: true, Source: "default", KeyHint: "Lokal / Tanpa API Key", RequiresKey: false, ModelCount: modelCount}
	}

	return providerStatus{Name: name, Configured: false, Source: "none", RequiresKey: true, ModelCount: modelCount}
}

func isPlaceholder(key string) bool {
	placeholders := []string{"OLLAMA_API_KEY", "YOUR_API_KEY", "sk-or-PLACEHOLDER", "dummy"}
	for _, p := range placeholders {
		if key == p {
			return true
		}
	}
	return false
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
