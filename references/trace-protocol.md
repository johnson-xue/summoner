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
