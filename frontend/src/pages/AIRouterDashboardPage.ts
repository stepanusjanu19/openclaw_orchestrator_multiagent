import { api, type CodeToolStatus, type RouterStatus } from '../api/client.js';
import { showToast } from '../components/Toast.js';
import { escapeHTML } from '../utils/dom.js';

type RouterToolID = '9router' | 'omniroute';

const ROUTER_LABELS: Record<RouterToolID, string> = {
  '9router': '9Router',
  omniroute: 'OmniRoute',
};

const ROUTER_ACCENTS: Record<RouterToolID, string> = {
  '9router': '#0ea5e9',
  omniroute: '#14b8a6',
};

function isRouterTool(tool: string): tool is RouterToolID {
  return tool === '9router' || tool === 'omniroute';
}

function formatCheckedAt(value?: string): string {
  if (!value) return 'waiting';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return 'waiting';
  return date.toLocaleTimeString();
}

function isLoopbackHTTPURL(raw: string): boolean {
  try {
    const url = new URL(raw);
    const host = url.hostname.toLowerCase();
    return url.protocol === 'http:' && (host === '127.0.0.1' || host === 'localhost' || host === '::1' || host === '[::1]');
  } catch {
    return false;
  }
}

export class AIRouterDashboardPage {
  private el: HTMLDivElement;
  private bodyEl!: HTMLDivElement;
  private logEl: HTMLPreElement | null = null;
  private statuses: CodeToolStatus[] = [];
  private routerStatus: RouterStatus | null = null;
  private routerStatusError = '';
  private activeTool: RouterToolID | null = null;
  private refreshTimer: ReturnType<typeof setInterval> | null = null;
  private logSource: EventSource | null = null;
  private logTool: RouterToolID | null = null;
  private lastRunningState: boolean | null = null;

  constructor() {
    this.el = document.createElement('div');
    this.el.className = 'ai-router-page';
    this.buildShell();
  }

  getElement() { return this.el; }

  async mount() {
    await this.load();
    this.refreshTimer = setInterval(() => this.load(true), 5000);
  }

  unmount() {
    if (this.refreshTimer) clearInterval(this.refreshTimer);
    this.closeLogStream();
  }

  private buildShell() {
    const header = document.createElement('div');
    header.className = 'page-header';
    header.innerHTML = `
      <div class="page-header-row">
        <div>
          <span class="page-eyebrow">Dynamic runtime control</span>
          <h1>AI Router Dashboard</h1>
          <p class="subtitle">Kontrol runtime private 9Router dan OmniRoute yang sedang berjalan sebagai downstream OpenAI-compatible router.</p>
        </div>
        <button class="btn btn-primary page-primary-action" id="btn-router-refresh">Refresh</button>
      </div>
    `;
    header.querySelector('#btn-router-refresh')?.addEventListener('click', () => this.load());

    this.bodyEl = document.createElement('div');
    this.bodyEl.className = 'page-body ai-router-body';
    this.el.append(header, this.bodyEl);
  }

  private async load(silent = false) {
    if (!silent) {
      this.bodyEl.innerHTML = `<div class="ai-router-loading">Memuat status router...</div>`;
    }
    try {
      const toolsRes = await api.getCodeToolsStatus();
      this.statuses = toolsRes.tools.filter(s => isRouterTool(s.tool));

      const running = this.runningRouters();
      if (running.length === 0) {
        this.activeTool = null;
        this.closeLogStream();
      } else if (!this.activeTool || !running.some(s => s.tool === this.activeTool)) {
        this.activeTool = running[0].tool as RouterToolID;
      }

      if (this.activeTool) {
        try {
          this.routerStatus = await api.getRouterStatus(this.activeTool);
          this.routerStatusError = '';
        } catch (err: unknown) {
          this.routerStatus = null;
          this.routerStatusError = err instanceof Error ? err.message : String(err);
        }
      } else {
        this.routerStatus = null;
        this.routerStatusError = '';
      }

      this.emitRouterStateIfChanged(running.length > 0);
      this.render();
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      this.bodyEl.innerHTML = `<div class="error-box">Gagal memuat status AI router: ${escapeHTML(message)}</div>`;
    }
  }

  private runningRouters(): CodeToolStatus[] {
    return this.statuses.filter(s => isRouterTool(s.tool) && s.running);
  }

  private render() {
    const running = this.runningRouters();
    if (running.length === 0) {
      this.renderEmptyState();
      return;
    }

    const active = running.find(s => s.tool === this.activeTool) ?? running[0];
    this.activeTool = active.tool as RouterToolID;
    const routerStatus = this.routerStatus;
    const models = routerStatus?.models ?? [];
    const warnings = [...(routerStatus?.warnings ?? []), ...(routerStatus?.security_warnings ?? active.security_warnings ?? [])];
    const notices = [...(routerStatus?.security_notices ?? active.security_notices ?? [])];
    if (routerStatus?.error) warnings.push(routerStatus.error);
    if (this.routerStatusError) warnings.push(this.routerStatusError);
    const agentRoutingEnabled = routerStatus?.agent_routing_enabled ?? false;
    const runtimeActiveLabel = routerStatus?.runtime_active === false ? 'not confirmed' : 'yes';
    const binaryTrusted = routerStatus?.binary_trusted ?? active.binary_trusted ?? false;
    const runtimeIsolated = routerStatus?.runtime_isolated ?? active.runtime_isolated ?? false;
    const loopbackEnforced = routerStatus?.loopback_enforced ?? active.loopback_enforced ?? false;
    const processGroupEnabled = routerStatus?.process_group_enabled ?? active.process_group_enabled ?? false;
    const runtimeDir = routerStatus?.runtime_dir ?? active.runtime_dir ?? '';
    const binaryPath = routerStatus?.binary_resolved_path ?? active.binary_resolved_path ?? active.bin_path ?? '';

    this.bodyEl.innerHTML = `
      <div class="ai-router-tabs">
        ${running.map(status => this.routerTab(status)).join('')}
      </div>

      <div class="ai-router-layout">
        <section class="ai-router-panel">
          <div class="ai-router-head">
            <div class="ai-router-brand">
              <span style="--router-accent:${ROUTER_ACCENTS[this.activeTool]}">${escapeHTML(ROUTER_LABELS[this.activeTool])}</span>
              <strong>Running</strong>
            </div>
            <span class="status-badge running"><span class="dot"></span>Private loopback</span>
          </div>

          <div class="ai-router-stat-grid">
            ${this.statCard('Runtime Router', ROUTER_LABELS[this.activeTool], 'endpoint')}
            ${this.statCard('Listen Port', String(active.listen_port || '-'), 'port')}
            ${this.statCard('Models', String(routerStatus?.model_count ?? models.length), 'models')}
            ${this.statCard('Agent Routing', agentRoutingEnabled ? 'Enabled' : 'Off', 'provider')}
          </div>

          <div class="ai-router-meta">
            <span>Router running <code>${runtimeActiveLabel}</code></span>
            <span>Agent routing <code>${agentRoutingEnabled ? 'enabled' : 'not enabled'}</code></span>
            <span>Route target <code>${escapeHTML(routerStatus?.route_target ?? 'ai_router')}</code></span>
            <span>Runtime support <code>${routerStatus?.runtime_supported ? 'ready' : 'needs setup'}</code></span>
            <span>OpenClaw/Gateway/Webhook <code>unaffected</code></span>
            <span>Endpoint <code>${escapeHTML(active.endpoint || 'waiting')}</code></span>
            <span>Binary trust <code>${binaryTrusted ? 'trusted' : 'untrusted'}</code></span>
            <span>Runtime isolation <code>${runtimeIsolated ? 'isolated' : 'not isolated'}</code></span>
            <span>Loopback <code>${loopbackEnforced ? 'enforced' : 'not enforced'}</code></span>
            <span>Process group <code>${processGroupEnabled ? 'enabled' : 'parent only'}</code></span>
            ${runtimeDir ? `<span>Runtime dir <code>${escapeHTML(runtimeDir)}</code></span>` : ''}
            ${active.version ? `<span>Version <code>${escapeHTML(active.version)}</code></span>` : ''}
            ${binaryPath ? `<span>Binary <code>${escapeHTML(binaryPath)}</code></span>` : ''}
            ${routerStatus?.checked_at ? `<span>Checked <code>${escapeHTML(formatCheckedAt(routerStatus.checked_at))}</code></span>` : ''}
            <span>Tool calls <code>${routerStatus?.tool_calls_allowed ? 'allowed' : 'blocked/default'}</code></span>
          </div>

          ${agentRoutingEnabled ? `
            <div class="ai-router-runtime-note">
              Agent routing enabled by config. Restart agent yang sedang berjalan agar perubahan routing diterapkan; OpenClaw Gateway, relay webhook, Visitor, dan Gateway tetap terisolasi.
            </div>
          ` : `
            <div class="ai-router-runtime-note">
              Runtime active, agent routing not enabled. OpenClaw Gateway, relay webhook, Visitor, dan Gateway tetap memakai jalur existing.
            </div>
          `}

          ${warnings.length ? `
            <div class="ai-router-warnings">
              ${warnings.map(w => `<div>${escapeHTML(w)}</div>`).join('')}
            </div>
          ` : ''}

          ${notices.length ? `
            <div class="ai-router-notices">
              ${notices.map(n => `<div>${escapeHTML(n)}</div>`).join('')}
            </div>
          ` : ''}

          <div class="ai-router-actions">
            <button class="btn btn-primary" id="btn-router-copy">Copy Endpoint</button>
            <button class="btn btn-ghost" id="btn-router-models">Open /models</button>
            <button class="btn btn-ghost" id="btn-router-reload">Refresh</button>
            <button class="btn btn-ghost" id="btn-router-stop" style="color:var(--clr-danger)">Stop Router</button>
          </div>
        </section>

        <section class="ai-router-panel">
          <div class="ai-router-section-title">
            <h3>Runtime Logs</h3>
            <span>${escapeHTML(ROUTER_LABELS[this.activeTool])}</span>
          </div>
          <pre class="ai-router-log" id="ai-router-log"></pre>
        </section>
      </div>

      <section class="ai-router-panel">
        <div class="ai-router-section-title">
          <h3>Model Inventory</h3>
          <span>${models.length} detected</span>
        </div>
        <div class="ai-router-models">
          ${models.length ? models.map(model => `<code>${escapeHTML(model)}</code>`).join('') : '<span class="ai-router-muted">Belum ada model dari runtime /v1/models, atau router belum selesai dikonfigurasi.</span>'}
        </div>
      </section>
    `;

    this.bodyEl.querySelectorAll<HTMLButtonElement>('.ai-router-tab').forEach(btn => {
      btn.addEventListener('click', () => {
        const tool = btn.dataset.tool;
        if (tool && isRouterTool(tool)) {
          this.activeTool = tool;
          this.render();
        }
      });
    });
    this.bodyEl.querySelector('#btn-router-copy')?.addEventListener('click', () => this.copyEndpoint(active));
    this.bodyEl.querySelector('#btn-router-models')?.addEventListener('click', () => this.openModels(active));
    this.bodyEl.querySelector('#btn-router-reload')?.addEventListener('click', () => this.load());
    this.bodyEl.querySelector('#btn-router-stop')?.addEventListener('click', () => this.stopActiveRouter());

    this.logEl = this.bodyEl.querySelector<HTMLPreElement>('#ai-router-log');
    this.startLogStream(this.activeTool);
  }

  private routerTab(status: CodeToolStatus): string {
    const tool = status.tool as RouterToolID;
    return `
      <button class="ai-router-tab ${tool === this.activeTool ? 'active' : ''}" data-tool="${escapeHTML(tool)}">
        <span>${escapeHTML(ROUTER_LABELS[tool])}</span>
        <small>${escapeHTML(status.endpoint || 'running')}</small>
      </button>
    `;
  }

  private statCard(label: string, value: string, kind: string): string {
    return `
      <div class="ai-router-stat ${escapeHTML(kind)}">
        <span>${escapeHTML(label)}</span>
        <strong>${escapeHTML(value)}</strong>
      </div>
    `;
  }

  private renderEmptyState() {
    this.bodyEl.innerHTML = `
      <div class="ai-router-empty">
        <img src="/openclaw-mark.svg" alt="" aria-hidden="true" />
        <h3>AI Router belum berjalan</h3>
        <p>Install dan start 9Router atau OmniRoute dari Code Tools. Menu ini akan muncul otomatis saat salah satu router aktif.</p>
        <button class="btn btn-primary" id="btn-open-code-tools">Buka Code Tools</button>
      </div>
    `;
    this.bodyEl.querySelector('#btn-open-code-tools')?.addEventListener('click', () => {
      window.location.hash = '#codetools';
      window.dispatchEvent(new HashChangeEvent('hashchange'));
    });
  }

  private async copyEndpoint(status: CodeToolStatus) {
    if (!status.endpoint) {
      showToast('Endpoint router belum tersedia', 'error');
      return;
    }
    try {
      await navigator.clipboard.writeText(status.endpoint);
      showToast('Endpoint router disalin', 'success');
    } catch {
      showToast('Browser tidak mengizinkan clipboard', 'error');
    }
  }

  private openModels(status: CodeToolStatus) {
    if (!status.endpoint) {
      showToast('Endpoint router belum tersedia', 'error');
      return;
    }
    if (!isLoopbackHTTPURL(status.endpoint)) {
      showToast('Endpoint router ditolak: hanya loopback http yang boleh dibuka', 'error');
      return;
    }
    window.open(`${status.endpoint.replace(/\/$/, '')}/models`, '_blank', 'noopener,noreferrer');
  }

  private async stopActiveRouter() {
    if (!this.activeTool) return;
    const label = ROUTER_LABELS[this.activeTool];
    if (!confirm(`Stop ${label}? Dashboard router akan kembali ke empty state jika tidak ada router lain yang aktif.`)) return;
    try {
      const res = await api.stopCodeTool(this.activeTool);
      showToast(res.message, 'success');
      this.lastRunningState = null;
      window.dispatchEvent(new CustomEvent('openclaw:router-status-changed', { detail: { lifecycle: 'stop' } }));
      await this.load();
    } catch (err: unknown) {
      showToast(`Gagal stop ${label}: ${err instanceof Error ? err.message : String(err)}`, 'error');
    }
  }

  private startLogStream(tool: RouterToolID) {
    if (this.logTool === tool && this.logSource) return;
    this.closeLogStream();
    this.logTool = tool;
    if (this.logEl) {
      this.logEl.textContent = `[${tool}] Log stream dimulai...\n`;
    }
    const source = api.streamCodeToolLogs(tool);
    this.logSource = source;
    source.onmessage = (event) => this.appendLog(String(event.data || ''));
    source.addEventListener('close', (event) => this.appendLog(String((event as MessageEvent).data || 'Stream berakhir.')));
    source.onerror = () => {
      this.appendLog('Log stream tidak tersedia atau router berhenti.');
      source.close();
    };
  }

  private appendLog(line: string) {
    if (!this.logEl) return;
    this.logEl.textContent += `${line}\n`;
    const maxChars = 24000;
    if (this.logEl.textContent.length > maxChars) {
      this.logEl.textContent = this.logEl.textContent.slice(-maxChars);
    }
    this.logEl.scrollTop = this.logEl.scrollHeight;
  }

  private closeLogStream() {
    if (this.logSource) {
      this.logSource.close();
      this.logSource = null;
    }
    this.logTool = null;
  }

  private emitRouterStateIfChanged(running: boolean) {
    if (this.lastRunningState === running) return;
    this.lastRunningState = running;
    window.dispatchEvent(new CustomEvent('openclaw:router-status-changed', { detail: { running } }));
  }
}
