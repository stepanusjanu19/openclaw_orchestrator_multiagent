#!/usr/bin/env bash
# scripts/stop-all.sh
# ─────────────────────────────────────────────────────────────────────────────
# Hentikan seluruh proses OpenClaw Multi-Agent Platform secara bersih.
# ─────────────────────────────────────────────────────────────────────────────

BASE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PID_FILE="$BASE_DIR/.pids"

ok()   { echo -e "\033[1;32m✅\033[0m $*"; }
warn() { echo -e "\033[1;33m⚠\033[0m  $*"; }

echo ""
echo "🛑  Menghentikan OpenClaw Multi-Agent Platform..."
echo ""

# ── Gunakan PID file jika tersedia ───────────────────────────────────────────
if [[ -f "$PID_FILE" ]]; then
  while IFS= read -r pid; do
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null && ok "PID $pid dihentikan."
    fi
  done < "$PID_FILE"
  rm -f "$PID_FILE"
fi

# ── Fallback: kill by port ────────────────────────────────────────────────────
kill_port() {
  local port=$1 label=$2
  local pid
  pid=$(lsof -ti tcp:"$port" 2>/dev/null || true)
  if [[ -n "$pid" ]]; then
    kill "$pid" 2>/dev/null && ok "$label (port $port, PID $pid) dihentikan."
  else
    warn "$label (port $port) tidak berjalan."
  fi
}

kill_port 8080 "Go Backend"
kill_port 5173 "Vite Frontend"

# ── Kill stray bot runners ────────────────────────────────────────────────────
BOT_PIDS=$(pgrep -f "bots/agents/.*/index.js" 2>/dev/null || true)
if [[ -n "$BOT_PIDS" ]]; then
  echo "$BOT_PIDS" | xargs kill 2>/dev/null || true
  ok "Bot runners dihentikan."
fi

echo ""
ok "Semua proses dihentikan."
echo ""
