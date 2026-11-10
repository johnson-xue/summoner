---
description: 运维操作 — 领域步骤委托给项目 ops 能力（manifest phases.ops）阶段化执行；用户门、trace、复盘走同一 reducer。
---

# /summoner:ops

Operations workflow: the project's ops capability owns the domain steps;
Summoner owns the loop around them.

## Route

Run-start records the run (ops has no framework-owned phase sequence yet —
the route comes from the manifest mapping; the manifest check registry task
formalizes it). Resolve `phases.ops` from `summoner.yaml`; no manifest →
built-in defaults, declared 「未配置项目专属能力」.

## Rules

1. The ops skill's internal staging (e.g. its own 5-step run) is the skill's
   business — Summoner does not re-orchestrate it. The framework loop wraps
   it: submit each batch via `summoner:phase-submit`, close at checkpoints,
   decisions via AskUserQuestion (`references/checkpoint-protocol.md`).
2. Mutating ops steps are exactly where OUTCOME_UNKNOWN bites: a crashed
   deploy/tag/write surfaces as a recovery checkpoint — run the capability's
   read-only probe, then let the user decide retry / recall / stop. Mutating
   actions are never auto-replayed.
3. Read-only checks run at whatever sandbox tier the status render reports
   (L2 / L1 / BLOCKED) — a BLOCKED project check is escalated, never "ran
   anyway".
4. ops skill failure → surface the error at the checkpoint; the user decides
   retry or stop.

## Post-Game Review

Mandatory — Type 4 (流程评价) per `references/post-game-review.md`.
