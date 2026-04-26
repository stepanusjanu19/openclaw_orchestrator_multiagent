/**
 * memory.js — OpenClaw Cognitive Persistence Memory Layer
 *
 * Menggantikan SQLite bawaan OpenClaw dengan PostgreSQL + pgvector.
 * Menggunakan nomic-embed-text (via Ollama lokal) untuk mengubah teks menjadi
 * vektor sempurna sebelum disimpan, memungkinkan semantic similarity search (RAG).
 *
 * Env yang dipakai (opsional — fallback ke nilai default lokal):
 *   OPENCLAW_PGVECTOR_URL  - PostgreSQL DSN (default: postgresql://lenvo_alma@localhost/openclaw)
 *   OLLAMA_EMBED_URL       - Ollama embedding endpoint (default: http://127.0.0.1:11434)
 *   OLLAMA_EMBED_MODEL     - Model embed (default: nomic-embed-text)
 *   MEMORY_ENABLED         - "false" untuk disable seluruh modul ini (default: enabled)
 */

import pg from "pg";

const { Pool } = pg;

const MEMORY_ENABLED = (process.env.MEMORY_ENABLED ?? "true") === "true";
const PGVECTOR_URL = process.env.OPENCLAW_PGVECTOR_URL || "postgresql://lenvo_alma@localhost/openclaw";
const OLLAMA_BASE  = process.env.OLLAMA_EMBED_URL || "http://127.0.0.1:11434";
const EMBED_MODEL  = process.env.OLLAMA_EMBED_MODEL || "nomic-embed-text";
const TOP_K        = 5;      // Berapa memori teratas yang ditarik per query
const MIN_SCORE    = 0.6;    // Threshold similarity (0-1) — di bawah ini tidak relevan

let pool = null;
let memoryRuntimeEnabled = MEMORY_ENABLED;
let memoryDisabledReason = "";

function redactSecrets(value) {
  return String(value || "")
    .replace(/\b\d{6,12}:[A-Za-z0-9_-]{30,}\b/g, "[REDACTED_TELEGRAM_TOKEN]")
    .replace(/\b(?:sk|pk|xox[baprs]|ghp|gho|AIza)[A-Za-z0-9_\-]{16,}\b/g, "[REDACTED_API_KEY]")
    .replace(/(postgres(?:ql)?:\/\/[^:\s/]+:)([^@\s]+)(@)/gi, "$1[REDACTED]$3")
    .replace(/(authorization|api[_-]?key|access[_-]?token|auth[_-]?token|gateway[_-]?token|password|secret)(["'\s:=]+)([^"',\s}]+)/gi, "$1$2[REDACTED]");
}

function disableMemory(reason, agentId) {
  if (!memoryDisabledReason) {
    memoryDisabledReason = redactSecrets(reason);
    console.warn(`[${agentId}][MEMORY] Disabled after non-fatal error: ${memoryDisabledReason}`);
  }
  memoryRuntimeEnabled = false;
}

function shouldDisableMemory(err) {
  const message = String(err?.message || err || "").toLowerCase();
  return (
    (message.includes("embed failed: 404") && message.includes("not found")) ||
    (message.includes("model") && message.includes("not found")) ||
    (message.includes("econnrefused") && message.includes("11434"))
  );
}

// ── Lazy init DB pool ─────────────────────────────────────────────────────────
function getPool() {
  if (!pool) {
    pool = new Pool({ connectionString: PGVECTOR_URL });
    pool.on("error", (err) => {
      console.error("[MEMORY] PG Pool error:", redactSecrets(err.message));
    });
  }
  return pool;
}

// ── Embed satu teks menggunakan Ollama local ───────────────────────────────────
async function embed(text) {
  const res = await fetch(`${OLLAMA_BASE}/api/embeddings`, {
    method:  "POST",
    headers: { "Content-Type": "application/json" },
    body:    JSON.stringify({ model: EMBED_MODEL, prompt: text }),
  });
  if (!res.ok) throw new Error(`[MEMORY] Embed failed: ${res.status} ${await res.text()}`);
  const json = await res.json();
  const vec = json.embedding;
  if (!Array.isArray(vec) || vec.length === 0) throw new Error("[MEMORY] Empty embedding returned");
  return vec;
}

// ── Ensure memories table exists (idempotent) ─────────────────────────────────
async function ensureSchema(client) {
  await client.query(`
    CREATE TABLE IF NOT EXISTS memories (
      id         SERIAL PRIMARY KEY,
      user_id    TEXT  NOT NULL,
      agent_id   TEXT  NOT NULL,
      content    TEXT  NOT NULL,
      embedding  vector(768),
      created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
    )
  `);
  // HNSW index: sangat cepat untuk ANN search jutaan baris
  await client.query(`
    CREATE INDEX IF NOT EXISTS memories_embedding_hnsw
    ON memories
    USING hnsw (embedding vector_cosine_ops)
  `).catch(() => {
    // Mungkin sudah ada atau pgvector versi lama — abaikan
  });
}

// ── Simpan percakapan baru ke pgvector ────────────────────────────────────────
// content = string ringkas: "User bertanya X. Agent menjawab Y."
export async function saveMemory(agentId, userId, content) {
  if (!memoryRuntimeEnabled) return;
  try {
    const vec = await embed(content);
    const client = await getPool().connect();
    try {
      await ensureSchema(client);
      await client.query(
        `INSERT INTO memories (user_id, agent_id, content, embedding) VALUES ($1, $2, $3, $4)`,
        [String(userId), agentId, content, `[${vec.join(",")}]`]
      );
    } finally {
      client.release();
    }
    console.log(`[${agentId}][MEMORY] Saved memory for user ${userId}`);
  } catch (err) {
    if (shouldDisableMemory(err)) {
      disableMemory(redactSecrets(err.message), agentId);
      return;
    }
    // Non-fatal: jika PG mati, agen tetap merespons (degradasi halus)
    console.warn(`[${agentId}][MEMORY] saveMemory failed (non-fatal): ${redactSecrets(err.message)}`);
  }
}

// ── Cari memori yang relevan dengan pesan masuk (RAG Retrieval) ───────────────
// Mengembalikan array string siap-sisip ke system prompt.
export async function searchMemory(agentId, userId, queryText) {
  if (!memoryRuntimeEnabled) return [];
  try {
    const vec = await embed(queryText);
    const client = await getPool().connect();
    try {
      await ensureSchema(client);
      const result = await client.query(
        `SELECT content,
                1 - (embedding <=> $1::vector) AS score
         FROM   memories
         WHERE  user_id  = $2
           AND  agent_id = $3
         ORDER  BY embedding <=> $1::vector
         LIMIT  $4`,
        [`[${vec.join(",")}]`, String(userId), agentId, TOP_K]
      );
      const rows = result.rows.filter(r => r.score >= MIN_SCORE);
      if (rows.length > 0) {
        console.log(`[${agentId}][MEMORY] Retrieved ${rows.length} memories (scores: ${rows.map(r => r.score.toFixed(2)).join(", ")})`);
      }
      return rows.map(r => r.content);
    } finally {
      client.release();
    }
  } catch (err) {
    if (shouldDisableMemory(err)) {
      disableMemory(redactSecrets(err.message), agentId);
      return [];
    }
    console.warn(`[${agentId}][MEMORY] searchMemory failed (non-fatal): ${redactSecrets(err.message)}`);
    return [];
  }
}

// ── Hapus seluruh memori user (perintah /forget) ─────────────────────────────
export async function forgetUser(agentId, userId) {
  if (!memoryRuntimeEnabled) return;
  try {
    const client = await getPool().connect();
    try {
      const res = await client.query(
        `DELETE FROM memories WHERE user_id = $1 AND agent_id = $2`,
        [String(userId), agentId]
      );
      return res.rowCount;
    } finally {
      client.release();
    }
  } catch (err) {
    console.warn(`[${agentId}][MEMORY] forgetUser failed: ${redactSecrets(err.message)}`);
    return 0;
  }
}
