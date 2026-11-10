# Summoner Workflow Reference

> Loaded on-demand during workflow execution. Not loaded at startup.

## Workflow Definitions (single-loop phase routes)

Framework-owned sequences (fixed order; the manifest maps each phase to a
project skill, it never rewrites the order):

- `/summoner:fix`: context → diagnosis(M) → reproduce → fix → regression → review
- `/summoner:new`: context → requirements → plan → implement → verify → review
- `/summoner:debug`: context → diagnosis（止于诊断检查点，不改码）

Manifest-routed workflows (run-start records the run; the phase route comes
from the project manifest — the manifest check registry task formalizes the
mapping; user gates, trace and post-game flow through the same reducer):

- `/summoner:review`: verification_spec route (manifest `phases.review`) — one independent reviewer answers「这份变更有哪些问题」
- `/summoner:ship`: one fresh reviewer over the full rubric → merge report → GO/NO-GO
- `/summoner:ops`: project ops capability runs the domain steps
- `/summoner:release`: version-plan → changelog → release-execution (framework-internal; implementation in skills/summoner-release/SKILL.md)

(M) = iron law: in fix/debug, writes are gate-denied until the diagnosis
phase is ACCEPTED at its checkpoint.

Every phase — including skipped ones — closes at a checkpoint; every arrow is
a recorded user decision (`references/checkpoint-protocol.md`).

## Auto-Skip Conditions

Phases are offered as skippable at their checkpoints (never auto-skipped —
the user confirms; a skipped phase is marked UNVERIFIED/UNREVIEWED):

| Workflow | Phase | Skip condition |
|----------|-------|---------------|
| fix | reproduce | Pure config fix (data-only, no logic change), or diff < 5 lines with no logic change, or user says "I know the fix" |
| fix | regression | Diff < 5 lines, config-only |
| fix | review | Single file, < 30 lines, no auth/data/config changes |
| new | review | Single file, < 100 lines, no new public API; or user says "不用 review" |

<!-- canonical:auto-skip-table — commands reference this table; inlining a
     variant elsewhere is a lint error (scripts/validate-repo.sh). -->

## Common Rationalizations

| AI thinks... | Reality |
|-------------|---------|
| "The error code is obvious, skip diagnosis" | Diagnosis is iron law — the gate denies writes until it is accepted. Obvious is often wrong. |
| "User skipped phase 2, I'll skip phase 4 too" | Each phase decides independently at its own checkpoint. |
| "This is too simple for checkpoints" | Checkpoints are the Summoner contract. Recorded as Type 5. |
| "User usually says yes, I'll auto-continue" | Advancement is AskUserQuestion-only; free text never advances. |
| "用户描述得很清楚了，直接写码就行" | 没 spec = 假设当需求。requirements/plan 先行。 |
| "这个功能简单，plan 和 spec 合并为一步就行" | Spec ≠ Plan。跳过 plan = 实现顺序靠猜。 |
| "审查员 PASS 了，直接进下一阶段" | PASS 仍留 WAIT_USER；推进只来自用户决定。 |

## Red Flags

- ✗ Advancing past a checkpoint without a recorded AskUserQuestion decision
- ✗ Hand-writing a checkpoint/status render instead of pasting the
  summoner-status output (display verification binds id + nonce)
- ✗ AskUserQuestion at a checkpoint whose question text lacks the nonce, or
  whose option labels do not LEAD with the decision keyword
- ✗ Misreading content feedback as CONTINUE (e.g. "方案漏了边界" → revise and re-ask)
- ✗ Writes before diagnosis acceptance in fix/debug (the gate's exit-2 deny
  IS the iron law, not an error to work around)
- ✗ Skipping diagnosis, or presenting a skipped phase as verified
- ✗ Hardcoding project names or domain skill names in framework output
- ✗ Skipping post-game review after workflow completion
- ✗ Presenting a BLOCKED review as a quality conclusion, or a NEEDS-FIX as PASS

## Verification Checklist

After workflow completion:
- [ ] Every phase opened, was submitted via summoner:phase-submit, and closed at a rendered checkpoint
- [ ] Every checkpoint render was the binary's output pasted verbatim (id + nonce present)
- [ ] Every decision came from AskUserQuestion (menu labels keyword-led)
- [ ] Content feedback replies were handled (not misread as advancement)
- [ ] Skipped phases are recorded as UNVERIFIED/UNREVIEWED — none presented as PASS
- [ ] Post-game review questionnaire presented and journal entry persisted
- [ ] Phase 0 memory retrieval was attempted (or skipped with no DB, stated)
- [ ] Matched patterns presented to user (if any found)

## Ship Merge Report Format (canonical — commands/ship.md points here)

```markdown
## Ship Decision: GO | NO-GO

### Blockers (must fix before ship)
- [Critical finding + file:line]

### Recommended fixes (should fix before ship)
- [Important finding + file:line]

### Acknowledged risks (shipping anyway)
- [Risk + mitigation]

### Rollback plan
- Trigger conditions: {what signals would prompt rollback}
- Rollback procedure: {exact steps}
- Recovery time objective: {target}

### Review report (full)
- [single reviewer's full report; sensitive changes add criteria, not reviewers]
```

<!-- canonical:ship-merge-template — commands/ship.md references this; inlining
     a variant elsewhere is a lint error (scripts/validate-repo.sh). -->
