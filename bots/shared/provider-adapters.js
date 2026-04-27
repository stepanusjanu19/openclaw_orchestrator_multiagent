const DIRECT_TARGETS = new Set([
  "direct_openrouter",
  "direct_openai",
  "direct_google",
  "direct_xai",
  "direct_anthropic",
  "direct_llamacpp",
]);

export const PROVIDER_ADAPTERS = Object.freeze({
  openclaw: {
    id: "openclaw",
    transport: "openai-compatible",
    baseURL: "OPENCLAW_GW_URL",
    authEnv: "OPENCLAW_GW_TOKEN",
    modelTransform: "canonical",
    supportsOpenAIChat: true,
    nativeAPI: false,
    providerDocsURL: "https://docs.openclaw.ai/concepts/model-providers",
    runtimeNotes: ["OpenClaw Gateway expects canonical provider/model refs."],
  },
  openrouter: {
    id: "openrouter",
    transport: "openai-compatible",
    baseURL: "https://openrouter.ai/api/v1",
    authEnv: "OPENROUTER_API_KEY",
    modelTransform: "strip-provider-prefix-for-direct",
    supportsOpenAIChat: true,
    nativeAPI: false,
    providerDocsURL: "https://openrouter.ai/docs/api-reference/chat-completion",
    runtimeNotes: ["Direct OpenRouter expects provider/model without the outer openrouter/ prefix."],
  },
  openai: {
    id: "openai",
    transport: "openai-compatible",
    baseURL: "https://api.openai.com/v1",
    authEnv: "OPENAI_API_KEY",
    modelTransform: "strip-provider-prefix-for-direct",
    supportsOpenAIChat: true,
    nativeAPI: false,
    providerDocsURL: "https://platform.openai.com/docs/api-reference/chat/create-chat-completion",
    runtimeNotes: ["Direct OpenAI-compatible calls use the provider-native model id."],
  },
  google: {
    id: "google",
    transport: "openai-compatible",
    baseURL: "https://generativelanguage.googleapis.com/v1beta/openai",
    authEnv: "GOOGLE_API_KEY",
    modelTransform: "strip-provider-prefix-for-direct",
    supportsOpenAIChat: true,
    nativeAPI: false,
    providerDocsURL: "https://ai.google.dev/gemini-api/docs/openai",
    runtimeNotes: ["Gemini OpenAI compatibility uses Gemini model ids without the openclaw provider prefix."],
  },
  xai: {
    id: "xai",
    transport: "openai-compatible",
    baseURL: "https://api.x.ai/v1",
    authEnv: "XAI_API_KEY",
    modelTransform: "strip-provider-prefix-for-direct",
    supportsOpenAIChat: true,
    nativeAPI: false,
    providerDocsURL: "https://docs.x.ai/docs/api-reference",
    runtimeNotes: ["xAI direct routing is OpenAI-compatible and uses provider-native model ids."],
  },
  anthropic: {
    id: "anthropic",
    transport: "native-messages",
    baseURL: "https://api.anthropic.com",
    authEnv: "ANTHROPIC_API_KEY",
    modelTransform: "strip-provider-prefix-for-direct",
    supportsOpenAIChat: false,
    nativeAPI: true,
    providerDocsURL: "https://docs.anthropic.com/en/api/messages-examples",
    runtimeNotes: ["Anthropic native Messages API is not called directly by the V1 bot runtime."],
  },
  ollama: {
    id: "ollama",
    transport: "ollama-native-or-openai-compatible",
    baseURL: "http://127.0.0.1:11434",
    authEnv: "OLLAMA_API_KEY",
    modelTransform: "strip-provider-prefix-for-direct",
    supportsOpenAIChat: true,
    nativeAPI: true,
    providerDocsURL: "https://docs.ollama.com/openai",
    runtimeNotes: ["Ollama direct bypass uses the local model name without ollama/."],
  },
  llamacpp: {
    id: "llamacpp",
    transport: "openai-compatible",
    baseURL: "http://127.0.0.1:8080/v1",
    authEnv: "",
    modelTransform: "strip-provider-prefix-for-direct",
    supportsOpenAIChat: true,
    nativeAPI: false,
    providerDocsURL: "https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md",
    runtimeNotes: ["llama.cpp server exposes an OpenAI-compatible local endpoint."],
  },
});

export function splitModelRef(model) {
  const configuredModel = String(model || "").trim();
  const slash = configuredModel.indexOf("/");
  if (slash <= 0 || slash === configuredModel.length - 1) {
    return { provider: "", modelId: configuredModel, configuredModel };
  }
  return {
    provider: configuredModel.slice(0, slash).toLowerCase(),
    modelId: configuredModel.slice(slash + 1),
    configuredModel,
  };
}

export function providerAdapterForModel(model) {
  const { provider } = splitModelRef(model);
  return PROVIDER_ADAPTERS[provider] || PROVIDER_ADAPTERS.openclaw;
}

export function stripProviderPrefix(model) {
  const { modelId } = splitModelRef(model);
  return modelId;
}

export function detectTargetURLKind(rawURL, { aiRouterEnabled = false } = {}) {
  if (aiRouterEnabled) return "ai_router";

  let parsed;
  try {
    parsed = new URL(String(rawURL || ""));
  } catch {
    return "openclaw_gateway";
  }

  const host = parsed.hostname.toLowerCase();
  const path = parsed.pathname.toLowerCase();
  if (host === "openrouter.ai" || host.endsWith(".openrouter.ai")) return "direct_openrouter";
  if (host === "api.openai.com") return "direct_openai";
  if (host === "generativelanguage.googleapis.com") return "direct_google";
  if (host === "api.x.ai") return "direct_xai";
  if (host === "api.anthropic.com") return "direct_anthropic";
  if ((host === "localhost" || host === "127.0.0.1") && path.endsWith("/api/chat")) return "ollama_direct";
  return "openclaw_gateway";
}

export function normalizeModelForTarget(model, targetURLKind) {
  const configuredModel = String(model || "").trim();
  if (!configuredModel) return "";
  const { provider } = splitModelRef(configuredModel);
  if (targetURLKind === "openclaw_gateway") return configuredModel;
  if (targetURLKind === "ai_router") return configuredModel;
  if (targetURLKind === "ollama_direct") return stripProviderPrefix(configuredModel);
  if (targetURLKind === "direct_openrouter") {
    return provider === "openrouter" ? stripProviderPrefix(configuredModel) : configuredModel;
  }
  if (targetURLKind === "direct_openai") {
    return provider === "openai" ? stripProviderPrefix(configuredModel) : configuredModel;
  }
  if (targetURLKind === "direct_google") {
    return provider === "google" ? stripProviderPrefix(configuredModel) : configuredModel;
  }
  if (targetURLKind === "direct_xai") {
    return provider === "xai" ? stripProviderPrefix(configuredModel) : configuredModel;
  }
  if (targetURLKind === "direct_anthropic") {
    return provider === "anthropic" ? stripProviderPrefix(configuredModel) : configuredModel;
  }
  if (targetURLKind === "direct_llamacpp") {
    return provider === "llamacpp" ? stripProviderPrefix(configuredModel) : configuredModel;
  }
  if (DIRECT_TARGETS.has(targetURLKind)) return stripProviderPrefix(configuredModel);
  return configuredModel;
}

export function resolveProviderRoute(model, { targetURL = "", aiRouterEnabled = false, aiRouterProvider = "" } = {}) {
  const configuredModel = String(model || "").trim();
  const { provider } = splitModelRef(configuredModel);
  const targetURLKind = detectTargetURLKind(targetURL, { aiRouterEnabled });
  const adapter = targetURLKind === "openclaw_gateway"
    ? PROVIDER_ADAPTERS.openclaw
    : providerAdapterForModel(configuredModel);

  return {
    configuredModel,
    canonicalModel: configuredModel,
    outboundModel: normalizeModelForTarget(configuredModel, targetURLKind),
    routeTarget: targetURLKind,
    adapterId: adapter.id,
    targetURLKind,
    targetLabel: targetLabelForKind(targetURLKind, aiRouterProvider),
    providerDocsURL: adapter.providerDocsURL,
    runtimeNotes: adapter.runtimeNotes,
  };
}

function targetLabelForKind(targetURLKind, aiRouterProvider) {
  switch (targetURLKind) {
    case "ai_router":
      return `AI Router (${aiRouterProvider || "custom"})`;
    case "direct_openrouter":
      return "Direct OpenRouter";
    case "direct_openai":
      return "Direct OpenAI";
    case "direct_google":
      return "Direct Google Gemini";
    case "direct_xai":
      return "Direct xAI";
    case "direct_anthropic":
      return "Direct Anthropic";
    case "ollama_direct":
      return "Ollama Direct";
    case "direct_llamacpp":
      return "llama.cpp Direct";
    default:
      return "OpenClaw Gateway";
  }
}
