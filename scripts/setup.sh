#!/usr/bin/env bash
# scripts/setup.sh — Initial setup script

set -e

BASE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NODE_BIN="$HOME/.local/share/mise/installs/node/22.22.2/bin"
PATH="$NODE_BIN:$PATH"

echo "═══════════════════════════════════════════════════"
echo "  OpenClaw Multi-Agent Platform — Setup"
echo "═══════════════════════════════════════════════════"
echo ""

# ── Check dependencies ──────────────────────────────────────────────────────
echo "▶ Checking dependencies..."

if ! command -v go &>/dev/null; then
  echo "❌ Go tidak ditemukan. Install dari https://go.dev/dl"
  exit 1
fi
echo "  ✅ Go: $(go version)"

if ! command -v node &>/dev/null; then
  echo "❌ Node.js tidak ditemukan. Install via mise atau nvm."
  exit 1
fi
echo "  ✅ Node: $(node --version)"

if ! command -v openclaw &>/dev/null 2>&1; then
  echo "  ⚠️  openclaw tidak ditemukan. Install dengan:"
  echo "      curl -fsSL https://openclaw.ai/install.sh | bash"
else
  echo "  ✅ OpenClaw: $(openclaw --version 2>/dev/null || echo 'installed')"
fi

echo ""

# ── Install bot dependencies ────────────────────────────────────────────────
echo "▶ Installing bot dependencies..."
cd "$BASE_DIR/bots"
npm install --silent
echo "  ✅ Bot npm packages installed"

echo ""

# ── Build Go backend ─────────────────────────────────────────────────────────
echo "▶ Building Go backend..."
cd "$BASE_DIR/backend"
mkdir -p bin
go build -o bin/multiagent-backend . 2>&1
echo "  ✅ Backend compiled: backend/bin/multiagent-backend"

echo ""

# ── Install frontend dependencies ────────────────────────────────────────────
echo "▶ Installing frontend dependencies..."
cd "$BASE_DIR/frontend"
npm install --silent
echo "  ✅ Frontend dependencies installed"

echo ""

# ── Update BOTS_DIR in credentials ──────────────────────────────────────────
BOTS_DIR="$BASE_DIR/bots"
CRED_FILE="$BASE_DIR/credentials/.env"

if grep -q "BOTS_DIR=" "$CRED_FILE"; then
  sed -i "s|BOTS_DIR=.*|BOTS_DIR=$BOTS_DIR|" "$CRED_FILE"
else
  echo "BOTS_DIR=$BOTS_DIR" >> "$CRED_FILE"
fi
echo "  ✅ BOTS_DIR set to: $BOTS_DIR"

if grep -q "OLLAMA_DIRECT_URL=" "$CRED_FILE"; then
  sed -i "s|OLLAMA_DIRECT_URL=.*|OLLAMA_DIRECT_URL=http://127.0.0.1:11434/api/chat|" "$CRED_FILE"
else
  echo "OLLAMA_DIRECT_URL=http://127.0.0.1:11434/api/chat" >> "$CRED_FILE"
fi
echo "  ✅ OLLAMA_DIRECT_URL set to: http://127.0.0.1:11434/api/chat"

echo ""
echo "═══════════════════════════════════════════════════"
echo "  ✅ Setup selesai!"
echo ""
echo "  Next steps:"
echo "  1. Edit credentials/agents.json — isi telegram_bot_token"
echo "  2. Jalankan: bash scripts/start-all.sh"
echo "  3. Buka dashboard: http://localhost:5173"
echo "═══════════════════════════════════════════════════"
