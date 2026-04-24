/**
 * base-bot.js — Generic OpenClaw Telegram Bot Runner
 */

import TelegramBot from "node-telegram-bot-api";
import { forgetUser } from "./memory.js";
import { createAgentRuntime } from "./agent-engine.js";

const REQUIRED_ENV = [
  "TELEGRAM_BOT_TOKEN",
  "AGENT_ID",
  "OPENCLAW_MODEL",
  "OPENCLAW_GW_URL",
];

for (const key of REQUIRED_ENV) {
  if (!process.env[key]) {
    console.error(`❌ [base-bot] Missing required env: ${key}`);
    process.exit(1);
  }
}

const BOT_TOKEN = process.env.TELEGRAM_BOT_TOKEN;
const AGENT_ID = process.env.AGENT_ID;
const MODEL = process.env.OPENCLAW_MODEL;
const GW_URL = process.env.OPENCLAW_GW_URL;
const INTERNAL_TOKEN = process.env.OPENCLAW_INTERNAL_TOKEN || "";
const BACKEND_URL = process.env.OPENCLAW_BACKEND_URL || "http://127.0.0.1:8080";
const runtime = createAgentRuntime();

export function createBot() {
  let bot = null;
  let pollingAlive = true;
  let retryCount = 0;
  let isRestarting = false;
  const MAX_RETRIES = 10;
  const processingChats = new Set();

  runtime.startIngressServer();

  function startPolling() {
    if (!pollingAlive) return;
    const delay = retryCount === 0 ? 0 : Math.min(2 ** retryCount * 1000, 30000);

    setTimeout(() => {
      if (!pollingAlive) return;

      bot = new TelegramBot(BOT_TOKEN, {
        polling: {
          interval: 1000,
          autoStart: true,
          params: { timeout: 20 }
        },
        request: {
          agentOptions: {
            family: 4
          }
        }
      });

      bot.on("polling_error", (err) => {
        const msg = err.message || String(err);
        if (msg.includes("409")) {
          if (isRestarting) return;
          isRestarting = true;
          const waitTime = Math.max(5000, Math.min(2 ** retryCount * 2000, 30000));
          console.warn(`⚠️ [${AGENT_ID}] Telegram 409 Conflict — sesi aktif di tempat lain, tunggu ${waitTime / 1000}s lalu retry...`);

          const oldBot = bot;
          bot = null;
          oldBot.stopPolling({ cancel: true }).catch(() => { }).finally(() => {
            retryCount++;
            isRestarting = false;
            if (retryCount <= MAX_RETRIES) startPolling();
            else console.error(`❌ [${AGENT_ID}] Max retry tercapai. Bot tidak dapat terhubung.`);
          });
          return;
        }

        if (msg.includes("ENOTFOUND") || msg.includes("EHOSTUNREACH") || msg.includes("ETIMEDOUT")) {
          console.warn(`⚠️ [${AGENT_ID}] Koneksi Telegram terputus sementara — menunggu...`);
          return;
        }

        if (msg.includes("EFATAL")) return;
        console.error(`⚠️ [${AGENT_ID}] Polling error: ${msg}`);
      });

      bot.on("error", (err) => {
        console.error(`❌ [${AGENT_ID}] Bot error: ${err.message || err}`);
      });

      bot.on("message", async (msg) => {
        retryCount = 0;
        const text = String(msg.text || "").slice(0, 4000);
        const sessionKey = runtime.buildSessionKey("telegram", msg.chat.id);

        if (!text || text.startsWith("/")) {
          if (text === "/start") {
            bot.sendMessage(
              msg.chat.id,
              `👋 Hi! Saya agent *${AGENT_ID}*. Ketik pesanmu dan saya akan jawab.`,
              { parse_mode: "Markdown" }
            );
          }
          if (text === "/forget") {
            const deleted = await forgetUser(AGENT_ID, sessionKey);
            bot.sendMessage(
              msg.chat.id,
              `🧹 Memori jangka panjang Anda sudah dihapus (${deleted ?? 0} entri). Mulai sesi baru!`,
              { parse_mode: "Markdown" }
            ).catch(() => { });
          }
          return;
        }

        const chatId = msg.chat.id;
        const displayName = msg.from?.first_name ?? `[${chatId}]`;
        const SNAPSHOT_MARKERS = [
          "// SYSTEM ERROR SNAPSHOT",
          "\"STATUS\": \"FAILED\"",
          "\"STATUS\": \"SUCCESS\"",
          "============== LOG SNAPSHOT =============="
        ];
        if (SNAPSHOT_MARKERS.some((marker) => text.includes(marker))) {
          return;
        }

        const messageAge = (Date.now() / 1000) - msg.date;
        if (messageAge > 60) {
          console.log(`⏳ [${AGENT_ID}/${chatId}] Skipping old message (age: ${Math.floor(messageAge)}s)`);
          return;
        }

        if (processingChats.has(chatId)) {
          console.log(`⚠️ [${AGENT_ID}/${chatId}] Permintaan sebelumnya belum selesai (Spam Drop)`);
          return;
        }

        processingChats.add(chatId);
        console.log(`📨 [${AGENT_ID}/${chatId}] ${displayName}: ${text.slice(0, 80)}`);
        bot.sendChatAction(chatId, "typing");
        let placeholderMsg = null;
        try {
          placeholderMsg = await bot.sendMessage(chatId, "⏳ _Memproses..._", { parse_mode: "Markdown" });
        } catch (_) { }

        let tick = 0;
        const typingInterval = setInterval(() => {
          if (!bot) return;
          bot.sendChatAction(chatId, "typing").catch(() => { });

          if (placeholderMsg) {
            tick = (tick + 1) % 4;
            const dots = ".".repeat(tick);
            bot.editMessageText(`⏳ _Memproses${dots}_`, {
              chat_id: chatId,
              message_id: placeholderMsg.message_id,
              parse_mode: "Markdown"
            }).catch(() => { });
          }
        }, 4000);

        try {
          const { reply } = await runtime.processMessage({
            channel: "telegram",
            sessionId: String(chatId),
            userText: text
          });

          clearInterval(typingInterval);
          if (placeholderMsg) {
            bot.deleteMessage(chatId, placeholderMsg.message_id).catch(() => { });
          }

          const chunkSize = 4000;
          for (let i = 0; i < reply.length; i += chunkSize) {
            const chunk = reply.substring(i, i + chunkSize);
            await bot.sendMessage(chatId, chunk);
          }
        } catch (err) {
          let detailErr = err.stack ? err.stack : String(err);
          if (detailErr.length > 500) detailErr = detailErr.substring(0, 500) + "...";

          const snapshotError = {
            STATUS: "FAILED",
            AGENT: AGENT_ID,
            ERROR: detailErr,
            TIMESTAMP: new Date().toISOString()
          };

          const snapshotString = JSON.stringify(snapshotError, null, 2);
          console.error(`\n============== LOG SNAPSHOT ==============\n${snapshotString}\n==========================================\n`);
          clearInterval(typingInterval);

          if (placeholderMsg) {
            bot.deleteMessage(chatId, placeholderMsg.message_id).catch(() => { });
          }

          const snapshotText = `\`\`\`json\n// SYSTEM ERROR SNAPSHOT\n${snapshotString}\n\`\`\``;
          try {
            if (!INTERNAL_TOKEN) throw new Error("INTERNAL_TOKEN tidak tersedia");

            const sendRes = await fetch(`${BACKEND_URL}/api/userbot/send`, {
              method: "POST",
              headers: {
                "Content-Type": "application/json",
                "Authorization": `Bearer ${INTERNAL_TOKEN}`
              },
              body: JSON.stringify({ chat_id: String(chatId), text: snapshotText })
            });
            if (!sendRes.ok) throw new Error(`UserBot HTTP ${sendRes.status}`);
            console.log(`[${AGENT_ID}] 📤 Error snapshot dikirim via UserBot ke chat ${chatId}`);
          } catch (ubErr) {
            const logLine = `[${new Date().toISOString()}] [${AGENT_ID}] [CHAT:${chatId}] ${snapshotString}\n`;
            try {
              const { appendFileSync } = await import("fs");
              const { join } = await import("path");
              const logPath = join(process.cwd(), `agent-error-${AGENT_ID}.log`);
              appendFileSync(logPath, logLine, "utf8");
              console.warn(`[${AGENT_ID}][SESSION-LOG] UserBot tidak aktif (${ubErr.message}). Snapshot disimpan ke: ${logPath}`);
            } catch (fsErr) {
              console.warn(`[${AGENT_ID}][SESSION-LOG] Gagal tulis log file (${fsErr.message}). Snapshot hanya di terminal.`);
            }
          }
        } finally {
          clearInterval(typingInterval);
          processingChats.delete(chatId);
        }
      });

      console.log(`🤖 [${AGENT_ID}] Telegram bot started (retry=${retryCount})`);
      console.log(`   Model  : ${MODEL}`);
      console.log(`   Gateway: ${GW_URL}`);
    }, delay);
  }

  const shutdown = (sig) => {
    console.log(`🛑 [${AGENT_ID}] Received ${sig}, stopping bot...`);
    pollingAlive = false;
    runtime.stopIngressServer().catch(() => { });
    if (bot) {
      bot.stopPolling({ cancel: true }).catch(() => { }).finally(() => process.exit(0));
      setTimeout(() => process.exit(0), 3000);
    } else {
      process.exit(0);
    }
  };

  process.on("SIGTERM", () => shutdown("SIGTERM"));
  process.on("SIGINT", () => shutdown("SIGINT"));

  startPolling();
  return { getBot: () => bot };
}
