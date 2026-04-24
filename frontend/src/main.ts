// main.ts v3 — Auth guard + routing + dual theme + mobile nav
import './style.css';
import { authStore, api } from './api/client.js';
import { StatusBar } from './components/StatusBar.js';
import { LoginPage } from './pages/LoginPage.js';
import { Dashboard } from './pages/Dashboard.js';
import { ConfigPage } from './pages/ConfigPage.js';
import { GatewayPage } from './pages/GatewayPage.js';
import { BotFatherPage } from './pages/BotFatherPage.js';
import { CodeToolsPage } from './pages/CodeToolsPage.js';
import { OpenClawCLIPage } from './pages/OpenClawCLIPage.js';
import { showToast } from './components/Toast.js';

interface Page {
  getElement(): HTMLElement;
  mount(): void | Promise<void>;
  unmount(): void;
}

let currentPage: Page | null = null;
let currentPageId = '';

const routes: Record<string, () => Page> = {
  dashboard:  () => new Dashboard(),
  config:     () => new ConfigPage(),
  gateway:    () => new GatewayPage(),
  botfather:  () => new BotFatherPage(),
  codetools:  () => new CodeToolsPage(),
  openclawcli: () => new OpenClawCLIPage(),
};

const appEl = document.getElementById('app')!;
let mainContent: HTMLElement;

// ── Theme ──────────────────────────────────────────────────────────────────────
function getTheme(): 'dark' | 'light' {
  return (localStorage.getItem('theme') as 'dark' | 'light') || 'dark';
}
function applyTheme(theme: 'dark' | 'light') {
  document.documentElement.setAttribute('data-theme', theme);
  localStorage.setItem('theme', theme);
}
function toggleTheme() {
  const next = getTheme() === 'dark' ? 'light' : 'dark';
  applyTheme(next);
  const btn = document.getElementById('theme-toggle-btn');
  if (btn) btn.textContent = next === 'dark' ? '☀️ Light' : '🌙 Dark';
}

// Apply on boot
applyTheme(getTheme());

// ── App Shell ─────────────────────────────────────────────────────────────────
function buildAppShell() {
  appEl.innerHTML = '';

  const statusBar = new StatusBar();
  statusBar.start();
  appEl.appendChild(statusBar.getElement());

  // Theme toggle (fixed top-right)
  const themeBtn = document.createElement('button');
  themeBtn.id = 'theme-toggle-btn';
  themeBtn.className = 'theme-toggle';
  themeBtn.textContent = getTheme() === 'dark' ? '☀️ Light' : '🌙 Dark';
  themeBtn.onclick = toggleTheme;
  appEl.appendChild(themeBtn);

  const layout = document.createElement('div');
  layout.className = 'app-layout';

  // ── Sidebar ────────────────────────────────────────────────────────────────
  const sidebar = document.createElement('nav');
  sidebar.id = 'main-sidebar';
  sidebar.className = 'sidebar';
  sidebar.innerHTML = `
    <button class="sidebar-close-btn" id="sidebar-close" aria-label="Tutup menu">✕</button>
    <div class="sidebar-logo">
      <div class="logo-icon">🦾</div>
      <div>
        <div class="logo-text">OpenClaw</div>
        <div class="logo-sub">Multi-Agent Platform</div>
      </div>
    </div>
    <div class="sidebar-nav">
      <div class="nav-label">Platform</div>
      <div class="nav-item active" data-page="dashboard" id="nav-dashboard" tabindex="0" role="button">
        <span class="nav-icon">🤖</span><span>Agents</span>
      </div>
      <div class="nav-item" data-page="gateway" id="nav-gateway" tabindex="0" role="button">
        <span class="nav-icon">🔀</span><span>Gateway</span>
      </div>
      <div class="nav-item" data-page="botfather" id="nav-botfather" tabindex="0" role="button">
        <span class="nav-icon">🤖</span><span>BotFather Setup</span>
      </div>
      <div class="nav-item" data-page="codetools" id="nav-codetools" tabindex="0" role="button">
        <span class="nav-icon">💻</span><span>Code Tools</span>
      </div>
      <div class="nav-item" data-page="openclawcli" id="nav-openclawcli" tabindex="0" role="button">
        <span class="nav-icon">⌘</span><span>OpenClaw CLI</span>
      </div>
      <div class="nav-item" data-page="config" id="nav-config" tabindex="0" role="button">
        <span class="nav-icon">⚙️</span><span>Konfigurasi</span>
      </div>
      <div class="nav-label" style="margin-top:24px">Docs</div>
      <a class="nav-item" href="https://docs.openclaw.ai/concepts/multi-agent" target="_blank" rel="noopener">
        <span class="nav-icon">📖</span><span>Multi-Agent Docs</span>
      </a>
      <a class="nav-item" href="https://docs.openclaw.ai/concepts/soul" target="_blank" rel="noopener">
        <span class="nav-icon">🧬</span><span>SOUL.md Guide</span>
      </a>
    </div>
    <div class="sidebar-footer">
      <div>OpenClaw Multi-Agent</div>
      <div style="margin-top:4px;color:var(--clr-accent-1)">v2.0.0</div>
      <div style="margin-top:4px;font-size:0.68rem">Go + Node.js + Vite/TS</div>
      <button class="btn btn-ghost btn-sm" id="btn-logout" style="margin-top:12px;width:100%">🚪 Logout</button>
    </div>
  `;

  sidebar.querySelectorAll('.nav-item[data-page]').forEach(item => {
    item.addEventListener('click', () => {
      navigate(item.getAttribute('data-page')!);
      closeMobileSidebar();
    });
    item.addEventListener('keydown', (e) => {
      if ((e as KeyboardEvent).key === 'Enter') navigate(item.getAttribute('data-page')!);
    });
  });

  sidebar.querySelector('#btn-logout')?.addEventListener('click', async () => {
    try { await api.logout(); } catch {}
    authStore.clearToken();
    showToast('Logout berhasil', 'info');
    showLoginPage();
  });

  sidebar.querySelector('#sidebar-close')?.addEventListener('click', closeMobileSidebar);

  const mainWrapper = document.createElement('div');
  mainWrapper.className = 'main-content';
  mainContent = document.createElement('div');
  mainContent.id = 'page-content';
  mainContent.style.flex = '1';
  mainWrapper.appendChild(mainContent);

  layout.append(sidebar, mainWrapper);
  appEl.appendChild(layout);

  // ── Mobile Bottom Nav ──────────────────────────────────────────────────────
  const bottomNav = document.createElement('div');
  bottomNav.className = 'mobile-bottom-nav';
  bottomNav.id = 'mobile-bottom-nav';
  bottomNav.innerHTML = `
    <button class="mobile-nav-item active" data-page="dashboard" id="mobile-nav-dashboard">
      <span class="nav-emoji">🤖</span><span>Agents</span>
    </button>
    <button class="mobile-nav-item" data-page="gateway" id="mobile-nav-gateway">
      <span class="nav-emoji">🔀</span><span>Gateway</span>
    </button>
    <button class="mobile-nav-item" data-page="botfather" id="mobile-nav-botfather">
      <span class="nav-emoji">🤖</span><span>BotFather</span>
    </button>
    <button class="mobile-nav-item" data-page="config" id="mobile-nav-config">
      <span class="nav-emoji">⚙️</span><span>Config</span>
    </button>
    <button class="mobile-hamburger" id="mobile-menu-btn" aria-label="Menu">☰</button>
  `;
  bottomNav.querySelectorAll('.mobile-nav-item[data-page]').forEach(item => {
    item.addEventListener('click', () => navigate(item.getAttribute('data-page')!));
  });
  bottomNav.querySelector('#mobile-menu-btn')?.addEventListener('click', openMobileSidebar);
  appEl.appendChild(bottomNav);
}

function openMobileSidebar() {
  document.getElementById('main-sidebar')?.classList.add('mobile-open');
}
function closeMobileSidebar() {
  document.getElementById('main-sidebar')?.classList.remove('mobile-open');
}

function navigate(pageId: string) {
  if (pageId === currentPageId) {
    closeMobileSidebar();
    return;
  }
  const factory = routes[pageId];
  if (!factory) return navigate('dashboard');

  if (currentPage) { currentPage.unmount(); mainContent.innerHTML = ''; }

  // Update sidebar active
  document.querySelectorAll('.nav-item[data-page]').forEach(el =>
    el.classList.toggle('active', el.getAttribute('data-page') === pageId)
  );
  // Update bottom nav active
  document.querySelectorAll('.mobile-nav-item[data-page]').forEach(el =>
    el.classList.toggle('active', el.getAttribute('data-page') === pageId)
  );

  currentPage = factory();
  currentPageId = pageId;
  mainContent.appendChild(currentPage.getElement());
  currentPage.mount();
  history.replaceState(null, '', `#${pageId}`);
}

function showLoginPage() {
  appEl.innerHTML = '';
  currentPage = null;
  currentPageId = '';

  const loginPage = new LoginPage(() => {
    buildAppShell();
    const initialPage = window.location.hash.replace('#', '') || 'dashboard';
    navigate(initialPage);
  });

  appEl.appendChild(loginPage.getElement());
  loginPage.mount();
}

async function boot() {
  try {
    await api.me();
    buildAppShell();
    const initialPage = window.location.hash.replace('#', '') || 'dashboard';
    navigate(initialPage);
  } catch {
    showLoginPage();
  }
}

boot();
