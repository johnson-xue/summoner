# Trace Fixtures

Contract-scorer trace fixtures for the graph + node-contract upgrade
(spec: `docs/superpowers/specs/2026-07-27-graph-node-contract-design.md`).
Each `.jsonl` file is one JSON event per line, following the trace-protocol.

## Catalog

| Fixture | Scenario | Expected scorer outcome |
|---|---|---|
| `example-C2-clean-graph-pass.jsonl` | **C2** — clean full-graph pass: every ⑤ returns PASS on the first attempt, no retries / back-edges / rejects. The happy path. | PASS all 3 scorers |
| `example-C3-verify-fail-backedge.jsonl` | **C3** — verify ③ FAIL → retry to `max_inner_turns` → `node_test_loop exhausted:true` → cross-node `handoff_reject` (verify→fix) with `skip:[reproduce]`; fix re-runs without re-diagnosing, then verify passes. | PASS all 3 scorers |
| `example-C4-new-graph-review-agent-catches.jsonl` | **C4 (NEW)** — ⑤ on fix independently greps all deref sites, finds the 2nd caller at `task.go:187` the producer missed; same-node `node_review_retry` back-edges to fix before any human checkpoint. | PASS all 3 scorers |
| `example-C4-old-chain-lets-defect-through.jsonl` | **C4 (OLD)** — pre-graph baseline: no ⑤, the 2nd-caller defect ships to the human. The OLD half of the C4 old-vs-new pair. | (baseline — not scorer-asserted) |
| `example-C5-review-isolation-violation.jsonl` | **C5** — ADVERSARIAL: rubber-stamp `review_verdict` (empty `evidence_tool_calls`) AND producer-reasoning leak (`stripped` omits `producer_reasoning_trace` + `producer_verdict_self_report`) on h-002. | **FAIL `review-isolation-check.sh`** (exit 1) — this is the fixture's purpose |
| `example-C9-three-times-same-finding.jsonl` | **C9** — cross-node ⑤ returns NEEDS-FIX with the SAME finding (`player/task/task.go:187`, the `findingKey` dedup key, walk.go:210-214) 3×. The 1st and 2nd NEEDS-FIX emit cross-node `handoff_reject` #1/#2 (`FindingsSeen[key]`=1 then 2, both <3); the 3rd makes `FindingsSeen[key]`=3 ≥3 → the walker returns `checkpoint` (walk.go:155-156) BEFORE the handoff_reject append (walk.go:164) → **NO 3rd `handoff_reject`** is emitted. So the trace has exactly 2 `handoff_reject` + 1 escalation `checkpoint`; the 4th back-edge never fires. Uses cross-node `handoff_reject` (from_node=verify), NOT same-node `node_review_retry` (same-node retries don't increment `FindingsSeen` / don't escalate). Verified empirically against the walker. | PASS all 3 scorers (well-formed trace showing the escalation pattern; the 3× counter itself is unit-tested in `internal/graph/walk_test.go`) |
| `example-C10-cross-file-soft-⑤-catches.jsonl` | **C10 (NEW)** — cross-file SOFT scenario: ⑤ independently greps drop/clear call-sites across `cross_server/` + `inventory/`, finds `inventory.go:410 Clear()` the producer's fix missed; `node_review_retry` on fix. | PASS all 3 scorers |
| `example-C10-old-chain-lets-defect-through.jsonl` | **C10 (OLD)** — pre-graph baseline: the `inventory.go:410` cross-file defect ships to the human. The OLD half of the C10 pair. | (baseline — not scorer-asserted) |
| `invalid-missing-phase1.jsonl` | Negative fixture (pre-graph): missing Phase 1. | FAIL (contract violation) |
| `valid-fix-workflow.jsonl` | Positive fixture (pre-graph): a well-formed chain-mode fix workflow. | (pre-graph baseline) |

## Scorers

Run from the repo root (`bash scorers/deterministic/<check>.sh <trace.jsonl>`):

- `handoff-contract-check.sh` — §2.2 + §2.2.1 typed-envelope contract (envelope_id correlation, no `passed` in `attempt_history`, `verdict_type` on every exit_criterion, no producer-reasoning leak fields). Exit 0=PASS, 1=FAIL, 2=SKIP.
- `review-isolation-check.sh` — §2.7 #6 ⑤ independence: non-empty `evidence_tool_calls` (no rubber-stamp), correlated handoff `stripped` includes `producer_reasoning_trace` + `producer_verdict_self_report`, no `passed` in `attempt_history`. Exit 0=PASS, 1=FAIL, 2=SKIP.
- `verifier-checklist-check.sh` — §2.4 + §2.5 B3 DECIDABLE/SOFT discipline: every DECIDABLE criterion a handoff claims satisfied is backed by a passed DECIDABLE `node_test_loop` on the same `from_node` (join on `node`+`verdict_type`+`passed`, not on a `criterion` name). Exit 0=PASS, 1=FAIL, 2=SKIP.

## Harness wiring

The harness scripts take arguments / glob (they do NOT list fixtures explicitly):

- `scripts/regression-test.sh --baseline <old.json> --new-trace <new.jsonl>` — compares a baseline trace to a new trace. The Δ (old lets a defect through, new catches it) is the headline ⑤ feasibility proof. Documented invocations:
  ```bash
  # C4: old chain ships the 2nd-caller defect; NEW graph ⑤ catches it before any human checkpoint.
  scripts/regression-test.sh \
    --baseline tests/fixtures/traces/example-C4-old-chain-lets-defect-through.jsonl \
    --new-trace tests/fixtures/traces/example-C4-new-graph-review-agent-catches.jsonl

  # C10: old chain ships the cross-file inventory.go:410 defect; NEW graph ⑤ catches it.
  scripts/regression-test.sh \
    --baseline tests/fixtures/traces/example-C10-old-chain-lets-defect-through.jsonl \
    --new-trace tests/fixtures/traces/example-C10-cross-file-soft-⑤-catches.jsonl
  ```
- `scripts/stability-test.sh` — globs `$TRACES_DIR/$PROJECT/*-${WORKFLOW}-*.jsonl`; no fixture list to edit.
- `scripts/score-trace.sh` — runs the 3 contract scorers on a single trace (the contract-gates loop).

The standalone new-graph fixtures (C2 / C3 / C5 / C9) are not old-vs-new pairs — they are verified directly with the 3 scorers per the table above (C2/C3/C9 PASS all 3; C5 FAILs `review-isolation-check.sh`).
