// components/LogViewer.ts
// SSE-based real-time log viewer per agent.
// Subscribe ke GET /api/agents/:id/logs endpoint.

export class LogViewer {
  private container: HTMLDivElement;
  private pre: HTMLDivElement;
  private source: EventSource | null = null;
  private agentId: string;
  private maxLines = 300;
  private lineCount = 0;
  private autoScroll = true;

  constructor(agentId: string) {
    this.agentId = agentId;

    this.container = document.createElement('div');
    this.container.style.position = 'relative';

    // Toolbar
    const toolbar = document.createElement('div');
    toolbar.style.cssText = `
      display:flex; align-items:center; gap:8px;
      padding: 8px 12px;
      border: 1px solid var(--clr-border);
      border-bottom: none;
      border-radius: var(--radius-md) var(--radius-md) 0 0;
      background: var(--clr-bg-elevated);
      font-size: 0.75rem;
      font-family: var(--font-mono);
    `;

    const title = document.createElement('span');
    title.style.color = 'var(--clr-text-muted)';
    title.textContent = `● agent:${agentId} / stdout+stderr`;

    const spacer = document.createElement('span');
    spacer.style.flex = '1';

    const clearBtn = this.makeToolbarBtn('Clear', () => this.clear());
    const scrollBtn = this.makeToolbarBtn('↓ Auto-scroll', () => this.toggleAutoScroll(scrollBtn));
    scrollBtn.style.color = 'var(--clr-success)';

    toolbar.append(title, spacer, clearBtn, scrollBtn);

    // Log area
    this.pre = document.createElement('div');
    this.pre.className = 'log-viewer';
    this.pre.style.borderRadius = '0 0 var(--radius-md) var(--radius-md)';

    const placeholder = document.createElement('div');
    placeholder.className = 'log-placeholder';
    placeholder.textContent = 'Log streaming akan dimulai ketika agent distart...';
    this.pre.appendChild(placeholder);

    // Scroll tracking
    this.pre.addEventListener('scroll', () => {
      const atBottom = this.pre.scrollHeight - this.pre.scrollTop - this.pre.clientHeight < 40;
      this.autoScroll = atBottom;
      scrollBtn.style.color = this.autoScroll ? 'var(--clr-success)' : 'var(--clr-text-muted)';
    });

    this.container.append(toolbar, this.pre);
  }

  getElement() { return this.container; }

  connect(apiBase: string) {
    this.disconnect();
    const url = `${apiBase}/api/agents/${this.agentId}/logs`;
    this.source = new EventSource(url, { withCredentials: true });

    this.source.onmessage = (e) => {
      const line = e.data as string;
      this.addLine(line);
    };

    // Dengarkan event "close" explicit dari Go backend saat StopAgent ditekan
    this.source.addEventListener('close', (e) => {
      this.addLine((e as MessageEvent).data);
      this.disconnect();
    });

    this.source.onerror = () => {
      // Hilangkan pesan "reconnecting..." agar UI tidak spam ketika internet sedang berkedip
    };
  }

  disconnect() {
    if (this.source) {
      this.source.close();
      this.source = null;
    }
  }

  clear() {
    this.pre.innerHTML = '';
    this.lineCount = 0;
  }

  private addLine(raw: string) {
    // Clear placeholder on first real line
    if (this.lineCount === 0) this.pre.innerHTML = '';

    // Trim old lines to prevent memory blowup
    if (this.lineCount > this.maxLines) {
      const first = this.pre.firstChild;
      if (first) this.pre.removeChild(first);
    }

    const span = document.createElement('span');
    span.className = 'log-line ' + this.classifyLine(raw);
    span.textContent = raw + '\n';
    this.pre.appendChild(span);
    this.lineCount++;

    if (this.autoScroll) {
      this.pre.scrollTop = this.pre.scrollHeight;
    }
  }

  private classifyLine(line: string): string {
    if (line.includes('[SYS]')) return 'sys';
    if (line.includes('[ERR]')) return 'err';
    if (line.includes('[PROMPT]') || line.includes('[OUTPUT]') || line.includes('[USAGE]') || line.includes('[SPEED]')) return 'metric';
    if (line.includes('✅') || line.includes('🤖') || line.includes('💬')) return 'info';
    return 'out';
  }

  private toggleAutoScroll(btn: HTMLButtonElement) {
    this.autoScroll = !this.autoScroll;
    btn.style.color = this.autoScroll ? 'var(--clr-success)' : 'var(--clr-text-muted)';
    if (this.autoScroll) this.pre.scrollTop = this.pre.scrollHeight;
  }

  private makeToolbarBtn(label: string, onClick: () => void): HTMLButtonElement {
    const btn = document.createElement('button');
    btn.textContent = label;
    btn.style.cssText = `
      background: transparent;
      border: 1px solid var(--clr-border);
      border-radius: var(--radius-sm);
      color: var(--clr-text-secondary);
      padding: 2px 8px;
      cursor: pointer;
      font-size: 0.7rem;
      font-family: var(--font-mono);
      transition: all 0.15s ease;
    `;
    btn.addEventListener('mouseenter', () => btn.style.background = 'var(--clr-bg-glass)');
    btn.addEventListener('mouseleave', () => btn.style.background = 'transparent');
    btn.addEventListener('click', onClick);
    return btn;
  }
}
