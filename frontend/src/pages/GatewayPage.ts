// pages/GatewayPage.ts
import { api, type Gateway, type AgentInfo } from '../api/client.js';
import { showToast } from '../components/Toast.js';
import { AddGatewayModal } from '../components/AddGatewayModal.js';
import { escapeHTML } from '../utils/dom.js';

export class GatewayPage {
  private container: HTMLDivElement;
  private gateways: Gateway[] = [];
  private registeredAgents: AgentInfo[] = [];
  private runningAgents: AgentInfo[] = [];
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
      this.registeredAgents = agents;
      this.runningAgents = agents.filter(agent => agent.status?.running);
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
      showToast('URL disalin!', 'success');
    }
  }

  private getHttpAvailableAgents(): AgentInfo[] {
    const occupiedAgentIds = new Set(
      this.gateways
        .filter(gw => gw.active && !gw.expired && gw.platform.toLowerCase() === 'http')
        .map(gw => gw.agent_id)
    );
    return this.runningAgents.filter(agent => agent.gateway_eligible !== false && agent.id !== 'main' && !occupiedAgentIds.has(agent.id));
  }

  private gatewayAvailability(gw: Gateway): { label: string; className: string } {
    const reason = gw.availability_reason || (gw.expired ? 'expired' : gw.active ? 'available' : 'inactive');
    switch (reason) {
      case 'available':
        return { label: 'available / running', className: 'running' };
      case 'agent_stopped':
        return { label: 'agent stopped', className: 'stopped' };
      case 'agent_unregistered':
        return { label: 'agent unregistered', className: 'stopped' };
      case 'reserved_main':
        return { label: 'main reserved', className: 'stopped' };
      case 'expired':
        return { label: 'expired', className: 'stopped' };
      case 'inactive':
        return { label: 'inactive', className: 'stopped' };
      default:
        return { label: reason.replace(/_/g, ' '), className: gw.runtime_available ? 'running' : 'stopped' };
    }
  }

  private render() {
    const listHtml = this.loading ? `
      <div class="gateway-list gateway-empty skeleton-block">
        Mengambil data gateway...
      </div>
    ` : this.gateways.length === 0 ? `
      <div class="gateway-list gateway-empty">
        <div class="empty-icon">G</div>
        <strong>Belum ada gateway</strong>
        <span>Gunakan tombol di atas untuk menyambungkan agent ke platform messaging.</span>
      </div>
    ` : `
      <div class="gateway-list">
        ${this.gateways.map(gw => {
          const sLabel = escapeHTML(gw.label);
          const sPlatform = escapeHTML(gw.platform);
          const sId = escapeHTML(gw.id);
          const sAgent = escapeHTML(gw.agent_id);
          const sUrl = escapeHTML(gw.webhook_url);
          const sAuth = escapeHTML(gw.auth_mode || (gw.public ? 'public' : 'signature'));
          const expires = gw.expires_at ? new Date(gw.expires_at).toLocaleString() : '-';
          const availability = this.gatewayAvailability(gw);
          const policy = gw.auth_mode === 'public-restricted'
            ? 'TTL + visitor intelligence + risk scoring'
            : gw.auth_mode === 'hmac-sha256'
              ? 'HMAC timestamp + nonce + body hash'
              : 'Platform signature validation';
          return `
          <div class="gateway-row">
            <div class="gateway-platform-cell">
              <span class="gateway-platform-badge platform-${sPlatform}">
                ${sPlatform.toUpperCase()}
              </span>
            </div>
            <div class="gateway-meta">
              <strong>${sLabel}</strong>
              <code>ID: ${sId}</code>
              <span class="${gw.expired ? 'text-danger' : ''}">Auth: ${sAuth} · Expires: ${escapeHTML(expires)}</span>
              <span>Runtime: <span class="status-badge ${availability.className}">${escapeHTML(availability.label)}</span></span>
              <span>Policy: ${escapeHTML(policy)}</span>
            </div>
            <div class="gateway-agent">
              <span>Agent</span>
              <strong>${sAgent}</strong>
            </div>
            <div>
              <div class="gateway-webhook-url" title="Klik untuk copy webhook url" data-url="${sUrl}">
               ${sUrl}
              </div>
            </div>
            <div class="gateway-actions">
               <button class="btn btn-sm btn-ghost btn-delete-gw btn-danger-text" data-id="${sId}">Hapus</button>
            </div>
          </div>
        `}).join('')}
      </div>
    `;

    this.container.innerHTML = `
      <div class="page-header-row page-header-inline">
        <div class="page-title">
          <span class="page-eyebrow">Webhook relay</span>
          <h1>Gateway Manager</h1>
          <p>Sambungkan agent running ke platform eksternal seperti WhatsApp atau webhook.</p>
        </div>
        <button id="btn-add-gw" class="btn btn-primary page-primary-action" ${this.loading ? 'disabled' : ''}>Tambah Gateway</button>
      </div>
      
      ${listHtml}
      
      <div class="guide-panel">
         <h4>Cara Menggunakan Meta Webhook (WhatsApp)</h4>
         1. Copy URL Webhook dari gateway yang dibuat.<br/>
         2. Paste di konfigurasi Webhook Meta App Anda.<br/>
         3. Gunakan Verify Token yang Anda tentukan saat membuat gateway ini.<br/>
         4. Pastikan OpenClaw berjalan di server dengan Public Domain/IP (bisa pakai ngrok untuk testing).
      </div>
    `;

    // Attach events
    this.container.querySelector('#btn-add-gw')?.addEventListener('click', () => {
      if (this.registeredAgents.length === 0) {
        showToast('Belum ada agent terdaftar untuk membuat gateway.', 'error');
        return;
      }
      if (this.runningAgents.length === 0) {
        showToast('Tidak ada agent running yang tersedia untuk membuat gateway. Start agent terlebih dahulu.', 'error', 6000);
        return;
      }
      const availableAgents = this.getHttpAvailableAgents();
      if (availableAgents.length === 0) {
        showToast('Semua agent running sudah memiliki HTTP gateway aktif. Hapus gateway lama atau tunggu expired sebelum membuat webhook baru.', 'error', 6000);
        return;
      }
      const modal = new AddGatewayModal(availableAgents, () => this.fetchData());
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
