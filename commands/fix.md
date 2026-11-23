---
description: 修 Bug 全链路 — context → diagnosis（铁律）→ reproduce → fix → regression → review，每阶段经检查点由用户裁决。Phase 2-5 可按条件显式跳过（标 UNVERIFIED/UNREVIEWED）。「排查」「修一下」类短语默认路由到这里（repair-bias）；仅当用户明确只要原因不要改码时才用 /summoner:debug。
allowed-tools:
  - Skill
  - Read
  - Write
  - Edit
  - Bash
  - AskUserQuestion
  - Agent
---

# /summoner:fix

Bug-repair pipeline. Every phase closes at a user checkpoint; decisions come
only from AskUserQuestion (`${CLAUDE_PLUGIN_ROOT}/references/checkpoint-protocol.md`).

## Route

context → diagnosis(M) → reproduce → fix → regression → review → post-game

- **context**: memory retrieval (`${CLAUDE_PLUGIN_ROOT}/references/memory-chain.md` §Phase 0); no DB → state it, continue.
- **diagnosis (M)**: the project's debug skill (manifest `phases.debug` or `phases.diagnosis`) produces root cause + trigger conditions + location + evidence. Writes are gate-denied until this phase is ACCEPTED at its checkpoint. Root cause unknown → keep investigating or stop; never proceed blind.
- **reproduce** (skippable): same-context reproduction test.
- **fix**: repair using the manifest-mapped fix skill (or freeform with the user); local same-context tests.
- **regression** (skippable): regression + integration verification.
- **review** (skippable → UNREVIEWED): one independent reviewer over the frozen acceptance view; can never be faked as PASS.

## Rules

1. Loop per `${CLAUDE_PLUGIN_ROOT}/references/single-loop.md`: work → `summoner:phase-submit`
   with a conclusion (the only submission channel) → paste the checkpoint and end
   the turn → the Stop hook hands back the question → AskUserQuestion. An auto-passed
   phase shows no checkpoint: go straight on.
2. Skips are proposed at the checkpoint against the auto-skip table
   (`${CLAUDE_PLUGIN_ROOT}/references/workflow-reference.md`) — only the user confirms; skipped
   phases are marked UNVERIFIED/UNREVIEWED.
3. Any phase checkpoint may take `review:` ([v] on-demand independent review).
4. Requirement tweaks mid-run open the change loop (delta → impact →
   single-point accept), not a recall and not a restart.
5. Rationalizations/red flags: `${CLAUDE_PLUGIN_ROOT}/references/workflow-reference.md`.

## Post-Game Review

Mandatory — questionnaire per `${CLAUDE_PLUGIN_ROOT}/references/post-game-review.md`; the run is
CLOSED only after the journal persists.
