import type { ModelEntry, ModelGroups, ModelRouteMeta, RouterStatus } from '../api/client.js';

export function flattenModelGroups(groups: ModelGroups): ModelEntry[] {
  return [
    ...(groups.cloud || []),
    ...(groups.cloud_proxied || []),
    ...(groups.local || []),
  ];
}

export function routerModelsToEntries(status: RouterStatus | null | undefined): ModelEntry[] {
  const models = status?.models || [];
  if (!models.length) return [];

  const runtimeSupported = Boolean(status?.agent_routing_enabled && status?.reachable && !status?.error);
  const warnings: string[] = [];
  if (!status?.agent_routing_enabled) warnings.push('AI Router belum aktif untuk agent routing');
  if (!status?.reachable) warnings.push('AI Router belum reachable');
  if (status?.error) warnings.push(status.error);

  return models.map((model) => ({
    id: model,
    label: model,
    provider: 'ai-router',
    tier: 'cloud_proxied',
    tier_label: 'AI Router',
    has_api_key: Boolean(status?.token_configured),
    route_target: 'ai_router',
    runtime_supported: runtimeSupported,
    adapter_id: status?.provider || 'ai-router',
    canonical_model: model,
    outbound_model_example: model,
    provider_docs_url: '',
    runtime_notes: ['AI Router memakai exact model id dari /v1/models tanpa transform universal.'],
    warnings,
  }));
}

export function modelRouteMetaFromEntry(model: ModelEntry | null | undefined): ModelRouteMeta | undefined {
  if (!model) return undefined;
  return {
    route_target: model.route_target,
    tier: model.tier,
    adapter_id: model.adapter_id || model.provider,
    outbound_model: model.outbound_model_example || model.id,
    provider_base_url: model.provider_base_url || '',
    runtime_supported: model.runtime_supported,
  };
}
