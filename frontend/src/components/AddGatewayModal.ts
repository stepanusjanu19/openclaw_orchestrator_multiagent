import { api, type AgentInfo } from '../api/client.js';
import { showToast } from './Toast.js';

type CreatedGateway = Awaited<ReturnType<typeof api.createGateway>>;

export class AddGatewayModal {
  private overlay: HTMLDivElement;
  private formEl: HTMLFormElement;
  private activeAgents: AgentInfo[] = [];
  private onCreated: () => void;

  constructor(agents: AgentInfo[], onCreated: () => void) {
    this.activeAgents = agents;
    this.onCreated = onCreated;
    this.overlay = document.createElement('div');
    this.overlay.className = 'modal-overlay';
    this.formEl = document.createElement('form');
    this.build();
  }

  open() {
    document.body.appendChild(this.overlay);
  }

  close() {
    this.overlay.remove();
  }

  private build() {
    const modal = document.createElement('div');
    modal.className = 'modal';
    modal.style.maxWidth = '500px';

    const header = document.createElement('div');
    header.className = 'modal-header';
    header.innerHTML = `
      <div>
        <h2>Tambah Gateway Eksternal</h2>
        <p class="modal-subtitle">Koneksikan agent ke platform messaging</p>
      </div>
    `;
    const closeBtn = document.createElement('button');
    closeBtn.className = 'modal-close';
    closeBtn.textContent = '✕';
    closeBtn.onclick = () => this.close();
    header.appendChild(closeBtn);

    this.formEl.style.padding = '20px';
    this.formEl.innerHTML = `
      <div class="form-group">
        <label class="form-label" for="gw-agent">Pilih Agent</label>
        <select id="gw-agent" class="form-input" required></select>
        <span class="form-help" id="gw-agent-help">Hanya agent aktif tanpa HTTP gateway aktif yang bisa dipilih.</span>
      </div>

      <div class="form-group">
        <label class="form-label" for="gw-platform">Platform Gateway</label>
        <select id="gw-platform" class="form-input" required>
          <option value="http">HTTP Generic (Webhook Custom)</option>
          <option value="whatsapp">WhatsApp (Meta Graph API)</option>
        </select>
      </div>

      <div class="form-group">
        <label class="form-label" for="gw-label">Label Gateway</label>
        <input id="gw-label" class="form-input" type="text" placeholder="e.g. WA Khusus Sales" required />
      </div>

      <div class="form-group">
        <label class="form-label" for="gw-ttl">Masa Berlaku Webhook</label>
        <select id="gw-ttl" class="form-input">
          <option value="30">30 hari</option>
          <option value="90" selected>90 hari</option>
          <option value="180">180 hari</option>
          <option value="365">365 hari</option>
        </select>
        <span class="form-help">Seperti PAT, webhook akan ditolak setelah melewati masa berlaku.</span>
      </div>

      <div id="dynamic-config-section" style="margin-top: 20px; padding-top: 20px; border-top: 1px dashed var(--clr-border);">
        <!-- Akan diisi otomatis berdasarkan platform -->
      </div>

      <div id="gw-create-result" class="gateway-create-result" hidden></div>

      <div style="display:flex; justify-content:flex-end; gap:8px; margin-top:24px;">
        <button type="button" class="btn btn-ghost" id="gw-cancel">Batal</button>
        <button type="submit" class="btn btn-primary" id="gw-submit">Buat Gateway</button>
      </div>
    `;

    modal.append(header, this.formEl);
    this.overlay.appendChild(modal);

    this.populateAgentSelect();

    const platformSelect = this.formEl.querySelector<HTMLSelectElement>('#gw-platform')!;
    const ds = this.formEl.querySelector<HTMLDivElement>('#dynamic-config-section')!;

    const renderConfig = () => {
      const p = platformSelect.value;
      if (p === 'http') {
        ds.innerHTML = `
          <div class="form-group">
            <label class="form-label" for="gw-token">Secret Token</label>
            <input id="gw-token" class="form-input font-mono" type="text" value="Auto-generate HMAC secret" disabled />
            <span class="form-help">Default aman: request wajib menyertakan <code>X-OpenClaw-Timestamp</code>, <code>X-OpenClaw-Nonce</code>, dan <code>X-OpenClaw-Signature</code>.</span>
          </div>
          <label style="display:flex;align-items:flex-start;gap:10px;color:var(--clr-text-secondary);font-size:0.82rem;line-height:1.4;">
            <input id="gw-public" type="checkbox" style="margin-top:3px" />
            <span>Public restricted webhook tanpa HMAC. Request tetap melewati TTL, body limit, visitor intelligence, rate limit, dan risk scoring.</span>
          </label>
          <div class="no-token-banner" style="margin-top:10px">
            Mode public restricted cocok untuk guest/ngrok/testing. Payload berisiko seperti prompt leakage, SSTI probe, command-looking input, dan file disclosure akan diblokir sebelum masuk agent.
          </div>
        `;
      } else if (p === 'whatsapp') {
        ds.innerHTML = `
          <div class="form-group">
            <label class="form-label" for="gw-wa-phone-id">Phone Number ID</label>
            <input id="gw-wa-phone-id" class="form-input" type="text" placeholder="Dari Meta Developer Console" required />
          </div>
          <div class="form-group">
            <label class="form-label" for="gw-wa-token">Permanent Access Token</label>
            <input id="gw-wa-token" class="form-input font-mono" type="password" placeholder="EAA..." required />
          </div>
          <div class="form-group">
            <label class="form-label" for="gw-wa-verify">Verify Token</label>
            <input id="gw-wa-verify" class="form-input" type="text" placeholder="Token rahasia untuk validasi Meta" required />
          </div>
          <div class="form-group">
            <label class="form-label" for="gw-wa-app-secret">App Secret</label>
            <input id="gw-wa-app-secret" class="form-input font-mono" type="password" placeholder="Meta App Secret" required />
            <span class="form-help">Dipakai untuk validasi header <code>X-Hub-Signature-256</code> dari Meta.</span>
          </div>
          <div class="form-group">
            <label class="form-label" for="gw-wa-relay">Relay Mode</label>
            <input id="gw-wa-relay" class="form-input font-mono" type="text" value="agent_engine" readonly />
            <span class="form-help">WhatsApp akan mengakses agent runtime yang sama dengan Telegram.</span>
          </div>
        `;
      }
    };
    platformSelect.onchange = renderConfig;
    renderConfig(); // initial render

    this.formEl.querySelector('#gw-cancel')?.addEventListener('click', () => this.close());
    this.overlay.addEventListener('click', (e) => {
      if (e.target === this.overlay) this.close();
    });

    this.formEl.addEventListener('submit', async (e) => {
      e.preventDefault();
      
      const submitBtn = this.formEl.querySelector<HTMLButtonElement>('#gw-submit')!;
      submitBtn.disabled = true;
      submitBtn.textContent = 'Menyimpan...';
      this.clearCreateResult();

      const agentId = this.formEl.querySelector<HTMLSelectElement>('#gw-agent')!.value;
      const platform = platformSelect.value;
      const label = this.formEl.querySelector<HTMLInputElement>('#gw-label')!.value;
      const ttlDays = this.formEl.querySelector<HTMLSelectElement>('#gw-ttl')!.value;

      if (!agentId) {
        const message = 'Pilih agent aktif yang belum memiliki HTTP gateway aktif.';
        showToast(message, 'error');
        this.renderCreateError(message);
        submitBtn.disabled = false;
        submitBtn.textContent = 'Buat Gateway';
        return;
      }

      const config: Record<string, string> = { ttl_days: ttlDays };
      
      if (platform === 'http') {
        const publicWebhook = this.formEl.querySelector<HTMLInputElement>('#gw-public')!.checked;
        config.public = publicWebhook ? 'true' : 'false';
        config.signature_required = publicWebhook ? 'false' : 'true';
      } else if (platform === 'whatsapp') {
        config.phone_number_id = this.formEl.querySelector<HTMLInputElement>('#gw-wa-phone-id')!.value;
        config.access_token = this.formEl.querySelector<HTMLInputElement>('#gw-wa-token')!.value;
        config.verify_token = this.formEl.querySelector<HTMLInputElement>('#gw-wa-verify')!.value;
        config.app_secret = this.formEl.querySelector<HTMLInputElement>('#gw-wa-app-secret')!.value;
        config.relay_mode = 'agent_engine';
      }

      try {
        const created = await api.createGateway({ agent_id: agentId, platform, label, config });
        showToast('Gateway berhasil dibuat!', 'success');
        this.onCreated();
        this.renderCreateSuccess(created);
        submitBtn.disabled = false;
        submitBtn.type = 'button';
        submitBtn.textContent = 'Selesai';
        submitBtn.onclick = () => this.close();
      } catch (err: any) {
        const message = err.message || String(err);
        showToast(message, 'error');
        this.renderCreateError(message);
        submitBtn.disabled = false;
        submitBtn.textContent = 'Buat Gateway';
      }
    });
  }

  private populateAgentSelect() {
    const select = this.formEl.querySelector<HTMLSelectElement>('#gw-agent')!;
    const help = this.formEl.querySelector<HTMLSpanElement>('#gw-agent-help')!;
    const submitBtn = this.formEl.querySelector<HTMLButtonElement>('#gw-submit')!;

    select.replaceChildren();

    if (this.activeAgents.length === 0) {
      const empty = document.createElement('option');
      empty.value = '';
      empty.textContent = 'Tidak ada agent tersedia';
      select.appendChild(empty);
      select.disabled = true;
      submitBtn.disabled = true;
      help.textContent = 'Semua agent aktif sudah memiliki HTTP gateway aktif, atau belum ada agent yang running.';
      return;
    }

    const placeholder = document.createElement('option');
    placeholder.value = '';
    placeholder.textContent = '-- Pilih Agent --';
    select.appendChild(placeholder);

    for (const agent of this.activeAgents) {
      const option = document.createElement('option');
      option.value = agent.id;
      option.textContent = `${agent.label} (${agent.id})`;
      select.appendChild(option);
    }
  }

  private clearCreateResult() {
    const result = this.formEl.querySelector<HTMLDivElement>('#gw-create-result')!;
    result.hidden = true;
    result.replaceChildren();
  }

  private renderCreateSuccess(created: CreatedGateway) {
    const result = this.formEl.querySelector<HTMLDivElement>('#gw-create-result')!;
    result.replaceChildren();

    const card = this.createResultCard('success', 'Webhook dibuat');
    this.appendResultField(card, 'URL', created.webhook_url);
    this.appendResultNote(card, 'Buka URL ini di browser untuk mencatat visitor + GPS consent, atau kirim POST JSON ke URL yang sama untuk webhook message.');
    this.appendResultField(card, 'Auth mode', created.auth_mode || '-');
    this.appendResultField(card, 'Expires', created.expires_at || '-');

    if (created.webhook_secret) {
      this.appendResultField(card, 'HMAC Secret - tampil sekali', created.webhook_secret);
      this.appendResultNote(card, 'Signature: v1=HMAC_SHA256(secret, METHOD + "\\n" + PATH_WITH_QUERY + "\\n" + TIMESTAMP + "\\n" + NONCE + "\\n" + SHA256(body))');
    }

    result.appendChild(card);
    result.hidden = false;
  }

  private renderCreateError(message: string) {
    const result = this.formEl.querySelector<HTMLDivElement>('#gw-create-result')!;
    result.replaceChildren();

    const card = this.createResultCard('error', message.includes('HTTP gateway aktif') ? 'HTTP gateway aktif sudah ada' : 'Gateway gagal dibuat');
    this.appendResultNote(card, message);

    if (message.includes('HTTP gateway aktif')) {
      this.appendResultNote(card, 'Satu agent hanya boleh memiliki satu HTTP webhook aktif. Hapus atau tunggu gateway lama expired sebelum membuat yang baru.');
    }

    result.appendChild(card);
    result.hidden = false;
  }

  private createResultCard(kind: 'success' | 'error', titleText: string): HTMLDivElement {
    const card = document.createElement('div');
    card.className = `gateway-result-card gateway-result-${kind}`;

    const title = document.createElement('strong');
    title.textContent = titleText;
    card.appendChild(title);
    return card;
  }

  private appendResultField(card: HTMLElement, labelText: string, valueText: string) {
    const label = document.createElement('div');
    label.className = 'gateway-result-label';
    label.textContent = labelText;

    const code = document.createElement('code');
    code.textContent = valueText;

    card.append(label, code);
  }

  private appendResultNote(card: HTMLElement, noteText: string) {
    const note = document.createElement('div');
    note.className = 'gateway-result-note';
    note.textContent = noteText;
    card.appendChild(note);
  }
}
