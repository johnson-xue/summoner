# Summoner Scoring System

> Based on AI Agent evaluation best practices — automated quality assessment using deterministic scorers + LLM-as-Judge + human calibration.

## Purpose

Provide **quantitative quality metrics** for each workflow execution to:
1. Detect capability regression after model upgrades or prompt changes
2. Identify low-quality executions before they reach production
3. Build confidence in AI-generated output
4. Enable A/B testing of different workflow configurations

## Scoring Framework

### Three-Tier Scorer Priority

**1. Deterministic Scorers** (highest priority)  
Script-based checks for objective criteria — compiled/tested/formatted/no-errors.

**2. Rubric Scorers** (LLM-as-Judge)  
Semantic evaluation for subjective criteria — code quality, reasoning clarity, error handling.

**3. Human Calibration** (ground truth)  
Periodic manual review to validate scorer accuracy and update rubrics.

**Iron Law:** If a deterministic scorer can verify something, never use LLM-as-Judge for it.

## Scoring Dimensions (P0 → P2)

### P0: Functional Correctness + Robustness

| Dimension | Weight | Pass Threshold | Scorer Type |
|-----------|--------|----------------|-------------|
| **Iron Law Compliance** | 30 | Must pass | Deterministic |
| **Build/Compile Success** | 20 | Must pass | Deterministic |
| **Test Pass Rate** | 20 | 100% | Deterministic |
| **No New Lint Errors** | 10 | 0 new | Deterministic |
| **Error Handling Present** | 10 | ≥80% | Rubric |
| **Edge Case Coverage** | 10 | ≥70% | Rubric |

**Total: 100 points**  
**Pass Threshold: ≥ 80 points**

### P1: Process Quality + Efficiency

| Dimension | Weight | Pass Threshold | Scorer Type |
|-----------|--------|----------------|-------------|
| **Reasoning Chain Complete** | 30 | ≥80% | Rubric |
| **Tool Call Efficiency** | 20 | No redundant calls | Deterministic |
| **Token Usage** | 20 | Within budget | Deterministic |
| **Phase Skip Justified** | 15 | Valid reason given | Rubric |
| **Checkpoint Protocol** | 15 | All checkpoints present | Deterministic |

### P2: Experience + Alignment

| Dimension | Weight | Scorer Type |
|-----------|--------|-------------|
| **Verbosity Score** | 40 | Rubric |
| **Clarity Score** | 30 | Rubric |
| **Tone Alignment** | 30 | Rubric |

## Deterministic Scorers

### 1. Iron Law Compliance (`scorers/deterministic/iron-law-check.sh`)

```bash
#!/bin/bash
# Checks: Phase 1 (diagnose) must complete in /summoner:fix and /summoner:debug

TRACE_FILE="$1"
WORKFLOW=$(jq -r 'select(.type=="session_start") | .workflow' "$TRACE_FILE" | head -1)

if [[ "$WORKFLOW" == "fix" ]] || [[ "$WORKFLOW" == "debug" ]]; then
  if ! jq -e 'select(.type=="phase_end" and .phase==1 and .status=="completed")' "$TRACE_FILE" > /dev/null; then
    echo "FAIL: Phase 1 (diagnose) was skipped — violates iron law"
    exit 1
  fi
fi

echo "PASS: Iron law compliant"
exit 0
```

**Scoring:**
- Pass → 30 points
- Fail → 0 points (auto-fail entire eval)

### 2. Build Success (`scorers/deterministic/build-check.sh`)

```bash
#!/bin/bash
# Checks: Compilation/build succeeded (looks for successful build tool call)

TRACE_FILE="$1"

# Check for successful build/compile/test commands
if jq -e '
  select(.type=="tool_call" and 
         (.tool=="Bash") and 
         (.args.command | test("(make|go build|npm run build|cargo build)")) and
         .result=="success")
' "$TRACE_FILE" > /dev/null; then
  echo "PASS: Build succeeded"
  exit 0
fi

# If no build tool was called, check for Edit without subsequent build errors
if jq -e 'select(.type=="tool_call" and .tool=="Edit")' "$TRACE_FILE" > /dev/null; then
  if jq -e 'select(.type=="error" and (.message | test("(compile|build|syntax)")))' "$TRACE_FILE" > /dev/null; then
    echo "FAIL: Build errors detected after edits"
    exit 1
  fi
  echo "PASS: No build errors detected"
  exit 0
fi

echo "SKIP: No build/edit operations found"
exit 2  # Exit code 2 = skip this scorer
```

**Scoring:**
- Pass → 20 points
- Fail → 0 points
- Skip → 20 points (no penalty if not applicable)

### 3. Test Pass Rate (`scorers/deterministic/test-pass-rate.sh`)

```bash
#!/bin/bash
# Checks: All tests passed in Phase 4 (verify)

TRACE_FILE="$1"

# Extract test tool calls from Phase 4
TEST_CALLS=$(jq -r '
  select(.type=="tool_call" and
         (.args.command | test("(test|go test|npm test|pytest|cargo test)")) and
         (.timestamp >= (
           [.. | select(.type=="phase_start" and .phase==4)] | last | .timestamp
         )))
' "$TRACE_FILE")

if [[ -z "$TEST_CALLS" ]]; then
  echo "SKIP: Phase 4 (verify) not executed or no tests run"
  exit 2
fi

# Check for test failures
if echo "$TEST_CALLS" | jq -e 'select(.result=="error" or (.args.command | test("FAIL|Error|failed")))' > /dev/null; then
  echo "FAIL: Tests failed in Phase 4"
  exit 1
fi

echo "PASS: All tests passed"
exit 0
```

**Scoring:**
- Pass → 20 points
- Fail → 0 points
- Skip → 10 points (partial credit if verify phase skipped with valid reason)

### 4. Lint Check (`scorers/deterministic/lint-check.sh`)

```bash
#!/bin/bash
# Checks: No new lint errors introduced

TRACE_FILE="$1"

# Look for lint tool calls
LINT_CALLS=$(jq -r '
  select(.type=="tool_call" and
         (.args.command | test("(lint|golangci-lint|eslint|pylint|clippy)")))
' "$TRACE_FILE")

if [[ -z "$LINT_CALLS" ]]; then
  echo "SKIP: No lint checks performed"
  exit 2
fi

# Check for lint errors
if echo "$LINT_CALLS" | jq -e 'select(.result=="error")' > /dev/null; then
  echo "FAIL: Lint errors detected"
  exit 1
fi

echo "PASS: No lint errors"
exit 0
```

**Scoring:**
- Pass → 10 points
- Fail → -10 points (penalty)
- Skip → 10 points (no penalty if not applicable)

### 5. Tool Call Efficiency (`scorers/deterministic/tool-efficiency.sh`)

```bash
#!/bin/bash
# Checks: No redundant tool calls (same tool + args called multiple times)

TRACE_FILE="$1"

# Extract all tool calls and check for duplicates
DUPLICATES=$(jq -r '
  select(.type=="tool_call") | 
  "\(.tool)|\(.args | tostring)"
' "$TRACE_FILE" | sort | uniq -d)

if [[ -n "$DUPLICATES" ]]; then
  DUPLICATE_COUNT=$(echo "$DUPLICATES" | wc -l)
  echo "FAIL: $DUPLICATE_COUNT redundant tool calls detected"
  exit 1
fi

echo "PASS: No redundant tool calls"
exit 0
```

**Scoring (P1):**
- Pass → 20 points
- Fail → 10 points (half credit)

## Rubric Scorers (LLM-as-Judge)

### 6. Error Handling Quality (`scorers/rubric/error-handling.yaml`)

```yaml
name: error-handling
description: Evaluates if code changes include proper error handling
model: claude-haiku-4-5  # Use fast model for cost efficiency
prompt: |
  Review the code changes in this workflow trace and rate error handling quality.
  
  Changes:
  {{edited_files}}
  
  Criteria:
  1. All error returns are checked (not ignored)
  2. Errors are wrapped with context (e.g., fmt.Errorf, errors.Wrap)
  3. Edge cases are handled (nil checks, bounds checks)
  4. Panics are avoided in production code paths
  
  Rating scale:
  - 10: Excellent — all criteria met
  - 8: Good — minor gaps (1 missing check)
  - 6: Acceptable — moderate gaps (2-3 missing checks)
  - 4: Poor — significant gaps (4+ missing checks)
  - 0: Fails — no error handling present
  
  Respond with ONLY a JSON object:
  {
    "score": <0-10>,
    "reasoning": "<1-2 sentences explaining the score>",
    "missing_checks": ["<specific locations where error handling is missing>"]
  }

scoring:
  max_points: 10
  pass_threshold: 8
```

### 7. Reasoning Chain Quality (`scorers/rubric/reasoning-quality.yaml`)

```yaml
name: reasoning-quality
description: Evaluates completeness and clarity of AI reasoning in Phase 1 (diagnose)
model: claude-haiku-4-5
prompt: |
  Review the Phase 1 (diagnose) reasoning steps from this trace:
  
  {{phase_1_reasoning}}
  
  Criteria:
  1. Root cause clearly identified (not just symptoms)
  2. Evidence cited (file:line references, log excerpts)
  3. Hypothesis-driven approach (ruled out alternatives)
  4. Confidence level justified
  
  Rating scale:
  - 30: Excellent — all criteria met, clear root cause with evidence
  - 24: Good — minor gaps (e.g., no confidence level)
  - 18: Acceptable — identified cause but weak evidence
  - 12: Poor — vague diagnosis or no root cause
  - 0: Fails — no diagnosis or wrong direction
  
  Respond with ONLY a JSON object:
  {
    "score": <0-30>,
    "reasoning": "<1-2 sentences>",
    "missing_elements": ["<what was missing>"]
  }

scoring:
  max_points: 30
  pass_threshold: 24
```

## Scoring Execution

### Running Scorers

```bash
# Run all P0 scorers on a trace
~/.claude/plugins/summoner/scripts/score-trace.sh \
  --trace traces/antia-server/2026-06-23-fix-abc123.jsonl \
  --priority P0

# Output:
# ✅ iron-law-check: 30/30
# ✅ build-check: 20/20
# ✅ test-pass-rate: 20/20
# ⊘ lint-check: 10/10 (skipped, no penalty)
# 🤖 error-handling: 8/10
# 🤖 edge-case-coverage: 7/10
# 
# Total: 95/100 (PASS — threshold: 80)
```

### Score Storage

Scores are appended to the trace file as a final event:

```jsonl
{"type":"scoring_result","timestamp":"2026-06-23T10:05:00Z","priority":"P0","total_score":95,"max_score":100,"pass":true,"details":[{"scorer":"iron-law-check","score":30,"max":30,"status":"pass"},{"scorer":"build-check","score":20,"max":20,"status":"pass"},{"scorer":"test-pass-rate","score":20,"max":20,"status":"pass"},{"scorer":"lint-check","score":10,"max":10,"status":"skip"},{"scorer":"error-handling","score":8,"max":10,"status":"pass"},{"scorer":"edge-case-coverage","score":7,"max":10,"status":"pass"}]}
```

## Baseline Management

### Creating a Baseline

After a successful workflow execution that passes manual review:

```bash
~/.claude/plugins/summoner/scripts/create-baseline.sh \
  --trace traces/antia-server/2026-06-23-fix-abc123.jsonl \
  --name "fix-nil-pointer-in-task" \
  --category "bugfix"

# Creates: baselines/antia-server/fix-nil-pointer-in-task.baseline.json
```

**Baseline Contents:**
```json
{
  "name": "fix-nil-pointer-in-task",
  "category": "bugfix",
  "workflow": "fix",
  "expected_phases": [0, 1, 3, 4, 5],
  "expected_tool_sequence": ["Read", "Bash", "Edit", "Bash"],
  "expected_score": {"P0": 95, "P1": 88},
  "expected_artifacts": ["edited_files", "test_results"],
  "trace_reference": "traces/antia-server/2026-06-23-fix-abc123.jsonl",
  "created_at": "2026-06-23T10:05:00Z",
  "created_by": "human",
  "approved": true
}
```

### Regression Testing Against Baseline

```bash
# Run the same task again and compare to baseline
~/.claude/plugins/summoner/scripts/regression-test.sh \
  --baseline baselines/antia-server/fix-nil-pointer-in-task.baseline.json \
  --new-trace traces/antia-server/2026-06-25-fix-xyz789.jsonl

# Output:
# 📊 Regression Test: fix-nil-pointer-in-task
# 
# Phase Coverage: ✅ MATCH (expected: [0,1,3,4,5], actual: [0,1,3,4,5])
# Tool Sequence:  ✅ MATCH (LCS similarity: 100%)
# P0 Score:       ⚠️  DEGRADED (expected: 95, actual: 85, delta: -10)
#   - test-pass-rate dropped from 20 to 10 (1 test failed)
# P1 Score:       ✅ MATCH (expected: 88, actual: 90, delta: +2)
# 
# Overall: ⚠️  REGRESSION DETECTED
```

## Stability Testing

Execute the same workflow N times and measure consistency:

```bash
~/.claude/plugins/summoner/scripts/stability-test.sh \
  --workflow fix \
  --input "修复 task.go:234 的 nil pointer" \
  --runs 5 \
  --tolerance 0

# Output:
# 🔄 Stability Test: 5 runs
# 
# Run 1: 95/100 (PASS)
# Run 2: 92/100 (PASS)
# Run 3: 95/100 (PASS)
# Run 4: 88/100 (PASS)
# Run 5: 95/100 (PASS)
# 
# Pass Rate: 100% (5/5) — tolerance: 0%
# Score Range: 88-95 (avg: 93, stdev: 2.8)
# 
# ✅ STABLE (meets 0% tolerance for critical workflow)
```

**Tolerance Guidelines (from article):**
- **Critical decision workflows** (fix, debug): 0% tolerance (5/5 must pass)
- **Auxiliary workflows** (review): ≤10% tolerance (4.5/5)
- **Creative workflows** (new): ≤40% tolerance (3/5)

## Integration with CI

```yaml
# .github/workflows/summoner-quality-gate.yml
name: Summoner Quality Gate

on:
  pull_request:
    paths:
      - 'skills/**'
      - 'commands/**'
      - 'references/**'

jobs:
  regression-test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - name: Run Baseline Regression
        run: |
          for baseline in baselines/**/*.baseline.json; do
            ./scripts/regression-test.sh --baseline "$baseline" --new-trace "$NEW_TRACE"
          done
      - name: Check Pass Rate
        run: |
          pass_rate=$(jq '.pass_rate' regression-results.json)
          if (( $(echo "$pass_rate < 0.95" | bc -l) )); then
            echo "::error::Regression test pass rate below 95%: $pass_rate"
            exit 1
          fi
```

## Related

- `trace-protocol.md` — Trace file format and event types
- `post-game-review.md` — Human feedback integration
- `memory-chain.md` — Pattern persistence from low-scoring sessions
