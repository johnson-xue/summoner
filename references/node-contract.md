# The Node Contract

A Summoner node is a closed-loop agent running **① Ingest+Validate → ⓪ Pre-Work snapshot → ② Work → ③ Test → ④ Handoff**, after which the **walker** schedules a separate-context **⑤ Review-agent** (`RUN_REVIEW`). ⑤ is NOT a 5th inline step of the node — it is walker-scheduled (§2.8/B4).

## Steps

| Step | Does | Decidable? |
|---|---|---|
| ① Ingest+Validate | Receive the upstream handoff envelope; check declared artifacts/fields/exit-criteria present + well-formed. Reject → cross-node `handoff_reject` back-edge. | Yes (schema) |
| ⓪ Pre-Work snapshot | (mutating nodes only) snapshot working tree so ③-retry or ⑤-back-edge re-runs ② on a clean tree. Owner = SKILL.md via `node-snapshot.sh`; walker signals `snapshot:`/`restore:` flags, never touches the tree (M2). | n/a |
| ② Work | Execute the closed-loop task. | n/a |
| ③ Test | Run a machine-decidable node-internal verifier. FAIL → restore ⓪ then retry ② (bounded by `max_inner_turns`). | Yes |
| ④ Handoff | Emit a clean, minimal, typed envelope: artifact paths + `exit_criteria` (each `{name, verdict_type, pin?, grep_pattern?}`) + one-line `factual_claim` + `attempt_history` (`{node, attempts, verifier}` only, NO `passed`) + `budget_remaining` + `stripped` (incl. `producer_reasoning_trace` + `producer_verdict_self_report`). | Yes (schema) |
| ⑤ Review-agent | Separate-context; independently re-derives findings with own Read/grep/Bash against artifact paths; returns `review_verdict` (standalone event keyed by `envelope_id`, non-empty `evidence_tool_calls`). Walker-scheduled, not node-spawned. | Decidable where re-derivation yields objective signals; pinned otherwise |

## Back-edge semantics (C2)

- **Same-node ⑤ NEEDS-FIX** (e.g. ⑤ on `fix` → `fix`): walker emits `node_review_retry`, bounded by `max_inner_turns`, no checkpoint, does NOT increment the global `max_back_edges_total`.
- **Cross-node ⑤ NEEDS-FIX / ① reject**: walker emits `handoff_reject`, increments global counter; 3× same-finding escalates to checkpoint.

## Budget (H6)

Precedence: `max_inner_turns` → 3× same-finding → `max_back_edges_total` → `max_graph_turns`. Same-node ⑤ does not increment the global counter; cross-node does.

## Invariants

1. Phase 1 (diagnose) is iron law — `conditional_edges`/back-edges may not bypass it.
2. No auto-advance past a checkpoint — the human retains the *flow* decision; ⑤ judges *quality* only.
3. No hardcoded project/domain names.
4. Agents never call other agents — the walker routes all back-edges.
5. Post-game review is mandatory.
6. Review-agent independence is enforced by tool-use (non-empty `evidence_tool_calls`, `stripped` includes producer reasoning) — not by a prompt promise.
