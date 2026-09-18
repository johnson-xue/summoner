# Graph-Mode Phase Execution

Loaded on demand — only when the active plan artifact contains a fenced
` ```yaml summoner-task-graph ` block. (If the plan has NO such block → chain
fallback: the linear behavior in SKILL.md §3. No existing workflow changes.)

The graph walker (`summoner-walker`) is the router + bookkeeper — it does NOT
execute agents and does NOT touch the working tree (M2).

## Directive loop

1. `summoner-walker --graph <plan.md> --trace <trace.jsonl> --session <session_id> next` → read the printed JSON directive. (If `summoner-walker` is not on PATH, dev fallback: `go run ./cmd/summoner-walker ...`. Guard with `command -v summoner-walker` — like the `summoner-ctx` precedent.)

2. If the directive `kind` is `RUN_NODE`:
   - If the directive carries `snapshot` (mutating node): run `bash scripts/node-snapshot.sh save` BEFORE ②. (The walker signals intent via the flag; YOU execute the helper — single owner. The walker never touches the tree.)
   - Run the node's ① Ingest+Validate → ⓪ (if not done) → ② Work → ③ Test → ④ Handoff. The node is a closed-loop agent; ③ FAIL self-retries bounded by the node's `max_inner_turns` (no checkpoint between retries).
   - On ④, write the handoff envelope to a temp file and call `summoner-walker record --step handoff --envelope <env.json>` → walker prints a `RUN_REVIEW` directive.

3. If the directive `kind` is `RUN_REVIEW`: spawn the `review-agent` persona (`agents/review-agent.md`) with the envelope's `envelope_id` — paths + exit_criteria ONLY, NO producer reasoning (review-isolation invariant). It returns a `review_verdict`. Write the verdict + findings + evidence to temp files and call `summoner-walker record --step review_verdict --envelope_id <id> --node <nodeID> --verdict <PASS|NEEDS-FIX> --findings <findings.json> --evidence <evidence.json>`. (Evidence must be non-empty — a rubber-stamp review FAILs review-isolation-check.)

4. The walker's next directive routes the step:
   - `RUN_NODE` (next node) → loop to step 2.
   - `NODE_RETRY` (same-node ⑤ NEEDS-FIX, bounded by max_inner_turns, no checkpoint, no counter increment) → if `restore` is set, run `bash scripts/node-snapshot.sh restore` before re-running ②; loop to step 2 (no checkpoint).
   - `BACK_EDGE` (cross-node, increments max_back_edges_total) → if `restore` is set, restore; run the target node's ②; loop to step 2 (the next forward CHECKPOINT reports the round-trip).
   - `CHECKPOINT` → render `summoner-walker explain` into the checkpoint block; the human picks continue/recall/skip/done/stop (FLOW decision only — quality already gated by ⑤).
   - `HALT` → budget exhausted (max_graph_turns / total_token_budget / max_back_edges_total); surface to the human.
