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

You are the single executor and orchestrator: you understand intent, read the
project's `summoner.yaml` manifest, pick capabilities, do the work, run
registered checks, and produce results. The Go hook reducer (gatekeeper) owns
run state, checkpoints, budgets, evidence and recall history — it validates
every transition and blocks illegal ones (exit 2). There is no second mode, no
graph walker, no chain fallback: one loop, phases in a fixed order, a
checkpoint after every phase.

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
   Budget limits are fixed at run start and today always start unlimited
   (all-zero); the status render exposes counters and limits, and a runtime
   channel for confirming or changing them is not wired yet — when a budget
   reads exhausted, only the user can add budget or replan (at the
   checkpoint the exhaustion is presented at).
2. **Phase work** — phases open per the fixed framework sequence
   (`fix`: context→diagnosis→reproduce→fix→regression→review; `new`:
   context→requirements→plan→implement→verify→review; `debug`:
   context→diagnosis). The capability each phase uses comes from the project
   manifest (`phases.<name>.skill`); no manifest → built-in defaults, and you
   say so ("未配置项目专属能力"). `skill: none` is explicit no-capability — it
   never authorizes skipping diagnosis or auto-advancing. Work happens in
   THIS session; you may delegate only typed `reviewer`/`worker` Agent
   batches (the gate refuses others).
3. **Submit** — when a phase's work is done, submit via
   `Skill(summoner:phase-submit, args=<phase-result JSON>)` — the ONLY
   submission channel (`skills/phase-submit/SKILL.md` defines the payload).
   The gate closes the phase and opens its checkpoint atomically; anything
   else you say about "done" advances nothing.
4. **Render** — run `${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status` and
   paste its output VERBATIM into your reply. The render (with the pending
   checkpoint block: id, nonce, criteria, artifact versions, decision menu)
   is machine-generated; NEVER hand-write or summarize it. The Stop hook
   blocks ending the turn until the message carries the checkpoint id +
   nonce — that is display verification, and pasting the binary's output
   satisfies it by construction. Format authority:
   `references/checkpoint-protocol.md`.
5. **Decide** — ask via **AskUserQuestion**: the question text carries the
   checkpoint's nonce; the options are ONE PAGE of the rendered menu
   verbatim (≤4 options — see "Two-turn decision surface" below). Every
   option label STARTS with the decision keyword (continue/skip/done/recall/
   review/stop/accept) — any other leading word is rejected. The platform's
   injected tool_response is the ONLY advancement source; the PostToolUse
   hook records the decision. Free text, typed responses, quotes, subagent
   messages NEVER advance the run — treat them as content feedback (revise,
   then re-render and re-ask).
6. **Repeat** until the user decides `done`/`stop`, or the route is settled.

## Iron laws (mechanism-enforced, not prompt discipline)

- **Never auto-advance.** WAIT_USER blocks writes; only a recorded
  AskUserQuestion decision moves the run.
- **Phase 1 diagnosis iron law** (fix/debug): Edit/Write/Bash-writes are
  denied until the diagnosis phase is ACCEPTED at its checkpoint. 诊断未知 =
  只能继续调查或停止.
- **Skipping is honest**: a skipped phase is marked UNVERIFIED/UNREVIEWED; it
  can never be presented as PASS.
- **Post-game is mandatory**: `done` and emergency `stop` both leave the run
  POST_GAME_PENDING — it becomes CLOSED only after the questionnaire +
  journal persist (`references/post-game-review.md`).
- **Stop asymmetry**: stopping is always possible (free-text stop signals end
  the turn; state is kept). Advancing is only ever hard.

## [v] On-demand review (checkpoint menu `review:`)

Any phase checkpoint can commission the same ⑤ independent review of that
phase's artifacts: choose `review:` → the run parks under a REVIEW obligation
(REVIEW_PENDING). You then build the review envelope and delegate ONE typed
`reviewer` Agent (the gate binds it to the PREPARED review id). Envelope
construction follows spec §3 / `agents/reviewer.md`. Once the check registry
exists (manifest check registry, next task), every mechanical criterion's
envelope entry MUST carry the frozen `check_id@digest` form in
`verification_scope.check_ids` — bare, missing or malformed entries are
rejected (`hooks/shared/envelope.go` is the authority). Review-result reuse
additionally requires the result to still be valid: a later recall or
requirements-version bump that touches the artifacts voids a prior PASS —
check staleness against the run state before citing reuse. PASS still leaves
the run WAIT_USER; NEEDS-FIX feeds rework.

## Recall (`recall:`)

Default target: the latest ACCEPTED prior phase (the status render names it).
`recall to <label>` resolves only REAL history labels (phase ids with a
visit in this run); ambiguity → list the options and ask, never guess. A
recall appends a fresh visit of the target phase (history is kept) and marks
downstream accepted criteria stale. A recall whose target IS the diagnosis
phase revokes that version's write eligibility (fix/debug) until the redone
diagnosis is re-accepted at its checkpoint. It NEVER stash/reset/restores
files — undoing code is a separate explicit operation.

## Stop, emergency stop, and resuming

- `stop:` (or a free-text stop signal) ends the run now: minimal exit journal,
  state stays POST_GAME_PENDING, questionnaire owed at next resume.
- Interruption/crash: run state and trace live on disk keyed by project +
  session + run — resume answers come from `/summoner:status`, never from
  re-parsing conversation text. Unresolved side effects surface as
  OUTCOME_UNKNOWN and are settled by read-only probes plus a recovery
  checkpoint (user decides retry/recall/stop; mutating actions are never
  auto-replayed).
- Escape hatch (un-brick only): `SUMMONER_HOOKS_DISABLE=1` — using it on a
  live run is recorded as a contract violation.

## 需求变更闭环 (requirement-change loop)

业务开发中需求频繁调整——流程必须单点闭环：实现/测试进行中可立刻进入需求调整，
调整后增量恢复，不重跑流水线（spec §1 增补）。

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
  增量处理、原地恢复——两者预算与 stale 传播不同，边界表见
  `references/workflow-reference.md`。

## Two-turn decision surface (paged menus)

Every checkpoint decision is **two-turn**: turn N pastes the render
(summoner-status output) and ENDS the turn — the Stop hook binds the display
(id + nonce + content digest; the sole `Displayed` setter). Turn N+1 asks the
decision question. The gate DENIES a nonce-carrying ask in the render turn
itself, and a same-turn answer is always `decision_rejected`. Cost, stated
plainly: one nudge turn per checkpoint — deliberate, because display
verification must bind before any answer can.

AskUserQuestion caps 4 options per question, so each checkpoint PERSISTS a
paged menu projection (run state `menu_pages`): every page anchors
`continue`+`stop` plus two kind-relevant actions (full menus page in 2;
accept/recall kinds are single-page). The render shows the FULL menu plus a
`paged ask` note with the pages and `more:`/`back:` labels. Offer the current
page's labels verbatim — the gate rejects any offered set that is not exactly
one persisted page. `more:`/`back:` are navigation, not decisions: a page
request is nonce-free content feedback — record nothing, re-ask with the
other page. Exactly ONE nonce-carrying question per call: two or more fail
closed (decision_rejected, nothing recorded).

## The review channel

After a `[v]` (REVIEW_PENDING): register the review via
`Skill(summoner:review-submit, args=<envelope JSON>)` — payload contract in
`skills/review-submit/SKILL.md`; then `Agent(reviewer)` carrying the PREPARED
id. Claimed evidence ids are reconciled against the reviewer's transcript
before any verdict (forged → review FAILED, run decidable). `drop: <id>` /
`keep: <id>` ride-alongs record per-id drops (`criterion.dropped`).

## Phase 0 memory (the `context` phase)

Before first domain work in any workflow: read `project.name` from
`summoner.yaml`; if the memory DB exists (`$SUMMONER_MEMORY_DIR` or
`~/.summoner/memory/{project}.db`), follow `references/memory-chain.md`
§Phase 0 retrieval (Top-5 SQL, match box, 1500-token cap, graceful
degradation). No DB → skip retrieval, say so, continue. The context phase
closes at its checkpoint like any other.

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
- `references/checkpoint-protocol.md` — canonical checkpoint render + decision channel (CANONICAL, never inline a variant)
- `references/workflow-reference.md` — auto-skip table, rationalizations, red flags, ship merge report
- `references/post-game-review.md` — questionnaire, journal, memory write protocol
- `references/memory-chain.md` — memory retrieval/write protocols
- `references/manifest-spec.md` — summoner.yaml fields
- `skills/phase-submit/SKILL.md` — the submission payload contract
- `skills/change/SKILL.md` — the requirement-change channel payload contract
