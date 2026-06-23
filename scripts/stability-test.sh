#!/bin/bash
set -o pipefail

# stability-test.sh — Execute workflow N times and measure consistency
# Usage: stability-test.sh --workflow <fix|new|debug> --input <task-description> --runs <N> --tolerance <0-100>

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_ROOT="$(dirname "$SCRIPT_DIR")"
TRACES_DIR="$PLUGIN_ROOT/traces"

# Parse arguments
RUNS=5
TOLERANCE=0
while [[ $# -gt 0 ]]; do
  case $1 in
    --workflow)
      WORKFLOW="$2"
      shift 2
      ;;
    --input)
      INPUT="$2"
      shift 2
      ;;
    --runs)
      RUNS="$2"
      shift 2
      ;;
    --tolerance)
      TOLERANCE="$2"
      shift 2
      ;;
    --project)
      PROJECT="$2"
      shift 2
      ;;
    *)
      echo "Usage: $0 --workflow <fix|new|debug> --input <task-description> --runs <N> --tolerance <0-100> [--project <name>]"
      exit 1
      ;;
  esac
done

# Validate arguments
if [[ -z "$WORKFLOW" ]] || [[ -z "$INPUT" ]]; then
  echo "Error: --workflow and --input are required"
  exit 1
fi

if [[ "$WORKFLOW" != "fix" ]] && [[ "$WORKFLOW" != "new" ]] && [[ "$WORKFLOW" != "debug" ]] && [[ "$WORKFLOW" != "ops" ]] && [[ "$WORKFLOW" != "review" ]]; then
  echo "Error: workflow must be fix, new, debug, ops, or review"
  exit 1
fi

if [[ $RUNS -lt 2 ]] || [[ $RUNS -gt 100 ]]; then
  echo "Error: runs must be between 2 and 100"
  exit 1
fi

if [[ $TOLERANCE -lt 0 ]] || [[ $TOLERANCE -gt 100 ]]; then
  echo "Error: tolerance must be between 0 and 100 (percentage)"
  exit 1
fi

echo "🔄 Stability Test: $WORKFLOW workflow"
echo "Task: $INPUT"
echo "Runs: $RUNS"
echo "Tolerance: $TOLERANCE%"
echo ""

# Note: This is a reference implementation
# Actual execution requires integration with Summoner's workflow system
echo "⚠️  Note: This is a simulation. Real implementation requires:"
echo "   1. Integration with /summoner:$WORKFLOW command"
echo "   2. Claude Code API to trigger workflow execution"
echo "   3. Trace file collection after each run"
echo ""
echo "For now, this script demonstrates the analysis logic using existing traces."
echo ""

# Simulate: collect traces (in real implementation, would trigger N executions)
PROJECT="${PROJECT:-test-project}"
TRACE_PATTERN="$TRACES_DIR/$PROJECT/*-${WORKFLOW}-*.jsonl"
TRACES=($(ls $TRACE_PATTERN 2>/dev/null | head -n "$RUNS"))

if [[ ${#TRACES[@]} -lt "$RUNS" ]]; then
  echo "⚠️  Not enough trace files found (need $RUNS, found ${#TRACES[@]})"
  echo "   Pattern: $TRACE_PATTERN"
  echo ""
  echo "To generate traces, run /summoner:$WORKFLOW multiple times with the same input."
  echo "Then re-run this stability test."
  exit 1
fi

echo "📊 Analyzing ${#TRACES[@]} traces..."
echo ""

# Score each run
SCORES=()
PASS_COUNT=0
FAIL_COUNT=0

for i in "${!TRACES[@]}"; do
  trace="${TRACES[$i]}"
  run_num=$((i + 1))

  # Run scoring
  if score_output=$("$SCRIPT_DIR/score-trace.sh" --trace "$trace" --priority P0 2>&1); then
    # Extract score from output
    score=$(echo "$score_output" | grep "Total:" | sed -E 's/.*Total: ([0-9]+)\/([0-9]+).*/\1/')
    max_score=$(echo "$score_output" | grep "Total:" | sed -E 's/.*Total: ([0-9]+)\/([0-9]+).*/\2/')

    if [[ -n "$score" ]] && [[ "$score" -ge 80 ]]; then
      SCORES+=("$score")
      PASS_COUNT=$((PASS_COUNT + 1))
      echo "Run $run_num: $score/$max_score ✅ PASS"
    else
      SCORES+=("${score:-0}")
      FAIL_COUNT=$((FAIL_COUNT + 1))
      echo "Run $run_num: ${score:-0}/$max_score ❌ FAIL"
    fi
  else
    SCORES+=(0)
    FAIL_COUNT=$((FAIL_COUNT + 1))
    echo "Run $run_num: 0/100 ❌ FAIL (scoring error)"
  fi
done

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# Calculate statistics
PASS_RATE=$((PASS_COUNT * 100 / RUNS))
FAIL_RATE=$((100 - PASS_RATE))

echo "Pass Rate: $PASS_RATE% ($PASS_COUNT/$RUNS)"

# Score statistics
if [[ ${#SCORES[@]} -gt 0 ]]; then
  # Min score
  MIN_SCORE=${SCORES[0]}
  for score in "${SCORES[@]}"; do
    if [[ $score -lt $MIN_SCORE ]]; then
      MIN_SCORE=$score
    fi
  done

  # Max score
  MAX_SCORE=${SCORES[0]}
  for score in "${SCORES[@]}"; do
    if [[ $score -gt $MAX_SCORE ]]; then
      MAX_SCORE=$score
    fi
  done

  # Average score
  SUM=0
  for score in "${SCORES[@]}"; do
    SUM=$((SUM + score))
  done
  AVG_SCORE=$((SUM / ${#SCORES[@]}))

  # Standard deviation (simplified)
  VARIANCE_SUM=0
  for score in "${SCORES[@]}"; do
    DIFF=$((score - AVG_SCORE))
    VARIANCE_SUM=$((VARIANCE_SUM + DIFF * DIFF))
  done
  VARIANCE=$((VARIANCE_SUM / ${#SCORES[@]}))
  # Approximation: sqrt(variance) ≈ sqrt using integer arithmetic
  STDEV=$(awk "BEGIN {printf \"%.1f\", sqrt($VARIANCE)}")

  echo "Score Range: $MIN_SCORE-$MAX_SCORE (avg: $AVG_SCORE, stdev: $STDEV)"
fi

echo ""

# Determine stability
REQUIRED_PASS_RATE=$((100 - TOLERANCE))

if [[ $PASS_RATE -ge $REQUIRED_PASS_RATE ]]; then
  echo "✅ STABLE (pass rate $PASS_RATE% meets $REQUIRED_PASS_RATE% requirement)"
  EXIT_CODE=0
else
  echo "❌ UNSTABLE (pass rate $PASS_RATE% below $REQUIRED_PASS_RATE% requirement)"
  EXIT_CODE=1
fi

# Recommendations based on workflow type
echo ""
echo "Tolerance Guidelines:"
case $WORKFLOW in
  fix|debug)
    echo "  - Critical workflows: 0% tolerance (5/5 must pass)"
    if [[ $TOLERANCE -gt 0 ]]; then
      echo "  ⚠️  You set $TOLERANCE% tolerance, but 0% is recommended for $WORKFLOW workflows"
    fi
    ;;
  review|ops)
    echo "  - Auxiliary workflows: ≤10% tolerance (4.5/5)"
    if [[ $TOLERANCE -gt 10 ]]; then
      echo "  ⚠️  You set $TOLERANCE% tolerance, exceeds 10% recommendation"
    fi
    ;;
  new)
    echo "  - Creative workflows: ≤40% tolerance (3/5)"
    if [[ $TOLERANCE -gt 40 ]]; then
      echo "  ⚠️  You set $TOLERANCE% tolerance, exceeds 40% recommendation"
    fi
    ;;
esac

exit $EXIT_CODE
