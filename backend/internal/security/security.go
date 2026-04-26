package security

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"multiagent-platform/internal/config"
)

const (
	ModeOff    = "off"
	ModeOn     = "on"
	ModeStrict = "strict"

	maxAuditStringLen    = 2048
	maxAuditMetadataKeys = 32
	maxRenderedOutputLen = 50000
	maxAuditQueryLimit   = 500
)

type Policy struct {
	MaskingMode              string `json:"masking_mode"`
	LoggingMode              string `json:"logging_mode"`
	TemplateRenderingEnabled bool   `json:"template_rendering_enabled"`
	DefaultTemplateID        string `json:"default_template_id,omitempty"`
	AuditDropped             uint64 `json:"audit_dropped,omitempty"`
	AuditWriteErrors         uint64 `json:"audit_write_errors,omitempty"`
	AuditIndexEnabled        bool   `json:"audit_index_enabled,omitempty"`
}

type MaskingRule struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Pattern     string `json:"pattern"`
	Strategy    string `json:"strategy"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description,omitempty"`
}

type RuleSet struct {
	Rules     []MaskingRule `json:"rules"`
	UpdatedAt time.Time     `json:"updated_at"`
}

type MaskResult struct {
	Text      string   `json:"text"`
	Mode      string   `json:"mode"`
	Blocked   bool     `json:"blocked"`
	Matches   []string `json:"matches"`
	Changed   bool     `json:"changed"`
	Error     string   `json:"error,omitempty"`
	CheckedAt string   `json:"checked_at"`
}

type AuditEvent struct {
	Timestamp   time.Time              `json:"timestamp"`
	RequestID   string                 `json:"request_id,omitempty"`
	Level       string                 `json:"level"`
	AgentID     string                 `json:"agent_id,omitempty"`
	Channel     string                 `json:"channel,omitempty"`
	Model       string                 `json:"model,omitempty"`
	PromptHash  string                 `json:"prompt_hash,omitempty"`
	OutputHash  string                 `json:"output_hash,omitempty"`
	MaskingMode string                 `json:"masking_mode,omitempty"`
	Usage       map[string]interface{} `json:"usage,omitempty"`
	LatencyMS   int64                  `json:"latency_ms,omitempty"`
	Status      string                 `json:"status,omitempty"`
	ErrorClass  string                 `json:"error_class,omitempty"`
	Message     string                 `json:"message,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

type AuditQuery struct {
	AgentID string
	Level   string
	Channel string
	Status  string
	Query   string
	From    time.Time
	To      time.Time
	Limit   int
}

type Template struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Type             string            `json:"type"`
	Content          string            `json:"content"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	Version          int               `json:"version"`
	Versions         []TemplateVersion `json:"versions,omitempty"`
	ValidationStatus string            `json:"validation_status"`
}

type TemplateVersion struct {
	Version   int       `json:"version"`
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updated_at"`
}

type TemplateStore struct {
	Templates []Template `json:"templates"`
}

type TemplateValidation struct {
	Valid        bool     `json:"valid"`
	Errors       []string `json:"errors"`
	Placeholders []string `json:"placeholders"`
}

type Manager struct {
	mu     sync.RWMutex
	store  *encryptedStore
	policy Policy
	rules  RuleSet
	tpls   TemplateStore
	queue  chan AuditEvent
	index  *auditIndex

	auditDropped    atomic.Uint64
	auditWriteError atomic.Uint64
}

func NewManager(cfg *config.Config) (*Manager, error) {
	keyMaterial, err := resolveStoreKey(cfg.SecurityStoreDir, cfg.SecurityStoreKey)
	if err != nil {
		return nil, err
	}
	store, err := newEncryptedStore(cfg.SecurityStoreDir, keyMaterial)
	if err != nil {
		return nil, err
	}
	idx, idxErr := newAuditIndex(cfg.SecurityStoreDir)
	if idxErr != nil {
		log.Printf("[security] audit index disabled: %v", idxErr)
	}
	m := &Manager{
		store: store,
		policy: Policy{
			MaskingMode:              normalizeMode(cfg.MaskingMode),
			LoggingMode:              normalizeLoggingMode(cfg.LoggingMode),
			TemplateRenderingEnabled: cfg.TemplateRenderingEnabled,
		},
		rules: RuleSet{Rules: defaultRules(), UpdatedAt: time.Now()},
		tpls:  TemplateStore{Templates: []Template{}},
		queue: make(chan AuditEvent, 500),
		index: idx,
	}
	m.load()
	if m.index != nil {
		if err := m.rebuildAuditIndex(); err != nil {
			m.auditWriteError.Add(1)
			log.Printf("[security] audit index rebuild failed: %v", err)
		}
	}
	go m.auditWorker()
	return m, nil
}

func (m *Manager) GetPolicy() Policy {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p := m.policy
	p.AuditDropped = m.auditDropped.Load()
	p.AuditWriteErrors = m.auditWriteError.Load()
	p.AuditIndexEnabled = m.index != nil
	return p
}

func (m *Manager) UpdatePolicy(p Policy) (Policy, error) {
	p.MaskingMode = normalizeMode(p.MaskingMode)
	p.LoggingMode = normalizeLoggingMode(p.LoggingMode)
	p.AuditDropped = 0
	p.AuditWriteErrors = 0
	p.AuditIndexEnabled = false
	m.mu.Lock()
	m.policy = p
	m.mu.Unlock()
	if err := m.store.writeJSON("policy", p); err != nil {
		return p, err
	}
	return m.GetPolicy(), nil
}

func (m *Manager) GetRules() RuleSet {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rules
}

func (m *Manager) UpdateRules(rules []MaskingRule) (RuleSet, error) {
	if len(rules) > 100 {
		return RuleSet{}, fmt.Errorf("maksimal 100 masking rule")
	}
	for i := range rules {
		if err := validateRule(rules[i]); err != nil {
			return RuleSet{}, err
		}
	}
	rs := RuleSet{Rules: rules, UpdatedAt: time.Now()}
	m.mu.Lock()
	m.rules = rs
	m.mu.Unlock()
	return rs, m.store.writeJSON("rulesets", rs)
}

func (m *Manager) Mask(text, mode string) MaskResult {
	m.mu.RLock()
	if mode == "" {
		mode = m.policy.MaskingMode
	}
	rules := append([]MaskingRule(nil), m.rules.Rules...)
	m.mu.RUnlock()

	mode = normalizeMode(mode)
	result := MaskResult{Text: text, Mode: mode, CheckedAt: time.Now().Format(time.RFC3339)}
	if mode == ModeOff || text == "" {
		return result
	}

	out := text
	seen := map[string]bool{}
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			continue
		}
		if re.MatchString(out) {
			seen[rule.ID] = true
			out = applyRule(re, out, rule.Strategy)
		}
	}
	result.Text = out
	result.Changed = out != text
	for id := range seen {
		result.Matches = append(result.Matches, id)
	}
	sort.Strings(result.Matches)

	if mode == ModeStrict {
		remaining := detectMatches(out, rules)
		if len(remaining) > 0 {
			result.Blocked = true
			result.Error = "strict masking blocked output"
			result.Matches = mergeStrings(result.Matches, remaining)
			result.Text = "[BLOCKED_BY_MASKING_POLICY]"
		}
	}
	return result
}

func (m *Manager) EnqueueAudit(event AuditEvent) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	if event.Level == "" {
		event.Level = "INFO"
	}
	if event.MaskingMode == "" {
		event.MaskingMode = m.GetPolicy().MaskingMode
	}
	var err error
	event, err = sanitizeAuditEvent(event)
	if err != nil {
		m.auditWriteError.Add(1)
		log.Printf("[security] audit event rejected: %v", err)
		return
	}
	select {
	case m.queue <- event:
	default:
		m.auditDropped.Add(1)
	}
}

func (m *Manager) QueryAudit(q AuditQuery) ([]AuditEvent, error) {
	if err := validateAuditQuery(q); err != nil {
		return nil, err
	}
	if q.Limit <= 0 {
		q.Limit = 100
	}
	if q.Limit > maxAuditQueryLimit {
		q.Limit = maxAuditQueryLimit
	}
	if m.index != nil {
		rows, err := m.index.query(q)
		if err == nil {
			return rows, nil
		}
		m.auditWriteError.Add(1)
		log.Printf("[security] audit index query failed, falling back to JSONL scan: %v", err)
	}
	path := filepath.Join(m.store.dir, "audit.jsonl")
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return []AuditEvent{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var rows []AuditEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := trimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		plain, err := m.store.decryptLine(line)
		if err != nil {
			continue
		}
		var ev AuditEvent
		if err := json.Unmarshal(plain, &ev); err != nil {
			continue
		}
		if q.AgentID != "" && ev.AgentID != q.AgentID {
			continue
		}
		if q.Level != "" && !strings.EqualFold(ev.Level, q.Level) {
			continue
		}
		if q.Channel != "" && !strings.EqualFold(ev.Channel, q.Channel) {
			continue
		}
		if q.Status != "" && !strings.EqualFold(ev.Status, q.Status) {
			continue
		}
		if q.Query != "" && !auditMatchesQuery(ev, q.Query) {
			continue
		}
		if !q.From.IsZero() && ev.Timestamp.Before(q.From) {
			continue
		}
		if !q.To.IsZero() && ev.Timestamp.After(q.To) {
			continue
		}
		rows = append(rows, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(rows) > q.Limit {
		rows = rows[len(rows)-q.Limit:]
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows, nil
}

func (m *Manager) ListTemplates() []Template {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := append([]Template(nil), m.tpls.Templates...)
	return out
}

func (m *Manager) UpsertTemplate(t Template) (Template, error) {
	v := ValidateTemplate(t.Content)
	if !v.Valid {
		return Template{}, errors.New(strings.Join(v.Errors, "; "))
	}
	now := time.Now()
	if t.ID == "" {
		t.ID = generateID("tpl")
	}
	if t.Name == "" {
		t.Name = t.ID
	}
	if t.Type == "" {
		t.Type = "report_template"
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.tpls.Templates {
		if m.tpls.Templates[i].ID == t.ID {
			prev := m.tpls.Templates[i]
			prev.Versions = append(prev.Versions, TemplateVersion{Version: prev.Version, Content: prev.Content, UpdatedAt: prev.UpdatedAt})
			if len(prev.Versions) > 20 {
				prev.Versions = prev.Versions[len(prev.Versions)-20:]
			}
			prev.Name = t.Name
			prev.Type = t.Type
			prev.Content = t.Content
			prev.UpdatedAt = now
			prev.Version++
			prev.ValidationStatus = "valid"
			m.tpls.Templates[i] = prev
			return prev, m.store.writeJSON("templates", m.tpls)
		}
	}
	t.CreatedAt = now
	t.UpdatedAt = now
	t.Version = 1
	t.ValidationStatus = "valid"
	m.tpls.Templates = append(m.tpls.Templates, t)
	return t, m.store.writeJSON("templates", m.tpls)
}

func (m *Manager) DeleteTemplate(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.tpls.Templates {
		if m.tpls.Templates[i].ID == id {
			m.tpls.Templates = append(m.tpls.Templates[:i], m.tpls.Templates[i+1:]...)
			return m.store.writeJSON("templates", m.tpls)
		}
	}
	return fmt.Errorf("template tidak ditemukan")
}

func (m *Manager) RenderTemplate(templateID string, values map[string]interface{}) (string, error) {
	if strings.TrimSpace(templateID) == "" {
		return "", fmt.Errorf("template id diperlukan")
	}
	m.mu.RLock()
	enabled := m.policy.TemplateRenderingEnabled
	var selected *Template
	for i := range m.tpls.Templates {
		if m.tpls.Templates[i].ID == templateID {
			c := m.tpls.Templates[i]
			selected = &c
			break
		}
	}
	m.mu.RUnlock()
	if !enabled {
		return "", fmt.Errorf("template rendering disabled")
	}
	if selected == nil {
		return "", fmt.Errorf("template tidak ditemukan")
	}
	rendered, err := RenderTemplateContent(selected.Content, values)
	if err != nil {
		return "", err
	}
	if len(rendered) > maxRenderedOutputLen {
		return "", fmt.Errorf("rendered template maksimal %d karakter", maxRenderedOutputLen)
	}
	return rendered, nil
}

func ValidateTemplate(content string) TemplateValidation {
	var out TemplateValidation
	if len(content) == 0 {
		out.Errors = append(out.Errors, "template tidak boleh kosong")
	}
	if len(content) > 20000 {
		out.Errors = append(out.Errors, "template maksimal 20000 karakter")
	}
	re := regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.]+)\s*\}\}`)
	matches := re.FindAllStringSubmatch(content, -1)
	if len(matches) > 100 {
		out.Errors = append(out.Errors, "placeholder maksimal 100")
	}
	seen := map[string]bool{}
	for _, m := range matches {
		key := m[1]
		if strings.Count(key, ".") > 3 {
			out.Errors = append(out.Errors, "placeholder terlalu dalam: "+key)
		}
		if !allowedPlaceholder(key) {
			out.Errors = append(out.Errors, "placeholder tidak dikenal: "+key)
		}
		if !seen[key] {
			out.Placeholders = append(out.Placeholders, key)
			seen[key] = true
		}
	}
	if strings.Contains(content, "{{#") || strings.Contains(content, "{{/") || strings.Contains(content, "{{>") {
		out.Errors = append(out.Errors, "block/helper template tidak didukung")
	}
	sort.Strings(out.Placeholders)
	out.Valid = len(out.Errors) == 0
	return out
}

func RenderTemplateContent(content string, values map[string]interface{}) (string, error) {
	v := ValidateTemplate(content)
	if !v.Valid {
		return "", errors.New(strings.Join(v.Errors, "; "))
	}
	flat := map[string]string{}
	flatten("", values, flat)
	re := regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.]+)\s*\}\}`)
	out := re.ReplaceAllStringFunc(content, func(match string) string {
		sub := re.FindStringSubmatch(match)
		if len(sub) != 2 {
			return ""
		}
		return flat[sub[1]]
	})
	if len(out) > maxRenderedOutputLen {
		return "", fmt.Errorf("rendered template maksimal %d karakter", maxRenderedOutputLen)
	}
	return out, nil
}

func HashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func ShortHash(text string) string {
	h := HashText(text)
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func MaskKnownSecrets(text string) string {
	return defaultMaskString(text)
}

func (m *Manager) LoadGatewaySecret(id string) (string, map[string]string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	store, err := m.readGatewaySecretStore()
	if err != nil {
		return "", nil, false
	}
	rec, ok := store.Gateways[id]
	if !ok {
		return "", nil, false
	}
	cfg := map[string]string{}
	for k, v := range rec.Config {
		cfg[k] = v
	}
	return rec.WebhookSecret, cfg, true
}

func (m *Manager) SaveGatewaySecret(id, webhookSecret string, config map[string]string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("gateway id diperlukan")
	}
	clean := map[string]string{}
	for k, v := range config {
		clean[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	store, err := m.readGatewaySecretStore()
	if err != nil {
		return err
	}
	store.Gateways[id] = gatewaySecretRecord{
		WebhookSecret: strings.TrimSpace(webhookSecret),
		Config:        clean,
	}
	return m.store.writeJSON("gateway_secrets", store)
}

func (m *Manager) DeleteGatewaySecret(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	store, err := m.readGatewaySecretStore()
	if err != nil {
		return err
	}
	delete(store.Gateways, id)
	return m.store.writeJSON("gateway_secrets", store)
}

func (m *Manager) load() {
	var p Policy
	if err := m.store.readJSON("policy", &p); err == nil {
		p.MaskingMode = normalizeMode(p.MaskingMode)
		p.LoggingMode = normalizeLoggingMode(p.LoggingMode)
		p.AuditDropped = 0
		p.AuditWriteErrors = 0
		p.AuditIndexEnabled = false
		m.policy = p
	}
	var rs RuleSet
	if err := m.store.readJSON("rulesets", &rs); err == nil && len(rs.Rules) > 0 {
		m.rules = rs
	}
	var ts TemplateStore
	if err := m.store.readJSON("templates", &ts); err == nil {
		m.tpls = ts
	}
}

type gatewaySecretRecord struct {
	WebhookSecret string            `json:"webhook_secret,omitempty"`
	Config        map[string]string `json:"config,omitempty"`
}

type gatewaySecretStore struct {
	Gateways map[string]gatewaySecretRecord `json:"gateways"`
}

func (m *Manager) readGatewaySecretStore() (gatewaySecretStore, error) {
	store := gatewaySecretStore{Gateways: map[string]gatewaySecretRecord{}}
	if err := m.store.readJSON("gateway_secrets", &store); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return store, nil
		}
		return store, err
	}
	if store.Gateways == nil {
		store.Gateways = map[string]gatewaySecretRecord{}
	}
	return store, nil
}

func (m *Manager) auditWorker() {
	for ev := range m.queue {
		if err := m.store.appendEncryptedLine("audit", ev); err != nil {
			m.auditWriteError.Add(1)
			log.Printf("[security] audit append failed: %v", err)
			continue
		}
		if m.index != nil {
			if err := m.index.add(ev); err != nil {
				m.auditWriteError.Add(1)
				log.Printf("[security] audit index write failed: %v", err)
			}
		}
	}
}

func (m *Manager) rebuildAuditIndex() error {
	if m.index == nil {
		return nil
	}
	if err := m.index.reset(); err != nil {
		return err
	}
	path := filepath.Join(m.store.dir, "audit.jsonl")
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := trimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		plain, err := m.store.decryptLine(line)
		if err != nil {
			continue
		}
		var ev AuditEvent
		if err := json.Unmarshal(plain, &ev); err != nil {
			continue
		}
		ev, err = sanitizeAuditEvent(ev)
		if err != nil {
			continue
		}
		if ev.Timestamp.IsZero() {
			ev.Timestamp = time.Now()
		}
		if err := m.index.add(ev); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func resolveStoreKey(storeDir, configuredKey string) (string, error) {
	if strings.TrimSpace(configuredKey) != "" {
		return configuredKey, nil
	}
	if err := os.MkdirAll(storeDir, 0700); err != nil {
		return "", fmt.Errorf("buat security store dir: %w", err)
	}
	keyPath := filepath.Join(storeDir, ".store-key")
	if data, err := os.ReadFile(keyPath); err == nil {
		key := strings.TrimSpace(string(data))
		if key != "" {
			return key, nil
		}
	}
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", fmt.Errorf("generate security store key: %w", err)
	}
	key := base64.RawURLEncoding.EncodeToString(raw)
	if err := os.WriteFile(keyPath, []byte(key+"\n"), 0600); err != nil {
		return "", fmt.Errorf("tulis security store key: %w", err)
	}
	return key, nil
}

func defaultRules() []MaskingRule {
	return []MaskingRule{
		{ID: "email", Label: "Email", Pattern: `[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`, Strategy: "replace", Enabled: true},
		{ID: "phone", Label: "Phone Number", Pattern: `\b(?:\+?\d[\d\s().-]{7,}\d)\b`, Strategy: "partial", Enabled: true},
		{ID: "telegram_bot_token", Label: "Telegram Bot Token", Pattern: `\b\d{6,12}:[A-Za-z0-9_-]{30,}\b`, Strategy: "redact", Enabled: true},
		{ID: "api_key", Label: "API Key", Pattern: `\b(?:sk|pk|xox[baprs]|ghp|gho|AIza)[A-Za-z0-9_\-]{16,}\b`, Strategy: "hash", Enabled: true},
		{ID: "internal_file_reference", Label: "Internal File Reference", Pattern: `(?i)(/etc/passwd|/etc/shadow|openclaw\.json|\.env\b|SOUL\.md|AGENTS\.md|IDENTITY\.md|TOOLS\.md)`, Strategy: "replace", Enabled: true},
		{ID: "prompt_disclosure_probe", Label: "Prompt Disclosure Probe", Pattern: `(?is)\b(reveal|show|print|dump|display|read|analyze|explain|translate)\b.{0,80}\b(system prompt|developer prompt|instructions|internal config|secret|token|environment|env|soul\.md|agents\.md|openclaw\.json)\b`, Strategy: "replace", Enabled: true},
		{ID: "template_function_probe", Label: "Template Function Probe", Pattern: `(?is)(\{\{[^}]*\b(printf|html|self|globals|builtins|import|popen|system|eval|request|config|env|read|file)\b)|(<%=?\s*[^%]*(system|eval|exec|whoami|id)\b)|(\[%\s*[^%]*(system|eval|exec)\b)`, Strategy: "replace", Enabled: true},
		{ID: "template_context_probe", Label: "Template Context Probe", Pattern: `(?is)\{\{\s*(\.|%#v\s+\.|printf\s+["']%[#a-z0-9]*["']\s+\.)\s*\}\}`, Strategy: "replace", Enabled: true},
	}
}

func validateRule(rule MaskingRule) error {
	if rule.ID == "" || len(rule.ID) > 64 {
		return fmt.Errorf("rule id tidak valid")
	}
	if rule.Label == "" || len(rule.Label) > 128 {
		return fmt.Errorf("rule label tidak valid")
	}
	if len(rule.Pattern) == 0 || len(rule.Pattern) > 2048 {
		return fmt.Errorf("rule pattern tidak valid")
	}
	if _, err := regexp.Compile(rule.Pattern); err != nil {
		return fmt.Errorf("regex rule %s tidak valid: %w", rule.ID, err)
	}
	switch rule.Strategy {
	case "replace", "partial", "hash", "redact":
		return nil
	default:
		return fmt.Errorf("strategy rule %s tidak valid", rule.ID)
	}
}

func validateAuditQuery(q AuditQuery) error {
	if q.AgentID != "" && !regexp.MustCompile(`^[a-z0-9_-]{1,32}$`).MatchString(q.AgentID) {
		return fmt.Errorf("agent_id audit tidak valid")
	}
	if q.Channel != "" && !regexp.MustCompile(`^[a-z0-9_-]{1,32}$`).MatchString(strings.ToLower(q.Channel)) {
		return fmt.Errorf("channel audit tidak valid")
	}
	if q.Status != "" && !regexp.MustCompile(`^[a-z0-9_-]{1,32}$`).MatchString(strings.ToLower(q.Status)) {
		return fmt.Errorf("status audit tidak valid")
	}
	if len(q.Query) > 128 {
		return fmt.Errorf("query audit terlalu panjang")
	}
	if q.Level != "" {
		switch strings.ToUpper(q.Level) {
		case "INFO", "DEBUG", "SECURITY", "ERROR":
		default:
			return fmt.Errorf("level audit tidak valid")
		}
	}
	if !q.From.IsZero() && !q.To.IsZero() && q.From.After(q.To) {
		return fmt.Errorf("rentang waktu audit tidak valid")
	}
	return nil
}

func auditMatchesQuery(ev AuditEvent, query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return true
	}
	fields := []string{
		ev.RequestID,
		ev.Level,
		ev.AgentID,
		ev.Channel,
		ev.Model,
		ev.PromptHash,
		ev.OutputHash,
		ev.MaskingMode,
		ev.Status,
		ev.ErrorClass,
		ev.Message,
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), q) {
			return true
		}
	}
	if len(ev.Metadata) > 0 {
		b, _ := json.Marshal(ev.Metadata)
		return strings.Contains(strings.ToLower(string(b)), q)
	}
	return false
}

func SanitizeAuditEvent(event AuditEvent) (AuditEvent, error) {
	return sanitizeAuditEvent(event)
}

func sanitizeAuditEvent(event AuditEvent) (AuditEvent, error) {
	var err error
	event.RequestID = clampAuditString(event.RequestID)
	event.Level = normalizeAuditLevel(event.Level)
	event.AgentID = clampAuditString(event.AgentID)
	event.Channel = clampAuditString(event.Channel)
	event.Model = clampAuditString(event.Model)
	event.PromptHash = clampAuditString(event.PromptHash)
	event.OutputHash = clampAuditString(event.OutputHash)
	event.MaskingMode = normalizeMode(event.MaskingMode)
	event.Status = clampAuditString(event.Status)
	event.ErrorClass = clampAuditString(defaultMaskString(event.ErrorClass))
	event.Message = clampAuditString(defaultMaskString(event.Message))
	event.Metadata, err = sanitizeAuditMetadata(event.Metadata)
	return event, err
}

func sanitizeAuditMetadata(in map[string]interface{}) (map[string]interface{}, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]interface{}, len(in))
	count := 0
	for key, value := range in {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if isSensitiveMetadataKey(key) {
			return nil, fmt.Errorf("metadata audit mengandung key sensitif: %s", key)
		}
		count++
		if count > maxAuditMetadataKeys {
			return nil, fmt.Errorf("metadata audit maksimal %d key", maxAuditMetadataKeys)
		}
		sanitized, err := sanitizeMetadataValue(value)
		if err != nil {
			return nil, err
		}
		out[key] = sanitized
	}
	return out, nil
}

func sanitizeMetadataValue(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case string:
		return clampAuditString(defaultMaskString(v)), nil
	case fmt.Stringer:
		return clampAuditString(defaultMaskString(v.String())), nil
	case float64, float32, int, int64, int32, bool, nil:
		return v, nil
	case map[string]interface{}:
		out := map[string]interface{}{}
		for k, child := range v {
			if isSensitiveMetadataKey(k) {
				return nil, fmt.Errorf("metadata audit mengandung key sensitif: %s", k)
			}
			sanitized, err := sanitizeMetadataValue(child)
			if err != nil {
				return nil, err
			}
			out[k] = sanitized
		}
		return out, nil
	case []string:
		if len(v) > 20 {
			v = v[:20]
		}
		out := make([]interface{}, 0, len(v))
		for _, item := range v {
			out = append(out, clampAuditString(defaultMaskString(item)))
		}
		return out, nil
	case []interface{}:
		if len(v) > 20 {
			v = v[:20]
		}
		out := make([]interface{}, 0, len(v))
		for _, item := range v {
			sanitized, err := sanitizeMetadataValue(item)
			if err != nil {
				return nil, err
			}
			out = append(out, sanitized)
		}
		return out, nil
	default:
		b, _ := json.Marshal(v)
		return clampAuditString(defaultMaskString(string(b))), nil
	}
}

func isSensitiveMetadataKey(key string) bool {
	k := strings.ToLower(key)
	sensitive := []string{"prompt", "output", "token", "api_key", "apikey", "password", "secret", "credential", "authorization", "cookie"}
	for _, part := range sensitive {
		if strings.Contains(k, part) {
			return true
		}
	}
	return false
}

func clampAuditString(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > maxAuditStringLen {
		return v[:maxAuditStringLen] + "...[TRUNCATED]"
	}
	return v
}

func normalizeAuditLevel(v string) string {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "DEBUG", "SECURITY", "ERROR":
		return strings.ToUpper(strings.TrimSpace(v))
	default:
		return "INFO"
	}
}

func defaultMaskString(v string) string {
	out := v
	for _, rule := range defaultRules() {
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			continue
		}
		out = applyRule(re, out, rule.Strategy)
	}
	return out
}

func applyRule(re *regexp.Regexp, text, strategy string) string {
	return re.ReplaceAllStringFunc(text, func(match string) string {
		switch strategy {
		case "partial":
			if len(match) <= 4 {
				return "***"
			}
			return "***" + match[len(match)-4:]
		case "hash":
			return "[HASH:" + ShortHash(match) + "]"
		case "redact":
			return ""
		default:
			return "***MASKED***"
		}
	})
}

func detectMatches(text string, rules []MaskingRule) []string {
	var ids []string
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		re, err := regexp.Compile(rule.Pattern)
		if err == nil && re.MatchString(text) {
			ids = append(ids, rule.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func mergeStrings(a, b []string) []string {
	seen := map[string]bool{}
	for _, v := range a {
		seen[v] = true
	}
	for _, v := range b {
		seen[v] = true
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func normalizeMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case ModeOff, ModeOn, ModeStrict:
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return ModeOn
	}
}

func normalizeLoggingMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "minimal", "audit", "debug":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "audit"
	}
}

func allowedPlaceholder(key string) bool {
	switch key {
	case "agent.id", "agent.reply", "channel", "session.id", "timestamp", "usage.prompt_tokens", "usage.completion_tokens":
		return true
	default:
		return false
	}
}

func flatten(prefix string, value interface{}, out map[string]string) {
	switch v := value.(type) {
	case map[string]interface{}:
		for k, child := range v {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flatten(key, child, out)
		}
	case map[string]string:
		for k, child := range v {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			out[key] = child
		}
	case string:
		out[prefix] = v
	case fmt.Stringer:
		out[prefix] = v.String()
	default:
		b, _ := json.Marshal(v)
		out[prefix] = strings.Trim(string(b), `"`)
	}
}

func generateID(prefix string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", prefix, time.Now().UnixNano())))
	return prefix + "_" + hex.EncodeToString(sum[:])[:12]
}

func encodeJSON(src interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(src); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeJSON(data []byte, dst interface{}) error {
	return json.Unmarshal(data, dst)
}

func trimSpace(data []byte) []byte {
	return bytes.TrimSpace(data)
}
