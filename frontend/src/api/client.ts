// api/client.ts — API client v2 dengan auth token support

// @ts-ignore
export const API_BASE = (typeof __API_URL__ !== 'undefined' ? __API_URL__ : import.meta.env.VITE_API_URL) || window.location.origin;

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

export interface CreateAgentPayload {
  id: string; label: string; telegram_bot_token: string;
  openclaw_model?: string; persona?: string;
  workspace_files?: Record<string, string>;
}

// Model classification types
export interface ModelEntry {
  id: string;         // "ollama/kimi-k2.5:cloud"
  label: string;      // "kimi-k2.5:cloud"
  provider: string;   // "ollama"
  tier: 'local' | 'cloud_proxied' | 'cloud';
  tier_label: string; // "☁️ Cloud Proxied"
  has_api_key: boolean; // true jika menggunakan real API key (bukan placeholder)
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
  created_at: string;
}

export interface CreateGatewayPayload {
  agent_id: string;
  platform: string;
  label: string;
  config: Record<string, string>;
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
  updateAgentModel(id: string, model: string): Promise<{ message: string; model: string }> {
    return request(`/api/agents/${id}/model`, {
      method: 'PUT',
      body: JSON.stringify({ model }),
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
  createGateway(payload: CreateGatewayPayload): Promise<{ message: string; id: string; webhook_url: string }> {
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

  // Code Tools (Claude Code + OpenCode)
  getCodeToolsStatus(): Promise<{ tools: CodeToolStatus[] }> {
    return request('/api/code-tools/status');
  },
  // Note: install & uninstall use fetch() with streaming directly in CodeToolsPage
  startCodeTool(tool: string, apiKey?: string, provider?: string): Promise<{ ok: boolean; message: string; pid: number }> {
    return request('/api/code-tools/start', {
      method: 'POST',
      body: JSON.stringify({ tool, api_key: apiKey ?? '', provider: provider ?? 'anthropic' }),
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
};

export interface CodeToolStatus {
  tool: string;
  installed: boolean;
  running: boolean;
  version?: string;
  bin_path?: string;
  ready: boolean;
  validation_message?: string;
}
