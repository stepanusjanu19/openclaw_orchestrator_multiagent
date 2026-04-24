function redactURL(rawURL) {
  try {
    const parsed = new URL(rawURL);
    parsed.username = "";
    parsed.password = "";
    parsed.search = "";
    parsed.hash = "";
    return parsed.toString();
  } catch {
    return String(rawURL || "").split("?")[0];
  }
}

export function detectOllamaTransport(rawURL) {
  try {
    const parsed = new URL(rawURL);
    const path = parsed.pathname.replace(/\/+$/, "");
    if (path.endsWith("/api/chat")) {
      return "ollama-native";
    }
  } catch {
    // Fall back to string heuristics below.
  }
  return "openai-compat";
}

export function buildOllamaRequestPayload(mode, model, messages) {
  const base = {
    model,
    messages,
    stream: false,
    keep_alive: "30m",
  };

  if (mode === "ollama-native") {
    return base;
  }

  return base;
}

export function buildCompatibleOllamaRetryURL(rawURL) {
  try {
    const parsed = new URL(rawURL);
    const trimmedPath = parsed.pathname.replace(/\/+$/, "");
    if (!trimmedPath.endsWith("/v1/chat/completions")) {
      return "";
    }
    parsed.pathname = trimmedPath.replace(/\/v1\/chat\/completions$/, "/api/chat");
    return parsed.toString();
  } catch {
    return "";
  }
}

export function parseOllamaResponse(mode, payload) {
  if (mode === "ollama-native") {
    const reply = payload?.message?.content || "No reply";
    const promptTokens = Number(payload?.prompt_eval_count || 0);
    const completionTokens = Number(payload?.eval_count || 0);
    const usage = promptTokens || completionTokens
      ? {
          prompt_tokens: promptTokens,
          completion_tokens: completionTokens,
          total_tokens: promptTokens + completionTokens,
        }
      : null;
    return { reply, usage };
  }

  return {
    reply: payload?.choices?.[0]?.message?.content || "No reply",
    usage: payload?.usage || null,
  };
}

export function buildOllamaDirectError({ status = 0, errText = "", model = "", rawURL = "", mode = "", networkError = null }) {
  const safeURL = redactURL(rawURL);
  const target = `${mode || detectOllamaTransport(rawURL)} @ ${safeURL}`;
  const message = networkError?.message || String(networkError || "");
  const lowerErr = String(errText || "").toLowerCase();
  const lowerMsg = message.toLowerCase();

  if (networkError) {
    if (lowerMsg.includes("econnrefused")) {
      return `❌ Ollama local tidak reachable (${target}). Pastikan service Ollama aktif di endpoint tersebut.`;
    }
    return `❌ Gagal terhubung ke Ollama local (${target}): ${message.slice(0, 200)}`;
  }

  if ((lowerErr.includes("model") && lowerErr.includes("not found")) || lowerErr.includes("pull")) {
    return `🤖 *Ollama (Local)*: Model \`${model}\` tidak ditemukan. Pull model tersebut atau periksa nama model agent.`;
  }

  if (status === 404) {
    return `❌ Endpoint Ollama tidak cocok (${target}). Pastikan memakai /api/chat atau /v1/chat/completions yang benar.`;
  }

  return `❌ Error dari *Ollama (Local)* (${status}) via ${target}: ${String(errText || "").slice(0, 200)}`;
}

async function executeOllamaRequest(rawURL, model, messages, fetchImpl) {
  const mode = detectOllamaTransport(rawURL);
  const response = await fetchImpl(rawURL, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(buildOllamaRequestPayload(mode, model, messages)),
  });

  if (!response.ok) {
    return {
      ok: false,
      status: response.status,
      errText: await response.text(),
      rawURL,
      mode,
    };
  }

  const payload = await response.json();
  return {
    ok: true,
    rawURL,
    mode,
    ...parseOllamaResponse(mode, payload),
  };
}

export async function requestDirectOllama({ rawURL, model, messages, fetchImpl = fetch }) {
  let firstAttempt;
  try {
    firstAttempt = await executeOllamaRequest(rawURL, model, messages, fetchImpl);
  } catch (networkError) {
    throw new Error(buildOllamaDirectError({ rawURL, model, networkError }));
  }

  if (firstAttempt.ok) {
    return firstAttempt;
  }

  if (firstAttempt.status === 404 && firstAttempt.mode === "openai-compat") {
    const retryURL = buildCompatibleOllamaRetryURL(rawURL);
    if (retryURL && retryURL !== rawURL) {
      try {
        const retryAttempt = await executeOllamaRequest(retryURL, model, messages, fetchImpl);
        if (retryAttempt.ok) {
          return retryAttempt;
        }
        throw new Error(buildOllamaDirectError({
          status: retryAttempt.status,
          errText: retryAttempt.errText,
          rawURL: retryAttempt.rawURL,
          mode: retryAttempt.mode,
          model,
        }));
      } catch (networkError) {
        if (networkError instanceof Error && networkError.message.includes("Ollama")) {
          throw networkError;
        }
        throw new Error(buildOllamaDirectError({
          rawURL: retryURL,
          mode: detectOllamaTransport(retryURL),
          model,
          networkError,
        }));
      }
    }
  }

  throw new Error(buildOllamaDirectError({
    status: firstAttempt.status,
    errText: firstAttempt.errText,
    rawURL: firstAttempt.rawURL,
    mode: firstAttempt.mode,
    model,
  }));
}
