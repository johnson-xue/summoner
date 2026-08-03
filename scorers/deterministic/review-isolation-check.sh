#!/bin/bash
# review-isolation-check.sh — P0 scorer: ⑤ independence (invariant #6, §2.7).
# Usage: review-isolation-check.sh <trace.jsonl>
# Exit: 0=PASS, 1=FAIL, 2=SKIP.

set -o pipefail
TRACE="$1"
if [[ ! -f "$TRACE" ]]; then echo "Error: trace not found"; exit 2; fi

VERDICTS=$(jq -c 'select(.type=="review_verdict")' "$TRACE" 2>/dev/null)
if [[ -z "$VERDICTS" ]]; then echo "SKIP: no review_verdict events"; exit 2; fi

FAILS=0
# NOTE: `printf '%s\n' "$line"` not `echo "$line"` — see handoff-contract-check.sh
# for the rationale (echo mangles backslashes in JSON regex values → jq parse fail).
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  env_id=$(printf '%s\n' "$line" | jq -r '.envelope_id')
  # non-empty evidence_tool_calls
  if ! printf '%s\n' "$line" | jq -e '.evidence_tool_calls | length > 0' >/dev/null 2>&1; then
    echo "FAIL: review_verdict $env_id has empty evidence_tool_calls (rubber-stamp, invariant #6)"; FAILS=$((FAILS+1))
  fi
  # correlated handoff's stripped must include producer_reasoning_trace AND producer_verdict_self_report
  stripped=$(jq -r --arg id "$env_id" 'select(.type=="handoff" and .envelope_id==$id) | .stripped[]?' "$TRACE")
  if ! echo "$stripped" | grep -q 'producer_reasoning_trace'; then
    echo "FAIL: handoff $env_id stripped lacks producer_reasoning_trace (B2)"; FAILS=$((FAILS+1))
  fi
  if ! echo "$stripped" | grep -q 'producer_verdict_self_report'; then
    echo "FAIL: handoff $env_id stripped lacks producer_verdict_self_report (B2)"; FAILS=$((FAILS+1))
  fi
  # attempt_history entries must not carry 'passed'
  if jq -e --arg id "$env_id" 'select(.type=="handoff" and .envelope_id==$id) | .attempt_history[]? | has("passed")' "$TRACE" >/dev/null 2>&1; then
    echo "FAIL: handoff $env_id attempt_history carries 'passed' (B2)"; FAILS=$((FAILS+1))
  fi
done <<< "$VERDICTS"

if [[ $FAILS -gt 0 ]]; then exit 1; fi
echo "PASS: all review_verdicts are isolation-compliant (§2.7 #6)"
exit 0
