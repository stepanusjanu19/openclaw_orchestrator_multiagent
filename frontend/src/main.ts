// main.ts v3 — Auth guard + routing + dual theme + mobile nav
import './style.css';
import {
  BookOpen,
  Bot,
  FileText,
  MapPin,
  Menu,
  Router,
  Settings2,
  ShieldCheck,
  SquareTerminal,
  UsersRound,
  Webhook,
  Wrench,
  X,
  createElement,
} from 'lucide';
import { authStore, api } from './api/client.js';
import { StatusBar } from './components/StatusBar.js';
import { LoginPage } from './pages/LoginPage.js';
import { Dashboard } from './pages/Dashboard.js';
import { ConfigPage } from './pages/ConfigPage.js';
import { GatewayPage } from './pages/GatewayPage.js';
import { BotFatherPage } from './pages/BotFatherPage.js';
import { CodeToolsPage } from './pages/CodeToolsPage.js';
import { OpenClawCLIPage } from './pages/OpenClawCLIPage.js';
import { AIRouterDashboardPage } from './pages/AIRouterDashboardPage.js';
import { SecurityPage } from './pages/SecurityPage.js';
import { VisitorPage } from './pages/VisitorPage.js';
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
  airouter:   () => new AIRouterDashboardPage(),
  openclawcli: () => new OpenClawCLIPage(),
  security:   () => new SecurityPage(),
  visitor:    () => new VisitorPage(),
};

const appEl = document.getElementById('app')!;
let mainContent: HTMLElement;
let routerNavRefreshTimer: ReturnType<typeof setInterval> | null = null;

const navIcons = {
  dashboard: UsersRound,
  gateway: Webhook,
  visitor: MapPin,
  botfather: Bot,
  codetools: Wrench,
  airouter: Router,
  openclawcli: SquareTerminal,
  config: Settings2,
  security: ShieldCheck,
  docs: BookOpen,
  soul: FileText,
  menu: Menu,
  close: X,
};

function iconMarkup(iconNode: Parameters<typeof createElement>[0], className = 'nav-svg'): string {
  const svg = createElement(iconNode, { width: 18, height: 18 });
  svg.classList.add(className);
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('focusable', 'false');
  return svg.outerHTML;
}

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
  if (btn) {
    btn.textContent = next === 'dark' ? 'Light mode' : 'Dark mode';
    btn.setAttribute('aria-label', next === 'dark' ? 'Switch to light theme' : 'Switch to dark theme');
  }
}

// Apply on boot
applyTheme(getTheme());

// ── App Shell ─────────────────────────────────────────────────────────────────
function buildAppShell() {
  appEl.innerHTML = '';
  if (routerNavRefreshTimer) clearInterval(routerNavRefreshTimer);

  const statusBar = new StatusBar();
  statusBar.start();
  appEl.appendChild(statusBar.getElement());

  // Theme toggle (fixed top-right)
  const themeBtn = document.createElement('button');
  themeBtn.id = 'theme-toggle-btn';
  themeBtn.className = 'theme-toggle';
  themeBtn.textContent = getTheme() === 'dark' ? 'Light mode' : 'Dark mode';
  themeBtn.setAttribute('aria-label', getTheme() === 'dark' ? 'Switch to light theme' : 'Switch to dark theme');
  themeBtn.onclick = toggleTheme;
  appEl.appendChild(themeBtn);

  const layout = document.createElement('div');
  layout.className = 'app-layout';

  // ── Sidebar ────────────────────────────────────────────────────────────────
  const sidebar = document.createElement('nav');
  sidebar.id = 'main-sidebar';
  sidebar.className = 'sidebar';
  sidebar.innerHTML = `
    <button class="sidebar-close-btn" id="sidebar-close" aria-label="Tutup menu">${iconMarkup(navIcons.close)}</button>
    <div class="sidebar-logo">
      <div class="logo-icon"><img src="/openclaw-mark.svg" alt="" aria-hidden="true" /></div>
      <div>
        <div class="logo-text">OpenClaw</div>
        <div class="logo-sub">Control Platform</div>
      </div>
    </div>
    <div class="sidebar-nav">
      <div class="nav-label">Platform</div>
      <div class="nav-item active" data-page="dashboard" id="nav-dashboard" tabindex="0" role="button">
        <span class="nav-icon">${iconMarkup(navIcons.dashboard)}</span><span>Agents</span>
      </div>
      <div class="nav-item" data-page="gateway" id="nav-gateway" tabindex="0" role="button">
        <span class="nav-icon">${iconMarkup(navIcons.gateway)}</span><span>Gateway</span>
      </div>
      <div class="nav-item" data-page="visitor" id="nav-visitor" tabindex="0" role="button">
        <span class="nav-icon">${iconMarkup(navIcons.visitor)}</span><span>Visitor</span>
      </div>
      <div class="nav-label nav-label-spaced">Operations</div>
      <div class="nav-item" data-page="botfather" id="nav-botfather" tabindex="0" role="button">
        <span class="nav-icon">${iconMarkup(navIcons.botfather)}</span><span>BotFather Setup</span>
      </div>
      <div class="nav-item" data-page="codetools" id="nav-codetools" tabindex="0" role="button">
        <span class="nav-icon">${iconMarkup(navIcons.codetools)}</span><span>Code Tools</span>
      </div>
      <div id="dynamic-airouter-nav"></div>
      <div class="nav-item" data-page="openclawcli" id="nav-openclawcli" tabindex="0" role="button">
        <span class="nav-icon">${iconMarkup(navIcons.openclawcli)}</span><span>OpenClaw CLI</span>
      </div>
      <div class="nav-label nav-label-spaced">Settings</div>
      <div class="nav-item" data-page="config" id="nav-config" tabindex="0" role="button">
        <span class="nav-icon">${iconMarkup(navIcons.config)}</span><span>Konfigurasi</span>
      </div>
      <div class="nav-item" data-page="security" id="nav-security" tabindex="0" role="button">
        <span class="nav-icon">${iconMarkup(navIcons.security)}</span><span>Security</span>
      </div>
      <div class="nav-label nav-label-spaced">Docs</div>
      <a class="nav-item" href="https://docs.openclaw.ai/concepts/multi-agent" target="_blank" rel="noopener">
        <span class="nav-icon">${iconMarkup(navIcons.docs)}</span><span>Multi-Agent Docs</span>
      </a>
      <a class="nav-item" href="https://docs.openclaw.ai/concepts/soul" target="_blank" rel="noopener">
        <span class="nav-icon">${iconMarkup(navIcons.soul)}</span><span>SOUL.md Guide</span>
      </a>
    </div>
    <div class="sidebar-footer">
      <div class="sidebar-product">OpenClaw Multi-Agent</div>
      <div class="sidebar-version">v2.0.0</div>
      <div class="sidebar-runtime">Go + Node.js + Vite/TS</div>
      <button class="btn btn-ghost btn-sm sidebar-logout" id="btn-logout">Logout</button>
    </div>
  `;

  sidebar.querySelectorAll<HTMLElement>('.nav-item[data-page]').forEach(item => bindNavItem(item));
  refreshAIRouterNav();
  routerNavRefreshTimer = setInterval(refreshAIRouterNav, 8000);

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
      <span class="nav-emoji">${iconMarkup(navIcons.dashboard)}</span><span>Agents</span>
    </button>
    <button class="mobile-nav-item" data-page="gateway" id="mobile-nav-gateway">
      <span class="nav-emoji">${iconMarkup(navIcons.gateway)}</span><span>Gateway</span>
    </button>
    <button class="mobile-nav-item" data-page="visitor" id="mobile-nav-visitor">
      <span class="nav-emoji">${iconMarkup(navIcons.visitor)}</span><span>Visitor</span>
    </button>
    <button class="mobile-nav-item" data-page="codetools" id="mobile-nav-codetools">
      <span class="nav-emoji">${iconMarkup(navIcons.codetools)}</span><span>Tools</span>
    </button>
    <button class="mobile-nav-item" data-page="config" id="mobile-nav-config">
      <span class="nav-emoji">${iconMarkup(navIcons.config)}</span><span>Config</span>
    </button>
    <button class="mobile-hamburger" id="mobile-menu-btn" aria-label="Menu">${iconMarkup(navIcons.menu, 'nav-svg')}</button>
  `;
  bottomNav.querySelectorAll<HTMLElement>('.mobile-nav-item[data-page]').forEach(item => bindNavItem(item));
  bottomNav.querySelector('#mobile-menu-btn')?.addEventListener('click', openMobileSidebar);
  appEl.appendChild(bottomNav);
}

function bindNavItem(item: HTMLElement) {
  if (item.dataset.bound === 'true') return;
  item.dataset.bound = 'true';
  item.addEventListener('click', () => {
    navigate(item.getAttribute('data-page')!);
    closeMobileSidebar();
  });
  item.addEventListener('keydown', (e) => {
    if ((e as KeyboardEvent).key === 'Enter') navigate(item.getAttribute('data-page')!);
  });
}

async function refreshAIRouterNav() {
  const slot = document.getElementById('dynamic-airouter-nav');
  if (!slot) return;
  try {
    const res = await api.getCodeToolsStatus();
    const hasRunningRouter = res.tools.some(tool =>
      (tool.tool === '9router' || tool.tool === 'omniroute') && tool.running
    );
    if (!hasRunningRouter) {
      slot.innerHTML = '';
      return;
    }
    if (!slot.querySelector('#nav-airouter')) {
      slot.innerHTML = `
        <div class="nav-item" data-page="airouter" id="nav-airouter" tabindex="0" role="button">
          <span class="nav-icon">${iconMarkup(navIcons.airouter)}</span><span>AI Router</span>
        </div>
      `;
      const item = slot.querySelector<HTMLElement>('#nav-airouter');
      if (item) bindNavItem(item);
    }
    slot.querySelector('.nav-item')?.classList.toggle('active', currentPageId === 'airouter');
  } catch {
    slot.innerHTML = '';
  }
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
  refreshAIRouterNav();
}

function showLoginPage() {
  appEl.innerHTML = '';
  currentPage = null;
  currentPageId = '';
  if (routerNavRefreshTimer) {
    clearInterval(routerNavRefreshTimer);
    routerNavRefreshTimer = null;
  }

  const loginPage = new LoginPage(() => {
    buildAppShell();
    const initialPage = window.location.hash.replace('#', '') || 'dashboard';
    navigate(initialPage);
  });

  appEl.appendChild(loginPage.getElement());
  loginPage.mount();
}

window.addEventListener('openclaw:router-status-changed', () => refreshAIRouterNav());
window.addEventListener('hashchange', () => {
  if (!mainContent) return;
  const nextPage = window.location.hash.replace('#', '') || 'dashboard';
  navigate(nextPage);
});

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
