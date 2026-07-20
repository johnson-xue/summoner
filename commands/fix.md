---
description: 修 Bug 全链路 — 诊断根因 → 复现测试 → 修复 → 验证 → 审查。Phase 1 是铁律，Phase 2-5 可按条件跳过。
phase_checkpoints: after_each
end_action: post_game_review
---

# /summoner:fix

Invoke the project's debug skill (from `summoner.yaml` `phases.debug`), then optionally test skill, then review.

Phase 1 is iron law — cannot be skipped. Full rationalizations, red flags, and verification: `references/workflow-reference.md`.

## Workflow

Phase 0→Memory Retrieval→Phase 1: diagnose(MANDATORY)→Phase 2: reproduce(optional)→Phase 3: fix(freeform)→Phase 4: verify(optional)→Phase 5: review(optional)

## Rules

1. Each phase: output **PHASE START** block at entry + **SUMMONER CHECKPOINT** block at end (format + field spec in `references/checkpoint-protocol.md`). Wait for user input.
2. Checkpoint options: continue / skip (current phase only) / done / recall / stop. If user reply is content feedback (方案/方向/漏了/不对…), handle it first then re-output CHECKPOINT — do NOT auto-advance as CONTINUE.
3. **Phase 1 iron law**: never skip diagnosis. Root cause unknown = all subsequent work is blind.

## Phase 3 Escalation (freeform fix — route by diagnosis outcome)

Phase 3 default skill is `phases.fix` (= `antia-logic` for antia). Escalate when diagnosis reveals the fix needs more than editing an existing function body:

| Diagnosis outcome | Route to |
|-------------------|----------|
| 改既有函数内部逻辑（数值/条件/分支/效果/规则，含 nil pointer / index out of range / panic 在既有函数内的修复），无新对外接口 | `phase.fix` = antia-logic（默认） |
| 修复需新增/改对外接口（proto + handler + 业务方法联动） | `phase.rpc` = antia-rpc |
| 修复需纯新增子系统（无既有枚举/目录） | `phase.subsystem` = antia-subsystem |
| 修复涉及 DB schema 变更（新表/改字段/迁移） | `phase.migrate` = antia-migrate（先迁移再逻辑修） |
| GMT 后台补偿/操作玩家数据 | `phase.gmt` = antia-gmt |

> 这条规则同时在 `summoner.yaml` `phases.fix` 注释里声明。Phase 3 前先向用户说明诊断结论与建议路由，用户确认后再进入对应 skill。

## Auto-Skip Conditions (propose to user — never auto-skip)

| Phase | Condition |
|-------|----------|
| Phase 2 (reproduce) | Pure config fix (data-only, no logic), or diff < 5 lines no logic change, or user says "I know the fix" |
| Phase 4 (verify) | Diff < 5 lines, config-only change |
| Phase 5 (review) | Single file, < 30 lines, no auth/data/config changes |

## Post-Game Review

Mandatory. Type selection per `references/post-game-review.md`. Priority merge: Type 1 > 5 > 3 > 2 > 4.
