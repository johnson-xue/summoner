#!/bin/bash
set -o pipefail

# regression-test.sh — Test a new trace against a baseline
# Usage: regression-test.sh --baseline <baseline.json> --new-trace <trace.jsonl>

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_ROOT="$(dirname "$SCRIPT_DIR")"

# Parse arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --baseline)
      BASELINE_FILE="$2"
      shift 2
      ;;
    --new-trace)
      NEW_TRACE="$2"
      shift 2
      ;;
    --output)
      OUTPUT_FILE="$2"
      shift 2
      ;;
    *)
      echo "Usage: $0 --baseline <baseline.json> --new-trace <trace.jsonl> [--output <result.json>]"
      exit 1
      ;;
  esac
done

# Validate arguments
if [[ -z "$BASELINE_FILE" ]] || [[ -z "$NEW_TRACE" ]]; then
  echo "Error: --baseline and --new-trace are required"
  exit 1
fi

if [[ ! -f "$BASELINE_FILE" ]]; then
  echo "Error: Baseline file not found: $BASELINE_FILE"
  exit 1
fi

if [[ ! -f "$NEW_TRACE" ]]; then
  echo "Error: New trace file not found: $NEW_TRACE"
  exit 1
fi

BASELINE_NAME=$(jq -r '.name' "$BASELINE_FILE")
echo "📊 Regression Test: $BASELINE_NAME"
echo "Baseline: $(basename "$BASELINE_FILE")"
echo "New trace: $(basename "$NEW_TRACE")"
echo ""

# Initialize results
PASS_COUNT=0
FAIL_COUNT=0
WARNINGS=()
DETAILS=()

# 1. Phase Coverage Check
echo "🔍 Checking phase coverage..."
EXPECTED_PHASES=$(jq -r '.expected_phases | join(",")' "$BASELINE_FILE")
ACTUAL_PHASES=$(jq -r 'select(.type=="phase_end") | .phase' "$NEW_TRACE" 2>/dev/null | tr '\n' ',' | sed 's/,$//')

if [[ "$EXPECTED_PHASES" == "$ACTUAL_PHASES" ]]; then
  echo "  ✅ MATCH (phases: [$ACTUAL_PHASES])"
  PASS_COUNT=$((PASS_COUNT + 1))
  DETAILS+=('{"check":"phase_coverage","status":"pass","expected":"'"$EXPECTED_PHASES"'","actual":"'"$ACTUAL_PHASES"'"}')
else
  echo "  ❌ MISMATCH"
  echo "     Expected: [$EXPECTED_PHASES]"
  echo "     Actual:   [$ACTUAL_PHASES]"
  FAIL_COUNT=$((FAIL_COUNT + 1))
  DETAILS+=('{"check":"phase_coverage","status":"fail","expected":"'"$EXPECTED_PHASES"'","actual":"'"$ACTUAL_PHASES"'"}')
fi

# 2. Tool Sequence Similarity (LCS algorithm)
echo "🔍 Checking tool sequence similarity..."
EXPECTED_TOOLS=$(jq -r '.expected_tool_sequence | join(",")' "$BASELINE_FILE")
ACTUAL_TOOLS=$(jq -r 'select(.type=="tool_call") | .tool' "$NEW_TRACE" 2>/dev/null | tr '\n' ',' | sed 's/,$//')

# Simple similarity check (exact match for now; TODO: implement LCS)
if [[ "$EXPECTED_TOOLS" == "$ACTUAL_TOOLS" ]]; then
  echo "  ✅ MATCH (100% similarity)"
  PASS_COUNT=$((PASS_COUNT + 1))
  DETAILS+=('{"check":"tool_sequence","status":"pass","similarity":100}')
else
  # Count matching tools
  EXPECTED_COUNT=$(echo "$EXPECTED_TOOLS" | tr ',' '\n' | wc -l)
  ACTUAL_COUNT=$(echo "$ACTUAL_TOOLS" | tr ',' '\n' | wc -l)

  # Simple Jaccard similarity
  COMMON=$(comm -12 <(echo "$EXPECTED_TOOLS" | tr ',' '\n' | sort) <(echo "$ACTUAL_TOOLS" | tr ',' '\n' | sort) | wc -l)
  UNION=$((EXPECTED_COUNT + ACTUAL_COUNT - COMMON))
  SIMILARITY=$((COMMON * 100 / UNION))

  if [[ $SIMILARITY -ge 80 ]]; then
    echo "  ⚠️  PARTIAL MATCH (${SIMILARITY}% similarity)"
    WARNINGS+=("Tool sequence differs but ${SIMILARITY}% similar")
    PASS_COUNT=$((PASS_COUNT + 1))
    DETAILS+=('{"check":"tool_sequence","status":"pass","similarity":'$SIMILARITY'}')
  else
    echo "  ❌ MISMATCH (${SIMILARITY}% similarity)"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    DETAILS+=('{"check":"tool_sequence","status":"fail","similarity":'$SIMILARITY'}')
  fi
fi

# 3. Score Comparison
echo "🔍 Checking scores..."
EXPECTED_P0=$(jq -r '.expected_scores.P0 // 0' "$BASELINE_FILE")
EXPECTED_P0_MAX=$(jq -r '.expected_scores.P0_max // 100' "$BASELINE_FILE")

# Get actual scores from new trace
SCORING_RESULT=$(jq -c 'select(.type=="scoring_result" and .priority=="P0")' "$NEW_TRACE" 2>/dev/null | tail -1)

if [[ -z "$SCORING_RESULT" ]]; then
  echo "  ⚠️  No scoring result in new trace. Run score-trace.sh first."
  WARNINGS+=("No scoring result in new trace")
  DETAILS+=('{"check":"scores","status":"skip","reason":"no_scoring_result"}')
else
  ACTUAL_P0=$(echo "$SCORING_RESULT" | jq -r '.total_score')
  ACTUAL_P0_MAX=$(echo "$SCORING_RESULT" | jq -r '.max_score')

  DELTA=$((ACTUAL_P0 - EXPECTED_P0))

  if [[ $DELTA -ge 0 ]]; then
    echo "  ✅ MAINTAINED or IMPROVED (expected: $EXPECTED_P0, actual: $ACTUAL_P0, delta: +$DELTA)"
    PASS_COUNT=$((PASS_COUNT + 1))
    DETAILS+=('{"check":"scores","status":"pass","expected":'$EXPECTED_P0',"actual":'$ACTUAL_P0',"delta":'$DELTA'}')
  elif [[ $DELTA -ge -5 ]]; then
    echo "  ⚠️  MINOR DEGRADATION (expected: $EXPECTED_P0, actual: $ACTUAL_P0, delta: $DELTA)"
    WARNINGS+=("P0 score dropped by $((DELTA * -1)) points (within 5-point tolerance)")
    PASS_COUNT=$((PASS_COUNT + 1))
    DETAILS+=('{"check":"scores","status":"pass","expected":'$EXPECTED_P0',"actual":'$ACTUAL_P0',"delta":'$DELTA'}')
  else
    echo "  ❌ DEGRADED (expected: $EXPECTED_P0, actual: $ACTUAL_P0, delta: $DELTA)"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    DETAILS+=('{"check":"scores","status":"fail","expected":'$EXPECTED_P0',"actual":'$ACTUAL_P0',"delta":'$DELTA'}')
  fi
fi

# 4. Duration Comparison (optional)
echo "🔍 Checking duration..."
EXPECTED_DURATION=$(jq -r '.expected_duration_ms // 0' "$BASELINE_FILE")
ACTUAL_DURATION=$(jq -r 'select(.type=="session_end") | .total_duration_ms' "$NEW_TRACE" 2>/dev/null | head -1)

if [[ -z "$ACTUAL_DURATION" ]] || [[ "$ACTUAL_DURATION" == "0" ]]; then
  echo "  ⊘ SKIP (no duration data)"
  DETAILS+=('{"check":"duration","status":"skip"}')
else
  DURATION_DELTA=$((ACTUAL_DURATION - EXPECTED_DURATION))
  DURATION_DELTA_PCT=$((DURATION_DELTA * 100 / EXPECTED_DURATION))

  if [[ $DURATION_DELTA_PCT -le 20 ]]; then
    echo "  ✅ ACCEPTABLE (expected: ${EXPECTED_DURATION}ms, actual: ${ACTUAL_DURATION}ms, delta: ${DURATION_DELTA_PCT}%)"
    DETAILS+=('{"check":"duration","status":"pass","expected":'$EXPECTED_DURATION',"actual":'$ACTUAL_DURATION',"delta_pct":'$DURATION_DELTA_PCT'}')
  else
    echo "  ⚠️  SLOWER (expected: ${EXPECTED_DURATION}ms, actual: ${ACTUAL_DURATION}ms, delta: ${DURATION_DELTA_PCT}%)"
    WARNINGS+=("Duration increased by ${DURATION_DELTA_PCT}% (>${EXPECTED_DURATION}ms → ${ACTUAL_DURATION}ms)")
    DETAILS+=('{"check":"duration","status":"pass","expected":'$EXPECTED_DURATION',"actual":'$ACTUAL_DURATION',"delta_pct":'$DURATION_DELTA_PCT'}')
  fi
fi

# Summary
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

if [[ $FAIL_COUNT -eq 0 ]]; then
  if [[ ${#WARNINGS[@]} -eq 0 ]]; then
    echo "✅ PASS — No regressions detected"
    OVERALL_STATUS="pass"
  else
    echo "⚠️  PASS with warnings — ${#WARNINGS[@]} minor issue(s)"
    OVERALL_STATUS="pass_with_warnings"
    for warning in "${WARNINGS[@]}"; do
      echo "   - $warning"
    done
  fi
  EXIT_CODE=0
else
  echo "❌ FAIL — $FAIL_COUNT regression(s) detected"
  OVERALL_STATUS="fail"
  EXIT_CODE=1
fi

echo "Checks: $PASS_COUNT passed, $FAIL_COUNT failed"

# Write JSON output if requested
if [[ -n "$OUTPUT_FILE" ]]; then
  DETAILS_JSON=$(IFS=,; echo "${DETAILS[*]}")
  WARNINGS_JSON=$(printf '%s\n' "${WARNINGS[@]}" | jq -R -s -c 'split("\n") | map(select(length > 0))')

  cat > "$OUTPUT_FILE" <<EOF
{
  "baseline": "$BASELINE_NAME",
  "status": "$OVERALL_STATUS",
  "pass_count": $PASS_COUNT,
  "fail_count": $FAIL_COUNT,
  "warnings": $WARNINGS_JSON,
  "checks": [$DETAILS_JSON],
  "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
EOF
  echo ""
  echo "Results written to: $OUTPUT_FILE"
fi

exit $EXIT_CODE
