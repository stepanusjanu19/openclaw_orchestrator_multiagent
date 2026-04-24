// components/AddAgentModal.ts v5
// v5: Grouped model select dengan Cloud/Local tier label

import { api, type DetectedAgent, type CreateAgentPayload, type ModelEntry, type ModelGroups } from '../api/client.js';
import { showToast } from './Toast.js';
import { escapeHTML } from '../utils/dom.js';

// ── Default Templates ─────────────────────────────────────────────────────────
const TEMPLATES: Record<string, (id: string, label: string) => string> = {
  'SOUL.md': (id, label) => `# SOUL.md — ${label}\n\n## Core Truths\n- Saya adalah ${label} (ID: ${id}).\n- Saya membantu dengan responsif dan efektif.\n\n## Vibe\n- Langsung ke inti, tidak berputar-putar.\n- Jujur dan tidak sugarcoat.\n- Hadirkan perspektif yang useful.\n\n## Boundaries\n- Private things stay private.\n- Konfirmasi sebelum aksi eksternal.\n`,
  'IDENTITY.md': (id, label) => `# IDENTITY.md — ${label}\n\n- **Subagent ID:** ${id}\n- **Display Name:** ${label}\n- **Type:** Specialized Subagent\n- **Reports To:** main (Lead Orchestrator)\n- **Emoji:** 🤖\n- **Role:** *(Tentukan keahlian khusus — e.g. Researcher, Coder)*\n\n---\n\n*"Saya bertugas pada domain spesifik saya."*\n`,
  'USER.md': (id, _label) => `# USER.md — Context\n\n- **Human Owner:** *(Inherited from main agent)*\n- **Timezone:** Asia/Jakarta (GMT+7)\n- **Subagent Context:** Melayani request dari Main Agent (${id} → main).\n\n## Cara Menerima Tugas\n\nTugas datang dari Main Agent. Prioritaskan instruksi yang eksplisit.\n`,
  'TOOLS.md': (id, label) => `# TOOLS.md — Local Notes for ${label}\n\n## API Keys / Credentials\n*(Kosong — isi sesuai kebutuhan)*\n\n## Environment Notes\n- Workspace path: ~/.openclaw/workspace-${id}/\n\n## Skill Notes\n*(Catatan skill spesifik domain Anda)*\n`,
  'HEARTBEAT.md': (_id, label) => `# HEARTBEAT.md — ${label}\n\nSubagent heartbeat checklist.\n\n\`\`\`markdown\n# - Cek antrian task dari Main Agent\n# - Verifikasi output terakhir sudah dikirim\n\`\`\`\n`,
  'BOOTSTRAP.md': (_id, label) => `# BOOTSTRAP.md — ${label} Subagent Init\n\n## Langkah Aktivasi\n\n1. Baca **SOUL.md**\n2. Baca **IDENTITY.md**\n3. Baca **AGENTS.md**\n4. Baca **USER.md**\n\n## Saat Selesai\nHapus file ini. Anda sudah aktif.\n`,
  'boot.md': (_id, label) => `// boot.md — ${label}\n// Instruksi saat subagent pertama menyala.\n\ntunggu sebentar\nbaca BOOTSTRAP.md\n`,
};

const WORKSPACE_FILES = ['SOUL.md', 'IDENTITY.md', 'USER.md', 'TOOLS.md', 'HEARTBEAT.md', 'BOOTSTRAP.md', 'boot.md'];

// ── Component ─────────────────────────────────────────────────────────────────
export class AddAgentModal {
  private overlay: HTMLDivElement;
  private onCreated: (agentId: string) => void;
  private loading = false;
  private prefilled: DetectedAgent | null = null;

  // Tab state
  private activeTab: 'basic' | 'workspace' = 'basic';
  private activeFile = 'SOUL.md';
  private fileContents: Map<string, string> = new Map();

  // Element refs
  private modalEl!: HTMLDivElement;
  private idInput!: HTMLInputElement;
  private labelInput!: HTMLInputElement;
  private tokenInput!: HTMLInputElement;
  private modelSelect!: HTMLSelectElement;
  private submitBtn!: HTMLButtonElement;
  private tabBasicEl!: HTMLDivElement;
  private tabWorkspaceEl!: HTMLDivElement;
  private fileListEl!: HTMLUListElement;
  private wsTextareaEl!: HTMLTextAreaElement;
  private tabBasicBtn!: HTMLButtonElement;
  private tabWsBtn!: HTMLButtonElement;
  private modelGroups: ModelGroups = { cloud: [], cloud_proxied: [], local: [] };

  constructor(onCreated: (agentId: string) => void, prefilled?: DetectedAgent) {
    this.onCreated = onCreated;
    this.prefilled = prefilled ?? null;
    this.overlay = document.createElement('div');
    this.overlay.className = 'modal-overlay';
    this.build();
  }

  open() {
    document.body.appendChild(this.overlay);
    setTimeout(() => this.idInput?.focus(), 100);
    // Fetch grouped models setelah modal tampil
    api.getAvailableModels().then(res => {
      this.modelGroups = res.groups || { cloud: [], cloud_proxied: [], local: [] };
      this.populateModelSelect();
    }).catch(() => { /* silent — select tetap bisa digunakan manual */ });
  }

  close() {
    this.overlay.remove();
  }

  private initFileContents(id: string, label: string) {
    for (const file of WORKSPACE_FILES) {
      if (!this.fileContents.has(file)) {
        const tpl = TEMPLATES[file];
        this.fileContents.set(file, tpl ? tpl(id || 'agent', label || 'Agent') : '');
      }
    }
  }

  private build() {
    // ── Modal Container ───────────────────────────────────────────────────────
    this.modalEl = document.createElement('div');
    this.modalEl.className = 'modal add-agent-modal';
    this.modalEl.style.cssText = 'max-width:840px;width:95%;display:flex;flex-direction:column;max-height:90vh;overflow:hidden;';

    // ── Header ────────────────────────────────────────────────────────────────
    const header = document.createElement('div');
    header.className = 'modal-header';
    header.style.cssText = 'flex-shrink:0;';
    header.innerHTML = `
      <div>
        <h2 style="font-size:1.1rem;font-weight:700;">${this.prefilled ? `📥 Import Agent: ${escapeHTML(this.prefilled.id)}` : '🤖 Tambah Agent Baru'}</h2>
        <p style="font-size:0.78rem;color:var(--clr-text-muted);margin-top:2px;">
          ${this.prefilled
            ? `Dari openclaw.json — ${this.prefilled.has_bot_token ? '✅ Token terdeteksi' : '⚠️ Token perlu diisi manual'}`
            : 'Konfigurasi lengkap dengan workspace files dan persona'}
        </p>
      </div>
    `;
    const closeBtn = document.createElement('button');
    closeBtn.type = 'button';
    closeBtn.className = 'modal-close';
    closeBtn.textContent = '✕';
    closeBtn.onclick = () => this.close();
    header.appendChild(closeBtn);

    // ── Tab Bar ───────────────────────────────────────────────────────────────
    const tabBar = document.createElement('div');
    tabBar.style.cssText = 'display:flex;border-bottom:1px solid var(--clr-border);padding:0 20px;background:var(--clr-surface-alt);flex-shrink:0;';

    this.tabBasicBtn = this.makeTabBtn('📋 Info Dasar', true);
    this.tabWsBtn = this.makeTabBtn('🗂️ Workspace Files', false);

    this.tabBasicBtn.onclick = () => this.switchTab('basic');
    this.tabWsBtn.onclick = () => {
      this.initFileContents(this.idInput.value.trim().toLowerCase(), this.labelInput.value.trim());
      this.renderFileList();
      this.loadFileIntoEditor(this.activeFile);
      this.switchTab('workspace');
    };

    tabBar.append(this.tabBasicBtn, this.tabWsBtn);

    // ── Tab: Basic Info (Inside a <form>) ─────────────────────────────────────
    this.tabBasicEl = document.createElement('div');
    this.tabBasicEl.style.cssText = 'display:block;overflow-y:auto;';

    // ── Tab: Workspace Files ──────────────────────────────────────────────────
    this.tabWorkspaceEl = document.createElement('div');
    this.tabWorkspaceEl.style.cssText = 'display:none;height:360px;flex-shrink:0;';

    const wsLayout = document.createElement('div');
    wsLayout.style.cssText = 'display:flex;height:100%;';

    // Sidebar file list
    const wsSidebar = document.createElement('div');
    wsSidebar.style.cssText = 'width:190px;background:var(--clr-surface-alt);border-right:1px solid var(--clr-border);padding:8px 0;overflow-y:auto;flex-shrink:0;';
    const sidebarTitle = document.createElement('div');
    sidebarTitle.textContent = 'WORKSPACE FILES';
    sidebarTitle.style.cssText = 'font-size:0.65rem;font-weight:700;color:var(--clr-text-muted);padding:4px 12px 8px;letter-spacing:1px;';
    this.fileListEl = document.createElement('ul');
    this.fileListEl.style.cssText = 'list-style:none;padding:0;margin:0;';
    wsSidebar.append(sidebarTitle, this.fileListEl);

    // Editor
    const wsEditor = document.createElement('div');
    wsEditor.style.cssText = 'flex:1;display:flex;flex-direction:column;';
    this.wsTextareaEl = document.createElement('textarea');
    this.wsTextareaEl.className = 'persona-editor';
    this.wsTextareaEl.style.cssText = 'flex:1;resize:none;border:none;padding:14px;background:transparent;color:var(--clr-text);font-family:var(--font-mono);font-size:12.5px;line-height:1.6;outline:none;';
    this.wsTextareaEl.spellcheck = false;
    this.wsTextareaEl.addEventListener('input', () => {
      this.fileContents.set(this.activeFile, this.wsTextareaEl.value);
    });
    wsEditor.appendChild(this.wsTextareaEl);

    wsLayout.append(wsSidebar, wsEditor);
    this.tabWorkspaceEl.appendChild(wsLayout);

    // ── Footer + Submit (will be INSIDE the form) ─────────────────────────────
    const footer = document.createElement('div');
    footer.style.cssText = 'display:flex;gap:10px;justify-content:flex-end;padding:16px 20px;border-top:1px solid var(--clr-border);background:var(--clr-surface-alt);flex-shrink:0;';

    const cancelBtn = document.createElement('button');
    cancelBtn.type = 'button';
    cancelBtn.className = 'btn btn-ghost';
    cancelBtn.textContent = 'Batal';
    cancelBtn.onclick = () => this.close();

    this.submitBtn = document.createElement('button');
    this.submitBtn.type = 'button'; // ← type="button" to avoid issues, we handle click manually
    this.submitBtn.className = 'btn btn-primary';
    this.submitBtn.innerHTML = this.prefilled ? '📥 Import Agent' : '➕ Buat Agent';
    this.submitBtn.onclick = () => this.handleSubmit();

    footer.append(cancelBtn, this.submitBtn);

    // ── Build Basic Tab Content ───────────────────────────────────────────────
    let importBanner = '';
    if (this.prefilled?.has_bot_token) {
      importBanner = `<div style="background:rgba(34,211,165,0.08);border:1px solid rgba(34,211,165,0.2);border-radius:var(--radius-sm);padding:10px 14px;font-size:0.8rem;color:var(--clr-success);margin-bottom:16px">
        ✅ Bot token berhasil dibaca dari openclaw.json.
      </div>`;
    }

    const basicContent = document.createElement('div');
    basicContent.style.cssText = 'padding:20px;display:flex;flex-direction:column;gap:16px;';
    basicContent.innerHTML = `
      ${importBanner}
      <div class="form-grid">
        <div class="form-group">
          <label class="form-label" for="aa-id">Agent ID <span style="color:var(--clr-danger)">*</span></label>
          <input class="form-input" id="aa-id" type="text" placeholder="e.g. assistant, dev, alpha"
            value="${escapeHTML(this.prefilled?.id ?? '')}"
            ${this.prefilled ? 'readonly style="opacity:0.6"' : ''}
            pattern="[a-z0-9_\\-]+" title="Huruf kecil, angka, - dan _" required />
          <span class="form-help">Lowercase, a-z 0-9 - _ (max 32)</span>
        </div>
        <div class="form-group">
          <label class="form-label" for="aa-label">Nama Tampilan <span style="color:var(--clr-danger)">*</span></label>
          <input class="form-input" id="aa-label" type="text" placeholder="e.g. Main Assistant"
            value="${escapeHTML(this.prefilled ? this.prefilled.id.charAt(0).toUpperCase() + this.prefilled.id.slice(1) + ' Agent' : '')}"
            required />
        </div>
      </div>
      <div class="form-group">
        <label class="form-label" for="aa-token">Telegram Bot Token <span style="color:var(--clr-danger)">*</span></label>
        <input class="form-input font-mono" id="aa-token" type="password"
          placeholder="123456789:ABCdef..."
          value="${this.prefilled?.has_bot_token ? this.prefilled.bot_token : ''}" />
        <span class="form-help">Dari @BotFather → /newbot. Setiap agent harus punya bot sendiri.</span>
      </div>
      <div class="form-group">
        <label class="form-label" for="aa-model">OpenClaw Model</label>
        <select class="form-input font-mono" id="aa-model-select" style="height:38px;">
          <option value="">— Auto-generate dari Agent ID —</option>
        </select>
        <span class="form-help">Model dikelompokkan berdasarkan tier. Kosongkan untuk auto-generate.</span>
      </div>
      <div style="background:rgba(59,130,246,0.08);border:1px solid rgba(59,130,246,0.2);border-radius:var(--radius-sm);padding:10px 14px;font-size:0.8rem;color:var(--clr-primary);">
        💡 Klik tab <b>🗂️ Workspace Files</b> untuk mengatur SOUL.md, IDENTITY.md dan file lainnya.
      </div>
    `;
    this.tabBasicEl.appendChild(basicContent);

    // Store element refs
    this.idInput    = this.tabBasicEl.querySelector<HTMLInputElement>('#aa-id')!;
    this.labelInput = this.tabBasicEl.querySelector<HTMLInputElement>('#aa-label')!;
    this.tokenInput = this.tabBasicEl.querySelector<HTMLInputElement>('#aa-token')!;
    this.modelSelect = this.tabBasicEl.querySelector<HTMLSelectElement>('#aa-model-select')!;

    // ── Assemble Modal ────────────────────────────────────────────────────────
    this.modalEl.append(header, tabBar, this.tabBasicEl, this.tabWorkspaceEl, footer);
    this.overlay.appendChild(this.modalEl);

    // Close on overlay click
    this.overlay.addEventListener('click', (e) => {
      if (e.target === this.overlay) this.close();
    });
  }

  private async handleSubmit() {
    if (this.loading) return;

    const id    = this.idInput.value.trim().toLowerCase();
    const label = this.labelInput.value.trim();
    const token = this.tokenInput.value.trim();
    const model = this.modelSelect ? this.modelSelect.value.trim() : '';

    if (!id) { showToast('Agent ID wajib diisi', 'error'); this.idInput.focus(); return; }
    if (!label) { showToast('Nama tampilan wajib diisi', 'error'); this.labelInput.focus(); return; }
    if (!token) { showToast('Telegram Bot Token wajib diisi', 'error'); this.tokenInput.focus(); return; }
    if (!/^[a-z0-9_-]+$/.test(id)) { showToast('ID hanya boleh: huruf kecil, angka, - dan _', 'error'); this.idInput.focus(); return; }

    // Sync text in active workspace file if on that tab
    if (this.activeTab === 'workspace') {
      this.fileContents.set(this.activeFile, this.wsTextareaEl.value);
    }

    // Ensure templates are populated before sending
    this.initFileContents(id, label);

    this.loading = true;
    this.submitBtn.disabled = true;
    this.submitBtn.innerHTML = '<span class="loading-spinner"></span> Membuat...';

    const payload: CreateAgentPayload = {
      id,
      label,
      telegram_bot_token: token,
      openclaw_model: model || undefined,
      persona: this.fileContents.get('SOUL.md') || undefined,
      workspace_files: Object.fromEntries(
        WORKSPACE_FILES.filter(f => f !== 'SOUL.md').map(f => [f, this.fileContents.get(f) ?? ''])
      ),
    };

    try {
      const res = await api.createAgent(payload);
      showToast(`✅ Agent "${label}" berhasil dibuat!`, 'success');
      this.onCreated(res.agent.id);
      this.close();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      showToast(`Gagal buat agent: ${msg}`, 'error');
      this.loading = false;
      this.submitBtn.disabled = false;
      this.submitBtn.innerHTML = this.prefilled ? '📥 Import Agent' : '➕ Buat Agent';
    }
  }

  private populateModelSelect() {
    if (!this.modelSelect) return;
    // Keep first default option
    this.modelSelect.innerHTML = '<option value="">— Auto-generate dari Agent ID —</option>';

    const buildGroup = (label: string, models: ModelEntry[]) => {
      if (!models || models.length === 0) return null;
      const grp = document.createElement('optgroup');
      grp.label = label;
      models.forEach(m => {
        const opt = document.createElement('option');
        opt.value = m.id;
        const keyIcon = m.has_api_key ? ' 🔑' : '';
        opt.textContent = `${m.label}${keyIcon}  [${m.provider}]`;
        grp.appendChild(opt);
      });
      return grp;
    };

    const g = this.modelGroups;
    [buildGroup('☁️ Cloud', g.cloud),
     buildGroup('☁️ Cloud Proxied (via local bridge)', g.cloud_proxied),
     buildGroup('🖥️ Local', g.local)
    ].filter(Boolean).forEach(grp => this.modelSelect.appendChild(grp!));

    // Pre-select prefilled model if available
    const preselect = this.prefilled ? 'openclaw/' + this.prefilled.id : '';
    if (preselect) {
      const opt = Array.from(this.modelSelect.options).find(o => o.value === preselect);
      if (opt) this.modelSelect.value = preselect;
    }
  }

  private makeTabBtn(text: string, active: boolean): HTMLButtonElement {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.textContent = text;
    btn.style.cssText = `
      padding:11px 18px;border:none;background:transparent;cursor:pointer;
      font-size:0.875rem;font-weight:600;
      border-bottom:2px solid ${active ? 'var(--clr-primary)' : 'transparent'};
      color:${active ? 'var(--clr-primary)' : 'var(--clr-text-muted)'};
      transition:all 0.15s;
    `;
    return btn;
  }

  private switchTab(tab: 'basic' | 'workspace') {
    this.activeTab = tab;

    const isBasic = tab === 'basic';
    this.tabBasicBtn.style.borderBottomColor = isBasic ? 'var(--clr-primary)' : 'transparent';
    this.tabBasicBtn.style.color = isBasic ? 'var(--clr-primary)' : 'var(--clr-text-muted)';
    this.tabWsBtn.style.borderBottomColor = !isBasic ? 'var(--clr-primary)' : 'transparent';
    this.tabWsBtn.style.color = !isBasic ? 'var(--clr-primary)' : 'var(--clr-text-muted)';

    this.tabBasicEl.style.display = isBasic ? 'block' : 'none';
    this.tabWorkspaceEl.style.display = isBasic ? 'none' : 'flex';
  }

  private renderFileList() {
    this.fileListEl.innerHTML = '';
    for (const file of WORKSPACE_FILES) {
      const li = document.createElement('li');
      const isActive = file === this.activeFile;
      li.style.cssText = `
        padding:8px 12px;cursor:pointer;display:flex;align-items:center;gap:8px;
        font-size:0.78rem;font-family:var(--font-mono);
        border-left:3px solid ${isActive ? 'var(--clr-primary)' : 'transparent'};
        background:${isActive ? 'rgba(59,130,246,0.12)' : 'transparent'};
        color:${isActive ? 'var(--clr-primary)' : 'var(--clr-text)'};
        transition:background 0.12s;
      `;
      li.innerHTML = `<span>📄</span><span>${file}</span>`;
      li.addEventListener('mouseenter', () => { if (!isActive) li.style.background = 'rgba(255,255,255,0.04)'; });
      li.addEventListener('mouseleave', () => { if (!isActive) li.style.background = 'transparent'; });
      li.addEventListener('click', () => {
        this.fileContents.set(this.activeFile, this.wsTextareaEl.value);
        this.activeFile = file;
        this.loadFileIntoEditor(file);
        this.renderFileList();
      });
      this.fileListEl.appendChild(li);
    }
  }

  private loadFileIntoEditor(file: string) {
    const content = this.fileContents.get(file) ?? '';
    this.wsTextareaEl.value = content;
    this.wsTextareaEl.focus();
  }
}
