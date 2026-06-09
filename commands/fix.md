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

1. Output SUMMONER CHECKPOINT after each phase (`references/checkpoint-protocol.md`). Wait for user input.
2. Checkpoint options: continue / skip (current phase only) / done / recall / stop.
3. **Phase 1 iron law**: never skip diagnosis. Root cause unknown = all subsequent work is blind.

## Auto-Skip Conditions (propose to user — never auto-skip)

| Phase | Condition |
|-------|----------|
| Phase 2 (reproduce) | Pure config fix (data-only, no logic), or diff < 5 lines no logic change, or user says "I know the fix" |
| Phase 4 (verify) | Diff < 5 lines, config-only change |
| Phase 5 (review) | Single file, < 30 lines, no auth/data/config changes |

## Post-Game Review

Mandatory. Type selection per `references/post-game-review.md`. Priority merge: Type 1 > 5 > 3 > 2 > 4.
