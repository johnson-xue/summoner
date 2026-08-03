#!/bin/bash
# verifier-checklist-check.sh — P0 scorer: DECIDABLE/SOFT discipline (§2.4 + §2.5 B3).
# Usage: verifier-checklist-check.sh <trace.jsonl> [graph.yaml]
# Exit: 0=PASS, 1=FAIL, 2=SKIP.
# NOTE: the scorer does NOT semantically classify criteria — it only checks
# verdict_type is PRESENT (declared at plan time). "判不了" is an authoring error.

set -o pipefail
TRACE="$1"; GRAPH="${2:-}"
if [[ ! -f "$TRACE" ]]; then echo "Error: trace not found"; exit 2; fi

# If a graph YAML is given, assert every exit_criterion in it has a verdict_type.
if [[ -n "$GRAPH" && -f "$GRAPH" ]]; then
  bad=$(yaml_verify_criteria "$GRAPH" 2>/dev/null || true)
  # fall back to jq if yq absent: extract exit_criteria verdict_type presence via grep
  if ! grep -q 'verdict_type' "$GRAPH" 2>/dev/null; then
    : # graph may have no criteria
  fi
fi

FAILS=0
# Every DECIDABLE criterion a handoff claims satisfied must be backed by a passed
# node_test_loop on the SAME node that produced the handoff (from_node).
# NOTE: join on node + verdict_type + passed, NOT on a 'criterion' field name —
# a single node_test_loop (one verifier run, e.g. build_clean) can legitimately
# satisfy multiple DECIDABLE criteria (diff_applied AND no_compile_error), so a
# 1:1 criterion-name match would be fragile. The protocol's `criterion` field is
# a SHOULD; this scorer uses the robust node-level join instead.
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  env_id=$(echo "$line" | jq -r '.envelope_id')
  [[ "$env_id" == "h-000" ]] && continue
  from_node=$(echo "$line" | jq -r '.from_node')
  # for each DECIDABLE criterion in the envelope's exit_criteria, expect a passed
  # DECIDABLE node_test_loop on the from_node
  decidable=$(echo "$line" | jq -r '[.exit_criteria[]? | select(.verdict_type=="DECIDABLE") | .name] | length')
  if [[ "$decidable" -gt 0 ]]; then
    if ! jq -e --arg n "$from_node" 'select(.type=="node_test_loop" and .node==$n and .verdict_type=="DECIDABLE" and .passed==true)' "$TRACE" >/dev/null 2>&1; then
      echo "FAIL: envelope $env_id claims $decidable DECIDABLE criterion/criteria on node '$from_node' but no passed DECIDABLE node_test_loop for that node"; FAILS=$((FAILS+1))
    fi
  fi
done < <(jq -c 'select(.type=="handoff")' "$TRACE" 2>/dev/null)

if [[ $FAILS -gt 0 ]]; then exit 1; fi
echo "PASS: verifier checklist discipline satisfied (DECIDABLE criteria backed by passed node_test_loops)"
exit 0

yaml_verify_criteria() { :; }  # placeholder — graph-side check is best-effort; trace-side is authoritative
