---
description: 修 Bug 全链路 — 诊断根因 → 复现测试 → 修复 → 验证 → 审查。「排查」类短语默认路由到这里（repair-bias：未说明只要原因即视为要修；Phase 1 检查点会先给出诊断，用户可随时 stop）。仅当用户明确只要原因不要改码时才用 /summoner:debug。Phase 1 是铁律，Phase 2-5 可按条件跳过。
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

Phase 3 default skill is `phases.fix`. Escalate when diagnosis reveals the fix needs more than editing an existing function body.

Routing is declarative: the `route_by_diagnosis` rule (declared in the project's `summoner.yaml` under `routing_rules:`) maps the diagnose node's `routing_tag` (logic/rpc/subsystem/migrate/gmt) to a target node id. This command does not hardcode the skill table — it references the rule. (The rule's `input_field` + `map` live in the project manifest; the framework command stays project-agnostic.)

Phase 3 前先向用户说明诊断结论与建议路由，确认后再进入对应 skill。

## Auto-Skip Conditions (propose to user — never auto-skip)

Canonical table: `references/workflow-reference.md` §Auto-Skip Conditions (fix rows: reproduce / verify / review). Propose the matching condition to the user; only the user confirms a skip.

## Post-Game Review

Mandatory. Type selection per `references/post-game-review.md`. Priority merge: Type 1 > 5 > 3 > 2 > 4.
