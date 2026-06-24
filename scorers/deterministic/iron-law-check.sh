#!/bin/bash
# iron-law-check.sh — Verify Phase 1 (diagnose) completion in fix/debug workflows

TRACE_FILE="$1"

if [[ ! -f "$TRACE_FILE" ]]; then
  echo "Error: Trace file not found"
  exit 1
fi

# Extract workflow type
WORKFLOW=$(jq -r 'select(.type=="session_start") | .workflow' "$TRACE_FILE" 2>/dev/null | head -1)

if [[ -z "$WORKFLOW" ]]; then
  echo "SKIP: No session_start event found in trace"
  exit 2
fi

# Iron law only applies to fix and debug workflows
if [[ "$WORKFLOW" != "fix" ]] && [[ "$WORKFLOW" != "debug" ]]; then
  echo "SKIP: Iron law does not apply to workflow: $WORKFLOW"
  exit 2
fi

# Check if Phase 1 completed
if ! jq -e 'select(.type=="phase_end" and .phase==1 and .status=="completed")' "$TRACE_FILE" > /dev/null 2>&1; then
  echo "FAIL: Phase 1 (diagnose) was skipped or did not complete — violates iron law"
  exit 1
fi

echo "PASS: Iron law compliant (Phase 1 completed)"
exit 0
