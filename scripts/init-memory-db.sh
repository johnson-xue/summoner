#!/bin/bash
set -e

# init-memory-db.sh — Initialize Summoner memory database for a project namespace
# Usage: init-memory-db.sh <project-name>

PROJECT="${1:?Usage: init-memory-db.sh <project-name>}"
MEMORY_DIR="$(dirname "$0")/../memory"
DB_FILE="${MEMORY_DIR}/${PROJECT}.db"
INDEX_FILE="${MEMORY_DIR}/_index.json"

mkdir -p "$MEMORY_DIR"

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

INSERT OR IGNORE INTO patterns (name, type, error_codes, modules, keywords, summary, priority)
VALUES
  ('ai-risk-003-global-impact', 'correction', '[]', '[]',
   '["局部修改遗漏全局影响","关联表","createNewRole","调用方","回滚路径"]',
   '修改代码前检查所有调用方和关联表，确保成功路径和失败路径同步更新。新增 init 步骤必须注册到 createNewRole。',
   'high'),
  ('ai-risk-004-pattern-match', 'correction', '[]', '[]',
   '["模式匹配代替验证","代码相似","假设"]',
   '代码相似不等于行为相同。修复 Bug 后必须说明如何验证，禁止仅凭代码阅读就断言修复完成。',
   'high'),
  ('ai-err-005-ok-check', 'correction', '[]', '[]',
   '["conf.GetPB","ok检查","nil pointer","配置访问"]',
   '使用 conf.GetPB* 时必须检查 ok 返回值，!ok 时返回 SC_NotFoundInConf 错误。忽略 ok 检查会导致 nil pointer panic。',
   'high'),
  ('ai-err-003-naked-error', 'correction', '[]', '[]',
   '["裸error","fmt.Errorf","errors.New","PBErrorEnum"]',
   '业务逻辑中不要使用 fmt.Errorf 返回裸 error。使用 errs.PBErrorEnum(msg.EMessageCode_SC_xxx, ...) 包装错误码。',
   'high'),
  ('ai-err-001-gen-files', 'correction', '[]', '[]',
   '["pkg/gen","生成代码","make conf","不可手动编辑"]',
   'pkg/gen/ 目录下的文件是自动生成的，禁止手动编辑。修改源定义后运行 make conf 或 make pb2db 重新生成。',
   'medium');

PRAGMA wal_checkpoint(TRUNCATE);
SQL

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
