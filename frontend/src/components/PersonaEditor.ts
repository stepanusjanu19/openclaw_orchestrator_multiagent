// components/PersonaEditor.ts
// Modal editor untuk SOUL.md per agent.
// Read/write via PUT /api/agents/:id/persona

import { api } from '../api/client.js';
import { showToast } from './Toast.js';

export class PersonaEditor {
  private overlay: HTMLDivElement;
  private textarea: HTMLTextAreaElement;
  private agentId: string;
  private saveBtn: HTMLButtonElement;
  private loading = false;

  constructor(agentId: string, agentLabel: string) {
    this.agentId = agentId;

    this.overlay = document.createElement('div');
    this.overlay.className = 'modal-overlay';

    const modal = document.createElement('div');
    modal.className = 'modal';

    // Header
    const header = document.createElement('div');
    header.className = 'modal-header';

    const titleWrap = document.createElement('div');
    const title = document.createElement('h2');
    title.textContent = `Edit Persona — ${agentLabel}`;
    const sub = document.createElement('p');
    sub.style.cssText = 'font-size:0.8rem;color:var(--clr-text-muted);margin-top:4px;font-family:var(--font-mono)';
    sub.textContent = `SOUL.md · agent:${agentId}`;
    titleWrap.append(title, sub);

    const closeBtn = document.createElement('button');
    closeBtn.className = 'modal-close';
    closeBtn.textContent = '✕';
    closeBtn.addEventListener('click', () => this.close());

    header.append(titleWrap, closeBtn);

    // Info banner
    const info = document.createElement('div');
    info.className = 'info-banner';
    info.innerHTML = `
      <strong>SOUL.md</strong> mendefinisikan persona, tone, dan batasan agent.
      Gunakan Markdown. Perubahan aktif setelah agent di-restart.
    `;

    // Textarea
    this.textarea = document.createElement('textarea');
    this.textarea.className = 'persona-editor';
    this.textarea.placeholder = 'Tulis SOUL.md persona di sini...\n\n# SOUL.md — Agent Name\n\n## Core Truths\n- ...\n\n## Vibe\n- ...';
    this.textarea.rows = 18;

    // Footer
    const footer = document.createElement('div');
    footer.style.cssText = 'display:flex;gap:8px;justify-content:flex-end;margin-top:16px;';

    const cancelBtn = document.createElement('button');
    cancelBtn.className = 'btn btn-ghost';
    cancelBtn.textContent = 'Batal';
    cancelBtn.addEventListener('click', () => this.close());

    this.saveBtn = document.createElement('button');
    this.saveBtn.className = 'btn btn-primary';
    this.saveBtn.textContent = 'Simpan SOUL.md';
    this.saveBtn.addEventListener('click', () => this.save());

    footer.append(cancelBtn, this.saveBtn);

    modal.append(header, info, this.textarea, footer);
    this.overlay.appendChild(modal);

    // Close on backdrop click
    this.overlay.addEventListener('click', (e) => {
      if (e.target === this.overlay) this.close();
    });

    // Keyboard shortcut: Ctrl+S
    this.overlay.addEventListener('keydown', (e) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 's') {
        e.preventDefault();
        this.save();
      }
    });
  }

  async open() {
    document.body.appendChild(this.overlay);
    this.textarea.focus();

    // Load existing SOUL.md
    try {
      const persona = await api.getPersona(this.agentId);
      this.textarea.value = persona.content;
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      showToast(`Gagal load persona: ${msg}`, 'error');
    }
  }

  close() {
    this.overlay.remove();
  }

  private async save() {
    if (this.loading) return;
    const content = this.textarea.value.trim();
    if (!content) {
      showToast('SOUL.md tidak boleh kosong', 'error');
      return;
    }

    this.loading = true;
    this.saveBtn.disabled = true;
    this.saveBtn.innerHTML = '<span class="loading-spinner"></span> Menyimpan...';

    try {
      await api.updatePersona(this.agentId, content);
      showToast(`SOUL.md agent "${this.agentId}" berhasil disimpan. Restart agent untuk apply.`, 'success', 5000);
      this.close();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      showToast(`Gagal simpan: ${msg}`, 'error');
    } finally {
      this.loading = false;
      this.saveBtn.disabled = false;
      this.saveBtn.textContent = 'Simpan SOUL.md';
    }
  }
}
