import type { ModelEntry, ModelGroups } from '../api/client.js';

type TierFilter = 'all' | ModelEntry['tier'];

interface ModelPickerOptions {
  currentModel?: string;
  allowEmpty?: boolean;
  emptyLabel?: string;
  manualPlaceholder?: string;
  onChange?: (value: string) => void;
}

const PROVIDER_ORDER = ['openclaw', 'ollama', 'openrouter', 'anthropic', 'google', 'openai', 'xai', 'llamacpp', 'ai-router'];
const TIER_LABELS: Record<ModelEntry['tier'], string> = {
  cloud: 'Cloud',
  cloud_proxied: 'Cloud Proxied',
  local: 'Local',
};

export class ModelPicker {
  private root = document.createElement('div');
  private searchInput = document.createElement('input');
  private manualInput = document.createElement('input');
  private listEl = document.createElement('div');
  private emptyEl = document.createElement('div');
  private filterButtons = new Map<TierFilter, HTMLButtonElement>();
  private models: ModelEntry[] = [];
  private selectedValue = '';
  private selectedModel: ModelEntry | null = null;
  private activeTier: TierFilter = 'all';
  private options: ModelPickerOptions;

  constructor(options: ModelPickerOptions = {}) {
    this.options = options;
    this.selectedValue = options.currentModel?.trim() || '';
    this.build();
  }

  getElement() {
    return this.root;
  }

  setGroups(groups: ModelGroups) {
    this.setModels([
      ...(groups.cloud || []),
      ...(groups.cloud_proxied || []),
      ...(groups.local || []),
    ]);
  }

  setModels(models: ModelEntry[]) {
    this.models = sortModelEntries(dedupeModels(models || []));
    this.selectedModel = this.models.find(model => model.id === this.selectedValue) || null;
    this.renderList();
  }

  getValue() {
    const manual = this.manualInput.value.trim();
    if (manual) return manual;
    return this.selectedValue.trim();
  }

  getSelectedModel() {
    const currentValue = this.getValue();
    return this.models.find(model => model.id === currentValue) || this.selectedModel;
  }

  focus() {
    this.searchInput.focus();
  }

  private build() {
    this.root.className = 'model-picker';

    const searchRow = document.createElement('div');
    searchRow.className = 'model-picker-search-row';

    this.searchInput.type = 'search';
    this.searchInput.className = 'form-input model-picker-search';
    this.searchInput.placeholder = 'Cari model atau provider...';
    this.searchInput.addEventListener('input', () => this.renderList());

    const filters = document.createElement('div');
    filters.className = 'model-picker-filters';
    ([
      ['all', 'All'],
      ['cloud', 'Cloud'],
      ['cloud_proxied', 'Cloud Proxied'],
      ['local', 'Local'],
    ] as Array<[TierFilter, string]>).forEach(([tier, label]) => {
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = `model-picker-filter ${tier === this.activeTier ? 'active' : ''}`;
      btn.textContent = label;
      btn.addEventListener('click', () => {
        this.activeTier = tier;
        this.updateFilterButtons();
        this.renderList();
      });
      this.filterButtons.set(tier, btn);
      filters.appendChild(btn);
    });

    searchRow.append(this.searchInput, filters);

    this.listEl.className = 'model-picker-list';
    this.emptyEl.className = 'model-picker-empty';
    this.emptyEl.textContent = 'Tidak ada model yang cocok.';

    const manualWrap = document.createElement('div');
    manualWrap.className = 'model-picker-manual';
    const manualLabel = document.createElement('label');
    manualLabel.className = 'form-label';
    manualLabel.textContent = 'Manual model ref';
    this.manualInput.type = 'text';
    this.manualInput.className = 'form-input font-mono';
    this.manualInput.placeholder = this.options.manualPlaceholder || 'provider/model-id, contoh: openrouter/nvidia/model:free';
    this.manualInput.value = this.selectedValue;
    this.manualInput.addEventListener('input', () => {
      this.selectedValue = this.manualInput.value.trim();
      this.selectedModel = this.models.find(model => model.id === this.selectedValue) || null;
      this.options.onChange?.(this.selectedValue);
      this.renderList();
    });
    const manualHelp = document.createElement('span');
    manualHelp.className = 'form-help';
    manualHelp.textContent = this.options.emptyLabel || 'Pilih dari daftar atau tulis manual jika model belum terindeks.';
    manualWrap.append(manualLabel, this.manualInput, manualHelp);

    this.root.append(searchRow, this.listEl, this.emptyEl, manualWrap);
  }

  private updateFilterButtons() {
    this.filterButtons.forEach((btn, tier) => {
      btn.classList.toggle('active', tier === this.activeTier);
    });
  }

  private renderList() {
    this.listEl.innerHTML = '';
    const query = this.searchInput.value.trim().toLowerCase();
    const visible = this.models.filter((model) => {
      if (this.activeTier !== 'all' && model.tier !== this.activeTier) return false;
      if (!query) return true;
      return [model.id, model.label, model.provider, model.tier].some(value =>
        String(value || '').toLowerCase().includes(query)
      );
    });

    this.emptyEl.hidden = visible.length > 0;
    if (visible.length === 0) return;

    const byProvider = new Map<string, ModelEntry[]>();
    visible.forEach((model) => {
      const provider = model.provider || 'unknown';
      const items = byProvider.get(provider) || [];
      items.push(model);
      byProvider.set(provider, items);
    });

    Array.from(byProvider.entries()).forEach(([provider, items]) => {
      const section = document.createElement('section');
      section.className = 'model-picker-provider';

      const header = document.createElement('div');
      header.className = 'model-picker-provider-header';
      const name = document.createElement('span');
      name.textContent = provider;
      const count = document.createElement('span');
      count.textContent = `${items.length} model`;
      header.append(name, count);
      section.appendChild(header);

      items.forEach((model) => section.appendChild(this.renderRow(model)));
      this.listEl.appendChild(section);
    });
  }

  private renderRow(model: ModelEntry) {
    const row = document.createElement('button');
    row.type = 'button';
    row.className = 'model-picker-row';
    row.classList.toggle('selected', model.id === this.selectedValue);
    row.title = buildModelTitle(model);

    const main = document.createElement('span');
    main.className = 'model-picker-row-main';
    const label = document.createElement('span');
    label.className = 'model-picker-row-label';
    label.textContent = model.label || model.id;
    const id = document.createElement('span');
    id.className = 'model-picker-row-id';
    id.textContent = model.id;
    main.append(label, id);

    const meta = document.createElement('span');
    meta.className = 'model-picker-row-meta';
    const provider = document.createElement('span');
    provider.className = 'model-picker-badge provider';
    provider.textContent = model.adapter_id || model.provider || 'unknown';
    const tier = document.createElement('span');
    tier.className = `model-picker-badge tier tier-${model.tier}`;
    tier.textContent = TIER_LABELS[model.tier] || model.tier;
    meta.append(provider, tier);
    if (model.route_target) {
      const route = document.createElement('span');
      route.className = `model-picker-badge route route-${model.route_target}`;
      route.textContent = routeLabel(model);
      meta.appendChild(route);
    }
    if (model.outbound_model_example && model.outbound_model_example !== model.id) {
      const outbound = document.createElement('span');
      outbound.className = 'model-picker-badge outbound';
      outbound.textContent = 'direct ref';
      meta.appendChild(outbound);
    }
    if (model.provider_docs_url) {
      const docs = document.createElement('span');
      docs.className = 'model-picker-badge docs';
      docs.textContent = 'docs';
      meta.appendChild(docs);
    }
    if (model.has_api_key) {
      const key = document.createElement('span');
      key.className = 'model-picker-badge key';
      key.textContent = 'key';
      meta.appendChild(key);
    }
    if (model.runtime_supported === false) {
      const setup = document.createElement('span');
      setup.className = 'model-picker-badge setup';
      setup.textContent = 'setup';
      meta.appendChild(setup);
    }

    row.append(main, meta);
    row.addEventListener('click', () => {
      this.selectedValue = model.id;
      this.selectedModel = model;
      this.manualInput.value = model.id;
      this.options.onChange?.(model.id);
      this.renderList();
    });
    return row;
  }
}

function routeLabel(model: ModelEntry) {
  if (model.route_target === 'ai_router') return 'AI Router exact';
  if (model.route_target === 'openclaw_gateway') {
    return model.id.startsWith('openrouter/') ? 'Gateway canonical' : 'Gateway';
  }
  if (model.route_target === 'ollama_direct') return 'Ollama direct';
  return 'Direct provider';
}

function buildModelTitle(model: ModelEntry) {
  const lines = [
    model.id,
    model.canonical_model ? `Canonical: ${model.canonical_model}` : '',
    model.outbound_model_example ? `Outbound example: ${model.outbound_model_example}` : '',
    model.adapter_id ? `Adapter: ${model.adapter_id}` : '',
    model.route_target ? `Route: ${model.route_target}` : '',
    ...(model.runtime_notes || []),
    ...(model.warnings || []),
    model.provider_docs_url ? `Docs: ${model.provider_docs_url}` : '',
  ].filter(Boolean);
  return lines.join('\n');
}

function dedupeModels(models: ModelEntry[]) {
  const seen = new Set<string>();
  const out: ModelEntry[] = [];
  models.forEach((model) => {
    if (!model.id || seen.has(model.id)) return;
    seen.add(model.id);
    out.push(model);
  });
  return out;
}

function providerRank(provider: string) {
  const index = PROVIDER_ORDER.indexOf(String(provider || '').toLowerCase());
  return index === -1 ? PROVIDER_ORDER.length : index;
}

export function sortModelEntries(models: ModelEntry[]) {
  return [...models].sort((a, b) => {
    const providerDelta = providerRank(a.provider) - providerRank(b.provider);
    if (providerDelta !== 0) return providerDelta;
    const providerName = a.provider.localeCompare(b.provider);
    if (providerName !== 0 && providerRank(a.provider) === PROVIDER_ORDER.length) return providerName;
    return (a.label || a.id).localeCompare(b.label || b.id);
  });
}
