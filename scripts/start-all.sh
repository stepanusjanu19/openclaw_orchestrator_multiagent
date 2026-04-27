#!/usr/bin/env bash
# scripts/start-all.sh
# ─────────────────────────────────────────────────────────────────────────────
# Satu perintah untuk menjalankan seluruh OpenClaw Multi-Agent Platform:
#   1) Go Backend (API + Process Manager)  → :8080
#   2) Vite Frontend Dev Server            → :5173
#
# Usage:
#   ./scripts/start-all.sh           # jalankan semua
#   ./scripts/start-all.sh --build   # force rebuild Go binary dulu
# ─────────────────────────────────────────────────────────────────────────────

set -euo pipefail

BASE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# ── Node.js path dari mise ────────────────────────────────────────────────────
NODE_BIN="$HOME/.local/share/mise/installs/node/22.22.2/bin"
export PATH="$NODE_BIN:$PATH"

FORCE_BUILD=${1:-}
PID_FILE="$BASE_DIR/.pids"

# ── Helpers ───────────────────────────────────────────────────────────────────
log()   { echo -e "\033[1;36m▶\033[0m $*"; }
ok()    { echo -e "\033[1;32m✅\033[0m $*"; }
warn()  { echo -e "\033[1;33m⚠\033[0m  $*"; }
error() { echo -e "\033[1;31m❌\033[0m $*"; exit 1; }

# ── Pastikan port tidak bentrok ───────────────────────────────────────────────
kill_port() {
  local port=$1
  local pid
  pid=$(lsof -ti tcp:"$port" 2>/dev/null || true)
  if [[ -n "$pid" ]]; then
    warn "Port $port masih dipakai (PID $pid), mematikan..."
    kill "$pid" 2>/dev/null || true
    sleep 0.5
  fi
}

# ── Cleanup handler (Ctrl+C / SIGTERM) ───────────────────────────────────────
cleanup() {
  echo ""
  warn "Menghentikan semua proses..."
  if [[ -f "$PID_FILE" ]]; then
    while IFS= read -r pid; do
      kill "$pid" 2>/dev/null || true
    done < "$PID_FILE"
    rm -f "$PID_FILE"
  fi
  ok "Semua proses dihentikan."
  exit 0
}
trap cleanup INT TERM
rm -f "$PID_FILE"

# ─────────────────────────────────────────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════════════════╗"
echo "║   🐾 OpenClaw Multi-Agent Platform  Launcher    ║"
echo "╚══════════════════════════════════════════════════╝"
echo ""

# ── 1) Build Go Backend ───────────────────────────────────────────────────────
BACKEND_BIN="$BASE_DIR/backend/bin/multiagent-backend"
NEEDS_BUILD=0
if [[ ! -f "$BACKEND_BIN" || "$FORCE_BUILD" == "--build" ]]; then
  NEEDS_BUILD=1
elif find "$BASE_DIR/backend" -name '*.go' -newer "$BACKEND_BIN" -print -quit | grep -q .; then
  NEEDS_BUILD=1
fi

if [[ "$NEEDS_BUILD" == "1" ]]; then
  log "Membangun Go backend..."
  cd "$BASE_DIR/backend"
  go build -o bin/multiagent-backend . || error "Gagal build backend!"
  ok "Backend berhasil dibangun."
else
  ok "Backend binary sudah up-to-date."
fi

# ── 2) Bebaskan port ──────────────────────────────────────────────────────────
kill_port 8080
kill_port 5173

# Bot runners keep their own Telegram polling locks. Stop them explicitly before
# a backend restart so agents cannot keep serving old shared runtime source.
BOT_PIDS=$(pgrep -f "$BASE_DIR/bots/shared/runner.js" 2>/dev/null || true)
if [[ -n "$BOT_PIDS" ]]; then
  warn "Bot runner lama masih aktif, mematikan sebelum start backend..."
  echo "$BOT_PIDS" | xargs kill 2>/dev/null || true
  sleep 1
  BOT_PIDS=$(pgrep -f "$BASE_DIR/bots/shared/runner.js" 2>/dev/null || true)
  if [[ -n "$BOT_PIDS" ]]; then
    echo "$BOT_PIDS" | xargs kill -9 2>/dev/null || true
  fi
fi

# ── 2.5) Start PostgreSQL (pgvector Cognitive Memory) ─────────────────────────
PGCTL="$HOME/.local/share/mise/installs/postgres/16.13/bin/pg_ctl"
PGDATA="$HOME/.local/share/mise/installs/postgres/16.13/data"
if [[ -x "$PGCTL" ]]; then
  PG_STATUS=$("$PGCTL" -D "$PGDATA" status 2>&1 || true)
  if echo "$PG_STATUS" | grep -q "no server running"; then
    log "Menjalankan PostgreSQL (pgvector Cognitive Memory)..."
    "$PGCTL" -D "$PGDATA" -l "$PGDATA/logfile" start > /dev/null 2>&1 && ok "PostgreSQL berjalan" || warn "PostgreSQL gagal start — memory module akan terdegradasi"
  else
    ok "PostgreSQL sudah berjalan"
  fi
else
  warn "pg_ctl tidak ditemukan — skipping PostgreSQL (memory module non-aktif)"
fi

# ── 3) Start Go Backend ───────────────────────────────────────────────────────
log "Menjalankan Go API Backend (port 8080)..."
cd "$BASE_DIR/backend"
"$BACKEND_BIN" -credentials "$BASE_DIR/credentials" &
BACKEND_PID=$!
echo "$BACKEND_PID" >> "$PID_FILE"

# Tunggu backend ready
sleep 1.2
if ! kill -0 "$BACKEND_PID" 2>/dev/null; then
  error "Backend gagal start. Cek log di atas."
fi
ok "Backend berjalan (PID $BACKEND_PID)"

# ── 4) Start Vite Frontend ────────────────────────────────────────────────────
log "Menjalankan Vite Frontend (port 5173)..."
cd "$BASE_DIR/frontend"
npm run dev &
FRONTEND_PID=$!
echo "$FRONTEND_PID" >> "$PID_FILE"

ok "Frontend berjalan (PID $FRONTEND_PID)"

# ─────────────────────────────────────────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════════════════╗"
echo "║  ✅  Platform siap digunakan!                   ║"
echo "║                                                  ║"
echo "║  Dashboard  →  http://localhost:5173             ║"
echo "║  API        →  http://localhost:8080/api/health  ║"
echo "║                                                  ║"
echo "║  Tekan  Ctrl+C  untuk menghentikan semua.        ║"
echo "╚══════════════════════════════════════════════════╝"
echo ""

# Tunggu semua child process
wait
