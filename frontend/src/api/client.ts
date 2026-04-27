// api/client.ts — API client v2 dengan auth token support

// @ts-ignore
const definedApiURL = typeof __API_URL__ !== 'undefined' ? __API_URL__ : undefined;
export const API_BASE = definedApiURL !== undefined ? definedApiURL : (import.meta.env.VITE_API_URL || window.location.origin);

// ── Auth / CSRF ───────────────────────────────────────────────────────────────
const CSRF_COOKIE = 'ocmap_csrf';

function readCookie(name: string): string {
  const parts = document.cookie.split(';');
  for (const part of parts) {
    const [key, ...rest] = part.trim().split('=');
    if (key === name) {
      return decodeURIComponent(rest.join('='));
    }
  }
  return '';
}

export const authStore = {
  getCSRFToken: (): string => readCookie(CSRF_COOKIE),
  clearToken: () => {
    localStorage.removeItem('ocmap_token');
    document.cookie = `${CSRF_COOKIE}=; Max-Age=0; path=/`;
  },
  isLoggedIn: () => !!readCookie(CSRF_COOKIE),
};

function authHeaders(method: string): HeadersInit {
  if (!['POST', 'PUT', 'PATCH', 'DELETE'].includes(method.toUpperCase())) {
    return {};
  }
  const csrf = authStore.getCSRFToken();
  return csrf ? { 'X-CSRF-Token': csrf } : {};
}

// ── Types ─────────────────────────────────────────────────────────────────────

export interface AgentStatus {
  id: string; label: string; running: boolean;
  started_at?: string; pid?: number;
  runtime_source_mtime?: string;
  runtime_stale?: boolean;
  runtime_warning?: string;
}

export interface AgentInfo {
  id: string; label: string; model: string;
  has_token: boolean; status: AgentStatus;
}

export interface HealthInfo {
  status: string; uptime: string; go_version: string; timestamp: string;
}

export interface PersonaInfo {
  agent_id: string; content: string; path: string;
}

export interface DetectedAgent {
  id: string; account_id: string; bot_token: string;
  has_bot_token: boolean; workspace?: string;
}

export interface OCStatus {
  reachable: boolean; gateway_url: string; models: string[];
  detected_agents: DetectedAgent[]; importable_count: number;
  error?: string; checked_at: string;
}

export interface RouterStatus {
  enabled: boolean;
  provider: string;
  base_url: string;
  masked_base_url: string;
  chat_url?: string;
  require_loopback: boolean;
  allow_cloud: boolean;
  token_configured: boolean;
  tool_calls_allowed: boolean;
  runtime_tool?: string;
  runtime_active: boolean;
  agent_routing_enabled: boolean;
  binary_trusted?: boolean;
  binary_resolved_path?: string;
  runtime_isolated?: boolean;
  runtime_dir?: string;
  loopback_enforced?: boolean;
  process_group_enabled?: boolean;
  route_target: 'openclaw_gateway' | 'ai_router' | 'direct_openrouter' | 'direct_openai' | 'direct_google' | 'direct_xai' | 'direct_anthropic' | 'ollama_direct' | 'direct_llamacpp';
  runtime_supported: boolean;
  security_warnings?: string[];
  security_notices?: string[];
  reachable: boolean;
  model_count: number;
  models?: string[];
  warnings?: string[];
  error?: string;
  checked_at: string;
}

export interface CreateAgentPayload {
  id: string; label: string; telegram_bot_token: string;
  openclaw_model?: string; persona?: string;
  template_profile?: string; tone_profile?: string;
  model_route?: ModelRouteMeta;
  workspace_files?: Record<string, string>;
}

export interface AgentTemplateProfile {
  id: string;
  label: string;
  description: string;
}

export interface AgentTemplateCatalog {
  default_role_profile: string;
  default_tone_profile: string;
  files: string[];
  role_profiles: AgentTemplateProfile[];
  tone_profiles: AgentTemplateProfile[];
}

export interface AgentTemplatePreviewPayload {
  id: string;
  label: string;
  template_profile?: string;
  tone_profile?: string;
  workspace_files?: Record<string, string>;
}

export interface AgentTemplatePreview {
  id: string;
  label: string;
  template_profile: string;
  tone_profile: string;
  files: string[];
  workspace_files: Record<string, string>;
}

// Model classification types
export interface ModelEntry {
  id: string;         // "ollama/kimi-k2.5:cloud"
  label: string;      // "kimi-k2.5:cloud"
  provider: string;   // "ollama"
  tier: 'local' | 'cloud_proxied' | 'cloud';
  tier_label: string; // "☁️ Cloud Proxied"
  has_api_key: boolean; // true jika menggunakan real API key (bukan placeholder)
  route_target: 'openclaw_gateway' | 'ai_router' | 'direct_openrouter' | 'direct_openai' | 'direct_google' | 'direct_xai' | 'direct_anthropic' | 'ollama_direct' | 'direct_llamacpp';
  runtime_supported: boolean;
  adapter_id?: string;
  canonical_model?: string;
  outbound_model_example?: string;
  provider_base_url?: string;
  provider_docs_url?: string;
  runtime_notes?: string[];
  warnings?: string[];
}
export interface ModelRouteMeta {
  route_target?: ModelEntry['route_target'];
  tier?: ModelEntry['tier'];
  adapter_id?: string;
  outbound_model?: string;
  provider_base_url?: string;
  runtime_supported?: boolean;
}
export interface ModelGroups {
  cloud: ModelEntry[];
  cloud_proxied: ModelEntry[];
  local: ModelEntry[];
}

export interface ProviderStatus {
  name: string;
  configured: boolean;
  key_hint?: string;
  source: string;
  requires_key: boolean;
  model_count: number;
}

export interface ProviderModelsResponse {
  provider: string;
  models: ModelEntry[];
  count: number;
}

// Daemon control types
export interface DaemonStatus {
  running: boolean;
  pid?: number;
  method: string;
  active_since?: string;
  error?: string;
}

export interface OpenClawCommandArg {
  name: string;
  label: string;
  required: boolean;
  placeholder?: string;
  options?: string[];
}

export interface OpenClawCommand {
  id: string;
  path: string[];
  category: string;
  safety: 'read_only' | 'mutating_confirm' | 'blocked';
  runnable: boolean;
  description: string;
  docs_url: string;
  args?: OpenClawCommandArg[];
}

export interface OpenClawCommandRunResult {
  id: string;
  argv?: string[];
  stdout?: string;
  stderr?: string;
  exit_code: number;
  confirmation_required?: boolean;
  error?: string;
}

// Orchestrator types
export interface SubAgentInfo {
  id: string; label: string; model: string;
  has_token: boolean; status: AgentStatus; role: string;
}
export interface OrchestratorStatus {
  main_agent: AgentInfo;
  sub_agents: SubAgentInfo[];
  total_sub_agents: number;
  running_count: number;
}

// Agent type alias (maps to AgentInfo)
export type Agent = AgentInfo;

// Gateway types
export interface Gateway {
  id: string;
  agent_id: string;
  platform: string;
  label: string;
  webhook_url: string;
  active: boolean;
  public?: boolean;
  auth_mode?: string;
  expires_at?: string;
  expired?: boolean;
  created_at: string;
}

export interface CreateGatewayPayload {
  agent_id: string;
  platform: string;
  label: string;
  config: Record<string, string>;
}

export interface SecurityPolicy {
  masking_mode: 'off' | 'on' | 'strict';
  logging_mode: 'minimal' | 'audit' | 'debug';
  template_rendering_enabled: boolean;
  default_template_id?: string;
  audit_dropped?: number;
  audit_write_errors?: number;
  audit_index_enabled?: boolean;
}

export interface MaskingRule {
  id: string;
  label: string;
  pattern: string;
  strategy: 'replace' | 'partial' | 'hash' | 'redact';
  enabled: boolean;
  description?: string;
}

export interface RuleSet {
  rules: MaskingRule[];
  updated_at: string;
}

export interface AuditLogEntry {
  timestamp: string;
  request_id?: string;
  level: string;
  agent_id?: string;
  channel?: string;
  model?: string;
  prompt_hash?: string;
  output_hash?: string;
  masking_mode?: string;
  latency_ms?: number;
  status?: string;
  error_class?: string;
  message?: string;
  metadata?: Record<string, any>;
}

export interface VisitorRecord {
  occurred_at: string;
  request_id: string;
  gateway_id?: string;
  agent_id?: string;
  channel?: string;
  status?: string;
  decision?: string;
  risk_score?: number;
  risk_reasons?: string[];
  visitor_ip?: string;
  visitor_hash?: string;
  ip_hash?: string;
  ip_class?: string;
  subnet?: string;
  cidr?: string;
  proxy_chain_depth?: number;
  asn?: string;
  org?: string;
  country?: string;
  region?: string;
  city?: string;
  latitude?: number;
  longitude?: number;
  accuracy_km?: number;
  timezone?: string;
  geo_source?: string;
  dns_provider?: string;
  reverse_dns?: string;
  user_agent_family?: string;
  user_agent_hash?: string;
  host_hash?: string;
  origin_hash?: string;
  proto?: string;
  method?: string;
  content_type?: string;
  body_bytes?: number;
  metadata?: Record<string, any>;
}

export interface VisitorQueryResult {
  rows: VisitorRecord[];
  total: number;
  limit: number;
  offset: number;
}

export interface SecurityTemplate {
  id?: string;
  name: string;
  type: string;
  content: string;
  created_at?: string;
  updated_at?: string;
  version?: number;
  validation_status?: string;
}

export interface TemplateValidation {
  valid: boolean;
  errors: string[];
  placeholders: string[];
}

export interface SystemMetrics {
  timestamp: string;
  cpu_percent: number;
  load_1: number;
  num_cpu: number;
  memory_used_mb: number;
  memory_total_mb: number;
  memory_percent: number;
  disk_used_gb: number;
  disk_total_gb: number;
  disk_percent: number;
  go_heap_alloc_mb: number;
  go_goroutines: number;
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const { headers: optHeaders, ...restOptions } = options || {};
  const method = (restOptions.method || 'GET').toUpperCase();
  
  const res = await fetch(`${API_BASE}${path}`, {
    ...restOptions,
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json',
      ...authHeaders(method),
      ...optHeaders,
    },
  });

  // 401 → clear token, caller will redirect to login
  if (res.status === 401) {
    throw new Error('UNAUTHORIZED');
  }

  if (!res.ok) {
    let errMsg = `HTTP ${res.status}`;
    try { const d = await res.json(); errMsg = d.error || errMsg; } catch {}
    throw new Error(errMsg);
  }

  return res.json() as Promise<T>;
}

// ── API ───────────────────────────────────────────────────────────────────────

export const api = {
  // Auth
  login(password: string) {
    return request<{ message: string }>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ password }),
    });
  },
  logout() {
    return request('/api/auth/logout', { method: 'POST' });
  },
  me() {
    return request<{ status: string }>('/api/auth/me');
  },

  // Health
  health(): Promise<HealthInfo> { return request('/api/health'); },

  // Agents
  listAgents(): Promise<AgentInfo[]> { return request('/api/agents'); },

  getAgentTemplates(): Promise<AgentTemplateCatalog> {
    return request('/api/agent-templates');
  },

  previewAgentTemplate(payload: AgentTemplatePreviewPayload): Promise<AgentTemplatePreview> {
    return request('/api/agent-templates/preview', { method: 'POST', body: JSON.stringify(payload) });
  },

  createAgent(payload: CreateAgentPayload): Promise<{ message: string; agent: AgentInfo }> {
    return request('/api/agents', { method: 'POST', body: JSON.stringify(payload) });
  },

  deleteAgent(id: string): Promise<{ message: string }> {
    return request(`/api/agents/${id}`, { method: 'DELETE' });
  },

  startAgent(id: string): Promise<{ message: string; status: AgentStatus }> {
    return request(`/api/agents/${id}/start`, { method: 'POST' });
  },
  stopAgent(id: string): Promise<{ message: string }> {
    return request(`/api/agents/${id}/stop`, { method: 'POST' });
  },
  getAgentStatus(id: string): Promise<AgentStatus> {
    return request(`/api/agents/${id}/status`);
  },

  getPersona(id: string): Promise<PersonaInfo> {
    return request(`/api/agents/${id}/persona`);
  },
  updatePersona(id: string, content: string): Promise<{ message: string }> {
    return request(`/api/agents/${id}/persona`, {
      method: 'PUT', body: content,
      headers: { 'Content-Type': 'text/plain' },
    });
  },

  // Workspace Manager
  getWorkspaceFiles(id: string): Promise<{ agent_id: string; files: string[]; dir: string }> {
    return request(`/api/agents/${id}/workspace`);
  },
  getWorkspaceFile(id: string, filename: string): Promise<{ agent_id: string; filename: string; content: string }> {
    return request(`/api/agents/${id}/workspace/file?name=${encodeURIComponent(filename)}`);
  },
  saveWorkspaceFile(id: string, filename: string, content: string): Promise<{ message: string; filename: string }> {
    return request(`/api/agents/${id}/workspace/file?name=${encodeURIComponent(filename)}`, {
      method: 'PUT',
      body: content,
      headers: { 'Content-Type': 'text/plain' },
    });
  },

  streamLogs(id: string): EventSource {
    // Saat dev proxy mode (API_BASE = ''), gunakan window.location.origin sebagai base
    // agar EventSource mendapat absolute URL yang valid.
    const base = API_BASE || window.location.origin;
    return new EventSource(`${base}/api/agents/${id}/logs`, { withCredentials: true });
  },

  // OpenClaw
  getOCStatus(): Promise<OCStatus> { return request('/api/openclaw/status'); },
  getRouterStatus(tool?: string): Promise<RouterStatus> {
    const query = tool ? `?tool=${encodeURIComponent(tool)}` : '';
    return request(`/api/router/status${query}`);
  },
  importOCAgent(id: string): Promise<{ agent: DetectedAgent; message: string }> {
    return request(`/api/openclaw/import/${id}`, { method: 'POST' });
  },
  getOpenClawCommands(): Promise<{ commands: OpenClawCommand[] }> {
    return request('/api/openclaw/commands');
  },
  async runOpenClawCommand(id: string, args: Record<string, string>, confirm = false): Promise<OpenClawCommandRunResult> {
    const base = API_BASE || '';
    const res = await fetch(`${base}/api/openclaw/commands/run`, {
      method: 'POST',
      credentials: 'include',
      headers: {
        'Content-Type': 'application/json',
        ...authHeaders('POST'),
      },
      body: JSON.stringify({ id, args, confirm }),
    });
    if (res.status === 401) {
      throw new Error('UNAUTHORIZED');
    }
    const data = await res.json().catch(() => null) as OpenClawCommandRunResult | null;
    if (data) return data;
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    throw new Error('Response kosong');
  },

  // Model Management
  getAvailableModels(): Promise<{ models: ModelEntry[]; groups: ModelGroups }> {
    return request('/api/agents/models');
  },
  updateAgentModel(id: string, model: string, modelRoute?: ModelRouteMeta): Promise<{ message: string; model: string }> {
    return request(`/api/agents/${id}/model`, {
      method: 'PUT',
      body: JSON.stringify({ model, model_route: modelRoute }),
    });
  },

  // Providers
  getProviders(): Promise<ProviderStatus[]> {
    return request('/api/providers');
  },
  saveProviderKey(name: string, api_key: string): Promise<{ message: string; provider: string; key_hint: string }> {
    return request(`/api/providers/${name}`, {
      method: 'POST',
      body: JSON.stringify({ api_key }),
    });
  },
  getProviderModels(name: string): Promise<ProviderModelsResponse> {
    return request(`/api/providers/${encodeURIComponent(name)}/models`);
  },

  // OpenClaw Daemon Control
  getDaemonStatus(): Promise<DaemonStatus> {
    return request('/api/openclaw/daemon');
  },
  restartDaemon(): Promise<{ message: string; status: DaemonStatus }> {
    return request('/api/openclaw/daemon/restart', { method: 'POST' });
  },
  stopDaemon(): Promise<{ message: string; status: DaemonStatus }> {
    return request('/api/openclaw/daemon/stop', { method: 'POST' });
  },
  reloadDaemonConfig(): Promise<{ message: string }> {
    return request('/api/openclaw/daemon/reload', { method: 'POST' });
  },

  // Orchestrator
  getOrchestratorStatus(): Promise<OrchestratorStatus> {
    return request('/api/orchestrator/status');
  },
  updateOrchestratorSOUL(): Promise<{ message: string; path: string }> {
    return request('/api/orchestrator/soul', { method: 'PUT' });
  },
  // ── Gateway Manager ───────────────────────────────────────────────────────
  getGateways(agentId?: string): Promise<Gateway[]> {
    const qs = agentId ? `?agent_id=${encodeURIComponent(agentId)}` : '';
    return request(`/api/gateway${qs}`);
  },
  createGateway(payload: CreateGatewayPayload): Promise<{ message: string; id: string; webhook_url: string; webhook_secret?: string; auth_mode?: string; expires_at?: string }> {
    return request('/api/gateway', {
      method: 'POST',
      body: JSON.stringify(payload),
      headers: { 'Content-Type': 'application/json' },
    });
  },
  deleteGateway(id: string): Promise<{ message: string }> {
    return request(`/api/gateway/${id}`, { method: 'DELETE' });
  },

  // Hardening
  hardenPreview() {
    return request<any>('/api/system/harden/preview', { method: 'GET' });
  },
  hardenExecute(selectedTasks: string[]) {
    return request<any>('/api/system/harden/execute', { 
      method: 'POST',
      body: JSON.stringify({ selected_tasks: selectedTasks })
    });
  },

  // Security
  getSecurityPolicy(): Promise<SecurityPolicy> {
    return request('/api/security/policy');
  },
  updateSecurityPolicy(payload: SecurityPolicy): Promise<SecurityPolicy> {
    return request('/api/security/policy', { method: 'PUT', body: JSON.stringify(payload) });
  },
  getMaskingRules(): Promise<RuleSet> {
    return request('/api/security/masking/rulesets');
  },
  updateMaskingRules(rules: MaskingRule[]): Promise<RuleSet> {
    return request('/api/security/masking/rulesets', { method: 'PUT', body: JSON.stringify({ rules }) });
  },
	  getAuditLogs(filters?: { agent_id?: string; level?: string; channel?: string; status?: string; q?: string; limit?: number }): Promise<{ logs: AuditLogEntry[] }> {
    const qs = new URLSearchParams();
    if (filters?.agent_id) qs.set('agent_id', filters.agent_id);
    if (filters?.level) qs.set('level', filters.level);
    if (filters?.channel) qs.set('channel', filters.channel);
    if (filters?.status) qs.set('status', filters.status);
    if (filters?.q) qs.set('q', filters.q);
    if (filters?.limit) qs.set('limit', String(filters.limit));
    const query = qs.toString();
	    return request(`/api/security/audit-logs${query ? `?${query}` : ''}`);
	  },
	  getVisitors(filters?: { q?: string; limit?: number; offset?: number }): Promise<VisitorQueryResult> {
	    const qs = new URLSearchParams();
	    if (filters?.q) qs.set('q', filters.q);
	    if (filters?.limit) qs.set('limit', String(filters.limit));
	    if (filters?.offset) qs.set('offset', String(filters.offset));
	    const query = qs.toString();
	    return request(`/api/visitors${query ? `?${query}` : ''}`);
	  },
  getSecurityTemplates(): Promise<{ templates: SecurityTemplate[] }> {
    return request('/api/security/templates');
  },
  saveSecurityTemplate(payload: SecurityTemplate): Promise<SecurityTemplate> {
    return request('/api/security/templates', { method: payload.id ? 'PUT' : 'POST', body: JSON.stringify(payload) });
  },
  deleteSecurityTemplate(id: string): Promise<{ message: string }> {
    return request(`/api/security/templates/${encodeURIComponent(id)}`, { method: 'DELETE' });
  },
  validateSecurityTemplate(content: string): Promise<TemplateValidation> {
    return request('/api/security/templates/validate', { method: 'POST', body: JSON.stringify({ content }) });
  },

  getSystemMetrics(): Promise<SystemMetrics> {
    return request('/api/system/metrics');
  },

  // Code Tools (Claude Code + OpenCode)
  getCodeToolsStatus(): Promise<{ tools: CodeToolStatus[] }> {
    return request('/api/code-tools/status');
  },
  // Note: install & uninstall use fetch() with streaming directly in CodeToolsPage
  startCodeTool(tool: string, apiKey?: string, provider?: string, listenPort?: number): Promise<{ ok: boolean; message: string; pid: number; endpoint?: string }> {
    return request('/api/code-tools/start', {
      method: 'POST',
      body: JSON.stringify({ tool, api_key: apiKey ?? '', provider: provider ?? 'anthropic', listen_port: listenPort || undefined }),
      headers: { 'Content-Type': 'application/json' },
    });
  },
  validateCodeTool(tool: string, apiKey?: string): Promise<CodeToolStatus> {
    return request('/api/code-tools/validate', {
      method: 'POST',
      body: JSON.stringify({ tool, api_key: apiKey ?? '' }),
      headers: { 'Content-Type': 'application/json' },
    });
  },
  stopCodeTool(tool: string): Promise<{ ok: boolean; message: string }> {
    return request('/api/code-tools/stop', {
      method: 'POST',
      body: JSON.stringify({ tool }),
      headers: { 'Content-Type': 'application/json' },
    });
  },
  stopUnmanagedCodeTool(tool: string, pid?: number, listenPort?: number): Promise<{ ok: boolean; message: string }> {
    return request('/api/code-tools/stop-unmanaged', {
      method: 'POST',
      body: JSON.stringify({ tool, pid: pid || undefined, listen_port: listenPort || undefined }),
      headers: { 'Content-Type': 'application/json' },
    });
  },
  streamCodeToolLogs(tool: string): EventSource {
    const base = API_BASE || window.location.origin;
    return new EventSource(`${base}/api/code-tools/logs/${encodeURIComponent(tool)}`, { withCredentials: true });
  },
};

export interface CodeToolStatus {
  tool: string;
  installed: boolean;
  running: boolean;
  managed?: boolean;
  control_state?: string;
  version?: string;
  bin_path?: string;
  ready: boolean;
  validation_message?: string;
  listen_host?: string;
  listen_port?: number;
  endpoint?: string;
  port_available?: boolean;
  port_owner?: string;
  unmanaged_pid?: number;
  unmanaged_command?: string;
  can_stop_unmanaged?: boolean;
  conflict_message?: string;
  binary_trusted?: boolean;
  binary_resolved_path?: string;
  runtime_isolated?: boolean;
  runtime_dir?: string;
  loopback_enforced?: boolean;
  process_group_enabled?: boolean;
  security_warnings?: string[];
  security_notices?: string[];
}
