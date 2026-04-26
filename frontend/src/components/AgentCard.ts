// components/AgentCard.ts
// Card komponen untuk satu agent: status, controls, log viewer, persona editor.

import { api, API_BASE, type AgentInfo } from '../api/client.js';
import { LogViewer } from './LogViewer.js';
import { PersonaEditor } from './PersonaEditor.js';
import { showToast } from './Toast.js';
import { escapeHTML } from '../utils/dom.js';

const AGENT_AVATARS: Record<string, string> = {
  main: 'M', dev: 'D', alpha: 'A',
};
function getAvatar(id: string) { return AGENT_AVATARS[id] ?? id.slice(0, 2).toUpperCase(); }

export class AgentCard {
  private el: HTMLDivElement;
  private info: AgentInfo;
  private logViewer: LogViewer;
  private startBtn!: HTMLButtonElement;
  private stopBtn!: HTMLButtonElement;
  private statusBadge!: HTMLSpanElement;
  private pidSpan!: HTMLSpanElement;
  private uptimeSpan!: HTMLSpanElement;
  private logVisible = false;
  private logContainer!: HTMLDivElement;
  private busy = false;

  constructor(info: AgentInfo) {
    this.info = info;
    this.logViewer = new LogViewer(info.id);
    this.el = document.createElement('div');
    this.el.className = 'card agent-card ' + (info.status.running ? 'running' : 'stopped');
    this.el.id = `agent-card-${info.id}`;
    this.render();
  }

  getElement() { return this.el; }

  updateInfo(info: AgentInfo) {
    this.info = info;
    this.el.className = 'card agent-card ' + (info.status.running ? 'running' : 'stopped');
    this.refreshStatus();
  }

  private render() {
    const info = this.info;

    // ── Header ────────────────────────────────────────────────────────────
    const header = document.createElement('div');
    header.className = 'card-header';

    const meta = document.createElement('div');
    meta.className = 'agent-meta';

    const avatar = document.createElement('div');
    avatar.className = 'agent-avatar';
    avatar.textContent = getAvatar(info.id);

    const agentInfo = document.createElement('div');
    agentInfo.className = 'agent-info';
    agentInfo.innerHTML = `
      <h3>${escapeHTML(info.label)}</h3>
      <div class="agent-id">${escapeHTML(info.id)}</div>
    `;

    meta.append(avatar, agentInfo);

    const modelBadge = document.createElement('span');
    modelBadge.className = 'agent-model';
    modelBadge.textContent = info.model;

    header.append(meta, modelBadge);

    // ── Status Badge ───────────────────────────────────────────────────────
    this.statusBadge = document.createElement('span');
    this.refreshStatusBadge();

    // ── No Token Warning ───────────────────────────────────────────────────
    let warningEl: HTMLElement | null = null;
    if (!info.has_token) {
      warningEl = document.createElement('div');
      warningEl.className = 'no-token-banner';
      warningEl.textContent = 'Token Telegram belum dikonfigurasi - edit ';
      const code = document.createElement('code');
      code.textContent = `credentials/agents/${info.id}.env`;
      warningEl.appendChild(code);
    }

    // ── Stats ─────────────────────────────────────────────────────────────
    const stats = document.createElement('div');
    stats.className = 'agent-stats';

    this.pidSpan = document.createElement('span');
    this.pidSpan.className = 'stat-value';

    const pidItem = document.createElement('div');
    pidItem.className = 'stat-item';
    pidItem.innerHTML = '<div class="stat-label">PID</div>';
    pidItem.appendChild(this.pidSpan);

    this.uptimeSpan = document.createElement('span');
    this.uptimeSpan.className = 'stat-value';

    const uptimeItem = document.createElement('div');
    uptimeItem.className = 'stat-item';
    uptimeItem.innerHTML = '<div class="stat-label">Started</div>';
    uptimeItem.appendChild(this.uptimeSpan);

    stats.append(pidItem, uptimeItem);
    this.refreshStats();

    // ── Action Buttons ─────────────────────────────────────────────────────
    const btnGroup = document.createElement('div');
    btnGroup.className = 'btn-group';

    this.startBtn = document.createElement('button');
    this.startBtn.className = 'btn btn-success btn-sm';
    this.startBtn.textContent = 'Start';
    this.startBtn.id = `btn-start-${info.id}`;
    this.startBtn.addEventListener('click', () => this.handleStart());

    this.stopBtn = document.createElement('button');
    this.stopBtn.className = 'btn btn-danger btn-sm';
    this.stopBtn.textContent = 'Stop';
    this.stopBtn.id = `btn-stop-${info.id}`;
    this.stopBtn.addEventListener('click', () => this.handleStop());

    const editBtn = document.createElement('button');
    editBtn.className = 'btn btn-ghost btn-sm';
    editBtn.textContent = 'Persona';
    editBtn.id = `btn-persona-${info.id}`;
    editBtn.addEventListener('click', () => this.openPersonaEditor());

    const logBtn = document.createElement('button');
    logBtn.className = 'btn btn-ghost btn-sm';
    logBtn.textContent = 'Logs';
    logBtn.id = `btn-logs-${info.id}`;
    logBtn.addEventListener('click', () => this.toggleLogs(logBtn));

    btnGroup.append(this.startBtn, this.stopBtn, editBtn, logBtn);
    this.refreshButtons();

    // ── Log Container (hidden by default) ──────────────────────────────────
    this.logContainer = document.createElement('div');
    this.logContainer.style.display = 'none';
    this.logContainer.style.marginTop = '12px';
    this.logContainer.appendChild(this.logViewer.getElement());

    // ── Divider ───────────────────────────────────────────────────────────
    const divider = document.createElement('hr');
    divider.className = 'divider';

    // ── Assemble ──────────────────────────────────────────────────────────
    this.el.innerHTML = '';
    this.el.append(header, this.statusBadge);
    if (warningEl) this.el.appendChild(warningEl);
    this.el.append(stats, divider, btnGroup, this.logContainer);
  }

  // ── Event Handlers ───────────────────────────────────────────────────────

  private async handleStart() {
    if (this.busy) return;
    this.setBusy(true, this.startBtn, 'Starting...');
    try {
      await api.startAgent(this.info.id);
      showToast(`Agent "${this.info.label}" berhasil distart`, 'success');
      // Auto-show logs on start
      if (!this.logVisible) {
        this.logVisible = true;
        this.logContainer.style.display = 'block';
        this.logViewer.connect(API_BASE);
      }
      await this.refreshFromServer();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      showToast(`Gagal start agent: ${msg}`, 'error');
    } finally {
      this.setBusy(false, this.startBtn, 'Start');
    }
  }

  private async handleStop() {
    if (this.busy) return;
    this.setBusy(true, this.stopBtn, 'Stopping...');
    try {
      await api.stopAgent(this.info.id);
      showToast(`Agent "${this.info.label}" dihentikan`, 'info');
      this.logViewer.disconnect();
      await this.refreshFromServer();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      showToast(`Gagal stop agent: ${msg}`, 'error');
    } finally {
      this.setBusy(false, this.stopBtn, 'Stop');
    }
  }

  private async openPersonaEditor() {
    const editor = new PersonaEditor(this.info.id, this.info.label);
    await editor.open();
  }

  private toggleLogs(btn: HTMLButtonElement) {
    this.logVisible = !this.logVisible;
    this.logContainer.style.display = this.logVisible ? 'block' : 'none';
    btn.textContent = this.logVisible ? 'Hide Logs' : 'Logs';

    if (this.logVisible && this.info.status.running) {
      this.logViewer.connect(API_BASE);
    } else if (!this.logVisible) {
      this.logViewer.disconnect();
    }
  }

  // ── Status Refresh ───────────────────────────────────────────────────────

  private async refreshFromServer() {
    try {
      const status = await api.getAgentStatus(this.info.id);
      this.info.status = status;
      this.el.className = 'card agent-card ' + (status.running ? 'running' : 'stopped');
      this.refreshStatus();
    } catch {}
  }

  private refreshStatus() {
    this.refreshStatusBadge();
    this.refreshStats();
    this.refreshButtons();
  }

  private refreshStatusBadge() {
    const running = this.info.status.running;
    this.statusBadge.className = `status-badge ${running ? 'running' : 'stopped'}`;
    this.statusBadge.innerHTML = `
      <span class="dot"></span>
      ${running ? 'Running' : 'Stopped'}
    `;
  }

  private refreshStats() {
    const { running, pid, started_at } = this.info.status;
    this.pidSpan.textContent  = running && pid ? String(pid) : '—';
    this.uptimeSpan.textContent = running && started_at
      ? new Date(started_at).toLocaleTimeString('id-ID')
      : '—';
  }

  private refreshButtons() {
    const running = this.info.status.running;
    const noToken = !this.info.has_token;
    this.startBtn.disabled = running || noToken;
    this.stopBtn.disabled  = !running;
  }

  private setBusy(busy: boolean, btn: HTMLButtonElement, label: string) {
    this.busy = busy;
    if (busy) {
      btn.innerHTML = `<span class="loading-spinner"></span> ${label}`;
      btn.disabled = true;
    } else {
      btn.textContent = label;
      this.refreshButtons();
    }
  }
}
