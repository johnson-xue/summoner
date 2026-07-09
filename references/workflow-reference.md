# Summoner Workflow Reference

> Loaded on-demand during workflow execution. Not loaded at startup.

## Workflow Definitions

### /summoner:fix
Phase 0→diagnose(MANDATORY)→reproduce(optional)→fix(freeform)→verify(optional)→review(optional)

### /summoner:new  
Phase 0→define→plan→implement→test→review

### /summoner:ship
Phase 0→fan-out(1-3 personas, adaptive)→merge→go/no-go decision

### /summoner:debug
Phase 0→diagnose only, no code changes

### /summoner:ops
Phase 0→ops skill (delegated)

### /summoner:review
Phase 0→code review (code-reviewer persona), no other phases

## Auto-Skip Conditions

Phases are offered as skippable (not auto-skipped — user confirms):

| Workflow | Phase | Skip condition |
|----------|-------|---------------|
| fix | reproduce | Pure config fix (data-only, no logic change) |
| fix | verify | Diff < 5 lines, config-only |
| fix | review | Single file, < 30 lines, no auth/data changes |
| ship | fan_out | < 3 files AND < 50 lines AND no sensitive changes |

## Common Rationalizations

| AI thinks... | Reality |
|-------------|---------|
| "The error code is obvious, skip Phase 1" | Phase 1 is iron law. Obvious is often wrong. |
| "User skipped phase 2, I'll skip phase 4 too" | Each phase decides independently. Only skip current. |
| "This is too simple for checkpoints" | Checkpoints are the Summoner contract. Recorded as Type 5. |
| "User usually says yes, I'll auto-continue" | The ONE time they want to stop is the ONE time it matters. |
| "Manifest says 'none' for security, just warn" | Ask: "No security phase. Proceed without audit?" |

## Red Flags

- ✗ Advancing past checkpoint without user confirmation
- ✗ Skipping Phase 1 in fix/debug workflows
- ✗ Not outputting exact SUMMONER CHECKPOINT format
- ✗ Not outputting PHASE START block at phase entry (user loses "which phase / what task" context)
- ✗ Misreading content feedback as CONTINUE (e.g. "方案漏了边界" → CONTINUE instead of handling feedback)
- ✗ Omitting CHECKPOINT fields when empty (must show `None` / `No artifacts — analysis only.`)
- ✗ Hardcoding project names or domain skill names in framework output
- ✗ Skipping post-game review after workflow completion
- ✗ Personas calling other personas instead of reporting

## Verification Checklist

After workflow completion:
- [ ] Every phase had a PHASE START block + CHECKPOINT block output (paired)
- [ ] CHECKPOINT fields all present (empty fields show explicit placeholder, not omitted)
- [ ] User confirmed each checkpoint decision
- [ ] Content feedback replies were handled (not misread as CONTINUE)
- [ ] Post-game review questionnaire was presented and answered
- [ ] Journal entry was written to SQLite
- [ ] No phase was silently skipped (auto-skip still requires user confirmation)
- [ ] All artifacts are documented in checkpoint blocks
- [ ] Phase 0 memory retrieval was attempted (or skipped if no db)
- [ ] Matched patterns presented to user (if any found)
- [ ] Token budget ≤1500 respected
