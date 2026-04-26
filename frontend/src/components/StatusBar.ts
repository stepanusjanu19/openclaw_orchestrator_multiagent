// components/StatusBar.ts — Top info bar: gateway status, uptime, agent count

import { api, type HealthInfo, type AgentInfo } from '../api/client.js';

type StatusBarState = {
  health: HealthInfo | null;
  agents: AgentInfo[];
  error: boolean;
};

export class StatusBar {
  private el: HTMLElement;
  private interval: ReturnType<typeof setInterval> | null = null;

  constructor() {
    this.el = document.createElement('div');
    this.el.className = 'status-bar';
    this.render({ health: null, agents: [], error: false });
  }

  getElement() { return this.el; }

  start() {
    this.refresh();
    this.interval = setInterval(() => this.refresh(), 10_000);
  }

  stop() {
    if (this.interval) clearInterval(this.interval);
  }

  private async refresh() {
    try {
      const [health, agents] = await Promise.all([api.health(), api.listAgents()]);
      this.render({ health, agents, error: false });
    } catch {
      this.render({ health: null, agents: [], error: true });
    }
  }

  private render(state: StatusBarState) {
    const running = state.agents.filter(a => a.status.running).length;
    const total   = state.agents.length;

    this.el.innerHTML = `
      <span class="status-item ${state.error ? 'err' : 'ok'}">
        <span class="status-dot"></span> Backend ${state.error ? 'offline' : 'online'}
      </span>
      <span class="status-item">
        Agents <strong>${running}/${total}</strong> running
      </span>
      ${state.health ? `
        <span class="status-item">
          Uptime ${state.health.uptime}
        </span>
        <span class="status-item">
          ${state.health.go_version}
        </span>
      ` : ''}
      <span class="spacer"></span>
      <span class="status-item status-product">
        OpenClaw Multi-Agent Platform v1.0
      </span>
    `;
  }
}
