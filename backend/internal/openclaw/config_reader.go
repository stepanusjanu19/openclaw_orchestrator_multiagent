// internal/openclaw/config_reader.go
// Membaca dan menulis ~/.openclaw/openclaw.json untuk detect & register agents.

package openclaw

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ── Struct lengkap untuk read + write openclaw.json ───────────────────────────

// OCConfig adalah struktur openclaw.json yang relevan.
type OCConfig struct {
	Gateway  OCGateway       `json:"gateway,omitempty"`
	Agents   OCAgents        `json:"agents"`
	Bindings []OCBinding     `json:"bindings,omitempty"`
	Channels OCChannels      `json:"channels,omitempty"`
	Session  json.RawMessage `json:"session,omitempty"`
	Tools    json.RawMessage `json:"tools,omitempty"`
	Models   json.RawMessage `json:"models,omitempty"`
}

type OCGateway struct {
	Mode string `json:"mode,omitempty"`
	Auth struct {
		Mode           string      `json:"mode,omitempty"`
		Token          SecretValue `json:"token,omitempty"`
		AllowTailscale bool        `json:"allowTailscale,omitempty"`
	} `json:"auth,omitempty"`
	Port  int             `json:"port,omitempty"`
	Bind  string          `json:"bind,omitempty"`
	HTTP  json.RawMessage `json:"http,omitempty"`
	Nodes json.RawMessage `json:"nodes,omitempty"`
}

type OCAgents struct {
	Defaults OCAgentDefaults `json:"defaults,omitempty"`
	List     []OCAgentEntry  `json:"list,omitempty"`
}

type OCAgentDefaults struct {
	Workspace string           `json:"workspace,omitempty"`
	Model     FlexibleModelRef `json:"model,omitempty"`
	Models    map[string]any   `json:"models,omitempty"`
}

// FlexibleModelRef accepts both legacy "provider/model" strings and the
// current OpenClaw object shape: {"primary":"...", "fallbacks":["..."]}.
type FlexibleModelRef struct {
	Primary   string          `json:"primary,omitempty"`
	Fallbacks []string        `json:"fallbacks,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

func (m *FlexibleModelRef) UnmarshalJSON(data []byte) error {
	m.Raw = append(m.Raw[:0], data...)
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		return json.Unmarshal(data, &m.Primary)
	}
	var obj struct {
		Primary   string   `json:"primary"`
		Fallbacks []string `json:"fallbacks"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	m.Primary = obj.Primary
	m.Fallbacks = obj.Fallbacks
	return nil
}

func (m FlexibleModelRef) MarshalJSON() ([]byte, error) {
	if len(m.Raw) > 0 {
		return m.Raw, nil
	}
	if len(m.Fallbacks) == 0 {
		return json.Marshal(m.Primary)
	}
	return json.Marshal(struct {
		Primary   string   `json:"primary,omitempty"`
		Fallbacks []string `json:"fallbacks,omitempty"`
	}{Primary: m.Primary, Fallbacks: m.Fallbacks})
}

func (m FlexibleModelRef) Refs() []string {
	refs := make([]string, 0, 1+len(m.Fallbacks))
	if strings.TrimSpace(m.Primary) != "" {
		refs = append(refs, strings.TrimSpace(m.Primary))
	}
	for _, fallback := range m.Fallbacks {
		if strings.TrimSpace(fallback) != "" {
			refs = append(refs, strings.TrimSpace(fallback))
		}
	}
	return refs
}

type OCAgentEntry struct {
	ID        string           `json:"id"`
	Name      string           `json:"name,omitempty"`
	Workspace string           `json:"workspace,omitempty"`
	AgentDir  string           `json:"agentDir,omitempty"`
	Model     FlexibleModelRef `json:"model,omitempty"`
	Tools     json.RawMessage  `json:"tools,omitempty"`
}

func (a OCAgentEntry) AgentID() string {
	if a.ID != "" {
		return a.ID
	}
	return a.Name
}

func (a OCAgentEntry) AgentWorkspace() string {
	if a.Workspace != "" {
		return a.Workspace
	}
	return a.AgentDir
}

// SecretRef mengikuti bentuk credential reference yang ditulis OpenClaw wizard,
// misalnya {"source":"env","provider":"default","id":"OPENCLAW_GATEWAY_TOKEN"}.
type SecretRef struct {
	Source   string `json:"source,omitempty"`
	Provider string `json:"provider,omitempty"`
	ID       string `json:"id,omitempty"`
}

// SecretValue menerima field config yang bisa berupa plaintext string atau
// SecretRef object. Ini menjaga kompatibilitas dengan output wizard OpenClaw.
type SecretValue struct {
	Value string
	Ref   *SecretRef
	Raw   json.RawMessage
}

func (s *SecretValue) UnmarshalJSON(data []byte) error {
	s.Raw = append(s.Raw[:0], data...)
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		return json.Unmarshal(data, &s.Value)
	}
	var ref SecretRef
	if err := json.Unmarshal(data, &ref); err != nil {
		return err
	}
	s.Ref = &ref
	return nil
}

func (s SecretValue) MarshalJSON() ([]byte, error) {
	if len(s.Raw) > 0 {
		return s.Raw, nil
	}
	if s.Value != "" {
		return json.Marshal(s.Value)
	}
	if s.Ref != nil {
		return json.Marshal(s.Ref)
	}
	return []byte("null"), nil
}

func (s SecretValue) Resolve() string {
	if s.Value != "" {
		return s.Value
	}
	if s.Ref == nil {
		return ""
	}
	if strings.EqualFold(s.Ref.Source, "env") && s.Ref.ID != "" {
		return os.Getenv(s.Ref.ID)
	}
	return ""
}

type OCBinding struct {
	AgentID string `json:"agentId"`
	Match   struct {
		Channel   string `json:"channel"`
		AccountID string `json:"accountId,omitempty"`
	} `json:"match"`
}

func (b *OCBinding) UnmarshalJSON(data []byte) error {
	var aux struct {
		AgentID string `json:"agentId"`
		Agent   string `json:"agent"`
		Match   struct {
			Channel   string `json:"channel"`
			AccountID string `json:"accountId,omitempty"`
		} `json:"match"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	b.AgentID = aux.AgentID
	if b.AgentID == "" {
		b.AgentID = aux.Agent
	}
	b.Match = aux.Match
	return nil
}

type OCChannels struct {
	Telegram OCTelegram `json:"telegram,omitempty"`
}

type OCTelegram struct {
	Accounts map[string]OCTelegramAccount `json:"accounts,omitempty"`
}

type OCTelegramAccount struct {
	BotToken string `json:"botToken"`
	DMPolicy string `json:"dmPolicy,omitempty"`
}

// DetectedAgent adalah agent yang terdeteksi dari openclaw.json.
type DetectedAgent struct {
	ID          string `json:"id"`
	AccountID   string `json:"account_id"`
	BotToken    string `json:"bot_token"`
	HasBotToken bool   `json:"has_bot_token"`
	Workspace   string `json:"workspace,omitempty"`
}

// ── Read ──────────────────────────────────────────────────────────────────────

// ReadOCConfig membaca dan parse file openclaw.json.
func ReadOCConfig(workspaceDir string) (*OCConfig, error) {
	doc, _, configPath, err := readEffectiveConfigDocument(workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("tidak bisa baca %s: %w", configPath, err)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("gagal normalize openclaw.json: %w", err)
	}

	var cfg OCConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("gagal parse openclaw.json: %w", err)
	}

	return &cfg, nil
}

// GlobalOCInfo menyimpan informasi esensial dari gateway
type GlobalOCInfo struct {
	GatewayToken     string
	DefaultWorkspace string
}

// ReadGlobalOCInfo mengekstrak token gateway dan workspace default
func ReadGlobalOCInfo(workspaceDir string) (*GlobalOCInfo, error) {
	cfg, err := ReadOCConfig(workspaceDir)
	if err != nil {
		return nil, err
	}
	return &GlobalOCInfo{
		GatewayToken:     cfg.Gateway.Auth.Token.Resolve(),
		DefaultWorkspace: cfg.Agents.Defaults.Workspace,
	}, nil
}

// DetectAgents menganalisis openclaw.json dan mengembalikan daftar agent yang terdeteksi.
func DetectAgents(workspaceDir string) ([]DetectedAgent, error) {
	cfg, err := ReadOCConfig(workspaceDir)
	if err != nil {
		return nil, err
	}

	// Build map: agentId → telegram accountId dari bindings
	agentToAccount := make(map[string]string)
	for _, b := range cfg.Bindings {
		if strings.ToLower(b.Match.Channel) == "telegram" && b.AgentID != "" {
			accountID := b.Match.AccountID
			if accountID == "" {
				accountID = "default"
			}
			agentToAccount[b.AgentID] = accountID
		}
	}

	// Build map: accountId → botToken dari channels.telegram.accounts
	accountToToken := make(map[string]string)
	if cfg.Channels.Telegram.Accounts != nil {
		for accountID, acc := range cfg.Channels.Telegram.Accounts {
			accountToToken[accountID] = acc.BotToken
		}
	}

	// Assemble detected agents
	var detected []DetectedAgent
	for _, ag := range cfg.Agents.List {
		agentID := ag.AgentID()
		if agentID == "" {
			continue
		}
		accountID := agentToAccount[agentID]
		token := ""
		hasToken := false
		if accountID != "" {
			token = accountToToken[accountID]
			hasToken = token != "" && !strings.HasPrefix(token, "GANTI")
		}

		detected = append(detected, DetectedAgent{
			ID:          agentID,
			AccountID:   accountID,
			BotToken:    token,
			HasBotToken: hasToken,
			Workspace:   ag.AgentWorkspace(),
		})
	}

	return detected, nil
}

func readConfigDocument(ocDir string) (map[string]interface{}, []byte, string, error) {
	configPath := filepath.Join(ocDir, "openclaw.json")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, nil, configPath, err
	}

	var doc map[string]interface{}
	if err := unmarshalConfigJSON(raw, &doc); err != nil {
		return nil, nil, configPath, err
	}
	return doc, raw, configPath, nil
}

func readEffectiveConfigDocument(ocDir string) (map[string]interface{}, []byte, string, error) {
	configPath := filepath.Join(ocDir, "openclaw.json")
	doc, raw, err := readConfigFile(configPath)
	if err != nil {
		return nil, nil, configPath, err
	}
	merged, err := resolveConfigIncludes(filepath.Dir(configPath), configPath, doc, map[string]bool{}, 0)
	if err != nil {
		return nil, nil, configPath, err
	}
	return merged, raw, configPath, nil
}

func readConfigFile(path string) (map[string]interface{}, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var doc map[string]interface{}
	if err := unmarshalConfigJSON(raw, &doc); err != nil {
		return nil, nil, err
	}
	return doc, raw, nil
}

func unmarshalConfigJSON(data []byte, target interface{}) error {
	if err := json.Unmarshal(data, target); err == nil {
		return nil
	}
	normalized := normalizeJSON5Like(string(data))
	return json.Unmarshal([]byte(normalized), target)
}

func DecodeConfigJSON(data []byte, target interface{}) error {
	return unmarshalConfigJSON(data, target)
}

func normalizeJSON5Like(in string) string {
	withoutComments := stripJSON5Comments(in)
	withoutTrailing := stripTrailingCommas(withoutComments)
	return quoteBareKeys(withoutTrailing)
}

func stripJSON5Comments(in string) string {
	var out strings.Builder
	inString := false
	escaped := false
	quote := byte(0)
	for i := 0; i < len(in); i++ {
		ch := in[i]
		if inString {
			if escaped {
				out.WriteByte(ch)
				escaped = false
				continue
			}
			if ch == '\\' {
				out.WriteByte(ch)
				escaped = true
			} else if ch == quote {
				if quote == '\'' {
					out.WriteByte('"')
				} else {
					out.WriteByte(ch)
				}
				inString = false
			} else if quote == '\'' && ch == '"' {
				out.WriteByte('\\')
				out.WriteByte(ch)
			} else {
				out.WriteByte(ch)
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			inString = true
			quote = ch
			if ch == '\'' {
				out.WriteByte('"')
			} else {
				out.WriteByte(ch)
			}
			continue
		}
		if ch == '/' && i+1 < len(in) {
			next := in[i+1]
			if next == '/' {
				i += 2
				for i < len(in) && in[i] != '\n' {
					i++
				}
				if i < len(in) {
					out.WriteByte('\n')
				}
				continue
			}
			if next == '*' {
				i += 2
				for i+1 < len(in) && !(in[i] == '*' && in[i+1] == '/') {
					i++
				}
				i++
				continue
			}
		}
		out.WriteByte(ch)
	}
	return out.String()
}

func stripTrailingCommas(in string) string {
	re := regexp.MustCompile(`,\s*([}\]])`)
	for {
		next := re.ReplaceAllString(in, `$1`)
		if next == in {
			return next
		}
		in = next
	}
}

func quoteBareKeys(in string) string {
	re := regexp.MustCompile(`([{\s,])([A-Za-z_$][A-Za-z0-9_$-]*)\s*:`)
	return re.ReplaceAllString(in, `$1"$2":`)
}

func resolveConfigIncludes(baseDir, currentPath string, doc map[string]interface{}, seen map[string]bool, depth int) (map[string]interface{}, error) {
	if depth > 8 {
		return nil, fmt.Errorf("$include terlalu dalam")
	}
	abs, _ := filepath.Abs(currentPath)
	if seen[abs] {
		return nil, fmt.Errorf("circular $include: %s", currentPath)
	}
	seen[abs] = true
	defer delete(seen, abs)

	includeValue, hasInclude := doc["$include"]
	if !hasInclude {
		return doc, nil
	}
	delete(doc, "$include")
	includes := includeList(includeValue)
	merged := map[string]interface{}{}
	for _, inc := range includes {
		incPath := inc
		if !filepath.IsAbs(incPath) {
			incPath = filepath.Join(baseDir, incPath)
		}
		incPath = filepath.Clean(incPath)
		if !strings.HasPrefix(incPath, filepath.Clean(baseDir)+string(os.PathSeparator)) && incPath != filepath.Clean(baseDir) {
			return nil, fmt.Errorf("$include di luar config dir ditolak: %s", inc)
		}
		child, _, err := readConfigFile(incPath)
		if err != nil {
			return nil, err
		}
		resolved, err := resolveConfigIncludes(filepath.Dir(incPath), incPath, child, seen, depth+1)
		if err != nil {
			return nil, err
		}
		merged = deepMergeMaps(merged, resolved)
	}
	return deepMergeMaps(merged, doc), nil
}

func includeList(v interface{}) []string {
	switch value := v.(type) {
	case string:
		if value == "" {
			return nil
		}
		return []string{value}
	case []interface{}:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func deepMergeMaps(base, overlay map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		if existing, ok := out[k].(map[string]interface{}); ok {
			if next, ok := v.(map[string]interface{}); ok {
				out[k] = deepMergeMaps(existing, next)
				continue
			}
		}
		out[k] = v
	}
	return out
}

func objectField(parent map[string]interface{}, key string) map[string]interface{} {
	if obj, ok := parent[key].(map[string]interface{}); ok {
		return obj
	}
	obj := map[string]interface{}{}
	parent[key] = obj
	return obj
}

func agentEntryIDMap(entry map[string]interface{}) string {
	if id, ok := entry["id"].(string); ok && id != "" {
		return id
	}
	if name, ok := entry["name"].(string); ok && name != "" {
		return name
	}
	return ""
}

func agentListField(agents map[string]interface{}) []interface{} {
	if list, ok := agents["list"].([]interface{}); ok {
		return list
	}
	list := []interface{}{}
	agents["list"] = list
	return list
}

func writeConfigDocument(configPath string, raw []byte, backupSuffix string, doc map[string]interface{}) error {
	if _, hasInclude := doc["$include"]; hasInclude {
		return fmt.Errorf("openclaw.json memakai $include; write root langsung ditolak agar split config tidak korup")
	}
	if len(raw) > 0 {
		_ = writeFileAtomic(configPath+backupSuffix, raw, 0600)
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(configPath, out, 0600)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, perm); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// ── Write ─────────────────────────────────────────────────────────────────────

// RegisterAgentParams berisi semua data yang dibutuhkan untuk mendaftarkan agent baru.
type RegisterAgentParams struct {
	ID           string // Agent ID (e.g. "researcher")
	WorkspaceDir string // Path absolut workspace agent (e.g. ~/.openclaw/workspace-researcher)
	Model        string // OpenClaw model (e.g. "ollama/kimi-k2.5:cloud")
	BotToken     string // Telegram bot token (kosong = tidak tambahkan binding)
}

// RegisterAgentInOCConfig mendaftarkan agent baru ke openclaw.json secara idempoten.
// Ini memastikan:
//  1. Agent masuk ke agents.list dengan workspace yang benar
//  2. Jika BotToken ada: ditambahkan ke channels.telegram.accounts + bindings
//
// Backup .bak dibuat sebelum tulis.
func RegisterAgentInOCConfig(ocDir string, params RegisterAgentParams) error {
	doc, raw, configPath, err := readConfigDocument(ocDir)
	if err != nil {
		return fmt.Errorf("RegisterAgentInOCConfig: gagal baca config: %w", err)
	}

	agents := objectField(doc, "agents")
	list := agentListField(agents)

	// ── 1. Pastikan agent ada di agents.list tanpa membuang field wizard lain ──
	agentExists := false
	for _, item := range list {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if agentEntryIDMap(entry) == params.ID {
			entry["workspace"] = params.WorkspaceDir
			if params.Model != "" {
				entry["model"] = params.Model
			}
			agentExists = true
			break
		}
	}
	if !agentExists {
		entry := map[string]interface{}{
			"id":        params.ID,
			"name":      params.ID,
			"workspace": params.WorkspaceDir,
		}
		if params.Model != "" {
			entry["model"] = params.Model
		}
		list = append(list, entry)
		agents["list"] = list
	}

	// ── 2. Telegram bot token & binding (DISABLE UNTUK MENCEGAH 409 CONFLICT) ──
	// Aplikasi kita sudah menggunakan `bots/shared/base-bot.js` mandiri (Node.js)
	// untuk setiap agen Telegram. Jika kita tulis token ke `openclaw.json`,
	// gateway core (port 14000) akan ikut menge-poll endpoint Telegram yang sama
	// dan memperebutkan sesi (409 Conflict).
	/*
		if params.BotToken != "" && !strings.HasPrefix(params.BotToken, "GANTI") {
			accountID := params.ID

			if cfg.Channels.Telegram.Accounts == nil {
				cfg.Channels.Telegram.Accounts = make(map[string]OCTelegramAccount)
			}
			cfg.Channels.Telegram.Accounts[accountID] = OCTelegramAccount{
				BotToken: params.BotToken,
				DMPolicy: "respond",
			}

			bindingExists := false
			for i, b := range cfg.Bindings {
				if b.AgentID == params.ID && strings.ToLower(b.Match.Channel) == "telegram" {
					cfg.Bindings[i].Match.AccountID = accountID
					bindingExists = true
					break
				}
			}
			if !bindingExists {
				nb := OCBinding{AgentID: params.ID}
				nb.Match.Channel = "telegram"
				nb.Match.AccountID = accountID
				cfg.Bindings = append(cfg.Bindings, nb)
			}
		}
	*/

	// ── 3. Backup & tulis ────────────────────────────────────────────────
	if err := writeConfigDocument(configPath, raw, ".platform-bak", doc); err != nil {
		return fmt.Errorf("RegisterAgentInOCConfig: gagal tulis %s: %w", configPath, err)
	}

	return nil
}

// UnregisterAgentFromOCConfig menghapus agent dari openclaw.json (untuk DeleteAgent).
func UnregisterAgentFromOCConfig(ocDir string, agentID string) error {
	doc, raw, configPath, err := readConfigDocument(ocDir)
	if err != nil {
		return err
	}

	// Hapus dari agents.list
	if agents, ok := doc["agents"].(map[string]interface{}); ok {
		list := agentListField(agents)
		newList := make([]interface{}, 0, len(list))
		for _, item := range list {
			entry, ok := item.(map[string]interface{})
			if !ok || agentEntryIDMap(entry) != agentID {
				newList = append(newList, item)
			}
		}
		agents["list"] = newList
	}

	// Hapus bindings
	if bindings, ok := doc["bindings"].([]interface{}); ok {
		newBindings := make([]interface{}, 0, len(bindings))
		for _, item := range bindings {
			binding, ok := item.(map[string]interface{})
			if !ok {
				newBindings = append(newBindings, item)
				continue
			}
			boundAgent, _ := binding["agentId"].(string)
			if boundAgent == "" {
				boundAgent, _ = binding["agent"].(string)
			}
			if boundAgent != agentID {
				newBindings = append(newBindings, item)
			}
		}
		doc["bindings"] = newBindings
	}

	// Hapus dari telegram accounts (accountID == agentID per konvensi kita)
	if channels, ok := doc["channels"].(map[string]interface{}); ok {
		if telegram, ok := channels["telegram"].(map[string]interface{}); ok {
			if accounts, ok := telegram["accounts"].(map[string]interface{}); ok {
				delete(accounts, agentID)
			}
		}
	}

	return writeConfigDocument(configPath, raw, ".unregister-bak", doc)
}

// ── Model Classification ───────────────────────────────────────────────────────

// ModelEntry merepresentasikan satu model dengan metadata klasifikasi tier-nya.
type ModelEntry struct {
	ID        string `json:"id"`          // full ID: "ollama/kimi-k2.5:cloud"
	Label     string `json:"label"`       // short: "kimi-k2.5:cloud"
	Provider  string `json:"provider"`    // "ollama"
	Tier      string `json:"tier"`        // "local" | "cloud_proxied" | "cloud"
	TierLabel string `json:"tier_label"`  // "🖥️ Local" | "☁️ Cloud Proxied" | "☁️ Cloud"
	HasAPIKey bool   `json:"has_api_key"` // indikator apakah ada API key riil
}

// classifyModel menentukan tier dengan mengecek langsung baseUrl dan API Key di config.
func classifyModel(provider, modelID, apiKey, baseUrl string) (tier, tierLabel string, hasApiKey bool) {
	isCloudSuffix := strings.HasSuffix(modelID, ":cloud")

	apiKey = strings.TrimSpace(apiKey)
	// Anggap punya API key jika string apiKey tidak kosong dan bukan variabel dummy (mengandung nama provider/dummy)
	hasApiKey = apiKey != "" && apiKey != "OLLAMA_API_KEY" && !strings.Contains(strings.ToLower(apiKey), "dummy")

	baseUrlLower := strings.ToLower(baseUrl)
	isLocalUrl := baseUrlLower == "" || strings.Contains(baseUrlLower, "127.0.0.1") || strings.Contains(baseUrlLower, "localhost")

	switch {
	case isLocalUrl && isCloudSuffix:
		return "cloud_proxied", "☁️ Cloud Proxied", hasApiKey
	case isLocalUrl && !isCloudSuffix:
		return "local", "🖥️ Local", hasApiKey
	default:
		return "cloud", "☁️ Cloud", hasApiKey
	}
}

// ModelGroups menyimpan model yang sudah dikelompokkan per tier.
type ModelGroups struct {
	Cloud        []ModelEntry `json:"cloud"`
	CloudProxied []ModelEntry `json:"cloud_proxied"`
	Local        []ModelEntry `json:"local"`
}

// GetAvailableModels membaca openclaw.json, query Ollama REST API untuk local models,
// dan fetch OpenRouter models jika API key tersedia.
func GetAvailableModels(ocDir string) ([]ModelEntry, ModelGroups, error) {
	doc, _, _, err := readEffectiveConfigDocument(ocDir)
	if err != nil {
		return nil, ModelGroups{}, err
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, ModelGroups{}, err
	}

	var raw struct {
		Agents struct {
			Defaults struct {
				Models map[string]interface{} `json:"models"`
				Model  FlexibleModelRef       `json:"model"`
			} `json:"defaults"`
			List []struct {
				Model FlexibleModelRef `json:"model"`
			} `json:"list"`
		} `json:"agents"`
		Models struct {
			Providers map[string]struct {
				ApiKey  SecretValue `json:"apiKey"`
				BaseUrl string      `json:"baseUrl"`
				Models  []struct {
					ID string `json:"id"`
				} `json:"models"`
			} `json:"providers"`
		} `json:"models"`
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, ModelGroups{}, err
	}
	if raw.Models.Providers == nil {
		raw.Models.Providers = map[string]struct {
			ApiKey  SecretValue `json:"apiKey"`
			BaseUrl string      `json:"baseUrl"`
			Models  []struct {
				ID string `json:"id"`
			} `json:"models"`
		}{}
	}
	if raw.Env == nil {
		raw.Env = map[string]string{}
	}

	seen := make(map[string]bool)
	var all []ModelEntry
	groups := ModelGroups{}

	addEntry := func(provider, modelID, apiKey, baseUrl string) {
		provider = strings.TrimSpace(provider)
		modelID = strings.TrimSpace(modelID)
		if provider == "" || modelID == "" {
			return
		}
		fullID := provider + "/" + modelID
		if seen[fullID] {
			return
		}
		seen[fullID] = true
		tier, tierLabel, hasApi := classifyModel(provider, modelID, apiKey, baseUrl)
		e := ModelEntry{
			ID:        fullID,
			Label:     modelID,
			Provider:  provider,
			Tier:      tier,
			TierLabel: tierLabel,
			HasAPIKey: hasApi,
		}
		all = append(all, e)
		switch tier {
		case "local":
			groups.Local = append(groups.Local, e)
		case "cloud_proxied":
			groups.CloudProxied = append(groups.CloudProxied, e)
		default:
			groups.Cloud = append(groups.Cloud, e)
		}
	}
	addModelRef := func(ref string) {
		provider, modelID := splitModelRef(ref)
		apiKey, baseUrl := "", ""
		if prov, ok := raw.Models.Providers[provider]; ok {
			apiKey = prov.ApiKey.Resolve()
			baseUrl = prov.BaseUrl
		}
		addEntry(provider, modelID, apiKey, baseUrl)
	}

	// ── Layer 1: Catalog dari agents.defaults.models di openclaw.json ──
	for modelRef := range raw.Agents.Defaults.Models {
		addModelRef(modelRef)
	}

	// ── Layer 2: agents.defaults.model.primary/fallbacks ─────────────────────
	for _, ref := range raw.Agents.Defaults.Model.Refs() {
		addModelRef(ref)
	}

	// ── Layer 3: Model explicit dari models.providers[].models[] ──────────────
	for providerName, prov := range raw.Models.Providers {
		for _, m := range prov.Models {
			if m.ID != "" {
				addEntry(providerName, m.ID, prov.ApiKey.Resolve(), prov.BaseUrl)
			}
		}
	}

	// ── Layer 4: Model dari agents.list yang belum ada di providers ───────────
	for _, ag := range raw.Agents.List {
		for _, ref := range ag.Model.Refs() {
			addModelRef(ref)
		}
	}

	// ── Layer 5: Ollama implicit discovery via REST /api/tags ─────────────────
	// Sesuai dokumentasi OpenClaw: jika provider ollama tersedia, query /api/tags
	// untuk mendapatkan semua model lokal yang ter-install.
	if ollamaProv, ok := raw.Models.Providers["ollama"]; ok {
		baseUrl := ollamaProv.BaseUrl
		if baseUrl == "" {
			// Baca dari env yang sudah di-inject oleh manager.go dari config
			baseUrl = os.Getenv("OLLAMA_EMBED_URL")
			if baseUrl == "" {
				baseUrl = "http://127.0.0.1:11434"
			}
		}
		tags, err := fetchOllamaTags(baseUrl)
		if err == nil {
			for _, name := range tags {
				addEntry("ollama", name, ollamaProv.ApiKey.Resolve(), baseUrl)
			}
		}
	}

	// ── Layer 6: OpenRouter live model fetch ──────────────────────────────────
	// Jika OPENROUTER_API_KEY ada di env section openclaw.json, fetch list model live.
	orKey := raw.Env["OPENROUTER_API_KEY"]
	// Juga cek models.providers.openrouter.apiKey
	if orKey == "" {
		if orProv, ok := raw.Models.Providers["openrouter"]; ok {
			orKey = orProv.ApiKey.Resolve()
		}
	}
	if orKey != "" && !strings.HasPrefix(orKey, "sk-or-PLACEHOLDER") {
		orModels, err := fetchOpenRouterModels(orKey)
		if err == nil {
			for _, m := range orModels {
				addEntry("openrouter", m, orKey, "https://openrouter.ai/api/v1")
			}
		}
	}

	return all, groups, nil
}

func GetProviderModels(ocDir, providerName string) ([]ModelEntry, error) {
	all, _, err := GetAvailableModels(ocDir)
	if err != nil {
		return nil, err
	}
	providerName = strings.TrimSpace(strings.ToLower(providerName))
	filtered := make([]ModelEntry, 0)
	for _, entry := range all {
		if strings.EqualFold(entry.Provider, providerName) {
			filtered = append(filtered, entry)
		}
	}
	return filtered, nil
}

func splitModelRef(ref string) (string, string) {
	ref = strings.TrimSpace(ref)
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return parts[0], parts[1]
	}
	return "unknown", ref
}

// fetchOllamaTags melakukan HTTP GET ke {baseUrl}/api/tags dan mengembalikan
// list nama model yang ter-install di Ollama lokal.
func fetchOllamaTags(baseUrl string) ([]string, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(strings.TrimRight(baseUrl, "/") + "/api/tags")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(result.Models))
	for _, m := range result.Models {
		if m.Name != "" {
			names = append(names, m.Name)
		}
	}
	return names, nil
}

// fetchOpenRouterModels mengambil list model dari OpenRouter API menggunakan API key.
// Mengembalikan model ID dalam format singkat (tanpa prefix "openrouter/").
func fetchOpenRouterModels(apiKey string) ([]string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", "https://openrouter.ai/api/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("HTTP-Referer", "https://openclaw.ai")
	req.Header.Set("X-OpenRouter-Title", "OpenClaw Multi-Agent")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openrouter API status: %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

// UpdateAgentModelInOCConfig mengupdate field model untuk agent tertentu di openclaw.json.
func UpdateAgentModelInOCConfig(ocDir, agentID, model string) error {
	doc, raw, configPath, err := readConfigDocument(ocDir)
	if err != nil {
		return err
	}

	agents := objectField(doc, "agents")
	list := agentListField(agents)
	found := false
	for _, item := range list {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if agentEntryIDMap(entry) == agentID {
			entry["model"] = model
			found = true
			break
		}
	}

	if !found {
		// Agent tidak ada di agents.list, buat entry minimal agar model tersimpan
		list = append(list, map[string]interface{}{
			"id":    agentID,
			"name":  agentID,
			"model": model,
		})
		agents["list"] = list
	}

	return writeConfigDocument(configPath, raw, ".model-bak", doc)
}
