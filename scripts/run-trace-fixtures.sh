#!/bin/bash
# run-trace-fixtures.sh — assert the v2 scorer × fixture matrix (spec §7
# 确定性回归必须覆盖; T12 full rewrite of the graph-era runner).
#
# Three assertion layers:
#   1. EXIT MATRIX — every fixture × every v2 scorer must produce the
#      expected exit code. Outcomes: PASS(0) / FAIL(1) / NA(2, justified
#      N/A) / MISSING(3, missing evidence — the strict fourth state: skip is
#      never a full score and a claimed-but-unevidenced surface is not an
#      N/A).
#   2. ADVERSARIAL MESSAGES — negative/missing fixtures additionally assert
#      the scorer's FAIL/MISSING message text (a hostile trace is caught by
#      a catchable reason, not just an exit code).
#   3. SIDECAR + BASELINE SELF-CHECK — on throwaway copies: score-trace.sh
#      writes `<trace>.score.json` next to the trace and never modifies the
#      trace bytes; create-baseline.sh refuses a missing sidecar;
#      regression-test.sh fails a missing sidecar and passes the round-trip.
#
# Exit codes: 0 = matrix green, 1 = any mismatch.
#
# Usage: scripts/run-trace-fixtures.sh [fixtures-dir]

set -u

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_ROOT="$(dirname "$SCRIPT_DIR")"
FIXTURES_DIR="${1:-$PLUGIN_ROOT/tests/fixtures/traces}"
SCORERS_DIR="$PLUGIN_ROOT/scorers/deterministic"

SCORERS=(schema-check review-evidence-check user-gate-check recovery-check post-game-check)

# Expected exit matrix — "fixture|schema|review|usergate|recovery|postgame"
# PASS(0) / FAIL(1) / NA(2) / MISSING(3). Derived from the designed outcome
# of each fixture; a change here means a scorer or fixture drifted and must
# be re-examined, not blindly updated.
EXPECTED="
valid-full-run.jsonl|PASS|PASS|PASS|NA|PASS
valid-review-reuse.jsonl|NA|PASS|PASS|NA|NA
valid-review-evidence-real.jsonl|NA|PASS|PASS|NA|NA
valid-recall.jsonl|NA|NA|NA|PASS|NA
valid-postgame-emergency.jsonl|NA|NA|PASS|NA|PASS
valid-change-loop.jsonl|NA|NA|PASS|NA|NA
valid-usergate-criterion-redrop.jsonl|NA|NA|PASS|NA|NA
valid-crash-recovery.jsonl|NA|NA|NA|PASS|NA
valid-auto-pass.jsonl|NA|NA|PASS|NA|PASS
valid-checks-ladder.jsonl|NA|NA|MISSING|NA|NA
valid-release-probe-match.jsonl|NA|NA|NA|PASS|NA
valid-release-probe-mismatch.jsonl|NA|NA|NA|PASS|NA
valid-release-probe-absent.jsonl|NA|NA|NA|PASS|NA
missing-postgame-closure.jsonl|MISSING|NA|PASS|NA|MISSING
missing-review-evidence.jsonl|NA|MISSING|NA|NA|NA
missing-recovery-resolution.jsonl|NA|NA|NA|MISSING|NA
invalid-schema-unknown-event.jsonl|FAIL|NA|NA|NA|NA
invalid-schema-no-start.jsonl|FAIL|NA|NA|NA|NA
invalid-schema-double-terminal.jsonl|FAIL|NA|PASS|NA|PASS
invalid-usergate-injected-answers.jsonl|NA|NA|FAIL|NA|NA
invalid-usergate-stale-render.jsonl|NA|NA|FAIL|NA|NA
invalid-usergate-undisplayed-decision.jsonl|NA|NA|FAIL|NA|NA
invalid-usergate-double-decision.jsonl|NA|NA|FAIL|NA|NA
invalid-usergate-nonce-mismatch.jsonl|NA|NA|FAIL|NA|NA
invalid-usergate-drop-replay.jsonl|NA|NA|FAIL|NA|NA
invalid-usergate-redrop-no-reaccept.jsonl|NA|NA|FAIL|NA|NA
invalid-usergate-auto-pass-diagnosis.jsonl|NA|NA|FAIL|NA|NA
invalid-usergate-auto-pass-unconfirmed.jsonl|NA|NA|FAIL|NA|NA
invalid-usergate-auto-after-display.jsonl|NA|NA|FAIL|NA|NA
invalid-review-rubber-stamp.jsonl|NA|FAIL|NA|NA|NA
invalid-review-forged-verdict.jsonl|NA|FAIL|NA|NA|NA
invalid-review-dangling.jsonl|NA|FAIL|NA|NA|NA
invalid-review-criterion-gap.jsonl|NA|FAIL|NA|NA|NA
invalid-review-evidence-forged-id.jsonl|NA|FAIL|NA|NA|NA
invalid-review-attempt-mismatch.jsonl|NA|FAIL|PASS|NA|NA
invalid-review-reuse-mismatch.jsonl|NA|FAIL|NA|NA|NA
invalid-recovery-no-checkpoint.jsonl|NA|NA|NA|FAIL|NA
invalid-recovery-forged-probe.jsonl|NA|NA|NA|FAIL|NA
invalid-recall-unknown-target.jsonl|NA|NA|NA|FAIL|NA
invalid-review-recovery-rerun.jsonl|NA|PASS|NA|FAIL|NA
invalid-postgame-ended-without-completed.jsonl|PASS|NA|PASS|NA|FAIL
invalid-postgame-emergency-instant-close.jsonl|PASS|NA|PASS|NA|FAIL
"

# Adversarial message assertions — "fixture|scorer|expected-exit|substring".
# The five named hostiles (伪造 verdict / 注入 answers / stale 渲染 / 盖章审查 /
# 悬空 review) each assert their scorer's catch reason.
MESSAGES="
invalid-schema-unknown-event.jsonl|schema-check|FAIL|not in the v2 allow-list
invalid-schema-no-start.jsonl|schema-check|FAIL|no workflow.started
invalid-schema-double-terminal.jsonl|schema-check|FAIL|exactly once
invalid-usergate-injected-answers.jsonl|user-gate-check|FAIL|not a recorded AskUserQuestion
invalid-usergate-stale-render.jsonl|user-gate-check|FAIL|stale render
invalid-usergate-undisplayed-decision.jsonl|user-gate-check|FAIL|never displayed
invalid-usergate-double-decision.jsonl|user-gate-check|FAIL|decided twice
invalid-usergate-nonce-mismatch.jsonl|user-gate-check|FAIL|nonce
invalid-usergate-drop-replay.jsonl|user-gate-check|FAIL|same answering call
invalid-usergate-redrop-no-reaccept.jsonl|user-gate-check|FAIL|no phase re-acceptance
invalid-usergate-auto-pass-diagnosis.jsonl|user-gate-check|FAIL|never auto-passes
invalid-usergate-auto-pass-unconfirmed.jsonl|user-gate-check|FAIL|only a confirmed
invalid-usergate-auto-after-display.jsonl|user-gate-check|FAIL|already displayed
invalid-review-rubber-stamp.jsonl|review-evidence-check|FAIL|rubber-stamp
invalid-review-forged-verdict.jsonl|review-evidence-check|FAIL|without a running review
invalid-review-dangling.jsonl|review-evidence-check|FAIL|no review.prepared
invalid-review-criterion-gap.jsonl|review-evidence-check|FAIL|per-criterion completeness
invalid-review-evidence-forged-id.jsonl|review-evidence-check|FAIL|not a recorded tool call
invalid-review-attempt-mismatch.jsonl|review-evidence-check|FAIL|foreign attempt
invalid-review-reuse-mismatch.jsonl|review-evidence-check|FAIL|same-digest reuse
invalid-recovery-no-checkpoint.jsonl|recovery-check|FAIL|no recovery checkpoint
invalid-recovery-forged-probe.jsonl|recovery-check|FAIL|forged probe
invalid-recall-unknown-target.jsonl|recovery-check|FAIL|not real history
invalid-review-recovery-rerun.jsonl|recovery-check|FAIL|producer re-run
invalid-postgame-ended-without-completed.jsonl|post-game-check|FAIL|without a preceding post_game.completed
invalid-postgame-emergency-instant-close.jsonl|post-game-check|FAIL|POST_GAME_PENDING
missing-postgame-closure.jsonl|schema-check|MISSING|closure evidence is missing
missing-postgame-closure.jsonl|post-game-check|MISSING|never closes
missing-review-evidence.jsonl|review-evidence-check|MISSING|cannot be audited
missing-recovery-resolution.jsonl|recovery-check|MISSING|never resolved
valid-checks-ladder.jsonl|user-gate-check|MISSING|never displayed
"

MISMATCH=0
RUN=0

code_to_name() {
  case "$1" in
    0) echo PASS ;;
    1) echo FAIL ;;
    2) echo NA ;;
    3) echo MISSING ;;
    *) echo "ERR($1)" ;;
  esac
}
code_for_name() {
  case "$1" in
    PASS) echo 0 ;;
    FAIL) echo 1 ;;
    NA) echo 2 ;;
    MISSING) echo 3 ;;
    *) echo -1 ;;
  esac
}

echo "── layer 1: exit matrix (fixture × 5 scorers)"
while IFS='|' read -r fixture a b c d e; do
  [[ -z "$fixture" || "$fixture" =~ ^# ]] && continue
  want=("$a" "$b" "$c" "$d" "$e")
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
TOTAL_MATRIX=$(grep -c '|' <<< "$EXPECTED")
[[ $MISMATCH -eq 0 ]] && echo "✓ exit matrix green ($RUN assertions across $TOTAL_MATRIX fixtures × 5 scorers)"

echo "── layer 2: adversarial message assertions"
MSG_RUN=0
MSG_BAD=0
while IFS='|' read -r fixture scorer want_name substring; do
  [[ -z "$fixture" || "$fixture" =~ ^# ]] && continue
  trace="$FIXTURES_DIR/$fixture"
  if [[ ! -f "$trace" ]]; then
    echo "✗ MISSING FIXTURE: $fixture"
    MSG_BAD=$((MSG_BAD + 1))
    continue
  fi
  want_code="$(code_for_name "$want_name")"
  out="$(bash "$SCORERS_DIR/$scorer.sh" "$trace" 2>&1)"
  actual_code=$?
  MSG_RUN=$((MSG_RUN + 1))
  if [[ "$actual_code" -ne "$want_code" ]]; then
    echo "✗ $fixture :: $scorer — message row expected exit $want_code, got $actual_code"
    MSG_BAD=$((MSG_BAD + 1))
  elif ! grep -qF "$substring" <<< "$out"; then
    echo "✗ $fixture :: $scorer — output does not carry the expected reason \"$substring\""
    echo "    got: $(head -1 <<< "$out")"
    MSG_BAD=$((MSG_BAD + 1))
  fi
done <<< "$MESSAGES"
RUN=$((RUN + MSG_RUN))
MISMATCH=$((MISMATCH + MSG_BAD))
[[ $MSG_BAD -eq 0 ]] && echo "✓ message assertions green ($MSG_RUN adversarial rows)"

echo "── layer 3: sidecar + baseline self-check (throwaway copies)"
SELF_BAD=0
SELF_RUN_START=$RUN
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
cp "$FIXTURES_DIR/valid-full-run.jsonl" "$TMP/run-a.jsonl"
cp "$FIXTURES_DIR/valid-full-run.jsonl" "$TMP/run-b.jsonl"
sha_before="$(shasum -a 256 "$TMP/run-a.jsonl" | cut -d' ' -f1)"

# 3a. score-trace writes the sidecar NEXT TO the trace and never touches it
if bash "$PLUGIN_ROOT/scripts/score-trace.sh" --trace "$TMP/run-a.jsonl" >/dev/null 2>&1; then
  :
else
  echo "✗ self-check: score-trace.sh did not pass the positive fixture"
  SELF_BAD=$((SELF_BAD + 1))
fi
RUN=$((RUN + 1))
if [[ ! -f "$TMP/run-a.jsonl.score.json" ]]; then
  echo "✗ self-check: sidecar run-a.jsonl.score.json was not written next to the trace"
  SELF_BAD=$((SELF_BAD + 1))
fi
RUN=$((RUN + 1))
sha_after="$(shasum -a 256 "$TMP/run-a.jsonl" | cut -d' ' -f1)"
if [[ "$sha_before" != "$sha_after" ]]; then
  echo "✗ self-check: score-trace.sh MODIFIED the authoritative trace bytes"
  SELF_BAD=$((SELF_BAD + 1))
fi
RUN=$((RUN + 1))
overall="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["overall"]["status"])' "$TMP/run-a.jsonl.score.json" 2>/dev/null || true)"
if [[ "$overall" != "PASS" ]]; then
  echo "✗ self-check: positive fixture sidecar overall=$overall (want PASS)"
  SELF_BAD=$((SELF_BAD + 1))
fi
RUN=$((RUN + 1))

# 3b. create-baseline REFUSES a trace with no sidecar (required score missing = FAIL)
if bash "$PLUGIN_ROOT/scripts/create-baseline.sh" --trace "$TMP/run-b.jsonl" \
     --name t12-selftest --category bugfix >/dev/null 2>&1; then
  echo "✗ self-check: create-baseline accepted a trace with NO sidecar (must FAIL)"
  SELF_BAD=$((SELF_BAD + 1))
fi
RUN=$((RUN + 1))

# 3c. baseline pins the sidecar; regression round-trip passes, and a missing
#     sidecar on the new trace FAILs (spec §6.10)
BASELINES_ROOT="$PLUGIN_ROOT/baselines"
BASELINES_EXISTED=0
[[ -d "$BASELINES_ROOT" ]] && BASELINES_EXISTED=1
if ! bash "$PLUGIN_ROOT/scripts/create-baseline.sh" --trace "$TMP/run-a.jsonl" \
     --name t12-selftest --category bugfix >/dev/null 2>&1; then
  echo "✗ self-check: create-baseline failed on a scored trace"
  SELF_BAD=$((SELF_BAD + 1))
fi
RUN=$((RUN + 1))
BASELINE="$BASELINES_ROOT/a1b2c3d4e5f60011/t12-selftest.baseline.json"
if [[ ! -f "$BASELINE" ]]; then
  echo "✗ self-check: baseline file not created at $BASELINE"
  SELF_BAD=$((SELF_BAD + 1))
else
  # score run-b, then regression against the baseline must PASS
  bash "$PLUGIN_ROOT/scripts/score-trace.sh" --trace "$TMP/run-b.jsonl" >/dev/null 2>&1
  if bash "$PLUGIN_ROOT/scripts/regression-test.sh" --baseline "$BASELINE" \
       --new-trace "$TMP/run-b.jsonl" >/dev/null 2>&1; then
    :
  else
    echo "✗ self-check: regression round-trip failed on an identical trace"
    SELF_BAD=$((SELF_BAD + 1))
  fi
  RUN=$((RUN + 1))
  # missing sidecar on the new trace must FAIL
  if bash "$PLUGIN_ROOT/scripts/regression-test.sh" --baseline "$BASELINE" \
       --new-trace "$FIXTURES_DIR/valid-recall.jsonl" >/dev/null 2>&1; then
    echo "✗ self-check: regression-test accepted a new trace with NO sidecar (must FAIL)"
    SELF_BAD=$((SELF_BAD + 1))
  fi
  RUN=$((RUN + 1))
  rm -f "$BASELINE"
  rmdir "$BASELINES_ROOT/a1b2c3d4e5f60011" 2>/dev/null || true
  if [[ $BASELINES_EXISTED -eq 0 ]]; then
    rmdir "$BASELINES_ROOT" 2>/dev/null || true
  fi
fi

# 3d. an adversarial trace scores overall FAIL through the sidecar too
cp "$FIXTURES_DIR/invalid-review-rubber-stamp.jsonl" "$TMP/bad.jsonl"
if bash "$PLUGIN_ROOT/scripts/score-trace.sh" --trace "$TMP/bad.jsonl" >/dev/null 2>&1; then
  echo "✗ self-check: rubber-stamp fixture scored overall PASS (must FAIL)"
  SELF_BAD=$((SELF_BAD + 1))
fi
RUN=$((RUN + 1))

MISMATCH=$((MISMATCH + SELF_BAD))
[[ $SELF_BAD -eq 0 ]] && echo "✓ sidecar + baseline self-checks green ($((RUN - SELF_RUN_START)) assertions)"

if [[ $MISMATCH -eq 0 ]]; then
  echo "✓ trace-fixture matrix green: $RUN assertions across $TOTAL_MATRIX fixtures (5 scorers + messages + sidecar self-checks)"
  exit 0
fi
echo "✗ trace-fixture matrix RED: $MISMATCH mismatch(es) across $TOTAL_MATRIX fixtures"
exit 1
