#!/bin/bash
# test-pass-rate.sh — Verify all tests passed in Phase 4 (verify)

TRACE_FILE="$1"

if [[ ! -f "$TRACE_FILE" ]]; then
  echo "Error: Trace file not found"
  exit 1
fi

# Check if Phase 4 (verify) was executed
if ! jq -e 'select(.type=="phase_start" and .phase==4)' "$TRACE_FILE" > /dev/null 2>&1; then
  echo "SKIP: Phase 4 (verify) was not executed"
  exit 2
fi

# Get Phase 4 start timestamp
PHASE4_START=$(jq -r 'select(.type=="phase_start" and .phase==4) | .timestamp' "$TRACE_FILE" | head -1)

if [[ -z "$PHASE4_START" ]]; then
  echo "SKIP: Could not determine Phase 4 start time"
  exit 2
fi

# Check for test commands after Phase 4 start
TEST_RESULTS=$(jq --arg start "$PHASE4_START" '
  select(.type=="tool_call" and
         .timestamp >= $start and
         (.args.command | test("(go test|npm test|pytest|cargo test|mvn test|make test)")))
' "$TRACE_FILE" 2>/dev/null)

if [[ -z "$TEST_RESULTS" ]]; then
  echo "SKIP: No test commands found in Phase 4"
  exit 2
fi

# Check for test failures
if echo "$TEST_RESULTS" | jq -e 'select(.result=="error")' > /dev/null 2>&1; then
  echo "FAIL: Test command returned error status"
  exit 1
fi

# Check Phase 4 artifacts for test passage confirmation
PHASE4_ARTIFACTS=$(jq -r 'select(.type=="phase_end" and .phase==4) | .artifacts[]' "$TRACE_FILE" 2>/dev/null)

if echo "$PHASE4_ARTIFACTS" | grep -qE "(all passed|tests: .*passed|PASS|OK)"; then
  echo "PASS: All tests passed in Phase 4"
  exit 0
elif echo "$PHASE4_ARTIFACTS" | grep -qE "(failed|FAIL|ERROR)"; then
  echo "FAIL: Test failures detected in Phase 4 artifacts"
  exit 1
fi

echo "PASS: Tests executed successfully in Phase 4"
exit 0
