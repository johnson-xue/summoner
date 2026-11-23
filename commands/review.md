---
description: 独立代码审查 — verification_spec 路线（manifest phases.review），单一独立 reviewer 黑盒重查，不改代码。回答「这份变更有哪些问题」。「审查/看看这份变更有什么问题」类意图路由到这里；要 GO/NO-GO 决策用 /summoner:ship。
allowed-tools:
  - Skill
  - Read
  - Bash
  - AskUserQuestion
  - Agent
---

# /summoner:review

Standalone review of a change. Review answers "这份变更有哪些问题" — it does
not decide shipping (that is `/summoner:ship`, which cites review evidence
without repeating it). Thin entry: the boundary and both capability rows are
canonical in `${CLAUDE_PLUGIN_ROOT}/references/capability-map.md`.

## Route

The capability comes from the project manifest: `phases.review` maps to a
**verification_spec** — criteria to verify, registered check ids, readable
scope, rubric pointer. The project defines WHAT to verify; the reviewer, in a
fresh restricted context, works out HOW (spec §3; envelope contract in
`${CLAUDE_PLUGIN_ROOT}/agents/reviewer.md`).

Run-start records the run on the framework route **context → review** (fixed
in `hooks/shared/workflow.go`). The manifest maps the CAPABILITY — the
`phases.review` verification_spec binding — never topology. User gates, trace
and post-game flow through the same reducer as every workflow.

## Rules

1. Loop per `${CLAUDE_PLUGIN_ROOT}/references/single-loop.md`: work → `summoner:phase-submit`
   with a conclusion (the only submission channel) → paste the checkpoint and end
   the turn → the Stop hook hands back the question → AskUserQuestion. An auto-passed
   phase shows no checkpoint: go straight on.
2. One typed `reviewer` Agent (non-fork, tool-restricted, no project memory):
   the gate binds the delegation to the PREPARED review id — build and
   register the envelope first. Every mechanical criterion's envelope entry
   MUST carry the frozen `check_id@digest` form in
   `verification_scope.check_ids` — bare/missing/malformed entries are
   rejected (hooks/shared/envelope.go is the authority).
3. Evidence discipline: every claim cites real tool calls
   (`evidence_tool_call_ids` reconciled against the reviewer's transcript);
   a PASS with empty evidence is a protocol error. grep 命中只是定位，不是证明。
4. Output: findings graded Critical / Important / Suggestion, each with
   criterion id + path:line + failure + suggestion. No next-phase choice —
   that is the user's checkpoint decision.
5. Review only — never modify code. Artifacts changing mid-review void the
   result and force a re-review.
6. Nothing to review (empty diff / merge-commit only) → say so and stop.

## Post-Game Review

Mandatory — Type 4 per `${CLAUDE_PLUGIN_ROOT}/references/post-game-review.md`.
