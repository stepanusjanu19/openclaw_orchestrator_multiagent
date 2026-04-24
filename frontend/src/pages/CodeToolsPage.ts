// pages/CodeToolsPage.ts
// Halaman manajemen Code Tools: Claude Code CLI & OpenCode CLI
// Toggle switch → install/uninstall + start/stop sebagai daemon sub-agent.

import { api, API_BASE, authStore, type CodeToolStatus } from '../api/client.js';
import { showToast } from '../components/Toast.js';

interface ToolDef {
  id: 'claude' | 'opencode';
  mode: 'validate' | 'daemon';
  name: string;
  icon: string;
  description: string;
  installNote: string;
  apiKeyLabel: string;
  apiKeyPlaceholder: string;
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
    providers: ['anthropic', 'openai', 'openrouter', 'ollama'],
    accentColor: '#6366f1',
  },
];

export class CodeToolsPage {
  private el: HTMLDivElement;
  private contentEl!: HTMLDivElement;
  private statuses: Record<string, CodeToolStatus> = {};
  private logEls: Record<string, HTMLElement> = {};
  private abortControllers: Record<string, AbortController> = {};

  constructor() {
    this.el = document.createElement('div');
    this.build();
  }

  getElement() { return this.el; }

  async mount() {
    await this.loadStatus();
  }

  unmount() {
    // Abort any in-flight SSE streams
    Object.values(this.abortControllers).forEach(ac => ac.abort());
  }

  private build() {
    const header = document.createElement('div');
    header.className = 'page-header';
    header.innerHTML = `
      <div>
        <h1>💻 Code Tools</h1>
        <p class="subtitle">Kelola agentic coding CLI — install, jalankan, dan integrasikan sebagai sub-agent OpenClaw</p>
      </div>
    `;

    const body = document.createElement('div');
    body.className = 'page-body';
    this.contentEl = document.createElement('div');
    this.contentEl.style.cssText = 'display:flex;flex-direction:column;gap:24px;';
    body.appendChild(this.contentEl);
    this.el.append(header, body);
  }

  private async loadStatus() {
    this.contentEl.innerHTML = `
      <div style="text-align:center;padding:40px;color:var(--clr-text-muted)">
        <div style="font-size:2rem;margin-bottom:8px">⏳</div>
        <div>Memeriksa status tools...</div>
      </div>
    `;
    try {
      const res = await api.getCodeToolsStatus();
      this.statuses = {};
      for (const s of res.tools) this.statuses[s.tool] = s;
      this.renderTools();
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
        Claude Code berada pada mode <strong>Install + Validate</strong> agar readiness API key jelas dan tidak ada status daemon palsu.
        OpenCode tetap bisa dijalankan sebagai proses jika dibutuhkan.
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
    card.style.cssText = `
      background: var(--clr-surface);
      border: 1px solid var(--clr-border);
      border-radius: var(--radius-lg);
      padding: 24px;
      border-left: 4px solid ${status.ready ? '#10b981' : status.running ? '#10b981' : status.installed ? def.accentColor : 'var(--clr-border)'};
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
              : (status.running ? 'RUNNING' : status.installed ? 'OFF' : 'NOT INSTALLED')}
          </span>
        </div>
      </div>

      <!-- Status Row -->
      <div id="status-${def.id}" style="display:flex;flex-wrap:wrap;gap:8px;margin-bottom:16px;align-items:center">
        ${this.buildStatusBadges(status)}
      </div>

      <div id="apikey-form-${def.id}" style="margin-bottom:16px;${def.mode === 'daemon' && status.running ? 'display:none' : ''}">
        ${def.providers ? `
          <div style="margin-bottom:8px">
            <label style="font-size:0.75rem;color:var(--clr-text-2);display:block;margin-bottom:4px">Provider</label>
            <select id="provider-${def.id}" class="form-input" style="height:36px;font-size:0.82rem">
              ${def.providers.map(p => `<option value="${p}">${p.charAt(0).toUpperCase()+p.slice(1)}</option>`).join('')}
            </select>
          </div>
        ` : ''}
        <div>
          <label style="font-size:0.75rem;color:var(--clr-text-2);display:block;margin-bottom:4px">${def.apiKeyLabel}</label>
          <input type="password" id="apikey-${def.id}" class="form-input" style="height:36px;font-size:0.82rem"
            placeholder="${def.apiKeyPlaceholder}">
          <span style="font-size:0.68rem;color:var(--clr-text-muted);margin-top:4px;display:block">
            Key disimpan sementara di memory — tidak ditulis ke disk
          </span>
        </div>
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
        ` : status.running ? `
          <button id="btn-stop-${def.id}" class="btn btn-ghost" style="color:var(--clr-danger)">
            ⏹️ Stop
          </button>
        ` : `
          <button id="btn-start-${def.id}" class="btn btn-primary">
            ▶️ Start Agent
          </button>
          <button id="btn-remove-${def.id}" class="btn btn-ghost" style="color:var(--clr-danger)">
            🗑️ Remove
          </button>
        `}
        ${status.version ? `<span style="font-size:0.72rem;color:var(--clr-text-muted);margin-left:8px">v${status.version}</span>` : ''}
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
    card.querySelector(`#btn-remove-${def.id}`)?.addEventListener('click', () => {
      this.doUninstall(def, card);
    });

    return card;
  }

  private buildStatusBadges(status: CodeToolStatus): string {
    const safeMessage = status.validation_message ? status.validation_message
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;') : '';
    const safePath = status.bin_path ? status.bin_path
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;') : '';
    const badges: string[] = [];
    if (status.installed) {
      badges.push(`<span style="font-size:0.72rem;padding:3px 10px;border-radius:20px;background:#10b98122;color:#10b981;font-weight:600">✅ Installed</span>`);
    } else {
      badges.push(`<span style="font-size:0.72rem;padding:3px 10px;border-radius:20px;background:#6b728022;color:var(--clr-text-muted);font-weight:600">📦 Not Installed</span>`);
    }
    if (status.running) {
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
    if (status.bin_path) {
      badges.push(`<code style="font-size:0.7rem;color:var(--clr-text-muted)">${safePath}</code>`);
    }
    return badges.join('');
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

  private async doInstall(def: ToolDef, card: HTMLElement) {
    const btn = card.querySelector<HTMLButtonElement>(`#btn-install-${def.id}`);
    if (btn) { btn.disabled = true; btn.textContent = '📦 Installing...'; }
    this.clearLog(def.id);
    this.showLog(def.id);

    const ac = new AbortController();
    this.abortControllers[def.id] = ac;

    try {
      const res = await fetch(`${API_BASE}/api/code-tools/install`, {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': authStore.getCSRFToken() },
        body: JSON.stringify({ tool: def.id }),
        signal: ac.signal,
      });

      const reader = res.body?.getReader();
      if (!reader) throw new Error('No stream');

      const decoder = new TextDecoder();
      let buffer = '';
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split('\n');
        buffer = lines.pop() ?? '';
        for (const line of lines) {
          if (line.startsWith('data: ')) this.appendLog(def.id, line.slice(6));
          if (line === 'event: done') break;
        }
      }
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
    const apiKey = apiKeyEl?.value.trim() ?? '';
    const provider = providerEl?.value ?? 'anthropic';

    const btn = card.querySelector<HTMLButtonElement>(`#btn-start-${def.id}`);
    if (btn) { btn.disabled = true; btn.textContent = '▶️ Starting...'; }

    try {
      const res = await api.startCodeTool(def.id, apiKey, provider);
      showToast(res.message, 'success');
      await this.loadStatus();
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
    } catch (err: any) {
      showToast(`Gagal stop: ${err.message}`, 'error');
      if (btn) { btn.disabled = false; btn.textContent = '⏹️ Stop'; }
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
      const res = await fetch(`${API_BASE}/api/code-tools/uninstall`, {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': authStore.getCSRFToken() },
        body: JSON.stringify({ tool: def.id }),
        signal: ac.signal,
      });

      const reader = res.body?.getReader();
      if (!reader) throw new Error('No stream');
      const decoder = new TextDecoder();
      let buffer = '';
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split('\n');
        buffer = lines.pop() ?? '';
        for (const line of lines) {
          if (line.startsWith('data: ')) this.appendLog(def.id, line.slice(6));
        }
      }
      showToast(`${def.name} dihapus`, 'info');
      await this.loadStatus();
    } catch (err: any) {
      if (err.name !== 'AbortError') {
        showToast(`Remove gagal: ${err.message}`, 'error');
        if (btn) { btn.disabled = false; btn.textContent = '🗑️ Remove'; }
      }
    }
  }
}
