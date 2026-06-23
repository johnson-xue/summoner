# Baseline Management and Regression Testing Scripts

## Prerequisites

### Required Dependencies

All baseline and regression testing scripts require `jq` (JSON processor) to be installed:

**macOS:**
```bash
brew install jq
```

**Ubuntu/Debian:**
```bash
sudo apt-get install jq
```

**Other platforms:**
See https://stedolan.github.io/jq/download/

### Verify Installation

```bash
jq --version
# Expected: jq-1.6 or higher
```

## Scripts Overview

### 1. create-baseline.sh

Create a golden reference baseline from a successful workflow trace.

**Usage:**
```bash
./scripts/create-baseline.sh \
  --trace <trace-file.jsonl> \
  --name <baseline-name> \
  --category <bugfix|feature|ops>
```

**Example:**
```bash
./scripts/create-baseline.sh \
  --trace traces/antia-server/2026-06-23-fix-abc123.jsonl \
  --name fix-nil-pointer-in-task \
  --category bugfix
```

**Output:**
- Creates `baselines/{project}/{name}.baseline.json`
- Extracts: phase sequence, tool sequence, scores, duration
- Prompts if trace has no scoring result

**Baseline Format:**
```json
{
  "name": "fix-nil-pointer-in-task",
  "category": "bugfix",
  "workflow": "fix",
  "project": "antia-server",
  "model": "claude-opus-4-8",
  "expected_phases": [0, 1, 3, 4, 5],
  "expected_tool_sequence": ["Read", "Bash", "Edit", "Bash"],
  "expected_scores": {"P0": 95, "P0_max": 100},
  "expected_artifacts": ["root_cause: task.go:234", "edited: task.go"],
  "expected_duration_ms": 75000,
  "trace_reference": "traces/...",
  "created_at": "2026-06-23T10:05:00Z",
  "created_by": "human",
  "approved": true
}
```

---

### 2. regression-test.sh

Compare a new trace against a baseline to detect regressions.

**Usage:**
```bash
./scripts/regression-test.sh \
  --baseline <baseline.json> \
  --new-trace <new-trace.jsonl> \
  [--output <result.json>]
```

**Example:**
```bash
./scripts/regression-test.sh \
  --baseline baselines/antia-server/fix-nil-pointer-in-task.baseline.json \
  --new-trace traces/antia-server/2026-06-25-fix-xyz789.jsonl
```

**Checks:**
1. **Phase Coverage** — Must execute same phases as baseline
2. **Tool Sequence Similarity** — ≥80% similarity (Jaccard index)
3. **Score Comparison** — P0 score within -5 points tolerance
4. **Duration** — Within +20% of baseline

**Exit Codes:**
- `0` — Pass (no regressions)
- `1` — Fail (regressions detected)

**Output Example:**
```
📊 Regression Test: fix-nil-pointer-in-task

🔍 Checking phase coverage...
  ✅ MATCH (phases: [0,1,3,4,5])

🔍 Checking tool sequence similarity...
  ✅ MATCH (100% similarity)

🔍 Checking scores...
  ⚠️  MINOR DEGRADATION (expected: 95, actual: 90, delta: -5)

🔍 Checking duration...
  ✅ ACCEPTABLE (expected: 75000ms, actual: 78000ms, delta: 4%)

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
⚠️  PASS with warnings — 1 minor issue(s)
   - P0 score dropped by 5 points (within 5-point tolerance)
```

---

### 3. stability-test.sh

Execute the same workflow N times and measure consistency.

**Usage:**
```bash
./scripts/stability-test.sh \
  --workflow <fix|new|debug|ops|review> \
  --input <task-description> \
  --runs <N> \
  --tolerance <0-100> \
  [--project <name>]
```

**Example:**
```bash
./scripts/stability-test.sh \
  --workflow fix \
  --input "修复 task.go:234 nil pointer" \
  --runs 5 \
  --tolerance 0 \
  --project antia-server
```

**Tolerance Guidelines:**
| Workflow Type | Recommended Tolerance | Meaning |
|---------------|----------------------|---------|
| `fix`, `debug` | **0%** | Critical decision workflows — 5/5 must pass |
| `review`, `ops` | **≤10%** | Auxiliary workflows — 4.5/5 pass |
| `new` | **≤40%** | Creative workflows — 3/5 pass |

**Output Example:**
```
🔄 Stability Test: fix workflow
Runs: 5
Tolerance: 0%

📊 Analyzing 5 traces...

Run 1: 95/100 ✅ PASS
Run 2: 92/100 ✅ PASS
Run 3: 95/100 ✅ PASS
Run 4: 88/100 ✅ PASS
Run 5: 95/100 ✅ PASS

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Pass Rate: 100% (5/5)
Score Range: 88-95 (avg: 93, stdev: 2.8)

✅ STABLE (pass rate 100% meets 100% requirement)
```

**Note:** Current implementation is a simulation that analyzes existing traces. Full integration requires:
1. Claude Code API to trigger workflow execution N times
2. Automatic trace collection after each run
3. Timeout and error handling for long-running executions

---

## Workflow

### Step 1: Create Baseline (One-Time)

After a successful workflow execution that you've manually verified:

```bash
# 1. Score the trace
./scripts/score-trace.sh \
  --trace traces/antia-server/2026-06-23-fix-abc123.jsonl \
  --priority P0

# 2. If score ≥80, create baseline
./scripts/create-baseline.sh \
  --trace traces/antia-server/2026-06-23-fix-abc123.jsonl \
  --name fix-nil-pointer-in-task \
  --category bugfix
```

### Step 2: Regression Testing (After Changes)

After modifying prompts, upgrading models, or changing workflow logic:

```bash
# Execute the same task again (manually or via CI)
# This generates: traces/antia-server/2026-06-25-fix-xyz789.jsonl

# Run regression test
./scripts/regression-test.sh \
  --baseline baselines/antia-server/fix-nil-pointer-in-task.baseline.json \
  --new-trace traces/antia-server/2026-06-25-fix-xyz789.jsonl \
  --output regression-result.json
```

### Step 3: Stability Testing (Periodic)

Every N weeks or after major changes:

```bash
./scripts/stability-test.sh \
  --workflow fix \
  --input "修复 task.go:234 nil pointer" \
  --runs 5 \
  --tolerance 0
```

---

## CI Integration

### GitHub Actions Example

```yaml
name: Summoner Regression Tests

on:
  pull_request:
    paths:
      - 'skills/**'
      - 'commands/**'
      - 'references/**'

jobs:
  regression:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      
      - name: Install jq
        run: sudo apt-get install -y jq
      
      - name: Run Baseline Regression Tests
        run: |
          for baseline in baselines/**/*.baseline.json; do
            echo "Testing baseline: $(basename $baseline)"
            
            # TODO: Trigger workflow execution to generate new trace
            # For now, test against reference trace in baseline
            REFERENCE_TRACE=$(jq -r '.trace_reference' "$baseline")
            
            ./scripts/regression-test.sh \
              --baseline "$baseline" \
              --new-trace "$REFERENCE_TRACE" \
              --output "results/$(basename $baseline .baseline.json).json"
          done
      
      - name: Check Results
        run: |
          FAIL_COUNT=$(find results/ -name "*.json" -exec jq -r 'select(.status=="fail") | .baseline' {} \; | wc -l)
          if [[ $FAIL_COUNT -gt 0 ]]; then
            echo "::error::$FAIL_COUNT baseline(s) failed regression tests"
            exit 1
          fi
      
      - name: Upload Results
        uses: actions/upload-artifact@v3
        with:
          name: regression-results
          path: results/
```

---

## Troubleshooting

### Error: "Cannot extract project name from trace"

**Cause:** Trace file is missing `session_start` event or `project` field.

**Fix:** Ensure trace contains:
```jsonl
{"type":"session_start","timestamp":"...","workflow":"fix","project":"your-project","model":"..."}
```

### Error: "No scoring result in new trace"

**Cause:** Trace was not scored before regression testing.

**Fix:** Run scoring first:
```bash
./scripts/score-trace.sh --trace <trace.jsonl> --priority P0
```

### Warning: "Not enough trace files found"

**Cause:** Stability test requires N traces but found fewer.

**Fix:** Execute the workflow multiple times to generate traces, or reduce `--runs` parameter.

---

## Limitations

### Current Implementation

1. **Manual workflow execution:** Scripts assume traces already exist; cannot trigger workflow execution automatically
2. **Single-machine:** Traces stored locally; no distributed execution support
3. **Limited metrics:** Only P0 scores tracked; P1/P2 support TODO
4. **No trace streaming:** Must wait for complete trace file before analysis

### Future Enhancements

1. **Claude Code API integration** for automated workflow triggering
2. **Distributed trace storage** (e.g., S3, shared NFS) for team collaboration
3. **Real-time trace analysis** via streaming JSONL parser
4. **Advanced similarity metrics** (LCS algorithm for tool sequence comparison)
5. **Multi-priority scoring** (P0 + P1 + P2 combined regression check)

---

## Related Documentation

- `../references/trace-protocol.md` — Trace file format specification
- `../references/scoring-system.md` — Scoring framework and evaluation criteria
- `../docs/PROPOSAL-trace-and-scoring.md` — Design rationale and architecture
