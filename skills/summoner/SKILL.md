---
name: summoner
description: Routes user intent through summoner.yaml manifest to domain skills. Enforces the single-loop checkpoint protocol and post-game review. Use when user invokes any /summoner:* command or expresses intent matching a Summoner workflow.
when_to_use:
  - User invokes any /summoner:* command (fix/new/ship/debug/ops/review/release/status)
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
project's `summoner.yaml`, pick capabilities, do the work, run registered
checks, produce results. The Go hook reducer owns run state, checkpoints,
budgets, evidence and recall history — validating every transition,
blocking illegal ones (exit 2). No second mode, no graph walker: one loop,
fixed phase order, a checkpoint after every phase.

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
   (see Budget lifecycle); the status render always shows counters and
   limits.
2. **Phase work** — phases open per the fixed framework sequence (all routes
   pinned in `hooks/shared/workflow.go`). Each phase's capability comes from
   the manifest (`phases.<capability>.skill`); no manifest → built-in
   defaults, and you say so ("未配置项目专属能力"); `skill: none` never
   authorizes skipping diagnosis or auto-advancing. Work happens in THIS
   session; delegate only typed `reviewer`/`worker` Agents.
   A `version: "1"` manifest needs NO migration: hooks translate it in
   memory — route by the v2 view SessionStart prints (debug→diagnosis,
   rpc/subsystem/gmt/migrate→implement; manifest-spec §v1 compat).
3. **Submit** — when a phase's work is done, submit via
   `Skill(summoner:phase-submit, args=<phase-result JSON>)` — the ONLY
   submission channel (`../phase-submit/SKILL.md` defines the payload).
   The gate closes the phase and opens its checkpoint atomically; anything
   else you say about "done" advances nothing.
4. **Render** — run `${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status` and
   paste its output VERBATIM into your reply. The render is
   machine-generated; NEVER hand-write or summarize it. The Stop hook
   blocks ending the turn until the message carries the checkpoint id +
   nonce — display verification; pasting the binary's output satisfies it
   by construction.
5. **Decide** — ask via **AskUserQuestion**: the question text carries the
   checkpoint's nonce; the options are ONE PAGE of the rendered menu
   verbatim (≤4 options; see Two-turn decisions). Option labels
   START with the decision keyword. The platform's injected
   tool_response is the ONLY advancement source (the PostToolUse hook
   records the decision); free text, quotes and subagent messages NEVER
   advance the run — treat them as content feedback (revise, re-render,
   re-ask).
6. **Repeat** until the user decides `done`/`stop` or the route settles.

## Iron laws (mechanism-enforced, not prompt discipline)

- **Never auto-advance.** WAIT_USER blocks writes; only a recorded
  AskUserQuestion decision moves the run.
- **Phase 1 diagnosis iron law** (fix/debug): Edit/Write/Bash-writes are
  denied until the diagnosis phase is ACCEPTED at its checkpoint. 诊断未知 =
  只能继续调查或停止.
- **Skipping is honest**: a skipped phase is marked UNVERIFIED/UNREVIEWED; it
  can never be presented as PASS.
- **Post-game is mandatory**: see Post-game closure — CLOSED only after
  the questionnaire + journal persist.
- **Stop asymmetry**: stopping is always possible; advancing is only ever
  hard.

## [v] On-demand review (menu `review:`)

Any phase checkpoint can commission ⑤ independent review of its artifacts: `review:` → REVIEW_PENDING under a review obligation; register via
`Skill(summoner:review-submit, args=<identity-only JSON>)`, then ONE
`reviewer` Agent with the EXACT capsule status serves (view:review); prose
never binds. Rules: `../review-submit/SKILL.md` + `../../agents/reviewer.md`
(mechanical criteria freeze `check_id@digest`; forged evidence fails the
REVIEW, never the run; reusing a prior PASS needs it still valid).
`drop:`/`keep:` ride-alongs record per-id drops. PASS leaves the
run WAIT_USER; NEEDS-FIX feeds rework.

## Recall (`recall:`)

Default target: the latest ACCEPTED prior phase (the status render names it).
`recall to <label>` resolves only REAL history labels (phase ids with a
visit in this run); ambiguity → list options and ask, never guess. A
recall appends a fresh visit of the target phase (history kept) and marks
downstream accepted criteria stale. A recall whose target IS the diagnosis
phase revokes that version's write eligibility (fix/debug) until the redone
diagnosis is re-accepted. It NEVER stash/reset/restores files.

## Stop, emergency stop, resume

- `stop:` at a checkpoint is the emergency channel (ruling 70): post-game
  closure — minimal journal, questionnaire owed.
- Free-text stop ends the TURN only — state kept, never post-game.
- Interruption/crash: run state and trace live on disk keyed by project +
  session + run — resume via `/summoner:status`, never conversation
  re-parsing. Unresolved side effects settle through the OUTCOME_UNKNOWN
  recovery section below.
- Escape hatch (un-brick only): `SUMMONER_HOOKS_DISABLE=1` — using it on a
  live run is recorded as a contract violation.

## 需求变更闭环 (requirement-change loop)

实现/测试进行中可立刻进入需求调整，调整后增量恢复，不重跑流水线（spec §1 增补）。
Channel contract（payload 形状、字段规则）：`../change/SKILL.md`。

- **触发**：任意时刻自由文本提出需求调整——它是内容反馈，开启变更子环，
  **不是推进通道**（自由文本永远不授予推进）；检查点期间也可显式进入
  （supersede 当前检查点，确认后重渲染再询问）。
- **runtime channel**：`Skill(summoner:change, args=<JSON>)`——`open` 冻结
  开发位置（当前任务/测试进度/未决验证项）进入 CHANGE_OPEN（合法于 ACTIVE
  与 WAIT_USER；WAIT_USER 下它是 gate 唯一放行的 Skill）；`apply` 提交
  delta {note, changed[], affected[]} 并开单点确认检查点。CHANGE_OPEN 期间
  phase-submit 被拒。影响评估方法由所选需求/计划 skill 承担，框架不另造。
- **恢复**：确认「接受变更」（`ACCEPT_REQUIREMENT_CHANGE`）→ Requirements-
  Version++、受影响 criterion/验证/审查结果标 **stale**、未受影响已接受结果
  保持有效；原地恢复，不重走 P1-P2。变更触及公共接口时必要重规划不可省，
  由影响评估结论决定并在确认点呈现。
- **变更 vs recall**：recall = 方向错了回城重做；变更 = 基线移动增量处理——
  边界表见 `../../references/workflow-reference.md`。

## Two-turn decisions (paged menus)

Every checkpoint decision is **two-turn**: turn N pastes the render and
ENDS the turn — the Stop hook binds the display
(id + nonce + content digest; the sole `Displayed` setter); turn N+1 asks the
decision question. A nonce-carrying ask in the render turn is denied; a
same-turn answer is always `decision_rejected`. One nudge turn per
checkpoint, deliberate.

AskUserQuestion caps 4 options per question, so each checkpoint PERSISTS a
paged menu projection (`menu_pages`; full menus page in 2, anchor pages carry
`continue`+`stop` plus kind actions). Offer ONE persisted page's labels
verbatim — the gate rejects any other offered set. `more:`/`back:` are
navigation, not decisions: nonce-free content feedback — re-ask with the
other page. Exactly ONE nonce-carrying question per call; two or more fail
closed. Canonical render + paging contract: `../../references/checkpoint-protocol.md`.

## Budget lifecycle (confirm → exhaust → extend)

Canonical rules: `../../references/checkpoint-protocol.md` (T18). In brief —
- **Run-start ask**: the FIRST checkpoint carries a budget ask (manifest
  `suggested_budgets`, else framework-unlimited) with nonce-free pointer
  options `budget: 按已显示预算` / `budget: 不设上限` riding the decision
  call; a continue/skip without a same-call budget answer rejects the whole
  callback (stop/done/recall/review exempt); offers are digest-bound,
  applied limits are not.
- **Exhaustion**: zero headroom denies all work except phase-submit, the
  exact read-only status call, the checkpoint ask, `summoner:change`,
  probe-submit and read-class OUTCOME_UNKNOWN probes. Never write around
  it — present the checkpoint; only the user extends or replans.
- **Extend ask**: a checkpoint opened while exhausted offers one extension
  (delta = current limit per set axis): `extend: 追加已显示额度` /
  `extend: 不追加`.

## Post-game closure (the sole CLOSED writer)

`done` and emergency `stop` land POST_GAME_PENDING; CLOSED needs BOTH, from
executed calls only: the questionnaire (ONE call, 4 questions, each text
carrying the run id + its `[pg:qN]` marker; afk/timeout observes nothing)
and the journal at the FIXED path `.summoner/journal/<run_id>.md` (latest
observed digest governs; emergency `stop` writes a minimal journal
hook-side at once, questionnaire owed as 补答). Closure fires
`post_game.completed` + `workflow.ended` once; CLOSED never reopens.
Protocol: `../../references/post-game-review.md`.

## Phase 0 memory (the `context` phase)

Before first domain work in any workflow: read `project.name` from
`summoner.yaml`; if the memory DB exists, follow `../../references/memory-chain.md`
§Phase 0 retrieval (Top-5 SQL, 1500-token cap). No DB → say so and
continue. The context phase closes at its checkpoint.

## Workflow quick reference

All seven phase routes are framework-owned (`hooks/shared/workflow.go`):
review = context→review; ship = prepare→review→merge→decide; ops =
context→operate; release = planning→changelog→publish; status = read-only;
fix/new/debug likewise. The manifest maps CAPABILITIES
(`phases.<capability>`), never topology; per-workflow rules live in
`../../commands/<name>.md`.

## OUTCOME_UNKNOWN recovery + the check trust ladder

- **Crash scan** (hooks, spec §5): SessionStart after a restart marks
  dangling MUTATING tool intents plus dead worker delegations; Stop at turn
  end marks tools only (a live worker is not a crash). Writes are denied
  while an episode is open; a crashed action is NEVER auto-replayed.
- **Resolution**: each unknown's EXACT probe (status serves it) binds
  once; the PostToolUse matcher computes every verdict — no outcome on
  the wire (`../probe-submit/SKILL.md`). All `accepted` →
  pre-episode status restores; any `failed`/`indeterminate` → RECOVERY
  checkpoint.
- **Check ladder** (spec §3; canonical rules: `../../references/manifest-spec.md`):
  L1 `trust: framework` = shipped adapter ops, computed in-gate at
  phase-submit. L2 `trust: project` = the EXACT registered line as a model
  Bash call, admitted only after the in-band probe
  `"${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-sandbox-probe"` refused the
  fresh HOME write (result tier is point-in-time). No/declined probe →
  honest BLOCKED `sandbox_unproven`; network-class → BLOCKED
  `l3_unsupported` — never "just run it". Cite as `check://<check_id>`; an
  L2 citation needs the observed current-digest PASS. Only `fail` ticks the
  failed-check budget; BLOCKED never does.

## Capability → provider resolution

Authority: `../../references/capability-map.md` (four layers + precedence). An
explicit manifest binding (`phases.<capability>.skill.default` or a
user-chosen alternate) wins — a configured project provider is never
displaced by a generic built-in. Only an UNBOUND capability walks the
layers: built-in/bundled → superpowers → project skills → Summoner's
disclosed defaults. An explicitly bound but missing skill stops the run —
never a silent fallback. Summoner keeps ONLY control capabilities ready
skills lack: checkpoints, requirement versions/change loop, pause-resume,
acceptance evidence, budgets, recovery, post-game.

## References

- `../../commands/<name>.md` — per-workflow rules
- `../../references/capability-map.md` — capability→provider authority
- `../../references/checkpoint-protocol.md` — canonical render + decision channel
- `../../references/workflow-reference.md` — auto-skip table, red flags
- `../../references/post-game-review.md` — questionnaire/journal protocol
- `../../references/memory-chain.md` — memory protocols
- `../../references/manifest-spec.md` — summoner.yaml fields
