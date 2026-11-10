#!/bin/bash
set -o pipefail

# score-trace.sh — v2 deterministic scoring (spec §7 确定性回归 / §6.8 sidecar).
#
# Runs the five v2 contract scorers against one trace and writes the DERIVED
# sidecar `<trace>.score.json` NEXT TO the trace. It never appends into the
# authoritative trace (落盘边界: the trace is append-only evidence written by
# the Go hooks; scores are derived views).
#
# skip ≠ 满分: the scorer statuses are strictly distinguished —
#   PASS             the audited surface holds
#   FAIL             contract violation
#   NA_JUSTIFIED     the surface does not exist in this trace, justified by
#                    the scorer from trace content (never scored as PASS)
#   MISSING_EVIDENCE the surface was claimed/begun but its evidence is absent
# A trace passes overall iff every scorer is PASS or NA_JUSTIFIED. Any FAIL
# or MISSING_EVIDENCE fails the overall score.
#
# Usage: score-trace.sh --trace <trace.jsonl>

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SCORERS_DIR="$(dirname "$SCRIPT_DIR")/scorers/deterministic"

TRACE_FILE=""
while [[ $# -gt 0 ]]; do
  case $1 in
    --trace) TRACE_FILE="$2"; shift 2 ;;
    *) echo "Usage: $0 --trace <trace.jsonl>"; exit 1 ;;
  esac
done

if [[ -z "$TRACE_FILE" || ! -f "$TRACE_FILE" ]]; then
  echo "Error: --trace <file> is required (and must exist)"
  exit 1
fi

SIDECAR="${TRACE_FILE}.score.json"

SCORE_TABLE=$(
  python3 - "$TRACE_FILE" "$SIDECAR" "$SCORERS_DIR" <<'PYEOF'
import datetime, hashlib, json, os, subprocess, sys

trace, sidecar, scorers_dir = sys.argv[1], sys.argv[2], sys.argv[3]
scorers = ["schema-check", "review-evidence-check", "user-gate-check",
           "recovery-check", "post-game-check"]
status_by_code = {0: "PASS", 1: "FAIL", 2: "NA_JUSTIFIED",
                  3: "MISSING_EVIDENCE"}

results = []
for name in scorers:
    proc = subprocess.run(
        ["bash", os.path.join(scorers_dir, name + ".sh"), trace],
        capture_output=True, text=True)
    detail = (proc.stdout or proc.stderr).splitlines()
    detail = detail[0] if detail else "(no output)"
    status = status_by_code.get(proc.returncode, "FAIL")
    if proc.returncode not in status_by_code:
        detail = f"scorer error (exit {proc.returncode}): {detail}"
    results.append({"name": name, "status": status, "detail": detail})

counts = {s: sum(1 for r in results if r["status"] == s)
          for s in ("PASS", "NA_JUSTIFIED", "MISSING_EVIDENCE", "FAIL")}
doc = {
    "schema_version": "2",
    "trace_file": os.path.basename(trace),
    "trace_sha256": hashlib.sha256(open(trace, "rb").read()).hexdigest(),
    "generated_at": datetime.datetime.now(datetime.timezone.utc)
                    .strftime("%Y-%m-%dT%H:%M:%SZ"),
    "scorers": results,
    "overall": {
        "status": "PASS" if counts["FAIL"] == 0
                  and counts["MISSING_EVIDENCE"] == 0 else "FAIL",
        **counts,
    },
}
with open(sidecar, "w", encoding="utf-8") as f:
    json.dump(doc, f, indent=2, ensure_ascii=False)
    f.write("\n")

for r in results:
    print(f'{r["name"]}\t{r["status"]}\t{r["detail"]}')
print(f'OVERALL\t{doc["overall"]["status"]}')
PYEOF
)

if [[ -z "$SCORE_TABLE" ]]; then
  echo "Error: could not write the sidecar $SIDECAR"
  exit 1
fi

echo "📊 Scored: $(basename "$TRACE_FILE")"
while IFS=$'\t' read -r name status detail; do
  case "$status" in
    PASS)             icon="✅" ;;
    NA_JUSTIFIED)     icon="⊘" ;;
    MISSING_EVIDENCE) icon="❔" ;;
    *)                icon="🚫" ;;
  esac
  [[ "$name" == "OVERALL" ]] && continue
  echo "$icon $name: $status — $detail"
done <<< "$SCORE_TABLE"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

if grep -q $'^OVERALL\tPASS$' <<< "$SCORE_TABLE"; then
  echo "✅ overall PASS — sidecar: $SIDECAR"
  exit 0
fi
echo "🚫 overall FAIL — sidecar: $SIDECAR"
exit 1
