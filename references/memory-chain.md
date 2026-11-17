# Summoner Memory Chain Protocol

## Overview

Memory Chain converts Post-Game Review outputs into structured, retrievable patterns that the framework loads on-demand during Phase 0 of matching workflows. The goal: AI encounters a similar problem → automatically recalls how it was handled before.

**Key design constraint:** Memory grows over time. Loading ALL memories every session would pollute context. The solution is namespace-isolated, index-matched, Top-N retrieval — only load what's relevant.

## Architecture

```
Post-Game Review 完成
      │
      ▼
Write Protocol (classification + INSERT/UPDATE)
      │
      ▼
Next /summoner:* session
      │
      ▼
Phase 0: Retrieval Protocol (feature extraction → SQL query → Top-5)
      │
      ▼
Patterns loaded into context (≤1500 tokens)
```

## Namespace Isolation

Memory is isolated by `project.name` declared in each project's `summoner.yaml`. The global `_index.json` maps project names to their database files:

```
${SUMMONER_PLUGIN_ROOT}/memory/          # plugin root = the dir with scripts/ + memory/
├── _index.json              # {"my-project": "my-project.db", ...}
├── my-project.db            # Project-specific patterns + journal
└── my-other-project.db      # Fully isolated
```

## Phase 0: Retrieval Protocol

Executed at the start of every `/summoner:*` workflow, before Phase 1.

### Feature Extraction

From the user's input:
1. **error_codes**: Scan for SC_* patterns, "panic", "nil pointer", "index out of range", etc.
2. **module**: Infer from log file paths, function mentions, or explicit references
   - e.g. `player/task/task.go` → try both `player` (the top subsystem dir, usually the right token) and `task`; `internal/module/game/player/guild/...` → `player` or `guild`
   - The inferred token is matched against the patterns' `modules` column via LIKE. If the most specific token (e.g. `task`) returns nothing, fall back to the broader subsystem token (`player`) before concluding "no match".
3. **keywords**: Tokenize Chinese (jieba-style splitting) and English words

### DB Path Resolution (version-independent, all platforms)

- **Canonical root:** `$SUMMONER_MEMORY_DIR`, default `~/.summoner/memory/` —
  `DB_FILE = <root>/<project-name>.db`. This root does NOT live inside the
  plugin install (plugin-cache paths change on every version bump and used to
  orphan every project DB on upgrade — the 2026-09 fix). Same rule on every
  platform (Claude Code, Gemini, OpenCode, ...).
- **Legacy locations** (pre-2026-09: `<plugin-root>/memory/` in the plugin
  cache, or `~/.claude/plugins/summoner/memory/`): `scripts/init-memory-db.sh
  <project-name>` migrates them by copy-if-missing (original kept as a
  rollback fossil); the SessionStart hook reports "legacy DB found" until
  then. summoner-ctx context DBs migrate automatically on first open.
- If the DB file doesn't exist (and no legacy one does): run
  `${CLAUDE_PLUGIN_ROOT}/scripts/init-memory-db.sh <project-name>` on Claude
  Code; on other platforms — or if init fails — skip Phase 0 and proceed to
  Phase 1.

### SQL Query

```sql
SELECT name, type, summary, hits, priority
FROM patterns
WHERE (
    error_codes LIKE '%' || ? || '%'
    OR modules LIKE '%' || ? || '%'
    OR keywords LIKE '%' || ? || '%'
)
AND priority != 'low'
ORDER BY
    CASE priority WHEN 'high' THEN 0 WHEN 'medium' THEN 1 ELSE 2 END,
    hits DESC
LIMIT 5
```

### Output Format

```
┌──────────────────────────────────────────────┐
│  📚 Summoner Memory — 匹配到 {N} 条历史经验    │
│                                              │
│  {emoji} {name} (匹配: {star-rating})         │
│     {summary}                                │
│     [hits: {N}]                              │
│                                              │
│  [enter] 加载经验继续  [no] 忽略              │
└──────────────────────────────────────────────┘
```

### Star Rating

| Stars | Condition |
|--------|---------|
| ★★★★★ | Exact error_code match + same module |
| ★★★★ | Same module + keyword match |
| ★★★ | Keyword match only |
| ★★ | Low-priority match |

### Token Budget

| Component | Base Budget |
|-----------|:---:|
| Retrieval prompt overhead | 100 tokens |
| Per-pattern summary (× Top 5, base) | 200 tokens each |
| Output block formatting | 100 tokens |
| **Normal total** | **≤1200 tokens** |
| **Hard cap** | **≤1500 tokens** |

**Token estimation:** AI cannot count tokens precisely at runtime. Use character-based approximation:
- 1 token ≈ 3 Chinese characters
- 1 token ≈ 4 English characters  
- Code blocks: count lines × ~8 tokens/line (conservative)
- When in doubt, round up — it's safer to drop to Level 1 than to overflow.

### Graceful Degradation

If the total token count exceeds 1500:

| Level | Action | Token Cap |
|-------|--------|:---:|
| Normal | Top 5 patterns, full summary (200 tokens each) | ≤1200 |
| Level 1 | Top 3 patterns, truncated summary (150 tokens each) | ≤700 |
| Level 2 | Top 1 pattern, short summary (100 tokens) | ≤300 |
| Skip | No patterns loaded, Phase 0 skipped | 0 |

**Degradation triggers (estimate BEFORE rendering — degrade pre-emptively, not after overflow):**
1. Estimate after feature extraction + SQL query (`estimated = count × avg_summary / 3`)
2. estimated ≥ 1000 OR conversation context > 70% of model limit → Level 1 immediately
3. estimated > 700 OR conversation context > 85% of model limit → Level 2
4. even Level 2 would push total context > 90% of model limit → skip Phase 0 entirely
5. Top 5 full summaries > 1500 tokens → Level 1; Top 3 > 1500 → Level 2; Top 1 > 1500 → skip

**Degradation is automatic and SILENT — do not explain the choice, just apply it.
When in doubt, degrade one level lower than you think: erring toward less output
is always correct.**

**Truncation rules:**
- Level 1: Keep first 2 sentences of summary, discard detail
- Level 2: Keep only the 1-line core rule from summary
- Always preserve: error_codes, type, hits count for relevance judgment

## Write Protocol

Executed after Post-Game Review, before session exit.

### Classification

| Review Type | Pattern `type` | When to persist |
|-------------|---------------|----------------|
| Type 1 (direction correction) | `correction` | Always — highest-value signal |
| Type 2 (phase skip) | `skip` | Only if user specified a skip condition (Q4) |
| Type 3 (knowledge injection) | `knowledge` | Only if AI couldn't discover this independently (Q3b) |
| Type 4 (full completion) | — | Only if user rated ≤2 or provided specific feedback |
| Type 5 (verbosity complaint) | `style` | Only if user specified a preference rule (Q4) |

### Feature Extraction for Writing

From the completed Phase context:
- `name`: kebab-case slug (auto-generated or user-provided in Q4)
- `error_codes`: JSON array from Phase 1 diagnostic output
- `modules`: JSON array from log file paths and function names
- `keywords`: JSON array from tokenized correction/feedback text
- `summary`: 1-2 sentences — the core lesson. Pattern: "遇到 X 时应该先 Y 再 Z"
- `detail`: Extended context from review answers (optional)

### SQL Operations

**New pattern:**
```sql
INSERT INTO patterns (name, type, error_codes, modules, keywords, summary, detail, priority)
VALUES (?, ?, ?, ?, ?, ?, ?, 'medium');
```

**Existing pattern (same name):**
```sql
UPDATE patterns SET hits = hits + 1, updated_at = datetime('now') WHERE name = ?;
```

**Priority recalculation (every 10 hits):**
```sql
UPDATE patterns SET priority =
    CASE WHEN hits >= 6 THEN 'high'
         WHEN hits >= 3 THEN 'medium'
         ELSE 'low'
    END
WHERE name = ?;
```

## Duplicate Prevention

When extracting a pattern for writing:
1. Generate candidate `name` from the correction/rule summary
2. Query: `SELECT id, hits FROM patterns WHERE name = ?`
3. If exists → UPDATE hits+1 (don't duplicate)
4. If not → INSERT new

If a new pattern is semantically similar to an existing one but has a different name, the human operator resolves the merge during periodic review.

## Memory Lifecycle

```
hits: 1-2     → priority: low       → Loaded only on exact match
hits: 3-5     → priority: medium    → Loaded on partial match
hits: 6-10    → priority: high      → Loaded aggressively (many keyword matches)
hits: >10     → Candidate            → Human review → promote to skill Red Flag
                                        or archive (baked into skill already)
```

When a pattern is promoted into a skill's Red Flags or Rationalizations section, it should be archived from the memory database — the lesson is now part of the framework itself.

## Concurrency

- **WAL mode**: Reads don't block reads, writes don't block reads. Only writer-writer conflicts.
- **Write pattern**: INSERT-only (new patterns) or atomic UPDATE hits+1. No read-modify-write.
- **Write timing**: Only at session end (Post-Game Review). Simultaneous writes from two sessions on the same namespace are astronomically unlikely.
- **Retry**: `busy_timeout=5000` + 3-retry with exponential backoff (100ms, 200ms, 400ms).

## Init Script

New project namespaces are initialized via `scripts/init-memory-db.sh <project-name>`:
- Creates the SQLite database with WAL mode and all pragmas
- Creates `patterns` and `journal` tables with indices
- Seeds ONE project-agnostic pattern (project-specific seeds are the project's own `--seed <file.sql>`, never the framework's)
- Registers the namespace in `memory/_index.json`
