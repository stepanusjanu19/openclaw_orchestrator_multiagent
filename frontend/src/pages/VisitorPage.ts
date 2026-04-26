import L from 'leaflet';
import 'leaflet/dist/leaflet.css';
import { api, type VisitorRecord } from '../api/client.js';
import { showToast } from '../components/Toast.js';
import { escapeHTML } from '../utils/dom.js';

type VisitorMapMode = 'leaflet' | 'plot';

export class VisitorPage {
  private el: HTMLDivElement;
  private rows: VisitorRecord[] = [];
  private total = 0;
  private limit = 25;
  private offset = 0;
  private q = '';
  private selected = 0;
  private loading = false;
  private refreshTimer?: number;
  private lastLoadedAt = '';
  private mapMode: VisitorMapMode = loadMapMode();
  private leafletMap?: L.Map;
  private leafletMarkers: L.Marker[] = [];
  private tileWarningShown = false;
  private readonly autoRefreshMs = 5000;
  private readonly onVisibilityChange = () => {
    if (document.hidden) {
      this.stopAutoRefresh();
      return;
    }
    this.startAutoRefresh();
    this.load(false);
  };

  constructor() {
    this.el = document.createElement('div');
    this.el.className = 'page-body visitor-page';
  }

  getElement() {
    return this.el;
  }

  async mount() {
    await this.load();
    document.addEventListener('visibilitychange', this.onVisibilityChange);
    this.startAutoRefresh();
  }

  unmount() {
    this.teardownLeafletMap();
    this.stopAutoRefresh();
    document.removeEventListener('visibilitychange', this.onVisibilityChange);
  }

  private async load(showLoading = true) {
    if (this.loading) return;
    const selectedRequestID = this.rows[this.selected]?.request_id || '';
    this.loading = showLoading;
    if (showLoading) this.render();
    try {
      const resp = await api.getVisitors({ q: this.q, limit: this.limit, offset: this.offset });
      this.rows = resp.rows || [];
      this.total = resp.total || 0;
      this.limit = resp.limit || this.limit;
      this.offset = resp.offset || this.offset;
      if (selectedRequestID) {
        const nextSelected = this.rows.findIndex(row => row.request_id === selectedRequestID);
        this.selected = nextSelected >= 0 ? nextSelected : 0;
      } else if (this.selected >= this.rows.length) {
        this.selected = 0;
      }
      this.lastLoadedAt = new Date().toLocaleTimeString();
    } catch (err: any) {
      showToast(err.message || 'Gagal memuat visitor database', 'error');
    } finally {
      this.loading = false;
      this.render();
    }
  }

  private startAutoRefresh() {
    this.stopAutoRefresh();
    if (document.hidden) return;
    this.refreshTimer = window.setInterval(() => this.load(false), this.autoRefreshMs);
  }

  private stopAutoRefresh() {
    if (this.refreshTimer) {
      window.clearInterval(this.refreshTimer);
      this.refreshTimer = undefined;
    }
  }

  private render() {
    this.teardownLeafletMap();
    const selected = this.rows[this.selected];
    const from = this.total === 0 ? 0 : this.offset + 1;
    const to = Math.min(this.offset + this.limit, this.total);
    this.el.innerHTML = `
      ${this.renderHeader(selected, from, to)}
      ${this.renderToolbar()}

      <div class="visitor-layout">
        <section class="visitor-list-panel">
          ${this.loading ? '<div class="audit-empty-state">Memuat visitor...</div>' : this.renderRows()}
          <div class="visitor-pagination">
            <button id="visitor-prev" class="btn btn-ghost btn-sm" ${this.offset <= 0 ? 'disabled' : ''}>Prev</button>
            <span>${from}-${to} / ${this.total}</span>
            <button id="visitor-next" class="btn btn-ghost btn-sm" ${this.offset + this.limit >= this.total ? 'disabled' : ''}>Next</button>
          </div>
        </section>
        <section class="visitor-detail-panel">
          ${this.renderMap()}
          ${selected ? this.renderDetail(selected) : '<div class="audit-detail-panel muted">Belum ada visitor tersimpan. Data akan muncul setelah gateway publik menerima request.</div>'}
        </section>
      </div>
    `;
    this.bindEvents();
    this.renderLeafletMap();
  }

  private renderHeader(selected: VisitorRecord | undefined, from: number, to: number) {
    const coordinateCount = this.coordinateRows().length;
    const selectedRisk = selected?.risk_score ?? 0;
    return `
      <div class="visitor-header">
        <div>
          <span class="page-eyebrow">Gateway intelligence</span>
          <h1>Visitor</h1>
          <p>Geo, subnet, CIDR, DNS/provider binding, risk, dan koordinat dari request gateway publik.</p>
        </div>
        <div class="visitor-header-stats" aria-label="Visitor summary">
          ${this.headerStat('Rows', `${from}-${to}`, `${this.total} total`)}
          ${this.headerStat('Coords', String(coordinateCount), 'mapped')}
          ${this.headerStat('Refresh', '5s', this.lastLoadedAt ? `last ${this.lastLoadedAt}` : 'waiting')}
          ${this.headerStat('Selected risk', String(selectedRisk), riskLabel(selectedRisk), `risk-${riskClass(selectedRisk)}`)}
        </div>
      </div>
    `;
  }

  private headerStat(label: string, value: string, meta: string, extraClass = '') {
    return `
      <div class="visitor-header-stat ${escapeHTML(extraClass)}">
        <span>${escapeHTML(label)}</span>
        <strong>${escapeHTML(value)}</strong>
        <small>${escapeHTML(meta)}</small>
      </div>
    `;
  }

  private renderToolbar() {
    return `
      <section class="visitor-toolbar">
        <div class="visitor-search-group">
          <input id="visitor-q" placeholder="Search IP, subnet, city, country, DNS, gateway, risk..." value="${escapeHTML(this.q)}" autocomplete="off">
          <button id="visitor-search" class="btn btn-primary btn-sm">Search</button>
        </div>
        <div class="visitor-control-group">
          <select id="visitor-limit" aria-label="Visitor rows per page">
            ${[10, 25, 50, 100].map(n => `<option value="${n}" ${this.limit === n ? 'selected' : ''}>${n}/page</option>`).join('')}
          </select>
          <div class="visitor-map-toggle" role="group" aria-label="Visitor map mode">
            <button id="visitor-map-leaflet" class="btn btn-sm ${this.mapMode === 'leaflet' ? 'btn-primary' : 'btn-ghost'}">Map</button>
            <button id="visitor-map-plot" class="btn btn-sm ${this.mapMode === 'plot' ? 'btn-primary' : 'btn-ghost'}">Coordinate Plot</button>
          </div>
          <button id="visitor-refresh" class="btn btn-ghost btn-sm">Refresh</button>
        </div>
      </section>
    `;
  }

  private renderRows() {
    if (this.rows.length === 0) {
      return '<div class="audit-empty-state"><strong>Belum ada visitor untuk filter ini.</strong><span>Pastikan PostgreSQL aktif dan gateway publik menerima request.</span></div>';
    }
    return `<div class="visitor-row-list">${this.rows.map((row, i) => {
      const identity = row.visitor_ip || row.visitor_hash || 'unknown';
      const subnet = row.subnet || row.cidr || '-';
      const risk = row.risk_score || 0;
      return `
      <button class="visitor-row ${i === this.selected ? 'selected' : ''}" data-index="${i}">
        <div class="visitor-row-main">
          <span class="visitor-field-label">Visitor</span>
          <strong class="visitor-mono visitor-clip" title="${escapeHTML(identity)}">${escapeHTML(identity)}</strong>
          <small>${escapeHTML(formatRelative(row.occurred_at))}</small>
        </div>
        <div class="visitor-row-location">
          <span class="visitor-field-label">Location</span>
          <strong title="${escapeHTML(locationLine(row))}">${escapeHTML(locationLine(row))}</strong>
          <small>${escapeHTML(locationSource(row))}${row.accuracy_km ? ` · ~${escapeHTML(String(row.accuracy_km))}km` : ''}</small>
        </div>
        <div class="visitor-row-network">
          <span class="visitor-field-label">Network</span>
          <code class="visitor-clip" title="${escapeHTML(subnet)}">${escapeHTML(subnet)}</code>
          <small>${escapeHTML(row.dns_provider || row.org || row.asn || '-')} · ${escapeHTML(row.ip_class || 'unknown')}</small>
        </div>
        <div class="visitor-row-state">
          ${sourceBadge(row)}
          <span class="risk-pill risk-${riskClass(risk)}">${risk}</span>
          ${statusBadge(row.status || row.decision || 'unknown')}
        </div>
      </button>
    `;
    }).join('')}</div>`;
  }

  private renderMap() {
    if (this.mapMode === 'plot') return this.renderCoordinatePlot();
    return `
      <div class="visitor-leaflet-shell visitor-map-shell">
        ${this.renderMapHeader(this.rows[this.selected])}
        <div id="visitor-leaflet-map" class="visitor-leaflet-map"></div>
        <div class="visitor-map-tile-warning">Tile peta gagal dimuat. Mode Coordinate Plot tetap tersedia.</div>
        ${this.coordinateRows().length === 0 ? '<div class="visitor-map-empty">Belum ada koordinat latitude/longitude</div>' : ''}
      </div>
    `;
  }

  private renderMapHeader(selected?: VisitorRecord) {
    const coords = this.coordinateRows().length;
    return `
      <div class="visitor-map-shell-header">
        <div>
          <strong>${this.mapMode === 'leaflet' ? 'Leaflet Map' : 'Coordinate Plot'}</strong>
          <span>${coords} coordinate${coords === 1 ? '' : 's'}</span>
        </div>
        <div class="visitor-map-legend">
          ${selected ? sourceBadge(selected) : '<span class="visitor-source-badge visitor-source-offline">Offline</span>'}
          <code>${selected && validCoordinate(selected) ? escapeHTML(`${selected.latitude}, ${selected.longitude}`) : 'no coordinate selected'}</code>
        </div>
      </div>
    `;
  }

  private renderCoordinatePlot() {
    const points = this.coordinateRows().map(row => {
      const x = ((Number(row.longitude) + 180) / 360) * 100;
      const y = ((90 - Number(row.latitude)) / 180) * 100;
      const selected = this.rows[this.selected] === row;
      return `<button class="visitor-map-point ${selected ? 'selected' : ''} source-${sourceClass(row)}" style="left:${x}%;top:${y}%;" title="${escapeHTML(pointTitle(row))}" data-point="${this.rows.indexOf(row)}"></button>`;
    }).join('');
    return `
      <div class="visitor-map-shell visitor-map-plot-shell">
        ${this.renderMapHeader(this.rows[this.selected])}
        <div class="visitor-map">
          <div class="visitor-map-grid"></div>
          <div class="visitor-map-label">lat/lon projection</div>
          ${points || '<div class="visitor-map-empty">Belum ada koordinat latitude/longitude</div>'}
        </div>
      </div>
    `;
  }

  private renderLeafletMap() {
    if (this.mapMode !== 'leaflet') return;
    const mount = this.el.querySelector('#visitor-leaflet-map') as HTMLDivElement | null;
    if (!mount) return;
    const rows = this.coordinateRows();
    this.leafletMap = L.map(mount, {
      zoomControl: true,
      attributionControl: true,
      preferCanvas: true,
    });
    const tileLayer = L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
      maxZoom: 19,
      attribution: '&copy; OpenStreetMap contributors',
    });
    tileLayer.on('tileerror', () => {
      if (this.tileWarningShown) return;
      this.tileWarningShown = true;
      this.el.querySelector('.visitor-leaflet-shell')?.classList.add('tile-error');
    });
    tileLayer.addTo(this.leafletMap);

    if (rows.length === 0) {
      this.leafletMap.setView([0, 0], 2);
      return;
    }

    const bounds = L.latLngBounds([]);
    rows.forEach(row => {
      const index = this.rows.indexOf(row);
      const lat = Number(row.latitude);
      const lon = Number(row.longitude);
      const marker = L.marker([lat, lon], {
        icon: markerIcon(row, index === this.selected),
        title: pointTitle(row),
      });
      marker.bindPopup(markerPopup(row));
      marker.on('click', () => {
        this.selected = index;
        this.render();
      });
      marker.addTo(this.leafletMap!);
      this.leafletMarkers.push(marker);
      bounds.extend([lat, lon]);
    });

    const selectedRow = validCoordinate(this.rows[this.selected]) ? this.rows[this.selected] : rows[0];
    if (selectedRow && validCoordinate(selectedRow)) {
      const zoom = sourceClass(selectedRow) === 'gps' ? 14 : 8;
      this.leafletMap.setView([Number(selectedRow.latitude), Number(selectedRow.longitude)], zoom);
    } else if (bounds.isValid()) {
      this.leafletMap.fitBounds(bounds, { padding: [24, 24], maxZoom: 10 });
    }
    window.setTimeout(() => this.leafletMap?.invalidateSize(), 0);
  }

  private teardownLeafletMap() {
    this.leafletMarkers = [];
    if (this.leafletMap) {
      this.leafletMap.remove();
      this.leafletMap = undefined;
    }
  }

  private coordinateRows() {
    return this.rows.filter(validCoordinate);
  }

  private setMapMode(mode: VisitorMapMode) {
    if (this.mapMode === mode) return;
    this.mapMode = mode;
    localStorage.setItem('openclaw.visitor.mapMode', mode);
    this.render();
  }

  private renderDetail(row: VisitorRecord) {
    const proxy = row.metadata?.proxy || {};
    const network = row.metadata?.network || {};
    const abuse = row.metadata?.abuse || {};
    return `
      <div class="audit-detail-panel visitor-detail-card">
        <div class="visitor-detail-head">
          <div>
            <span class="page-eyebrow">Selected visitor</span>
            <strong>${escapeHTML(row.visitor_ip || row.visitor_hash || 'visitor')}</strong>
          </div>
          <div class="visitor-detail-badges">
            ${sourceBadge(row)}
            <span class="risk-pill risk-${riskClass(row.risk_score || 0)}">${row.risk_score || 0}</span>
            ${statusBadge(row.status || row.decision || 'unknown')}
          </div>
        </div>
        ${detailSection('Identity', [
          ['occurred_at', row.occurred_at],
          ['request_id', row.request_id],
          ['visitor_ip', row.visitor_ip],
          ['visitor_hash', row.visitor_hash],
          ['ip_hash', row.ip_hash],
          ['ip_class', row.ip_class],
        ])}
        ${detailSection('Network', [
          ['subnet', row.subnet],
          ['cidr', row.cidr],
          ['asn', row.asn],
          ['org', row.org],
          ['dns_provider', row.dns_provider],
          ['reverse_dns', row.reverse_dns],
          ['proxy_chain_depth', row.proxy_chain_depth],
          ['real_ip_source', proxy.real_ip_source],
          ['trusted_proxy_chain', proxy.trusted_proxy_chain],
          ['guard_provider', proxy.guard_provider],
          ['network_confidence', network.confidence],
        ])}
        ${detailSection('Location', [
          ['country', row.country],
          ['region', row.region],
          ['city', row.city],
          ['latitude', row.latitude],
          ['longitude', row.longitude],
          ['accuracy_km', row.accuracy_km],
          ['location_source', locationSource(row)],
          ['timezone', row.timezone],
          ['geo_source', row.geo_source],
        ])}
        ${detailSection('Gateway', [
          ['gateway_id', row.gateway_id],
          ['agent_id', row.agent_id],
          ['channel', row.channel],
          ['status', row.status],
          ['decision', row.decision],
          ['proto', row.proto],
          ['method', row.method],
          ['content_type', row.content_type],
          ['body_bytes', row.body_bytes],
          ['user_agent_family', row.user_agent_family],
          ['user_agent_hash', row.user_agent_hash],
          ['host_hash', row.host_hash],
          ['origin_hash', row.origin_hash],
        ])}
        ${detailSection('Risk', [
          ['risk_score', row.risk_score],
          ['risk_reasons', (row.risk_reasons || []).join(', ')],
          ['traffic_class', abuse.traffic_class],
          ['abuse_score', abuse.score],
          ['abuse_signals', Array.isArray(abuse.signals) ? abuse.signals.join(', ') : abuse.signals],
        ])}
        <div class="visitor-detail-section">
          <div class="visitor-detail-section-title">Metadata</div>
          <pre class="audit-json">${escapeHTML(JSON.stringify(row.metadata || {}, null, 2))}</pre>
        </div>
      </div>
    `;
  }

  private bindEvents() {
    this.el.querySelector('#visitor-map-leaflet')?.addEventListener('click', () => this.setMapMode('leaflet'));
    this.el.querySelector('#visitor-map-plot')?.addEventListener('click', () => this.setMapMode('plot'));
    this.el.querySelector('#visitor-search')?.addEventListener('click', () => {
      this.q = (this.el.querySelector('#visitor-q') as HTMLInputElement).value.trim();
      this.limit = Number((this.el.querySelector('#visitor-limit') as HTMLSelectElement).value) || 25;
      this.offset = 0;
      this.load();
    });
    this.el.querySelector('#visitor-refresh')?.addEventListener('click', () => this.load());
    this.el.querySelector('#visitor-q')?.addEventListener('keydown', e => {
      if ((e as KeyboardEvent).key !== 'Enter') return;
      this.q = (e.currentTarget as HTMLInputElement).value.trim();
      this.limit = Number((this.el.querySelector('#visitor-limit') as HTMLSelectElement).value) || 25;
      this.offset = 0;
      this.load();
    });
    this.el.querySelector('#visitor-prev')?.addEventListener('click', () => {
      this.offset = Math.max(0, this.offset - this.limit);
      this.load();
    });
    this.el.querySelector('#visitor-next')?.addEventListener('click', () => {
      this.offset += this.limit;
      this.load();
    });
    this.el.querySelectorAll('.visitor-row').forEach(row => {
      row.addEventListener('click', e => {
        this.selected = Number((e.currentTarget as HTMLElement).dataset.index || 0);
        this.render();
      });
    });
    this.el.querySelectorAll('.visitor-map-point').forEach(point => {
      point.addEventListener('click', e => {
        this.selected = Number((e.currentTarget as HTMLElement).dataset.point || 0);
        this.render();
      });
    });
  }
}

function loadMapMode(): VisitorMapMode {
  return localStorage.getItem('openclaw.visitor.mapMode') === 'plot' ? 'plot' : 'leaflet';
}

function validCoordinate(row?: VisitorRecord) {
  return !!row && typeof row.latitude === 'number' && Number.isFinite(row.latitude) && typeof row.longitude === 'number' && Number.isFinite(row.longitude);
}

function markerIcon(row: VisitorRecord, selected: boolean) {
  const cls = sourceClass(row);
  return L.divIcon({
    className: `visitor-leaflet-marker source-${cls} ${selected ? 'selected' : ''}`,
    html: '<span></span>',
    iconSize: selected ? [22, 22] : [16, 16],
    iconAnchor: selected ? [11, 11] : [8, 8],
  });
}

function markerPopup(row: VisitorRecord) {
  return `
    <div class="visitor-map-popup">
      <strong>${escapeHTML(row.visitor_ip || row.visitor_hash || 'visitor')}</strong>
      <span>${escapeHTML(locationLine(row))}</span>
      <code>${escapeHTML(`${row.latitude}, ${row.longitude}`)}</code>
      ${sourceBadge(row)}
    </div>
  `;
}

function riskClass(score: number) {
  if (score >= 70) return 'high';
  if (score >= 25) return 'medium';
  return 'low';
}

function riskLabel(score: number) {
  if (score >= 70) return 'high';
  if (score >= 25) return 'medium';
  return 'low';
}

function pointTitle(row: VisitorRecord) {
  return `${row.visitor_ip || row.visitor_hash || 'visitor'} ${locationLine(row)} ${row.latitude},${row.longitude}`;
}

function locationLine(row: VisitorRecord) {
  return [row.city, row.region, row.country].filter(Boolean).join(', ') || row.ip_class || 'unknown';
}

function locationSource(row: VisitorRecord) {
  const metaSource = row.metadata?.location?.source;
  return typeof metaSource === 'string' && metaSource ? metaSource : (row.geo_source || 'offline');
}

function sourceClass(row: VisitorRecord) {
  const source = locationSource(row);
  if (source === 'gps_consent') return 'gps';
  if (source === 'trusted_headers') return 'header';
  if (source === 'api_provider') return 'ip';
  return 'offline';
}

function sourceBadge(row: VisitorRecord) {
  const cls = sourceClass(row);
  const label = cls === 'gps' ? 'GPS' : cls === 'header' ? 'Header' : cls === 'ip' ? 'IP' : 'Offline';
  return `<span class="visitor-source-badge visitor-source-${cls}">${label}</span>`;
}

function statusBadge(status: string) {
  const normalized = status.trim() || 'unknown';
  const cls = tokenClass(normalized);
  return `<span class="visitor-status-badge status-${cls}" title="${escapeHTML(normalized)}">${escapeHTML(normalized)}</span>`;
}

function detailSection(title: string, rows: Array<[string, any]>) {
  const rendered = rows
    .filter(([, value]) => value !== undefined && value !== '')
    .map(([key, value]) => {
      const text = String(value);
      return `
        <span>${escapeHTML(key)}</span>
        <code title="${escapeHTML(text)}">${escapeHTML(text)}</code>
      `;
    }).join('');
  if (!rendered) return '';
  return `
    <div class="visitor-detail-section">
      <div class="visitor-detail-section-title">${escapeHTML(title)}</div>
      <div class="audit-detail-grid visitor-detail-grid">${rendered}</div>
    </div>
  `;
}

function formatRelative(value?: string) {
  if (!value) return 'time unknown';
  const time = new Date(value).getTime();
  if (!Number.isFinite(time)) return value;
  const diff = Math.max(0, Date.now() - time);
  const minutes = Math.floor(diff / 60000);
  if (minutes < 1) return 'just now';
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

function tokenClass(value: string) {
  return value.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'unknown';
}
