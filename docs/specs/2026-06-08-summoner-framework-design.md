# Summoner — AI Agent Orchestration Framework

> 设计日期: 2026-06-08
> 状态: Spec (待评审)

## Objective

构建一个可移植、项目无关的 AI Agent 编排框架。像 **Makefile 定义构建步骤**一样定义 AI 工作流——框架动词固定，项目 skill 可替换。支持任意 Phase 中断暂停（Recall）、赛后复盘（Post-Game Review），作为个人跨项目可复用的 AI 工程基础设施。

### 核心隐喻

**Summoner = 召唤师。** 召唤师选择召唤哪个英雄（skill）上场，知道什么时候进场、什么时候 B 键回城（checkpoint）。每场比赛结束后复盘（Post-Game Review），越打越强。

---

## Architecture

```
┌──────────────────────────────────────────────────────┐
│  summoner.yaml (项目端 — 每个项目一份)                 │
│  声明: debug → my-debug-skill, test → my-test-skill, ...   │
├──────────────────────────────────────────────────────┤
│  Summoner Plugin (框架端 — ~/.claude/plugins/summoner)│
│  ┌────────────┐  ┌────────────┐  ┌────────────────┐  │
│  │ commands/  │  │ skills/    │  │ agents/        │  │
│  │ 薄入口     │  │ summoner/  │  │ 通用 personas  │  │
│  │ /summoner: │  │ SKILL.md   │  │ code-reviewer  │  │
│  │ fix, new,  │  │ 路由中枢   │  │ security-      │  │
│  │ ship, ...  │  │            │  │ auditor        │  │
│  └────────────┘  └────────────┘  │ test-engineer  │  │
│                                  └────────────────┘  │
├──────────────────────────────────────────────────────┤
│  Existing Skills (已有，不改)                          │
│  ┌──────────────┐  ┌──────────────┐                  │
│  │ Superpowers  │  │ Project Domain Skills │                  │
│  │ 通用流程     │  │ 领域流程     │                  │
│  │ brainstorming│  │ my-debug-skill  │                  │
│  │ writing-plans│  │ my-ops-skill    │                  │
│  │ TDD          │  │ my-test-skill   │                  │
│  │ ...          │  │ ...          │                  │
│  └──────────────┘  └──────────────┘                  │
└──────────────────────────────────────────────────────┘
```

### 三层职责

| 层 | 位置 | 职责 | 可移植 |
|----|------|------|:---:|
| Commands | 框架 `commands/` | 用户入口，编排 phase 顺序、退出规则 | ✅ |
| Summoner Skill | 框架 `skills/summoner/` | 读取 manifest、路由 skill、checkpoint 协议 | ✅ |
| Personas | 框架 `agents/` | 通用角色视角（reviewer/auditor/tester） | ✅ |
| Manifest | 项目根 `summoner.yaml` | 声明每个 phase 对应的领域 skill | ❌ 项目特定 |
| Domain Skills | 项目 `skills/` | 领域工作流（my-debug-skill 等） | ❌ 项目特定 |

---

## Plugin Structure

```
~/.claude/plugins/summoner/
├── plugin.json
├── CLAUDE.md
├── AGENTS.md
│
├── skills/
│   └── summoner/
│       └── SKILL.md                    # Meta-skill: 路由中枢
│
├── commands/
│   ├── fix.md                          # /summoner:fix
│   ├── new.md                          # /summoner:new
│   ├── ship.md                         # /summoner:ship
│   ├── debug.md                        # /summoner:debug
│   ├── ops.md                          # /summoner:ops
│   └── review.md                       # /summoner:review
│
├── agents/
│   ├── code-reviewer.md
│   ├── security-auditor.md
│   └── test-engineer.md
│
├── references/
│   ├── manifest-spec.md                # summoner.yaml 字段规范
│   ├── checkpoint-protocol.md          # 中断/恢复机制
│   ├── post-game-review.md             # 赛后复盘协议
│   └── persona-composition.md          # Persona 组合模式
│
├── memory/                             # SQLite 结构化记忆（跨 session 持久化）
│   ├── _index.json                     # project.name → db 文件映射
│   ├── my-project.db                 # 各项目独立的 pattern 数据库
│   └── ...                             # 每个 project.name 一个 .db
│
├── journal/                            # 复盘产物（按项目分目录，运行时写入）
│   └── {project-name}/
│       └── 2026-06-08-bugfix-bug001.md
│
└── scripts/
    └── validate-manifest.sh
```

---

## Manifest Specification

### `summoner.yaml` — 项目端配置文件

```yaml
# summoner.yaml — 项目的 AI 能力声明
# 类比 Makefile：框架动词固定，项目 skill 可替换
# 每个使用 Summoner 的项目在根目录维护此文件

version: "1"

project:
  name: my-project

# === Phase 映射 ============================================
# 框架动词 → 项目 skill
# skill: none 表示显式无此能力，框架回退到通用流程或跳过

phases:
  # --- 诊断 ---
  debug:
    skill: my-debug-skill
    triggers: [config]

  # --- 配置 ---
  config:
    skill: my-config-skill

  # --- 测试 ---
  test:
    skill: my-test-skill

  # --- 运维 ---
  ops:
    skill: my-ops-skill

  # --- 新增模块 ---
  subsystem:
    skill: my-subsystem-skill

  # --- RPC 接口 ---
  rpc:
    skill: my-rpc-skill

  # --- 后台工具 ---
  gmt:
    skill: my-gmt-skill

  # --- 数据库迁移 ---
  migrate:
    skill: my-migrate-skill

  # --- Worktree ---
  worktree:
    skill: my-worktree-skill

  # --- 文档 ---
  docs:
    skill: my-docs-skill

  # --- 通用 phase（默认回退到 superpowers，可覆写）---

  # 定义阶段
  define:
    skill: superpowers:brainstorming

  # 计划阶段
  plan:
    skill: superpowers:writing-plans

  # 审查阶段
  review:
    skill: superpowers:requesting-code-review

  # 验证阶段（复用 test skill，语义侧重"回归验证"）
  verify:
    skill: my-test-skill

  # 复现阶段（复用 test skill，语义侧重"写复现测试"）
  reproduce:
    skill: my-test-skill

  # --- 显式无此能力 ---
  security:
    skill: none

# === 复合工作流 ============================================

# 注: `fix` phase 是 freeform（无对应 skill），
# Summoner 在 checkpoint 后交给用户自由修复，不走固定流程

workflows:
  new-subsystem:
    chain: [define, plan, subsystem, test, review]
    checkpoints: after_each

  bugfix:
    chain: [debug, reproduce, fix, verify, review]
    checkpoints: after_each

  ship:
    fan_out:
      - persona: code-reviewer
      - persona: security-auditor
      - persona: test-engineer
    merge: review
    checkpoints: after_merge
```

### 字段说明

| 字段 | 类型 | 说明 |
|------|------|------|
| `phases.<name>.skill` | string | 项目使用的 skill 名。`none` 表示无此能力。省略时回退 superpowers |
| `phases.<name>.triggers` | list | 本 phase 执行中可能触发的其他 phase |
| `workflows.<name>.chain` | list | 顺序执行的 phase 列表 |
| `workflows.<name>.fan_out` | list | 并行执行的 persona 列表 |
| `workflows.<name>.merge` | string | fan_out 后合并结果的 phase |
| `workflows.<name>.checkpoints` | enum | `after_each` / `after_merge` / `none` |

---

## Commands

### Command 通用结构

每个 Command 文件遵循统一模板：

```markdown
---
description: <一句话>
phase_checkpoints: after_each | after_merge | none
end_action: post_game_review
---

# /summoner:<name>

Invoke <phase mappings from summoner.yaml>.

## Workflow
<phase 链路图>

## Rules
<不可违反的铁律>

## Auto-Skip Conditions
<什么条件下自动跳过某个 phase>

## Rationalizations
<AI 会找的借口及反驳>

## Post-Game Review
<触发 Type 1-5 中的哪种>
```

### 所有 Command 速览

| Command | Phase 链路 | 特点 |
|---------|-----------|------|
| `/summoner:fix` | debug → reproduce → fix → verify → review | Phase 1 铁律，Phase 2-5 按条件可跳过 |
| `/summoner:new` | define → plan → subsystem → test → review | 全链路，未 spec 不动工 |
| `/summoner:ship` | (review ∥ security ∥ test) → merge | fan-out 三人并行，merge 后单 checkpoint |
| `/summoner:debug` | debug only | 轻量版，只诊断不出手修 |
| `/summoner:ops` | ops | 继承项目 ops skill 的阶段化执行 |
| `/summoner:review` | review | 独立审查，不改代码 |

### `/summoner:fix` 完整定义

```markdown
---
description: 修 Bug 全链路 — 诊断根因 → 复现测试 → 修复 → 验证 → 审查
phase_checkpoints: after_each
end_action: post_game_review
---

# /summoner:fix

Invoke the project's debug skill (from summoner.yaml `phases.debug`),
then optionally test skill (`phases.test`), then review.

## Workflow

```
Phase 1 ──→ 诊断根因          (phase.debug)
Phase 2 ──→ 复现测试 (可选)    (phase.test, Prove-It)
Phase 3 ──→ 修复              (freeform)
Phase 4 ──→ 验证              (phase.test, suite)
Phase 5 ──→ 审查              (phase.review)
```

## Rules

1. 每个 Phase 结束后输出 SUMMONER CHECKPOINT，等待用户选择。
2. 用户可在任何 checkpoint 选择: 继续 / 跳过 / 自己修 / 回城 / 停止。
3. **Phase 1 铁律**: 禁止跳过 Phase 1（根因不明 = 后续全瞎）。

## Auto-Skip Conditions

Phase 2 (复现测试) 在以下条件全部满足时自动跳过:
- 纯配置缺失 (如 SC_NotFoundInConf 且只需补数据)
- 或 diff < 5 行且不涉及业务逻辑
- 或用户选择"我知道怎么修"

Phase 5 (审查) 在以下条件满足时自动跳过:
- 单文件改动且 < 30 行
- 或用户说"不用 review"

## Rationalizations

| AI 会想… | 现实 |
|----------|------|
| "错误码很明显，直接修就行" | Phase 1 是铁律。没诊断完不许动手 |
| "用户选跳过，我就帮他全跳了" | 只能跳当前 Phase，不能替用户决定下一个 |
| "这个 bug 太简单，checkpoint 太多" | 啰嗦投诉→赛后复盘记录偏好，运行中不擅自精简 |

## Post-Game Review

流程结束后强制触发赛后复盘问卷。根据结束类型自适应:
- 被纠正 → Type 1 问卷
- 被跳过 → Type 2 问卷
- 被灌输 → Type 3 问卷
- 正常完成 → Type 4 问卷
- 被嫌弃啰嗦 → Type 5 问卷
```

---

## Checkpoint Protocol

### 输出格式

每个 Phase 结束时，框架强制输出以下格式：

```
┌──────────────────────────────────────────────┐
│  ⚡ SUMMONER — Phase {N}/{Total}: {phase名}   │
│                                              │
│  ✅ 完成内容: {这个 phase 产出了什么}           │
│  📋 产物: {文件路径 / 修复方案 / 测试结果}      │
│  ⚠️ 发现: {需要注意的问题}                     │
│                                              │
│  Next:                                       │
│  [enter] 继续下一 Phase                       │
│  [skip]  跳过下一 Phase                       │
│  [done]  完成，退出框架                       │
│  [recall] B 键回城 — 回到之前 Phase 重新来过   │
│  [stop]  紧急停止 — 保留产物，立即退出          │
└──────────────────────────────────────────────┘
```

### 中断信号识别

框架在每次 agent 回复后扫描用户输入，匹配以下信号：

| 用户输入 | 动作 |
|---------|------|
| "stop" / "停" / "我自己来" | 退出框架，保留所有产物 |
| "skip" / "跳过" / "下一步不用了" | 跳过下一 Phase |
| "recall" / "回城" / "方向不对" / "换个思路" | 回到上一 Phase 重新开始 |
| "done" / "够了" / "可以了" | 标记完成，触发复盘后退出 |
| "别废话" / "简洁点" / "太啰嗦" | 记录 Type 5 投诉，精简当前及后续回复 |

### 恢复机制

如果对话因超时/网络中断被截断，用户重新进入时：
- 框架读取上一轮的最后一次 checkpoint 产物
- 从该 checkpoint 恢复，不从头开始
- 已有产物（spec/plan/code/test 结果）保留

---

## Post-Game Review

### 五种复盘类型

**Type 1: 方向纠正**

触发: 用户说"方向不对 / 换思路"

```
1. 在哪个 Phase 纠正了 AI？
   [诊断 / 复现 / 修复 / 验证 / 审查]

2. AI 当时走的方向是什么？
   （简短描述）

3. 正确的方向是什么？
   （简短描述）

4. 如果这个纠正能变成一条规则，怎么写？
   （可选）
```

**Type 2: Phase 跳过**

触发: 用户说"跳过"

```
1. 跳过了哪个 Phase？
   [复现测试 / 修复 / 验证 / 审查]

2. 跳过的原因？
   [我知道怎么修 / 太简单不值得 / 环境不支持 / 已有现成方案 / 其他]

3. 这个 Phase 在这个场景下真的不需要吗？
   [是，完全多余 / 不是，我只是赶时间 / 不确定]

4. 什么条件下这个 Phase 应该自动跳过？
   （可选）
```

**Type 3: 知识灌输**

触发: 用户直接告诉 AI 答案

```
1. 你直接告诉 AI 的结论是什么？

2. 你是怎么知道的？
   [经验/直觉 / 之前遇到过 / 其他工具分析过 / 同事告诉 / 日志里有但 AI 没看到]

3. AI 有可能自己推断出这个结论吗？
   [能，它漏看了 → 哪里的信息？ / 不能，隐性知识 → 是否写入 skill？]
```

**Type 4: 流程评价**

触发: 正常完成全部 Phase

```
1. 整体流程顺畅吗？ [1-5 分]

2. 哪个 Phase 最有用？

3. 哪个 Phase 最拖时间/最没用？

4. 产物质量？ [生产就绪 / 需要小改 / 需要大改 / 方向错了]

5. 有什么想记住的经验？（可选）
```

**Type 5: 啰嗦投诉**

触发: 用户说"别废话 / 简洁点 / 太啰嗦"

```
1. 什么地方让你觉得啰嗦？
   [检查点报告太长 / Phase 解释过多 / 每步都"我确认一下" /
    重复你的话 / 代码写完了还解释 / 其他]

2. 你想让 AI 砍掉什么？
   （例："定位到代码直接说行号+原因，不需要展开调用链解释"）

3. 你偏好的沟通风格？
   [结论先行，细节按需展开 / 安静干活，失败再说话 /
    只报告 checkpoint，中间不啰嗦 / 我只想看 diff]

4. 如果这个偏好能写成一条规则？
   （可选）
```

### 复盘产物流转

```
journal/{date}-{workflow}.md         → 原始复盘记录
        │
        ▼ (定期/手动聚合)
insights/frequent-corrections.md     → 最高频纠正 → 更新 skill Red Flags
insights/skip-patterns.md            → 最常跳过 Phase → 优化 auto-skip 规则
insights/candidate-rules.md          → 隐性知识候选 → 写入 skill/reference
```

---

## Summoner Memory Chain

### Design Philosophy

Post-Game Review produces artifacts, but artifacts alone don't make AI smarter. Memory Chain converts review outputs into **retrievable, structured patterns** that the framework loads on-demand during Phase 0 of matching workflows. The goal: AI encounters a similar problem → automatically recalls how it was handled before.

**Key constraint:** Memory grows over time. Loading ALL memories every session would pollute context. The solution is **namespace-isolated, index-matched, Top-N retrieval** — only load what's relevant.

### Architecture

```
Post-Game Review 完成
      │
      ▼
Summoner 分析本次复盘:
  ├── 是否匹配已有 pattern? → UPDATE hits+1, 补充来源
  ├── 是否新 pattern? → INSERT 新记录
  └── 纯一次性事件? → 只留 journal，不写 memory
      │
      ▼
下次 /summoner:* 启动
      │
Phase 0: Memory Retrieval
  1. 读取 summoner.yaml → project.name (命名空间隔离)
  2. 从用户输入提取特征: error_code, module, keywords
  3. 在当前项目命名空间中检索匹配的 patterns
  4. 返回 Top 5，每条摘要 <200 字
  5. 输出: "📚 Summoner Memory — 匹配到 N 条历史经验"
```

### Namespace Isolation

Memory is isolated by `project.name` declared in each project's `summoner.yaml`. Different projects or divergent worktree branches use different namespaces:

```yaml
# my-project/summoner.yaml (develop/release branches — shared bug experience)
project:
  name: my-project

# my-project-variant/summoner.yaml (wechat branch — isolated, different config system)
project:
  name: my-project-variant
```

| Scenario | Behavior |
|----------|----------|
| develop + release branches | Both have `name: my-project` → share memory |
| wechat branch | `name: my-project-variant` → fully isolated |
| Different project entirely | `name: my-other-project` → fully isolated |

The user controls isolation by setting `project.name`. Same name = shared experience. Different name = clean slate.

### Storage: SQLite

Uses SQLite with WAL mode — zero-conflict with codebase-memory-mcp (separate database files).

```
~/.claude/plugins/summoner/memory/
├── _index.json                    # {project_name: db_filename, ...}
├── my-project.db                # Per-project SQLite databases
├── my-project-variant.db
└── my-side-project.db
```

**Schema:**

```sql
CREATE TABLE IF NOT EXISTS patterns (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,           -- pattern-config-chain-break
    type TEXT NOT NULL,                  -- correction | skip | knowledge | style
    error_codes TEXT,                    -- JSON: ["SC_ErrInnerLogic"]
    modules TEXT,                        -- JSON: ["character","hero"]
    keywords TEXT,                       -- JSON: ["配置缺失","5表链"]
    summary TEXT NOT NULL,               -- 1-2 sentences core rule (loaded in Phase 0)
    detail TEXT,                         -- Full description (loaded on-demand)
    hits INTEGER DEFAULT 1,             -- Reference count
    priority TEXT DEFAULT 'medium',      -- high | medium | low
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS journal (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    date TEXT NOT NULL,
    workflow TEXT NOT NULL,
    review_type INTEGER NOT NULL,
    project TEXT NOT NULL,
    answers TEXT,                        -- JSON: review questionnaire
    agent_summary TEXT,
    created_at TEXT DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_patterns_error_codes ON patterns(error_codes);
CREATE INDEX IF NOT EXISTS idx_patterns_modules ON patterns(modules);
CREATE INDEX IF NOT EXISTS idx_patterns_priority ON patterns(priority);
```

**SQLite pragmas (init once):**

```sql
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;
PRAGMA foreign_keys=ON;
PRAGMA cache_size=-8000;
PRAGMA synchronous=NORMAL;
```

### Phase 0: Retrieval Protocol

```
1. Extract features from user input:
   - error_codes: scan for known codes (SC_ErrInnerLogic, SC_NotFoundInConf, ...)
   - module: infer from log file paths or function names
   - keywords: tokenize and filter

2. Query against current project namespace:
   SELECT name, type, summary, hits, priority
   FROM patterns
   WHERE (error_codes MATCH ? OR modules MATCH ? OR keywords MATCH ?)
   ORDER BY priority DESC, hits DESC
   LIMIT 5

3. Present to user:

┌──────────────────────────────────────────────┐
│  📚 Summoner Memory — 匹配到 2 条历史经验      │
│                                              │
│  🐛 配置关联表断裂 (匹配: ★★★★★)              │
│     SC_NotFoundInConf 类错误先查 conf 源文件，  │
│     再查 5 表关联链，最后才看代码。              │
│     [hits: 3]                                │
│                                              │
│  ⚡ 纯配置补全可跳过复现测试 (匹配: ★★★)        │
│     仅补充 conf 数据不涉及代码逻辑时，           │
│     复现测试 Phase 可跳过。                     │
│     [hits: 3]                                │
│                                              │
│  [enter] 继续  [no] 忽略历史经验               │
└──────────────────────────────────────────────┘

4. If user selects "no": skip memory for this session
5. If user selects "enter": patterns loaded into context for all phases
```

### Write Protocol

```
Post-Game Review answers collected
      │
      ▼
Extract pattern:
  - Review type → pattern type mapping:
    Type 1 (direction correction) → correction
    Type 2 (phase skip) → skip
    Type 3 (knowledge injection) → knowledge
    Type 5 (verbosity complaint) → style

  - Extract error_codes, modules, keywords from user input + phase results
  - Generate summary (1-2 sentences) from review answers
  - Generate detail from full context

Check for existing pattern:
  - Same name? → UPDATE hits+1, updated_at
  - New pattern? → INSERT

Deduplication:
  - Max 3 source references per pattern
  - hits > 10 → candidate for manual promotion into skill Red Flags
```

### Memory Lifecycle

```
hits: 1-2    → priority: low      → loaded only when exact match
hits: 3-5    → priority: medium   → loaded on partial match
hits: 6-10   → priority: high     → loaded aggressively
hits: >10    → candidate           → human review → promote to skill or archive
              (no longer needed — the lesson is baked into a skill's Red Flags)
```

### Token Budget

| Component | Token Budget |
|-----------|:---:|
| Phase 0 memory retrieval prompt | ≤ 200 tokens |
| Per-memory summary (× Top 5) | ≤ 200 tokens each |
| Phase 0 output block (format + labels) | ≤ 100 tokens |
| **Absolute maximum per session** | **≤ 1500 tokens** |

If no patterns match → Phase 0 is skipped entirely (0 token cost).

### Concurrency

SQLite WAL mode handles concurrent read/write safely. Summoner's write pattern is INSERT-only (no read-modify-write), eliminating lost-update risk. Writes occur at session end (Post-Game Review), reads at session start (Phase 0). Simultaneous writes from two sessions on the same project namespace are astronomically unlikely but handled via `busy_timeout=5000` + 3-retry with exponential backoff.

### Comparison with External Projects

| Aspect | ClawMem / ramem | Summoner Memory |
|--------|:---:|:---:|
| Search | BM25 + vector + RRF (complex) | Exact + substring matching (simple, sufficient) |
| Scope | Every session transcript | Post-Game Review patterns only |
| Infrastructure | ONNX embeddings, vector DB | Single SQLite file, no dependencies |
| Token cost | Managed by MCP server | Fixed Top-5 budget |
| Isolation | Per-workspace config | `project.name` in summoner.yaml |

Summoner Memory is deliberately simpler than external memory projects. It doesn't need to index every conversation — only the distilled patterns from structured post-game reviews. This keeps the retrieval precise and the infrastructure zero-dependency.

---

## Personas

### 通用 Persona 定义（cross-project）

三个通用 persona 随框架分发，可跨项目复用：

| Persona | Role | 审查维度 |
|---------|------|---------|
| code-reviewer | Senior Staff Engineer | Correctness / Idiom / Architecture / Security / Impact |
| security-auditor | Security Engineer | OWASP Top 10 / Secrets / Auth / Dependencies |
| test-engineer | QA Engineer | Coverage / Edge Cases / Prove-It / Concurrency |

### 输出格式（统一模板）

```markdown
## {Persona}: {Subject}

### Critical (必须修)
- [file:line] 问题 + 修复建议

### Important (应该修)
- [file:line] 问题 + 修复建议

### Suggestion (建议修)
- [file:line] 问题 + 修复建议

### Summary
- 审查范围: N files, M lines
- 风险等级: low / medium / high
```

### 组合规则

1. Persona 是单角色单输出，不加第二个角色
2. Persona 不调用另一个 Persona
3. Persona 可调用 skill（the *how*）
4. 编排（fan-out / chain / merge）是 Command 的职责

---

## Compatibility with CLAUDE.md / AGENTS.md

### 冲突分析

Summoner 与项目 CLAUDE.md/AGENTS.md **不存在技术冲突**，但存在"认知遗漏"风险：

```
场景 A: 用户显式用 /summoner:fix → Summoner 编排模式，checkpoint 规则生效 ✅
场景 B: 用户说"帮我排查这个 bug" → CLAUDE.md 路由到 my-debug-skill → 直达 skill，绕过编排 ❌
```

**问题不是冲突，而是 CLAUDE.md 不知道 Summoner 的存在。** Agent 走了直达路径，跳过了 checkpoint 和复盘。

### 解决方案

项目 CLAUDE.md 需补充一段 Summoner 集成声明（~15 行）：

```markdown
## AI 编排框架

本项目接入 Summoner 编排框架。

### 命令入口（优先使用）
| 命令 | 场景 |
|------|------|
| `/summoner:fix` | 修 Bug 全链路（诊断→复现→修复→验证→审查） |
| `/summoner:new` | 新增子系统全链路 |
| `/summoner:ship` | 发版前审查（fan-out 并行检查） |
| `/summoner:debug` | 仅诊断，不修复 |
| `/summoner:ops` | 运维操作 |
| `/summoner:review` | 独立代码审查 |

### 规则
- 用户输入 `/summoner:*` → 严格走编排流程（含 checkpoint 暂停）
- 用户意图匹配但未指定命令 → **建议**使用对应 Summoner 命令，不自动选择
- 用户明确说"直接用 X skill" → 跳过编排，直达 skill
- Summoner checkpoint 规则在编排模式下优先于本文件其他快捷规则
```

### 修改后的指令优先级

```
1. 用户显式指令（"/summoner:fix" 或 "直接用 my-debug-skill"）
2. Summoner 编排规则（checkpoint、铁律、复盘）    ← 编排模式下
3. CLAUDE.md 项目规则（代码规范、边界）
4. Superpowers skills（通用流程）
5. 系统默认 prompt
```

---

## Project Integration

### my-project 集成方式

my-project 现有的 10 个技能**不需要修改**。只需在项目根目录添加 `summoner.yaml`（内容已在上方 Manifest 章节）。

现有 skill 触发方式不变：
- 直接调用 `my-debug-skill` / `my-ops-skill` / ... 继续可用
- 通过 Summoner 编排：`/summoner:fix` / `/summoner:new` / ...

### 新项目接入方式

```bash
# 1. 安装 Summoner 插件
cp -r summoner ~/.claude/plugins/summoner/

# 2. 在新项目根目录创建 summoner.yaml
# 至少声明 debug, test, build 三个 phase

# 3. 验证
summoner validate
```

---

## Boundaries

### Always Do

- Phase 1 铁律：`/summoner:fix` 和 `/summoner:debug` 禁止跳过诊断
- 每个 checkpoint 停下来等用户确认
- 流程结束触发 Post-Game Review
- 中断时保留所有产物

### Ask First

- 修改 auto-skip 条件（从 insights 中提取的优化）
- 新增 Command 或 Persona
- 修改 checkpoint 协议格式

### Never Do

- 在用户未确认时自动推进到下一 Phase
- 跳过 Phase 1（即使 auto-skip 也不能跳诊断）
- 静默忽略中断信号
- 在框架层硬编码任何项目名或项目特定路径

---

## Verification

- [ ] `summoner.yaml` 合法性: `scripts/validate-manifest.sh` exit 0
- [ ] 所有 6 个 Command 文件 frontmatter 完整
- [ ] checkpoint 协议: 每种中断信号都能正确识别并响应
- [ ] Post-Game Review: 5 种类型问卷完整
- [ ] **Memory Chain: SQLite schema 创建脚本就绪，project.name 命名空间隔离正确**
- [ ] **Memory Chain: Phase 0 检索协议实现 — 特征提取 → SQL 查询 → Top-5 摘要输出**
- [ ] **Memory Chain: 写入协议实现 — Post-Game Review → pattern 提取 → INSERT/UPDATE**
- [ ] **Memory Chain: Token 预算硬性遵守 — Phase 0 ≤1500 tokens, 无匹配则跳过 (0 token)**
- [ ] my-project 接入: 在 my-project 根目录创建 `summoner.yaml`，所有 10 个 phase 正确映射
- [ ] Personas: 3 个 persona 文件符合单角色单输出规则
- [ ] 无项目名硬编码: grep 框架代码中无 "my-project" / "my-" 等硬编码引用
