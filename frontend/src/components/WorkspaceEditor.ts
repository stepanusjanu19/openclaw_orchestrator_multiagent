// components/WorkspaceEditor.ts
// Dual-pane UI to manage all .md files in the agent's workspace.

import { api } from '../api/client.js';
import { showToast } from './Toast.js';

export class WorkspaceEditor {
  private agentId: string;
  private label: string;
  private overlay: HTMLDivElement;
  
  private files: string[] = [];
  private currentFile: string = '';
  private fileContents: Map<string, string> = new Map();
  private originalContents: Map<string, string> = new Map();

  private fileListEl!: HTMLUListElement;
  private textareaEl!: HTMLTextAreaElement;
  private saveBtn!: HTMLButtonElement;
  private loadingEl!: HTMLDivElement;
  private headerTitle!: HTMLHeadingElement;
  
  private loading = false;

  constructor(agentId: string, label: string) {
    this.agentId = agentId;
    this.label = label;
    
    this.overlay = document.createElement('div');
    this.overlay.className = 'modal-overlay';
    this.build();
  }

  open() {
    document.body.appendChild(this.overlay);
    this.loadWorkspaceFiles();
  }

  close() {
    // Check if there are unsaved changes
    if (this.hasUnsavedChanges()) {
      if (!confirm('Terdapat perubahan yang belum disimpan. Yakin ingin menutup?')) {
        return;
      }
    }
    this.overlay.remove();
  }

  private hasUnsavedChanges(): boolean {
    if (this.currentFile && this.textareaEl) {
      // Sync current textarea to map
      this.fileContents.set(this.currentFile, this.textareaEl.value);
    }
    for (const file of this.files) {
      if (this.fileContents.get(file) !== this.originalContents.get(file)) {
        return true;
      }
    }
    return false;
  }

  private build() {
    const modal = document.createElement('div');
    modal.className = 'modal workspace-modal';
    // Gunakan width lebih besar karena layout tipe dual-pane (sidebar)
    modal.style.maxWidth = '900px';
    modal.style.width = '90%';
    modal.style.height = '80vh';
    modal.style.display = 'flex';
    modal.style.flexDirection = 'column';

    // Header
    const header = document.createElement('div');
    header.className = 'modal-header';
    
    this.headerTitle = document.createElement('h2');
    this.headerTitle.textContent = `🗂️ Workspace: ${this.label} `;
    const subtitle = document.createElement('span');
    subtitle.style.fontSize = '0.7em';
    subtitle.style.opacity = '0.6';
    subtitle.style.fontFamily = 'monospace';
    subtitle.textContent = `(${this.agentId})`;
    this.headerTitle.appendChild(subtitle);
    
    const closeBtn = document.createElement('button');
    closeBtn.className = 'modal-close';
    closeBtn.textContent = '✕';
    closeBtn.onclick = () => this.close();
    
    header.append(this.headerTitle, closeBtn);

    // Main Content Area (Dual Pane)
    const contentArea = document.createElement('div');
    contentArea.style.display = 'flex';
    contentArea.style.flex = '1';
    contentArea.style.overflow = 'hidden';
    contentArea.style.borderTop = '1px solid var(--clr-border)';

    // Left Sidebar (File List)
    const sidebar = document.createElement('div');
    sidebar.style.width = '240px';
    sidebar.style.background = 'var(--clr-surface-alt)';
    sidebar.style.borderRight = '1px solid var(--clr-border)';
    sidebar.style.overflowY = 'auto';
    sidebar.style.padding = '12px 0';

    const sidebarTitle = document.createElement('div');
    sidebarTitle.textContent = 'FILES (.md)';
    sidebarTitle.style.fontSize = '0.75rem';
    sidebarTitle.style.fontWeight = 'bold';
    sidebarTitle.style.color = 'var(--clr-text-muted)';
    sidebarTitle.style.padding = '0 16px 8px';
    sidebarTitle.style.letterSpacing = '1px';
    
    this.fileListEl = document.createElement('ul');
    this.fileListEl.style.listStyle = 'none';
    this.fileListEl.style.padding = '0';
    this.fileListEl.style.margin = '0';
    
    sidebar.append(sidebarTitle, this.fileListEl);

    // Right Pane (Editor)
    const editorPane = document.createElement('div');
    editorPane.style.flex = '1';
    editorPane.style.display = 'flex';
    editorPane.style.flexDirection = 'column';
    editorPane.style.position = 'relative';

    this.loadingEl = document.createElement('div');
    const loadingSpinner = document.createElement('span');
    loadingSpinner.className = 'loading-spinner';
    const loadingText = document.createElement('span');
    loadingText.textContent = ' Memuat workspace...';
    this.loadingEl.append(loadingSpinner, loadingText);
    this.loadingEl.style.position = 'absolute';
    this.loadingEl.style.inset = '0';
    this.loadingEl.style.display = 'flex';
    this.loadingEl.style.alignItems = 'center';
    this.loadingEl.style.justifyContent = 'center';
    this.loadingEl.style.background = 'var(--clr-surface)';
    this.loadingEl.style.zIndex = '10';

    this.textareaEl = document.createElement('textarea');
    this.textareaEl.className = 'workspace-editor-area';
    this.textareaEl.placeholder = 'Pilih file di samping kiri untuk mulai mengedit...';
    this.textareaEl.disabled = true; // disable until file is loaded
    this.textareaEl.style.flex = '1';
    this.textareaEl.style.border = 'none';
    this.textareaEl.style.resize = 'none';
    this.textareaEl.style.padding = '16px';
    this.textareaEl.style.background = 'transparent';
    this.textareaEl.style.color = 'var(--clr-text)';
    this.textareaEl.style.fontFamily = 'monospace';
    this.textareaEl.style.fontSize = '14px';
    this.textareaEl.style.lineHeight = '1.5';
    this.textareaEl.style.outline = 'none';

    this.textareaEl.addEventListener('input', () => {
      this.updateUnsavedIndicator();
    });

    editorPane.append(this.loadingEl, this.textareaEl);
    contentArea.append(sidebar, editorPane);

    // Footer
    const footer = document.createElement('div');
    footer.style.padding = '16px';
    footer.style.borderTop = '1px solid var(--clr-border)';
    footer.style.display = 'flex';
    footer.style.justifyContent = 'space-between';
    footer.style.alignItems = 'center';

    const infoText = document.createElement('span');
    infoText.style.color = 'var(--clr-text-muted)';
    infoText.style.fontSize = '0.85rem';
    infoText.textContent = '💡 Hint: ';
    const soulStrong = document.createElement('b');
    soulStrong.textContent = 'SOUL.md';
    const agentsStrong = document.createElement('b');
    agentsStrong.textContent = 'AGENTS.md';
    infoText.append(soulStrong, document.createTextNode(' untuk identitas, '), agentsStrong, document.createTextNode(' untuk instruksi utama.'));

    const actions = document.createElement('div');
    actions.style.display = 'flex';
    actions.style.gap = '8px';

    const cancelBtn = document.createElement('button');
    cancelBtn.className = 'btn btn-ghost';
    cancelBtn.textContent = 'Tutup';
    cancelBtn.onclick = () => this.close();

    this.saveBtn = document.createElement('button');
    this.saveBtn.className = 'btn btn-primary';
    this.saveBtn.textContent = 'Simpan File Ini';
    this.saveBtn.disabled = true;
    this.saveBtn.onclick = () => this.saveCurrentFile();

    actions.append(cancelBtn, this.saveBtn);
    footer.append(infoText, actions);

    modal.append(header, contentArea, footer);
    this.overlay.appendChild(modal);

    this.overlay.addEventListener('click', (e) => {
      if (e.target === this.overlay) this.close();
    });
  }

  private async loadWorkspaceFiles() {
    this.loading = true;
    this.loadingEl.style.display = 'flex';
    
    try {
      const res = await api.getWorkspaceFiles(this.agentId);
      this.files = res.files || [];
      this.renderFileList();
      
      // Auto-select initial file (prioritize SOUL.md, then AGENTS.md, then first file)
      if (this.files.length > 0) {
        let target = this.files[0];
        if (this.files.includes('SOUL.md')) target = 'SOUL.md';
        else if (this.files.includes('AGENTS.md')) target = 'AGENTS.md';
        
        await this.selectFile(target);
      } else {
        this.textareaEl.placeholder = 'Tidak ada file .md ditemukan di workspace.';
      }
    } catch (err: any) {
      showToast(`Gagal memuat workspace: ${err.message || err}`, 'error');
    } finally {
      this.loading = false;
      this.loadingEl.style.display = 'none';
    }
  }

  private renderFileList() {
    this.fileListEl.innerHTML = '';
    
    // Sort files to put SOUL.md and AGENTS.md on top
    const sortedFiles = [...this.files].sort((a, b) => {
      const pA = a === 'SOUL.md' ? 2 : a === 'AGENTS.md' ? 1 : 0;
      const pB = b === 'SOUL.md' ? 2 : b === 'AGENTS.md' ? 1 : 0;
      if (pA !== pB) return pB - pA;
      return a.localeCompare(b);
    });

    for (const file of sortedFiles) {
      const li = document.createElement('li');
      
      // Check if visually modified
      const isModified = this.fileContents.get(file) !== undefined && 
                         this.fileContents.get(file) !== this.originalContents.get(file);
      
      li.className = `workspace-file-item ${file === this.currentFile ? 'active' : ''}`;
      const icon = document.createElement('span');
      icon.className = 'file-icon';
      icon.textContent = '📄';
      const name = document.createElement('span');
      name.className = 'file-name';
      name.textContent = file;
      li.append(icon, name);
      if (isModified) {
        const modifiedDot = document.createElement('span');
        modifiedDot.className = 'file-modified-dot';
        modifiedDot.textContent = '●';
        li.appendChild(modifiedDot);
      }
      
      // Basic styling for file items
      li.style.padding = '10px 16px';
      li.style.cursor = 'pointer';
      li.style.display = 'flex';
      li.style.alignItems = 'center';
      li.style.gap = '8px';
      li.style.fontSize = '0.9rem';
      li.style.borderLeft = file === this.currentFile ? '3px solid var(--clr-primary)' : '3px solid transparent';
      li.style.background = file === this.currentFile ? 'rgba(59, 130, 246, 0.1)' : 'transparent';
      li.style.color = file === this.currentFile ? 'var(--clr-primary)' : 'var(--clr-text)';

      li.onmouseenter = () => { if (file !== this.currentFile) li.style.background = 'rgba(255,255,255,0.05)'; };
      li.onmouseleave = () => { if (file !== this.currentFile) li.style.background = 'transparent'; };

      li.onclick = () => this.selectFile(file);
      this.fileListEl.appendChild(li);
    }
  }

  private async selectFile(filename: string) {
    if (this.currentFile === filename) return; // already selected
    if (this.loading) return;

    // Save current textarea state to memory
    if (this.currentFile) {
      this.fileContents.set(this.currentFile, this.textareaEl.value);
    }

    this.currentFile = filename;
    this.renderFileList(); // Update active state
    
    // Switch or fetch content
    if (this.fileContents.has(filename)) {
      this.textareaEl.value = this.fileContents.get(filename)!;
      this.textareaEl.disabled = false;
      this.saveBtn.disabled = false;
      this.updateUnsavedIndicator();
    } else {
      await this.fetchFileContent(filename);
    }
  }

  private async fetchFileContent(filename: string) {
    this.loading = true;
    this.textareaEl.disabled = true;
    const oldPlaceholder = this.textareaEl.placeholder;
    this.textareaEl.placeholder = `Sedang memuat ${filename}...`;
    this.textareaEl.value = '';

    try {
      const res = await api.getWorkspaceFile(this.agentId, filename);
      const content = res.content || '';
      this.fileContents.set(filename, content);
      this.originalContents.set(filename, content);
      this.textareaEl.value = content;
      this.textareaEl.disabled = false;
      this.saveBtn.disabled = false;
    } catch (err: any) {
      showToast(`Gagal baca ${filename}: ${err.message || err}`, 'error');
      this.textareaEl.placeholder = `Gagal memuat ${filename}`;
    } finally {
      this.loading = false;
      if (this.textareaEl.placeholder.startsWith('Sedang memuat')) {
        this.textareaEl.placeholder = oldPlaceholder;
      }
      this.updateUnsavedIndicator();
    }
  }

  private updateUnsavedIndicator() {
    if (!this.currentFile) return;
    
    // Sync current textarea to memory for checking
    this.fileContents.set(this.currentFile, this.textareaEl.value);
    
    const isModified = this.textareaEl.value !== this.originalContents.get(this.currentFile);
    
    if (isModified) {
      this.saveBtn.textContent = 'Simpan File Ini •';
      this.saveBtn.style.background = 'var(--clr-warning)';
      this.saveBtn.style.color = '#000';
    } else {
      this.saveBtn.textContent = 'Simpan File Ini';
      this.saveBtn.style.background = 'var(--clr-primary)';
      this.saveBtn.style.color = '#fff';
    }
    
    this.renderFileList(); // Update the yellow dot in the sidebar
  }

  private async saveCurrentFile() {
    if (!this.currentFile || this.loading) return;
    
    const contentToSave = this.textareaEl.value;
    const filename = this.currentFile;
    
    this.loading = true;
    const originalText = this.saveBtn.textContent;
    this.saveBtn.textContent = 'Menyimpan...';
    this.saveBtn.disabled = true;
    this.textareaEl.disabled = true;

    try {
      await api.saveWorkspaceFile(this.agentId, filename, contentToSave);
      showToast(`✅ ${filename} berhasil disimpan!`, 'success');
      
      // Update original state to match saved state
      this.originalContents.set(filename, contentToSave);
      this.updateUnsavedIndicator();
    } catch (err: any) {
      showToast(`Gagal simpan ${filename}: ${err.message || err}`, 'error');
    } finally {
      this.loading = false;
      this.saveBtn.disabled = false;
      this.saveBtn.textContent = originalText || 'Simpan File Ini';
      this.textareaEl.disabled = false;
      this.textareaEl.focus();
    }
  }
}
