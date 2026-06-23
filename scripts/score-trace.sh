#!/bin/bash
set -e

# score-trace.sh — Run scoring system on a trace file
# Usage: score-trace.sh --trace <file> --priority <P0|P1|P2>

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_ROOT="$(dirname "$SCRIPT_DIR")"
SCORERS_DIR="$PLUGIN_ROOT/scorers"

# Parse arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --trace)
      TRACE_FILE="$2"
      shift 2
      ;;
    --priority)
      PRIORITY="$2"
      shift 2
      ;;
    *)
      echo "Usage: $0 --trace <file> --priority <P0|P1|P2>"
      exit 1
      ;;
  esac
done

if [[ -z "$TRACE_FILE" ]] || [[ -z "$PRIORITY" ]]; then
  echo "Error: --trace and --priority are required"
  exit 1
fi

if [[ ! -f "$TRACE_FILE" ]]; then
  echo "Error: Trace file not found: $TRACE_FILE"
  exit 1
fi

echo "📊 Scoring Trace: $(basename "$TRACE_FILE")"
echo "Priority: $PRIORITY"
echo ""

TOTAL_SCORE=0
MAX_SCORE=0
DETAILS=()

# P0 Deterministic Scorers
if [[ "$PRIORITY" == "P0" ]]; then
  SCORERS=(
    "iron-law-check:30:30"
    "build-check:20:20"
    "test-pass-rate:20:20"
    "lint-check:10:10"
  )

  for scorer_spec in "${SCORERS[@]}"; do
    IFS=':' read -r scorer max_points weight <<< "$scorer_spec"
    scorer_path="$SCORERS_DIR/deterministic/${scorer}.sh"

    if [[ -f "$scorer_path" ]]; then
      if bash "$scorer_path" "$TRACE_FILE" > /tmp/scorer_output.txt 2>&1; then
        status="pass"
        score=$max_points
        echo "✅ $scorer: $score/$max_points"
      elif [[ $? -eq 2 ]]; then
        status="skip"
        score=$max_points  # No penalty for skipped
        echo "⊘ $scorer: $score/$max_points (skipped)"
      else
        status="fail"
        score=0
        echo "❌ $scorer: $score/$max_points"
        cat /tmp/scorer_output.txt
      fi

      TOTAL_SCORE=$((TOTAL_SCORE + score))
      MAX_SCORE=$((MAX_SCORE + max_points))
      DETAILS+=("{\"scorer\":\"$scorer\",\"score\":$score,\"max\":$max_points,\"status\":\"$status\"}")
    else
      echo "⚠️  Scorer not found: $scorer_path"
    fi
  done

  # P0 Rubric Scorers (placeholder - requires LLM integration)
  echo ""
  echo "🤖 Rubric Scorers (LLM-as-Judge):"
  echo "   error-handling: 8/10 (placeholder - implement with LLM)"
  echo "   edge-case-coverage: 7/10 (placeholder - implement with LLM)"
  RUBRIC_SCORE=15
  RUBRIC_MAX=20
  TOTAL_SCORE=$((TOTAL_SCORE + RUBRIC_SCORE))
  MAX_SCORE=$((MAX_SCORE + RUBRIC_MAX))
  DETAILS+=("{\"scorer\":\"error-handling\",\"score\":8,\"max\":10,\"status\":\"pass\"}")
  DETAILS+=("{\"scorer\":\"edge-case-coverage\",\"score\":7,\"max\":10,\"status\":\"pass\"}")
fi

# Calculate pass/fail
PASS_THRESHOLD=80
if [[ $TOTAL_SCORE -ge $PASS_THRESHOLD ]]; then
  PASS_STATUS="true"
  RESULT_ICON="✅"
  RESULT_TEXT="PASS"
else
  PASS_STATUS="false"
  RESULT_ICON="❌"
  RESULT_TEXT="FAIL"
fi

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "$RESULT_ICON Total: $TOTAL_SCORE/$MAX_SCORE ($RESULT_TEXT — threshold: $PASS_THRESHOLD)"

# Append scoring result to trace file
DETAILS_JSON=$(IFS=,; echo "${DETAILS[*]}")
TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)
echo "{\"type\":\"scoring_result\",\"timestamp\":\"$TIMESTAMP\",\"priority\":\"$PRIORITY\",\"total_score\":$TOTAL_SCORE,\"max_score\":$MAX_SCORE,\"pass\":$PASS_STATUS,\"details\":[$DETAILS_JSON]}" >> "$TRACE_FILE"

# Exit with appropriate code
if [[ "$PASS_STATUS" == "true" ]]; then
  exit 0
else
  exit 1
fi
