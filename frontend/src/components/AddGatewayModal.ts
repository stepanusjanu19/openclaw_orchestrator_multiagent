import { api, type AgentInfo } from '../api/client.js';
import { showToast } from './Toast.js';
import { escapeHTML } from '../utils/dom.js';

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
        <p style="font-size:0.8rem;color:var(--clr-text-muted);margin-top:4px">Konekungkan Agent ke platform messaging</p>
      </div>
    `;
    const closeBtn = document.createElement('button');
    closeBtn.className = 'modal-close';
    closeBtn.textContent = '✕';
    closeBtn.onclick = () => this.close();
    header.appendChild(closeBtn);

    let agentOptions = '<option value="">-- Pilih Agent --</option>';
    this.activeAgents.forEach(a => {
      agentOptions += `<option value="${escapeHTML(a.id)}">${escapeHTML(a.label)} (${escapeHTML(a.id)})</option>`;
    });

    this.formEl.style.padding = '20px';
    this.formEl.innerHTML = `
      <div class="form-group">
        <label class="form-label" for="gw-agent">Pilih Agent</label>
        <select id="gw-agent" class="form-input" required>
          ${agentOptions}
        </select>
        <span class="form-help">Hanya agent yang aktif yang bisa merespons webhook.</span>
      </div>

      <div class="form-group">
        <label class="form-label" for="gw-platform">Platform Gateway</label>
        <select id="gw-platform" class="form-input" required>
          <option value="http">🌐 HTTP Generic (Webhook Custom)</option>
          <option value="whatsapp">💬 WhatsApp (Meta Graph API)</option>
        </select>
      </div>

      <div class="form-group">
        <label class="form-label" for="gw-label">Label Gateway</label>
        <input id="gw-label" class="form-input" type="text" placeholder="e.g. WA Khusus Sales" required />
      </div>

      <div id="dynamic-config-section" style="margin-top: 20px; padding-top: 20px; border-top: 1px dashed var(--clr-border);">
        <!-- Akan diisi otomatis berdasarkan platform -->
      </div>

      <div style="display:flex; justify-content:flex-end; gap:8px; margin-top:24px;">
        <button type="button" class="btn btn-ghost" id="gw-cancel">Batal</button>
        <button type="submit" class="btn btn-primary" id="gw-submit">Buat Gateway</button>
      </div>
    `;

    modal.append(header, this.formEl);
    this.overlay.appendChild(modal);

    const platformSelect = this.formEl.querySelector<HTMLSelectElement>('#gw-platform')!;
    const ds = this.formEl.querySelector<HTMLDivElement>('#dynamic-config-section')!;

    const renderConfig = () => {
      const p = platformSelect.value;
      if (p === 'http') {
        ds.innerHTML = `
          <div class="form-group">
            <label class="form-label" for="gw-token">Secret Token (Opsional)</label>
            <input id="gw-token" class="form-input font-mono" type="text" placeholder="Sembarang string rahasia" />
            <span class="form-help">Jika diisi, request wajib menyertakan header <code>Authorization: Bearer &lt;token&gt;</code>.</span>
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

      const agentId = this.formEl.querySelector<HTMLSelectElement>('#gw-agent')!.value;
      const platform = platformSelect.value;
      const label = this.formEl.querySelector<HTMLInputElement>('#gw-label')!.value;

      const config: Record<string, string> = {};
      
      if (platform === 'http') {
        config.auth_token = this.formEl.querySelector<HTMLInputElement>('#gw-token')!.value;
      } else if (platform === 'whatsapp') {
        config.phone_number_id = this.formEl.querySelector<HTMLInputElement>('#gw-wa-phone-id')!.value;
        config.access_token = this.formEl.querySelector<HTMLInputElement>('#gw-wa-token')!.value;
        config.verify_token = this.formEl.querySelector<HTMLInputElement>('#gw-wa-verify')!.value;
        config.app_secret = this.formEl.querySelector<HTMLInputElement>('#gw-wa-app-secret')!.value;
        config.relay_mode = 'agent_engine';
      }

      try {
        await api.createGateway({ agent_id: agentId, platform, label, config });
        showToast('✅ Gateway berhasil dibuat!', 'success');
        this.onCreated();
        this.close();
      } catch (err: any) {
        showToast(err.message || String(err), 'error');
        submitBtn.disabled = false;
        submitBtn.textContent = 'Buat Gateway';
      }
    });
  }
}
