// BotFatherPage.ts — Redesigned Clean UI
import { authStore, API_BASE } from '../api/client.js';
import { showToast } from '../components/Toast.js';

async function ubRequest(path: string, method = 'GET', body?: object) {
  const res = await fetch(`${API_BASE}${path}`, {
    method,
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json',
      ...(method !== 'GET' ? { 'X-CSRF-Token': authStore.getCSRFToken() } : {}),
    },
    ...(body ? { body: JSON.stringify(body) } : {}),
  });
  const data = await res.json();
  if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
  return data;
}

type UBState = 'loading' | 'stopped' | 'auth' | 'ready';

export class BotFatherPage {
  private el: HTMLElement;

  constructor() {
    this.el = document.createElement('div');
    this.el.className = 'botfather-page page-container';
  }

  getElement() { return this.el; }

  async mount() {
    this.render('loading');
    try {
      const status = await ubRequest('/api/userbot/status');
      if (!status.running) this.render('stopped');
      else if (!status.authenticated) this.render('auth');
      else this.render('ready', status.data || {});
    } catch {
      this.render('stopped');
    }
  }

  unmount() {}

  private render(state: UBState, user: any = {}) {
    const stateChip = {
      loading: `<span class="status-stopped">⏳ Memuat</span>`,
      stopped: `<span class="status-stopped">⏹ Mati</span>`,
      auth:    `<span class="status-warning">🔑 Perlu Login</span>`,
      ready:   `<span class="status-running">✅ Aktif</span>`,
    }[state];

    this.el.innerHTML = `
      <div class="page-header">
        <div>
          <div class="page-title">🤖 BotFather</div>
          <div class="page-subtitle">Kelola bot Telegram tanpa buka aplikasi Telegram</div>
        </div>
        ${stateChip}
      </div>

      <div class="ub-grid">
        <!-- Sidebar Engine Info -->
        <div class="ub-sidebar card">
          <div class="ub-sidebar-header">
            <div class="ub-avatar">${state === 'ready' ? '👤' : '🔌'}</div>
            <div>
              <div class="ub-sidebar-title">${state === 'ready' ? (user.firstName || 'Pengguna') : 'UserBot Engine'}</div>
              <div class="ub-sidebar-sub">${state === 'ready' ? ('@' + (user.username || '-')) : 'MTProto · GramJS'}</div>
            </div>
          </div>

          <div class="ub-sidebar-actions">
            ${state === 'stopped'
              ? `<button class="btn btn-primary" id="ub-btn-start">▶ Hidupkan</button>`
              : `<button class="btn btn-danger" id="ub-btn-stop">⏹ Matikan</button>`}
          </div>

          <div class="ub-sidebar-info">
            <div class="info-row"><span>Status</span><span class="chip ${state==='ready'?'chip-green':state==='auth'?'chip-yellow':'chip-red'}">${state}</span></div>
            <div class="info-row"><span>Library</span><span>GramJS</span></div>
            <div class="info-row"><span>Transport</span><span>Node.js IPC</span></div>
          </div>

          <div class="ub-sidebar-warning">
            🔒 Sesi tersimpan di <code>credentials/userbot.session</code> — tidak pernah dikirim ke luar.
          </div>
        </div>

        <!-- Main Panel -->
        <div id="ub-main">
          ${this.renderMain(state)}
        </div>
      </div>
    `;

    this.attachHandlers();
  }

  private renderMain(state: UBState) {
    if (state === 'loading') {
      return `<div class="empty-state"><div class="spinner-large"></div><p>Menghubungi backend...</p></div>`;
    }
    if (state === 'stopped') {
      return `
        <div class="card">
          <div class="empty-state">
            <div style="font-size:3.5rem;margin-bottom:12px">🔌</div>
            <h3>Engine Belum Berjalan</h3>
            <p>Klik <strong>Hidupkan</strong> untuk memulai MTProto engine. Pastikan API credentials sudah diisi.</p>
          </div>
          <div class="alert alert-info" style="margin-top:8px">
            <strong>Prasyarat:</strong> Isi <code>TELEGRAM_API_ID</code> &amp; <code>TELEGRAM_API_HASH</code> di <code>credentials/.env</code>.<br>
            Dapatkan di <a href="https://my.telegram.org/apps" target="_blank">my.telegram.org/apps</a> &rarr; API Development Tools.
          </div>
        </div>`;
    }
    if (state === 'auth') return this.renderAuth();
    return this.renderDashboard();
  }

  private renderAuth() {
    return `
      <div class="auth-flow card">
        <h2>🔑 Login Telegram</h2>
        <p class="page-subtitle" style="margin-bottom:24px">Masuk sekali — sesi disimpan otomatis</p>

        <div id="auth-phone-step">
          <div class="form-group">
            <label class="form-label">Nomor Telepon <span class="hint">(format +628...)</span></label>
            <div class="input-row">
              <input type="tel" id="ub-phone" class="form-input" placeholder="+628123456789" autocomplete="tel" />
              <button class="btn btn-primary" id="ub-btn-sendcode">Kirim OTP</button>
            </div>
          </div>
        </div>

        <div id="auth-otp-step" style="display:none">
          <div class="form-group">
            <label class="form-label">Kode OTP dari Telegram</label>
            <div class="input-row">
              <input type="text" id="ub-otp" class="form-input" placeholder="12345" maxlength="10" autocomplete="one-time-code" />
              <button class="btn btn-primary" id="ub-btn-signin">Verifikasi</button>
            </div>
            <input type="hidden" id="ub-phone-code-hash" />
          </div>
        </div>

        <div id="auth-2fa-step" style="display:none">
          <div class="form-group">
            <label class="form-label">Password 2FA <span class="hint">(jika akun menggunakan 2FA)</span></label>
            <div class="input-row">
              <input type="password" id="ub-2fa" class="form-input" placeholder="Password Telegram" />
              <button class="btn btn-primary" id="ub-btn-2fa">Masuk</button>
            </div>
          </div>
        </div>

        <div id="ub-auth-error" style="display:none" class="alert alert-error"></div>
      </div>`;
  }

  private renderDashboard() {
    return `
      <div class="bf-dashboard">
        <div class="tab-bar" id="bf-tabs">
          <button class="tab active" data-tab="create">➕ Buat Bot</button>
          <button class="tab" data-tab="manage">🔧 Kelola Bot</button>
          <button class="tab" data-tab="list">📋 Daftar Bot</button>
          <button class="tab" data-tab="delete">🗑 Hapus</button>
          <button class="tab" data-tab="revoke">🔄 Revoke Token</button>
        </div>

        <!-- Create -->
        <div class="tab-content" id="tab-create">
          <div class="card">
            <h3 style="margin-bottom:16px">Buat Bot Baru Otomatis</h3>
            <div class="form-group">
              <label class="form-label">Nama Bot <span class="hint">tampil di Telegram</span></label>
              <input type="text" id="bf-name" class="form-input" placeholder="Contoh: Asisten AI Saya" />
            </div>
            <div class="form-group">
              <label class="form-label">Username <span class="hint">harus diakhiri 'bot'</span></label>
              <input type="text" id="bf-username" class="form-input" placeholder="Contoh: asisten_ai_saya_bot" />
            </div>
            <button class="btn btn-primary" id="bf-btn-create">🤖 Buat Bot via @BotFather</button>
            <div id="bf-create-result" style="display:none"></div>
          </div>
        </div>

        <!-- Manage Existing -->
        <div class="tab-content" id="tab-manage" style="display:none">
          <div class="card">
            <h3 style="margin-bottom:4px">Kelola Bot yang Sudah Ada</h3>
            <p class="page-subtitle" style="margin-bottom:20px">Semua perubahan langsung via @BotFather</p>

            <div class="form-group">
              <label class="form-label">Username Bot Target</label>
              <div class="input-row">
                <input type="text" id="mgr-username" class="form-input" placeholder="@nama_bot" />
                <button class="btn btn-ghost btn-sm" id="mgr-btn-load">📋 Muat</button>
              </div>
            </div>

            <div id="mgr-panel" style="display:none;margin-top:16px">
              <!-- Sub-tabs -->
              <div class="tab-bar" id="mgr-tabs" style="margin-bottom:16px">
                <button class="tab active" data-mgr="token">🔑 Token</button>
                <button class="tab" data-mgr="name">✏️ Nama</button>
                <button class="tab" data-mgr="desc">📝 Deskripsi</button>
                <button class="tab" data-mgr="about">ℹ️ About</button>
                <button class="tab" data-mgr="commands">⌨️ Perintah</button>
              </div>

              <!-- Get Token -->
              <div class="mgr-content" id="mgr-token">
                <p style="color:var(--clr-text-2);margin-bottom:16px;font-size:.875rem">Ambil token API bot dari @BotFather (berguna jika token hilang).</p>
                <button class="btn btn-primary" id="mgr-btn-token">🔑 Tampilkan Token</button>
                <div id="mgr-token-result" style="display:none;margin-top:12px"></div>
              </div>

              <!-- Set Name -->
              <div class="mgr-content" id="mgr-name" style="display:none">
                <div class="form-group">
                  <label class="form-label">Nama Baru</label>
                  <input type="text" id="mgr-new-name" class="form-input" placeholder="Nama tampilan baru di Telegram" />
                </div>
                <button class="btn btn-primary" id="mgr-btn-name">✏️ Ubah Nama</button>
                <div id="mgr-name-result" style="display:none;margin-top:8px"></div>
              </div>

              <!-- Set Description -->
              <div class="mgr-content" id="mgr-desc" style="display:none">
                <div class="form-group">
                  <label class="form-label">Deskripsi <span class="hint">tampil saat pertama buka bot</span></label>
                  <textarea id="mgr-new-desc" class="form-input" rows="4" placeholder="Apa yang bisa dilakukan bot ini?" style="resize:vertical"></textarea>
                </div>
                <button class="btn btn-primary" id="mgr-btn-desc">📝 Set Deskripsi</button>
                <div id="mgr-desc-result" style="display:none;margin-top:8px"></div>
              </div>

              <!-- Set About -->
              <div class="mgr-content" id="mgr-about" style="display:none">
                <div class="form-group">
                  <label class="form-label">Teks About <span class="hint">tampil di profil bot</span></label>
                  <textarea id="mgr-new-about" class="form-input" rows="3" placeholder="Tentang bot ini..." style="resize:vertical"></textarea>
                </div>
                <button class="btn btn-primary" id="mgr-btn-about">ℹ️ Set About</button>
                <div id="mgr-about-result" style="display:none;margin-top:8px"></div>
              </div>

              <!-- Set Commands -->
              <div class="mgr-content" id="mgr-commands" style="display:none">
                <div class="form-group">
                  <label class="form-label">Daftar Perintah <span class="hint">format: /perintah - Deskripsi (satu per baris)</span></label>
                  <textarea id="mgr-new-cmds" class="form-input font-mono" rows="6"
                    placeholder="/start - Mulai bot&#10;/help - Bantuan&#10;/info - Informasi" style="resize:vertical"></textarea>
                </div>
                <button class="btn btn-primary" id="mgr-btn-cmds">⌨️ Set Commands</button>
                <div id="mgr-cmds-result" style="display:none;margin-top:8px"></div>
              </div>
            </div>
          </div>
        </div>

        <!-- List -->
        <div class="tab-content" id="tab-list" style="display:none">
          <div class="card">
            <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:16px">
              <h3>Bot yang Anda Miliki</h3>
              <button class="btn btn-ghost btn-sm" id="bf-btn-list">🔄 Refresh</button>
            </div>
            <div id="bf-list-result" class="empty-state"><p>Klik Refresh untuk memuat daftar bot</p></div>
          </div>
        </div>

        <!-- Delete -->
        <div class="tab-content" id="tab-delete" style="display:none">
          <div class="card">
            <h3 style="margin-bottom:4px">Hapus Bot</h3>
            <p class="page-subtitle" style="margin-bottom:20px">Permanen — tidak dapat dibatalkan</p>
            <div class="form-group">
              <label class="form-label">Username Bot</label>
              <input type="text" id="bf-del-username" class="form-input" placeholder="@nama_bot" />
            </div>
            <button class="btn btn-danger" id="bf-btn-delete">🗑 Hapus Permanen</button>
          </div>
        </div>

        <!-- Revoke -->
        <div class="tab-content" id="tab-revoke" style="display:none">
          <div class="card">
            <h3 style="margin-bottom:4px">Regenerate Token</h3>
            <p class="page-subtitle" style="margin-bottom:20px">Token lama langsung tidak valid setelah revoke</p>
            <div class="form-group">
              <label class="form-label">Username Bot</label>
              <input type="text" id="bf-rev-username" class="form-input" placeholder="@nama_bot" />
            </div>
            <button class="btn btn-warning" id="bf-btn-revoke">🔄 Regenerate Token</button>
            <div id="bf-revoke-result" style="display:none"></div>
          </div>
        </div>
      </div>`;
  }

  private attachHandlers() {
    // Engine lifecycle
    this.el.querySelector('#ub-btn-start')?.addEventListener('click', () => this.startEngine());
    this.el.querySelector('#ub-btn-stop')?.addEventListener('click', () => this.stopEngine());

    // Auth
    this.el.querySelector('#ub-btn-sendcode')?.addEventListener('click', () => this.sendCode());
    this.el.querySelector('#ub-btn-signin')?.addEventListener('click', () => this.signIn());
    this.el.querySelector('#ub-btn-2fa')?.addEventListener('click', () => this.signIn2FA());

    // Main tabs
    this.el.querySelectorAll('.tab[data-tab]').forEach(tab => {
      tab.addEventListener('click', () => {
        const id = (tab as HTMLElement).dataset.tab!;
        this.el.querySelectorAll('.tab[data-tab]').forEach(t => t.classList.remove('active'));
        this.el.querySelectorAll('.tab-content').forEach(t => (t as HTMLElement).style.display = 'none');
        tab.classList.add('active');
        const panel = this.el.querySelector(`#tab-${id}`) as HTMLElement;
        if (panel) panel.style.display = '';
      });
    });

    // Manage sub-tabs
    this.el.querySelectorAll('.tab[data-mgr]').forEach(tab => {
      tab.addEventListener('click', () => {
        const id = (tab as HTMLElement).dataset.mgr!;
        this.el.querySelectorAll('.tab[data-mgr]').forEach(t => t.classList.remove('active'));
        this.el.querySelectorAll('.mgr-content').forEach(t => (t as HTMLElement).style.display = 'none');
        tab.classList.add('active');
        const panel = this.el.querySelector(`#mgr-${id}`) as HTMLElement;
        if (panel) panel.style.display = '';
      });
    });

    // BotFather create/list/delete/revoke
    this.el.querySelector('#bf-btn-create')?.addEventListener('click', () => this.createBot());
    this.el.querySelector('#bf-btn-list')?.addEventListener('click', () => this.listBots());
    this.el.querySelector('#bf-btn-delete')?.addEventListener('click', () => this.deleteBot());
    this.el.querySelector('#bf-btn-revoke')?.addEventListener('click', () => this.revokeToken());

    // Manage Bot panel
    this.el.querySelector('#mgr-btn-load')?.addEventListener('click', () => this.loadBotPanel());
    this.el.querySelector('#mgr-btn-token')?.addEventListener('click', () => this.getBotToken());
    this.el.querySelector('#mgr-btn-name')?.addEventListener('click', () => this.setBotName());
    this.el.querySelector('#mgr-btn-desc')?.addEventListener('click', () => this.setBotDescription());
    this.el.querySelector('#mgr-btn-about')?.addEventListener('click', () => this.setBotAbout());
    this.el.querySelector('#mgr-btn-cmds')?.addEventListener('click', () => this.setBotCommands());
  }

  private maskSecret(secret: string): string {
    if (!secret) return '';
    if (secret.length <= 10) return '••••••';
    return `${secret.slice(0, 4)}••••${secret.slice(-4)}`;
  }

  private copyToClipboard(value: string) {
    navigator.clipboard?.writeText(value);
    showToast('Disalin ke clipboard', 'success');
  }

  private switchMainTab(tabId: string) {
    this.el.querySelectorAll('.tab[data-tab]').forEach(t => t.classList.remove('active'));
    this.el.querySelectorAll('.tab-content').forEach(t => (t as HTMLElement).style.display = 'none');
    const tabBtn = this.el.querySelector(`.tab[data-tab="${tabId}"]`) as HTMLElement | null;
    const panel = this.el.querySelector(`#tab-${tabId}`) as HTMLElement | null;
    if (tabBtn) tabBtn.classList.add('active');
    if (panel) panel.style.display = '';
  }

  private renderTokenResult(containerId: string, title: string, token: string, fields: Array<{ label: string; value: string }> = []) {
    const host = this.el.querySelector(containerId) as HTMLElement | null;
    if (!host) return;
    host.style.display = '';
    host.textContent = '';

    const card = document.createElement('div');
    card.className = 'result-card';

    const heading = document.createElement('h4');
    heading.textContent = title;
    card.appendChild(heading);

    for (const field of fields) {
      const row = document.createElement('div');
      row.className = 'info-row';
      const label = document.createElement('span');
      label.textContent = field.label;
      const value = document.createElement('strong');
      value.textContent = field.value;
      row.append(label, value);
      card.appendChild(row);
    }

    const row = document.createElement('div');
    row.className = 'info-row';
    const rowLabel = document.createElement('span');
    rowLabel.textContent = 'Token API';
    row.appendChild(rowLabel);
    card.appendChild(row);

    const box = document.createElement('div');
    box.className = 'result-token-box';
    const code = document.createElement('code');
    code.textContent = this.maskSecret(token);

    const revealBtn = document.createElement('button');
    revealBtn.className = 'btn btn-ghost btn-sm';
    revealBtn.textContent = '👁';
    revealBtn.addEventListener('click', () => {
      const revealed = code.textContent === token;
      code.textContent = revealed ? this.maskSecret(token) : token;
      revealBtn.textContent = revealed ? '👁' : '🙈';
    });

    const copyBtn = document.createElement('button');
    copyBtn.className = 'btn btn-ghost btn-sm';
    copyBtn.textContent = '📋';
    copyBtn.addEventListener('click', () => this.copyToClipboard(token));

    box.append(code, revealBtn, copyBtn);
    card.appendChild(box);

    const note = document.createElement('p');
    note.style.cssText = 'font-size:.8rem;color:var(--clr-text-muted);margin-top:12px';
    note.textContent = 'Token disamarkan secara default. Gunakan reveal hanya saat diperlukan.';
    card.appendChild(note);

    host.appendChild(card);
  }

  private renderListBotsResult(bots: string[], raw = '') {
    const host = this.el.querySelector('#bf-list-result') as HTMLElement | null;
    if (!host) return;
    host.textContent = '';

    if (bots.length === 0) {
      const wrapper = document.createElement('div');
      wrapper.className = 'empty-state';
      const icon = document.createElement('div');
      icon.style.cssText = 'font-size:2.5rem;margin-bottom:12px';
      icon.textContent = '🤖';
      const text = document.createElement('p');
      text.textContent = 'Belum ada bot ditemukan';
      wrapper.append(icon, text);
      if (raw) {
        const small = document.createElement('small');
        small.style.color = 'var(--clr-text-muted)';
        small.textContent = raw;
        wrapper.appendChild(small);
      }
      host.appendChild(wrapper);
      return;
    }

    const list = document.createElement('div');
    list.style.cssText = 'display:flex;flex-direction:column;gap:10px';

    for (const name of bots) {
      const item = document.createElement('div');
      item.className = 'bot-list-item';

      const avatar = document.createElement('div');
      avatar.className = 'bot-list-avatar';
      avatar.textContent = '🤖';

      const info = document.createElement('div');
      info.className = 'bot-list-info';
      const botName = document.createElement('div');
      botName.className = 'bot-list-name';
      botName.textContent = name;
      const sub = document.createElement('div');
      sub.className = 'bot-list-sub';
      sub.textContent = 'Bot Telegram';
      info.append(botName, sub);

      const actions = document.createElement('div');
      actions.className = 'bot-list-actions';

      const manageBtn = document.createElement('button');
      manageBtn.className = 'btn btn-ghost btn-sm';
      manageBtn.textContent = '🔧 Kelola';
      manageBtn.addEventListener('click', () => {
        const input = this.el.querySelector('#mgr-username') as HTMLInputElement | null;
        if (input) input.value = name;
        this.switchMainTab('manage');
      });

      const revokeBtn = document.createElement('button');
      revokeBtn.className = 'btn btn-ghost btn-sm';
      revokeBtn.textContent = '🔄 Revoke';
      revokeBtn.addEventListener('click', () => {
        const input = this.el.querySelector('#bf-rev-username') as HTMLInputElement | null;
        if (input) input.value = name;
        this.switchMainTab('revoke');
      });

      actions.append(manageBtn, revokeBtn);
      item.append(avatar, info, actions);
      list.appendChild(item);
    }

    host.appendChild(list);
  }

  // ── Engine ────────────────────────────────────────────────────────────────
  private async startEngine() {
    const btn = this.el.querySelector('#ub-btn-start') as HTMLButtonElement;
    if (btn) { btn.disabled = true; btn.textContent = '⏳ Memulai...'; }
    try {
      await ubRequest('/api/userbot/start', 'POST');
      showToast('Engine berhasil dihidupkan', 'success');
      await this.mount();
    } catch (e: any) {
      // Tampilkan error spesifik dari Go backend (mis. API_ID kosong)
      showToast('Gagal: ' + e.message, 'error');
      // Re-render stopped state + tampilkan error di panel utama
      this.render('stopped');
      const mainEl = this.el.querySelector('#ub-main');
      if (mainEl) {
        const alert = document.createElement('div');
        alert.className = 'alert alert-error';
        alert.style.cssText = 'margin-top:12px;animation:fadeIn .3s ease';
        alert.textContent = `❌ Gagal menghidupkan engine: ${e.message}`;
        mainEl.appendChild(alert);
      }
    }
  }

  private async stopEngine() {
    try {
      await ubRequest('/api/userbot/stop', 'POST');
      showToast('Engine dihentikan', 'info');
      this.render('stopped');
    } catch (e: any) {
      showToast('Gagal: ' + e.message, 'error');
    }
  }

  // ── Auth ──────────────────────────────────────────────────────────────────
  private async sendCode() {
    const phone = (this.el.querySelector('#ub-phone') as HTMLInputElement)?.value?.trim();
    if (!phone) { showToast('Masukkan nomor telepon', 'error'); return; }
    const btn = this.el.querySelector('#ub-btn-sendcode') as HTMLButtonElement;
    btn.disabled = true; btn.textContent = '⏳';
    try {
      const res = await ubRequest('/api/userbot/auth/sendCode', 'POST', { phone });
      (this.el.querySelector('#ub-phone-code-hash') as HTMLInputElement).value = res.phoneCodeHash || '';
      (this.el.querySelector('#auth-otp-step') as HTMLElement).style.display = '';
      showToast('Kode OTP dikirim ke Telegram!', 'success');
    } catch (e: any) { this.showAuthErr(e.message); }
    finally { btn.disabled = false; btn.textContent = 'Kirim OTP'; }
  }

  private async signIn() {
    const code = (this.el.querySelector('#ub-otp') as HTMLInputElement)?.value?.trim();
    const hash = (this.el.querySelector('#ub-phone-code-hash') as HTMLInputElement)?.value;
    if (!code) { showToast('Masukkan kode OTP', 'error'); return; }
    const btn = this.el.querySelector('#ub-btn-signin') as HTMLButtonElement;
    btn.disabled = true; btn.textContent = '⏳';
    try {
      await ubRequest('/api/userbot/auth/signIn', 'POST', { code, phoneCodeHash: hash });
      showToast('Login berhasil!', 'success');
      await this.mount();
    } catch (e: any) {
      if (e.message === '2FA_REQUIRED') {
        (this.el.querySelector('#auth-2fa-step') as HTMLElement).style.display = '';
        showToast('Diperlukan password 2FA', 'info');
      } else { this.showAuthErr(e.message); }
    } finally { btn.disabled = false; btn.textContent = 'Verifikasi'; }
  }

  private async signIn2FA() {
    const pw = (this.el.querySelector('#ub-2fa') as HTMLInputElement)?.value;
    if (!pw) { showToast('Masukkan password 2FA', 'error'); return; }
    const btn = this.el.querySelector('#ub-btn-2fa') as HTMLButtonElement;
    btn.disabled = true; btn.textContent = '⏳';
    try {
      await ubRequest('/api/userbot/auth/signIn2FA', 'POST', { password: pw });
      showToast('Login berhasil!', 'success');
      await this.mount();
    } catch (e: any) { this.showAuthErr(e.message); }
    finally { btn.disabled = false; btn.textContent = 'Masuk'; }
  }

  private showAuthErr(msg: string) {
    const el = this.el.querySelector('#ub-auth-error') as HTMLElement;
    if (el) { el.style.display = ''; el.textContent = '❌ ' + msg; }
    showToast(msg, 'error');
  }

  // ── BotFather ─────────────────────────────────────────────────────────────
  private async createBot() {
    const name     = (this.el.querySelector('#bf-name') as HTMLInputElement)?.value?.trim();
    const username = (this.el.querySelector('#bf-username') as HTMLInputElement)?.value?.trim();
    if (!name || !username) { showToast('Isi nama dan username bot', 'error'); return; }
    const btn = this.el.querySelector('#bf-btn-create') as HTMLButtonElement;
    btn.disabled = true; btn.textContent = '⏳ Bernegosiasi dengan @BotFather...';
    try {
      const res = await ubRequest('/api/userbot/botfather/create', 'POST', { name, username });
      this.renderTokenResult('#bf-create-result', '✅ Bot Berhasil Dibuat', res.token, [
        { label: 'Nama', value: name },
        { label: 'Username', value: `@${res.username}` },
      ]);
      showToast('Bot baru siap!', 'success');
    } catch (e: any) { showToast('Gagal: ' + e.message, 'error'); }
    finally { btn.disabled = false; btn.textContent = '🤖 Buat Bot via @BotFather'; }
  }

  private async listBots() {
    const el = this.el.querySelector('#bf-list-result') as HTMLElement;
    el.innerHTML = '<div class="spinner" style="margin:32px auto"></div>';
    try {
      const res = await ubRequest('/api/userbot/botfather/list');
      const bots: string[] = res.bots || [];
      this.renderListBotsResult(bots, res.raw || '');
      showToast(`${bots.length} bot ditemukan`, 'success');
    } catch (e: any) {
      el.textContent = '';
      const alert = document.createElement('div');
      alert.className = 'alert alert-error';
      alert.textContent = e.message;
      el.appendChild(alert);
    }
  }

  private async deleteBot() {
    const username = (this.el.querySelector('#bf-del-username') as HTMLInputElement)?.value?.trim();
    if (!username) { showToast('Masukkan username bot', 'error'); return; }
    if (!confirm(`Hapus bot ${username} secara permanen?`)) return;
    const btn = this.el.querySelector('#bf-btn-delete') as HTMLButtonElement;
    btn.disabled = true; btn.textContent = '⏳ Menghapus...';
    try {
      await ubRequest('/api/userbot/botfather/delete', 'POST', { username });
      showToast(`Bot ${username} berhasil dihapus`, 'success');
      (this.el.querySelector('#bf-del-username') as HTMLInputElement).value = '';
    } catch (e: any) { showToast('Gagal: ' + e.message, 'error'); }
    finally { btn.disabled = false; btn.textContent = '🗑 Hapus Permanen'; }
  }

  private async revokeToken() {
    const username = (this.el.querySelector('#bf-rev-username') as HTMLInputElement)?.value?.trim();
    if (!username) { showToast('Masukkan username bot', 'error'); return; }
    if (!confirm(`Revoke token ${username}? Token lama langsung tidak valid.`)) return;
    const btn = this.el.querySelector('#bf-btn-revoke') as HTMLButtonElement;
    btn.disabled = true; btn.textContent = '⏳ Merevoke...';
    try {
      const res = await ubRequest('/api/userbot/botfather/revoke', 'POST', { username });
      this.renderTokenResult('#bf-revoke-result', '✅ Token Baru', res.token);
      showToast('Token berhasil diperbarui!', 'success');
    } catch (e: any) { showToast('Gagal: ' + e.message, 'error'); }
    finally { btn.disabled = false; btn.textContent = '🔄 Regenerate Token'; }
  }

  // ── Manage Bot Panel ─────────────────────────────────────────────────────

  private mgrUsername(): string {
    return (this.el.querySelector('#mgr-username') as HTMLInputElement)?.value?.trim() || '';
  }

  private showMgrResult(id: string, message: string, detail?: { kind: 'strong' | 'code'; value: string }) {
    const el = this.el.querySelector(`#mgr-${id}-result`) as HTMLElement;
    if (!el) return;
    el.style.display = '';
    el.textContent = '';

    const alert = document.createElement('div');
    alert.className = 'alert alert-info';
    alert.appendChild(document.createTextNode(message));
    if (detail) {
      alert.appendChild(document.createTextNode(' '));
      const node = document.createElement(detail.kind);
      node.textContent = detail.value;
      if (detail.kind === 'code') {
        node.style.fontSize = '.8rem';
      }
      alert.appendChild(node);
    }
    el.appendChild(alert);
  }

  /** Validasi username dan tampilkan sub-panel (tanpa API call) */
  private async loadBotPanel() {
    const username = this.mgrUsername();
    if (!username) { showToast('Masukkan username bot terlebih dahulu (@username)', 'error'); return; }
    // Validasi format dasar
    const clean = username.replace(/^@/, '');
    if (clean.length < 3 || !/^[a-zA-Z0-9_]+$/.test(clean)) {
      showToast('Format username tidak valid. Contoh: @nama_bot', 'error'); return;
    }
    const panel = this.el.querySelector('#mgr-panel') as HTMLElement;
    if (panel) panel.style.display = '';
    showToast(`Panel manajemen untuk @${clean} dibuka`, 'success');
  }

  private async getBotToken() {
    const username = this.mgrUsername();
    if (!username) { showToast('Muat bot terlebih dahulu', 'error'); return; }
    const btn = this.el.querySelector('#mgr-btn-token') as HTMLButtonElement;
    btn.disabled = true; btn.textContent = '⏳';
    try {
      const res = await ubRequest('/api/userbot/botfather/token', 'POST', { username });
      this.renderTokenResult('#mgr-token-result', '🔑 Token API', res.token);
    } catch (e: any) { showToast('Gagal: ' + e.message, 'error'); }
    finally { btn.disabled = false; btn.textContent = '🔑 Tampilkan Token'; }
  }

  private async setBotName() {
    const username = this.mgrUsername();
    const name = (this.el.querySelector('#mgr-new-name') as HTMLInputElement)?.value?.trim();
    if (!username || !name) { showToast('Isi username dan nama baru', 'error'); return; }
    const btn = this.el.querySelector('#mgr-btn-name') as HTMLButtonElement;
    btn.disabled = true; btn.textContent = '⏳';
    try {
      await ubRequest('/api/userbot/botfather/setname', 'POST', { username, name });
      this.showMgrResult('name', '✅ Nama berhasil diubah ke', { kind: 'strong', value: name });
      showToast('Nama bot diperbarui!', 'success');
    } catch (e: any) { showToast('Gagal: ' + e.message, 'error'); }
    finally { btn.disabled = false; btn.textContent = '✏️ Ubah Nama'; }
  }

  private async setBotDescription() {
    const username = this.mgrUsername();
    const description = (this.el.querySelector('#mgr-new-desc') as HTMLTextAreaElement)?.value?.trim();
    if (!username || !description) { showToast('Isi username dan deskripsi', 'error'); return; }
    const btn = this.el.querySelector('#mgr-btn-desc') as HTMLButtonElement;
    btn.disabled = true; btn.textContent = '⏳';
    try {
      await ubRequest('/api/userbot/botfather/setdescription', 'POST', { username, description });
      this.showMgrResult('desc', '✅ Deskripsi berhasil diperbarui');
      showToast('Deskripsi diperbarui!', 'success');
    } catch (e: any) { showToast('Gagal: ' + e.message, 'error'); }
    finally { btn.disabled = false; btn.textContent = '📝 Set Deskripsi'; }
  }

  private async setBotAbout() {
    const username = this.mgrUsername();
    const about = (this.el.querySelector('#mgr-new-about') as HTMLTextAreaElement)?.value?.trim();
    if (!username || !about) { showToast('Isi username dan teks about', 'error'); return; }
    const btn = this.el.querySelector('#mgr-btn-about') as HTMLButtonElement;
    btn.disabled = true; btn.textContent = '⏳';
    try {
      await ubRequest('/api/userbot/botfather/setabout', 'POST', { username, about });
      this.showMgrResult('about', '✅ Teks About berhasil diperbarui');
      showToast('About diperbarui!', 'success');
    } catch (e: any) { showToast('Gagal: ' + e.message, 'error'); }
    finally { btn.disabled = false; btn.textContent = 'ℹ️ Set About'; }
  }

  private async setBotCommands() {
    const username = this.mgrUsername();
    const raw = (this.el.querySelector('#mgr-new-cmds') as HTMLTextAreaElement)?.value?.trim();
    if (!username || !raw) { showToast('Isi username dan daftar perintah', 'error'); return; }

    // Parse: "/cmd - desc" per line
    const commands = raw.split('\n').map(l => l.trim()).filter(Boolean).map(l => {
      const m = l.match(/^\/?([\w]+)\s*[-–]\s*(.+)$/);
      return m ? { cmd: m[1], desc: m[2].trim() } : null;
    }).filter(Boolean) as { cmd: string; desc: string }[];

    if (commands.length === 0) { showToast('Format salah. Contoh: /start - Mulai bot', 'error'); return; }

    const btn = this.el.querySelector('#mgr-btn-cmds') as HTMLButtonElement;
    btn.disabled = true; btn.textContent = '⏳';
    try {
      await ubRequest('/api/userbot/botfather/setcommands', 'POST', { username, commands });
      this.showMgrResult('cmds', `✅ ${commands.length} perintah berhasil didaftarkan:`, {
        kind: 'code',
        value: commands.map(c => '/' + c.cmd).join(', '),
      });
      showToast('Commands berhasil diset!', 'success');
    } catch (e: any) { showToast('Gagal: ' + e.message, 'error'); }
    finally { btn.disabled = false; btn.textContent = '⌨️ Set Commands'; }
  }
}
