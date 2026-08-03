#!/bin/bash
set -o pipefail

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

# Validate priority
if [[ "$PRIORITY" != "P0" ]] && [[ "$PRIORITY" != "P1" ]] && [[ "$PRIORITY" != "P2" ]]; then
  echo "Error: priority must be P0, P1, or P2 (got: $PRIORITY)"
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

# Create temp file for scorer output
TEMP_OUTPUT=$(mktemp)
trap "rm -f $TEMP_OUTPUT" EXIT

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
      bash "$scorer_path" "$TRACE_FILE" > "$TEMP_OUTPUT" 2>&1
      exit_code=$?

      if [[ $exit_code -eq 0 ]]; then
        status="pass"
        score=$max_points
        echo "✅ $scorer: $score/$max_points"
      elif [[ $exit_code -eq 2 ]]; then
        status="skip"
        score=$max_points  # No penalty for skipped
        echo "⊘ $scorer: $score/$max_points (skipped)"
      else
        status="fail"
        score=0
        echo "❌ $scorer: $score/$max_points"
        cat "$TEMP_OUTPUT"
      fi

      TOTAL_SCORE=$((TOTAL_SCORE + score))
      MAX_SCORE=$((MAX_SCORE + max_points))
      DETAILS+=("{\"scorer\":\"$scorer\",\"score\":$score,\"max\":$max_points,\"status\":\"$status\"}")
    else
      echo "⚠️  Scorer not found: $scorer_path"
    fi
  done

  # P0 Contract Gates — enforcement scorers. A FAIL (exit 1) is a hard fail
  # regardless of points (these are contract invariants, not point scorers;
  # SKIP/exit 2 is non-fatal — chain-mode traces legitimately have no handoffs).
  CONTRACT_SCORERS=(
    "handoff-contract-check"
    "verifier-checklist-check"
    "review-isolation-check"
  )
  for scorer in "${CONTRACT_SCORERS[@]}"; do
    scorer_path="$SCORERS_DIR/deterministic/${scorer}.sh"
    if [[ -f "$scorer_path" ]]; then
      bash "$scorer_path" "$TRACE_FILE" > "$TEMP_OUTPUT" 2>&1
      gate_exit=$?
      if [[ $gate_exit -eq 0 ]]; then
        echo "🔒 $scorer: GATE PASS"
      elif [[ $gate_exit -eq 2 ]]; then
        echo "⊘ $scorer: GATE SKIP (non-fatal)"
      else
        echo "🚫 $scorer: GATE FAIL — contract violated"
        cat "$TEMP_OUTPUT"
        PASS_STATUS="false"
        CONTRACT_FAILED=1
      fi
    fi
  done
  if [[ "${CONTRACT_FAILED:-0}" == "1" ]]; then
    echo ""
    echo "🚫 Contract gate failed — overall result FAIL regardless of points"
    TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    echo "{\"type\":\"scoring_result\",\"timestamp\":\"$TIMESTAMP\",\"priority\":\"$PRIORITY\",\"total_score\":$TOTAL_SCORE,\"max_score\":$MAX_SCORE,\"pass\":false,\"details\":[]}" >> "$TRACE_FILE"
    exit 1
  fi

  # P0 Rubric Scorers (TODO: implement LLM-as-Judge)
  # Currently skipped - will be implemented in follow-up PR
  echo ""
  echo "🤖 Rubric Scorers (LLM-as-Judge): TODO"
  echo "   error-handling: SKIP (not implemented yet)"
  echo "   edge-case-coverage: SKIP (not implemented yet)"

  # Note: When implemented, these will add 20 points to MAX_SCORE
  # For now, max score is 80 (deterministic only), threshold remains 80
  # RUBRIC_MAX=20
  # MAX_SCORE=$((MAX_SCORE + RUBRIC_MAX))
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
