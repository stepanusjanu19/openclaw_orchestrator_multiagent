// pages/ConfigPage.ts
// Halaman konfigurasi: info path, openclaw config reference.
import { api } from '../api/client.js';
import { showToast } from '../components/Toast.js';

export class ConfigPage {
  private el: HTMLDivElement;

  constructor() {
    this.el = document.createElement('div');
    this.build();
  }

  getElement() { return this.el; }
  async mount() {
    await Promise.allSettled([
      this.loadHardenAssistant(),
      this.loadRouterStatus(),
    ]);
  }
  unmount() {}

  private async loadHardenAssistant() {
    const root = this.el.querySelector('#harden-assistant-root') as HTMLDivElement;
    if (!root) return;

    try {
      const resp = await api.hardenPreview();
      const tasksHtml = resp.tasks.map((t: any) => `
        <label style="display:flex;align-items:flex-start;gap:12px;padding:12px;border:1px solid var(--clr-border-light);border-radius:6px;background:var(--clr-background);cursor:pointer;margin-bottom:8px;">
          <input type="checkbox" name="hardenTask" value="${t.id}" checked style="margin-top:4px;accent-color:var(--clr-accent-1);">
          <div style="flex:1;">
            <div style="font-weight:600;font-size:0.9rem;margin-bottom:2px;">${t.title}</div>
            <div style="font-size:0.8rem;color:var(--clr-text-secondary);">${t.description}</div>
          </div>
        </label>
      `).join('');

      root.innerHTML = `
        <div style="margin-bottom:16px;">
          <h4 style="margin:0 0 8px 0;font-size:0.85rem;color:var(--clr-text-muted);">OS Detected: <span style="color:var(--clr-text);font-weight:600">${resp.os}</span></h4>
          ${tasksHtml}
        </div>
        <button id="btn-generate-harden" class="btn" style="width:100%;justify-content:center;background:var(--clr-accent-1);color:#fff;">
          Prepare Trusted Hardening Script
        </button>
        <div id="harden-result" style="margin-top:16px;display:none;"></div>
      `;

      const btn = root.querySelector('#btn-generate-harden') as HTMLButtonElement;
      btn.textContent = '⚡ Execute Configuration (Root)';
      btn.addEventListener('click', async () => {
        const checkboxes = root.querySelectorAll<HTMLInputElement>('input[name="hardenTask"]:checked');
        const selected = Array.from(checkboxes).map(c => c.value);

        if (selected.length === 0) {
          showToast('Pilih setidaknya satu komponen', 'error');
          return;
        }

        btn.disabled = true;
        btn.textContent = 'Executing Structured Tasks...';

        try {
          const genResp = await api.hardenExecute(selected);
          const resultDiv = root.querySelector('#harden-result') as HTMLDivElement;
          resultDiv.style.display = 'block';
          resultDiv.textContent = '';

          const box = document.createElement('div');
          box.style.cssText = 'background:rgba(16,185,129,0.1);border-left:3px solid #10b981;padding:12px;border-radius:4px;';

          const status = document.createElement('span');
          status.style.cssText = 'font-size:0.8rem;color:#10b981;font-weight:600';
          status.textContent = '✅ EKSEKUSI SUKSES';

          const message = document.createElement('div');
          message.style.cssText = 'color:var(--clr-text-secondary);font-size:0.8rem;margin-top:4px;';
          message.textContent = genResp.message;

          const logLabel = document.createElement('span');
          logLabel.style.cssText = 'font-size:0.75rem;color:var(--clr-text-muted);display:block;margin-top:8px;';
          logLabel.textContent = 'Log Output dari Backend:';

          const pre = document.createElement('pre');
          pre.style.cssText = 'margin-top:4px;font-family:var(--font-mono);font-size:0.82rem;background:var(--clr-bg-elevated);border:1px solid var(--clr-border);border-radius:var(--radius-sm);padding:8px 12px;color:var(--clr-accent-2);white-space:pre-wrap;word-break:break-all;max-height:300px;overflow-y:auto;';
          pre.textContent = genResp.logs;

          box.append(status, message, logLabel, pre);
          resultDiv.appendChild(box);
          showToast('Eksekusi konfigurasi selesai.');
        } catch (err: any) {
          showToast(`Structured hardening error: ${err.message}`, 'error');
        } finally {
          btn.disabled = false;
          btn.textContent = '⚡ Execute Configuration (Root)';
        }
      });

    } catch (err: any) {
      root.textContent = '';
      const msg = document.createElement('span');
      msg.style.cssText = 'color:#ef4444;font-size:0.8rem;';
      msg.textContent = `Gagal memuat tugas UI Assistant: ${err.message}`;
      root.appendChild(msg);
    }
  }

  private async loadRouterStatus() {
    const root = this.el.querySelector('#router-status-root') as HTMLDivElement;
    if (!root) return;
    root.textContent = '';

    try {
      const status = await api.getRouterStatus();
      const badge = document.createElement('span');
      badge.style.cssText = `display:inline-flex;align-items:center;gap:6px;padding:4px 10px;border-radius:999px;font-size:0.76rem;font-weight:700;border:1px solid ${status.enabled ? 'rgba(34,211,238,0.35)' : 'rgba(148,163,184,0.25)'};background:${status.enabled ? 'rgba(34,211,238,0.12)' : 'rgba(148,163,184,0.08)'};color:${status.enabled ? 'var(--clr-accent-2)' : 'var(--clr-text-muted)'};`;
      badge.textContent = status.agent_routing_enabled ? `Agent routing: ${status.provider}` : 'Agent routing: disabled';

      const grid = document.createElement('div');
      grid.style.cssText = 'display:grid;grid-template-columns:150px 1fr;gap:8px 14px;margin-top:14px;font-family:var(--font-mono);font-size:0.8rem;';
      const addRow = (label: string, value: string) => {
        const k = document.createElement('span');
        k.style.color = 'var(--clr-text-muted)';
        k.textContent = label;
        const v = document.createElement('span');
        v.style.color = 'var(--clr-accent-2)';
        v.style.wordBreak = 'break-all';
        v.textContent = value;
        grid.append(k, v);
      };
      addRow('base_url', status.masked_base_url || '-');
      addRow('agent_routing', status.agent_routing_enabled ? 'enabled' : 'disabled');
      addRow('reachable', status.reachable ? 'yes' : 'no');
      addRow('models', String(status.model_count || 0));
      addRow('loopback_only', status.require_loopback ? 'yes' : 'no');
      addRow('cloud_allowed', status.allow_cloud ? 'yes' : 'no');
      addRow('token', status.token_configured ? 'configured' : 'empty');
      addRow('tool_calls', status.tool_calls_allowed ? 'allowed' : 'blocked');

      root.appendChild(badge);
      root.appendChild(grid);

      const warnings = [...(status.warnings || [])];
      if (status.error) warnings.unshift(status.error);
      if (warnings.length > 0) {
        const warnBox = document.createElement('div');
        warnBox.style.cssText = 'margin-top:14px;padding:10px 12px;border-radius:6px;border:1px solid rgba(245,158,11,0.35);background:rgba(245,158,11,0.08);color:#fbbf24;font-size:0.8rem;line-height:1.55;';
        const title = document.createElement('div');
        title.style.fontWeight = '700';
        title.textContent = 'Security warnings';
        warnBox.appendChild(title);
        for (const warning of warnings.slice(0, 5)) {
          const item = document.createElement('div');
          item.textContent = `- ${warning}`;
          warnBox.appendChild(item);
        }
        root.appendChild(warnBox);
      }
    } catch (err: any) {
      const msg = document.createElement('span');
      msg.style.cssText = 'color:#ef4444;font-size:0.8rem;';
      msg.textContent = `Gagal memuat status AI router: ${err.message}`;
      root.appendChild(msg);
    }
  }

  private build() {
    const header = document.createElement('div');
    header.className = 'page-header';
    header.innerHTML = `
      <h1>Konfigurasi</h1>
      <p class="subtitle">Panduan setup credential dan OpenClaw agent config</p>
    `;

    const body = document.createElement('div');
    body.className = 'page-body';

    body.innerHTML = `
      <div style="max-width:760px;display:flex;flex-direction:column;gap:24px">

        <!-- Credential Setup -->
        <div class="card">
          <h3 style="margin-bottom:16px;display:flex;align-items:center;gap:8px">
            🔒 <span>Credential Setup</span>
          </h3>
          <p style="color:var(--clr-text-secondary);font-size:0.9rem;margin-bottom:16px">
            Semua credential dipisah dari logika kode dan disimpan di folder <code>credentials/</code>.
            File-file ini sudah ada di <code>.gitignore</code>.
          </p>

          <h4 style="margin-bottom:8px;margin-top:16px">1. Global Config</h4>
          <pre class="log-viewer" style="height:auto;padding:12px;font-size:0.8rem">credentials/.env

OPENCLAW_GW_URL=http://127.0.0.1:14000/v1/chat/completions
OPENCLAW_GW_TOKEN=&lt;token dari openclaw gateway&gt;
OLLAMA_DIRECT_URL=http://127.0.0.1:11434/api/chat
API_PORT=8080
BOTS_DIR=/path/to/multiagentp/bots
FRONTEND_ORIGIN=http://localhost:5173</pre>

          <p style="color:var(--clr-text-muted);font-size:0.8rem;margin-top:8px">
            <code>OLLAMA_DIRECT_URL</code> mendukung endpoint native <code>/api/chat</code> dan OpenAI-compatible <code>/v1/chat/completions</code>.
            Untuk Ollama lokal standar, gunakan <code>http://127.0.0.1:11434/api/chat</code>.
          </p>

          <h4 style="margin-bottom:8px;margin-top:16px">2. Per-Agent Credentials</h4>
          <p style="color:var(--clr-text-secondary);font-size:0.85rem;margin-bottom:8px">
            Simpan registry semua agent di <code>credentials/agents.json</code>:
          </p>
          <pre class="log-viewer" style="height:auto;padding:12px;font-size:0.8rem">{
  "version": 1,
  "agents": {
    "main": {
      "id": "main",
      "label": "Main Assistant",
      "telegram_bot_token": "&lt;token dari BotFather&gt;",
      "openclaw_model": "openclaw/main",
      "model_route": {}
    }
  }
}</pre>

          <div class="no-token-banner" style="margin-top:16px">
            ⚠️ Pastikan BOTS_DIR di <code>.env</code> diisi dengan path absolut ke folder <code>bots/</code>.
            File credential yang di-ignore Git tetap bisa terlihat di VS Code Explorer; gunakan
            <code>git check-ignore -v credentials/agents.json</code> untuk memverifikasi.
          </div>
        </div>

        <!-- AI Router Status -->
        <div class="card">
          <h3 style="margin-bottom:16px;display:flex;align-items:center;gap:8px">
            🧭 <span>AI Router Status</span>
          </h3>
          <p style="color:var(--clr-text-secondary);font-size:0.85rem;margin-bottom:12px;line-height:1.6">
            9Router/OmniRoute dapat berjalan sebagai runtime router private. Agent baru akan memakai router hanya jika
            <code>AI_ROUTER_ENABLED=true</code>; OpenClaw Gateway, relay webhook, Visitor, dan Gateway tetap memakai jalur existing.
          </p>
          <div style="background:rgba(34,211,238,0.06);border-left:3px solid var(--clr-accent-2);padding:12px;border-radius:4px;margin-bottom:16px">
            <span style="font-size:0.8rem;color:var(--clr-accent-2);font-weight:600">PRIVATE DOWNSTREAM ONLY</span><br/>
            <span style="color:var(--clr-text-secondary);font-size:0.8rem">
              Router running berarti proses tersedia. Agent routing enabled berarti agent mengirim chat completion ke router.
              Jangan expose port router ke internet.
            </span>
          </div>
          <div id="router-status-root">
            <span style="color:var(--clr-text-muted);font-size:0.8rem;">Loading router status...</span>
          </div>
        </div>

        <!-- Telegram Bot Setup -->
        <div class="card">
          <h3 style="margin-bottom:16px;display:flex;align-items:center;gap:8px">
            📱 <span>Setup Telegram Bots</span>
          </h3>
          <ol style="color:var(--clr-text-secondary);font-size:0.9rem;padding-left:20px;display:flex;flex-direction:column;gap:10px;line-height:1.7">
            <li>Buka Telegram dan cari <strong>@BotFather</strong></li>
            <li>Kirim <code>/newbot</code> → masukkan nama bot → dapat token</li>
            <li>Ulangi untuk setiap agent (main, dev, alpha) — masing-masing bot berbeda</li>
            <li>Paste token ke entry agent di <code>credentials/agents.json</code></li>
            <li>Restart backend: <code>cd backend && make run</code></li>
          </ol>
        </div>

        <!-- OpenClaw Multi-Agent Config -->
        <div class="card">
          <h3 style="margin-bottom:16px;display:flex;align-items:center;gap:8px">
            ⚙️ <span>OpenClaw Gateway Config</span>
          </h3>
          <p style="color:var(--clr-text-secondary);font-size:0.85rem;margin-bottom:12px">
            Tambahkan config berikut ke <code>~/.openclaw/openclaw.json</code>
            untuk mendaftarkan semua agent dan binding Telegram:
          </p>
          <pre class="log-viewer" style="height:auto;padding:12px;font-size:0.78rem;overflow-x:auto">{
  "agents": {
    "list": [
      { "id": "main",  "workspace": "~/.openclaw/workspace-main" },
      { "id": "dev",   "workspace": "~/.openclaw/workspace-dev" },
      { "id": "alpha", "workspace": "~/.openclaw/workspace-alpha" }
    ]
  },
  "bindings": [
    { "agentId": "main",  "match": { "channel": "telegram", "accountId": "main" } },
    { "agentId": "dev",   "match": { "channel": "telegram", "accountId": "dev" } },
    { "agentId": "alpha", "match": { "channel": "telegram", "accountId": "alpha" } }
  ],
  "channels": {
    "telegram": {
      "accounts": {
        "main":  { "botToken": "TOKEN_MAIN",  "dmPolicy": "pairing" },
        "dev":   { "botToken": "TOKEN_DEV",   "dmPolicy": "pairing" },
        "alpha": { "botToken": "TOKEN_ALPHA", "dmPolicy": "pairing" }
      }
    }
  }
}</pre>

          <p style="color:var(--clr-text-muted);font-size:0.8rem;margin-top:12px">
            Setelah edit config: <code>openclaw gateway restart</code>
          </p>
        </div>

        <!-- System & Security Hardening -->
        <div class="card" style="border:1px solid rgba(255,100,100,0.3)">
          <h3 style="margin-bottom:16px;display:flex;align-items:center;gap:8px">
            🛡️ <span>Secure UI Hardening Assistant</span>
          </h3>
          <p style="color:var(--clr-text-secondary);font-size:0.85rem;margin-bottom:12px;line-height:1.6">
            Pilih komponen OS yang ingin Anda keraskan (Hardening). Backend hanya mengeksekusi task allowlist
            terstruktur tanpa <code>bash -c</code> atau input shell bebas.
          </p>
          <div style="background:rgba(255,100,100,0.05);border-left:3px solid #ff4b4b;padding:12px;border-radius:4px;margin-bottom:16px">
            <span style="font-size:0.8rem;color:#ff8b8b;font-weight:600">⚠️ ROOT EXECUTION REQUIRED</span><br/>
            <span style="color:var(--clr-text-secondary);font-size:0.8rem">
              Jalankan <code>sudo -v</code> di terminal server terlebih dahulu. UI ini memakai <code>sudo -n</code>
              sehingga password root tidak pernah dikirim dari browser.
            </span>
          </div>

          <!-- Container dinamis untuk Hardening UI -->
          <div id="harden-assistant-root">
             <span style="color:var(--clr-text-muted);font-size:0.8rem;">Loading tasks...</span>
          </div>
        </div>

        <!-- Quick Commands -->
        <div class="card">
          <h3 style="margin-bottom:16px;display:flex;align-items:center;gap:8px">
            🚀 <span>Quick Commands</span>
          </h3>
          <div style="display:flex;flex-direction:column;gap:10px">
            ${[
              ['Install OpenClaw', 'curl -fsSL https://openclaw.ai/install.sh | bash'],
              ['Start OpenClaw Gateway', 'openclaw gateway start'],
              ['Add agent workspace', 'openclaw agents add main'],
              ['Start Go backend', 'cd backend && make run'],
              ['Start frontend dev', 'cd frontend && npm run dev'],
              ['Install bot dependencies', 'cd bots && npm install'],
              ['Start all (script)', 'bash scripts/start-all.sh'],
            ].map(([label, cmd]) => `
              <div style="display:flex;flex-direction:column;gap:4px">
                <span style="font-size:0.75rem;color:var(--clr-text-muted)">${label}</span>
                <pre style="font-family:var(--font-mono);font-size:0.82rem;background:var(--clr-bg-elevated);border:1px solid var(--clr-border);border-radius:var(--radius-sm);padding:8px 12px;color:var(--clr-accent-2);white-space:pre-wrap;word-break:break-all">${cmd}</pre>
              </div>
            `).join('')}
          </div>
        </div>

      </div>
    `;

    this.el.append(header, body);
  }
}
