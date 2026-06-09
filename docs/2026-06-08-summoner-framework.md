# Summoner Framework Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Summoner Claude Code plugin — a portable, project-agnostic AI agent orchestration framework with checkpoint-based interruption and post-game review.

**Architecture:** A Claude Code plugin at `~/.claude/plugins/summoner/` with 6 slash commands, 1 meta-skill, 3 personas, 4 reference docs, 1 script, and 1 per-project YAML manifest. The meta-skill reads the manifest, routes to domain skills, enforces checkpoints, and triggers post-game review.

**Tech Stack:** Markdown (SKILL.md / commands / personas), YAML (manifest), Bash (validation script), Claude Code plugin.json

---

## File Map

```
~/.claude/plugins/summoner/
├── plugin.json                    ← Create: Plugin declaration
├── CLAUDE.md                      ← Create: Agent bootstrap
├── AGENTS.md                      ← Create: Dev guidelines
├── skills/summoner/SKILL.md       ← Create: Routing hub (meta-skill)
├── commands/
│   ├── fix.md                     ← Create: /summoner:fix
│   ├── new.md                     ← Create: /summoner:new
│   ├── ship.md                    ← Create: /summoner:ship
│   ├── debug.md                   ← Create: /summoner:debug
│   ├── ops.md                     ← Create: /summoner:ops
│   └── review.md                  ← Create: /summoner:review
├── agents/
│   ├── code-reviewer.md           ← Create: 5-axis review persona
│   ├── security-auditor.md        ← Create: OWASP audit persona
│   └── test-engineer.md           ← Create: QA coverage persona
├── references/
│   ├── manifest-spec.md           ← Create: summoner.yaml field spec
│   ├── checkpoint-protocol.md     ← Create: Interrupt/recovery spec
│   ├── post-game-review.md        ← Create: 5-type questionnaire
│   └── persona-composition.md     ← Create: Persona composition rules
└── scripts/
    └── validate-manifest.sh       ← Create: YAML validator

my-project/
└── summoner.yaml                  ← Create: Project manifest
```

**Dependency order:**
```
plugin.json ──→ references ──→ meta-skill ──→ commands
                                    │
                              personas (independent)
                                    │
                              scripts (independent)
                                    │
                              summoner.yaml (last, depends on manifest spec)
```

---

### Task 1: Plugin Skeleton

**Files:**
- Create: `~/.claude/plugins/summoner/plugin.json`
- Create: `~/.claude/plugins/summoner/CLAUDE.md`
- Create: `~/.claude/plugins/summoner/AGENTS.md`

- [ ] **Step 1: Create plugin.json**

```json
{
  "name": "summoner",
  "version": "0.1.0",
  "description": "Summoner — AI Agent Orchestration Framework. Portable, project-agnostic workflow orchestration with checkpoint-based interruption and post-game review. Like a Makefile for AI workflows: framework verbs are fixed, project skills are replaceable.",
  "author": "Jingshan Xue",
  "license": "MIT",
  "skills": [
    {
      "name": "summoner",
      "path": "skills/summoner/SKILL.md",
      "description": "Routes user intent through summoner.yaml manifest to domain skills. Enforces checkpoint protocol and post-game review. Use when user invokes any /summoner:* command or expresses intent matching a Summoner workflow."
    }
  ],
  "agents": [
    {
      "name": "code-reviewer",
      "path": "agents/code-reviewer.md",
      "description": "Senior Staff Engineer — 5-axis code review (correctness, idiom, architecture, security, impact)"
    },
    {
      "name": "security-auditor",
      "path": "agents/security-auditor.md",
      "description": "Security Engineer — OWASP Top 10 vulnerability audit, secrets handling, dependency analysis"
    },
    {
      "name": "test-engineer",
      "path": "agents/test-engineer.md",
      "description": "QA Engineer — test strategy, coverage analysis, Prove-It pattern for bug fixes"
    }
  ],
  "commands": [
    { "name": "summoner:fix", "path": "commands/fix.md", "description": "Bug fix full pipeline: diagnose → reproduce → fix → verify → review" },
    { "name": "summoner:new", "path": "commands/new.md", "description": "New feature full pipeline: define → plan → implement → test → review" },
    { "name": "summoner:ship", "path": "commands/ship.md", "description": "Pre-launch review: fan-out 3 personas in parallel → merge → go/no-go decision" },
    { "name": "summoner:debug", "path": "commands/debug.md", "description": "Diagnose only — root cause analysis without making changes" },
    { "name": "summoner:ops", "path": "commands/ops.md", "description": "Ops operations routed through project's ops skill" },
    { "name": "summoner:review", "path": "commands/review.md", "description": "Standalone code review without other phases" }
  ]
}
```

- [ ] **Step 2: Create CLAUDE.md**

```markdown
# Summoner — AI Agent Orchestration Framework

Summoner is a portable, project-agnostic orchestration layer for AI coding agents. It sits between user intent and domain skills, providing structured workflows with checkpoint-based interruption.

## How Summoner Works

1. User invokes a `/summoner:*` command (e.g. `/summoner:fix`) or expresses intent
2. Summoner reads the project's `summoner.yaml` manifest to discover available skills
3. Summoner executes phases in order, pausing at each checkpoint for user confirmation
4. At workflow end, Summoner triggers a post-game review questionnaire

## Key Design Principles

- **Framework verbs are fixed — project skills are replaceable.** Like a Makefile.
- **Every phase ends with a checkpoint.** The user can continue, skip, recall, or stop.
- **Phase 1 is iron law.** For fix/debug, root cause must be found before any code changes.
- **Post-game review is mandatory.** Every session produces a journal entry that feeds back into skill improvement.

## Plugin Structure

```
skills/summoner/SKILL.md    → Meta-skill: reads manifest, routes, enforces protocol
commands/                   → Slash command entry points (thin — just workflow + rules)
agents/                     → Reusable personas (code-reviewer, security-auditor, test-engineer)
references/                 → Protocol specifications (manifest, checkpoint, review, personas)
scripts/                    → Validation and utility scripts
journal/                    → Post-game review artifacts (written at runtime)
```

## Boundaries

- Never hardcode any project name or domain-specific path in the framework
- Never auto-advance past a checkpoint without user confirmation
- Never skip Phase 1 (diagnosis) in fix/debug workflows
- Project CLAUDE.md integration is the project's responsibility, not the framework's
```

- [ ] **Step 3: Create AGENTS.md**

```markdown
# Summoner — AI Agent Development Guidelines

## If You Are an AI Agent

This is the Summoner orchestration framework. It is a Claude Code plugin designed to be portable across projects.

## When Working on This Plugin

### Code You Touch
- `skills/summoner/SKILL.md` — the meta-skill. This is the routing brain.
- `commands/*.md` — slash command definitions. Keep them thin (50-80 lines).
- `agents/*.md` — persona definitions. Single role, single output format.
- `references/*.md` — protocol specs. Reference material, not workflows.
- `scripts/*.sh` — bash utilities. Must use `#!/bin/bash` and `set -e`.

### Hard Rules
1. **Never hardcode project names.** No "my-project", no "my-", no domain-specific paths.
2. **Commands are thin.** They declare workflow phases and rules. The meta-skill does the heavy lifting.
3. **Personas are single-role.** Don't add a second role to an existing persona. Create a new one.
4. **References are specs, not skills.** They define contracts, not workflows.
5. **Checkpoint protocol is sacred.** Don't modify the checkpoint format without updating the spec.

### Adding a New Command
1. Create `commands/<name>.md` with frontmatter (description, phase_checkpoints, end_action)
2. Follow the template: Workflow → Rules → Auto-Skip → Rationalizations → Post-Game Review
3. Add the command to `plugin.json`
4. If the workflow uses new phases, update `references/manifest-spec.md`

### Adding a New Persona
1. Create `agents/<role>.md` with name + description in frontmatter
2. Define: Role, Scope, Review Dimensions, Output Format, Composition rules
3. Add to `plugin.json` agents array
4. Persona must NOT call another persona
5. Persona MAY invoke skills

### Testing
- Validate manifest: `scripts/validate-manifest.sh <path-to-summoner.yaml>`
- Manual: invoke each `/summoner:*` command and verify checkpoint output
- Check that interrupting at each checkpoint type works correctly
```

- [ ] **Step 4: Commit**

```bash
mkdir -p ~/.claude/plugins/summoner/{skills/summoner,commands,agents,references,scripts,journal,insights}
git add plugin.json CLAUDE.md AGENTS.md
git commit -m "feat(summoner): add plugin skeleton — plugin.json, CLAUDE.md, AGENTS.md"
```

---

### Task 2: Reference Documents

**Files:**
- Create: `~/.claude/plugins/summoner/references/manifest-spec.md`
- Create: `~/.claude/plugins/summoner/references/checkpoint-protocol.md`
- Create: `~/.claude/plugins/summoner/references/post-game-review.md`
- Create: `~/.claude/plugins/summoner/references/persona-composition.md`

- [ ] **Step 1: Create manifest-spec.md**

```markdown
# summoner.yaml Manifest Specification

## Overview

Each project using Summoner must have a `summoner.yaml` at its root. This file declares which skills the project provides for each workflow phase. It is the project's "AI capability manifest" — analogous to a Makefile that maps targets to commands.

## Schema

### Top-Level

| Field | Type | Required | Description |
|-------|------|:--------:|-------------|
| `version` | string | yes | Manifest schema version. Currently `"1"`. |
| `project.name` | string | yes | Human-readable project name for journal entries. |
| `phases` | map | yes | Phase name → skill mapping. |
| `workflows` | map | no | Composite workflow definitions (chains and fan-outs). |

### Phase Entry

| Field | Type | Required | Description |
|-------|------|:--------:|-------------|
| `skill` | string | yes | Skill name to invoke. `"none"` means explicit no-capability. Omit to fall back to superpowers default. |
| `triggers` | list | no | Other phases this phase may trigger during execution. |

### Reserved Phase Names

These verbs are recognized by Summoner commands. Projects map them to their domain skills:

| Phase | Default Skill | Purpose |
|-------|--------------|---------|
| `define` | `superpowers:brainstorming` | Requirements and design |
| `plan` | `superpowers:writing-plans` | Task decomposition |
| `debug` | — | Root cause diagnosis |
| `config` | — | Configuration inspection |
| `test` | — | Test execution |
| `verify` | — | Regression verification (reuses test skill) |
| `reproduce` | — | Write reproduction test (reuses test skill) |
| `ops` | — | Server operations |
| `subsystem` | — | New module creation |
| `rpc` | — | TCP service interface |
| `gmt` | — | Admin/backoffice tools |
| `migrate` | — | Database migrations |
| `review` | `superpowers:requesting-code-review` | Code review |
| `security` | — | Security audit |
| `docs` | — | Documentation generation |
| `worktree` | — | Git worktree management |

### Workflow Entry

| Field | Type | Required | Description |
|-------|------|:--------:|-------------|
| `chain` | list | one of chain/fan_out | Sequential phase names to execute in order. |
| `fan_out` | list | one of chain/fan_out | Parallel persona invocations. |
| `merge` | string | with fan_out | Phase to merge fan-out results. |
| `checkpoints` | enum | yes | `after_each`, `after_merge`, or `none`. |

### Workflow Chain Phase Names

Phases in a chain can be:
- A key in `phases` (uses project skill)
- `fix` — freeform fix phase (no skill mapping; user makes changes manually between checkpoints)

## Example

See the my-project `summoner.yaml` for a complete example.
```

- [ ] **Step 2: Create checkpoint-protocol.md**

```markdown
# Summoner Checkpoint Protocol

## Purpose

Checkpoints are the core interruption mechanism in Summoner. After each phase completes, the framework pauses and presents a structured status block. The user chooses how to proceed.

## Checkpoint Output Format

Every phase end MUST output this exact format:

```
┌──────────────────────────────────────────────┐
│  ⚡ SUMMONER — Phase {N}/{Total}: {phase名}   │
│                                              │
│  ✅ 完成内容: {what this phase produced}       │
│  📋 产物: {files / solutions / test results}  │
│  ⚠️ 发现: {issues worth noting}               │
│                                              │
│  Next:                                       │
│  [enter] 继续下一 Phase                       │
│  [skip]  跳过下一 Phase                       │
│  [done]  完成，退出框架                       │
│  [recall] B 键回城 — 回到之前 Phase 重新来过   │
│  [stop]  紧急停止 — 保留产物，立即退出          │
└──────────────────────────────────────────────┘
```

## Field Requirements

- **完成内容**: 1 paragraph max. What was accomplished.
- **产物**: Comma-separated list of concrete artifacts (file paths, code snippets, decision records). Never empty — if nothing was produced, state "No artifacts — analysis only."
- **发现**: Issues, risks, or open questions. "None" if clean.

## Interrupt Signal Grammar

The framework scans EVERY user reply after checkpoint for these signals. Matching is case-insensitive and whitespace-tolerant.

| Signal | Keywords | Action |
|--------|----------|--------|
| CONTINUE | enter, 继续, next, proceed, yes, ok, go | Advance to next phase |
| SKIP | skip, 跳过, 不用, 不需要, skip this | Skip the NEXT phase (not the current one) |
| DONE | done, 够了, 可以了, 完成, finish, good | Mark workflow complete, trigger post-game review, exit |
| RECALL | recall, 回城, 方向不对, 换个思路, go back, redo | Return to previous phase, discard current phase output |
| STOP | stop, 停, 我自己来, 退出, quit, abort | Exit framework immediately, preserve all artifacts, NO post-game review |
| VERBOSE | 别废话, 简洁点, 太啰嗦, too verbose, be brief, tldr | Record Type 5 complaint, condense current and future output |

### Ambiguity Resolution

If user input matches multiple signals:
- STOP > RECALL > DONE > SKIP > CONTINUE (safety-first)
- "stop 方向不对" → STOP wins (highest priority)
- "skip 我自己来" → STOP wins (STOP > SKIP)

If no signal is detected and input doesn't look like a workflow decision:
- Treat as CONTINUE with user feedback (the input may be additional context for the next phase)

## Recovery

If the conversation is interrupted by timeout or disconnection:

1. On reconnect, framework reads the LAST checkpoint block in the conversation
2. Framework restores: current phase number, total phases, completed artifacts
3. Framework outputs: "⚡ SUMMONER RECONNECT — Resuming from Phase {N}/{Total}: {phase名}. Previous artifacts preserved."
4. User can continue, recall, or stop from there.

Artifact preservation: all files written to disk survive. In-memory state (current analysis results) must be reconstructable from the checkpoint block text.
```

- [ ] **Step 3: Create post-game-review.md**

```markdown
# Summoner Post-Game Review Protocol

## Purpose

Every Summoner workflow session ends with a post-game review. The questionnaire adapts based on how the session ended. Review responses are written to `journal/{date}-{workflow}.md`.

## Trigger Rules

Post-game review triggers when:
- Workflow completes all phases (user says "done" or reaches end)
- User says "stop" — SKIP review (emergency stop = no time for questions)
- Framework detects no user input for 5+ minutes after checkpoint — SKIP (user is away)

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

Each review writes to `journal/{date}-{workflow}-{short-slug}.md`:

```markdown
---
date: {ISO date}
workflow: {fix|new|ship|debug|ops|review}
review_type: {1|2|3|4|5}
project: {from summoner.yaml project.name}
---

# {workflow} — {date}

{review answers in prose / key-value format}

## Agent Summary
{AI self-reflection: what could have been done better?}
```

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
```

- [ ] **Step 4: Create persona-composition.md**

```markdown
# Summoner Persona Composition Rules

## Three Layers

| Layer | What | Example | Job |
|-------|------|---------|-----|
| Command | User entry point | `/summoner:ship` | The *when* — orchestrates phases and personas |
| Persona | Role + perspective | `code-reviewer` | The *who* — adopts a viewpoint, produces a report |
| Skill | Workflow + exit criteria | `my-test-skill` | The *how* — step-by-step process |

## Composition Rules

1. **A persona is a single role with a single output format.** If you need a second role, create a second persona.
2. **Personas never call other personas.** Composition is the job of commands.
3. **Personas MAY invoke skills.** The *how* lives in skills.
4. **Commands are the only orchestrators.** They decide which personas to fan out and how to merge results.

## Valid Orchestration: Parallel Fan-Out

`/summoner:ship` is the canonical example:

```
/summoner:ship
  ├── (parallel) code-reviewer    → review report
  ├── (parallel) security-auditor → audit report
  └── (parallel) test-engineer    → coverage report
                  ↓
        merge phase (main agent)
                  ↓
        go/no-go decision + risk summary
```

Why this works:
- Each sub-agent operates on the same diff but produces a DIFFERENT perspective
- No dependencies between sub-agents → genuine parallelism
- Each runs in fresh context → main session stays uncluttered
- Merge step is small and benefits from full context → stays in main agent

## Invalid Orchestration (Anti-Patterns)

### Meta-Orchestrator Persona

```
❌ /work-on-pr → meta-orchestrator persona
                    ↓ (decides "needs review")
                code-reviewer persona
                    ↓ (returns)
                meta-orchestrator (paraphrases result)
```

Why this fails:
- Pure routing layer with no domain value
- Adds two paraphrasing hops → information loss + 2× token cost
- The user already knows they want a review; let them call `/summoner:review` directly
- Replicates work that slash commands and the routing tree already do

### Persona Calling Persona

```
❌ code-reviewer → "this looks like a security issue" → spawns security-auditor
```

Why this fails:
- Personas should report findings, not delegate
- If a review finds security concerns, the REVIEWER notes them; the COMMAND decides whether to fan out security-auditor separately
- On Claude Code: subagents cannot spawn other subagents (hard platform constraint)

## Adding a New Persona

1. Create `agents/<role>.md` with these sections: Role, Scope, Review Dimensions, Output Format, Rules, Composition
2. The Composition section must state: "Invoke directly when / Invoke via / Do NOT invoke from another persona"
3. Add to `plugin.json` agents array
4. If the persona enables a new orchestration pattern, document it here
```

- [ ] **Step 5: Commit**

```bash
git add references/manifest-spec.md references/checkpoint-protocol.md references/post-game-review.md references/persona-composition.md
git commit -m "feat(summoner): add reference documents — manifest spec, checkpoint protocol, post-game review, persona composition"
```

---

### Task 3: Meta-Skill (Routing Hub)

**Files:**
- Create: `~/.claude/plugins/summoner/skills/summoner/SKILL.md`

- [ ] **Step 1: Create SKILL.md**

```markdown
---
name: summoner
description: Routes user intent through summoner.yaml manifest to domain skills. Enforces checkpoint protocol and post-game review. Use when user invokes any /summoner:* command or expresses intent matching a Summoner workflow.
---

# Summoner — AI Agent Orchestration Framework

## Overview

Summoner is the routing hub. It reads the project's `summoner.yaml` manifest, resolves phase names to domain skills, enforces checkpoint pauses between phases, and triggers post-game review at workflow end. It does NOT implement any domain logic itself — that lives in project skills.

## When to Use

- User invokes `/summoner:fix`, `/summoner:new`, `/summoner:ship`, `/summoner:debug`, `/summoner:ops`, or `/summoner:review`
- User expresses intent that matches a Summoner workflow (e.g., "帮我排查这个线上 bug" → suggest `/summoner:fix`)

**When NOT to use:**
- User explicitly says "直接用 my-debug-skill" or names a specific domain skill → route directly, skip Summoner orchestration
- Pure Q&A, no workflow needed → respond directly

## Core Operating Behaviors

### 1. Skill Resolution

On receiving a command, read the project's `summoner.yaml`:

```
1. Locate summoner.yaml at project root
2. If not found: "This project has no summoner.yaml. Summoner needs a manifest to know which skills to use. Create one? [y/n]"
3. If found: resolve each phase in the workflow to its skill
4. If a phase has skill: "none": skip that phase (explicit no-capability)
5. If a phase is not in the manifest: use superpowers default (define → brainstorming, plan → writing-plans, review → requesting-code-review)
```

### 2. Checkpoint Enforcement

After each phase completes:
1. Output the SUMMONER CHECKPOINT block (exact format in `references/checkpoint-protocol.md`)
2. Wait for user response
3. Scan for interrupt signals (per `references/checkpoint-protocol.md` interrupt signal grammar)
4. Execute the selected action: continue / skip / done / recall / stop

**Iron Law:** Never auto-advance past a checkpoint. Never assume the user wants to continue.

### 3. Phase Execution

For each phase in the workflow chain:
1. Read the phase's skill from manifest
2. Invoke the skill via the Skill tool: `Skill(skill="<skill-name>", args="<user's original input>")`
3. The skill runs its internal workflow and returns results
4. Summoner extracts: what was accomplished, artifacts produced, issues found
5. Output checkpoint

For the `fix` phase (freeform — no skill mapping):
1. Present the diagnosis from Phase 1
2. Ask the user: "How would you like to fix this? I can implement the fix, or you can make changes yourself."
3. If user implements: wait for them to confirm changes are done
4. If agent implements: apply changes, show diff

### 4. Post-Game Review

At workflow end (user says "done" or all phases complete):
1. Determine the review type based on session events (corrections, skips, injections, verbosity complaints)
2. Present the appropriate questionnaire from `references/post-game-review.md`
3. Collect answers and write journal entry
4. Agent self-reflection: one paragraph on what could have been done better

## Workflow Definitions

### /summoner:fix

```
Phase 1: debug (MANDATORY) — root cause diagnosis
Phase 2: reproduce (optional) — write failing test via Prove-It pattern
Phase 3: fix (freeform) — apply the fix
Phase 4: verify (optional) — run test suite
Phase 5: review (optional) — code review
```

### /summoner:new

```
Phase 1: define — requirements and design via brainstorming
Phase 2: plan — task decomposition via writing-plans
Phase 3: subsystem — implementation via domain skill
Phase 4: test — verification via domain test skill
Phase 5: review — code review
```

### /summoner:ship

```
Fan-out (parallel):
  - code-reviewer persona
  - security-auditor persona
  - test-engineer persona
Merge: synthesize reports → go/no-go decision
```

### /summoner:debug

```
Phase 1: debug only — diagnose and report, no code changes
```

### /summoner:ops

```
Phase 1: ops — delegated entirely to project's ops skill
```

### /summoner:review

```
Phase 1: review — standalone code review via code-reviewer persona
```

## Auto-Skip Conditions

Phases are automatically offered as skippable (not auto-skipped — user still confirms):

| Workflow | Phase | Skip if |
|----------|-------|---------|
| fix | reproduce | Pure config fix (SC_NotFoundInConf + data-only), diff < 5 lines no logic change |
| fix | verify | Diff < 5 lines, config-only change |
| fix | review | Single file, < 30 lines, no auth/data changes |
| ship | fan_out | Diff < 50 lines AND < 3 files AND no auth/data/config changes |

## Common Rationalizations

| AI thinks... | Reality |
|-------------|---------|
| "The error code makes the root cause obvious, skip Phase 1" | Phase 1 is iron law. What's obvious to you may be wrong. Check first. |
| "User chose skip, I'll skip all remaining phases too" | Only skip the CURRENT phase. Ask again at the next checkpoint. |
| "This is too simple for checkpoints, let me just run through" | Checkpoints are the Summoner contract. Violating them = violating the user's control. Recorded as Type 5 complaint. |
| "I'll just auto-continue since the user usually says yes" | The ONE time they want to stop is the ONE time it matters most. Never assume. |
| "The manifest says 'none' for security, so I'll just warn and move on" | Correct behavior: "Note: this project has no security phase. Proceed without audit?" Let the user decide. |

## Red Flags

- ✗ Advancing past a checkpoint without user confirmation
- ✗ Skipping Phase 1 in fix/debug workflows
- ✗ Not outputting the exact SUMMONER CHECKPOINT format
- ✗ Hardcoding any project name or domain skill name in the framework output
- ✗ Skipping post-game review after workflow completion
- ✗ Personas calling other personas instead of reporting and returning

## Verification

After workflow completion:
- [ ] Every phase had a checkpoint block output
- [ ] User confirmed each checkpoint decision
- [ ] Post-game review questionnaire was presented and answered
- [ ] Journal entry was written
- [ ] No phase was silently skipped (auto-skip still requires user confirmation)
- [ ] All artifacts (files, decisions) are documented in checkpoint blocks
- [ ] Framework output contains zero hardcoded project names

## References

- `references/manifest-spec.md` — summoner.yaml field specification
- `references/checkpoint-protocol.md` — Checkpoint format and interrupt signals
- `references/post-game-review.md` — 5-type questionnaire and journal format
- `references/persona-composition.md` — Persona composition rules and anti-patterns
```

- [ ] **Step 2: Commit**

```bash
git add skills/summoner/SKILL.md
git commit -m "feat(summoner): add meta-skill — routing hub with checkpoint protocol and post-game review"
```

---

### Task 4: Personas

**Files:**
- Create: `~/.claude/plugins/summoner/agents/code-reviewer.md`
- Create: `~/.claude/plugins/summoner/agents/security-auditor.md`
- Create: `~/.claude/plugins/summoner/agents/test-engineer.md`

- [ ] **Step 1: Create code-reviewer.md**

```markdown
---
name: code-reviewer
description: Senior Staff Engineer — 5-axis code review covering correctness, idiom, architecture, security, and impact. Outputs structured report with Critical/Important/Suggestion tiers.
---

# Code Reviewer

## Role

You are a Senior Staff Engineer with 15 years of experience. You review code with surgical precision and unfiltered honesty. You do not sugarcoat. You do not hedge. You call out problems by file:line with concrete fix recommendations.

## Scope

Review the current diff (staged changes or recent commits). If no diff is specified, ask.

## Five-Axis Review

1. **Correctness** — Logic errors? Missing edge cases? Race conditions? Nil pointer dereferences? Unchecked error returns?
2. **Idiom** — Follows project conventions? Uses project error handling patterns? No cross-module internal imports? No generated file edits?
3. **Architecture** — Changes respect module boundaries? No layer violations? Right abstraction level? Dependencies flow in the right direction?
4. **Security** — Input validated? Secrets exposed? SQL injection? Auth checked? Privilege escalation?
5. **Impact** — All callers checked? Related tables synced? Rollback path considered? Migration needed?

## Output Format

```markdown
## Review: {brief summary of what changed}

### Critical (must fix before merge)
- [file:line] **{Issue}** — {Why it matters}. Fix: {concrete suggestion}.

### Important (should fix)
- [file:line] **{Issue}** — {Why it matters}. Fix: {concrete suggestion}.

### Suggestion (consider)
- [file:line] **{Issue}** — {Why}. Fix: {suggestion}.

### Summary
- Files: N | Lines: +M / -K
- Risk: low / medium / high
- Verdict: approve / approve with fixes / request changes
```

## Rules

1. Every finding MUST have a file:line reference.
2. Critical findings must come with a concrete fix suggestion, not just "fix this."
3. If there are no findings for an axis, state it: "Security: No issues found."
4. Do not praise code that "looks good." Engineers don't need validation — they need problems found.
5. If the diff is empty or only whitespace, say so and stop.

## Composition

- **Invoke directly when:** User wants a standalone review of current changes.
- **Invoke via:** `/summoner:ship` (fan-out) or `/summoner:review`.
- **Do NOT invoke from:** Another persona. Report findings and return.
```

- [ ] **Step 2: Create security-auditor.md**

```markdown
---
name: security-auditor
description: Security Engineer — OWASP Top 10 vulnerability audit, secrets detection, authentication/authorization review, dependency analysis
---

# Security Auditor

## Role

You are a Security Engineer specializing in application security. You think like an attacker and review like a defender. Every finding is a potential breach vector until proven otherwise.

## Scope

Review the current diff for security vulnerabilities. Focus on what CHANGED — not a full app audit.

## Audit Dimensions

1. **Input Validation** — User input sanitized? SQL injection? Command injection? XSS? Path traversal?
2. **Secrets & Keys** — Hardcoded keys, tokens, passwords? Secrets in logs or error messages? Encryption keys exposed?
3. **Authentication & Authorization** — Auth check on every endpoint? Privilege escalation possible? Session handling safe?
4. **Data Exposure** — PII logged? Sensitive data in responses? Error messages leak internals?
5. **Dependencies** — New imports? Known vulnerabilities? Supply chain risk?
6. **Configuration** — Default passwords? Debug mode in production? Permissive CORS?

## Output Format

```markdown
## Security Audit: {brief summary}

### Critical (exploitable — fix immediately)
- [file:line] **{Vulnerability}** — Attack vector: {how}. Impact: {what's at risk}. Fix: {concrete mitigation}.

### High (risky — fix before deploy)
- [file:line] **{Issue}** — Risk: {scenario}. Fix: {mitigation}.

### Medium (should fix)
- [file:line] **{Issue}** — Fix: {mitigation}.

### Low / Info
- [file:line] **{Note}**

### Summary
- Critical: N | High: M | Medium: K
- Risk level: low / medium / high / critical
- Safe to deploy: yes / with fixes / no
```

## Rules

1. If a finding is exploitable, describe the ACTUAL attack vector, not a theoretical one.
2. Every critical/high finding needs a concrete fix, not just "use parameterized queries" — show the code pattern.
3. If no security issues found, state clearly: "No security vulnerabilities detected in this diff."
4. Do not flag issues that were pre-existing and unchanged by this diff.

## Composition

- **Invoke directly when:** User wants a security-focused review.
- **Invoke via:** `/summoner:ship` (fan-out).
- **Do NOT invoke from:** Another persona. Report findings and return.
```

- [ ] **Step 3: Create test-engineer.md**

```markdown
---
name: test-engineer
description: QA Engineer — test strategy analysis, coverage gap detection, Prove-It pattern enforcement for bug fixes
---

# Test Engineer

## Role

You are a QA Engineer who believes untested code is broken code. You analyze test coverage for changed code and identify what's NOT being tested — because that's where bugs hide.

## Scope

Review test coverage for the current diff. Identify gaps in:
- Happy path (does it work?)
- Error paths (does it fail correctly?)
- Edge cases (boundaries, empties, extremes)
- Concurrency (race conditions, shared state)

## Analysis Dimensions

1. **Happy Path Coverage** — Is the main success scenario tested?
2. **Error Path Coverage** — Are failure modes tested? Config not found? Invalid input? Timeout?
3. **Edge Cases** — Zero values, max values, empty collections, nil pointers, concurrent access?
4. **Regression Risk** — Could this change break existing tests? Are related tests still passing?
5. **Prove-It Pattern** — For bug fixes: is there a test that FAILED before the fix and PASSES after?

## Output Format

```markdown
## Test Analysis: {brief summary}

### Missing Coverage (must add)
- **{Scenario}**: {what's untested} → Add test: `func Test{Name}_{Scenario}` in {file}

### Weak Coverage (should strengthen)
- **{Scenario}**: {what the test misses} → Add assertion for {condition}

### Adequate Coverage
- {list of scenarios that are well-tested}

### Concurrency Check
- Shared state? yes / no
- Race condition risk? low / medium / high
- {If yes}: Add `-race` flag and concurrent test scenario

### Prove-It Check (bug fixes only)
- Reproduction test before fix: PASS / FAIL
- Reproduction test after fix: PASS / FAIL
- Regression test for the fix scenario: present / missing

### Summary
- Coverage: adequate / needs work / insufficient
- Missing tests: N
- Weak tests: M
```

## Rules

1. For bug fixes, the Prove-It check is MANDATORY. If no reproduction test exists, that's a Critical finding.
2. Don't just say "add more tests" — name the exact test function and scenario.
3. If coverage is fully adequate, say so concisely and stop.

## Composition

- **Invoke directly when:** User wants test coverage analysis.
- **Invoke via:** `/summoner:ship` (fan-out).
- **Do NOT invoke from:** Another persona. Report findings and return.
```

- [ ] **Step 4: Commit**

```bash
git add agents/code-reviewer.md agents/security-auditor.md agents/test-engineer.md
git commit -m "feat(summoner): add personas — code-reviewer, security-auditor, test-engineer"
```

---

### Task 5: Commands

**Files:**
- Create: `~/.claude/plugins/summoner/commands/fix.md`
- Create: `~/.claude/plugins/summoner/commands/new.md`
- Create: `~/.claude/plugins/summoner/commands/ship.md`
- Create: `~/.claude/plugins/summoner/commands/debug.md`
- Create: `~/.claude/plugins/summoner/commands/ops.md`
- Create: `~/.claude/plugins/summoner/commands/review.md`

- [ ] **Step 1: Create fix.md**

```markdown
---
description: 修 Bug 全链路 — 诊断根因 → 复现测试 → 修复 → 验证 → 审查。Phase 1 是铁律，Phase 2-5 可按条件跳过。
phase_checkpoints: after_each
end_action: post_game_review
---

# /summoner:fix

Invoke the project's debug skill (from `summoner.yaml` `phases.debug`), then optionally test skill (`phases.test` via `reproduce`/`verify` phases), then review.

## Workflow

```
Phase 1 ──→ 诊断根因          (phase.debug, MANDATORY)
Phase 2 ──→ 复现测试 (可选)    (phase.reproduce, Prove-It)
Phase 3 ──→ 修复              (freeform, no skill mapping)
Phase 4 ──→ 验证 (可选)       (phase.verify, suite run)
Phase 5 ──→ 审查 (可选)       (phase.review)
```

## Rules

1. 每个 Phase 结束后输出 SUMMONER CHECKPOINT（格式见 `references/checkpoint-protocol.md`），等待用户选择。
2. 用户可在任何 checkpoint 选择: 继续 / 跳过 / 自己修 / 回城 / 停止。
3. **Phase 1 铁律**: 禁止跳过 Phase 1。根因不明 = 后续全瞎。即使 auto-skip 条件满足也不能跳。

## Auto-Skip Conditions

以下条件在 checkpoint 时向用户提议跳过（不自动跳）：

Phase 2 (复现测试):
- 纯配置缺失 (SC_NotFoundInConf 且只需补数据，不涉及代码逻辑)
- diff < 5 行且不涉及业务逻辑
- 用户选择"我知道怎么修"

Phase 4 (验证):
- diff < 5 行且为纯配置改动
- 用户确认"不需要验证"

Phase 5 (审查):
- 单文件改动且 < 30 行
- 不涉及 auth/data/config/env 变更
- 用户说"不用 review"

## Rationalizations

| AI 会想… | 现实 |
|----------|------|
| "错误码很明显，直接修就行" | Phase 1 铁律。没诊断完不许动手。即使 SC_NotFoundInConf 看起来只是缺数据。 |
| "用户选了跳过 Phase 2，我帮他把 Phase 4 也跳了" | 每个 Phase 独立决定。只跳当前 Phase。 |
| "这个 bug 太简单，checkpoint 太多显得啰嗦" | 啰嗦投诉 → Type 5 复盘记录偏好。但运行中不擅自精简 checkpoint。 |
| "用户什么都没说，应该是默认继续" | 必须等用户明确输入。沉默 ≠ 同意。 |

## Post-Game Review

流程结束后强制触发赛后复盘问卷（`references/post-game-review.md`）：
- 被纠正 → Type 1
- 被跳过 → Type 2
- 被灌输 → Type 3
- 正常完成 → Type 4
- 被嫌弃啰嗦 → Type 5

如有多种类型事件，按优先级合并：Type 1 > Type 5 > Type 3 > Type 2 > Type 4
```

- [ ] **Step 2: Create new.md**

```markdown
---
description: 新增功能全链路 — 需求定义 → 任务拆解 → 实现 → 测试 → 审查
phase_checkpoints: after_each
end_action: post_game_review
---

# /summoner:new

Invoke define → plan → subsystem → test → review phases from `summoner.yaml`.

## Workflow

```
Phase 1 ──→ 需求定义   (phase.define, brainstorming)
Phase 2 ──→ 任务拆解   (phase.plan, writing-plans)
Phase 3 ──→ 实现       (phase.subsystem or phase.rpc or phase.gmt)
Phase 4 ──→ 测试       (phase.test)
Phase 5 ──→ 审查       (phase.review)
```

## Rules

1. 每个 Phase 结束后输出 SUMMONER CHECKPOINT，等待用户选择。
2. Phase 1-2 不可跳过（没 spec 不动工，没 plan 不写码）。
3. Phase 3 根据功能类型选择对应 skill（subsystem / rpc / gmt）。
4. 用户可在 Phase 1 后选择 "方向不对" → 回到 brainstorming 重新定义。

## Auto-Skip Conditions

Phase 5 (审查):
- 单文件 < 100 行且无新增 public API
- 用户说"不用 review"

## Rationalizations

| AI 会想… | 现实 |
|----------|------|
| "用户描述得很清楚了，直接写码就行" | 没 spec = 假设当需求。Phase 1 必须出书面设计。 |
| "这个功能简单，plan 和 spec 合并为一步就行" | Spec ≠ Plan。Spec 定义 what，Plan 定义 how。跳过 Plan = 实现顺序靠猜。 |

## Post-Game Review

流程结束后触发复盘问卷。类型规则同 `/summoner:fix`。
```

- [ ] **Step 3: Create ship.md**

```markdown
---
description: 发版前审查 — 并行 fan-out 三个 persona → merge 报告 → go/no-go 决策
phase_checkpoints: after_merge
end_action: post_game_review
---

# /summoner:ship

Fan-out orchestrator. Runs three personas in parallel, merges their reports, produces a go/no-go decision with rollback plan.

## Workflow

```
Phase A (PARALLEL):
  ├── code-reviewer    → review report
  ├── security-auditor → audit report
  └── test-engineer    → coverage report

Phase B (MERGE):
  synthesize → go/no-go + rollback plan
```

## Phase A — Parallel Fan-Out

Spawn all three personas simultaneously using the Agent tool in a single turn:

1. `code-reviewer` — 5-axis review on staged changes / recent commits
2. `security-auditor` — Vulnerability and threat-model pass
3. `test-engineer` — Test coverage analysis

## Phase B — Merge

Synthesize the three reports into one output:

```markdown
## Ship Decision: GO | NO-GO

### Blockers (must fix before ship)
- [Source persona: Critical finding + file:line]

### Recommended fixes (should fix before ship)
- [Source persona: Important finding + file:line]

### Acknowledged risks (shipping anyway)
- [Risk + mitigation]

### Rollback plan
- Trigger conditions: {what signals would prompt rollback}
- Rollback procedure: {exact steps}
- Recovery time objective: {target}

### Reports (full)
- [code-reviewer report]
- [security-auditor report]
- [test-engineer report]
```

## Rules

1. Phase A personas run in PARALLEL — never sequentially.
2. Personas do NOT call each other. The main agent merges in Phase B.
3. Rollback plan is MANDATORY before any GO decision.
4. If any persona returns a Critical finding, default verdict is NO-GO unless user explicitly accepts the risk.
5. **Skip the fan-out** if: changes touch ≤ 2 files AND diff < 50 lines AND no auth/payments/data/config changes. Suggest `/summoner:review` instead.

## Post-Game Review

Trigger Type 4 (流程评价) after ship decision.
```

- [ ] **Step 4: Create debug.md**

```markdown
---
description: 仅诊断 — 根因分析，不出手修复。快速、轻量、单 Phase。
phase_checkpoints: after_each
end_action: post_game_review
---

# /summoner:debug

Invoke the project's debug skill for diagnosis only. No code changes.

## Workflow

```
Phase 1 ──→ 诊断根因 (phase.debug)
           → 输出根因分析报告
           → 如涉及配置问题，触发 phase.config
           → 结束（不进入修复）
```

## Rules

1. Phase 1 结束时输出完整诊断报告：根因、影响范围、修复建议。
2. 不写任何代码。不创建任何测试文件。
3. 诊断报告 = 给用户的输入，用户可以拿着它自己修，或手动 `/summoner:fix`。
4. 如果诊断过程中发现需要配置检查，自动触发 `phases.debug.triggers` 中声明的 phase（如 config）。

## Rationalizations

| AI 会想… | 现实 |
|----------|------|
| "问题很简单，我顺手修了吧" | `/summoner:debug` 的合约是只诊断。想修用 `/summoner:fix`。 |

## Post-Game Review

触发 Type 4 (流程评价)。
```

- [ ] **Step 5: Create ops.md**

```markdown
---
description: 运维操作 — 委托给项目 ops skill 的阶段化执行
phase_checkpoints: after_each
end_action: post_game_review
---

# /summoner:ops

Invoke the project's ops skill from `summoner.yaml` `phases.ops`.

## Workflow

```
Phase 1 ──→ 运维操作 (phase.ops)
```

## Rules

1. ops skill 内部可能有自己的阶段化执行（如 my-ops-skill 的 5 阶段）。Summoner 不干涉 skill 内部流程。
2. ops skill 执行完毕后，输出 SUMMONER CHECKPOINT。
3. 如果 ops skill 失败，输出错误信息，用户决定重试或退出。

## Post-Game Review

触发 Type 4 (流程评价)。
```

- [ ] **Step 6: Create review.md**

```markdown
---
description: 独立代码审查 — 使用 code-reviewer persona，不改代码
phase_checkpoints: after_merge
end_action: post_game_review
---

# /summoner:review

Standalone code review using the code-reviewer persona. No other phases.

## Workflow

```
Phase 1 ──→ 代码审查 (code-reviewer persona)
           → 输出审查报告
           → 结束
```

## Rules

1. 只审查，不修改代码。
2. 使用 `agents/code-reviewer.md` 的 5-axis 审查框架。
3. 输出按 Critical / Important / Suggestion 分级。

## Auto-Skip

如果审查范围内没有 diff（空提交或只有 merge commit）→ 报告 "Nothing to review" 并退出。

## Post-Game Review

触发 Type 4 (流程评价)。
```

- [ ] **Step 7: Commit**

```bash
git add commands/fix.md commands/new.md commands/ship.md commands/debug.md commands/ops.md commands/review.md
git commit -m "feat(summoner): add all 6 commands — fix, new, ship, debug, ops, review"
```

---

### Task 6: Validate Script

**Files:**
- Create: `~/.claude/plugins/summoner/scripts/validate-manifest.sh`

- [ ] **Step 1: Create validate-manifest.sh**

```bash
#!/bin/bash
set -e

# validate-manifest.sh — Validate a summoner.yaml manifest
# Usage: validate-manifest.sh <path-to-summoner.yaml>

MANIFEST="${1:-summoner.yaml}"

if [ ! -f "$MANIFEST" ]; then
    echo "ERROR: $MANIFEST not found" >&2
    exit 1
fi

ERRORS=0

# Check required top-level fields
for field in version project phases; do
    if ! grep -qE "^${field}:" "$MANIFEST"; then
        echo "ERROR: missing required field '$field'" >&2
        ERRORS=$((ERRORS + 1))
    fi
done

# Check version
VERSION=$(grep -oE 'version:.*"[^"]*"' "$MANIFEST" | head -1 | sed 's/.*"\(.*\)".*/\1/')
if [ "$VERSION" != "1" ]; then
    echo "ERROR: version must be \"1\", got \"$VERSION\"" >&2
    ERRORS=$((ERRORS + 1))
fi

# Check project.name
if ! grep -qE '^\s+name:' "$MANIFEST"; then
    echo "ERROR: missing required field 'project.name'" >&2
    ERRORS=$((ERRORS + 1))
fi

# Check that each phase has a 'skill' field
PHASE_COUNT=$(grep -cE '^\s+skill:' "$MANIFEST" || true)
if [ "$PHASE_COUNT" -eq 0 ]; then
    echo "ERROR: no phases with 'skill' field found" >&2
    ERRORS=$((ERRORS + 1))
fi

# Validate workflows section if present
if grep -qE '^workflows:' "$MANIFEST"; then
    # Check each workflow has checkpoints
    WORKFLOW_NAMES=$(grep -oE '^\s{2}[a-z-]+:' "$MANIFEST" | grep -A100 '^workflows:' | tail -n +2 | sed 's/://g' | tr -d ' ')
    for wf in $WORKFLOW_NAMES; do
        if ! grep -A20 "^\s\s${wf}:" "$MANIFEST" | grep -q 'checkpoints:'; then
            echo "ERROR: workflow '$wf' missing 'checkpoints' field" >&2
            ERRORS=$((ERRORS + 1))
        fi
        
        HAS_CHAIN=$(grep -A20 "^\s\s${wf}:" "$MANIFEST" | grep -c 'chain:' || true)
        HAS_FANOUT=$(grep -A20 "^\s\s${wf}:" "$MANIFEST" | grep -c 'fan_out:' || true)
        if [ "$HAS_CHAIN" -eq 0 ] && [ "$HAS_FANOUT" -eq 0 ]; then
            echo "ERROR: workflow '$wf' must have 'chain' or 'fan_out'" >&2
            ERRORS=$((ERRORS + 1))
        fi
        
        if [ "$HAS_FANOUT" -gt 0 ]; then
            if ! grep -A30 "^\s\s${wf}:" "$MANIFEST" | grep -q 'merge:'; then
                echo "ERROR: workflow '$wf' has fan_out but no 'merge' field" >&2
                ERRORS=$((ERRORS + 1))
            fi
        fi
    done
fi

if [ "$ERRORS" -eq 0 ]; then
    echo "✓ $MANIFEST is valid"
    exit 0
else
    echo "✗ $MANIFEST has $ERRORS error(s)" >&2
    exit 1
fi
```

- [ ] **Step 2: Make executable and test**

```bash
chmod +x ~/.claude/plugins/summoner/scripts/validate-manifest.sh
```

- [ ] **Step 3: Commit**

```bash
git add scripts/validate-manifest.sh
git commit -m "feat(summoner): add manifest validation script"
```

---

### Task 7: my-project Integration

**Files:**
- Create: `my-project/summoner.yaml` (in the summoner-design worktree)

This task creates the project manifest that connects Summoner to my-project's 10 domain skills.

- [ ] **Step 1: Create summoner.yaml**

```yaml
# summoner.yaml — my-project AI 能力声明
# Summoner 框架通过此文件了解项目有哪些 skill

version: "1"

project:
  name: my-project

phases:
  # 诊断
  debug:
    skill: my-debug-skill
    triggers: [config]

  # 配置
  config:
    skill: my-config-skill

  # 测试
  test:
    skill: my-test-skill

  # 运维
  ops:
    skill: my-ops-skill

  # 新增子系统
  subsystem:
    skill: my-subsystem-skill

  # TCP RPC 接口
  rpc:
    skill: my-rpc-skill

  # HTTP 后台工具
  gmt:
    skill: my-gmt-skill

  # 数据库迁移
  migrate:
    skill: my-migrate-skill

  # Git worktree
  worktree:
    skill: my-worktree-skill

  # 文档
  docs:
    skill: my-docs-skill

  # 通用阶段
  define:
    skill: superpowers:brainstorming

  plan:
    skill: superpowers:writing-plans

  review:
    skill: superpowers:requesting-code-review

  verify:
    skill: my-test-skill

  reproduce:
    skill: my-test-skill

  # 显式无此能力
  security:
    skill: none

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

- [ ] **Step 2: Validate**

```bash
~/.claude/plugins/summoner/scripts/validate-manifest.sh summoner.yaml
# Expected: ✓ summoner.yaml is valid
```

- [ ] **Step 3: Commit**

```bash
git add summoner.yaml
git commit -m "feat(summoner): add my-project summoner.yaml — project AI capability manifest"
```

---

### Task 8: Final Integration Check

**Files:**
- Verify: `~/.claude/plugins/summoner/plugin.json` references all files correctly
- Verify: All file paths in plugin.json point to files that exist
- Verify: Manifest validates

- [ ] **Step 1: Verify plugin.json completeness**

```bash
cd ~/.claude/plugins/summoner

# Verify all declared files exist
echo "=== Checking plugin.json references ==="
for cmd in fix new ship debug ops review; do
    [ -f "commands/${cmd}.md" ] && echo "✓ commands/${cmd}.md" || echo "✗ MISSING: commands/${cmd}.md"
done

for agent in code-reviewer security-auditor test-engineer; do
    [ -f "agents/${agent}.md" ] && echo "✓ agents/${agent}.md" || echo "✗ MISSING: agents/${agent}.md"
done

[ -f "skills/summoner/SKILL.md" ] && echo "✓ skills/summoner/SKILL.md" || echo "✗ MISSING: skills/summoner/SKILL.md"

echo "=== Validating manifest ==="
bash scripts/validate-manifest.sh summoner.yaml
```

- [ ] **Step 2: Verify no hardcoded project references**

```bash
cd ~/.claude/plugins/summoner

# Check that framework files don't hardcode my-project or my-
echo "=== Checking for hardcoded project names ==="
if grep -r "my-project\|my-" skills/ commands/ agents/ references/ --include="*.md" 2>/dev/null; then
    echo "✗ FOUND hardcoded project references in framework files"
else
    echo "✓ No hardcoded project references found"
fi
```

- [ ] **Step 3: Commit any fixes and finalize**

```bash
git add -A
git commit -m "chore(summoner): final integration check — verify plugin completeness and no hardcoded refs"
```

---

---

### Task 9: SQLite Memory Database

**Files:**
- Create: `~/.claude/plugins/summoner/scripts/init-memory-db.sh`
- Create: `~/.claude/plugins/summoner/memory/_index.json` (empty, init)

**Description:** Create the SQLite schema initialization script and bootstrap the memory database system.

- [ ] **Step 1: Create init-memory-db.sh**

Write to `~/.claude/plugins/summoner/scripts/init-memory-db.sh`:

```bash
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
```

- [ ] **Step 2: Make executable and test**

```bash
chmod +x ~/.claude/plugins/summoner/scripts/init-memory-db.sh
~/.claude/plugins/summoner/scripts/init-memory-db.sh my-project
# Expected: ✓ Memory database initialized: .../memory/my-project.db

# Verify tables exist
sqlite3 ~/.claude/plugins/summoner/memory/my-project.db ".tables"
# Expected: journal  patterns
```

- [ ] **Step 3: Commit**

```bash
git add scripts/init-memory-db.sh memory/_index.json
git commit -m "feat(summoner): add SQLite memory database init script with seed patterns"
```

---

### Task 10: Memory Chain Reference Doc

**Files:**
- Create: `~/.claude/plugins/summoner/references/memory-chain.md`

**Description:** Create the reference document specifying the Memory Chain protocol: Phase 0 retrieval, write protocol, namespace isolation, token budget, and lifecycle.

- [ ] **Step 1: Create memory-chain.md**

Write to `~/.claude/plugins/summoner/references/memory-chain.md` with these sections:

1. **Overview** — Memory Chain converts Post-Game Review outputs into structured, retrievable patterns
2. **Namespace Isolation** — `project.name` in summoner.yaml controls which .db file is used
3. **Phase 0 Retrieval Protocol** — Feature extraction → SQL query → Top-5 → output format
4. **Write Protocol** — Post-Game Review completion → pattern classification → INSERT/UPDATE
5. **Token Budget** — 0 if no match, ≤1500 tokens if matched
6. **Lifecycle** — hits 1-2 (low) → 3-5 (medium) → 6-10 (high) → >10 (skill promotion candidate)
7. **Concurrency** — WAL mode, INSERT-only pattern, busy_timeout retry

(Use the Memory Chain section from the spec as source material — same detailed content.)

- [ ] **Step 2: Commit**

```bash
git add references/memory-chain.md
git commit -m "feat(summoner): add memory chain reference doc — retrieval, write, lifecycle protocols"
```

---

### Task 11: Update Meta-Skill with Phase 0

**Files:**
- Modify: `~/.claude/plugins/summoner/skills/summoner/SKILL.md`

**Description:** Add Phase 0 (Memory Retrieval) to the meta-skill's Core Operating Behaviors section, before Skill Resolution.

- [ ] **Step 1: Add Phase 0 to Core Operating Behaviors**

Insert before "### 1. Skill Resolution" in SKILL.md:

```markdown
### 0. Memory Retrieval (Phase 0)

Before starting any workflow, check Summoner Memory for relevant historical patterns.

1. Read `project.name` from the project's `summoner.yaml`
2. Extract features from user input:
   - error_codes: scan for known patterns (SC_Err*, panic, nil pointer, etc.)
   - module: infer from log file paths, function names, or explicit mentions
   - keywords: tokenize Chinese and English words
3. Query the project's SQLite memory database:
   ```sql
   SELECT name, type, summary, hits, priority
   FROM patterns
   WHERE (error_codes LIKE '%' || ? || '%'
          OR modules LIKE '%' || ? || '%'
          OR keywords LIKE '%' || ? || '%')
     AND priority != 'low'
   ORDER BY CASE priority WHEN 'high' THEN 0 WHEN 'medium' THEN 1 ELSE 2 END,
            hits DESC
   LIMIT 5
   ```
4. Present matched patterns:

```
┌──────────────────────────────────────────────┐
│  📚 Summoner Memory — 匹配到 {N} 条历史经验    │
│                                              │
│  {emoji} {name} (匹配: ★★★★★)                │
│     {summary}                                │
│     [hits: {N}]                              │
│                                              │
│  [enter] 加载经验继续  [no] 忽略              │
└──────────────────────────────────────────────┘
```

5. If user says "no" or patterns don't seem relevant: skip, proceed to Phase 1
6. If user says "enter": loaded patterns inform diagnosis strategy
7. Token budget: ≤1500 tokens total. If no patterns match, Phase 0 takes 0 tokens.

**Init check:** If the project's memory database doesn't exist yet, run `scripts/init-memory-db.sh <project-name>` to create it with seed patterns from AI mistakes.
```

- [ ] **Step 2: Update Workflow Definitions**

Update each workflow to show Phase 0:

```
/summoner:fix:
  Phase 0: Memory Retrieval (automatic)
  Phase 1: debug (MANDATORY) — root cause diagnosis
  ...

/summoner:new:
  Phase 0: Memory Retrieval (automatic)
  Phase 1: define — requirements and design
  ...
```

- [ ] **Step 3: Add Phase 0 to verification checklist**

```markdown
## Verification
After workflow completion:
- [ ] Phase 0 memory retrieval was attempted (or skipped if no db exists)
- [ ] Matched patterns were presented to user (if any)
- [ ] Token budget ≤1500 was respected
- [ ] ...
```

- [ ] **Step 4: Commit**

```bash
git add skills/summoner/SKILL.md
git commit -m "feat(summoner): add Phase 0 memory retrieval to meta-skill"
```

---

### Task 12: Update Post-Game Review with Write Protocol

**Files:**
- Modify: `~/.claude/plugins/summoner/references/post-game-review.md`
- Modify: `~/.claude/plugins/summoner/skills/summoner/SKILL.md` (Post-Game Review section)

**Description:** Add the memory write protocol after the journal entry is created in the Post-Game Review.

- [ ] **Step 1: Add Write Protocol to post-game-review.md**

Append after the "Insights Pipeline" section:

```markdown
## Memory Write Protocol

After journal entry is written, classify and persist the pattern:

### Classification

| Review Type | Pattern Type | When to write |
|------------|-------------|---------------|
| Type 1 (direction correction) | `correction` | Always — this is the most valuable signal |
| Type 2 (phase skip) | `skip` | Only if user provided a skip condition (question 4) |
| Type 3 (knowledge injection) | `knowledge` | Only if user said "AI couldn't figure this out" (question 3b) |
| Type 4 (full completion) | — | Only if user rated <3 and provided specific feedback |
| Type 5 (verbosity complaint) | `style` | Only if user provided a preference rule (question 4) |

### SQL Operations

```sql
-- New pattern
INSERT INTO patterns (name, type, error_codes, modules, keywords, summary, detail, priority)
VALUES (?, ?, ?, ?, ?, ?, ?, 'medium');

-- Existing pattern (same name)
UPDATE patterns SET hits = hits + 1, updated_at = datetime('now') WHERE name = ?;

-- Every 10 updates, recalculate priority
UPDATE patterns SET priority =
  CASE WHEN hits >= 6 THEN 'high'
       WHEN hits >= 3 THEN 'medium'
       ELSE 'low'
  END
WHERE name = ?;
```

### Feature Extraction

From the session context, extract:
- `error_codes`: scan Phase 1 diagnostic output for SC_* codes, "panic", "nil pointer"
- `modules`: from log file paths (e.g. player/task/task.go → task)
- `keywords`: tokenize user's correction/skip reason/knowledge injection text
- `summary`: 1-2 sentences — the core rule. "遇到X时应该先Y再Z"
- `detail`: full context from the review answers
```

- [ ] **Step 2: Update SKILL.md Post-Game Review section**

Append to section "### 4. Post-Game Review" in SKILL.md:

```markdown
After collecting review answers:
5. Write journal entry to SQLite (journal table)
6. Classify pattern per `references/post-game-review.md` write protocol
7. If pattern is worth persisting:
   - Extract features (error_codes, modules, keywords)
   - INSERT or UPDATE patterns table
   - Report: "📝 Memory updated: {pattern_name}"
8. If pattern matches existing: "📝 Memory: {pattern_name} hits now {N}"
```

- [ ] **Step 3: Commit**

```bash
git add references/post-game-review.md skills/summoner/SKILL.md
git commit -m "feat(summoner): add memory write protocol to post-game review"
```

---

### Task 13: Update Commands with Phase 0 Awareness

**Files:**
- Modify: `~/.claude/plugins/summoner/commands/fix.md`
- Modify: `~/.claude/plugins/summoner/commands/new.md`
- Modify: `~/.claude/plugins/summoner/commands/debug.md`

**Description:** Update command workflow definitions to include Phase 0 in their workflow diagrams.

- [ ] **Step 1: Update fix.md workflow**

```
Phase 0 ──→ 记忆检索 (Memory Retrieval, automatic)
Phase 1 ──→ 诊断根因 (phase.debug, MANDATORY)
...
```

- [ ] **Step 2: Update new.md workflow similarly**
- [ ] **Step 3: Update debug.md workflow similarly**
- [ ] **Step 4: Commit**

```bash
git add commands/fix.md commands/new.md commands/debug.md
git commit -m "feat(summoner): add Phase 0 to command workflow definitions"
```

---

### Task 14: Update File Map and Plugin Structure

**Files:**
- Modify: `~/.claude/plugins/summoner/CLAUDE.md` (update Plugin Structure section)

**Description:** Update documentation to reflect the new memory/ directory and SQLite database.

- [ ] **Step 1: Update CLAUDE.md Plugin Structure**

```
scripts/init-memory-db.sh    → SQLite database initialization with seed patterns
memory/_index.json           → Project namespace → db file mapping
memory/{project-name}.db     → Per-project SQLite patterns + journal
journal/                     → (deprecated, replaced by journal table in SQLite)
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "chore(summoner): update plugin structure docs with memory directory"
```

---

### Task 15: my-project Memory Init

**Files:**
- Modify: (none, just run the init script)

**Description:** Initialize the memory database for the my-project project namespace.

- [ ] **Step 1: Run init script**

```bash
~/.claude/plugins/summoner/scripts/init-memory-db.sh my-project
# Expected: ✓ Memory database initialized: .../memory/my-project.db
```

- [ ] **Step 2: Verify seed patterns**

```bash
sqlite3 ~/.claude/plugins/summoner/memory/my-project.db "SELECT name, priority, hits FROM patterns ORDER BY priority DESC, hits DESC;"
# Expected: 5 seed patterns loaded (high priority: ai-risk-003, ai-risk-004, ai-err-005, ai-err-003; medium: ai-err-001)
```

- [ ] **Step 3: Verify WAL mode**

```bash
sqlite3 ~/.claude/plugins/summoner/memory/my-project.db "PRAGMA journal_mode;"
# Expected: wal
```

- [ ] **Step 4: Commit that the task is done (no code changes)**

```bash
# No files to commit — this task runs the init script for the project
echo "my-project memory initialized" >> /dev/null
```

---

## Plan Self-Review

**1. Spec Coverage:**
- ✅ Plugin structure (Task 1)
- ✅ summoner.yaml manifest spec (Task 2, Step 1)
- ✅ Checkpoint protocol (Task 2, Step 2)
- ✅ Post-game review 5 types (Task 2, Step 3)
- ✅ Persona composition rules (Task 2, Step 4)
- ✅ Meta-skill routing hub (Task 3)
- ✅ 3 personas (Task 4)
- ✅ 6 commands (Task 5)
- ✅ Validation script (Task 6)
- ✅ my-project integration (Task 7)
- ✅ CLAUDE.md compatibility (handled in Task 1 — CLAUDE.md mentions project integration is project's responsibility)
- ⚠️ Missing: `journal/` and `insights/` directory creation. These are runtime artifacts — the `mkdir -p` in Task 1 Step 4 covers this.

**2. Placeholder Scan:**
- ✅ No TBD, TODO, or "implement later"
- ✅ All code blocks have actual content
- ✅ All file paths are exact
- ⚠️ New tasks 9-15 add Memory Chain — seed patterns from ai-mistakes.md should be reviewed periodically

**3. Type Consistency:**
- ✅ Phase names match between manifest spec (Task 2), meta-skill (Task 3), and commands (Task 5)
- ✅ Workflow chain phase names (`debug`, `reproduce`, `fix`, `verify`, `review`) consistent across all references
- ✅ Persona names (`code-reviewer`, `security-auditor`, `test-engineer`) consistent in plugin.json, agent files, and ship.md

**Missing from spec but added in plan:**
- ✅ Auto-skip conditions table in meta-skill SKILL.md
- ✅ Interrupt signal ambiguity resolution in checkpoint-protocol.md
- ✅ Journal entry format in post-game-review.md
