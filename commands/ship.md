---
description: 发版前审查 — 并行 fan-out 三个 persona → merge 报告 → go/no-go 决策
phase_checkpoints: after_merge
end_action: post_game_review
---

# /summoner:ship

Fan-out orchestrator. Runs three personas in parallel, merges their reports, produces a go/no-go decision with rollback plan.

## Workflow

```
Phase A (PARALLEL):
  ├── code-reviewer    → review report
  ├── security-auditor → audit report
  └── test-engineer    → coverage report

Phase B (MERGE):
  synthesize → go/no-go + rollback plan
```

## Phase A — Parallel Fan-Out

Spawn all three personas simultaneously using the Agent tool in a single turn:

1. `code-reviewer` — 5-axis review on staged changes / recent commits
2. `security-auditor` — Vulnerability and threat-model pass
3. `test-engineer` — Test coverage analysis

## Phase B — Merge

Synthesize the three reports into one output:

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

## Adaptive Fan-Out

Fan-out scale is determined by diff size before launching personas:

| Diff Size | Personas | Rationale |
|-----------|----------|-----------|
| < 50 lines AND < 3 files | Skip fan-out entirely | Trivial change — suggest `/summoner:review` |
| < 200 lines | code-reviewer only | Small change — 5-axis review sufficient |
| < 500 lines | code-reviewer + test-engineer | Medium change — review + coverage check |
| ≥ 500 lines OR touches auth/data/config | All 3 (code-reviewer + security-auditor + test-engineer) | Large/sensitive change — full audit |

## Rules

1. 每个 Phase（A 并行 / B merge）开始输出 **PHASE START** 块 + 结束输出 **SUMMONER CHECKPOINT** 块（格式与字段规约见 `references/checkpoint-protocol.md`），等待用户选择。内容反馈先处理再重问。
2. Phase A personas run in PARALLEL — never sequentially.
3. Personas do NOT call each other. The main agent merges in Phase B.
4. Rollback plan is MANDATORY before any GO decision.
5. If any persona returns a Critical finding, default verdict is NO-GO unless user explicitly accepts the risk.
6. **Fan-out scale is dynamic** — determine diff size first, then select personas per the Adaptive Fan-Out table above.
7. **Skip fan-out entirely** if changes touch < 3 files AND diff < 50 lines AND no auth/payments/data/config changes. Suggest `/summoner:review` instead.
8. **Always run all 3** if the diff touches auth, payments, personal data, or configuration/env files — regardless of line count.

## Post-Game Review

Trigger Type 4 (流程评价) after ship decision.
