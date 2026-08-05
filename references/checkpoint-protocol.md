# Summoner Checkpoint Protocol

## Purpose

Checkpoints are the core interruption mechanism in Summoner. Each phase has a **START block** (entering the phase) and a **CHECKPOINT block** (phase end). The START block gives the user continuous context ("which phase, doing what task"). The CHECKPOINT block reports results and asks how to proceed. The user chooses how to advance.

## PHASE START Block (entering each phase)

Every phase start MUST output this lightweight 3-line block (plain text, no ASCII frame — keep it brief so it doesn't compete with the phase's own output):

```
⚡ SUMMONER START — Workflow={workflow名} Phase {N}/{Total}: {phase名}
🎯 任务: {one sentence — what this phase will do}
🔧 Skill: {skill name | freeform | none (无专属 skill)}
```

Field rules:
- **Workflow**: the workflow name (`fix` / `new` / `ship` / `debug` / `ops` / `review`).
- **Phase {N}/{Total}**: N and Total are mandatory, never omit. phase名 from manifest.
- **任务**: ≤60 chars, one sentence, the task this phase performs (derive from manifest phase comment or user input). Not a restatement of the phase name.
- **Skill**: the skill this phase invokes. `freeform` for free-form phases (e.g. fix Phase 3). `none (无专属 skill)` when manifest declares `skill: none`.

## CHECKPOINT Block (phase end)

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

### Field Specification (mandatory — no free-form deviation)

| Field | Type | Rule |
|------|------|------|
| `Phase {N}/{Total}: {phase名}` | header | Mandatory. N/Total never omitted. phase名 from manifest. |
| `完成内容` | 1 paragraph | ≤80 chars, declarative. **What** was accomplished (not how). |
| `产物` | comma-separated list | Concrete paths / decisions / test results. Never empty — if nothing produced, write `No artifacts — analysis only.` |
| `发现` | list | Risks / open questions. `None` if clean. |
| `Next:` 5 options | fixed | `[enter]/[skip]/[done]/[recall]/[stop]` — order fixed, never reordered, never dropped. |

**Iron law — fields cannot be omitted:** any field with no content must show an explicit placeholder (`None` / `No artifacts — analysis only.`). Never delete the field row. This guarantees the block is always structurally identical — the user (and AI) can grep/parse it reliably.

### Standard Example (fix workflow, Phase 1 diagnose complete)

```
⚡ SUMMONER START — Workflow=fix Phase 1/5: diagnose
🎯 任务: 定位 player/task 模块的空指针根因
🔧 Skill: antia-debug

[phase execution output...]

┌──────────────────────────────────────────────┐
│  ⚡ SUMMONER — Phase 1/5: diagnose            │
│                                              │
│  ✅ 完成内容: 定位到 TaskModule.handle() 未判空  │
│     player.SubTask 字段，player 离线时触发 NPE。 │
│  📋 产物: docs/reviews/2026-07-07-task-npe.md │
│     (根因报告), player/task/task.go:142       │
│  ⚠️ 发现: handle() 还存在 3 处类似未判空，建议   │
│     Phase 3 一并修。                           │
│                                              │
│  Next:                                       │
│  [enter] 继续下一 Phase                       │
│  [skip]  跳过下一 Phase                       │
│  [done]  完成，退出框架                       │
│  [recall] B 键回城 — 回到之前 Phase 重新来过   │
│  [stop]  紧急停止 — 保留产物，立即退出          │
└──────────────────────────────────────────────┘
```

### Anti-examples (common deviations — do NOT output these)

**反例 1 — 字段省略（完成内容为空就删行）:**
```
│  ✅ 完成内容:                                  │   ← ✗ 空 content 也不能删字段
│  📋 产物: task.go:142                         │
```
错在哪：删了字段行，块结构变形，用户无法靠固定行位 grep。正确：写 `完成内容: No artifacts — analysis only.` 或补一句陈述。

**反例 2 — 完成内容写成流水账（how 而非 what）:**
```
│  ✅ 完成内容: 我先 grep 了 task 模块，然后读了   │
│     handle 函数，发现第 142 行没判空，又查了     │
│     player 离线逻辑，确认是 NPE...              │
```
错在哪：写成执行流水账（how），超 80 字，掩盖了「定位到什么」（what）。正确：`完成内容: 定位到 handle() 未判空 player.SubTask，离线触发 NPE。`

**反例 3 — 选项顺序乱/漏:**
```
│  Next:                                       │
│  [skip]  跳过                                 │
│  [done]  完成                                 │
│  [enter] 继续                                 │   ← ✗ 顺序乱
```
错在哪：选项顺序不是 `enter/skip/done/recall/stop`，且漏了 recall/stop。用户肌肉记忆失效。正确：固定 5 选项 + 固定顺序。

## Interrupt Signal Grammar

The framework scans EVERY user reply after the CHECKPOINT block. Matching is case-insensitive and whitespace-tolerant.

| Signal | Keywords | Action |
|--------|----------|--------|
| CONTINUE | enter, 继续, next, proceed, yes, ok, go, 好, 收到 | Advance to next phase |
| SKIP | skip, 跳过, 不用, 不需要, skip this | Skip the NEXT phase (not the current one) |
| DONE | done, 够了, 可以了, 完成, finish, good | Mark workflow complete, trigger post-game review, exit |
| RECALL | recall, 回城, 方向不对, 换个思路, go back, redo | Return to previous phase, discard current phase output. In graph mode: `recall to <node-label>` where `<node-label>` is the human-facing verb (not the id) — the WALKER parses the target (the LLM no longer improvises it). Reason codes: `receiver_rejected_handoff \| direction_wrong \| verifier_failed`. |
| STOP | stop, 停, 我自己来, 退出, quit, abort | Exit framework immediately, preserve all artifacts, NO post-game review |
| VERBOSE | 别废话, 简洁点, 太啰嗦, too verbose, be brief, tldr | Record Type 5 complaint, condense current and future output |

### Content Feedback Recognition (do NOT misread as CONTINUE)

User replies after a checkpoint are not always workflow decisions — many are **content feedback** about the phase's output (the solution is wrong, misses a case, wrong direction). Misreading these as CONTINUE ignores the user's question.

**Rule (keyword list + semantic judgment, combined):**

1. **Keyword list (deterministic):** if the reply contains content-feedback markers — `方案/方向/漏了/不对/应该/边界/缺/错了/有问题/不对劲/重做/换一个` (CN) or `wrong/misses/should/wrong direction/redo/alternative` (EN) — treat as **content feedback**, NOT CONTINUE. Handle the feedback first, then re-output the CHECKPOINT block to re-ask the flow decision.

2. **Semantic judgment (fallback):** if no signal keyword matches AND the reply is NOT a pure confirmation word (`好/继续/ok/收到/enter/yes`), treat it as content feedback. Handle it, then re-output the CHECKPOINT block.

3. **Pure confirmation only → CONTINUE:** only replies that are clearly bare confirmations (no substantive content) advance. When in doubt, do NOT auto-advance — re-ask.

**Ambiguity Resolution (unchanged, safety-first):**

If user input matches multiple signals:
- STOP > RECALL > DONE > VERBOSE > SKIP > CONTINUE (safety-first)
- "stop 方向不对" → STOP wins (highest priority)
- "skip 我自己来" → STOP wins (STOP > SKIP)
- "别废话，继续" → VERBOSE wins (VERBOSE > CONTINUE)

### Content Feedback Examples

**反例 A — 实质反馈被当 CONTINUE（旧规则错）:**
- 用户回复: `"这个方案漏了 player 离线的边界 case"`
- 旧规则: 无 signal 关键词命中 → CONTINUE → 忽略反馈，进入下一 phase ✗
- 新规则: 命中内容反馈关键词「漏了」→ 先处理反馈（补边界 case），再重新输出 CHECKPOINT 问流程决策 ✓

**反例 B — 方向性反馈（命中 RECALL 也补确认）:**
- 用户回复: `"方向不对，应该用缓存方案"`
- 新规则: 「方向不对」命中 RECALL 关键词 → RECALL。但补一句确认: "确认回城到上一 Phase 重新来过？当前产物会丢弃。"（避免误判 recall 浪费产物）

**正例 — 纯确认才 CONTINUE:**
- 用户回复: `"好，继续"` → 纯确认词 → CONTINUE ✓
- 用户回复: `"enter"` → CONTINUE ✓

### Graph-Mode Checkpoint Rendering (M9)

In graph mode, SKILL.md renders `summoner-walker explain` into the CHECKPOINT block (the walker precomputes the human-facing view; the LLM does not assemble it). The render shows:

- **Node `label`** (never the id) — the human-facing verb the author wrote in the graph (e.g. "定位根因", not "diagnose").
- **Internal step names ①②③④⑤ HIDDEN** — the closed-loop ① Ingest+Validate → ⓪ → ② Work → ③ Test → ④ Handoff → ⑤ Review are machine internals; the human sees the node label and its outcome, not the step jargon.
- **A "you are here" route map** — each node shown as one of: ✓ (pass), ✗ (needs-fix), ▶ (current), ⊘ (skipped). This is the `route_map` the walker carries in walk-state (§10.2).
- **The ⑤ evidence** — grep hits, `file:line` citations the review-agent produced, as proof the verdict is grounded (a verdict with empty `evidence_tool_calls` is a fail, not a checkpoint).
- **A walker-precomputed default recall option** — when the verdict is NEEDS-FIX, the walker already knows the target node; the checkpoint surfaces "recall to <node-label>" rather than asking the human to name it.

`summoner-walker status` stays for debug/scorers (raw machine state: graph_turns, tokens_used, back_edges, findings_seen) — it is NOT shown in the human checkpoint. The human-facing surface is `explain` only.

## Recovery

If the conversation is interrupted by timeout or disconnection:

1. On reconnect, framework reads the LAST checkpoint block in the conversation
2. Framework restores: current phase number, total phases, completed artifacts
3. Framework outputs: "⚡ SUMMONER RECONNECT — Resuming from Phase {N}/{Total}: {phase名}. Previous artifacts preserved."
4. User can continue, recall, or stop from there.

Artifact preservation: all files written to disk survive. In-memory state (current analysis results) must be reconstructable from the checkpoint block text.
