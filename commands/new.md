---
description: 新增功能全链路 — context → requirements → plan → implement → verify → review，每阶段经检查点由用户裁决。需求调整走变更子环（增量恢复，不重跑流水线）。「加个功能」「新需求」类意图路由到这里。
allowed-tools:
  - Skill
  - Read
  - Write
  - Edit
  - Bash
  - AskUserQuestion
  - Agent
---

# /summoner:new

Feature pipeline. Spec/plan first; every phase closes at a user checkpoint
(`${CLAUDE_PLUGIN_ROOT}/references/checkpoint-protocol.md`).

## Route

context → requirements → plan → implement → verify → review → post-game

- **context**: memory retrieval (`${CLAUDE_PLUGIN_ROOT}/references/memory-chain.md` §Phase 0).
- **requirements**: definition + exit criteria — the acceptance view freezes at user acceptance; producers cannot trim it later.
- **plan**: task breakdown frozen at its checkpoint (goal/deliverables/dependencies/acceptance — a document, not an execution topology).
- **implement**: build in this session via the manifest-mapped implement skill; local tests in the same context.
- **verify**: cross-component + negative-path verification.
- **review**: one independent reviewer over the full rubric (skippable → UNREVIEWED).

Phase capabilities come from the project manifest (`summoner.yaml` `phases.*`);
the mapping (including capability variants) is the manifest's job — this
command hardcodes no project skill. 无 manifest → 内置默认能力，明示「未配置项目专属能力」。

## Capability split (需求管理 vs 开发交付)

ONE run entry, TWO capability roles, each independently bindable via the
manifest: **需求管理 (requirements)** owns requirements + plan (definition,
exit criteria, freeze) plus delta/impact assessment when a change opens
mid-run (method = the bound skill's; the framework carries only the
conclusion). **开发交付 (delivery)** owns implement + verify
(building/testing in this session; context/review as usual). 拆能力，不拆
上下文，不拆状态 — both roles share the ONE run state and the main session's context.
A mid-delivery change opens the change loop
(`${CLAUDE_PLUGIN_ROOT}/skills/change/SKILL.md`), freezes the development
position, and resumes in place after the single-point confirm — never a
restart of the pipeline.

## Rules

1. Loop per `${CLAUDE_PLUGIN_ROOT}/references/single-loop.md`: work → `summoner:phase-submit`
   (the only submission channel) → paste the summoner-status render verbatim
   and end the turn → next turn AskUserQuestion (nonce in the question,
   keyword-led labels from the persisted menu page).
2. requirements/plan are not skippable (没 spec 不动工，没 plan 不写码).
3. Direction wrong after acceptance → `recall:` to the phase's history label
   (downstream accepted criteria go stale). Baseline moved → change loop.
4. Rationalizations: `${CLAUDE_PLUGIN_ROOT}/references/workflow-reference.md`.

## Post-Game Review

Mandatory; type rules per `${CLAUDE_PLUGIN_ROOT}/references/post-game-review.md`.
