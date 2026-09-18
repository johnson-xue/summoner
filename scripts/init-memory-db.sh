#!/bin/bash
set -e

# init-memory-db.sh — Initialize Summoner memory database for a project namespace
# Usage: init-memory-db.sh <project-name> [--seed <extra-patterns.sql>]
# The framework seeds ONE project-agnostic pattern; project-specific seeds
# (your error codes, module names, generated-dir rules) live in YOUR repo and
# are applied via --seed (INSERT OR IGNORE INTO patterns ... statements).

PROJECT="${1:?Usage: init-memory-db.sh <project-name> [--seed <extra.sql>]}"
SEED_FILE=""
if [ "${2:-}" = "--seed" ]; then
  if [ -z "${3:-}" ]; then
    echo "✗ --seed requires a file argument" >&2
    exit 1
  fi
  SEED_FILE="$3"
fi
# project name becomes a filename under memory/ — reject separators/quotes
case "$PROJECT" in
  */*|*..*|*"'"*|*'"'*)
    echo "✗ invalid project name: $PROJECT (no path separators or quotes)" >&2
    exit 1 ;;
esac
# 2026-09: memory lives at a VERSION-INDEPENDENT root — the plugin-cache
# memory/ dir died with every version bump, orphaning each project's DB.
MEMORY_DIR="${SUMMONER_MEMORY_DIR:-$HOME/.summoner/memory}"
DB_FILE="${MEMORY_DIR}/${PROJECT}.db"
INDEX_FILE="${MEMORY_DIR}/_index.json"
LEGACY_DB="$(dirname "$0")/../memory/${PROJECT}.db"

mkdir -p "$MEMORY_DIR"

# Migrate a legacy plugin-cache DB (copy-if-missing, non-destructive: the
# legacy file stays as a rollback fossil; the copy is idempotent).
if [ ! -f "$DB_FILE" ] && [ -f "$LEGACY_DB" ]; then
    cp "$LEGACY_DB" "$DB_FILE"
    [ -f "${LEGACY_DB}-wal" ] && cp "${LEGACY_DB}-wal" "${DB_FILE}-wal"
    [ -f "${LEGACY_DB}-shm" ] && cp "${LEGACY_DB}-shm" "${DB_FILE}-shm"
    echo "✓ Migrated legacy memory DB → $DB_FILE (original kept at $LEGACY_DB)"
fi

sqlite3 "$DB_FILE" <<SQL
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;
PRAGMA foreign_keys=ON;
PRAGMA cache_size=-8000;
PRAGMA synchronous=NORMAL;

CREATE TABLE IF NOT EXISTS patterns (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,
    type TEXT NOT NULL CHECK(type IN ('correction','skip','knowledge','style')),
    error_codes TEXT DEFAULT '[]',
    modules TEXT DEFAULT '[]',
    keywords TEXT DEFAULT '[]',
    summary TEXT NOT NULL,
    detail TEXT DEFAULT '',
    hits INTEGER DEFAULT 1,
    priority TEXT DEFAULT 'medium' CHECK(priority IN ('high','medium','low')),
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS journal (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    date TEXT NOT NULL,
    workflow TEXT NOT NULL,
    review_type INTEGER NOT NULL CHECK(review_type BETWEEN 1 AND 5),
    project TEXT NOT NULL,
    answers TEXT DEFAULT '{}',
    agent_summary TEXT DEFAULT '',
    created_at TEXT DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_patterns_error_codes ON patterns(error_codes);
CREATE INDEX IF NOT EXISTS idx_patterns_modules ON patterns(modules);
CREATE INDEX IF NOT EXISTS idx_patterns_priority ON patterns(priority);
CREATE INDEX IF NOT EXISTS idx_patterns_type ON patterns(type);
CREATE INDEX IF NOT EXISTS idx_journal_date ON journal(date);
CREATE INDEX IF NOT EXISTS idx_journal_workflow ON journal(workflow);

-- Framework default seed: ONE project-agnostic pattern only.
-- Project-specific seeds (error codes, module names, generated-dir rules)
-- belong to the PROJECT, not the framework -- pass them via:
--   init-memory-db.sh <project> --seed <extra.sql>
-- (extra.sql: INSERT OR IGNORE INTO patterns (...) statements.)
INSERT OR IGNORE INTO patterns (name, type, error_codes, modules, keywords, summary, priority)
VALUES
  ('ai-risk-004-pattern-match', 'correction', '[]', '[]',
   '["模式匹配代替验证","代码相似","假设","fix without verification","assumed similar"]',
   '代码相似不等于行为相同。修复 Bug 后必须说明如何验证，禁止仅凭代码阅读就断言修复完成。',
   'high');

PRAGMA wal_checkpoint(TRUNCATE);
SQL

# Project-side seed file: explicitly requested ⇒ missing file is FATAL
# (warn-and-exit-0 would leave an agent-driven setup silently pattern-less)
if [ -n "$SEED_FILE" ]; then
    if [ -f "$SEED_FILE" ]; then
        sqlite3 "$DB_FILE" < "$SEED_FILE"
        echo "✓ Project seed applied: $SEED_FILE"
    else
        echo "✗ Seed file not found: $SEED_FILE" >&2
        exit 1
    fi
fi

# Update index
if [ -f "$INDEX_FILE" ]; then
    python3 -c "
import json, sys
try:
    with open('$INDEX_FILE') as f:
        idx = json.load(f)
except (json.JSONDecodeError, FileNotFoundError):
    idx = {}
idx['${PROJECT}'] = '${PROJECT}.db'
with open('$INDEX_FILE', 'w') as f:
    json.dump(idx, f, indent=2)
"
else
    echo "{\"${PROJECT}\": \"${PROJECT}.db\"}" > "$INDEX_FILE"
fi

echo "✓ Memory database initialized: $DB_FILE"
