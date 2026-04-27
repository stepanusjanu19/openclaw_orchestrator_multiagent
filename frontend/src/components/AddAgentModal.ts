// components/AddAgentModal.ts
// Add/import agent with backend-rendered OpenClaw workspace template profiles.

import { api, type AgentTemplateCatalog, type DetectedAgent, type CreateAgentPayload, type ModelGroups } from '../api/client.js';
import { ModelPicker } from './ModelPicker.js';
import { showToast } from './Toast.js';
import { flattenModelGroups, modelRouteMetaFromEntry, routerModelsToEntries } from '../utils/modelRuntime.js';

const FALLBACK_CATALOG: AgentTemplateCatalog = {
  default_role_profile: 'generalist',
  default_tone_profile: 'direct',
  files: ['SOUL.md', 'AGENTS.md', 'IDENTITY.md', 'USER.md', 'TOOLS.md', 'HEARTBEAT.md', 'BOOTSTRAP.md', 'boot.md'],
  role_profiles: [
    { id: 'generalist', label: 'Generalist', description: 'Balanced assistant for broad delegated tasks.' },
    { id: 'researcher', label: 'Researcher', description: 'Investigates facts, sources, and tradeoffs.' },
    { id: 'coder', label: 'Coder', description: 'Implements focused code changes and verification.' },
    { id: 'analyst', label: 'Analyst', description: 'Models systems, data, and failure modes.' },
    { id: 'support', label: 'Support', description: 'Handles operational help and user-facing triage.' },
    { id: 'creative', label: 'Creative', description: 'Generates ideas, copy, and expressive variants.' },
    { id: 'guardian', label: 'Guardian', description: 'Reviews safety, security, and policy risk.' },
    { id: 'orchestrator-helper', label: 'Orchestrator Helper', description: 'Plans delegation and coordinates outputs.' },
  ],
  tone_profiles: [
    { id: 'direct', label: 'Direct', description: 'Clear, concise, and low ceremony.' },
    { id: 'warm', label: 'Warm', description: 'Friendly, patient, and still concise.' },
    { id: 'formal', label: 'Formal', description: 'Structured, precise, and professional.' },
    { id: 'casual', label: 'Casual', description: 'Conversational and lightweight.' },
    { id: 'strict', label: 'Strict', description: 'Conservative, boundary-focused, and careful.' },
    { id: 'playful', label: 'Playful', description: 'Lighter tone for creative or informal work.' },
  ],
};

export class AddAgentModal {
  private overlay: HTMLDivElement;
  private onCreated: (agentId: string) => void;
  private loading = false;
  private prefilled: DetectedAgent | null = null;

  private activeTab: 'basic' | 'workspace' = 'basic';
  private activeFile = 'SOUL.md';
  private fileContents: Map<string, string> = new Map();
  private modifiedFiles: Set<string> = new Set();
  private workspaceFiles = [...FALLBACK_CATALOG.files];
  private templateCatalog: AgentTemplateCatalog = FALLBACK_CATALOG;
  private applyingPreview = false;
  private previewTimer: number | undefined;
  private previewSeq = 0;

  private modalEl!: HTMLDivElement;
  private idInput!: HTMLInputElement;
  private labelInput!: HTMLInputElement;
  private tokenInput!: HTMLInputElement;
  private roleSelect!: HTMLSelectElement;
  private toneSelect!: HTMLSelectElement;
  private modelPicker!: ModelPicker;
  private submitBtn!: HTMLButtonElement;
  private tabBasicEl!: HTMLDivElement;
  private tabWorkspaceEl!: HTMLDivElement;
  private fileListEl!: HTMLUListElement;
  private wsTextareaEl!: HTMLTextAreaElement;
  private workspaceStatusEl!: HTMLSpanElement;
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
    this.loadModelOptions();
    this.loadTemplateCatalog();
  }

  close() {
    if (this.previewTimer !== undefined) window.clearTimeout(this.previewTimer);
    this.overlay.remove();
  }

  private build() {
    this.modalEl = document.createElement('div');
    this.modalEl.className = 'modal add-agent-modal';
    this.modalEl.style.cssText = 'display:flex;flex-direction:column;overflow:hidden;';

    const header = document.createElement('div');
    header.className = 'modal-header';
    header.style.cssText = 'flex-shrink:0;';
    const headerText = document.createElement('div');
    const title = document.createElement('h2');
    title.className = 'modal-title';
    title.textContent = this.prefilled ? `Import Agent: ${this.prefilled.id}` : 'Tambah Agent Baru';
    const subtitle = document.createElement('p');
    subtitle.className = 'modal-subtitle';
    subtitle.textContent = this.prefilled
      ? `Dari openclaw.json - ${this.prefilled.has_bot_token ? 'token terdeteksi' : 'token perlu diisi manual'}`
      : 'Konfigurasi agent dengan template workspace OpenClaw dinamis';
    headerText.append(title, subtitle);
    header.appendChild(headerText);
    const closeBtn = document.createElement('button');
    closeBtn.type = 'button';
    closeBtn.className = 'modal-close';
    closeBtn.textContent = 'x';
    closeBtn.onclick = () => this.close();
    header.appendChild(closeBtn);

    const tabBar = document.createElement('div');
    tabBar.style.cssText = 'display:flex;border-bottom:1px solid var(--clr-border);padding:0 20px;background:var(--clr-surface-alt);flex-shrink:0;';
    this.tabBasicBtn = this.makeTabBtn('Info Dasar', true);
    this.tabWsBtn = this.makeTabBtn('Workspace Files', false);
    this.tabBasicBtn.onclick = () => this.switchTab('basic');
    this.tabWsBtn.onclick = () => {
      this.switchTab('workspace');
      this.loadTemplatePreview(false);
    };
    tabBar.append(this.tabBasicBtn, this.tabWsBtn);

    this.tabBasicEl = document.createElement('div');
    this.tabBasicEl.style.cssText = 'display:block;overflow-y:auto;';
    this.buildBasicTab();

    this.tabWorkspaceEl = document.createElement('div');
    this.tabWorkspaceEl.className = 'add-agent-workspace-tab';
    this.tabWorkspaceEl.style.cssText = 'display:none;';
    this.buildWorkspaceTab();

    const footer = document.createElement('div');
    footer.style.cssText = 'display:flex;gap:10px;justify-content:flex-end;padding:16px 20px;border-top:1px solid var(--clr-border);background:var(--clr-surface-alt);flex-shrink:0;';
    const cancelBtn = document.createElement('button');
    cancelBtn.type = 'button';
    cancelBtn.className = 'btn btn-ghost';
    cancelBtn.textContent = 'Batal';
    cancelBtn.onclick = () => this.close();
    this.submitBtn = document.createElement('button');
    this.submitBtn.type = 'button';
    this.submitBtn.className = 'btn btn-primary';
    this.submitBtn.textContent = this.prefilled ? 'Import Agent' : 'Buat Agent';
    this.submitBtn.onclick = () => this.handleSubmit();
    footer.append(cancelBtn, this.submitBtn);

    this.modalEl.append(header, tabBar, this.tabBasicEl, this.tabWorkspaceEl, footer);
    this.overlay.appendChild(this.modalEl);
    this.overlay.addEventListener('click', (e) => {
      if (e.target === this.overlay) this.close();
    });
  }

  private buildBasicTab() {
    let importBanner = '';
    if (this.prefilled?.has_bot_token) {
      importBanner = `<div style="background:rgba(34,211,165,0.08);border:1px solid rgba(34,211,165,0.2);border-radius:var(--radius-sm);padding:10px 14px;font-size:0.8rem;color:var(--clr-success);margin-bottom:16px">
        Bot token berhasil dibaca dari openclaw.json.
      </div>`;
    }

    const basicContent = document.createElement('div');
    basicContent.className = 'add-agent-basic-content';
    basicContent.style.cssText = 'padding:20px;display:flex;flex-direction:column;gap:16px;';
    basicContent.innerHTML = `
      ${importBanner}
      <div class="form-grid">
        <div class="form-group">
          <label class="form-label" for="aa-id">Agent ID <span style="color:var(--clr-danger)">*</span></label>
          <input class="form-input" id="aa-id" type="text" placeholder="e.g. assistant, dev, alpha"
            pattern="[a-z0-9_\\-]+" title="Huruf kecil, angka, - dan _" required />
          <span class="form-help">Lowercase, a-z 0-9 - _ (max 32)</span>
        </div>
        <div class="form-group">
          <label class="form-label" for="aa-label">Nama Tampilan <span style="color:var(--clr-danger)">*</span></label>
          <input class="form-input" id="aa-label" type="text" placeholder="e.g. Main Assistant"
            required />
        </div>
      </div>
      <div class="form-grid">
        <div class="form-group">
          <label class="form-label" for="aa-role">Role Profile</label>
          <select class="form-input" id="aa-role"></select>
          <span class="form-help" id="aa-role-help">Backend canonical template profile.</span>
        </div>
        <div class="form-group">
          <label class="form-label" for="aa-tone">Tone Profile</label>
          <select class="form-input" id="aa-tone"></select>
          <span class="form-help" id="aa-tone-help">Gaya komunikasi untuk workspace files.</span>
        </div>
      </div>
      <div class="form-group">
        <label class="form-label" for="aa-token">Telegram Bot Token <span style="color:var(--clr-danger)">*</span></label>
        <input class="form-input font-mono" id="aa-token" type="password"
          placeholder="123456789:ABCdef..." />
        <span class="form-help">Dari @BotFather -> /newbot. Setiap agent harus punya bot sendiri.</span>
      </div>
      <div class="form-group">
        <label class="form-label" for="aa-model">OpenClaw Model</label>
        <div id="aa-model-picker"></div>
      </div>
      <div style="background:rgba(59,130,246,0.08);border:1px solid rgba(59,130,246,0.2);border-radius:var(--radius-sm);padding:10px 14px;font-size:0.8rem;color:var(--clr-primary);">
        Workspace Files dirender dari backend. Edit manual di tab Workspace Files akan dipertahankan saat profile berubah.
      </div>
    `;
    this.tabBasicEl.appendChild(basicContent);

    this.idInput = this.tabBasicEl.querySelector<HTMLInputElement>('#aa-id')!;
    this.labelInput = this.tabBasicEl.querySelector<HTMLInputElement>('#aa-label')!;
    this.tokenInput = this.tabBasicEl.querySelector<HTMLInputElement>('#aa-token')!;
    this.roleSelect = this.tabBasicEl.querySelector<HTMLSelectElement>('#aa-role')!;
    this.toneSelect = this.tabBasicEl.querySelector<HTMLSelectElement>('#aa-tone')!;
    this.populateTemplateSelectors();

    this.idInput.value = this.prefilled?.id ?? '';
    if (this.prefilled) {
      this.idInput.readOnly = true;
      this.idInput.style.opacity = '0.6';
      this.labelInput.value = `${this.prefilled.id.charAt(0).toUpperCase()}${this.prefilled.id.slice(1)} Agent`;
    }
    if (this.prefilled?.has_bot_token) {
      this.tokenInput.value = this.prefilled.bot_token || '';
    }

    const modelPickerMount = this.tabBasicEl.querySelector<HTMLDivElement>('#aa-model-picker')!;
    this.modelPicker = new ModelPicker({
      currentModel: this.prefilled ? `openclaw/${this.prefilled.id}` : '',
      manualPlaceholder: 'Kosongkan untuk auto-generate, atau isi provider/model-id',
      emptyLabel: 'Kosongkan untuk auto-generate dari Agent ID. Pilih dari katalog atau isi manual.',
    });
    modelPickerMount.appendChild(this.modelPicker.getElement());

    this.idInput.addEventListener('input', () => this.scheduleTemplatePreview());
    this.labelInput.addEventListener('input', () => this.scheduleTemplatePreview());
    this.roleSelect.addEventListener('change', () => this.scheduleTemplatePreview());
    this.toneSelect.addEventListener('change', () => this.scheduleTemplatePreview());
  }

  private buildWorkspaceTab() {
    const wsLayout = document.createElement('div');
    wsLayout.className = 'add-agent-workspace-layout';
    wsLayout.style.cssText = 'display:flex;height:100%;';

    const wsSidebar = document.createElement('div');
    wsSidebar.className = 'add-agent-workspace-sidebar';
    wsSidebar.style.cssText = 'background:var(--clr-surface-alt);border-right:1px solid var(--clr-border);padding:8px 0;overflow-y:auto;flex-shrink:0;';
    const sidebarTitle = document.createElement('div');
    sidebarTitle.textContent = 'WORKSPACE FILES';
    sidebarTitle.style.cssText = 'font-size:0.65rem;font-weight:700;color:var(--clr-text-muted);padding:4px 12px 8px;letter-spacing:1px;';
    this.fileListEl = document.createElement('ul');
    this.fileListEl.style.cssText = 'list-style:none;padding:0;margin:0;';
    wsSidebar.append(sidebarTitle, this.fileListEl);

    const wsEditor = document.createElement('div');
    wsEditor.style.cssText = 'flex:1;min-width:0;display:flex;flex-direction:column;';
    const editorBar = document.createElement('div');
    editorBar.style.cssText = 'display:flex;align-items:center;justify-content:space-between;gap:12px;padding:8px 12px;border-bottom:1px solid var(--clr-border);background:var(--clr-surface-alt);';
    this.workspaceStatusEl = document.createElement('span');
    this.workspaceStatusEl.style.cssText = 'font-size:0.75rem;color:var(--clr-text-muted);';
    this.workspaceStatusEl.textContent = 'Preview belum dimuat';
    const resetBtn = document.createElement('button');
    resetBtn.type = 'button';
    resetBtn.className = 'btn btn-ghost btn-sm';
    resetBtn.textContent = 'Reset all from template';
    resetBtn.onclick = () => this.loadTemplatePreview(true);
    editorBar.append(this.workspaceStatusEl, resetBtn);

    this.wsTextareaEl = document.createElement('textarea');
    this.wsTextareaEl.className = 'persona-editor add-agent-workspace-editor';
    this.wsTextareaEl.style.cssText = 'flex:1;resize:none;border:none;background:transparent;color:var(--clr-text);font-family:var(--font-mono);line-height:1.6;outline:none;';
    this.wsTextareaEl.spellcheck = false;
    this.wsTextareaEl.addEventListener('input', () => {
      if (this.applyingPreview) return;
      this.fileContents.set(this.activeFile, this.wsTextareaEl.value);
      this.modifiedFiles.add(this.activeFile);
      this.renderFileList();
      this.updateWorkspaceStatus();
    });
    wsEditor.append(editorBar, this.wsTextareaEl);
    wsLayout.append(wsSidebar, wsEditor);
    this.tabWorkspaceEl.appendChild(wsLayout);
    this.renderFileList();
  }

  private async handleSubmit() {
    if (this.loading) return;

    const id = this.idInput.value.trim().toLowerCase();
    const label = this.labelInput.value.trim();
    const token = this.tokenInput.value.trim();
    const model = this.modelPicker ? this.modelPicker.getValue().trim() : '';

    if (!id) { showToast('Agent ID wajib diisi', 'error'); this.idInput.focus(); return; }
    if (!label) { showToast('Nama tampilan wajib diisi', 'error'); this.labelInput.focus(); return; }
    if (!token) { showToast('Telegram Bot Token wajib diisi', 'error'); this.tokenInput.focus(); return; }
    if (!/^[a-z0-9_-]+$/.test(id)) { showToast('ID hanya boleh: huruf kecil, angka, - dan _', 'error'); this.idInput.focus(); return; }

    this.syncActiveWorkspaceFile();
    if (this.fileContents.size === 0) {
      await this.loadTemplatePreview(false);
    }

    this.loading = true;
    this.submitBtn.disabled = true;
    this.submitBtn.innerHTML = '<span class="loading-spinner"></span> Membuat...';

    const shouldSendWorkspaceFiles = this.modifiedFiles.size > 0;
    const workspaceFiles = shouldSendWorkspaceFiles
      ? Object.fromEntries([...this.modifiedFiles].map(file => [file, this.fileContents.get(file) ?? '']))
      : undefined;
    const payload: CreateAgentPayload = {
      id,
      label,
      telegram_bot_token: token,
      openclaw_model: model || undefined,
      template_profile: this.roleSelect.value || undefined,
      tone_profile: this.toneSelect.value || undefined,
      model_route: modelRouteMetaFromEntry(this.modelPicker?.getSelectedModel()),
      persona: this.modifiedFiles.has('SOUL.md') ? (this.fileContents.get('SOUL.md') || undefined) : undefined,
      workspace_files: workspaceFiles,
    };

    try {
      const res = await api.createAgent(payload);
      showToast(`Agent "${label}" berhasil dibuat!`, 'success');
      this.onCreated(res.agent.id);
      this.close();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      showToast(`Gagal buat agent: ${msg}`, 'error');
      this.loading = false;
      this.submitBtn.disabled = false;
      this.submitBtn.textContent = this.prefilled ? 'Import Agent' : 'Buat Agent';
    }
  }

  private async loadTemplateCatalog() {
    try {
      this.templateCatalog = await api.getAgentTemplates();
    } catch {
      this.templateCatalog = FALLBACK_CATALOG;
      showToast('Gagal load katalog template dari backend; memakai pilihan fallback UI.', 'info');
    }
    this.workspaceFiles = this.templateCatalog.files.length ? [...this.templateCatalog.files] : [...FALLBACK_CATALOG.files];
    if (!this.workspaceFiles.includes(this.activeFile)) this.activeFile = this.workspaceFiles[0] || 'SOUL.md';
    this.populateTemplateSelectors();
    this.renderFileList();
    this.scheduleTemplatePreview(0);
  }

  private populateTemplateSelectors() {
    if (!this.roleSelect || !this.toneSelect) return;
    this.fillSelect(this.roleSelect, this.templateCatalog.role_profiles, this.templateCatalog.default_role_profile);
    this.fillSelect(this.toneSelect, this.templateCatalog.tone_profiles, this.templateCatalog.default_tone_profile);
    this.updateTemplateHelp();
  }

  private fillSelect(select: HTMLSelectElement, profiles: AgentTemplateCatalog['role_profiles'], selectedID: string) {
    const current = select.value || selectedID;
    select.replaceChildren();
    for (const profile of profiles) {
      const opt = document.createElement('option');
      opt.value = profile.id;
      opt.textContent = profile.label;
      opt.title = profile.description;
      select.appendChild(opt);
    }
    select.value = profiles.some(p => p.id === current) ? current : selectedID;
  }

  private updateTemplateHelp() {
    const role = this.templateCatalog.role_profiles.find(p => p.id === this.roleSelect.value);
    const tone = this.templateCatalog.tone_profiles.find(p => p.id === this.toneSelect.value);
    const roleHelp = this.tabBasicEl.querySelector<HTMLSpanElement>('#aa-role-help');
    const toneHelp = this.tabBasicEl.querySelector<HTMLSpanElement>('#aa-tone-help');
    if (roleHelp) roleHelp.textContent = role?.description || 'Backend canonical template profile.';
    if (toneHelp) toneHelp.textContent = tone?.description || 'Gaya komunikasi untuk workspace files.';
  }

  private scheduleTemplatePreview(delay = 350) {
    this.updateTemplateHelp();
    if (this.previewTimer !== undefined) window.clearTimeout(this.previewTimer);
    this.previewTimer = window.setTimeout(() => this.loadTemplatePreview(false), delay);
  }

  private async loadTemplatePreview(forceReset: boolean) {
    const id = this.idInput.value.trim().toLowerCase() || 'agent';
    const label = this.labelInput.value.trim() || id;
    if (!/^[a-z0-9_-]+$/.test(id)) {
      this.workspaceStatusEl.textContent = 'Preview menunggu Agent ID valid';
      return;
    }

    this.syncActiveWorkspaceFile();
    if (forceReset) this.modifiedFiles.clear();
    const overrides = forceReset ? {} : Object.fromEntries(
      [...this.modifiedFiles].map(file => [file, this.fileContents.get(file) ?? ''])
    );

    const seq = ++this.previewSeq;
    this.workspaceStatusEl.textContent = 'Memuat preview template...';
    try {
      const preview = await api.previewAgentTemplate({
        id,
        label,
        template_profile: this.roleSelect.value || undefined,
        tone_profile: this.toneSelect.value || undefined,
        workspace_files: overrides,
      });
      if (seq !== this.previewSeq) return;
      this.workspaceFiles = preview.files.length ? preview.files : this.workspaceFiles;
      this.applyingPreview = true;
      for (const file of this.workspaceFiles) {
        if (forceReset || !this.modifiedFiles.has(file) || !this.fileContents.has(file)) {
          this.fileContents.set(file, preview.workspace_files[file] ?? '');
        }
      }
      if (!this.workspaceFiles.includes(this.activeFile)) this.activeFile = this.workspaceFiles[0] || 'SOUL.md';
      this.loadFileIntoEditor(this.activeFile);
      this.applyingPreview = false;
      this.renderFileList();
      this.updateWorkspaceStatus();
    } catch (err) {
      this.applyingPreview = false;
      const msg = err instanceof Error ? err.message : String(err);
      this.workspaceStatusEl.textContent = `Preview gagal: ${msg}`;
    }
  }

  private async loadModelOptions() {
    try {
      const res = await api.getAvailableModels();
      this.modelGroups = res.groups || { cloud: [], cloud_proxied: [], local: [] };
      const routerStatus = await api.getRouterStatus().catch(() => null);
      this.modelPicker?.setModels([
        ...flattenModelGroups(this.modelGroups),
        ...routerModelsToEntries(routerStatus),
      ]);
    } catch {
      /* select manual tetap bisa digunakan */
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
    this.tabWorkspaceEl.style.display = isBasic ? 'none' : 'block';
    if (!isBasic) {
      this.renderFileList();
      this.loadFileIntoEditor(this.activeFile);
      this.updateWorkspaceStatus();
    }
  }

  private renderFileList() {
    this.fileListEl.innerHTML = '';
    for (const file of this.workspaceFiles) {
      const li = document.createElement('li');
      const isActive = file === this.activeFile;
      li.style.cssText = `
        padding:8px 12px;cursor:pointer;display:flex;align-items:center;justify-content:space-between;gap:8px;
        font-size:0.8rem;font-family:var(--font-mono);
        border-left:3px solid ${isActive ? 'var(--clr-primary)' : 'transparent'};
        background:${isActive ? 'rgba(59,130,246,0.12)' : 'transparent'};
        color:${isActive ? 'var(--clr-primary)' : 'var(--clr-text)'};
        transition:background 0.12s;
      `;
      const name = document.createElement('span');
      name.textContent = file;
      li.appendChild(name);
      if (this.modifiedFiles.has(file)) {
        const badge = document.createElement('span');
        badge.textContent = 'modified';
        badge.style.cssText = 'font-size:0.65rem;color:var(--clr-warning);';
        li.appendChild(badge);
      }
      li.addEventListener('mouseenter', () => { if (!isActive) li.style.background = 'rgba(255,255,255,0.04)'; });
      li.addEventListener('mouseleave', () => { if (!isActive) li.style.background = 'transparent'; });
      li.addEventListener('click', () => {
        this.syncActiveWorkspaceFile();
        this.activeFile = file;
        this.loadFileIntoEditor(file);
        this.renderFileList();
      });
      this.fileListEl.appendChild(li);
    }
  }

  private syncActiveWorkspaceFile() {
    if (this.activeTab === 'workspace' && this.wsTextareaEl) {
      this.fileContents.set(this.activeFile, this.wsTextareaEl.value);
    }
  }

  private loadFileIntoEditor(file: string) {
    const content = this.fileContents.get(file) ?? '';
    this.applyingPreview = true;
    this.wsTextareaEl.value = content;
    this.applyingPreview = false;
    if (this.activeTab === 'workspace') this.wsTextareaEl.focus();
  }

  private updateWorkspaceStatus() {
    if (!this.workspaceStatusEl) return;
    const modified = this.modifiedFiles.size;
    const role = this.roleSelect?.value || this.templateCatalog.default_role_profile;
    const tone = this.toneSelect?.value || this.templateCatalog.default_tone_profile;
    this.workspaceStatusEl.textContent = `Template ${role}/${tone} - ${this.workspaceFiles.length} files${modified ? ` - ${modified} modified` : ''}`;
  }
}
