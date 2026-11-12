# Summoner Post-Game Review Protocol

## Purpose

Every Summoner workflow session ends with a post-game review — there is NO
skip path. `done` and emergency `stop` BOTH leave the run POST_GAME_PENDING;
the run becomes CLOSED only after the questionnaire answers AND the journal
entry persist (the reducer's `CompletePostGame` is the sole CLOSED writer).
The journal lives at the FIXED path `.summoner/journal/<run_id>.md` — never
a date-keyed filename.

## Runtime closure contract (single-loop v2)

- **Entry** — `done:` lands POST_GAME_PENDING with kind `normal`; an
  emergency `stop:` lands POST_GAME_PENDING with kind `emergency_minimal`
  AND a minimal exit journal (`kind: emergency_minimal` frontmatter) written
  hook-side IMMEDIATELY — the questionnaire is owed as 补答 debt, never
  skipped. No timer ever closes, skips, or auto-answers a post-game: a
  5-minute idle does nothing.
- **The questionnaire is ONE AskUserQuestion call** — the platform caps 4
  questions and 4 options per question, so exactly FOUR questions ride one
  call: the selected type's questions 1-4. Each question text MUST carry the
  run id AND its marker token `[pg:q1]`…`[pg:q4]` — that pairing is what the
  PostToolUse hook observes. A question without the run id observes nothing
  (it is not this run's questionnaire). An afk/timeout-resolved dialog
  observes NOTHING: markers require actual non-timeout platform answers.
- **Journal** — write the journal at `.summoner/journal/<run_id>.md` (Write
  or Edit; the path is budget-exempt at POST_GAME_PENDING — any other write
  still waits). The observation digest is read from the file AFTER the
  platform executed the tool; the LATEST observed version governs closure
  (rewrite freely before closing; a closed run's journal is final).
- **Closure** — when all four markers are answered AND a journal observation
  matches the file on disk, the hook closes the run: CLOSED, one
  `post_game.completed` and one `workflow.ended` — exactly once. CLOSED
  never reopens; later edits of a closed run's journal observe nothing.
- **Cross-session** — closure happens in the OWNING session (run state is
  keyed by session); a fresh session's SessionStart hook points at the owed
  runs. `/summoner:status` shows the owed markers and the journal path.

## Trigger Rules

Post-game review triggers when the run reaches POST_GAME_PENDING:
- Workflow completes: the user decides `done:` at a checkpoint — kind
  `normal`.
- Emergency stop: the user decides `stop:` at a checkpoint — kind
  `emergency_minimal`, minimal journal written at once, questionnaire
  owed at next resume (补答). A free-text stop signal is NOT a post-game
  trigger (ruling 70): it ends the turn with run state kept — resumable
  via `/summoner:status` — and never advances the run into POST_GAME_PENDING.

Both kinds are mandatory. There is no idle-skip, no auto-close, and no
"stop means skip" rule — that older behavior is retired.

## Multi-Type Collision

A session can trigger multiple review types (e.g., user skipped AND injected knowledge). Priority order:

**Type 1 > Type 5 > Type 3 > Type 2 > Type 4**

When multiple types are triggered, pick the **highest-priority type only** and present a single questionnaire. Do NOT merge or chain multiple questionnaires — that defeats the brevity goal (and the platform caps one call at 4 questions).

**Common collision rules:**
- User says "skip, 我知道怎么修" → both Type 2 and Type 3 trigger → show Type 3 (higher priority)
- User said "别废话" AND made a correction → both Type 5 and Type 1 → show Type 1
- Session completed cleanly but user was verbose about it → Type 4 only (no Type 5 unless explicit complaint)

**Marker binding**: whatever type is selected, its questions 1-4 carry the
markers `[pg:q1]`…`[pg:q4]` (plus the run id) in their question texts — the
pinned observation set is type-independent. The trailing 备注 question is
optional and carries no marker; fold its content into the journal prose.

## Type 1: Direction Correction

**Trigger:** User said "recall / 方向不对 / 换个思路" during the session.

```
📋 赛后复盘 — 方向纠正

1. 你在哪个 Phase 纠正了 AI？
   [诊断 / 复现 / 修复 / 验证 / 审查 / 计划 / 实现 / 其他]

2. AI 当时走的方向是什么？
   （简短描述 AI 的思路路径）

3. 正确的方向是什么？
   （简短描述你纠正后的方向）

4. 如果这个纠正能变成一条规则，怎么写？
   （可选。例："SC_NotFoundInConf 先查 conf 表再查代码，不要直接 grep 源码"）

5. 额外备注（可选）
```

## Type 2: Phase Skipped

**Trigger:** User said "skip / 跳过" during the session.

```
📋 赛后复盘 — Phase 跳过

1. 你跳过了哪个 Phase？
   [复现测试 / 修复 / 验证 / 审查 / 计划 / 其他]

2. 跳过的原因是什么？
   [我知道怎么修 / 太简单不值得 / 环境不支持 / 已有现成方案 / 其他: ___]

3. 这个 Phase 在这个场景下真的不需要吗？
   [是，完全多余 / 不是，我只是赶时间 / 不确定]

4. 什么条件下这个 Phase 应该自动跳过？
   （可选。例："纯配置改动（<5行）不需要复现测试"）

5. 额外备注（可选）
```

## Type 3: Knowledge Injection

**Trigger:** User directly gave the answer (detected when Phase 3 `fix` is skipped with "我知道怎么修" or user provides the solution unprompted).

```
📋 赛后复盘 — 知识注入

1. 你直接告诉 AI 的结论是什么？
   （简短描述）

2. 你是怎么知道这个结论的？
   [经验/直觉 / 之前遇到过 / 其他工具分析过 / 同事告诉我的 / 日志里其实有但 AI 没看到 / 其他: ___]

3. AI 有可能自己推断出这个结论吗？
   [能，它漏看了 → 漏看了哪里的信息？___ / 不能，这是隐性知识 → 要不要写进 skill？ / 不确定]

4. 如果这个知识能写进 skill，建议加到哪个 skill 的什么位置？
   （可选）

5. 额外备注（可选）
```

## Type 4: Full Completion

**Trigger:** Workflow completed all phases without interruption.

```
📋 赛后复盘 — 流程评价

1. 整体流程顺畅吗？
   [1] 很卡 [2] 有点卡 [3] 一般 [4] 顺畅 [5] 非常顺畅

2. 哪个 Phase 最有用？
   [诊断 / 复现 / 修复 / 验证 / 审查 / 定义 / 计划 / 实现 / 其他: ___]

3. 哪个 Phase 最拖时间/最没用？
   [无 / 诊断 / 复现 / 修复 / 验证 / 审查 / 其他: ___]

4. 产物质量如何？
   [生产就绪 / 需要小改 / 需要大改 / 方向错了]

5. 有什么想记住的经验？
   （可选。会被写入 journal 供后续参考）

6. 额外备注（可选）
```

## Type 5: Verbosity Complaint

**Trigger:** User said "别废话 / 简洁点 / 太啰嗦 / too verbose" during the session.

```
📋 赛后复盘 — 啰嗦投诉

1. 什么地方让你觉得啰嗦？
   [检查点报告太长 / Phase 解释过多 / 每次都要"我确认一下" /
    重复我的话 / 代码写完了还逐行解释 / 调用链展开太啰嗦 / 其他: ___]

2. 你想让 AI 砍掉什么？
   （例："定位到代码直接说行号+原因，不需要展开 5 行调用链解释"）

3. 你偏好的沟通风格是？
   [结论先行，细节按需展开 / 安静干活，失败再说话 /
    只报告 checkpoint，中间不啰嗦 / 我只想看 diff / 其他: ___]

4. 如果这个偏好能写成一条规则，怎么写？
   （可选。例："排查代码时 3 句话内说出根因，不要展开推理过程"）

5. 额外备注（可选）
```

## Journal Entry Format

The journal is written to the FIXED path `.summoner/journal/<run_id>.md`
(one file per run; the run id makes it unambiguous even when several runs
share a date):

```markdown
---
run_id: {run id}
date: {ISO date}
workflow: {fix|new|ship|debug|ops|review|release}
kind: {normal|emergency_minimal}
review_type: {1|2|3|4|5}
project: {from summoner.yaml project.name}
---

# {workflow} — {date} — {run id}

{review answers in prose / key-value format}

## Agent Summary
{AI self-reflection: what could have been done better?}
```

An emergency stop may rewrite the hook-written minimal journal in place
(same fixed path) with the full entry — the reducer closes on the LATEST
observed version. After CLOSED the journal is final.

## Insights Pipeline

```
journal/*.md  ──(manual periodic review)──→ insights/
                                              ├── frequent-corrections.md
                                              ├── skip-patterns.md
                                              └── candidate-rules.md
                                                      │
                                                      ▼ (human decision)
                                              Update skill Red Flags / auto-skip rules
```

## Memory Write Protocol

After journal entry is written, classify and persist the pattern into the SQLite memory database.

### Classification

| Review Type | Pattern Type | When to write |
|------------|-------------|---------------|
| Type 1 (direction correction) | `correction` | Always — this is the most valuable signal |
| Type 2 (phase skip) | `skip` | Only if user provided a skip condition (question 4) |
| Type 3 (knowledge injection) | `knowledge` | Only if user said "AI couldn't figure this out" (question 3b) |
| Type 4 (full completion) | — | Only if user rated ≤2 or provided specific actionable feedback |
| Type 5 (verbosity complaint) | `style` | Only if user provided a preference rule (question 4) |

### Feature Extraction

From the session context, extract:
- `name`: kebab-case slug summarizing the pattern (e.g. `config-chain-break-check`)
- `error_codes`: JSON array — scan Phase 1 diagnostic output for SC_* codes, "panic", "nil pointer"
- `modules`: JSON array — from log file paths (e.g. player/task/task.go → "task")
- `keywords`: JSON array — tokenize user's correction/skip reason/knowledge text
- `summary`: 1-2 sentences — the core rule. Pattern: "遇到X时应该先Y再Z"
- `detail`: Extended context from review answers (optional)

### SQL Operations

```sql
-- Check if pattern already exists
SELECT id, hits FROM patterns WHERE name = ?;

-- If exists: increment hits
UPDATE patterns SET hits = hits + 1, updated_at = datetime('now') WHERE name = ?;

-- If new: insert
INSERT INTO patterns (name, type, error_codes, modules, keywords, summary, detail, priority)
VALUES (?, ?, ?, ?, ?, ?, ?, 'medium');

-- Every 10 hits, recalculate priority
UPDATE patterns SET priority =
    CASE WHEN hits >= 6 THEN 'high'
         WHEN hits >= 3 THEN 'medium'
         ELSE 'low'
    END
WHERE name = ? AND hits % 10 = 0;
```

### Duplicate Prevention

When extracting a pattern:
1. Generate candidate `name` from the correction/rule summary
2. Query patterns table by name
3. If name collision and patterns are semantically similar → UPDATE hits (merge)
4. If name collision but different semantics → append suffix to make unique
5. If no collision → INSERT new

Human operator resolves ambiguous merges during periodic review of the memory database.
