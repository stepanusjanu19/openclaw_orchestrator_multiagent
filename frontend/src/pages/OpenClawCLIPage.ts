import { api, type OpenClawCommand, type OpenClawCommandRunResult } from '../api/client.js';
import { showToast } from '../components/Toast.js';

function escapeHTML(value: string): string {
  return value.replace(/[&<>"']/g, ch => ({
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;',
  }[ch] ?? ch));
}

function safeExternalURL(raw: string): string {
  try {
    const url = new URL(raw);
    if (url.protocol === 'http:' || url.protocol === 'https:') {
      return url.toString();
    }
  } catch {}
  return '#';
}

export class OpenClawCLIPage {
  private el: HTMLDivElement;
  private contentEl!: HTMLDivElement;
  private searchEl!: HTMLInputElement;
  private categoryEl!: HTMLSelectElement;
  private commands: OpenClawCommand[] = [];
  private selectedId = '';
  private outputEl!: HTMLPreElement;

  constructor() {
    this.el = document.createElement('div');
    this.el.className = 'openclaw-cli-page';
    this.build();
  }

  getElement() { return this.el; }

  async mount() {
    await this.loadCommands();
  }

  unmount() {}

  private build() {
    const header = document.createElement('div');
    header.className = 'page-header';
    header.innerHTML = `
      <div>
        <h1>OpenClaw CLI</h1>
        <p class="subtitle">Command map aman untuk inspeksi, diagnostik, dan operasi OpenClaw terkurasi</p>
      </div>
    `;

    const body = document.createElement('div');
    body.className = 'page-body';
    this.contentEl = document.createElement('div');
    this.contentEl.className = 'oc-cli-layout';
    body.appendChild(this.contentEl);
    this.el.append(header, body);
  }

  private async loadCommands() {
    this.contentEl.innerHTML = `<div class="oc-cli-loading">Memuat command map...</div>`;
    try {
      const res = await api.getOpenClawCommands();
      this.commands = res.commands ?? [];
      if (!this.selectedId && this.commands.length > 0) this.selectedId = this.commands[0].id;
      this.render();
    } catch (err: unknown) {
      this.contentEl.innerHTML = `<div class="error-box">Gagal memuat command map: ${escapeHTML(err instanceof Error ? err.message : String(err))}</div>`;
    }
  }

  private render() {
    const categories = Array.from(new Set(this.commands.map(c => c.category))).sort();
    const left = document.createElement('section');
    left.className = 'oc-cli-sidebar';
    left.innerHTML = `
      <div class="oc-cli-toolbar">
        <input id="oc-cli-search" class="form-input" placeholder="Cari command">
        <select id="oc-cli-category" class="form-input">
          <option value="">Semua</option>
          ${categories.map(cat => `<option value="${escapeHTML(cat)}">${escapeHTML(cat)}</option>`).join('')}
        </select>
      </div>
      <div id="oc-cli-list" class="oc-cli-list"></div>
    `;

    const right = document.createElement('section');
    right.id = 'oc-cli-detail';
    right.className = 'oc-cli-detail';

    this.contentEl.innerHTML = '';
    this.contentEl.append(left, right);
    this.searchEl = left.querySelector<HTMLInputElement>('#oc-cli-search')!;
    this.categoryEl = left.querySelector<HTMLSelectElement>('#oc-cli-category')!;
    this.searchEl.addEventListener('input', () => this.renderList());
    this.categoryEl.addEventListener('change', () => this.renderList());
    this.renderList();
    this.renderDetail();
  }

  private renderList() {
    const list = this.contentEl.querySelector<HTMLDivElement>('#oc-cli-list')!;
    const q = this.searchEl.value.trim().toLowerCase();
    const category = this.categoryEl.value;
    const filtered = this.commands.filter(cmd => {
      const haystack = `${cmd.id} ${cmd.path.join(' ')} ${cmd.description} ${cmd.category}`.toLowerCase();
      return (!q || haystack.includes(q)) && (!category || cmd.category === category);
    });
    list.innerHTML = filtered.map(cmd => {
      const active = cmd.id === this.selectedId;
      return `
        <button class="oc-cli-command ${active ? 'selected' : ''}" data-command="${escapeHTML(cmd.id)}">
          <span class="oc-cli-command-copy">
            <span class="oc-cli-command-path">${escapeHTML(cmd.path.join(' '))}</span>
            <span class="oc-cli-command-category">${escapeHTML(cmd.category)}</span>
          </span>
          ${this.safetyLabel(cmd.safety)}
        </button>
      `;
    }).join('') || `<div class="oc-cli-empty">Tidak ada command cocok.</div>`;
    list.querySelectorAll<HTMLButtonElement>('[data-command]').forEach(btn => {
      btn.addEventListener('click', () => {
        this.selectedId = btn.dataset.command ?? '';
        this.renderList();
        this.renderDetail();
      });
    });
  }

  private renderDetail() {
    const detail = this.contentEl.querySelector<HTMLDivElement>('#oc-cli-detail')!;
    const cmd = this.commands.find(c => c.id === this.selectedId);
    if (!cmd) {
      detail.innerHTML = '';
      return;
    }
    const docsURL = safeExternalURL(cmd.docs_url);
    detail.innerHTML = `
      <div class="oc-cli-detail-card">
        <div class="oc-cli-detail-head">
          <div class="oc-cli-detail-title">
            <div class="oc-cli-detail-path">${escapeHTML(cmd.path.join(' '))}</div>
            <div class="oc-cli-detail-description">${escapeHTML(cmd.description)}</div>
          </div>
          ${this.safetyLabel(cmd.safety)}
        </div>
        <div class="oc-cli-meta">
          <span>Category <code>${escapeHTML(cmd.category)}</code></span>
          <span>Runnable <code>${cmd.runnable ? 'yes' : 'no'}</code></span>
          <a href="${escapeHTML(docsURL)}" target="_blank" rel="noopener noreferrer">Docs</a>
        </div>
        <form id="oc-cli-form" class="oc-cli-form">
          ${(cmd.args ?? []).map(arg => `
            <label class="oc-cli-arg">
              ${escapeHTML(arg.label)}${arg.required ? ' *' : ''}
              ${arg.options && arg.options.length ? `
                <select class="form-input" name="${escapeHTML(arg.name)}">
                  ${arg.options.map(opt => `<option value="${escapeHTML(opt)}">${escapeHTML(opt)}</option>`).join('')}
                </select>
              ` : `
                <input class="form-input" name="${escapeHTML(arg.name)}" placeholder="${escapeHTML(arg.placeholder ?? '')}">
              `}
            </label>
          `).join('')}
          <div class="oc-cli-actions">
            <button id="oc-cli-run" class="btn btn-primary" type="submit" ${cmd.runnable ? '' : 'disabled'}>Run</button>
            <button id="oc-cli-clear" class="btn btn-ghost" type="button">Clear Output</button>
          </div>
        </form>
        <pre id="oc-cli-output" class="oc-cli-output"></pre>
      </div>
    `;
    this.outputEl = detail.querySelector<HTMLPreElement>('#oc-cli-output')!;
    detail.querySelector<HTMLButtonElement>('#oc-cli-clear')!.addEventListener('click', () => {
      this.outputEl.textContent = '';
    });
    detail.querySelector<HTMLFormElement>('#oc-cli-form')!.addEventListener('submit', (event) => {
      event.preventDefault();
      this.runSelected(cmd, false);
    });
    if (!cmd.runnable) {
      this.outputEl.textContent = cmd.safety === 'blocked'
        ? 'Blocked by dashboard policy. Jalankan manual dari terminal jika benar-benar diperlukan.'
        : 'Command ini belum memiliki runner aman di dashboard.';
    }
  }

  private async runSelected(cmd: OpenClawCommand, confirmed: boolean) {
    const form = this.contentEl.querySelector<HTMLFormElement>('#oc-cli-form');
    if (!form) return;
    const data = new FormData(form);
    const args: Record<string, string> = {};
    for (const [key, value] of data.entries()) {
      args[key] = String(value).trim();
    }

    const runBtn = this.contentEl.querySelector<HTMLButtonElement>('#oc-cli-run');
    if (runBtn) {
      runBtn.disabled = true;
      runBtn.textContent = confirmed ? 'Running confirmed...' : 'Running...';
    }
    this.outputEl.textContent = `> openclaw ${cmd.path.join(' ')}\n`;
    try {
      const result = await api.runOpenClawCommand(cmd.id, args, confirmed);
      if (result.confirmation_required) {
        if (window.confirm('Command ini mengubah state OpenClaw. Jalankan dengan konfirmasi?')) {
          await this.runSelected(cmd, true);
          return;
        }
        this.renderResult(result);
        return;
      }
      this.renderResult(result);
      if (result.error && result.error !== 'blocked_by_policy') {
        showToast(`Command selesai dengan error: ${result.error}`, 'error');
      } else {
        showToast('Command selesai', 'success');
      }
    } catch (err: unknown) {
      this.outputEl.textContent += `\nERROR: ${err instanceof Error ? err.message : String(err)}`;
      showToast(`Command gagal: ${err instanceof Error ? err.message : err}`, 'error');
    } finally {
      if (runBtn) {
        runBtn.disabled = !cmd.runnable;
        runBtn.textContent = 'Run';
      }
    }
  }

  private renderResult(result: OpenClawCommandRunResult) {
    const argv = result.argv?.join(' ') ?? '';
    const parts = [
      argv ? `argv: ${argv}` : '',
      `exit_code: ${result.exit_code}`,
      result.error ? `error: ${result.error}` : '',
      result.stdout ? `\nstdout:\n${result.stdout}` : '',
      result.stderr ? `\nstderr:\n${result.stderr}` : '',
    ].filter(Boolean);
    this.outputEl.textContent = parts.join('\n');
  }

  private safetyLabel(safety: OpenClawCommand['safety']) {
    switch (safety) {
      case 'read_only':
        return '<span class="oc-cli-safety read_only">read_only</span>';
      case 'mutating_confirm':
        return '<span class="oc-cli-safety mutating_confirm">mutating_confirm</span>';
      default:
        return '<span class="oc-cli-safety blocked">blocked</span>';
    }
  }
}
