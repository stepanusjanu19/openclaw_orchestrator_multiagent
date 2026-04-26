// pages/CodeToolsPage.ts
// Halaman manajemen Code Tools: Claude Code, OpenCode, 9Router, dan OmniRoute.
// Toggle switch -> install/uninstall + start/stop untuk tool daemon lokal.

import { api, API_BASE, authStore, type CodeToolStatus } from '../api/client.js';
import { showToast } from '../components/Toast.js';

interface ToolDef {
  id: 'claude' | 'opencode' | '9router' | 'omniroute';
  mode: 'validate' | 'daemon';
  name: string;
  icon: string;
  description: string;
  installNote: string;
  apiKeyLabel?: string;
  apiKeyPlaceholder?: string;
  showApiKey?: boolean;
  showPort?: boolean;
  providers?: string[];
  accentColor: string;
}

const TOOLS: ToolDef[] = [
  {
    id: 'claude',
    mode: 'validate',
    name: 'Claude Code',
    icon: '🧠',
    description: 'Anthropic\'s agentic coding CLI — analisis codebase, refactor, debug, dan generate kode menggunakan Claude Sonnet/Opus.',
    installNote: 'Memerlukan npm global. Terinstall sebagai `claude` CLI.',
    apiKeyLabel: 'ANTHROPIC_API_KEY',
    apiKeyPlaceholder: 'sk-ant-api03-...',
    showApiKey: true,
    accentColor: '#cc785c',
  },
  {
    id: 'opencode',
    mode: 'daemon',
    name: 'OpenCode',
    icon: '💻',
    description: 'Open-source agentic coding CLI — mendukung berbagai provider AI (OpenAI, Anthropic, Ollama) untuk coding automation.',
    installNote: 'Memerlukan npm global. Terinstall sebagai `opencode` CLI.',
    apiKeyLabel: 'API Key Provider',
    apiKeyPlaceholder: 'sk-... atau sama dengan OpenRouter',
    showApiKey: true,
    providers: ['anthropic', 'openai', 'openrouter', 'ollama'],
    accentColor: '#6366f1',
  },
  {
    id: '9router',
    mode: 'daemon',
    name: '9Router',
    icon: '🧭',
    description: 'Local OpenAI-compatible AI router dengan fallback subscription, cheap, dan free provider.',
    installNote: 'Memerlukan npm global. Default dijalankan private di port 20128.',
    showApiKey: false,
    showPort: true,
    accentColor: '#0ea5e9',
  },
  {
    id: 'omniroute',
    mode: 'daemon',
    name: 'OmniRoute',
    icon: '🛣️',
    description: 'Multi-provider AI gateway OpenAI-compatible untuk routing, budget, dashboard, dan fallback.',
    installNote: 'Memerlukan npm global. Default dijalankan private di port 20129.',
    showApiKey: false,
    showPort: true,
    accentColor: '#14b8a6',
  },
];

function escapeHTML(value: unknown): string {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

export class CodeToolsPage {
  private el: HTMLDivElement;
  private contentEl!: HTMLDivElement;
  private statuses: Record<string, CodeToolStatus> = {};
  private logEls: Record<string, HTMLElement> = {};
  private abortControllers: Record<string, AbortController> = {};
  private refreshTimer: ReturnType<typeof setInterval> | null = null;

  constructor() {
    this.el = document.createElement('div');
    this.build();
  }

  getElement() { return this.el; }

  async mount() {
    await this.loadStatus();
    this.refreshTimer = setInterval(() => this.loadStatus(true), 7000);
  }

  unmount() {
    if (this.refreshTimer) clearInterval(this.refreshTimer);
    // Abort any in-flight SSE streams
    Object.values(this.abortControllers).forEach(ac => ac.abort());
  }

  private build() {
    const header = document.createElement('div');
    header.className = 'page-header';
    header.innerHTML = `
      <div class="page-header-row">
      <div>
        <h1>💻 Code Tools</h1>
        <p class="subtitle">Kelola agentic coding CLI — install, jalankan, dan integrasikan sebagai sub-agent OpenClaw</p>
      </div>
      <button class="btn btn-primary page-primary-action" id="btn-code-tools-refresh">Refresh</button>
      </div>
    `;
    header.querySelector('#btn-code-tools-refresh')?.addEventListener('click', () => this.loadStatus());

    const body = document.createElement('div');
    body.className = 'page-body';
    this.contentEl = document.createElement('div');
    this.contentEl.style.cssText = 'display:flex;flex-direction:column;gap:24px;';
    body.appendChild(this.contentEl);
    this.el.append(header, body);
  }

  private async loadStatus(silent = false) {
    if (!silent) {
      this.contentEl.innerHTML = `
      <div style="text-align:center;padding:40px;color:var(--clr-text-muted)">
        <div style="font-size:2rem;margin-bottom:8px">⏳</div>
        <div>Memeriksa status tools...</div>
      </div>
    `;
    }
    try {
      const res = await api.getCodeToolsStatus();
      this.statuses = {};
      for (const s of res.tools) this.statuses[s.tool] = s;
      this.renderTools();
      this.emitRouterStatusChanged();
    } catch {
      this.contentEl.innerHTML = `<div class="error-box">Gagal memuat status tools</div>`;
    }
  }

  private renderTools() {
    this.contentEl.innerHTML = '';

    // Info banner
    const banner = document.createElement('div');
    banner.style.cssText = `
      background: linear-gradient(135deg, rgba(99,102,241,0.12), rgba(139,92,246,0.08));
      border: 1px solid rgba(99,102,241,0.3);
      border-radius: var(--radius-lg);
      padding: 16px 20px;
      display: flex;
      align-items: flex-start;
      gap: 12px;
      font-size: 0.82rem;
      color: var(--clr-text-muted);
    `;
    banner.innerHTML = `
      <span style="font-size:1.2rem;flex-shrink:0">🤖</span>
      <div>
        <strong style="color:var(--clr-text);display:block;margin-bottom:4px">Integrasi Sub-Agent</strong>
        Claude Code berada pada mode <strong>Install + Validate</strong>. OpenCode, 9Router, dan OmniRoute dapat dijalankan sebagai daemon lokal.
        Router AI wajib diperlakukan sebagai downstream private, bukan public endpoint.
      </div>
    `;
    this.contentEl.appendChild(banner);

    for (const def of TOOLS) {
      const status = this.statuses[def.id] ?? { tool: def.id, installed: false, running: false, ready: false };
      const card = this.buildToolCard(def, status);
      this.contentEl.appendChild(card);
    }
  }

  private buildToolCard(def: ToolDef, status: CodeToolStatus): HTMLElement {
    const card = document.createElement('div');
    card.id = `tool-card-${def.id}`;
    const runningLike = status.running || status.control_state === 'unmanaged_running';
    const unmanaged = status.control_state === 'unmanaged_running';
    const blockedByPort = status.control_state === 'port_conflict';
    const canStopExternal = status.can_stop_unmanaged === true;
    card.style.cssText = `
      background: var(--clr-surface);
      border: 1px solid var(--clr-border);
      border-radius: var(--radius-lg);
      padding: 24px;
      border-left: 4px solid ${status.ready ? '#10b981' : runningLike ? '#10b981' : blockedByPort ? '#f59e0b' : status.installed ? def.accentColor : 'var(--clr-border)'};
      transition: border-color 0.3s ease;
    `;

    card.innerHTML = `
      <!-- Header Row -->
      <div style="display:flex;align-items:flex-start;justify-content:space-between;gap:16px;margin-bottom:16px">
        <div style="display:flex;align-items:center;gap:14px;flex:1">
          <div style="font-size:2.2rem;width:48px;height:48px;display:flex;align-items:center;justify-content:center;
               background:${def.accentColor}22;border-radius:12px;flex-shrink:0">${def.icon}</div>
          <div>
            <div style="font-size:1.05rem;font-weight:700;color:var(--clr-text)">${def.name}</div>
            <div style="font-size:0.78rem;color:var(--clr-text-muted);margin-top:3px">${def.description}</div>
            <div style="font-size:0.72rem;color:var(--clr-text-muted);margin-top:6px">${def.installNote}</div>
          </div>
        </div>

        <div style="display:flex;flex-direction:column;align-items:center;gap:6px;flex-shrink:0">
          <span style="font-size:0.7rem;color:var(--clr-text-muted);font-weight:600">
            ${def.mode === 'validate'
              ? (status.ready ? 'READY' : status.installed ? 'VALIDATE' : 'NOT INSTALLED')
              : this.statusLabel(status)}
          </span>
        </div>
      </div>

      <!-- Status Row -->
      <div id="status-${def.id}" style="display:flex;flex-wrap:wrap;gap:8px;margin-bottom:16px;align-items:center">
        ${this.buildStatusBadges(status)}
      </div>

      <div id="apikey-form-${def.id}" style="margin-bottom:16px;${(!def.showApiKey || (def.mode === 'daemon' && runningLike)) ? 'display:none' : ''}">
        ${def.providers ? `
          <div style="margin-bottom:8px">
            <label style="font-size:0.75rem;color:var(--clr-text-2);display:block;margin-bottom:4px">Provider</label>
            <select id="provider-${def.id}" class="form-input" style="height:36px;font-size:0.82rem">
              ${def.providers.map(p => `<option value="${p}">${p.charAt(0).toUpperCase()+p.slice(1)}</option>`).join('')}
            </select>
          </div>
        ` : ''}
        <div>
          <label style="font-size:0.75rem;color:var(--clr-text-2);display:block;margin-bottom:4px">${def.apiKeyLabel ?? ''}</label>
          <input type="password" id="apikey-${def.id}" class="form-input" style="height:36px;font-size:0.82rem"
            placeholder="${def.apiKeyPlaceholder ?? ''}">
          <span style="font-size:0.68rem;color:var(--clr-text-muted);margin-top:4px;display:block">
            Key disimpan sementara di memory — tidak ditulis ke disk
          </span>
        </div>
      </div>

      <div id="router-port-form-${def.id}" style="margin-bottom:16px;${(!def.showPort || runningLike || blockedByPort) ? 'display:none' : ''}">
        <label style="font-size:0.75rem;color:var(--clr-text-2);display:block;margin-bottom:4px">Listen Port</label>
        <input type="number" id="listen-port-${def.id}" class="form-input" style="height:36px;font-size:0.82rem"
          min="1024" max="65535" step="1" value="${status.listen_port || (def.id === 'omniroute' ? 20129 : 20128)}">
        <span style="font-size:0.68rem;color:var(--clr-text-muted);margin-top:4px;display:block">
          Host dikunci ke 127.0.0.1. Gunakan endpoint <code>http://127.0.0.1:&lt;port&gt;/v1</code> untuk <code>AI_ROUTER_BASE_URL</code>.
        </span>
      </div>

      <!-- Action Buttons -->
      <div style="display:flex;gap:8px;flex-wrap:wrap;align-items:center">
        ${!status.installed ? `
          <button id="btn-install-${def.id}" class="btn btn-primary" style="gap:6px">
            📦 Install
          </button>
        ` : def.mode === 'validate' ? `
          <button id="btn-validate-${def.id}" class="btn btn-primary">
            ✅ Validate
          </button>
          <button id="btn-remove-${def.id}" class="btn btn-ghost" style="color:var(--clr-danger)">
            🗑️ Remove
          </button>
        ` : status.running && status.managed !== false ? `
          <button id="btn-stop-${def.id}" class="btn btn-ghost" style="color:var(--clr-danger)">
            ⏹️ Stop
          </button>
        ` : canStopExternal ? `
          <button id="btn-stop-external-${def.id}" class="btn btn-ghost" style="color:var(--clr-danger)">
            ⏹️ Stop external
          </button>
          <button id="btn-remove-${def.id}" class="btn btn-ghost" style="color:var(--clr-danger)">
            🗑️ Remove
          </button>
        ` : unmanaged ? `
          <button class="btn btn-ghost" disabled>
            External process
          </button>
          <button id="btn-remove-${def.id}" class="btn btn-ghost" style="color:var(--clr-danger)">
            🗑️ Remove
          </button>
        ` : blockedByPort ? `
          <button class="btn btn-ghost" disabled>
            Port blocked
          </button>
          <button id="btn-remove-${def.id}" class="btn btn-ghost" style="color:var(--clr-danger)">
            🗑️ Remove
          </button>
        ` : `
          <button id="btn-start-${def.id}" class="btn btn-primary">
            ▶️ Start Agent
          </button>
          <button id="btn-remove-${def.id}" class="btn btn-ghost" style="color:var(--clr-danger)">
            🗑️ Remove
          </button>
        `}
        ${status.version ? `<span style="font-size:0.72rem;color:var(--clr-text-muted);margin-left:8px">v${escapeHTML(status.version)}</span>` : ''}
      </div>

      <!-- Log Output -->
      <div id="log-${def.id}" style="
        display:none;
        margin-top:16px;
        background:var(--clr-bg);
        border:1px solid var(--clr-border);
        border-radius:var(--radius-md);
        padding:12px;
        font-family:monospace;
        font-size:0.75rem;
        color:#0f0;
        max-height:200px;
        overflow-y:auto;
        white-space:pre-wrap;
        word-break:break-all;
      "></div>
    `;

    // Store log el reference
    this.logEls[def.id] = card.querySelector(`#log-${def.id}`)!;

    // Wire buttons
    card.querySelector(`#btn-install-${def.id}`)?.addEventListener('click', () => {
      this.doInstall(def, card);
    });
    card.querySelector(`#btn-validate-${def.id}`)?.addEventListener('click', () => {
      this.doValidate(def, card);
    });
    card.querySelector(`#btn-start-${def.id}`)?.addEventListener('click', () => {
      this.doStart(def, card);
    });
    card.querySelector(`#btn-stop-${def.id}`)?.addEventListener('click', () => {
      this.doStop(def, card);
    });
    card.querySelector(`#btn-stop-external-${def.id}`)?.addEventListener('click', () => {
      this.doStopExternal(def, status, card);
    });
    card.querySelector(`#btn-remove-${def.id}`)?.addEventListener('click', () => {
      this.doUninstall(def, card);
    });

    return card;
  }

  private buildStatusBadges(status: CodeToolStatus): string {
    const safeMessage = status.validation_message ? escapeHTML(status.validation_message) : '';
    const safePath = status.bin_path ? escapeHTML(status.bin_path) : '';
    const safeEndpoint = status.endpoint ? escapeHTML(status.endpoint) : '';
    const safeConflict = status.conflict_message ? escapeHTML(status.conflict_message) : '';
    const safeOwner = status.port_owner ? escapeHTML(status.port_owner) : '';
    const isRouter = status.tool === '9router' || status.tool === 'omniroute';
    const badges: string[] = [];
    if (status.installed) {
      badges.push(`<span style="font-size:0.72rem;padding:3px 10px;border-radius:20px;background:#10b98122;color:#10b981;font-weight:600">✅ Installed</span>`);
    } else {
      badges.push(`<span style="font-size:0.72rem;padding:3px 10px;border-radius:20px;background:#6b728022;color:var(--clr-text-muted);font-weight:600">📦 Not Installed</span>`);
    }
    if (status.control_state === 'managed') {
      badges.push(`<span style="font-size:0.72rem;padding:3px 10px;border-radius:20px;background:#10b98133;color:#10b981;font-weight:600">🟢 Running</span>`);
      badges.push(`<span style="font-size:0.72rem;padding:3px 10px;border-radius:20px;background:#10b98122;color:#10b981;font-weight:600">Managed</span>`);
    } else if (status.control_state === 'unmanaged_running') {
      badges.push(`<span style="font-size:0.72rem;padding:3px 10px;border-radius:20px;background:#f5930022;color:#f59300;font-weight:600">Unmanaged</span>`);
    } else if (status.control_state === 'port_conflict') {
      badges.push(`<span style="font-size:0.72rem;padding:3px 10px;border-radius:20px;background:#f5930022;color:#f59300;font-weight:600">Port conflict</span>`);
    } else if (status.running) {
      badges.push(`<span style="font-size:0.72rem;padding:3px 10px;border-radius:20px;background:#10b98133;color:#10b981;font-weight:600">🟢 Running</span>`);
    } else if (status.installed) {
      badges.push(`<span style="font-size:0.72rem;padding:3px 10px;border-radius:20px;background:#f5930022;color:#f59300;font-weight:600">⏸️ Stopped</span>`);
    }
    if (status.ready) {
      badges.push(`<span style="font-size:0.72rem;padding:3px 10px;border-radius:20px;background:#10b98122;color:#10b981;font-weight:600">🔑 Ready</span>`);
    }
    if (status.validation_message) {
      badges.push(`<span style="font-size:0.72rem;color:var(--clr-text-muted)">${safeMessage}</span>`);
    }
    if (status.endpoint) {
      badges.push(`<code style="font-size:0.7rem;color:var(--clr-accent-2)">endpoint: ${safeEndpoint}</code>`);
    }
    if (safeConflict) {
      badges.push(`<span style="font-size:0.72rem;color:#f59300">${safeConflict}</span>`);
    }
    if (safeOwner) {
      badges.push(`<code style="font-size:0.7rem;color:var(--clr-text-muted)">owner: ${safeOwner}</code>`);
    }
    if (isRouter) {
      badges.push(`<span style="font-size:0.72rem;padding:3px 10px;border-radius:20px;background:${status.binary_trusted ? '#10b98122' : '#ef444422'};color:${status.binary_trusted ? '#10b981' : 'var(--clr-danger)'};font-weight:600">${status.binary_trusted ? 'Binary trusted' : 'Binary untrusted'}</span>`);
      badges.push(`<span style="font-size:0.72rem;padding:3px 10px;border-radius:20px;background:${status.runtime_isolated ? '#10b98122' : '#f5930022'};color:${status.runtime_isolated ? '#10b981' : '#f59300'};font-weight:600">${status.runtime_isolated ? 'Runtime isolated' : 'Runtime not isolated'}</span>`);
      badges.push(`<span style="font-size:0.72rem;padding:3px 10px;border-radius:20px;background:${status.loopback_enforced ? '#0ea5e922' : '#ef444422'};color:${status.loopback_enforced ? '#0ea5e9' : 'var(--clr-danger)'};font-weight:600">${status.loopback_enforced ? 'Loopback enforced' : 'Loopback missing'}</span>`);
      badges.push(`<span style="font-size:0.72rem;padding:3px 10px;border-radius:20px;background:${status.process_group_enabled ? '#10b98122' : '#f5930022'};color:${status.process_group_enabled ? '#10b981' : '#f59300'};font-weight:600">${status.process_group_enabled ? 'Process group' : 'Parent-only stop'}</span>`);
      if (status.runtime_dir) {
        badges.push(`<code style="font-size:0.7rem;color:var(--clr-text-muted)" title="${escapeHTML(status.runtime_dir)}">runtime: ${escapeHTML(status.runtime_dir)}</code>`);
      }
      for (const warning of status.security_warnings ?? []) {
        badges.push(`<span style="font-size:0.72rem;color:#f59300">${escapeHTML(warning)}</span>`);
      }
      for (const notice of status.security_notices ?? []) {
        badges.push(`<span style="font-size:0.72rem;color:var(--clr-text-muted)">${escapeHTML(notice)}</span>`);
      }
    }
    if (status.bin_path) {
      badges.push(`<code style="font-size:0.7rem;color:var(--clr-text-muted)">${safePath}</code>`);
    }
    return badges.join('');
  }

  private statusLabel(status: CodeToolStatus): string {
    switch (status.control_state) {
      case 'managed':
        return 'RUNNING';
      case 'unmanaged_running':
        return 'UNMANAGED';
      case 'port_conflict':
        return 'PORT BUSY';
      case 'stopped':
        return 'OFF';
      case 'not_installed':
        return 'NOT INSTALLED';
      default:
        return status.running ? 'RUNNING' : status.installed ? 'OFF' : 'NOT INSTALLED';
    }
  }

  private showLog(id: string, show = true) {
    const el = this.logEls[id];
    if (el) el.style.display = show ? 'block' : 'none';
  }

  private appendLog(id: string, line: string) {
    const el = this.logEls[id];
    if (!el) return;
    el.textContent += line + '\n';
    el.scrollTop = el.scrollHeight;
  }

  private clearLog(id: string) {
    const el = this.logEls[id];
    if (el) el.textContent = '';
  }

  private async streamToolAction(def: ToolDef, path: string, ac: AbortController): Promise<'success' | 'error'> {
    const res = await fetch(`${API_BASE}${path}`, {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': authStore.getCSRFToken() },
      body: JSON.stringify({ tool: def.id }),
      signal: ac.signal,
    });
    if (!res.ok) {
      let message = `HTTP ${res.status}`;
      try {
        const data = await res.json();
        message = data.error || message;
      } catch {}
      throw new Error(message);
    }

    const reader = res.body?.getReader();
    if (!reader) throw new Error('No stream');

    const decoder = new TextDecoder();
    let buffer = '';
    let currentEvent = 'message';
    let doneStatus: 'success' | 'error' | '' = '';
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split('\n');
      buffer = lines.pop() ?? '';
      for (const rawLine of lines) {
        const line = rawLine.trimEnd();
        if (line.startsWith('event: ')) {
          currentEvent = line.slice(7).trim();
          continue;
        }
        if (!line.startsWith('data: ')) continue;
        const data = line.slice(6);
        if (currentEvent === 'done') {
          doneStatus = data === 'success' ? 'success' : 'error';
          currentEvent = 'message';
          continue;
        }
        this.appendLog(def.id, data);
      }
    }
    return doneStatus || 'success';
  }

  private async doInstall(def: ToolDef, card: HTMLElement) {
    const btn = card.querySelector<HTMLButtonElement>(`#btn-install-${def.id}`);
    if (btn) { btn.disabled = true; btn.textContent = '📦 Installing...'; }
    this.clearLog(def.id);
    this.showLog(def.id);

    const ac = new AbortController();
    this.abortControllers[def.id] = ac;

    try {
      const status = await this.streamToolAction(def, '/api/code-tools/install', ac);
      if (status !== 'success') throw new Error('Install command gagal');
      showToast(`${def.name} berhasil diinstall!`, 'success');
      await this.loadStatus();
    } catch (err: any) {
      if (err.name !== 'AbortError') {
        showToast(`Install gagal: ${err.message}`, 'error');
        if (btn) { btn.disabled = false; btn.textContent = '📦 Install'; }
      }
    }
  }

  private async doStart(def: ToolDef, card: HTMLElement) {
    const apiKeyEl = card.querySelector<HTMLInputElement>(`#apikey-${def.id}`);
    const providerEl = card.querySelector<HTMLSelectElement>(`#provider-${def.id}`);
    const portEl = card.querySelector<HTMLInputElement>(`#listen-port-${def.id}`);
    const apiKey = apiKeyEl?.value.trim() ?? '';
    const provider = providerEl?.value ?? 'anthropic';
    const listenPort: number | undefined = def.showPort ? Number(portEl?.value || 0) : undefined;
    if (def.showPort && (listenPort === undefined || !Number.isInteger(listenPort) || listenPort < 1024 || listenPort > 65535)) {
      showToast('Listen port harus 1024..65535', 'error');
      return;
    }

    const btn = card.querySelector<HTMLButtonElement>(`#btn-start-${def.id}`);
    if (btn) { btn.disabled = true; btn.textContent = '▶️ Starting...'; }

    try {
      const res = await api.startCodeTool(def.id, apiKey, provider, listenPort);
      showToast(res.endpoint ? `${res.message}. Endpoint: ${res.endpoint}` : res.message, 'success');
      await this.loadStatus();
      if (def.id === '9router' || def.id === 'omniroute') {
        window.dispatchEvent(new CustomEvent('openclaw:router-status-changed'));
        window.location.hash = '#airouter';
      }
    } catch (err: any) {
      showToast(`Gagal start: ${err.message}`, 'error');
      if (btn) { btn.disabled = false; btn.textContent = '▶️ Start Agent'; }
    }
  }

  private async doValidate(def: ToolDef, card: HTMLElement) {
    const apiKeyEl = card.querySelector<HTMLInputElement>(`#apikey-${def.id}`);
    const apiKey = apiKeyEl?.value.trim() ?? '';
    const btn = card.querySelector<HTMLButtonElement>(`#btn-validate-${def.id}`);
    if (btn) { btn.disabled = true; btn.textContent = '✅ Validating...'; }

    try {
      const res = await api.validateCodeTool(def.id, apiKey);
      showToast(res.validation_message || 'Validation selesai', res.ready ? 'success' : 'info');
      await this.loadStatus();
    } catch (err: any) {
      showToast(`Validate gagal: ${err.message}`, 'error');
      if (btn) { btn.disabled = false; btn.textContent = '✅ Validate'; }
    }
  }

  private async doStop(def: ToolDef, card: HTMLElement) {
    const btn = card.querySelector<HTMLButtonElement>(`#btn-stop-${def.id}`);
    if (btn) { btn.disabled = true; btn.textContent = '⏹️ Stopping...'; }

    try {
      const res = await api.stopCodeTool(def.id);
      showToast(res.message, 'success');
      await this.loadStatus();
      this.emitRouterStatusChanged();
    } catch (err: any) {
      showToast(`Gagal stop: ${err.message}`, 'error');
      if (btn) { btn.disabled = false; btn.textContent = '⏹️ Stop'; }
    }
  }

  private async doStopExternal(def: ToolDef, status: CodeToolStatus, card: HTMLElement) {
    const pid = status.unmanaged_pid;
    if (!pid) {
      showToast('PID external process tidak tersedia', 'error');
      return;
    }
    if (!confirm(`Stop external ${def.name} process PID ${pid}?`)) return;
    const btn = card.querySelector<HTMLButtonElement>(`#btn-stop-external-${def.id}`);
    if (btn) { btn.disabled = true; btn.textContent = '⏹️ Stopping external...'; }
    try {
      const res = await api.stopUnmanagedCodeTool(def.id, pid, status.listen_port);
      showToast(res.message, 'success');
      await this.loadStatus();
      this.emitRouterStatusChanged();
    } catch (err: any) {
      showToast(`Gagal stop external: ${err.message}`, 'error');
      if (btn) { btn.disabled = false; btn.textContent = '⏹️ Stop external'; }
    }
  }

  private async doUninstall(def: ToolDef, card: HTMLElement) {
    if (!confirm(`Hapus ${def.name} dari sistem? Tool akan di-uninstall sepenuhnya.`)) return;

    const btn = card.querySelector<HTMLButtonElement>(`#btn-remove-${def.id}`);
    if (btn) { btn.disabled = true; btn.textContent = '🗑️ Removing...'; }
    this.clearLog(def.id);
    this.showLog(def.id);

    const ac = new AbortController();
    this.abortControllers[def.id] = ac;

    try {
      const status = await this.streamToolAction(def, '/api/code-tools/uninstall', ac);
      if (status !== 'success') throw new Error('Uninstall command gagal');
      showToast(`${def.name} dihapus`, 'info');
      await this.loadStatus();
    } catch (err: any) {
      if (err.name !== 'AbortError') {
        showToast(`Remove gagal: ${err.message}`, 'error');
        if (btn) { btn.disabled = false; btn.textContent = '🗑️ Remove'; }
      }
    }
  }

  private emitRouterStatusChanged() {
    const running = ['9router', 'omniroute'].some(id => this.statuses[id]?.running);
    window.dispatchEvent(new CustomEvent('openclaw:router-status-changed', { detail: { running } }));
  }
}
