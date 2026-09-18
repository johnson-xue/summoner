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

### /summoner:release
version-plan→changelog→release-execution (framework-internal, self-contained — no manifest routing; implementation in skills/summoner-release/SKILL.md)

## Auto-Skip Conditions

Phases are offered as skippable (not auto-skipped — user confirms):

| Workflow | Phase | Skip condition |
|----------|-------|---------------|
| fix | reproduce | Pure config fix (data-only, no logic change), or diff < 5 lines with no logic change, or user says "I know the fix" |
| fix | verify | Diff < 5 lines, config-only |
| fix | review | Single file, < 30 lines, no auth/data/config changes |
| new | review | Single file, < 100 lines, no new public API; or user says "不用 review" |
| ship | fan_out | < 3 files AND < 50 lines AND no sensitive changes |

<!-- canonical:auto-skip-table — commands reference this table; inlining a
     variant elsewhere is a lint error (scripts/validate-repo.sh). -->

## Common Rationalizations

| AI thinks... | Reality |
|-------------|---------|
| "The error code is obvious, skip Phase 1" | Phase 1 is iron law. Obvious is often wrong. |
| "User skipped phase 2, I'll skip phase 4 too" | Each phase decides independently. Only skip current. |
| "This is too simple for checkpoints" | Checkpoints are the Summoner contract. Recorded as Type 5. |
| "User usually says yes, I'll auto-continue" | The ONE time they want to stop is the ONE time it matters. |
| "Manifest says 'none' for security, just warn" | Ask: "No security phase. Proceed without audit?" |
| "用户描述得很清楚了，直接写码就行" | 没 spec = 假设当需求。Phase 1 必须出书面设计。 |
| "这个功能简单，plan 和 spec 合并为一步就行" | Spec ≠ Plan。Spec 定义 what，Plan 定义 how。跳过 Plan = 实现顺序靠猜。 |

## Per-Task Graph

A plan artifact may carry a fenced ` ```yaml summoner-task-graph ` block. When present, SKILL.md drives the graph walker (`summoner-walker next` → directive → `record` → route) instead of the linear phase chain. When absent, the plain chain runs (backward compat, spec §2.6).

**Emission rule (M12):** the plan emits a graph block iff the task has ≥3 nodes OR any mutating node carries a back-edge. A ≤2-node task with no back-edge is a plain chain — do not wrap it in a graph (the walker's bookkeeping overhead buys nothing there).

**Graph red flags (review-isolation invariant — these are fails, not warnings):**
- ✗ A review-agent verdict with empty `evidence_tool_calls` — a rubber-stamp ⑤. The verdict MUST be grounded in grep hits / file:line citations.
- ✗ ⑤ reading producer reasoning — the review-agent receives `envelope_id` (paths + exit_criteria ONLY). The handoff envelope body must NOT carry `producer_reasoning_trace` or `producer_verdict_self_report`; exposing them to ⑤ defeats review-isolation (⑤ must re-derive, never read the producer's self-report).
- ✗ A handoff whose `stripped` array omits `producer_reasoning_trace`/`producer_verdict_self_report` — the `stripped` field is the list of fields that WERE removed, and it MUST list these two (recording that they were actually stripped before the envelope reached ⑤).

**Writing-plans contract:** when a plan includes a graph block, the `budget` (max_graph_turns / total_token_budget / max_back_edges_total), `nodes` (each with `id`, `label`, `skill`, `exit_criteria`, `max_inner_turns`), and `edges` must all be present. The walker parses this block via the fenced-yaml extractor (M4) — a malformed fence means the walker falls back to chain mode, silently. Validate the fence before committing the plan.

## Red Flags

- ✗ Advancing past checkpoint without user confirmation
- ✗ Skipping Phase 1 in fix/debug workflows
- ✗ Chain-mode: code edits before a phase skill has run in the session — the PreToolUse hook DENIES such Edit/Write calls (exit 2). The deny IS the iron law, not an error to work around; complete Phase 0-1 first. (Kill switch: `SUMMONER_IRON_LAW=off`; all-none manifests are pre-advanced.)
- ✗ Not outputting exact SUMMONER CHECKPOINT format
- ✗ Not outputting PHASE START block at phase entry (user loses "which phase / what task" context)
- ✗ Misreading content feedback as CONTINUE (e.g. "方案漏了边界" → CONTINUE instead of handling feedback)
- ✗ Omitting CHECKPOINT fields when empty (must show `None` / `No artifacts — analysis only.`)
- ✗ Hardcoding project names or domain skill names in framework output
- ✗ Skipping post-game review after workflow completion
- ✗ Personas calling other personas instead of reporting
- ✗ Graph-mode: review-agent verdict with empty `evidence_tool_calls` (rubber-stamp ⑤)
- ✗ Graph-mode: review-agent reading producer reasoning (review-isolation break)
- ✗ Graph-mode: handoff envelope whose `stripped` array omits `producer_reasoning_trace`/`producer_verdict_self_report` (review-isolation invariant #6 — see §Per-Task Graph red flags above)

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

## Ship Merge Report Format (canonical — commands/ship.md points here)

```markdown
## Ship Decision: GO | NO-GO

### Blockers (must fix before ship)
- [Source persona: Critical finding + file:line]

### Recommended fixes (should fix before ship)
- [Source persona: Important finding + file:line]

### Acknowledged risks (shipping anyway)
- [Risk + mitigation]

### Rollback plan
- Trigger conditions: {what signals would prompt rollback}
- Rollback procedure: {exact steps}
- Recovery time objective: {target}

### Reports (full)
- [code-reviewer report]
- [security-auditor report]
- [test-engineer report]
```

<!-- canonical:ship-merge-template — commands/ship.md references this; inlining
     a variant elsewhere is a lint error (scripts/validate-repo.sh). -->
