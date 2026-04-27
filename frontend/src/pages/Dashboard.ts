// pages/Dashboard.ts v2
// List view yang scalable — semua agent dalam tabel yang bisa expand.
// Dynamic: tambah/hapus agent langsung dari UI tanpa reload.

import { api, API_BASE, type AgentInfo, type OCStatus, type DetectedAgent, type DaemonStatus, type OrchestratorStatus, type ProviderStatus, type SystemMetrics } from '../api/client.js';
import { AddAgentModal } from '../components/AddAgentModal.js';
import { LogViewer } from '../components/LogViewer.js';
import { ModelPicker } from '../components/ModelPicker.js';
import { WorkspaceEditor } from '../components/WorkspaceEditor.js';
import { showToast } from '../components/Toast.js';
import { escapeHTML } from '../utils/dom.js';
import { flattenModelGroups, modelRouteMetaFromEntry, routerModelsToEntries } from '../utils/modelRuntime.js';

const AVATARS: Record<string, string> = { main: 'M', dev: 'D', alpha: 'A', assistant: 'AI', bot: 'B' };
const avatar = (id: string) => AVATARS[id] ?? id.charAt(0).toUpperCase();

export class Dashboard {
  private el: HTMLDivElement;
  private agents: AgentInfo[] = [];
  private ocStatus: OCStatus | null = null;
  private refreshTimer: ReturnType<typeof setInterval> | null = null;
  private metricsTimer: ReturnType<typeof setInterval> | null = null;
  private expandedRows = new Set<string>();
  private logViewers = new Map<string, LogViewer>();
  private busyAgents = new Set<string>();
  private metrics: SystemMetrics | null = null;
  private metricsHistory: SystemMetrics[] = [];
  private metricsState: 'loading' | 'connected' | 'route-missing' | 'auth-expired' | 'unavailable' = 'loading';
  private metricsMessage = 'Menghubungkan telemetry backend...';

  // DOM refs
  private agentListEl!: HTMLDivElement;
  private summaryEl!: HTMLDivElement;
  private metricsEl!: HTMLDivElement;
  private ocPanelEl!: HTMLDivElement;
  private providersPanelEl!: HTMLDivElement;
  private providers: ProviderStatus[] = [];
  private providerModels = new Map<string, string[]>();

  constructor() {
    this.el = document.createElement('div');
    this.buildShell();
  }

  getElement() { return this.el; }

  async mount() {
    await Promise.all([this.loadAgents(), this.loadOCStatus(), this.loadProviders(), this.loadSystemMetrics()]);
    this.refreshTimer = setInterval(() => this.silentRefresh(), 8000);
    this.metricsTimer = setInterval(() => this.loadSystemMetrics(), 5000);
  }

  unmount() {
    if (this.refreshTimer) clearInterval(this.refreshTimer);
    if (this.metricsTimer) clearInterval(this.metricsTimer);
    this.logViewers.forEach(lv => lv.disconnect());
  }

  // ── Shell (header + layout) ───────────────────────────────────────────────

  private buildShell() {
    const header = document.createElement('div');
    header.className = 'page-header';
    header.innerHTML = `
      <div class="page-header-row">
        <div>
          <span class="page-eyebrow">Control overview</span>
          <h1>Agent Dashboard</h1>
          <p class="subtitle">Kelola semua agent OpenClaw Telegram — tambah, start, edit persona secara real-time</p>
        </div>
        <button class="btn btn-primary page-primary-action" id="btn-add-agent">Tambah Agent</button>
      </div>
    `;
    header.querySelector('#btn-add-agent')!.addEventListener('click', () => this.openAddModal());

    const body = document.createElement('div');
    body.className = 'page-body';

    this.metricsEl = document.createElement('div');
    this.metricsEl.className = 'device-monitor-panel';
    this.renderDeviceMonitor();

    // Summary strip
    this.summaryEl = document.createElement('div');
    this.summaryEl.className = 'summary-grid';

    // OpenClaw panel
    this.ocPanelEl = document.createElement('div');
    this.ocPanelEl.className = 'oc-panel';
    this.renderOCPanel();

    // Providers Panel
    this.providersPanelEl = document.createElement('div');
    this.providersPanelEl.className = 'providers-panel';

    // Agent list
    const listSection = document.createElement('div');
    listSection.className = 'dashboard-section';

    const listHeader = document.createElement('div');
    listHeader.className = 'list-header';
    listHeader.innerHTML = `
      <h4>Agents</h4>
      <button class="btn btn-ghost btn-sm" id="btn-refresh-agents">↻ Refresh</button>
    `;
    listHeader.querySelector('#btn-refresh-agents')!.addEventListener('click', () => this.loadAgents());

    this.agentListEl = document.createElement('div');
    this.agentListEl.className = 'agent-list';
    this.agentListEl.innerHTML = this.skeletonHTML();

    listSection.append(listHeader, this.agentListEl);
    body.append(this.metricsEl, this.summaryEl, this.ocPanelEl, this.providersPanelEl, listSection);
    this.el.append(header, body);
  }

  // ── Data loading ─────────────────────────────────────────────────────────

  private async loadAgents() {
    try {
      this.agents = await api.listAgents();
      this.renderSummary();
      this.renderAgentList();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      if (msg === 'UNAUTHORIZED') return;
      this.agentListEl.innerHTML = this.errorHTML(msg);
    }
  }

  private async loadOCStatus() {
    try {
      this.ocStatus = await api.getOCStatus();
      this.renderOCPanel();
    } catch {
      this.renderOCPanel();
    }
  }

  private async loadProviders() {
    try {
      this.providers = await api.getProviders();
      const modelEntries = await Promise.all(this.providers.map(async (provider) => {
        try {
          const res = await api.getProviderModels(provider.name);
          return [provider.name, res.models.map(m => m.label)] as const;
        } catch {
          return [provider.name, [] as string[]] as const;
        }
      }));
      this.providerModels = new Map(modelEntries);
      this.renderProvidersPanel();
    } catch {
      this.providersPanelEl.innerHTML = '';
    }
  }

  private async silentRefresh() {
    try {
      const [agents] = await Promise.all([api.listAgents()]);
      this.agents = agents;
      this.renderSummary();
      // Hanya update status di rows, jangan re-render penuh (preserves expanded state)
      this.agents.forEach(info => this.updateRowStatus(info));
    } catch {}
  }

  private async loadSystemMetrics() {
    if (!this.metrics) {
      this.metricsState = 'loading';
      this.renderDeviceMonitor();
    }
    try {
      this.metrics = await api.getSystemMetrics();
      this.metricsState = 'connected';
      this.metricsMessage = 'Realtime host telemetry untuk backend, memory, disk, dan runtime Go.';
      this.metricsHistory.push(this.metrics);
      if (this.metricsHistory.length > 24) this.metricsHistory.shift();
      this.renderDeviceMonitor();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      if (msg === 'UNAUTHORIZED') {
        this.metricsState = 'auth-expired';
        this.metricsMessage = 'Session dashboard sudah expired. Login ulang untuk melihat telemetry.';
      } else if (msg.includes('HTTP 404')) {
        this.metricsState = 'route-missing';
        this.metricsMessage = 'Route /api/system/metrics belum aktif. Restart backend agar route baru terdaftar.';
      } else {
        this.metricsState = 'unavailable';
        this.metricsMessage = 'Realtime host telemetry tidak tersedia.';
      }
      this.renderDeviceMonitor();
    }
  }

  private renderDeviceMonitor() {
    if (!this.metricsEl) return;
    if (this.metricsState !== 'connected' || !this.metrics) {
      const statusLabel = this.metricsState === 'loading'
        ? 'Loading'
        : this.metricsState === 'route-missing'
          ? 'Backend route missing'
          : this.metricsState === 'auth-expired'
            ? 'Auth expired'
            : 'Unavailable';
      this.metricsEl.innerHTML = `
        <div class="device-monitor-header">
          <div>
            <h3>Device Monitor</h3>
            <p>${escapeHTML(this.metricsMessage)}</p>
          </div>
          <span class="device-monitor-status ${this.metricsState}">${escapeHTML(statusLabel)}</span>
        </div>
      `;
      return;
    }
    const m = this.metrics;
    const cpu = m?.cpu_percent ?? 0;
    const memory = m?.memory_percent ?? 0;
    const disk = m?.disk_percent ?? 0;
    const spark = this.renderSparkline(this.metricsHistory.map(item => item.cpu_percent));
    const ts = m?.timestamp ? new Date(m.timestamp).toLocaleTimeString() : 'waiting';
    this.metricsEl.innerHTML = `
      <div class="device-monitor-header">
        <div>
          <h3>Device Monitor</h3>
          <p>${escapeHTML(this.metricsMessage)}</p>
        </div>
        <span class="device-monitor-time">Connected · ${escapeHTML(ts)}</span>
      </div>
      <div class="device-monitor-grid">
        ${this.metricCard('CPU Load', cpu, `${m?.load_1?.toFixed(2) ?? '0.00'} / ${m?.num_cpu ?? 0} cores`, 'cpu')}
        ${this.metricCard('Memory', memory, `${m?.memory_used_mb ?? 0} / ${m?.memory_total_mb ?? 0} MB`, 'memory')}
        ${this.metricCard('Disk', disk, `${m?.disk_used_gb ?? 0} / ${m?.disk_total_gb ?? 0} GB`, 'disk')}
        <div class="device-card runtime">
          <div>
            <span class="device-label">Go Runtime</span>
            <strong>${m?.go_heap_alloc_mb ?? 0} MB</strong>
            <small>${m?.go_goroutines ?? 0} goroutines</small>
          </div>
          <div class="device-sparkline">${spark}</div>
        </div>
      </div>
    `;
  }

  private metricCard(label: string, value: number, sub: string, kind: string) {
    const pct = Math.max(0, Math.min(100, value || 0));
    return `
      <div class="device-card ${kind}">
        <div style="display:flex;justify-content:space-between;align-items:flex-start;gap:12px;">
          <div>
            <span class="device-label">${label}</span>
            <strong>${pct.toFixed(1)}%</strong>
            <small>${escapeHTML(sub)}</small>
          </div>
          <div class="device-ring" style="--pct:${pct};">${Math.round(pct)}</div>
        </div>
        <div class="device-bar"><span style="width:${pct}%"></span></div>
      </div>
    `;
  }

  private renderSparkline(values: number[]) {
    if (values.length === 0) return '';
    const width = 140;
    const height = 42;
    const points = values.map((v, i) => {
      const x = values.length === 1 ? width : (i / (values.length - 1)) * width;
      const y = height - (Math.max(0, Math.min(100, v)) / 100) * height;
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    }).join(' ');
    return `<svg viewBox="0 0 ${width} ${height}" preserveAspectRatio="none" aria-hidden="true"><polyline points="${points}" /></svg>`;
  }

  // ── Summary ───────────────────────────────────────────────────────────────

  private renderSummary() {
    const total   = this.agents.length;
    const running = this.agents.filter(a => a.status.running).length;
    const stopped = total - running;
    const noToken = this.agents.filter(a => !a.has_token).length;

    this.summaryEl.innerHTML = `
      <div class="summary-card"><span class="sc-label">Total Agents</span><span class="sc-value purple">${total}</span><span class="sc-sub">registered</span></div>
      <div class="summary-card"><span class="sc-label">Running</span><span class="sc-value green">${running}</span><span class="sc-sub">active bots</span></div>
      <div class="summary-card"><span class="sc-label">Stopped</span><span class="sc-value teal">${stopped}</span><span class="sc-sub">idle</span></div>
      <div class="summary-card"><span class="sc-label">Need Token</span><span class="sc-value pink">${noToken}</span><span class="sc-sub">unconfigured</span></div>
    `;
  }

  // ── OpenClaw Panel ────────────────────────────────────────────────────────

  private renderOCPanel() {
    const oc = this.ocStatus;
    const reachable = oc?.reachable ?? false;
    const importable = oc?.detected_agents?.filter(d =>
      !this.agents.find(a => a.id === d.id)
    ) ?? [];
    const gatewayURL = escapeHTML(oc?.gateway_url ?? 'http://127.0.0.1:14000');
    const modelChips = reachable && oc?.models?.length
      ? oc.models.map(m => `<span class="oc-model-chip">${escapeHTML(m)}</span>`).join('')
      : '';
    const importableHTML = importable.length > 0 ? `
          <div class="oc-importable">
            <span class="oc-import-label">⬇️ ${importable.length} agent terdeteksi dari openclaw.json — belum ada di platform:</span>
            <div class="oc-import-chips">
              ${importable.map(d => `
                <button class="oc-import-btn" data-agent-id="${escapeHTML(d.id)}">
                  ${d.has_bot_token ? '✅' : '⚠️'} ${escapeHTML(d.id)}
                  <span style="font-size:0.65rem;color:var(--clr-text-muted)">${d.has_bot_token ? 'token ready' : 'need token'}</span>
                </button>
              `).join('')}
            </div>
          </div>
        ` : (oc && reachable ? `<div style="font-size:0.78rem;color:var(--clr-text-muted);margin-top:8px">✅ Semua agent openclaw sudah terdaftar di platform</div>` : '');
    const ocError = oc?.error ? `<div style="font-size:0.75rem;color:var(--clr-warning);margin-top:6px">⚠️ ${escapeHTML(oc.error)}</div>` : '';

    this.ocPanelEl.innerHTML = `
      <div class="oc-panel-inner">
        <div class="oc-status-row">
          <span class="oc-icon">${reachable ? '🟢' : '🔴'}</span>
          <strong>OpenClaw Gateway</strong>
          <span class="status-badge ${reachable ? 'running' : 'stopped'}" style="margin-left:8px">
            <span class="dot"></span>${reachable ? 'Connected' : 'Offline'}
          </span>
          <span class="oc-url">${gatewayURL}</span>
          <div style="flex:1"></div>
          <button class="btn btn-ghost btn-sm" id="btn-refresh-oc">↻ Probe</button>
        </div>

        <!-- Daemon Control Strip -->
        <div id="daemon-strip" style="display:flex;align-items:center;gap:8px;padding:8px 0;border-top:1px solid var(--clr-border);margin-top:8px;flex-wrap:wrap;">
          <span style="font-size:0.75rem;color:var(--clr-text-muted);font-weight:600;letter-spacing:0.5px;">DAEMON CTRL</span>
          <span id="daemon-status-badge" style="font-size:0.72rem;padding:2px 8px;border-radius:100px;background:rgba(255,255,255,0.06);color:var(--clr-text-muted);">⏳ loading...</span>
          <div style="flex:1"></div>
          <button class="btn btn-ghost btn-sm" id="btn-daemon-reload" title="Reload openclaw.json tanpa downtime (SIGHUP)">↺ Reload</button>
          <button class="btn btn-ghost btn-sm" id="btn-daemon-restart" title="Restart daemon">⟳ Restart</button>
          <button class="btn btn-ghost btn-sm" id="btn-daemon-stop" title="Stop daemon" style="color:var(--clr-danger);">⏹ Stop</button>
        </div>

        ${modelChips ? `<div class="oc-models">${modelChips}</div>` : ''}
        ${importableHTML}
        ${ocError}
      </div>
    `;

    this.ocPanelEl.querySelector('#btn-refresh-oc')?.addEventListener('click', () => this.loadOCStatus());
    this.ocPanelEl.querySelectorAll<HTMLButtonElement>('.oc-import-btn').forEach(btn => {
      btn.addEventListener('click', () => this.openImportModal(btn.dataset.agentId!));
    });

    // Daemon control buttons
    this.ocPanelEl.querySelector('#btn-daemon-restart')?.addEventListener('click', () => this.handleDaemonCtrl('restart'));
    this.ocPanelEl.querySelector('#btn-daemon-stop')?.addEventListener('click', () => this.handleDaemonCtrl('stop'));
    this.ocPanelEl.querySelector('#btn-daemon-reload')?.addEventListener('click', () => this.handleDaemonCtrl('reload'));

    // Load daemon status async
    this.refreshDaemonBadge();
  }

  private async refreshDaemonBadge() {
    const badge = this.ocPanelEl.querySelector<HTMLElement>('#daemon-status-badge');
    if (!badge) return;
    try {
      const s: DaemonStatus = await api.getDaemonStatus();
      if (s.running) {
        badge.style.background = 'rgba(34,197,94,0.15)';
        badge.style.color = 'var(--clr-success)';
        badge.textContent = `🟢 PID ${s.pid ?? '?'} · via ${s.method}`;
      } else {
        badge.style.background = 'rgba(239,68,68,0.12)';
        badge.style.color = 'var(--clr-danger)';
        badge.textContent = '🔴 Daemon stopped';
      }
    } catch {
      badge.textContent = '⚠️ Tidak dapat probe daemon';
    }
  }

  private async handleDaemonCtrl(action: 'restart' | 'stop' | 'reload') {
    const btnMap: Record<string, string> = { restart: '#btn-daemon-restart', stop: '#btn-daemon-stop', reload: '#btn-daemon-reload' };
    const btn = this.ocPanelEl.querySelector<HTMLButtonElement>(btnMap[action]);
    const origText = btn?.textContent ?? '';
    if (btn) { btn.disabled = true; btn.textContent = '⏳...'; }
    try {
      if (action === 'restart') {
        const r = await api.restartDaemon();
        showToast(`✅ ${r.message}`, 'success');
      } else if (action === 'stop') {
        if (!confirm('Stop OpenClaw daemon? Semua agent akan berhenti menerima pesan Telegram.')) { if (btn) { btn.disabled = false; btn.textContent = origText; } return; }
        const r = await api.stopDaemon();
        showToast(`🛑 ${r.message}`, 'info');
      } else {
        const r = await api.reloadDaemonConfig();
        showToast(`↺ ${r.message}`, 'success');
      }
      await this.refreshDaemonBadge();
    } catch (err: unknown) {
      showToast(`Gagal ${action}: ${err instanceof Error ? err.message : err}`, 'error');
    } finally {
      if (btn) { btn.disabled = false; btn.textContent = origText; }
    }
  }

  // ── Providers Panel ───────────────────────────────────────────────────────

  private renderProvidersPanel() {
    if (!this.providersPanelEl) return;
    if (this.providers.length === 0) {
      this.providersPanelEl.innerHTML = '';
      return;
    }

    const html = `
      <div style="background:var(--clr-surface);border:1px solid var(--clr-border);border-radius:var(--radius-lg);padding:24px;box-shadow:var(--shadow-sm);">
        <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:16px;">
          <h3 style="margin:0;display:flex;align-items:center;gap:8px;">
            ⚡ API Providers
          </h3>
        </div>
        <div style="display:grid;grid-template-columns:repeat(auto-fill, minmax(300px, 1fr));gap:16px;">
          ${this.providers.map(p => {
            const providerId = this.providerDomId(p.name);
            const providerName = escapeHTML(p.name);
            const statusBadge = p.configured
              ? `<span style="font-size:0.7rem;padding:2px 6px;border-radius:4px;background:rgba(16,185,129,0.15);color:#10b981;border:1px solid rgba(16,185,129,0.3);">✅ Ready</span>`
              : `<span style="font-size:0.7rem;padding:2px 6px;border-radius:4px;background:rgba(239,68,68,0.15);color:#ef4444;border:1px solid rgba(239,68,68,0.3);">${p.requires_key ? '❌ Missing Key' : '🖥 Local'}</span>`;
            const models = this.providerModels.get(p.name) ?? [];
            const preview = models.slice(0, 6).map(name => `<span class="oc-model-chip" style="font-size:0.68rem">${escapeHTML(name)}</span>`).join('');
            return `
            <div style="border:1px solid var(--clr-border-light);border-radius:var(--radius-md);padding:16px;background:var(--clr-background);">
              <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:12px;">
                <strong style="text-transform:capitalize;">${providerName}</strong>
                ${statusBadge}
              </div>
              <p style="font-size:0.75rem;color:var(--clr-text-muted);margin:0 0 6px;">Source: <code>${escapeHTML(p.source)}</code></p>
              <p style="font-size:0.75rem;color:var(--clr-text-muted);margin:0 0 6px;">Key: <code>${escapeHTML(p.key_hint || (p.requires_key ? 'Belum diatur' : 'Tidak diperlukan'))}</code></p>
              <p style="font-size:0.75rem;color:var(--clr-text-muted);margin:0 0 12px;">Models detected: <strong>${p.model_count}</strong></p>
              ${p.requires_key ? `
                <div style="display:flex;gap:8px;margin-bottom:12px;">
                  <input type="password" id="${providerId}" class="form-input" style="flex:1;height:32px;font-size:0.8rem;" placeholder="Paste API Key...">
                  <button class="btn btn-primary btn-save-prov" data-prov="${providerName}" data-input-id="${providerId}" style="height:32px;padding:0 12px;font-size:0.8rem;">Save</button>
                </div>
              ` : ''}
              <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px;">
                <span style="font-size:0.72rem;color:var(--clr-text-muted);font-weight:600;">MODEL LIST</span>
                <button class="btn btn-ghost btn-sm btn-refresh-prov-models" data-prov="${providerName}" style="padding:2px 8px;font-size:0.72rem;">↻ Refresh</button>
              </div>
              <div style="display:flex;gap:6px;flex-wrap:wrap;min-height:28px;">
                ${preview || `<span style="font-size:0.72rem;color:var(--clr-text-muted)">Belum ada model terdeteksi</span>`}
              </div>
            </div>
            `;
          }).join('')}
        </div>
      </div>
    `;

    this.providersPanelEl.innerHTML = html;

    this.providersPanelEl.querySelectorAll('.btn-save-prov').forEach(btn => {
      btn.addEventListener('click', async (e) => {
        const provName = (e.currentTarget as HTMLElement).dataset.prov!;
        const inputId = (e.currentTarget as HTMLElement).dataset.inputId!;
        const input = this.providersPanelEl.querySelector<HTMLInputElement>(`#${inputId}`);
        if (!input || !input.value.trim()) {
          showToast('Masukkan API key terlebih dahulu', 'error');
          return;
        }

        const btnEl = e.currentTarget as HTMLButtonElement;
        const origText = btnEl.textContent;
        btnEl.disabled = true;
        btnEl.textContent = 'Saving...';

        try {
          const res = await api.saveProviderKey(provName, input.value.trim());
          // Secara otomatis reload daemon tanpa mengganggu runtime!
          await api.reloadDaemonConfig().catch(() => {});
          
          showToast(`✅ ${res.message} (config reloaded). Restart agent yang sedang running agar env runtime ikut segar.`, 'success');
          input.value = '';
          await this.loadProviders();
        } catch (err: unknown) {
          showToast(`Gagal menyimpan key: ${err instanceof Error ? err.message : err}`, 'error');
        } finally {
          btnEl.disabled = false;
          btnEl.textContent = origText || 'Save';
        }
      });
    });

    this.providersPanelEl.querySelectorAll('.btn-refresh-prov-models').forEach(btn => {
      btn.addEventListener('click', async (e) => {
        const provName = (e.currentTarget as HTMLElement).dataset.prov!;
        try {
          const res = await api.getProviderModels(provName);
          this.providerModels.set(provName, res.models.map(m => m.label));
          this.renderProvidersPanel();
          showToast(`Model provider "${provName}" diperbarui`, 'success');
        } catch (err: unknown) {
          showToast(`Gagal refresh model ${provName}: ${err instanceof Error ? err.message : err}`, 'error');
        }
      });
    });
  }

  private providerDomId(name: string) {
    return `input-prov-${name.toLowerCase().replace(/[^a-z0-9_-]/g, '-')}`;
  }


  // ── Agent List ────────────────────────────────────────────────────────────

  private renderAgentList() {
    if (this.agents.length === 0) {
      this.agentListEl.innerHTML = this.emptyHTML();
      return;
    }

    this.agentListEl.innerHTML = `
      <div class="agent-table-header">
        <span class="col-agent">Agent</span>
        <span class="col-model">Model</span>
        <span class="col-status">Status</span>
        <span class="col-pid">PID</span>
        <span class="col-actions">Aksi</span>
      </div>
    `;

    this.agents.forEach(info => {
      // 🎼 main agent → tampil sebagai Orchestrator Panel khusus
      if (info.id === 'main') {
        const orchWrap = document.createElement('div');
        orchWrap.id = 'orchestrator-panel';
        orchWrap.style.cssText = 'margin-bottom:12px;';
        this.agentListEl.appendChild(orchWrap);
        this.renderOrchestratorPanel(orchWrap, info);
        return;
      }

      const rowWrap = this.buildAgentRow(info);
      this.agentListEl.appendChild(rowWrap);
      
      // Preserve expanded state and re-mount LogViewer if it detached
      if (this.expandedRows.has(info.id)) {
        const detail = rowWrap.querySelector<HTMLDivElement>(`#detail-${info.id}`)!;
        detail.style.display = 'block';
        const lv = this.logViewers.get(info.id);
        if (lv) detail.querySelector(`#logmount-${info.id}`)!.appendChild(lv.getElement());
      }
    });
  }

  private renderOrchestratorPanel(container: HTMLDivElement, mainInfo: AgentInfo) {
    const running = mainInfo.status?.running ?? false;
    container.innerHTML = `
      <div style="
        background: linear-gradient(135deg, rgba(108,99,255,0.12) 0%, rgba(16,185,129,0.08) 100%);
        border: 1px solid rgba(108,99,255,0.35);
        border-radius: var(--radius-lg);
        padding: 16px 20px;
        margin-bottom: 4px;
      ">
        <div style="display:flex;align-items:center;gap:12px;margin-bottom:12px;">
          <div style="
            width:42px;height:42px;border-radius:10px;
            background:linear-gradient(135deg,#6c63ff,#22d3a5);
            display:flex;align-items:center;justify-content:center;
            font-size:1.3rem;flex-shrink:0;
          ">🎼</div>
          <div style="flex:1;min-width:0;">
            <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;">
              <strong style="font-size:0.95rem;">${escapeHTML(mainInfo.label || 'Main Orchestrator')}</strong>
              <span style="font-size:0.68rem;padding:2px 8px;border-radius:100px;background:rgba(108,99,255,0.25);color:#a78bfa;font-weight:700;letter-spacing:0.5px;">LEAD ORCHESTRATOR</span>
              <span class="status-badge ${running ? 'running' : 'stopped'}">
                <span class="dot"></span>${running ? 'Running' : 'Stopped'}
              </span>
            </div>
            <div style="font-size:0.75rem;color:var(--clr-text-muted);margin-top:3px;">
              Model: <code>${escapeHTML(mainInfo.model || '(default)')}</code>
              ${mainInfo.status?.pid ? `· PID <code>${mainInfo.status.pid}</code>` : ''}
            </div>
          </div>
          <div style="display:flex;gap:6px;align-items:center;flex-shrink:0;">
            ${running
              ? `<button class="btn btn-sm" id="orch-stop-btn" style="background:rgba(239,68,68,0.15);color:var(--clr-danger);border:1px solid rgba(239,68,68,0.3);">⏹ Stop</button>`
              : `<button class="btn btn-sm btn-primary" id="orch-start-btn">▶ Start</button>`
            }
            <button class="btn btn-ghost btn-sm" id="orch-workspace-btn" title="Kelola Workspace">🗂️ Workspace</button>
            <button class="btn btn-ghost btn-sm btn-model-edit" id="orch-model-btn" title="Ganti model">✏️</button>
            <button class="btn btn-ghost btn-sm" id="orch-soul-btn" title="Update SOUL.md dengan daftar sub-agent terkini">🔄 Sync SOUL</button>
          </div>
        </div>

        <!-- Sub-agent summary loading placeholder -->
        <div id="orch-subagents" style="border-top:1px solid rgba(108,99,255,0.2);padding-top:10px;margin-top:4px;">
          <span style="font-size:0.75rem;color:var(--clr-text-muted);">Memuat daftar sub-agent… ⏳</span>
        </div>
      </div>
    `;

    // Wire up buttons
    container.querySelector('#orch-start-btn')?.addEventListener('click', () => this.handleStart('main'));
    container.querySelector('#orch-stop-btn')?.addEventListener('click', () => this.handleStop('main'));
    container.querySelector('#orch-workspace-btn')?.addEventListener('click', () => this.openWorkspaceEditor('main', mainInfo.label || 'Main Orchestrator'));
    container.querySelector('#orch-model-btn')?.addEventListener('click', () => this.handleUpdateModel('main', mainInfo.model));
    container.querySelector('#orch-soul-btn')?.addEventListener('click', () => this.handleSyncOrchestratorSOUL());

    // Async: load sub-agent list
    this.loadOrchestratorStatus(container.querySelector<HTMLDivElement>('#orch-subagents')!);
  }

  private async loadOrchestratorStatus(subEl: HTMLDivElement) {
    try {
      const status: OrchestratorStatus = await api.getOrchestratorStatus();
      const subs = status.sub_agents ?? [];
      if (subs.length === 0) {
        subEl.innerHTML = `<span style="font-size:0.75rem;color:var(--clr-text-muted);">Belum ada sub-agent terdaftar.</span>`;
        return;
      }
      subEl.innerHTML = `
        <div style="font-size:0.72rem;color:var(--clr-text-muted);font-weight:600;letter-spacing:0.5px;margin-bottom:6px;">
          SUB-AGENTS (${status.running_count}/${subs.length} running)
        </div>
        <div style="display:flex;flex-wrap:wrap;gap:8px;">
          ${subs.map(s => `
            <div style="
              background:rgba(255,255,255,0.04);border:1px solid var(--clr-border);
              border-radius:var(--radius-sm);padding:6px 10px;
              display:flex;align-items:center;gap:8px;min-width:160px;
            ">
              <span style="font-size:0.85rem;">${s.status?.running ? '🟢' : '🔴'}</span>
              <div style="min-width:0;">
                <div style="font-size:0.78rem;font-weight:600;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">${escapeHTML(s.label || s.id)}</div>
                <div style="font-size:0.68rem;color:var(--clr-text-muted);">${escapeHTML(s.role || s.id)}</div>
              </div>
              <div style="display:flex;gap:4px;margin-left:auto;flex-shrink:0;">
                ${s.status?.running
                  ? `<button class="btn btn-ghost btn-sm" data-sub-stop="${s.id}" style="font-size:0.65rem;padding:2px 6px;">⏹</button>`
                  : `<button class="btn btn-ghost btn-sm" data-sub-start="${s.id}" style="font-size:0.65rem;padding:2px 6px;">▶</button>`
                }
              </div>
            </div>
          `).join('')}
        </div>
      `;
      // Wire sub-agent start/stop
      subEl.querySelectorAll<HTMLButtonElement>('[data-sub-start]').forEach(btn =>
        btn.addEventListener('click', () => this.handleStart(btn.dataset.subStart!))
      );
      subEl.querySelectorAll<HTMLButtonElement>('[data-sub-stop]').forEach(btn =>
        btn.addEventListener('click', () => this.handleStop(btn.dataset.subStop!))
      );
    } catch {
      subEl.innerHTML = `<span style="font-size:0.72rem;color:var(--clr-text-muted);">⚠️ Gagal memuat status sub-agent</span>`;
    }
  }

  private async handleSyncOrchestratorSOUL() {
    try {
      const res = await api.updateOrchestratorSOUL();
      showToast(`✅ SOUL.md Orchestrator diperbarui: ${res.path}`, 'success');
    } catch (err: unknown) {
      showToast(`Gagal sync SOUL: ${err instanceof Error ? err.message : err}`, 'error');
    }
  }


  private buildAgentRow(info: AgentInfo): HTMLDivElement {
    const rowWrap = document.createElement('div');
    rowWrap.className = 'agent-row-wrap';
    rowWrap.id = `row-${info.id}`;

    const row = document.createElement('div');
    row.className = `agent-row ${info.status.running ? 'running' : ''} ${!info.has_token ? 'no-token' : ''}`;
    row.dataset.agentId = info.id;
    row.innerHTML = this.rowInnerHTML(info);

    // Expand collapse on row click (except buttons)
    row.addEventListener('click', (e) => {
      if ((e.target as HTMLElement).closest('button')) return;
      this.toggleExpand(info.id, rowWrap);
    });

    // Button handlers
    row.querySelector<HTMLButtonElement>(`#row-start-${info.id}`)?.addEventListener('click', (e) => {
      e.stopPropagation(); this.handleStart(info.id);
    });
    row.querySelector<HTMLButtonElement>(`#row-stop-${info.id}`)?.addEventListener('click', (e) => {
      e.stopPropagation(); this.handleStop(info.id);
    });
    row.querySelector<HTMLButtonElement>(`#row-persona-${info.id}`)?.addEventListener('click', (e) => {
      e.stopPropagation(); this.openWorkspaceEditor(info.id, info.label);
    });
    row.querySelector<HTMLButtonElement>(`#row-delete-${info.id}`)?.addEventListener('click', (e) => {
      e.stopPropagation(); this.handleDelete(info.id, info.label);
    });
    row.querySelector<HTMLButtonElement>(`#row-logs-${info.id}`)?.addEventListener('click', (e) => {
      e.stopPropagation(); this.toggleExpand(info.id, rowWrap);
    });
    row.querySelector<HTMLButtonElement>(`#row-model-${info.id}`)?.addEventListener('click', (e) => {
      e.stopPropagation(); this.handleUpdateModel(info.id, info.model);
    });

    rowWrap.appendChild(row);

    // Detail panel (expanded logs + info)
    const detail = document.createElement('div');
    detail.className = 'agent-row-detail';
    detail.id = `detail-${info.id}`;
    detail.style.display = 'none';

    // No-token warning
    if (!info.has_token) {
      const warn = document.createElement('div');
      warn.className = 'no-token-banner';
      warn.innerHTML = `⚠️ Token Telegram belum dikonfigurasi — edit <code>credentials/agents.json</code> untuk agent <code>${escapeHTML(info.id)}</code> atau hapus dan buat ulang via dashboard`;
      detail.appendChild(warn);
    }

    // Log panel (lazy-init)
    const logMount = document.createElement('div');
    logMount.id = `logmount-${info.id}`;
    detail.appendChild(logMount);

    rowWrap.appendChild(detail);
    return rowWrap;
  }

  private rowInnerHTML(info: AgentInfo): string {
    const running = info.status.running;
    const pid = info.status.pid;
    const startedAt = info.status.started_at ? new Date(info.status.started_at).toLocaleTimeString('id-ID') : '—';
    const runtimeWarning = info.status.runtime_stale
      ? escapeHTML(info.status.runtime_warning || 'Agent runtime stale, restart agent.')
      : '';
    const cleanId = escapeHTML(info.id);
    const cleanLabel = escapeHTML(info.label);
    const cleanModel = escapeHTML(info.model);

    return `
      <div class="col-agent">
        <div class="agent-avatar-sm">${avatar(info.id)}</div>
        <div>
          <div class="agent-row-name">${cleanLabel}</div>
          <div class="agent-row-id">${cleanId}</div>
        </div>
        ${!info.has_token ? '<span class="token-warn-badge">⚠ No Token</span>' : ''}
      </div>
      <div class="col-model">
        <span class="agent-model" title="${cleanModel}">${cleanModel}</span>
        <button class="btn-model-edit" id="row-model-${cleanId}" title="Ganti Model">✏️</button>
      </div>
      <div class="col-status">
        <span class="status-badge ${running ? 'running' : 'stopped'}">
          <span class="dot"></span>${running ? 'Running' : 'Stopped'}
        </span>
        ${running ? `<span style="font-size:0.7rem;color:var(--clr-text-muted);margin-left:6px">${startedAt}</span>` : ''}
        ${runtimeWarning ? `<span class="token-warn-badge" title="${runtimeWarning}" style="margin-left:6px">stale</span>` : ''}
      </div>
      <div class="col-pid" style="font-family:var(--font-mono);font-size:0.8rem;color:var(--clr-text-muted)">${running && pid ? pid : '—'}</div>
      <div class="col-actions">
        <button class="btn btn-success btn-sm" id="row-start-${info.id}" ${running || !info.has_token ? 'disabled' : ''}>▶</button>
        <button class="btn btn-danger btn-sm" id="row-stop-${info.id}" ${!running ? 'disabled' : ''}>■</button>
        <button class="btn btn-ghost btn-sm" id="row-logs-${info.id}" title="Logs">📋</button>
        <button class="btn btn-ghost btn-sm" id="row-persona-${info.id}" title="Kelola Workspace">🗂️</button>
        <button class="btn btn-danger btn-sm" id="row-delete-${info.id}" title="Hapus Agent" style="opacity:0.6">🗑</button>
      </div>
    `;
  }

  private updateRowStatus(info: AgentInfo) {
    const row = this.agentListEl.querySelector<HTMLDivElement>(`[data-agent-id="${info.id}"]`);
    if (!row) return;
    row.className = `agent-row ${info.status.running ? 'running' : ''} ${!info.has_token ? 'no-token' : ''}`;
    row.innerHTML = this.rowInnerHTML(info);

    // Re-bind events
    row.addEventListener('click', (e) => {
      if ((e.target as HTMLElement).closest('button')) return;
      const rowWrap = document.getElementById(`row-${info.id}`) as HTMLDivElement;
      this.toggleExpand(info.id, rowWrap);
    });
    row.querySelector<HTMLButtonElement>(`#row-start-${info.id}`)?.addEventListener('click', (e) => { e.stopPropagation(); this.handleStart(info.id); });
    row.querySelector<HTMLButtonElement>(`#row-stop-${info.id}`)?.addEventListener('click', (e) => { e.stopPropagation(); this.handleStop(info.id); });
    row.querySelector<HTMLButtonElement>(`#row-persona-${info.id}`)?.addEventListener('click', (e) => { e.stopPropagation(); this.openWorkspaceEditor(info.id, info.label); });
    row.querySelector<HTMLButtonElement>(`#row-delete-${info.id}`)?.addEventListener('click', (e) => { e.stopPropagation(); this.handleDelete(info.id, info.label); });
    row.querySelector<HTMLButtonElement>(`#row-logs-${info.id}`)?.addEventListener('click', (e) => { e.stopPropagation(); const rw = document.getElementById(`row-${info.id}`) as HTMLDivElement; this.toggleExpand(info.id, rw); });
    row.querySelector<HTMLButtonElement>(`#row-model-${info.id}`)?.addEventListener('click', (e) => { e.stopPropagation(); this.handleUpdateModel(info.id, info.model); });
  }

  private toggleExpand(agentId: string, rowWrap: HTMLDivElement) {
    const detail = rowWrap.querySelector<HTMLDivElement>(`#detail-${agentId}`)!;
    const isOpen = this.expandedRows.has(agentId);

    if (isOpen) {
      this.expandedRows.delete(agentId);
      detail.style.display = 'none';
      this.logViewers.get(agentId)?.disconnect();
    } else {
      this.expandedRows.add(agentId);
      detail.style.display = 'block';

      // Lazy-init or re-mount log viewer if detached
      let lv = this.logViewers.get(agentId);
      if (!lv) {
        lv = new LogViewer(agentId);
        this.logViewers.set(agentId, lv);
      }
      const mount = detail.querySelector(`#logmount-${agentId}`)!;
      if (!mount.contains(lv.getElement())) {
          mount.innerHTML = '';
          mount.appendChild(lv.getElement());
      }

      const info = this.agents.find(a => a.id === agentId);
      if (info?.status.running) {
        this.logViewers.get(agentId)?.connect(API_BASE);
      }
    }
  }

  // ── Actions ───────────────────────────────────────────────────────────────

  private async handleStart(agentId: string) {
    if (this.busyAgents.has(agentId)) return;
    this.busyAgents.add(agentId);
    const startBtn = this.agentListEl.querySelector<HTMLButtonElement>(`#row-start-${agentId}`);
    if (startBtn) { startBtn.disabled = true; startBtn.innerHTML = '<span class="loading-spinner"></span>'; }
    try {
      await api.startAgent(agentId);
      showToast(`✅ Agent "${agentId}" distart`, 'success');

      // Auto-expand logs
      const rowWrap = document.getElementById(`row-${agentId}`) as HTMLDivElement;
      if (rowWrap && !this.expandedRows.has(agentId)) this.toggleExpand(agentId, rowWrap);
      else this.logViewers.get(agentId)?.connect(API_BASE);

      await this.loadAgents();
    } catch (err: unknown) {
      showToast(`Gagal start: ${err instanceof Error ? err.message : err}`, 'error');
    } finally {
      this.busyAgents.delete(agentId);
    }
  }

  private async handleStop(agentId: string) {
    if (this.busyAgents.has(agentId)) return;
    this.busyAgents.add(agentId);
    const stopBtn = this.agentListEl.querySelector<HTMLButtonElement>(`#row-stop-${agentId}`);
    if (stopBtn) { stopBtn.disabled = true; stopBtn.innerHTML = '<span class="loading-spinner"></span>'; }
    try {
      await api.stopAgent(agentId);
      showToast(`🛑 Agent "${agentId}" dihentikan`, 'info');
      this.logViewers.get(agentId)?.disconnect();
      await this.loadAgents();
    } catch (err: unknown) {
      showToast(`Gagal stop: ${err instanceof Error ? err.message : err}`, 'error');
    } finally {
      this.busyAgents.delete(agentId);
    }
  }

  private async handleDelete(agentId: string, label: string) {
    const confirmed = window.confirm(`Hapus agen "${label}" (${agentId}) sepenuhnya?\n\nPerhatian: Tindakan ini akan MENGHAPUS seluruh file yang berkaitan:\n- Profil registry (credentials/agents.json)\n- Persona (Workspace SOUL.md)\n- Registrasi OpenClaw agent`);
    if (!confirmed) return;

    try {
      await api.deleteAgent(agentId);
      showToast(`🗑 Agent "${label}" dihapus`, 'info');
      this.logViewers.get(agentId)?.disconnect();
      this.logViewers.delete(agentId);
      this.expandedRows.delete(agentId);
      await this.loadAgents();
    } catch (err: unknown) {
      showToast(`Gagal hapus: ${err instanceof Error ? err.message : err}`, 'error');
    }
  }

  private openWorkspaceEditor(agentId: string, label: string) {
    const ed = new WorkspaceEditor(agentId, label);
    ed.open();
  }

  private async handleUpdateModel(agentId: string, currentModel: string) {
    const picker = new ModelPicker({
      currentModel,
      manualPlaceholder: 'provider/model-id, contoh: openrouter/nvidia/model:free',
      emptyLabel: 'Pilih dari katalog atau tulis manual untuk provider/model yang belum muncul.',
    });

    try {
      const res = await api.getAvailableModels();
      const groups = res.groups || { cloud: [], cloud_proxied: [], local: [] };
      const routerStatus = await api.getRouterStatus().catch(() => null);
      picker.setModels([
        ...flattenModelGroups(groups),
        ...routerModelsToEntries(routerStatus),
      ]);
    } catch { /* silent */ }

    const overlay = document.createElement('div');
    overlay.style.cssText = 'position:fixed;inset:0;background:rgba(0,0,0,0.55);z-index:9999;display:flex;align-items:center;justify-content:center;';
    const dialog = document.createElement('div');
    dialog.style.cssText = 'background:var(--clr-surface);border:1px solid var(--clr-border);border-radius:var(--radius-lg);padding:24px;width:min(760px,92vw);max-height:86vh;overflow:auto;box-shadow:0 20px 60px rgba(0,0,0,0.5);';
    dialog.innerHTML = `
      <h3 style="margin:0 0 6px;font-size:1rem;">🔄 Ganti Model — <code style="font-size:0.85rem">${escapeHTML(agentId)}</code></h3>
      <p style="font-size:0.78rem;color:var(--clr-text-muted);margin-bottom:16px;">Saat ini: <strong>${escapeHTML(currentModel || '(default)')}</strong></p>
      <div id="model-picker-mount" style="margin-bottom:12px;">
        <label style="font-size:0.8rem;color:var(--clr-text-2);margin-bottom:6px;display:block;">Model Baru</label>
      </div>
      <div style="display:flex;gap:8px;justify-content:flex-end;margin-top:20px;">
        <button id="model-picker-cancel" class="btn btn-ghost">Batal</button>
        <button id="model-picker-save" class="btn btn-primary">💾 Simpan</button>
      </div>
    `;
    overlay.appendChild(dialog);
    document.body.appendChild(overlay);

    dialog.querySelector<HTMLDivElement>('#model-picker-mount')!.appendChild(picker.getElement());
    setTimeout(() => picker.focus(), 80);

    const cleanup = () => overlay.remove();
    overlay.addEventListener('click', (e) => { if (e.target === overlay) cleanup(); });
    dialog.querySelector('#model-picker-cancel')!.addEventListener('click', cleanup);
    dialog.querySelector('#model-picker-save')!.addEventListener('click', async () => {
      const newModel = picker.getValue().trim();
      if (!newModel) { showToast('Pilih model terlebih dahulu', 'error'); return; }
      if (newModel === currentModel) { cleanup(); return; }
	      const saveBtn = dialog.querySelector<HTMLButtonElement>('#model-picker-save')!;
	      saveBtn.disabled = true; saveBtn.textContent = '⏳ Menyimpan...';
	      try {
	        await api.updateAgentModel(agentId, newModel, modelRouteMetaFromEntry(picker.getSelectedModel()));
	        showToast(`✅ Model "${agentId}" → ${newModel}`, 'success');
        cleanup();
        await this.loadAgents();
      } catch (err: unknown) {
        showToast(`Gagal: ${err instanceof Error ? err.message : err}`, 'error');
        saveBtn.disabled = false; saveBtn.textContent = '💾 Simpan';
      }
    });
  }

  private openAddModal() {
    const modal = new AddAgentModal((newId) => {
      this.loadAgents();
      this.renderOCPanel();
      // Highlight new row with flash animation
      setTimeout(() => {
        const row = document.getElementById(`row-${newId}`);
        if (row) {
          row.style.transition = 'box-shadow 0.5s';
          row.style.boxShadow = '0 0 20px rgba(108,99,255,0.5)';
          setTimeout(() => { row.style.boxShadow = ''; }, 2000);
        }
      }, 300);
    });
    modal.open();
  }

  private async openImportModal(agentId: string) {
    try {
      const res = await api.importOCAgent(agentId);
      const det: DetectedAgent = res.agent;
      const modal = new AddAgentModal((_newId) => {
        this.loadAgents();
        this.loadOCStatus();
      }, det);
      modal.open();
    } catch (err: unknown) {
      showToast(`Gagal load data agent: ${err instanceof Error ? err.message : err}`, 'error');
    }
  }

  // ── Placeholders ──────────────────────────────────────────────────────────

  private skeletonHTML() {
    return `
      <div class="agent-table-header skeletons">
        <span class="col-agent">Agent</span><span class="col-model">Model</span>
        <span class="col-status">Status</span><span class="col-pid">PID</span>
        <span class="col-actions">Aksi</span>
      </div>
      ${Array.from({length: 3}).map(() => `
        <div class="agent-row-wrap">
          <div class="agent-row skeleton-row">
            <div class="skel-block" style="width:180px;height:38px"></div>
            <div class="skel-block" style="width:140px;height:20px"></div>
            <div class="skel-block" style="width:80px;height:22px;border-radius:100px"></div>
            <div class="skel-block" style="width:40px;height:18px"></div>
            <div class="skel-block" style="width:160px;height:32px"></div>
          </div>
        </div>
      `).join('')}
    `;
  }

  private emptyHTML() {
    return `
      <div style="padding:48px;text-align:center;color:var(--clr-text-muted)">
        <div style="font-size:3rem;margin-bottom:16px">🤖</div>
        <p style="font-weight:600;margin-bottom:8px">Belum ada agent</p>
        <p style="font-size:0.85rem">Klik <strong>➕ Tambah Agent</strong> untuk membuat agent pertama</p>
      </div>
    `;
  }

  private errorHTML(msg: string) {
    return `
      <div style="padding:32px;text-align:center;">
        <div style="color:var(--clr-danger);font-size:2rem;margin-bottom:12px">⚠️</div>
        <p style="color:var(--clr-danger);font-weight:600">Gagal menghubungi backend</p>
        <p style="color:var(--clr-text-muted);font-size:0.85rem;margin-top:8px">${escapeHTML(msg)}</p>
        <p style="color:var(--clr-text-muted);font-size:0.8rem;margin-top:4px">Pastikan backend: <code>cd backend && make run</code></p>
      </div>
    `;
  }
}
