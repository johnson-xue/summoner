#!/bin/bash
# lint-check.sh — Verify no lint errors introduced

TRACE_FILE="$1"

if [[ ! -f "$TRACE_FILE" ]]; then
  echo "Error: Trace file not found"
  exit 1
fi

# Check for lint tool calls
LINT_RESULTS=$(jq '
  select(.type=="tool_call" and
         (.args.command | test("(lint|golangci-lint|eslint|pylint|flake8|clippy|rubocop)")))
' "$TRACE_FILE" 2>/dev/null)

if [[ -z "$LINT_RESULTS" ]]; then
  echo "SKIP: No lint checks performed"
  exit 2
fi

# Check for lint failures
if echo "$LINT_RESULTS" | jq -e 'select(.result=="error")' > /dev/null 2>&1; then
  echo "FAIL: Lint errors detected"
  exit 1
fi

echo "PASS: No lint errors"
exit 0
