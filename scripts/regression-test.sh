#!/bin/bash
set -o pipefail

# regression-test.sh — test a new v2 trace against a pinned baseline
# (spec §6.10: read the version-bound derived-score sidecar; a REQUIRED score
# that is missing is a FAIL, never a silent skip).
#
# Checks:
#   1. the new trace's derived sidecar `<new>.score.json` exists — missing
#      sidecar = FAIL (run scripts/score-trace.sh first)
#   2. the new trace is the same trace schema version the baseline pinned
#   3. every scorer the baseline pinned is present in the new sidecar
#      (missing = FAIL) and did not regress:
#        PASS > NA_JUSTIFIED > MISSING_EVIDENCE > FAIL
#      improvements are reported, not penalized
#
# Usage: regression-test.sh --baseline <baseline.json> --new-trace <trace.jsonl> [--output <result.json>]

BASELINE_FILE=""
NEW_TRACE=""
OUTPUT_FILE=""
while [[ $# -gt 0 ]]; do
  case $1 in
    --baseline) BASELINE_FILE="$2"; shift 2 ;;
    --new-trace) NEW_TRACE="$2"; shift 2 ;;
    --output) OUTPUT_FILE="$2"; shift 2 ;;
    *) echo "Usage: $0 --baseline <b.json> --new-trace <t.jsonl> [--output <r.json>]"; exit 1 ;;
  esac
done

if [[ -z "$BASELINE_FILE" || -z "$NEW_TRACE" ]]; then
  echo "Error: --baseline and --new-trace are required"
  exit 1
fi
if [[ ! -f "$BASELINE_FILE" ]]; then
  echo "Error: Baseline file not found: $BASELINE_FILE"
  exit 1
fi
if [[ ! -f "$NEW_TRACE" ]]; then
  echo "Error: New trace file not found: $NEW_TRACE"
  exit 1
fi

SIDECAR="${NEW_TRACE}.score.json"
if [[ ! -f "$SIDECAR" ]]; then
  echo "❌ FAIL — required score missing: no derived sidecar at $SIDECAR."
  echo "   Run scripts/score-trace.sh --trace '$NEW_TRACE' first (spec §6.10:"
  echo "   必需评分缺失=FAIL, never a silent skip)."
  if [[ -n "$OUTPUT_FILE" ]]; then
    printf '{"baseline": "%s", "new_trace": "%s", "status": "fail", "reason": "missing_sidecar"}\n' \
      "${BASELINE_FILE##*/}" "${NEW_TRACE##*/}" > "$OUTPUT_FILE"
  fi
  exit 1
fi

python3 - "$BASELINE_FILE" "$NEW_TRACE" "$SIDECAR" "$OUTPUT_FILE" <<'PYEOF'
import json, os, sys

baseline_path, new_trace, sidecar, output = sys.argv[1:5]

RANK = {"PASS": 3, "NA_JUSTIFIED": 2, "MISSING_EVIDENCE": 1, "FAIL": 0}

try:
    baseline = json.load(open(baseline_path, encoding="utf-8"))
except json.JSONDecodeError as e:
    print(f"❌ FAIL — baseline does not parse: {e}")
    sys.exit(1)
try:
    new = json.load(open(sidecar, encoding="utf-8"))
except json.JSONDecodeError as e:
    print(f"❌ FAIL — sidecar does not parse: {e}")
    sys.exit(1)

print(f"📊 Regression Test: {baseline.get('name')}")
print(f"Baseline:  {os.path.basename(baseline_path)} (pinned "
      f"{baseline.get('created_at', '?')})")
print(f"New trace: {os.path.basename(new_trace)}")
print("")

fails, notes = [], []

# --- version binding -------------------------------------------------------------
pinned = baseline.get("schema_version")
carried = new.get("schema_version")
if pinned and carried and pinned != carried:
    fails.append(f"schema version drift: baseline pins {pinned}, new trace "
                 f"sidecar carries {carried} (re-pin the baseline or migrate)")

expected = baseline.get("expected_scores", {})
if not expected:
    fails.append("baseline pins no expected_scores — re-create it with "
                 "scripts/create-baseline.sh (the v1 phase/tool-sequence "
                 "baseline format is gone)")

new_scores = {e.get("name"): e.get("status") for e in new.get("scorers", [])
              if isinstance(e, dict)}

# --- every REQUIRED score present, none regressed ---------------------------------
print("🔍 Checking pinned scorer statuses...")
for scorer, want in expected.items():
    got = new_scores.get(scorer)
    if got is None:
        fails.append(f"required score missing: sidecar records no result for "
                     f"{scorer} (baseline pinned {want})")
        print(f"   ❌ {scorer}: MISSING (baseline pinned {want})")
    elif got not in RANK:
        fails.append(f"{scorer}: unknown sidecar status {got!r}")
        print(f"   ❌ {scorer}: {got}")
    elif RANK[got] < RANK.get(want, 0):
        fails.append(f"{scorer} regressed: baseline pinned {want}, new trace "
                     f"scores {got}")
        print(f"   ❌ {scorer}: {want} → {got}")
    elif RANK[got] > RANK.get(want, 3):
        notes.append(f"{scorer} improved: {want} → {got}")
        print(f"   ✅ {scorer}: {want} → {got} (improved)")
    else:
        print(f"   ✅ {scorer}: {got} (maintained)")

# --- overall sanity ---------------------------------------------------------------
overall = new.get("overall", {}).get("status")
if overall is not None and overall != "PASS":
    for entry in new.get("scorers", []):
        if entry.get("status") in ("FAIL", "MISSING_EVIDENCE"):
            fails.append(f"{entry.get('name')} = {entry.get('status')}")

print("")
print("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
result = {
    "baseline": baseline.get("name"),
    "new_trace": os.path.basename(new_trace),
    "status": "fail" if fails else "pass",
    "fails": fails,
    "notes": notes,
    "scores": new_scores,
}
if fails:
    print(f"❌ FAIL — {len(fails)} regression(s):")
    for msg in fails:
        print(f"   - {msg}")
    exit_code = 1
else:
    print("✅ PASS — no pinned score regressed"
          + (f" ({len(notes)} improvement(s))" if notes else ""))
    exit_code = 0

if output:
    with open(output, "w", encoding="utf-8") as f:
        json.dump(result, f, indent=2, ensure_ascii=False)
        f.write("\n")
    print(f"Results written to: {output}")
sys.exit(exit_code)
PYEOF
exit $?
