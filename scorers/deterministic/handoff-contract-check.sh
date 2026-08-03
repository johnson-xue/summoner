#!/bin/bash
# handoff-contract-check.sh — P0 scorer: typed-envelope contract (§2.2 + §2.2.1).
# Usage: handoff-contract-check.sh <trace.jsonl>
# Exit: 0=PASS, 1=FAIL, 2=SKIP (no handoff events = SKIP).

set -o pipefail
TRACE="$1"
if [[ ! -f "$TRACE" ]]; then echo "Error: trace not found"; exit 2; fi

HANDOFFS=$(jq -c 'select(.type=="handoff")' "$TRACE" 2>/dev/null)
if [[ -z "$HANDOFFS" ]]; then echo "SKIP: no handoff events (chain-mode trace)"; exit 2; fi

FAILS=0
# NOTE: use `printf '%s\n' "$line"` (NOT `echo "$line"`) to pipe each envelope to
# jq — `echo` interprets backslashes (e.g. `\\|` in a grep_pattern JSON value
# becomes `\|`, corrupting the JSON → jq parse error → false FAILs). `printf '%s'`
# passes the bytes verbatim. This is mandatory for traces whose envelope fields
# contain regex/escape strings (the C10 fixtures carry grep_pattern values like
# "inventory.*Clear\\|DeleteItem\\|RemoveItem").
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  env_id=$(printf '%s\n' "$line" | jq -r '.envelope_id')
  # bootstrap h-000 exempt from review_verdict correlation (§5)
  if [[ "$env_id" == "h-000" ]]; then continue; fi
  # required fields
  for f in envelope_id from_node to_node label artifacts factual_claim attempt_history budget_remaining stripped; do
    if ! printf '%s\n' "$line" | jq -e --arg f "$f" 'has($f)' >/dev/null 2>&1; then
      echo "FAIL: handoff $env_id missing field $f"; FAILS=$((FAILS+1))
    fi
  done
  # artifacts non-empty
  if ! printf '%s\n' "$line" | jq -e '.artifacts | length > 0' >/dev/null 2>&1; then
    echo "FAIL: handoff $env_id has empty artifacts"; FAILS=$((FAILS+1))
  fi
  # exit_criteria each has verdict_type
  bad=$(printf '%s\n' "$line" | jq -r '.exit_criteria[]? | select((.verdict_type // "") | IN("DECIDABLE","SOFT") | not) | .name')
  if [[ -n "$bad" ]]; then
    echo "FAIL: handoff $env_id criterion '$bad' missing verdict_type (B3)"; FAILS=$((FAILS+1))
  fi
  # reject fields outside the allow-list (producer_reasoning_trace / handoff_note / passed)
  leak=$(printf '%s\n' "$line" | jq -r 'keys[] | select(IN("producer_reasoning_trace","handoff_note","passed"))')
  if [[ -n "$leak" ]]; then
    echo "FAIL: handoff $env_id carries banned field $leak (producer-reasoning leak)"; FAILS=$((FAILS+1))
  fi
  # attempt_history entries must NOT carry 'passed'
  if printf '%s\n' "$line" | jq -e '.attempt_history[]? | has("passed")' >/dev/null 2>&1; then
    echo "FAIL: handoff $env_id attempt_history carries 'passed' (B2)"; FAILS=$((FAILS+1))
  fi
  # correlate to a review_verdict by envelope_id
  if ! jq -e --arg id "$env_id" 'select(.type=="review_verdict" and .envelope_id==$id)' "$TRACE" >/dev/null 2>&1; then
    echo "FAIL: handoff $env_id has no correlated review_verdict (B1/⑤-skip)"; FAILS=$((FAILS+1))
  fi
done <<< "$HANDOFFS"

if [[ $FAILS -gt 0 ]]; then exit 1; fi
echo "PASS: all handoffs conform to §2.2 contract"
exit 0
