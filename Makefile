# ═══════════════════════════════════════════════════════════════════════════════
# Makefile — OpenClaw Multi-Agent Platform
# Satu titik kendali untuk build, run, stop, dan reset.
#
# Usage:
#   make          → sama dengan make start
#   make start    → jalankan postgres + backend + frontend sekaligus
#   make stop     → hentikan semua proses
#   make build    → build ulang Go binary
#   make rebuild  → build ulang + langsung start
#   make status   → cek apakah backend, frontend, dan postgres berjalan
#   make db-start → jalankan PostgreSQL (pgvector Cognitive Memory)
#   make db-stop  → hentikan PostgreSQL
#   make clean    → hapus binary hasil build
# ═══════════════════════════════════════════════════════════════════════════════

.PHONY: all start stop build rebuild status clean help db-start db-stop

SCRIPTS_DIR := scripts
BACKEND_DIR := backend
BIN         := $(BACKEND_DIR)/bin/multiagent-backend
PGCTL       := $(HOME)/.local/share/mise/installs/postgres/16.13/bin/pg_ctl
PGDATA      := $(HOME)/.local/share/mise/installs/postgres/16.13/data

# ── Default target ────────────────────────────────────────────────────────────
all: start

# ── Jalankan semua (Go Backend + Vite Frontend) ───────────────────────────────
start:
	@bash $(SCRIPTS_DIR)/start-all.sh

# ── Hentikan semua proses ─────────────────────────────────────────────────────
stop:
	@bash $(SCRIPTS_DIR)/stop-all.sh

# ── Build Go binary saja ──────────────────────────────────────────────────────
build:
	@echo "▶ Building Go backend..."
	@cd $(BACKEND_DIR) && go build -o bin/multiagent-backend .
	@echo "✅ Binary siap: $(BIN)"

# ── Force rebuild lalu start ──────────────────────────────────────────────────
rebuild: stop build
	@bash $(SCRIPTS_DIR)/start-all.sh

# ── Cek status proses ─────────────────────────────────────────────────────────
status:
	@echo ""
	@echo "══ Status Proses ════════════════════════════"
	@( lsof -ti tcp:8080 > /dev/null 2>&1 && echo "  ✅ Go Backend   → http://localhost:8080" ) || echo "  ❌ Go Backend   → tidak berjalan"
	@( lsof -ti tcp:5173 > /dev/null 2>&1 && echo "  ✅ Vite Frontend → http://localhost:5173" ) || echo "  ❌ Vite Frontend → tidak berjalan"
	@( lsof -ti tcp:5432 > /dev/null 2>&1 && echo "  ✅ PostgreSQL    → localhost:5432 (pgvector active)" ) || echo "  ❌ PostgreSQL    → tidak berjalan (memory non-aktif)"
	@echo ""

# ── Start/Stop PostgreSQL (pgvector Cognitive Memory) ─────────────────────────
db-start:
	@echo "▶ Starting PostgreSQL pgvector..."
	@$(PGCTL) -D $(PGDATA) -l $(PGDATA)/logfile start && echo "✅ PostgreSQL started" || echo "⚠ Already running or failed"

db-stop:
	@echo "▶ Stopping PostgreSQL pgvector..."
	@$(PGCTL) -D $(PGDATA) stop && echo "✅ PostgreSQL stopped" || echo "⚠ Not running"

# ── Hapus binary build ────────────────────────────────────────────────────────
clean:
	@rm -f $(BIN)
	@echo "✅ Binary dihapus."

# ── Bantuan ───────────────────────────────────────────────────────────────────
help:
	@echo ""
	@echo "  OpenClaw Multi-Agent Platform — Make Commands"
	@echo ""
	@echo "  make / make start   Jalankan postgres + backend + frontend"
	@echo "  make stop           Hentikan semua proses"
	@echo "  make build          Build Go binary"
	@echo "  make rebuild        Build ulang lalu start"
	@echo "  make status         Cek apakah proses berjalan (incl. pgvector)"
	@echo "  make db-start       Jalankan PostgreSQL pgvector"
	@echo "  make db-stop        Hentikan PostgreSQL pgvector"
	@echo "  make clean          Hapus binary hasil build"
	@echo ""
