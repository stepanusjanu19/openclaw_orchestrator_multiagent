import { api, type AuditLogEntry, type MaskingRule, type SecurityPolicy, type SecurityTemplate } from '../api/client.js';
import { showToast } from '../components/Toast.js';
import { escapeHTML } from '../utils/dom.js';

export class SecurityPage {
  private el: HTMLDivElement;
  private policy: SecurityPolicy | null = null;
  private rules: MaskingRule[] = [];
  private templates: SecurityTemplate[] = [];
  private logs: AuditLogEntry[] = [];
  private selectedTemplateId = '';
  private selectedLogIndex = -1;
  private auditFilters = { agent_id: '', level: '', channel: '', status: '', q: '', limit: 100 };

  constructor() {
    this.el = document.createElement('div');
    this.el.className = 'page-body security-page';
  }

  getElement() { return this.el; }

  async mount() {
    await this.load();
  }

  unmount() {}

  private async load() {
    this.renderLoading();
    try {
      const [policy, rules, templates, logs] = await Promise.all([
        api.getSecurityPolicy(),
        api.getMaskingRules(),
        api.getSecurityTemplates(),
        api.getAuditLogs({ limit: 50 }),
      ]);
      this.policy = policy;
      this.rules = rules.rules;
      this.templates = templates.templates || [];
      this.logs = Array.isArray(logs.logs) ? logs.logs : [];
      if (!this.selectedTemplateId && this.templates[0]?.id) this.selectedTemplateId = this.templates[0].id;
      this.render();
    } catch (err: any) {
      this.el.innerHTML = `<div class="card"><span style="color:var(--clr-danger)">Gagal memuat security center: ${escapeHTML(err.message)}</span></div>`;
    }
  }

  private renderLoading() {
    this.el.innerHTML = `
      <div class="page-header">
        <h1>Security Center</h1>
        <p class="subtitle">Memuat policy masking, template, dan audit log...</p>
      </div>
    `;
  }

  private render() {
    const policy = this.policy!;
    const selected = this.templates.find(t => t.id === this.selectedTemplateId) || this.templates[0];
    const rulesJson = escapeHTML(JSON.stringify(this.rules, null, 2));
    const templateOptions = this.templates.map(t => `<option value="${escapeHTML(t.id || '')}" ${t.id === selected?.id ? 'selected' : ''}>${escapeHTML(t.name)} v${t.version || 1}</option>`).join('');
    const tplContent = escapeHTML(selected?.content || 'Agent {{agent.id}} menjawab:\n\n{{agent.reply}}');

    this.el.innerHTML = `
      <div class="page-header">
        <h1>Security Center</h1>
        <p class="subtitle">Masking policy, encrypted audit log, dan template output aman</p>
      </div>

      <div style="display:grid;grid-template-columns:minmax(280px,0.85fr) minmax(360px,1.15fr);gap:20px;align-items:start;">
        <section class="card">
          <h3 style="margin-bottom:14px">Policy</h3>
          <label class="security-field">
            <span>Masking Mode</span>
            <select id="masking-mode">
              <option value="off" ${policy.masking_mode === 'off' ? 'selected' : ''}>OFF</option>
              <option value="on" ${policy.masking_mode === 'on' ? 'selected' : ''}>ON</option>
              <option value="strict" ${policy.masking_mode === 'strict' ? 'selected' : ''}>STRICT</option>
            </select>
          </label>
          <label class="security-field">
            <span>Logging Mode</span>
            <select id="logging-mode">
              <option value="minimal" ${policy.logging_mode === 'minimal' ? 'selected' : ''}>Minimal</option>
              <option value="audit" ${policy.logging_mode === 'audit' ? 'selected' : ''}>Audit</option>
              <option value="debug" ${policy.logging_mode === 'debug' ? 'selected' : ''}>Debug</option>
            </select>
          </label>
          <label style="display:flex;align-items:center;gap:10px;margin:12px 0;color:var(--clr-text-secondary);font-size:0.86rem;">
            <input id="template-enabled" type="checkbox" ${policy.template_rendering_enabled ? 'checked' : ''}>
            Template rendering enabled
          </label>
          <button id="save-policy" class="btn btn-primary" style="width:100%;justify-content:center;">Simpan Policy</button>
          <div class="security-runtime-status">
            <span>Audit index: <strong>${policy.audit_index_enabled ? 'enabled' : 'fallback scan'}</strong></span>
            <span>Dropped: <strong>${policy.audit_dropped || 0}</strong></span>
            <span>Write errors: <strong>${policy.audit_write_errors || 0}</strong></span>
          </div>
        </section>

        <section class="card">
          <h3 style="margin-bottom:14px">Masking Ruleset</h3>
          <textarea id="rules-editor" spellcheck="false" style="width:100%;min-height:320px;background:var(--clr-bg-elevated);border:1px solid var(--clr-border);border-radius:var(--radius-sm);color:var(--clr-text);font-family:var(--font-mono);font-size:0.78rem;padding:12px;">${rulesJson}</textarea>
          <div style="display:flex;gap:8px;margin-top:12px;justify-content:flex-end;">
            <button id="reload-rules" class="btn btn-ghost btn-sm">Reload</button>
            <button id="save-rules" class="btn btn-primary btn-sm">Simpan Rules</button>
          </div>
        </section>

        <section class="card">
          <h3 style="margin-bottom:14px">Templates</h3>
          <label class="security-field">
            <span>Template</span>
            <select id="template-select">
              ${templateOptions || '<option value="">Template baru</option>'}
            </select>
          </label>
          <label class="security-field">
            <span>Nama</span>
            <input id="template-name" value="${escapeHTML(selected?.name || 'Default Report')}" placeholder="Default Report">
          </label>
          <textarea id="template-content" spellcheck="false" style="width:100%;min-height:220px;background:var(--clr-bg-elevated);border:1px solid var(--clr-border);border-radius:var(--radius-sm);color:var(--clr-text);font-family:var(--font-mono);font-size:0.82rem;padding:12px;">${tplContent}</textarea>
          <div id="template-validation" style="font-size:0.8rem;color:var(--clr-text-muted);margin-top:8px;">Placeholder: {{agent.id}}, {{agent.reply}}, {{channel}}, {{session.id}}, {{timestamp}}, {{usage.prompt_tokens}}, {{usage.completion_tokens}}</div>
          <div style="display:flex;gap:8px;margin-top:12px;justify-content:flex-end;flex-wrap:wrap;">
            <button id="new-template" class="btn btn-ghost btn-sm">Baru</button>
            <button id="validate-template" class="btn btn-ghost btn-sm">Validate</button>
            <button id="delete-template" class="btn btn-ghost btn-sm" style="color:var(--clr-danger)">Hapus</button>
            <button id="save-template" class="btn btn-primary btn-sm">Simpan Template</button>
          </div>
        </section>

        <section class="card">
          <div style="display:flex;justify-content:space-between;align-items:center;gap:12px;margin-bottom:14px;">
            <h3>Audit Log</h3>
            <div class="audit-filter-bar">
              <input id="audit-q" placeholder="search hash/message/status" value="${escapeHTML(this.auditFilters.q)}">
              <input id="audit-agent" placeholder="agent_id" value="${escapeHTML(this.auditFilters.agent_id)}">
              <input id="audit-channel" placeholder="channel" value="${escapeHTML(this.auditFilters.channel)}">
              <input id="audit-status" placeholder="status: blocked/rate_limited/upstream_timeout" value="${escapeHTML(this.auditFilters.status)}">
              <select id="audit-level">
                <option value="">All</option>
                <option value="INFO" ${this.auditFilters.level === 'INFO' ? 'selected' : ''}>INFO</option>
                <option value="SECURITY" ${this.auditFilters.level === 'SECURITY' ? 'selected' : ''}>SECURITY</option>
                <option value="ERROR" ${this.auditFilters.level === 'ERROR' ? 'selected' : ''}>ERROR</option>
                <option value="DEBUG" ${this.auditFilters.level === 'DEBUG' ? 'selected' : ''}>DEBUG</option>
              </select>
              <select id="audit-limit">
                <option value="50" ${this.auditFilters.limit === 50 ? 'selected' : ''}>50</option>
                <option value="100" ${this.auditFilters.limit === 100 ? 'selected' : ''}>100</option>
                <option value="250" ${this.auditFilters.limit === 250 ? 'selected' : ''}>250</option>
                <option value="500" ${this.auditFilters.limit === 500 ? 'selected' : ''}>500</option>
              </select>
              <button id="refresh-audit" class="btn btn-sm btn-ghost">Refresh</button>
            </div>
          </div>
          <div class="audit-search-note">
            Filter memakai SQLite FTS metadata index lokal. Encrypted JSONL tetap menjadi source-of-truth dan dapat membangun ulang index bila diperlukan.
          </div>
          <div class="security-log-list">
            ${this.renderLogs()}
          </div>
          ${this.renderLogDetail()}
        </section>
      </div>
    `;
    this.bind();
  }

  private renderLogs() {
    const logs = Array.isArray(this.logs) ? this.logs : [];
    if (logs.length === 0) {
      return `
        <div class="audit-empty-state">
          <strong>Belum ada audit event untuk filter ini.</strong>
          <span>Audit akan muncul setelah agent memproses pesan, policy/rules/template berubah, atau log query dijalankan.</span>
          <span>Gunakan filter yang lebih longgar, misalnya level "All" dan kosongkan agent/channel/status.</span>
        </div>
      `;
    }
    return logs.map((log, index) => `
      <button class="security-log-row ${index === this.selectedLogIndex ? 'selected' : ''}" data-log-index="${index}">
        <div style="display:flex;justify-content:space-between;gap:12px;">
          <strong>${escapeHTML(log.level || 'INFO')}</strong>
          <span style="color:var(--clr-text-muted);font-family:var(--font-mono);font-size:0.72rem;">${escapeHTML(log.timestamp || '')}</span>
        </div>
        <div style="font-size:0.8rem;color:var(--clr-text-secondary);margin-top:4px;">
          ${escapeHTML(log.message || '')} ${log.agent_id ? `- agent ${escapeHTML(log.agent_id)}` : ''} ${log.channel ? `- ${escapeHTML(log.channel)}` : ''}
        </div>
        <div style="font-family:var(--font-mono);font-size:0.72rem;color:var(--clr-text-muted);margin-top:4px;word-break:break-all;">
          prompt=${escapeHTML((log.prompt_hash || '').slice(0, 16))} output=${escapeHTML((log.output_hash || '').slice(0, 16))} status=${escapeHTML(log.status || '')}
        </div>
      </button>
    `).join('');
  }

  private renderLogDetail() {
    const logs = Array.isArray(this.logs) ? this.logs : [];
    const log = this.selectedLogIndex >= 0 ? logs[this.selectedLogIndex] : null;
    if (!log) {
      return `
        <div class="audit-detail-panel muted">
          Pilih salah satu audit event untuk melihat detail metadata, hash, latency, model, dan masking mode.
        </div>
      `;
    }
	    const rows: Array<[string, any]> = [
	      ['timestamp', log.timestamp],
      ['level', log.level],
      ['status', log.status],
      ['agent_id', log.agent_id],
      ['channel', log.channel],
      ['model', log.model],
      ['masking_mode', log.masking_mode],
      ['latency_ms', log.latency_ms],
      ['prompt_hash', log.prompt_hash],
      ['output_hash', log.output_hash],
      ['error_class', log.error_class],
	      ['message', log.message],
	    ];
	    const metadata = log.metadata || {};
	    const riskRows: Array<[string, any]> = [
	      ['gateway_id', metadata.gateway_id],
	      ['visitor_hash', metadata.visitor_hash],
	      ['ip_class', metadata.ip_class],
	      ['subnet', metadata.subnet],
	      ['proxy_chain_depth', metadata.proxy_chain_depth],
	      ['risk_score', metadata.risk_score],
	      ['decision', metadata.decision],
	      ['risk_reasons', Array.isArray(metadata.risk_reasons) ? metadata.risk_reasons.join(', ') : metadata.risk_reasons],
	      ['geo', metadata.geo ? JSON.stringify(metadata.geo) : ''],
	    ];
	    return `
	      <div class="audit-detail-panel">
        <div style="display:flex;justify-content:space-between;align-items:center;gap:8px;margin-bottom:10px;">
          <strong>Audit Detail</strong>
          <button id="close-audit-detail" class="btn btn-ghost btn-sm">Tutup</button>
        </div>
	        <div class="audit-detail-grid">
	          ${rows.filter(([, value]) => value !== undefined && value !== '').map(([key, value]) => `
	            <span>${escapeHTML(key)}</span>
	            <code>${escapeHTML(String(value))}</code>
	          `).join('')}
	        </div>
	        ${riskRows.some(([, value]) => value !== undefined && value !== '') ? `
	          <div style="font-size:0.72rem;color:var(--clr-text-muted);margin:12px 0 4px;">gateway visitor/risk</div>
	          <div class="audit-detail-grid">
	            ${riskRows.filter(([, value]) => value !== undefined && value !== '').map(([key, value]) => `
	              <span>${escapeHTML(key)}</span>
	              <code>${escapeHTML(String(value))}</code>
	            `).join('')}
	          </div>
	        ` : ''}
	        <div style="margin-top:10px;">
          <div style="font-size:0.72rem;color:var(--clr-text-muted);margin-bottom:4px;">metadata</div>
          <pre class="audit-json">${escapeHTML(JSON.stringify(log.metadata || {}, null, 2))}</pre>
        </div>
      </div>
    `;
  }

  private bind() {
    this.el.querySelector('#save-policy')?.addEventListener('click', () => this.savePolicy());
    this.el.querySelector('#save-rules')?.addEventListener('click', () => this.saveRules());
    this.el.querySelector('#reload-rules')?.addEventListener('click', () => this.load());
    this.el.querySelector('#template-select')?.addEventListener('change', e => {
      this.selectedTemplateId = (e.currentTarget as HTMLSelectElement).value;
      this.render();
    });
    this.el.querySelector('#new-template')?.addEventListener('click', () => {
      this.selectedTemplateId = '';
      const name = this.el.querySelector('#template-name') as HTMLInputElement;
      const content = this.el.querySelector('#template-content') as HTMLTextAreaElement;
      name.value = 'Default Report';
      content.value = 'Agent {{agent.id}} menjawab:\n\n{{agent.reply}}';
    });
    this.el.querySelector('#validate-template')?.addEventListener('click', () => this.validateTemplate());
    this.el.querySelector('#save-template')?.addEventListener('click', () => this.saveTemplate());
    this.el.querySelector('#delete-template')?.addEventListener('click', () => this.deleteTemplate());
    this.el.querySelector('#refresh-audit')?.addEventListener('click', () => this.refreshAudit());
    this.el.querySelector('#close-audit-detail')?.addEventListener('click', () => {
      this.selectedLogIndex = -1;
      this.render();
    });
    this.el.querySelectorAll('.security-log-row').forEach(row => {
      row.addEventListener('click', e => {
        const raw = (e.currentTarget as HTMLElement).getAttribute('data-log-index');
        this.selectedLogIndex = raw ? Number(raw) : -1;
        this.render();
      });
    });
  }

  private async savePolicy() {
    try {
      const payload: SecurityPolicy = {
        masking_mode: (this.el.querySelector('#masking-mode') as HTMLSelectElement).value as any,
        logging_mode: (this.el.querySelector('#logging-mode') as HTMLSelectElement).value as any,
        template_rendering_enabled: (this.el.querySelector('#template-enabled') as HTMLInputElement).checked,
        default_template_id: this.policy?.default_template_id || '',
      };
      this.policy = await api.updateSecurityPolicy(payload);
      showToast('Security policy disimpan');
      this.render();
    } catch (err: any) {
      showToast(err.message || 'Gagal menyimpan policy', 'error');
    }
  }

  private async saveRules() {
    try {
      const raw = (this.el.querySelector('#rules-editor') as HTMLTextAreaElement).value;
      const rules = JSON.parse(raw) as MaskingRule[];
      const saved = await api.updateMaskingRules(rules);
      this.rules = saved.rules;
      showToast('Masking rules disimpan');
      this.render();
    } catch (err: any) {
      showToast(err.message || 'Rules JSON tidak valid', 'error');
    }
  }

  private async validateTemplate() {
    const content = (this.el.querySelector('#template-content') as HTMLTextAreaElement).value;
    const box = this.el.querySelector('#template-validation') as HTMLDivElement;
    try {
      const result = await api.validateSecurityTemplate(content);
      box.style.color = result.valid ? 'var(--clr-success)' : 'var(--clr-danger)';
      box.textContent = result.valid ? `Valid: ${result.placeholders.join(', ') || 'tanpa placeholder'}` : result.errors.join('; ');
    } catch (err: any) {
      box.style.color = 'var(--clr-danger)';
      box.textContent = err.message;
    }
  }

  private async saveTemplate() {
    try {
      const selected = this.templates.find(t => t.id === this.selectedTemplateId);
      const tpl = await api.saveSecurityTemplate({
        id: selected?.id,
        name: (this.el.querySelector('#template-name') as HTMLInputElement).value,
        type: selected?.type || 'report_template',
        content: (this.el.querySelector('#template-content') as HTMLTextAreaElement).value,
      });
      this.selectedTemplateId = tpl.id || '';
      showToast('Template disimpan');
      await this.load();
    } catch (err: any) {
      showToast(err.message || 'Gagal menyimpan template', 'error');
    }
  }

  private async deleteTemplate() {
    if (!this.selectedTemplateId) return;
    if (!confirm('Hapus template ini?')) return;
    try {
      await api.deleteSecurityTemplate(this.selectedTemplateId);
      this.selectedTemplateId = '';
      showToast('Template dihapus', 'info');
      await this.load();
    } catch (err: any) {
      showToast(err.message || 'Gagal menghapus template', 'error');
    }
  }

  private async refreshAudit() {
    try {
      this.auditFilters = {
        agent_id: (this.el.querySelector('#audit-agent') as HTMLInputElement).value.trim(),
        level: (this.el.querySelector('#audit-level') as HTMLSelectElement).value,
        channel: (this.el.querySelector('#audit-channel') as HTMLInputElement).value.trim(),
        status: (this.el.querySelector('#audit-status') as HTMLInputElement).value.trim(),
        q: (this.el.querySelector('#audit-q') as HTMLInputElement).value.trim(),
        limit: Number((this.el.querySelector('#audit-limit') as HTMLSelectElement).value) || 100,
      };
      const resp = await api.getAuditLogs(this.auditFilters);
      this.logs = Array.isArray(resp.logs) ? resp.logs : [];
      this.selectedLogIndex = -1;
      this.render();
    } catch (err: any) {
      showToast(err.message || 'Gagal refresh audit log', 'error');
    }
  }
}
