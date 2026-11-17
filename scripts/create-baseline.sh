#!/bin/bash
set -o pipefail

# create-baseline.sh — pin a v2 baseline from a scored trace (spec §6.10:
# version-bound derived-score sidecar; a REQUIRED score that is missing is a
# FAIL, never a silent skip).
#
# Reads the trace AND its derived sidecar `<trace>.score.json` (run
# scripts/score-trace.sh first — a missing sidecar is a hard error, not a
# warning), and writes baselines/<project_root_hash>/<name>.baseline.json
# pinning: schema version, trace sha, workflow/phases, and the exact per-
# scorer statuses the sidecar recorded.
#
# Usage: create-baseline.sh --trace <file> --name <kebab-name> --category <bugfix|feature|ops>

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_ROOT="$(dirname "$SCRIPT_DIR")"
BASELINES_DIR="$PLUGIN_ROOT/baselines"

TRACE_FILE=""
BASELINE_NAME=""
CATEGORY=""
while [[ $# -gt 0 ]]; do
  case $1 in
    --trace) TRACE_FILE="$2"; shift 2 ;;
    --name) BASELINE_NAME="$2"; shift 2 ;;
    --category) CATEGORY="$2"; shift 2 ;;
    *) echo "Usage: $0 --trace <file> --name <name> --category <bugfix|feature|ops>"; exit 1 ;;
  esac
done

if [[ -z "$TRACE_FILE" || -z "$BASELINE_NAME" || -z "$CATEGORY" ]]; then
  echo "Error: --trace, --name, and --category are required"
  exit 1
fi
if [[ ! -f "$TRACE_FILE" ]]; then
  echo "Error: Trace file not found: $TRACE_FILE"
  exit 1
fi
if [[ "$CATEGORY" != "bugfix" && "$CATEGORY" != "feature" && "$CATEGORY" != "ops" ]]; then
  echo "Error: category must be bugfix, feature, or ops (got: $CATEGORY)"
  exit 1
fi
if [[ ! "$BASELINE_NAME" =~ ^[a-z0-9-]+$ ]]; then
  echo "Error: baseline name must be kebab-case (lowercase letters, numbers, hyphens only)"
  exit 1
fi

SIDECAR="${TRACE_FILE}.score.json"
if [[ ! -f "$SIDECAR" ]]; then
  # REQUIRED score missing = FAIL (spec §6.10): no interactive escape hatch.
  echo "Error: required score missing — no derived sidecar at $SIDECAR."
  echo "Run scripts/score-trace.sh --trace '$TRACE_FILE' first."
  exit 1
fi

echo "📊 Creating baseline: $BASELINE_NAME"
echo "Category: $CATEGORY"
echo "Source: $(basename "$TRACE_FILE")"
echo ""

python3 - "$TRACE_FILE" "$SIDECAR" "$BASELINE_NAME" "$CATEGORY" "$BASELINES_DIR" <<'PYEOF'
import datetime, hashlib, json, os, sys

trace, sidecar, name, category, baselines_dir = sys.argv[1:6]

# --- the trace side (v2 events) ------------------------------------------------
run_id = workflow = project = None
phases_closed = []
schema_versions = set()
with open(trace, encoding="utf-8") as f:
    for line in f:
        line = line.strip()
        if not line:
            continue
        try:
            ev = json.loads(line)
        except json.JSONDecodeError:
            continue
        schema_versions.add(ev.get("schema_version"))
        if ev.get("type") == "workflow.started" and workflow is None:
            workflow = ev.get("payload", {}).get("workflow")
            run_id = ev.get("run_id")
            project = ev.get("project_root_hash")
        elif ev.get("type") == "phase.closed":
            phases_closed.append(ev.get("payload", {}).get("phase_id"))

errors = []
if schema_versions != {"2"}:
    errors.append(f"trace is not schema_version 2 throughout (found "
                  f"{sorted(map(str, schema_versions))})")
if not workflow:
    errors.append("trace has no workflow.started event (cannot pin a baseline "
                  "on a fragment)")
if errors:
    for e in errors:
        print(f"✗ {e}", file=sys.stderr)
    sys.exit(1)

# --- the sidecar side (REQUIRED scores) -----------------------------------------
try:
    doc = json.load(open(sidecar, encoding="utf-8"))
except json.JSONDecodeError as e:
    print(f"✗ sidecar does not parse: {e}", file=sys.stderr)
    sys.exit(1)
if doc.get("schema_version") != "2":
    print("✗ sidecar is not schema_version 2", file=sys.stderr)
    sys.exit(1)
scores = {}
for entry in doc.get("scorers", []):
    if "name" not in entry or "status" not in entry:
        print("✗ sidecar scorer entry missing name/status", file=sys.stderr)
        sys.exit(1)
    scores[entry["name"]] = entry["status"]
required = ["schema-check", "review-evidence-check", "user-gate-check",
            "recovery-check", "post-game-check"]
missing = [s for s in required if s not in scores]
if missing:
    print(f"✗ required scores missing from sidecar: {missing} — a baseline "
          "never pins a partial score set", file=sys.stderr)
    sys.exit(1)

# --- write the baseline -----------------------------------------------------------
out_dir = os.path.join(baselines_dir, project or "unknown-project")
os.makedirs(out_dir, exist_ok=True)
baseline = {
    "name": name,
    "category": category,
    "schema_version": "2",
    "trace_file": os.path.basename(trace),
    "trace_sha256": hashlib.sha256(open(trace, "rb").read()).hexdigest(),
    "run_id": run_id,
    "workflow": workflow,
    "project_root_hash": project,
    "phases_closed": [p for p in phases_closed if p],
    "expected_scores": scores,
    "created_at": datetime.datetime.now(datetime.timezone.utc)
                  .strftime("%Y-%m-%dT%H:%M:%SZ"),
}
path = os.path.join(out_dir, name + ".baseline.json")
with open(path, "w", encoding="utf-8") as f:
    json.dump(baseline, f, indent=2, ensure_ascii=False)
    f.write("\n")

print(f"✅ Baseline created: {path}")
print(f"   workflow={workflow} phases={baseline['phases_closed']}")
print(f"   pinned scores: " + ", ".join(f"{k}={v}" for k, v in scores.items()))
print("")
print("To test against this baseline:")
print("  scripts/regression-test.sh --baseline '$path' --new-trace <new.jsonl>")
PYEOF
exit $?
