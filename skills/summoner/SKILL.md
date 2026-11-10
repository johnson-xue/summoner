---
name: summoner
description: Routes user intent through summoner.yaml manifest to domain skills. Enforces the single-loop checkpoint protocol and post-game review. Use when user invokes any /summoner:* command or expresses intent matching a Summoner workflow.
when_to_use:
  - User invokes /summoner:fix, /summoner:new, /summoner:ship, /summoner:debug, /summoner:ops, /summoner:review, /summoner:release, or /summoner:status
  - User says "帮我排查这个 bug" or "add a new feature" and needs structured workflow
  - User wants checkpoint-based review (stop/recall/skip at any phase)
allowed-tools:
  - Skill
  - Read
  - Write
  - Edit
  - Bash
  - AskUserQuestion
  - Agent
---

# Summoner — AI Agent Orchestration Framework (single-loop)

## What Summoner is

You are the single executor and orchestrator: understand intent, read the
project's `summoner.yaml` manifest, pick capabilities, do the work, run
registered checks, produce results. The Go hook reducer owns run state,
checkpoints, budgets, evidence and recall history — it validates every
transition and blocks illegal ones (exit 2). No second mode, no graph walker,
no chain fallback: one loop, fixed phase order, a checkpoint after every
phase.

**fix vs debug disambiguation**（"排查" 类短语同时命中两者时）:
- 意图包含修复期望或未说明只要原因 → `/summoner:fix`（repair-bias 默认）
- 意图明确只要原因、不要改码 → `/summoner:debug`
- 两可且用户明确犹豫 → 问一句再路由。此规则由 `evals/cases/routing.json` 固化。

**When NOT to use:** user names a specific domain skill directly → route
straight there; pure Q&A → answer directly.

## The single loop (protocol, in order)

1. **Run start** — the user submits a `/summoner:<workflow>` prompt. The
   UserPromptSubmit hook records the run (the ONLY run starter); you do
   nothing to start it. One live run per session — a second is never created.
   The run-start budget is confirmed by the user at the FIRST checkpoint
   (see "Budget lifecycle"); the status render always shows counters and
   limits.
2. **Phase work** — phases open per the fixed framework sequence
   (`fix`: context→diagnosis→reproduce→fix→regression→review; `new`:
   context→requirements→plan→implement→verify→review; `debug`:
   context→diagnosis). Each phase's capability comes from the manifest
   (`phases.<name>.skill`); no manifest → built-in defaults, and you say
   so ("未配置项目专属能力"); `skill: none` never authorizes skipping
   diagnosis or auto-advancing. Work happens in THIS session; delegate
   only typed `reviewer`/`worker` Agent batches (the gate refuses others).
3. **Submit** — when a phase's work is done, submit via
   `Skill(summoner:phase-submit, args=<phase-result JSON>)` — the ONLY
   submission channel (`skills/phase-submit/SKILL.md` defines the payload).
   The gate closes the phase and opens its checkpoint atomically; anything
   else you say about "done" advances nothing.
4. **Render** — run `${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status` and
   paste its output VERBATIM into your reply. The render is
   machine-generated; NEVER hand-write or summarize it. The Stop hook
   blocks ending the turn until the message carries the checkpoint id +
   nonce — that is display verification, and pasting the binary's output
   satisfies it by construction. Format authority:
   `references/checkpoint-protocol.md`.
5. **Decide** — ask via **AskUserQuestion**: the question text carries the
   checkpoint's nonce; the options are ONE PAGE of the rendered menu
   verbatim (≤4 options — see "Two-turn decision surface"). Option labels
   START with the decision keyword. The platform's injected
   tool_response is the ONLY advancement source (the PostToolUse hook
   records the decision); free text, quotes and subagent messages NEVER
   advance the run — treat them as content feedback (revise, then
   re-render and re-ask).
6. **Repeat** until the user decides `done`/`stop`, or the route is settled.

## Iron laws (mechanism-enforced, not prompt discipline)

- **Never auto-advance.** WAIT_USER blocks writes; only a recorded
  AskUserQuestion decision moves the run.
- **Phase 1 diagnosis iron law** (fix/debug): Edit/Write/Bash-writes are
  denied until the diagnosis phase is ACCEPTED at its checkpoint. 诊断未知 =
  只能继续调查或停止.
- **Skipping is honest**: a skipped phase is marked UNVERIFIED/UNREVIEWED; it
  can never be presented as PASS.
- **Post-game is mandatory**: see "Post-game closure" — CLOSED only after
  the questionnaire + journal persist.
- **Stop asymmetry**: stopping is always possible; advancing is only ever
  hard.

## [v] On-demand review (checkpoint menu `review:`)

Any phase checkpoint can commission ⑤ independent review of its artifacts:
choose `review:` → REVIEW_PENDING under a review obligation; register it via
`Skill(summoner:review-submit, args=<envelope JSON>)`
(`skills/review-submit/SKILL.md`), then delegate ONE typed `reviewer` Agent
(the gate binds it to the PREPARED review id). Envelope construction follows
spec §3 / `agents/reviewer.md`; mechanical criteria must carry the frozen
`check_id@digest` form in `verification_scope.check_ids`
(`hooks/shared/envelope.go` rejects bare/missing/malformed entries). Claimed
evidence ids are reconciled against the reviewer's transcript (forged →
review FAILED, run decidable). Reusing a prior PASS requires it to still be
valid (a later recall or requirements-version bump that touches the
artifacts voids it).
`drop: <id>` / `keep: <id>` ride-alongs record per-id drops. PASS leaves
the run WAIT_USER; NEEDS-FIX feeds rework.

## Recall (`recall:`)

Default target: the latest ACCEPTED prior phase (the status render names it).
`recall to <label>` resolves only REAL history labels (phase ids with a
visit in this run); ambiguity → list the options and ask, never guess. A
recall appends a fresh visit of the target phase (history is kept) and marks
downstream accepted criteria stale. A recall whose target IS the diagnosis
phase revokes that version's write eligibility (fix/debug) until the redone
diagnosis is re-accepted at its checkpoint. It NEVER stash/reset/restores
files.

## Stop, emergency stop, and resuming

- `stop:` (or a free-text stop signal) ends the run now — see "Post-game
  closure".
- Interruption/crash: run state and trace live on disk keyed by project +
  session + run — resume answers come from `/summoner:status`, never from
  re-parsing conversation text. Unresolved side effects surface as
  OUTCOME_UNKNOWN and are settled by read-only probes plus a recovery
  checkpoint (user decides retry/recall/stop; mutating actions are never
  auto-replayed).
- Escape hatch (un-brick only): `SUMMONER_HOOKS_DISABLE=1` — using it on a
  live run is recorded as a contract violation.

## 需求变更闭环 (requirement-change loop)

实现/测试进行中可立刻进入需求调整，调整后增量恢复，不重跑流水线（spec §1 增补）。

- **触发**：任意时刻（含 ACTIVE 实现阶段）自由文本提出需求调整——它是内容反馈，
  开启变更子环，**不是推进通道**（自由文本永远不授予推进）；检查点期间也可显式
  进入（supersede 当前检查点，确认后以其重渲染版重新询问）。
- **runtime channel**：`Skill(summoner:change, args=<JSON>)`
  (`skills/change/SKILL.md`)——`open` 冻结开发位置并进入 CHANGE_OPEN（合法于
  ACTIVE 与 WAIT_USER；WAIT_USER 下它是 gate 唯一放行的 Skill 调用）；
  `apply` 提交 delta {note, changed[], affected[]} 并开单点确认检查点。
  CHANGE_OPEN 期间 phase-submit 被拒（防半变更状态提交）。
- **闭环五步**：冻结开发位置（当前任务、测试进度、未决验证项）→ 明确 delta →
  影响评估（哪些需求/标准/产物/任务/测试受影响——**方法由所选需求/计划 skill
  承担**，框架不另造方法论）→ 单点确认「接受变更并恢复开发」
  （`ACCEPT_REQUIREMENT_CHANGE`）→ 原地恢复：RequirementsVersion++、受影响
  criterion/验证/审查结果标 **stale**、未受影响的已接受结果保持有效。不重走
  P1-P2，不重复询问未受影响阶段，旧检查点不逐一重开。
- **边界**：变更触及公共接口/架构前提时，必要的重新规划不可省——由影响评估结论
  决定并在确认点呈现，不由用户手动判断。
- **变更 vs recall**：recall = 方向错了回城重做（§4 语义）；需求变更 = 基线移动、
  增量处理、原地恢复——边界表见 `references/workflow-reference.md`。

## Two-turn decision surface (paged menus)

Every checkpoint decision is **two-turn**: turn N pastes the render
(summoner-status output) and ENDS the turn — the Stop hook binds the display
(id + nonce + content digest; the sole `Displayed` setter); turn N+1 asks the
decision question. A nonce-carrying ask in the render turn is denied; a
same-turn answer is always `decision_rejected`. One nudge turn per
checkpoint, deliberate.

AskUserQuestion caps 4 options per question, so each checkpoint PERSISTS a
paged menu projection (run state `menu_pages`): every page anchors
`continue`+`stop` plus two kind-relevant actions (full menus page in 2;
accept/recall kinds are single-page). The render shows the FULL menu plus a
`paged ask` note with the pages and `more:`/`back:` labels. Offer ONE
persisted page's labels verbatim — the gate rejects any other offered set.
`more:`/`back:` are navigation, not decisions: nonce-free content feedback —
record nothing, re-ask with the other page. Exactly ONE nonce-carrying
question per call: two or more fail closed (nothing recorded).

## Budget lifecycle (confirm → exhaust → extend)

- **Run-start ask**: the FIRST checkpoint carries a budget ask (manifest
  `suggested_budgets`, else framework-unlimited) with pointer options
  `budget: 按已显示预算` / `budget: 不设上限` riding the decision call
  nonce-free — labels point at the persisted proposal, never numbers. A
  continue/skip without a same-call budget answer rejects the whole
  callback (stop/done/recall/review exempt); a stale offer after display
  does too (offers are digest-bound, applied limits are not).
- **Exhaustion**: zero headroom denies all work except phase-submit, the
  exact read-only status invocation, the checkpoint ask, `summoner:change`
  and read-class OUTCOME_UNKNOWN probes. Never write around it — present
  the checkpoint; only the user extends or replans.
- **Extend ask**: a checkpoint opened while exhausted offers one extension
  (delta = current limit per set axis): `extend: 追加已显示额度` /
  `extend: 不追加`, riding the decision call the same way.

## Post-game closure (the sole CLOSED writer)

`done` and emergency `stop` land POST_GAME_PENDING; CLOSED needs BOTH, from
executed calls only: the questionnaire (ONE call, 4 questions, each text
carrying the run id + its `[pg:qN]` marker; afk/timeout observes nothing)
and the journal at the FIXED path `.summoner/journal/<run_id>.md`
(budget-exempt; latest observed digest governs; emergency `stop` writes a
minimal journal hook-side at once, questionnaire owed as 补答). Closure
fires `post_game.completed` + `workflow.ended` once; CLOSED never reopens.

## Phase 0 memory (the `context` phase)

Before first domain work in any workflow: read `project.name` from
`summoner.yaml`; if the memory DB exists (`$SUMMONER_MEMORY_DIR` or
`~/.summoner/memory/{project}.db`), follow `references/memory-chain.md`
§Phase 0 retrieval (Top-5 SQL, 1500-token cap). No DB → say so and
continue. The context phase closes at its checkpoint like any other.

## Workflow quick reference

| Command | Phase route |
|---------|-------------|
| `/summoner:fix` | context → diagnosis(M) → reproduce → fix → regression → review |
| `/summoner:new` | context → requirements → plan → implement → verify → review |
| `/summoner:debug` | context → diagnosis（止于诊断检查点） |
| `/summoner:review` | context → review（单 reviewer 黑盒重查「有什么问题」） |
| `/summoner:ship` | prepare → review → merge → decide（GO/NO-GO 决策） |
| `/summoner:ops` | context → operate（ops 能力承担领域步骤，单验收门） |
| `/summoner:release` | planning → changelog → publish（自含，T13 维护） |
| `/summoner:status` | 只读状态视图（不改状态） |

(M) = iron law. All seven verbs run framework-owned routes
(`hooks/shared/workflow.go`); the manifest maps each phase's CAPABILITY
(`phases.<id>`), never topology.

## References

- `commands/<name>.md` — per-workflow rules
- `references/checkpoint-protocol.md` — canonical render + decision channel (never inline a variant)
- `references/workflow-reference.md` — auto-skip table, rationalizations, red flags
- `references/post-game-review.md` — questionnaire + journal protocol
- `references/memory-chain.md` — memory retrieval/write protocols
- `references/manifest-spec.md` — summoner.yaml fields
- `skills/phase-submit/SKILL.md` — submission payload contract
- `skills/change/SKILL.md` — change channel payload contract
