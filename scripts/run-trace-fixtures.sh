#!/bin/bash
# run-trace-fixtures.sh — Assert the deterministic scorer × fixture matrix.
#
# The expected-outcome table in tests/fixtures/traces/README.md was, until this
# script existed, asserted by nothing (a documented table no code reads — the
# C10 fixture shipped contradicting it). This runner is the executable form of
# that table: every fixture × every scorer must produce the expected exit code.
#
# Expectation sources:
#   - README table rows (contract assertions): C2/C3/C4-new/C9/C10-new PASS the
#     3 contract scorers; C5 FAILs review-isolation; invalid-missing-phase1
#     FAILs iron-law.
#   - Observed-baseline rows (regression tripwires): the old-chain pre-graph
#     baselines and chain-mode fixtures have deterministic outcomes recorded
#     from the 2026-09 baseline run; a change here means a scorer or fixture
#     drifted and must be re-examined, not blindly updated.
#   - The 2026-09 P0-coverage fixtures (valid-p0-clean / invalid-build-error /
#     invalid-lint-error / invalid-tests-fail) give build-check, lint-check and
#     test-pass-rate their first designed PASS/FAIL coverage.
#
# Exit codes follow the repo convention: 0 = matrix green, 1 = any mismatch.
#
# Usage: scripts/run-trace-fixtures.sh [fixtures-dir]

set -u

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_ROOT="$(dirname "$SCRIPT_DIR")"
FIXTURES_DIR="${1:-$PLUGIN_ROOT/tests/fixtures/traces}"
SCORERS_DIR="$PLUGIN_ROOT/scorers/deterministic"

# Expected matrix — "fixture|build|handoff|iron-law|lint|review-iso|tpr|verifier"
# Values: PASS(0) / FAIL(1) / SKIP(2)
EXPECTED="
example-C2-clean-graph-pass.jsonl|PASS|PASS|PASS|SKIP|PASS|SKIP|PASS
example-C3-verify-fail-backedge.jsonl|PASS|PASS|PASS|SKIP|PASS|SKIP|PASS
example-C4-new-graph-review-agent-catches.jsonl|PASS|PASS|PASS|SKIP|PASS|SKIP|PASS
example-C4-old-chain-lets-defect-through.jsonl|PASS|SKIP|PASS|SKIP|SKIP|PASS|PASS
example-C5-review-isolation-violation.jsonl|PASS|PASS|PASS|SKIP|FAIL|SKIP|FAIL
example-C9-three-times-same-finding.jsonl|PASS|PASS|PASS|SKIP|PASS|SKIP|PASS
example-C10-cross-file-soft-⑤-catches.jsonl|PASS|PASS|PASS|SKIP|PASS|SKIP|PASS
example-C10-old-chain-lets-defect-through.jsonl|SKIP|SKIP|FAIL|SKIP|SKIP|SKIP|PASS
invalid-missing-phase1.jsonl|PASS|SKIP|FAIL|SKIP|SKIP|SKIP|PASS
valid-fix-workflow.jsonl|PASS|SKIP|PASS|SKIP|SKIP|PASS|PASS
valid-p0-clean.jsonl|PASS|SKIP|PASS|PASS|SKIP|PASS|PASS
invalid-build-error.jsonl|FAIL|SKIP|PASS|SKIP|SKIP|SKIP|PASS
invalid-lint-error.jsonl|SKIP|SKIP|PASS|FAIL|SKIP|SKIP|PASS
invalid-tests-fail.jsonl|SKIP|SKIP|PASS|SKIP|SKIP|FAIL|PASS
"

SCORERS=(build-check handoff-contract-check iron-law-check lint-check review-isolation-check test-pass-rate verifier-checklist-check)

MISMATCH=0
RUN=0

code_to_name() {
  case "$1" in
    0) echo PASS ;;
    1) echo FAIL ;;
    2) echo SKIP ;;
    *) echo "ERR($1)" ;;
  esac
}

while IFS='|' read -r fixture b h i l r t v; do
  [[ -z "$fixture" || "$fixture" =~ ^# ]] && continue
  want=("$b" "$h" "$i" "$l" "$r" "$t" "$v")
  trace="$FIXTURES_DIR/$fixture"
  if [[ ! -f "$trace" ]]; then
    echo "✗ MISSING FIXTURE: $fixture"
    MISMATCH=$((MISMATCH + 1))
    continue
  fi
  for idx in "${!SCORERS[@]}"; do
    scorer="${SCORERS[$idx]}"
    expected="${want[$idx]}"
    bash "$SCORERS_DIR/$scorer.sh" "$trace" >/dev/null 2>&1
    actual_code=$?
    actual="$(code_to_name "$actual_code")"
    RUN=$((RUN + 1))
    if [[ "$actual" != "$expected" ]]; then
      echo "✗ $fixture :: $scorer — expected $expected, got $actual (exit $actual_code)"
      MISMATCH=$((MISMATCH + 1))
    fi
  done
done <<< "$EXPECTED"

TOTAL=$(grep -c '|' <<< "$EXPECTED")
if [[ $MISMATCH -eq 0 ]]; then
  echo "✓ trace-fixture matrix green: $RUN assertions across $TOTAL fixtures (7 scorers each)"
  exit 0
fi
echo "✗ trace-fixture matrix RED: $MISMATCH mismatch(es) across $TOTAL fixtures"
exit 1
