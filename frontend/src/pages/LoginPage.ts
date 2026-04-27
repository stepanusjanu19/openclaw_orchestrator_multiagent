// pages/LoginPage.ts — Halaman login dashboard

import { api } from '../api/client.js';
import { showToast } from '../components/Toast.js';

export class LoginPage {
  private el: HTMLDivElement;
  private passwordInput!: HTMLInputElement;
  private loginBtn!: HTMLButtonElement;
  private onSuccess: () => void;

  constructor(onSuccess: () => void) {
    this.onSuccess = onSuccess;
    this.el = document.createElement('div');
    this.el.className = 'login-page';
    this.build();
  }

  getElement() { return this.el; }
  mount() { setTimeout(() => this.passwordInput.focus(), 100); }
  unmount() {}

  private build() {
    this.el.innerHTML = `
      <div class="login-bg">
        <div class="login-grid"></div>
      </div>
    `;

    const shell = document.createElement('div');
    shell.className = 'login-shell';

    const intro = document.createElement('section');
    intro.className = 'login-intro';
    intro.innerHTML = `
      <div class="login-kicker">OpenClaw Control</div>
      <h1>Multi-agent control plane yang ringkas dan terarah.</h1>
      <p>Kelola agent, gateway, security, visitor intelligence, dan code tools dari satu dashboard operasional.</p>
      <div class="login-feature-row">
        <span>Realtime</span>
        <span>Secure</span>
        <span>Operator-first</span>
      </div>
    `;

    const card = document.createElement('div');
    card.className = 'login-card';

    // Logo
    const logoEl = document.createElement('div');
    logoEl.className = 'login-logo';
    logoEl.innerHTML = `
      <div class="login-logo-icon"><img src="/openclaw-mark.svg" alt="" aria-hidden="true" /></div>
      <div>
        <div class="login-logo-title">OpenClaw</div>
        <div class="login-logo-sub">Control Platform</div>
      </div>
    `;

    // Headline
    const headline = document.createElement('div');
    headline.className = 'login-headline';
    headline.innerHTML = `
      <h1>Masuk ke dashboard</h1>
      <p>Gunakan password operator untuk membuka panel kontrol.</p>
    `;

    // Form
    const form = document.createElement('form');
    form.className = 'login-form';
    form.addEventListener('submit', (e) => { e.preventDefault(); this.handleLogin(); });

    const inputWrap = document.createElement('div');
    inputWrap.className = 'login-input-wrap';

    const label = document.createElement('label');
    label.htmlFor = 'login-password';
    label.className = 'login-label';
    label.textContent = 'Password';

    this.passwordInput = document.createElement('input');
    this.passwordInput.type = 'password';
    this.passwordInput.id = 'login-password';
    this.passwordInput.className = 'login-input';
    this.passwordInput.placeholder = 'Masukkan password dashboard';
    this.passwordInput.autocomplete = 'current-password';

    inputWrap.append(label, this.passwordInput);

    this.loginBtn = document.createElement('button');
    this.loginBtn.type = 'submit';
    this.loginBtn.className = 'btn btn-primary login-btn';
    this.loginBtn.textContent = 'Masuk';

    const hint = document.createElement('p');
    hint.className = 'login-hint';
    hint.innerHTML = `Jika masih memakai default <code>admin</code>, segera ubah <code>DASHBOARD_PASSWORD</code> di <code>credentials/.env</code> dan restart backend.`;

    form.append(inputWrap, this.loginBtn, hint);
    card.append(logoEl, headline, form);
    shell.append(intro, card);
    this.el.appendChild(shell);
  }

  private async handleLogin() {
    const password = this.passwordInput.value;
    if (!password) {
      showToast('Masukkan password terlebih dahulu', 'error');
      return;
    }

    this.loginBtn.disabled = true;
    this.loginBtn.innerHTML = '<span class="loading-spinner"></span> Memverifikasi...';

    try {
      await api.login(password);
      showToast('Login berhasil', 'success');
      this.onSuccess();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      if (msg === 'UNAUTHORIZED' || msg.includes('salah')) {
        showToast('Password salah', 'error');
        this.passwordInput.select();
      } else {
        showToast(`Error: ${msg}`, 'error');
      }
    } finally {
      this.loginBtn.disabled = false;
      this.loginBtn.textContent = 'Masuk';
    }
  }
}
