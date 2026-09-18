#!/bin/bash
# run-routing-evals.sh — opt-in behavioral eval for intent→workflow routing.
#
# NOT a CI gate (panel decision 2026-09: no model-calling ratchet in the
# merge path). Run it manually or from cron when touching when_to_use text,
# command descriptions, or the fix-vs-debug disambiguation rule in
# skills/summoner/SKILL.md.
#
# Method: headless claude with THIS checkout loaded via --plugin-dir, asked
# only "which /summoner:* workflow" — compares against
# evals/cases/routing.json. Baseline = measured-current (first run defines
# the number to defend; there is no aspirational threshold).
#
# Requires: claude CLI + ANTHROPIC_API_KEY (or apiKeyHelper via --settings).
# NOTE: --bare NEVER reads OAuth/keychain login — with a logged-in but
# keyless session this script fails auth; export ANTHROPIC_API_KEY first.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(dirname "$SCRIPT_DIR")"
CASES="${1:-$REPO/evals/cases/routing.json}"

if ! command -v claude >/dev/null 2>&1; then
  echo "✗ claude CLI not found on PATH"; exit 1
fi
if [[ ! -f "$CASES" ]]; then
  echo "✗ cases file not found: $CASES"; exit 1
fi

PASS=0; FAIL=0; WATCH=0; FAILED_IDS=()

while IFS= read -r case_line; do
  id=$(python3 -c 'import sys,json; print(json.loads(sys.argv[1])["id"])' "$case_line")
  prompt=$(python3 -c 'import sys,json; print(json.loads(sys.argv[1])["prompt"])' "$case_line")
  expected=$(python3 -c 'import sys,json; print(json.loads(sys.argv[1])["expected"])' "$case_line")
  borderline=$(python3 -c 'import sys,json; print(json.loads(sys.argv[1]).get("borderline", False))' "$case_line")

  raw=$(claude --bare -p \
    --plugin-dir "$REPO" \
    "You have the Summoner plugin loaded. Which /summoner:* workflow command matches this user intent? FIRST LINE of your reply must be EXACTLY the bare command name from: fix, new, ship, debug, ops, review, release — no reasoning before it, no markup. Put any explanation AFTER that line. Intent: $prompt" \
    </dev/null 2>/dev/null)

  # Extraction (routing fidelity, not format-compliance): line-1 bare name,
  # then first /summoner:<name> mention, then first bare keyword.
  answer=$(printf '%s\n' "$raw" | head -1 | grep -ioE '^(fix|new|ship|debug|ops|review|release)\s*\.?$' | tr -d '[:space:].' || true)
  if [[ -z "$answer" ]]; then
    answer=$(printf '%s\n' "$raw" | grep -oE '/summoner:[a-z]+' | head -1 | sed 's|/summoner:||' || true)
  fi
  if [[ -z "$answer" ]]; then
    answer=$(printf '%s\n' "$raw" | grep -oE '\b(fix|new|ship|debug|ops|review|release)\b' | head -1 || true)
  fi

  if [[ "$answer" == "$expected" ]]; then
    PASS=$((PASS + 1)); echo "✓ $id → $answer"
  elif [[ "$borderline" == "True" ]]; then
    # Known stochastic phrase (flips run-to-run even with the disambiguation
    # rule present at every routing-read layer) — report, don't fail.
    WATCH=$((WATCH + 1)); echo "~ $id (borderline): expected $expected, got '${answer:-<empty>}'"
  else
    FAIL=$((FAIL + 1)); FAILED_IDS+=("$id"); echo "✗ $id: expected $expected, got '${answer:-<empty>}'"
  fi
done < <(python3 -c '
import json, sys
for c in json.load(open(sys.argv[1])):
    print(json.dumps(c, ensure_ascii=False))
' "$CASES")

TOTAL=$((PASS + FAIL + WATCH))
echo "─"
echo "routing evals: $PASS/$TOTAL correct (+$WATCH borderline watch, not counted)"
if [[ $FAIL -gt 0 ]]; then
  echo "failed: ${FAILED_IDS[*]}"
  echo "(fix the when_to_use / disambiguation text, or the case's expectation — then re-run)"
  exit 1
fi
