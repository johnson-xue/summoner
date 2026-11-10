---
description: 新增功能全链路 — context → requirements → plan → implement → verify → review，每阶段经检查点由用户裁决。需求调整走变更子环（增量恢复，不重跑流水线）。
---

# /summoner:new

Feature pipeline. Spec/plan first; every phase closes at a user checkpoint
(`references/checkpoint-protocol.md`).

## Route

context → requirements → plan → implement → verify → review → post-game

- **context**: memory retrieval (`references/memory-chain.md` §Phase 0).
- **requirements**: definition + exit criteria — the acceptance view freezes at user acceptance; producers cannot trim it later.
- **plan**: task breakdown frozen at its checkpoint (goal/deliverables/dependencies/acceptance — a document, not an execution topology).
- **implement**: build in this session via the manifest-mapped implement skill; local tests in the same context.
- **verify**: cross-component + negative-path verification.
- **review**: one independent reviewer over the full rubric (skippable → UNREVIEWED).

Phase capabilities come from the project manifest (`summoner.yaml` `phases.*`);
the mapping (including capability variants) is the manifest's job — this
command hardcodes no project skill. 无 manifest → 内置默认能力，明示「未配置项目专属能力」。
需求管理 and 开发交付 are two capability roles over ONE run state — 拆能力不拆上下文。

## Rules

1. Loop per `skills/summoner/SKILL.md`: work → `summoner:phase-submit` →
   paste the summoner-status render verbatim → AskUserQuestion.
2. requirements/plan are not skippable (没 spec 不动工，没 plan 不写码).
3. Direction wrong after acceptance → `recall:` to the phase's history label
   (downstream accepted criteria go stale). Baseline moved → change loop.
4. Rationalizations: `references/workflow-reference.md`.

## Post-Game Review

Mandatory; type rules per `references/post-game-review.md`.
