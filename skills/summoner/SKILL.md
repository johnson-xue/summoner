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
   Budgets surface at the first checkpoint (unlimited until the user sets
   them at a run's checkpoints).
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
   checkpoint's nonce; the options are the rendered menu verbatim. Every
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

## Requirement changes vs recall (pointer)

自由文本提出需求调整 = 开变更子环（CHANGE_OPEN：冻结开发位置 → delta → 影响评估 →
单点确认「接受变更并恢复开发」），增量恢复、不重跑流水线——与 recall（方向错了回城
重做）是两条不同的路。边界全文（触发、stale 传播、预算口径）落在变更协议文档
(spec §1 需求变更闭环；runtime wiring 后续任务接线)。检查点期间提出变更会 supersede
当前检查点，确认后以其重渲染版重新询问。

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
| `/summoner:review` | verification_spec 路线（manifest `phases.review`） |
| `/summoner:ship` | 单 reviewer 全 rubric → merge 报告 → GO/NO-GO |
| `/summoner:ops` | 项目 ops 能力承担领域步骤；用户门/trace/复盘走同一 reducer |
| `/summoner:release` | 版本计划 → changelog → 发布执行（自含，T13 维护） |
| `/summoner:status` | 只读状态视图（不改状态） |

(M) = iron law. Framework phase sequences cover fix/new/debug today;
review/ship/ops/release runs record via run-start and take their phase route
from the project manifest (manifest check registry task formalizes it).

## References

- `commands/<name>.md` — per-workflow rules
- `references/checkpoint-protocol.md` — canonical checkpoint render + decision channel (CANONICAL, never inline a variant)
- `references/workflow-reference.md` — auto-skip table, rationalizations, red flags, ship merge report
- `references/post-game-review.md` — questionnaire, journal, memory write protocol
- `references/memory-chain.md` — memory retrieval/write protocols
- `references/manifest-spec.md` — summoner.yaml fields
- `skills/phase-submit/SKILL.md` — the submission payload contract
