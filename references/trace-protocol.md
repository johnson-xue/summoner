# Summoner Trace Protocol

> Based on AI Agent evaluation methodology — structured execution traces enable automated quality assessment and regression testing.

## Purpose

Capture the complete execution trajectory of each Summoner workflow in a machine-readable format (JSONL) to enable:
1. **Deterministic scoring** — automated quality checks via scripts
2. **Regression testing** — detect capability degradation after model upgrades or prompt changes
3. **Root cause analysis** — post-mortem debugging when workflows fail
4. **Performance profiling** — token usage, latency, tool call patterns

## Trace File Format

Each workflow session generates a trace file: `$HOME/.claude/plugins/summoner/traces/{project-name}/{date}-{workflow}-{session-id}.jsonl`

### JSONL Schema

Every line is a JSON object with a `type` field:

```jsonl
{"type":"session_start","timestamp":"2026-06-23T10:00:00Z","workflow":"fix","session_id":"abc123","project":"antia-server","model":"claude-opus-4-8"}
{"type":"phase_start","timestamp":"2026-06-23T10:00:05Z","phase":0,"name":"memory","skill":null}
{"type":"memory_query","timestamp":"2026-06-23T10:00:06Z","features":{"error_codes":["SC_ErrInnerLogic"],"modules":["task"],"keywords":["nil pointer"]},"matches":3}
{"type":"memory_result","timestamp":"2026-06-23T10:00:07Z","patterns":[{"name":"config-chain-break","summary":"...","hits":5}]}
{"type":"phase_end","timestamp":"2026-06-23T10:00:10Z","phase":0,"status":"completed","artifacts":["patterns: 3 matches"]}
{"type":"checkpoint","timestamp":"2026-06-23T10:00:10Z","phase":0,"user_input":"continue"}
{"type":"phase_start","timestamp":"2026-06-23T10:00:12Z","phase":1,"name":"diagnose","skill":"antia-debug"}
{"type":"tool_call","timestamp":"2026-06-23T10:00:15Z","tool":"Read","args":{"file_path":"player/task/task.go"},"result":"success","duration_ms":120}
{"type":"reasoning","timestamp":"2026-06-23T10:00:20Z","step":"root_cause_analysis","content":"发现 line 234 未检查 p.Data 是否为 nil","confidence":0.95}
{"type":"tool_call","timestamp":"2026-06-23T10:00:25Z","tool":"Bash","args":{"command":"grep -n 'p.Data' task.go"},"result":"success","duration_ms":50}
{"type":"conclusion","timestamp":"2026-06-23T10:00:30Z","phase":1,"root_cause":"nil pointer dereference at task.go:234","fix_approach":"add nil check before access"}
{"type":"phase_end","timestamp":"2026-06-23T10:00:35Z","phase":1,"status":"completed","artifacts":["root_cause: task.go:234 nil pointer"]}
{"type":"checkpoint","timestamp":"2026-06-23T10:00:35Z","phase":1,"user_input":"continue"}
{"type":"phase_start","timestamp":"2026-06-23T10:00:40Z","phase":3,"name":"fix","skill":"freeform"}
{"type":"tool_call","timestamp":"2026-06-23T10:00:45Z","tool":"Edit","args":{"file_path":"task.go","old_string":"...","new_string":"..."},"result":"success","duration_ms":80}
{"type":"phase_end","timestamp":"2026-06-23T10:00:50Z","phase":3,"status":"completed","artifacts":["edited: task.go"]}
{"type":"checkpoint","timestamp":"2026-06-23T10:00:50Z","phase":3,"user_input":"continue"}
{"type":"phase_start","timestamp":"2026-06-23T10:00:55Z","phase":4,"name":"verify","skill":"antia-test"}
{"type":"tool_call","timestamp":"2026-06-23T10:01:00Z","tool":"Bash","args":{"command":"go test ./..."},"result":"success","duration_ms":3500}
{"type":"phase_end","timestamp":"2026-06-23T10:01:05Z","phase":4,"status":"completed","artifacts":["tests: all passed"]}
{"type":"checkpoint","timestamp":"2026-06-23T10:01:05Z","phase":4,"user_input":"done"}
{"type":"post_game_review","timestamp":"2026-06-23T10:01:10Z","review_type":4,"answers":{"q1":"5","q2":"diagnose","q3":"none","q4":"production-ready"}}
{"type":"session_end","timestamp":"2026-06-23T10:01:15Z","status":"completed","total_duration_ms":75000,"total_phases":5,"skipped_phases":1}
```

## Event Types

| Type | When | Required Fields |
|------|------|-----------------|
| `session_start` | Workflow begins | `workflow`, `session_id`, `project`, `model` |
| `session_end` | Workflow completes/stops | `status` (completed/stopped/failed), `total_duration_ms` |
| `phase_start` | Phase begins | `phase` (0-5), `name`, `skill` |
| `phase_end` | Phase completes | `phase`, `status`, `artifacts` |
| `checkpoint` | User response at checkpoint | `phase`, `user_input` (continue/skip/done/recall/stop) |
| `tool_call` | Any tool invoked | `tool`, `args`, `result`, `duration_ms` |
| `reasoning` | AI reasoning step | `step`, `content`, `confidence` (optional) |
| `conclusion` | Phase conclusion | `phase`, summary fields (varies by phase) |
| `memory_query` | Memory DB queried | `features`, `matches` |
| `memory_result` | Patterns retrieved | `patterns` |
| `post_game_review` | Review completed | `review_type`, `answers` |
| `error` | Any error occurs | `phase`, `message`, `recoverable` |

## Graph-Mode Event Types

These events appear when a plan carries a `summoner-task-graph` block (§node-contract). They are emitted by the walker (`cmd/summoner-walker`) and SKILL.md; scorers join on `envelope_id`.

### `handoff`
Typed envelope produced by a node's ④ step. Fields:
- `envelope_id` (string, required) — e.g. `h-001`. Correlation key shared with the `review_verdict` that reviews it (§2.2.1). `h-000` is reserved for the bootstrap envelope.
- `from_node` (string, required) — producer node id; `phase0` for the bootstrap.
- `to_node` (string, required) — consumer node id.
- `label` (string, required) — human-facing verb from the graph node (M9).
- `artifacts` (array of strings, required, non-empty) — validated product paths / file:line refs.
- `exit_criteria` (array, required) — each entry `{name, verdict_type: "DECIDABLE"|"SOFT", pin?, grep_pattern?}`. SOFT SHOULD carry `grep_pattern`.
- `factual_claim` (string, required) — one line, fact only, NO producer reasoning.
- `attempt_history` (array, required) — each entry `{node, attempts, verifier}` only; MUST NOT carry `passed`.
- `budget_remaining` (object, required) — `{graph_turns_left, token_budget_left}`.
- `stripped` (array, required) — intentionally-dropped fields; MUST include `producer_reasoning_trace` AND `producer_verdict_self_report`.

Allow-list for `handoff` events: only the fields above. A `handoff` carrying `producer_reasoning_trace`, `handoff_note`, or `passed` = producer-reasoning leak (scorer FAIL). (`passed` is legitimate on `node_test_loop`, NOT on `handoff` — the filter is per-event-type.)

### `review_verdict` (standalone event, §2.2.1)
Emitted by the ⑤ Review-agent. Fields:
- `envelope_id` (string, required) — joins to the `handoff` it reviews.
- `node` (string, required) — the node whose ④ was reviewed.
- `reviewer` (string, required) — e.g. `review-agent:fix`.
- `verdict` (string, required) — `PASS` | `NEEDS-FIX`.
- `findings` (array) — on NEEDS-FIX: each `{file, line?, issue, fix}`.
- `evidence_tool_calls` (array of strings, required, NON-EMPTY) — the reviewer's OWN Read/grep/Bash invocations (invariant #6; empty = rubber-stamp = FAIL).

### `node_test_loop`
Node-internal ③ verifier result. Fields:
- `node` (string, required), `label` (string, required).
- `criterion` (string, required) — the exit-criteria name tested.
- `passed` (boolean, required) — legitimate HERE (not on `handoff`).
- `exhausted` (boolean) — `true` when `max_inner_turns` hit (§5).

### `node_turn`
Walker directive-to-trace echo. Fields:
- `node` (string, required), `label` (string, required), `attempt` (int, required).
- `step` (string, required) — e.g. `review-scheduled`.
- `walker_directive` (string, required) — the raw directive, e.g. `RUN_NODE id=fix attempt=2 snapshot=before_②` or `RUN_REVIEW envelope_id=h-002`.

### `node_review_retry` (same-node ⑤ NEEDS-FIX, §2.1 clarification)
Emitted by the walker when ⑤ returns NEEDS-FIX and `from_node == to_node` (node-internal retry). Fields:
- `envelope_id` (string, required), `from_node` (string, required), `reason` (string, required), `findings` (array), `executor` (string, required, `"walker"`).
- Does NOT increment the global back-edge counter (bounded by `max_inner_turns`).

### `handoff_reject` (cross-node reject)
Emitted by the walker on a cross-node ⑤ NEEDS-FIX or ① Ingest reject. Fields:
- `envelope_id` (string, required), `from_node` (string, required), `reason` (string, required), `findings` (array), `executor` (string, required, `"walker"`).
- Increments the global `max_back_edges_total` counter; 3× same-finding escalates to checkpoint.

## Implementation

### In SKILL.md

```markdown
## Trace Output

Every workflow execution MUST write a trace file. Use the shared trace writer:

```bash
TRACE_FILE="$HOME/.claude/plugins/summoner/traces/${PROJECT}/${DATE}-${WORKFLOW}-${SESSION_ID}.jsonl"
mkdir -p "$(dirname "$TRACE_FILE")"

# Helper function
trace() {
  local type="$1"
  shift
  echo "{\"type\":\"$type\",\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",${*}}" >> "$TRACE_FILE"
}

# Usage
trace session_start "\"workflow\":\"fix\",\"session_id\":\"$SESSION_ID\",\"project\":\"$PROJECT\""
trace phase_start "\"phase\":1,\"name\":\"diagnose\",\"skill\":\"antia-debug\""
trace tool_call "\"tool\":\"Read\",\"args\":{\"file_path\":\"task.go\"},\"result\":\"success\",\"duration_ms\":120"
```

Or use the Go hook helper (if available):

```go
trace.Event("session_start", map[string]interface{}{
    "workflow": "fix",
    "session_id": sessionID,
    "project": projectName,
})
```
```

### Trace Activation

Traces are always written by default. To disable for a session (e.g., privacy concerns):

```bash
export SUMMONER_NO_TRACE=1
```

## Trace Retention

- **Local storage only** — traces never leave the user's machine
- **Retention policy**: Last 30 days OR last 100 sessions per project (whichever is larger)
- **Cleanup**: `~/.claude/plugins/summoner/scripts/cleanup-traces.sh` (cron-able)
- **Privacy**: User can `rm -rf ~/.claude/plugins/summoner/traces/` anytime

## Integration with Scoring System

See `scoring-system.md` for how traces feed into automated quality checks.

## Example: Extracting Tool Call Sequence

```bash
# Extract all tool calls from a trace
jq -r 'select(.type == "tool_call") | "\(.tool) → \(.result) (\(.duration_ms)ms)"' trace.jsonl

# Output:
# Read → success (120ms)
# Bash → success (50ms)
# Edit → success (80ms)
# Bash → success (3500ms)
```

## Example: Checking for Red Flags

```bash
# Check if Phase 1 (diagnose) was skipped
if ! grep -q '"phase":1,"status":"completed"' trace.jsonl; then
  echo "⚠️  Phase 1 (diagnose) was skipped — violates iron law"
fi

# Check if tests passed in Phase 4
if grep -q '"phase":4' trace.jsonl && ! grep -q '"tests: all passed"' trace.jsonl; then
  echo "⚠️  Phase 4 (verify) did not confirm test passage"
fi
```

## Related

- `scoring-system.md` — Automated quality scoring based on traces
- `checkpoint-protocol.md` — Checkpoint output format (user-facing)
- `post-game-review.md` — Post-game review questionnaire (feeds into traces)
