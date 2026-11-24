---
description: 发版前审查 — 单一独立 reviewer 覆盖完整 rubric → merge 报告 → GO/NO-GO 决策。敏感变更增加标准，不增加 reviewer。「上线前把把关」「发版前审查」类意图路由到这里。
allowed-tools:
  - Skill
  - Read
  - Bash
  - AskUserQuestion
  - Agent
---

# /summoner:ship

Pre-launch decision review. ONE fresh independent reviewer covers the complete
rubric (correctness/idiom/architecture/security/impact + Prove-It); the main
session merges the report into a go/no-go decision. Sensitive changes add
CRITERIA to the verification spec — never more reviewers (parallel fan-out
returns only if an evaluation proves net benefit).

## Route

Run-start records the run on the framework route **prepare → review → merge →
decide** (fixed in `hooks/shared/workflow.go`; the four steps below ARE the
four phases). The manifest maps CAPABILITIES, never topology. In practice:

1. **Prepare** — freeze the diff baseline (base + snapshot, or file digests
   without Git), assemble the verification_spec (criteria from the project's
   rubric; auth/payment/personal-data/config touches ADD criteria), register
   the review envelope. Mechanical criteria freeze their check definitions:
   `verification_scope.check_ids` carries `check_id@digest` (registry
   enforced; hooks/shared/envelope.go is the authority).
2. **Review** — delegate one typed `reviewer` Agent; evidence reconciled
   against its transcript; BLOCKED (sandbox/env) is a reason code, never a
   quality verdict.
3. **Merge** — render the canonical **Ship Merge Report Format** from
   `${CLAUDE_PLUGIN_ROOT}/references/workflow-reference.md` (GO | NO-GO + blockers / recommended /
   risks / rollback / full report — never inline a variant).
4. **Decide** — the checkpoint asks the user; a Critical finding defaults
   NO-GO unless the user explicitly accepts the risk.

## Rules

1. Loop per `${CLAUDE_PLUGIN_ROOT}/references/single-loop.md`: work → `summoner:phase-submit`
   with a conclusion (the only submission channel) → paste the checkpoint and end
   the turn → the Stop hook hands back the question → AskUserQuestion. An auto-passed
   phase shows no checkpoint: go straight on.
   Hooks dormant (no `SUMMONER_HOOKS_ENFORCE=1`)? Replace this loop with
   plain AskUserQuestion checkpoints — single-loop.md §2 (0.7.1).
2. Cite existing review/verification evidence instead of re-running it —
   same-digest artifacts with a still-valid result reuse once.
3. Rollback plan is MANDATORY before any GO.
4. review vs ship: review answers「这份变更有哪些问题」；ship 引用其证据做
   GO/NO-GO，不重复同一审查（boundary canonical:
   `${CLAUDE_PLUGIN_ROOT}/references/capability-map.md`）.

## Post-Game Review

Mandatory — Type 4 (流程评价) after the ship decision.
