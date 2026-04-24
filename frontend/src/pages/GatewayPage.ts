// pages/GatewayPage.ts
import { api, type Gateway, type AgentInfo } from '../api/client.js';
import { showToast } from '../components/Toast.js';
import { AddGatewayModal } from '../components/AddGatewayModal.js';
import { escapeHTML } from '../utils/dom.js';

export class GatewayPage {
  private container: HTMLDivElement;
  private gateways: Gateway[] = [];
  private activeAgents: AgentInfo[] = [];
  private loading = false;

  constructor() {
    this.container = document.createElement('div');
    this.container.className = 'page-body gateway-page';
  }

  getElement() {
    return this.container;
  }

  async mount() {
    this.render(); // skeleton
    await this.fetchData();
  }

  unmount() {}

  private async fetchData() {
    this.loading = true;
    this.render();
    try {
      const [gateways, agents] = await Promise.all([
        api.getGateways(),
        api.listAgents()           // returns AgentInfo[] directly
      ]);
      this.gateways = gateways;
      this.activeAgents = agents.filter(agent => agent.status?.running);
    } catch (err: any) {
      showToast(err.message || 'Gagal load gateway data', 'error');
    } finally {
      this.loading = false;
      this.render();
    }
  }

  private async deleteGw(id: string) {
    if (!confirm('Apakah Anda yakin ingin menghapus gateway ini? Integrasi yang berjalan akan terputus.')) return;
    try {
      await api.deleteGateway(id);
      showToast('Gateway dihapus', 'info');
      await this.fetchData();
    } catch (err: any) {
      showToast(err.message || 'Gagal hapus gateway', 'error');
    }
  }

  private copyWebhook(url: string) {
    if (navigator.clipboard) {
      navigator.clipboard.writeText(url);
      showToast('Webhook URL disalin!', 'success');
    }
  }

  private render() {
    const listHtml = this.loading ? `
      <div class="gateway-list skeleton-block" style="padding:40px; text-align:center; color:var(--clr-text-muted);">
        Mengambil data gateway...
      </div>
    ` : this.gateways.length === 0 ? `
      <div class="gateway-list" style="padding:40px; text-align:center; color:var(--clr-text-muted);">
        <div style="font-size:3rem; margin-bottom:12px;">🔀</div>
        Belum ada Gateway yang dikonfigurasi.<br/>
        Gunakan tombol di atas untuk menyambungkan Agent ke platform messaging.
      </div>
    ` : `
      <div class="gateway-list">
        ${this.gateways.map(gw => {
          const sLabel = escapeHTML(gw.label);
          const sPlatform = escapeHTML(gw.platform);
          const sId = escapeHTML(gw.id);
          const sAgent = escapeHTML(gw.agent_id);
          const sUrl = escapeHTML(gw.webhook_url);
          return `
          <div class="gateway-row">
            <div>
              <span class="gateway-platform-badge platform-${sPlatform}">
                ${sPlatform.toUpperCase()}
              </span>
            </div>
            <div>
              <div style="font-weight:600;font-size:0.95rem;">${sLabel}</div>
              <div style="font-size:0.7rem;color:var(--clr-text-muted);font-family:var(--font-mono)">ID: ${sId}</div>
            </div>
            <div>
              <div style="font-size:0.75rem;color:var(--clr-text-muted)">Agent:</div>
              <div style="font-weight:600;">🤖 ${sAgent}</div>
            </div>
            <div>
              <div class="gateway-webhook-url" title="Klik untuk copy webhook url" data-url="${sUrl}">
               🔗 ${sUrl}
              </div>
            </div>
            <div style="display:flex;gap:4px;">
               <button class="btn btn-sm btn-ghost btn-delete-gw" style="color:var(--clr-danger)" data-id="${sId}">🗑 Hapus</button>
            </div>
          </div>
        `}).join('')}
      </div>
    `;

    this.container.innerHTML = `
      <div style="display:flex; justify-content:space-between; align-items:flex-end; margin-bottom:24px; flex-wrap:wrap; gap:16px;">
        <div class="page-title">
          <h1>Gateway Manager</h1>
          <p>Sambungkan agent aktif ke platform eksternal seperti WhatsApp atau webhook.</p>
        </div>
        <button id="btn-add-gw" class="btn btn-primary" ${this.loading ? 'disabled' : ''}>
          ➕ Tambah Gateway
        </button>
      </div>
      
      ${listHtml}
      
      <div style="margin-top:24px; padding:16px; background:rgba(34,211,165,0.08); border:1px solid rgba(34,211,165,0.2); border-radius:var(--radius-md); font-size:0.85rem;">
         <h4 style="color:var(--clr-success); margin-bottom:6px;">💡 Cara Menggunakan Meta Webhook (WhatsApp)</h4>
         1. Copy URL Webhook dari gateway yang dibuat.<br/>
         2. Paste di konfigurasi Webhook Meta App Anda.<br/>
         3. Gunakan Verify Token yang Anda tentukan saat membuat gateway ini.<br/>
         4. Pastikan OpenClaw berjalan di server dengan Public Domain/IP (bisa pakai ngrok untuk testing).
      </div>
    `;

    // Attach events
    this.container.querySelector('#btn-add-gw')?.addEventListener('click', () => {
      if (this.activeAgents.length === 0) {
        showToast('Minimal harus ada 1 agent sebelum membuat Gateway', 'error');
        return;
      }
      const modal = new AddGatewayModal(this.activeAgents, () => this.fetchData());
      modal.open();
    });

    this.container.querySelectorAll('.btn-delete-gw').forEach(btn => {
      btn.addEventListener('click', (e) => {
        const id = (e.currentTarget as HTMLElement).getAttribute('data-id');
        if (id) this.deleteGw(id);
      });
    });

    this.container.querySelectorAll('.gateway-webhook-url').forEach(el => {
      el.addEventListener('click', (e) => {
        const url = (e.currentTarget as HTMLElement).getAttribute('data-url');
        if (url) this.copyWebhook(url);
      });
    });
  }
}
