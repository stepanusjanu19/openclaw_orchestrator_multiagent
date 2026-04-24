#!/usr/bin/env node
/**
 * bots/userbot/cli.js
 * ──────────────────────────────────────────────────────────────────────────────
 * OpenClaw MTProto UserBot CLI — BotFather Full Automation Engine v2
 *
 * Protocol: JSON over stdin/stdout (one JSON object per line)
 *
 * Commands:
 *   { "cmd": "sendCode",        "phone": "+628..." }
 *   { "cmd": "signIn",          "code": "12345", "phoneCodeHash": "..." }
 *   { "cmd": "signIn2FA",       "password": "..." }
 *   { "cmd": "getCurrentUser" }
 *   { "cmd": "createBot",       "name": "...", "username": "..." }
 *   { "cmd": "listBots" }
 *   { "cmd": "getBotInfo",      "username": "..." }
 *   { "cmd": "getBotToken",     "username": "..." }
 *   { "cmd": "setBotName",      "username": "...", "name": "..." }
 *   { "cmd": "setBotDescription","username": "...", "description": "..." }
 *   { "cmd": "setBotAbout",     "username": "...", "about": "..." }
 *   { "cmd": "setBotCommands",  "username": "...", "commands": [{"cmd":"...","desc":"..."}] }
 *   { "cmd": "setToken",        "username": "...", "regenerate": true }
 *   { "cmd": "deleteBot",       "username": "..." }
 *   { "cmd": "quit" }
 * ──────────────────────────────────────────────────────────────────────────────
 */

import { TelegramClient, Api } from 'telegram';
import { StringSession } from 'telegram/sessions/index.js';
import telegramEvents from 'telegram/events/index.js';
const { NewMessage, Raw } = telegramEvents;

import * as readline from 'readline';
import * as fs from 'fs';
import * as path from 'path';
import { fileURLToPath } from 'url';

// ── Guard stdout — hanya JSON yang boleh lewat ──────────────────────────────
const _rawWrite = process.stdout.write.bind(process.stdout);
process.stdout.write = function (chunk, encoding, callback) {
  const str = typeof chunk === 'string' ? chunk : chunk.toString();
  const clean = str.split('\n')
    .filter(l => l.trim() === '' || l.trim().startsWith('{'))
    .join('\n');
  if (clean.trim()) return _rawWrite(clean + '\n', encoding, callback);
  process.stderr.write(str);
  if (typeof encoding === 'function') encoding();
  else if (callback) callback();
  return true;
};

// ── Load .env ───────────────────────────────────────────────────────────────
const __dirname = path.dirname(fileURLToPath(import.meta.url));
const envPath   = path.resolve(__dirname, '../../credentials/.env');
const envVars   = {};
if (fs.existsSync(envPath)) {
  for (const line of fs.readFileSync(envPath, 'utf8').split('\n')) {
    const t = line.trim();
    if (!t || t.startsWith('#')) continue;
    const eq = t.indexOf('=');
    if (eq < 0) continue;
    envVars[t.slice(0, eq).trim()] = t.slice(eq + 1).trim().replace(/^['"]|['"]$/g, '');
  }
}

const API_ID   = parseInt(envVars.TELEGRAM_API_ID  || process.env.TELEGRAM_API_ID  || '0', 10);
const API_HASH =           envVars.TELEGRAM_API_HASH || process.env.TELEGRAM_API_HASH || '';
const SESSION_FILE = path.resolve(__dirname, '../../credentials/userbot.session');

// ── Helpers ──────────────────────────────────────────────────────────────────
function reply(ok, data = {}) {
  _rawWrite(JSON.stringify({ ok, ...data }) + '\n');
}
function loadSession()         { return fs.existsSync(SESSION_FILE) ? fs.readFileSync(SESSION_FILE, 'utf8').trim() : ''; }
function saveSession(s)        { fs.writeFileSync(SESSION_FILE, s, 'utf8'); }
function sleep(ms)             { return new Promise(r => setTimeout(r, ms)); }

// ── State ────────────────────────────────────────────────────────────────────
let pendingPhoneCodeHash = '';
let pendingPhone         = '';

// ── Validation ───────────────────────────────────────────────────────────────
if (!API_ID || !API_HASH) {
  reply(false, { error: 'TELEGRAM_API_ID / TELEGRAM_API_HASH belum diisi di credentials/.env' });
  process.exit(1);
}

// ── Telegram Client ──────────────────────────────────────────────────────────
const client = new TelegramClient(
  new StringSession(loadSession()),
  API_ID,
  API_HASH,
  { connectionRetries: 5, useWSS: false, logLevel: 'none' }
);

// ════════════════════════════════════════════════════════════════════════════
//  BotFather Conversation Engine v2
//  Mendukung alur text-based DAN inline keyboard (button clicks)
// ════════════════════════════════════════════════════════════════════════════
const BF = '@BotFather';
const TIMEOUT = 20_000;

/**
 * Kirim pesan ke BotFather dan tunggu SATU balasan (text atau keyboard).
 * Mengembalikan Message object lengkap agar pemanggil bisa akses .text & .buttons.
 */
async function bfSend(text) {
  const p = bfWaitReply();
  await client.sendMessage(BF, { message: text });
  return p;
}

/**
 * Tunggu 1 balasan dari BotFather: pesan BARU atau pesan DIEDIT.
 * GramJS versi ini tidak punya EditedMessage event, jadi kita gunakan Raw
 * untuk menangkap update.editMessage dari BotFather secara manual.
 */
function bfWaitReply() {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      client.removeEventHandler(newHandler, newEv);
      client.removeEventHandler(rawHandler, rawEv);
      reject(new Error('Timeout menunggu respons BotFather (>20 detik)'));
    }, TIMEOUT);

    // Tangkap pesan BARU dari BotFather
    const newEv = new NewMessage({ fromUsers: [BF] });
    const newHandler = async (upd) => {
      clearTimeout(timer);
      client.removeEventHandler(newHandler, newEv);
      client.removeEventHandler(rawHandler, rawEv);
      resolve(upd.message);
    };
    client.addEventHandler(newHandler, newEv);

    // Tangkap pesan DIEDIT dari BotFather (digunakan saat klik inline keyboard)
    // Raw event menangkap semua update termasuk editMessage
    const rawEv = new Raw({});
    const rawHandler = async (update) => {
      // Update yang relevan: UpdateEditMessage atau UpdateEditChannelMessage
      const inner = update.message || update.newMessage;
      if (!inner) return;

      // Cek apakah dari BotFather
      const fromBot = inner.peerId?.userId?.toString() ||
                      inner.fromId?.userId?.toString() || '';

      // BotFather user ID: 93372553
      if (fromBot !== '93372553') return;

      // Hanya proses jika ini edit update (bukan new msg yang sudah ditangani)
      const typeName = update.className || update.constructor?.name || '';
      if (!typeName.toLowerCase().includes('edit')) return;

      clearTimeout(timer);
      client.removeEventHandler(newHandler, newEv);
      client.removeEventHandler(rawHandler, rawEv);
      resolve(inner);
    };
    client.addEventHandler(rawHandler, rawEv);
  });
}

/**
 * ═══════════════════════════════════════════════════════════════════════════
 *  BotFather Navigation Engine v3 — Polling-based (reliable)
 *
 *  Masalah dengan event-based approach:
 *  - BotFather MENGEDIT pesan lama setelah klik tombol (bukan kirim baru)
 *  - GramJS versi ini tidak punya EditedMessage event
 *  - Raw event filter kompleks dan tidak reliable
 *
 *  Solusi: Gunakan polling via getMessages() — selalu dapat state terbaru
 * ═══════════════════════════════════════════════════════════════════════════
 */

/**
 * Klik tombol inline dan tunggu respons BotFather via polling.
 * Bekerja baik untuk new message MAUPUN edited message.
 */
async function bfClickAndPoll(msg, btnLabel, pollMs = 20000) {
  // Cari tombol yang cocok
  const buttons = msg.buttons || [];
  let targetBtn = null;
  for (const row of buttons) {
    for (const btn of row) {
      if (btn.text && btn.text.toLowerCase().includes(btnLabel.toLowerCase())) {
        targetBtn = btn; break;
      }
    }
    if (targetBtn) break;
  }
  if (!targetBtn) {
    const avail = buttons.flatMap(r => r.map(b => b.text)).join('", "');
    throw new Error(`Tombol "${btnLabel}" tidak ditemukan. Tersedia: "${avail}"`);
  }

  // Snapshot state sebelum klik
  const beforeMsgs = await client.getMessages(BF, { limit: 1 });
  const beforeId   = beforeMsgs[0]?.id || 0;
  const beforeEdit = beforeMsgs[0]?.editDate || 0;

  // Klik tombol menggunakan method msg.click({text}) yang ada di GramJS Message object
  // Menghindari bug "sharePhone" di GramJS button click internal
  await msg.click({ text: targetBtn.text });

  // Poll sampai ada perubahan
  const deadline = Date.now() + pollMs;
  while (Date.now() < deadline) {
    await sleep(700);
    const after = await client.getMessages(BF, { limit: 1 });
    if (!after[0]) continue;

    // Pesan baru dikirim BotFather
    if (after[0].id > beforeId) return after[0];

    // Pesan lama diedit BotFather
    if (after[0].editDate && after[0].editDate !== beforeEdit) return after[0];
  }
  throw new Error(`BotFather tidak merespons dalam ${pollMs / 1000}s setelah klik tombol "${btnLabel}"`);
}

/**
 * Navigasi ke menu manajemen bot via /mybots → klik nama/username bot.
 * Mengembalikan message berisi menu manajemen bot.
 */
async function bfNavigateToBotMenu(username) {
  const safeUser = username.replace(/^@/, '').toLowerCase();

  // Kirim /mybots
  const msg = await bfSend('/mybots');
  await sleep(800);

  // Ambil salinan segar pesan dengan handler tombol yang proper
  const [freshMsg] = await client.getMessages(BF, { ids: [msg.id] });
  const targetMsg = freshMsg || msg;

  if (!targetMsg.buttons || targetMsg.buttons.length === 0) {
    throw new Error(`BotFather tidak menampilkan daftar bot. Pesan: "${targetMsg.text}"`);
  }

  // Log available buttons untuk debug
  const allBtns = targetMsg.buttons.flatMap(r => r.map(b => b.text));
  process.stderr.write(`[DEBUG] /mybots buttons: ${allBtns.join(', ')}\n`);

  // Coba match berdasarkan: username substring (case-insensitive)
  for (const row of targetMsg.buttons) {
    for (const btn of row) {
      const btnText = (btn.text || '').toLowerCase();
      if (btnText.includes(safeUser) || safeUser.includes(btnText.replace(/^@/, ''))) {
        return bfClickAndPoll(targetMsg, btn.text);
      }
    }
  }
  throw new Error(`Bot "@${safeUser}" tidak ditemukan. Bot yang tersedia: ${allBtns.join(', ')}`);
}

// ════════════════════════════════════════════════════════════════════════════
//  Implementasi Fitur BotFather
// ════════════════════════════════════════════════════════════════════════════

/** Buat bot baru via /newbot */
async function createBot(name, username) {
  const safeUsername = username.replace(/^@/, '').endsWith('bot')
    ? username.replace(/^@/, '')
    : username.replace(/^@/, '') + '_bot';

  const r1 = await bfSend('/newbot');
  if (!r1.text.toLowerCase().includes('name')) throw new Error(`BotFather: ${r1.text}`);

  const r2 = await bfSend(name);
  if (!r2.text.toLowerCase().includes('username')) throw new Error(`BotFather: ${r2.text}`);

  const r3 = await bfSend(safeUsername);
  const tokenMatch = r3.text.match(/(\d{8,12}:[A-Za-z0-9_-]{35,})/);
  if (!tokenMatch) throw new Error(`Gagal ekstrak token. BotFather: ${r3.text}`);

  return { token: tokenMatch[1], username: safeUsername, message: r3.text };
}

/** Daftar semua bot — ekstrak dari tombol inline `/mybots` */
async function listBots() {
  const msg = await bfSend('/mybots');
  if (!msg.buttons || msg.buttons.length === 0) {
    return { raw: msg.text, bots: [] };
  }
  const SKIP = ['create', 'new bot', 'done', 'back', 'cancel'];
  const bots = msg.buttons
    .flatMap(row => row.map(btn => btn.text || ''))
    .filter(t => t.length > 0 && !SKIP.some(s => t.toLowerCase().includes(s)));
  return { raw: msg.text, bots };
}

/** Info singkat bot (validasi bot ada di BotFather) */
async function getBotInfo(username) {
  // Cukup panggil /mybots dan cek apakah username ada di tombol
  const { bots } = await listBots();
  const safeUser = username.replace(/^@/, '').toLowerCase();
  const found = bots.find(b => b.toLowerCase().replace('@', '').includes(safeUser));
  if (!found) throw new Error(`Bot "${username}" tidak ditemukan di akun ini. Tersedia: ${bots.join(', ')}`);
  return { found, username, bots };
}

/** Ambil token API bot via /mybots → bot menu → API Token */
async function getBotToken(username) {
  const menuMsg = await bfNavigateToBotMenu(username);
  await sleep(800);
  // Fetch fresh copy of menu message
  const [freshMenu] = await client.getMessages(BF, { ids: [menuMsg.id] });
  const tokenPanelMsg = await bfClickAndPoll(freshMenu || menuMsg, 'API Token');
  // Token ada di text, atau perlu klik lagi
  const directToken = tokenPanelMsg.text.match(/(\d{8,12}:[A-Za-z0-9_-]{35,})/);
  if (directToken) return { token: directToken[1] };
  // Mungkin ada tombol "Show token" atau sejenisnya
  throw new Error(`Token tidak ditemukan di respons: ${tokenPanelMsg.text}`);
}

/** Revoke & regenerate token bot */
async function revokeToken(username) {
  const menuMsg = await bfNavigateToBotMenu(username);
  await sleep(800);
  const [freshMenu] = await client.getMessages(BF, { ids: [menuMsg.id] });
  const apiTokenMsg = await bfClickAndPoll(freshMenu || menuMsg, 'API Token');
  await sleep(800);
  const [freshApiToken] = await client.getMessages(BF, { ids: [apiTokenMsg.id] });
  const revokeMsg = await bfClickAndPoll(freshApiToken || apiTokenMsg, 'Revoke');
  const tokenMatch = revokeMsg.text.match(/(\d{8,12}:[A-Za-z0-9_-]{35,})/);
  if (!tokenMatch) throw new Error(`Token baru tidak ditemukan. BotFather: ${revokeMsg.text}`);
  return { token: tokenMatch[1] };
}

/**
 * Helper: kirim perintah text ke BotFather, tunggu respons.
 * Digunakan untuk semua perintah SET yang menggunakan alur teks.
 * BotFather setelah /setname dll. akan tampilkan tombol pilih bot,
 * kita tetap mengirim @username sebagai teks — BotFather modern masih
 * menerima ini sebagai input teks.
 */
async function bfTextCommand(cmd, username, value) {
  const safeUser = username.startsWith('@') ? username : '@' + username;

  // Kirim perintah
  await client.sendMessage(BF, { message: cmd });
  await sleep(1200);

  // Ambil respons terbaru dari BotFather
  const r1 = await client.getMessages(BF, { limit: 1 });
  process.stderr.write(`[CMD ${cmd}] BotFather: ${r1[0]?.text}\n`);

  // Kirim username
  await client.sendMessage(BF, { message: safeUser });
  await sleep(1200);

  // Jika halaman ada nilai (setname, setdesc, dll.) kirim nilai
  if (value !== undefined) {
    const r2 = await client.getMessages(BF, { limit: 1 });
    process.stderr.write(`[CMD ${cmd}] After username: ${r2[0]?.text}\n`);
    await client.sendMessage(BF, { message: value });
    await sleep(1200);
  }

  const final = await client.getMessages(BF, { limit: 1 });
  return { message: final[0]?.text || 'OK' };
}

/** Ganti nama tampilan bot */
async function setBotName(username, name) {
  return bfTextCommand('/setname', username, name);
}

/** Set deskripsi bot */
async function setBotDescription(username, description) {
  return bfTextCommand('/setdescription', username, description);
}

/** Set teks About bot */
async function setBotAbout(username, about) {
  return bfTextCommand('/setabouttext', username, about);
}

/** Set daftar perintah bot */
async function setBotCommands(username, commands) {
  const cmdText = commands.map(c => `${c.cmd} - ${c.desc}`).join('\n');
  return bfTextCommand('/setcommands', username, cmdText);
}

/** Hapus bot via /deletebot */
async function deleteBot(username) {
  const safeUser = username.startsWith('@') ? username : '@' + username;
  await client.sendMessage(BF, { message: '/deletebot' });
  await sleep(1000);
  await client.sendMessage(BF, { message: safeUser });
  await sleep(1000);
  // BotFather akan tanya konfirmasi — kirim teks konfirmasi
  await client.sendMessage(BF, { message: 'Yes, I am totally sure.' });
  await sleep(1200);
  const final = await client.getMessages(BF, { limit: 1 });
  return { message: final[0]?.text || 'Bot dihapus' };
}

// ════════════════════════════════════════════════════════════════════════════
//  Command Dispatcher
// ════════════════════════════════════════════════════════════════════════════
async function dispatch(cmd) {
  switch (cmd.cmd) {

    // ── Auth ────────────────────────────────────────────────────────────────

    case 'sendCode': {
      const phone = (cmd.phone || '').trim();
      if (!phone) return reply(false, { error: 'Field "phone" wajib diisi' });
      try {
        const result = await client.sendCode({ apiId: API_ID, apiHash: API_HASH }, phone);
        pendingPhoneCodeHash = result.phoneCodeHash;
        pendingPhone         = phone;
        reply(true, { data: { phoneCodeHash: result.phoneCodeHash } });
      } catch (e) { reply(false, { error: e.message }); }
      break;
    }

    case 'signIn': {
      const { code, phoneCodeHash } = cmd;
      if (!code) return reply(false, { error: 'Field "code" wajib diisi' });
      try {
        await client.invoke(new Api.auth.SignIn({
          phoneNumber:   pendingPhone,
          phoneCodeHash: phoneCodeHash || pendingPhoneCodeHash,
          phoneCode:     code.toString().trim(),
        }));
        saveSession(client.session.save());
        const me = await client.getMe();
        reply(true, { data: { authenticated: true, user: { id: me.id?.toString(), username: me.username, firstName: me.firstName } } });
      } catch (e) {
        if ((e.errorMessage || e.message || '').includes('SESSION_PASSWORD_NEEDED')) {
          reply(false, { error: '2FA_REQUIRED' });
        } else {
          reply(false, { error: e.message || String(e) });
        }
      }
      break;
    }

    case 'signIn2FA': {
      const { password } = cmd;
      if (!password) return reply(false, { error: 'Field "password" wajib diisi' });
      try {
        await client.signInWithPassword(
          { apiId: API_ID, apiHash: API_HASH },
          { password: async () => password, onError: async (e) => { throw e; } }
        );
        saveSession(client.session.save());
        const me = await client.getMe();
        reply(true, { data: { authenticated: true, user: { id: me.id?.toString(), username: me.username, firstName: me.firstName } } });
      } catch (e) { reply(false, { error: e.message }); }
      break;
    }

    case 'getCurrentUser': {
      try {
        const me = await client.getMe();
        reply(true, { data: { id: me.id?.toString(), username: me.username, firstName: me.firstName, authenticated: true } });
      } catch (e) { reply(false, { error: e.message }); }
      break;
    }

    // ── BotFather ───────────────────────────────────────────────────────────

    case 'createBot': {
      const { name, username } = cmd;
      if (!name || !username) return reply(false, { error: 'Field "name" dan "username" wajib diisi' });
      try { reply(true, { data: await createBot(name, username) }); }
      catch (e) { reply(false, { error: e.message }); }
      break;
    }

    case 'listBots': {
      try { reply(true, { data: await listBots() }); }
      catch (e) { reply(false, { error: e.message }); }
      break;
    }

    case 'getBotInfo': {
      const { username } = cmd;
      if (!username) return reply(false, { error: 'Field "username" wajib diisi' });
      try { reply(true, { data: await getBotInfo(username) }); }
      catch (e) { reply(false, { error: e.message }); }
      break;
    }

    case 'getBotToken': {
      const { username } = cmd;
      if (!username) return reply(false, { error: 'Field "username" wajib diisi' });
      try { reply(true, { data: await getBotToken(username) }); }
      catch (e) { reply(false, { error: e.message }); }
      break;
    }

    case 'setToken': {
      const { username, regenerate } = cmd;
      if (!username) return reply(false, { error: 'Field "username" wajib diisi' });
      if (!regenerate) return reply(false, { error: 'Hanya "regenerate: true" yang didukung' });
      try { reply(true, { data: await revokeToken(username) }); }
      catch (e) { reply(false, { error: e.message }); }
      break;
    }

    case 'setBotName': {
      const { username, name } = cmd;
      if (!username || !name) return reply(false, { error: 'Field "username" dan "name" wajib diisi' });
      try { reply(true, { data: await setBotName(username, name) }); }
      catch (e) { reply(false, { error: e.message }); }
      break;
    }

    case 'setBotDescription': {
      const { username, description } = cmd;
      if (!username || !description) return reply(false, { error: 'Field "username" dan "description" wajib diisi' });
      try { reply(true, { data: await setBotDescription(username, description) }); }
      catch (e) { reply(false, { error: e.message }); }
      break;
    }

    case 'setBotAbout': {
      const { username, about } = cmd;
      if (!username || !about) return reply(false, { error: 'Field "username" dan "about" wajib diisi' });
      try { reply(true, { data: await setBotAbout(username, about) }); }
      catch (e) { reply(false, { error: e.message }); }
      break;
    }

    case 'setBotCommands': {
      const { username, commands } = cmd;
      if (!username || !Array.isArray(commands) || commands.length === 0)
        return reply(false, { error: 'Field "username" dan "commands" (array) wajib diisi' });
      try { reply(true, { data: await setBotCommands(username, commands) }); }
      catch (e) { reply(false, { error: e.message }); }
      break;
    }

    case 'deleteBot': {
      const { username } = cmd;
      if (!username) return reply(false, { error: 'Field "username" wajib diisi' });
      try { reply(true, { data: await deleteBot(username) }); }
      catch (e) { reply(false, { error: e.message }); }
      break;
    }

    case 'sendMessage': {
      // Kirim pesan sebagai UserBot (Guest MTProto session) ke chatId target.
      // chatId bisa berupa string angka (group/DM) atau @username.
      const { chatId, text } = cmd;
      if (!chatId || !text) return reply(false, { error: 'Field "chatId" dan "text" wajib diisi' });
      try {
        const peer = String(chatId);
        await client.sendMessage(peer, { message: text, parseMode: 'md' });
        reply(true, { data: { sent: true, chatId: peer } });
      } catch (e) { reply(false, { error: e.message }); }
      break;
    }

    case 'quit': {
      reply(true, { data: { message: 'UserBot shutting down' } });
      await client.disconnect();
      process.exit(0);
      break;
    }

    default:
      reply(false, { error: `Perintah tidak dikenal: ${cmd.cmd}` });
  }
}

// ── Main ─────────────────────────────────────────────────────────────────────
async function main() {
  await client.connect();
  reply(true, { data: { ready: true, authenticated: client.connected && !!loadSession() } });

  const rl = readline.createInterface({ input: process.stdin, terminal: false });
  rl.on('line', async (line) => {
    const t = line.trim();
    if (!t) return;
    try { await dispatch(JSON.parse(t)); }
    catch (e) { reply(false, { error: `JSON parse error: ${e.message}` }); }
  });
  rl.on('close', async () => { await client.disconnect(); process.exit(0); });
}

main().catch((e) => {
  reply(false, { error: `Fatal startup error: ${e.message}` });
  process.exit(1);
});
