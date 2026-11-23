---
description: 运维操作 — 领域步骤委托给项目 ops 能力（manifest phases.ops）阶段化执行；用户门、trace、复盘走同一 reducer。「运维」「部署变更」「上线操作」类意图路由到这里。
allowed-tools:
  - Skill
  - Read
  - Write
  - Edit
  - Bash
  - AskUserQuestion
  - Agent
---

# /summoner:ops

Operations workflow: the project's ops capability owns the domain steps;
Summoner owns the loop around them.

## Route

Run-start records the run on the framework route **context → operate** (fixed
in `hooks/shared/workflow.go`). The manifest maps the CAPABILITY — the
`phases.ops` skill binding (capability keys name the verb; `operate` is this
route's internal phase id — never `phases.operate`) — never topology.
Resolve the operate capability from `summoner.yaml`; no manifest → built-in
default, declared「未配置项目专属能力」. User gates, trace, budgets and
post-game run through the same single-loop reducer as every workflow
(`${CLAUDE_PLUGIN_ROOT}/references/checkpoint-protocol.md`; red flags / auto-skip table:
`${CLAUDE_PLUGIN_ROOT}/references/workflow-reference.md`) — no second execution chain.

## Rules

1. Loop per `${CLAUDE_PLUGIN_ROOT}/references/single-loop.md`: work → `summoner:phase-submit`
   (the only submission channel) → paste the summoner-status render verbatim
   and end the turn → next turn AskUserQuestion (nonce in the question,
   keyword-led labels from the persisted menu page).
2. The ops skill's internal staging (e.g. its own 5-step run) is the skill's
   business — Summoner does not re-orchestrate it. The framework loop wraps
   it: the operate phase closes ONCE at its checkpoint (single acceptance
   gate per operate phase) via `summoner:phase-submit`.
3. Mutating ops steps are exactly where OUTCOME_UNKNOWN bites: a crashed
   deploy/tag/write surfaces as a recovery checkpoint — run the capability's
   read-only probe, then let the user decide retry / recall / stop. Mutating
   actions are never auto-replayed.
4. Read-only checks run at whatever sandbox tier the status render reports
   (L2 / L1 / BLOCKED) — a BLOCKED project check is escalated, never "ran
   anyway".
5. ops skill failure → surface the error at the checkpoint; the user decides
   retry or stop.

## Post-Game Review

Mandatory — Type 4 (流程评价) per `${CLAUDE_PLUGIN_ROOT}/references/post-game-review.md`.
