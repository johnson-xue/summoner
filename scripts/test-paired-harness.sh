#!/bin/bash
# test-paired-harness.sh — deterministic selftests for the paired behavioral
# eval harness (spec §7; adjudication 2026-11-11). No model, no network, no
# claude CLI run: every model-facing surface is exercised through a stub
# runner (PAIRED_EVAL_CLAUDE_CMD) and a PATH stub that fails the suite the
# moment anything shells out to `claude` uninvited.
#
# Covered (adjudicated selftest list):
#   --list snapshot with PATH-stub claude (zero invocations)
#   prepare: pristine-commit-before-patch, per-arm manifest variant,
#            truth/meta absent from $RUN, allowlist corpus-free AND contains
#            scripts/, patched-tree digest pinned, generic fixture author
#   staging reproducibility (two stages, equal digests)
#   P1-P9 probe plumbing via stub transcripts (PASS/FAIL/BLOCKED semantics)
#   stub-runner honesty (fixture transcripts -> expected metric columns;
#            missing/empty -> nonzero BLOCKED/EMPTY exit; canary quarantine)
#   g4 regeneration shasum -c; MANIFEST.digest verification
#   refusal gates (run/judge refuse without user-filled tolerances + budget;
#            example ships EMPTY placeholders, no digits anywhere)
#   manifest golden + expected-delta table (both drift directions trip)
#   gate-script digest + per-arm channel declaration in the run manifest
#   settings schema-validation catches a malformed file pre-launch
#
# Standalone: bash scripts/test-paired-harness.sh   (exit 0 = green)

set -u

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_ROOT="$(dirname "$SCRIPT_DIR")"
RUNNER="$SCRIPT_DIR/run-paired-eval.sh"
PAIRED="$PLUGIN_ROOT/evals/paired"

command -v python3 >/dev/null 2>&1 || { echo "✗ python3 required" >&2; exit 1; }

SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/paired-harness-selftest.XXXXXX")"
trap 'rm -rf "$SANDBOX"' EXIT

RESULTS="$SANDBOX/results"
TMPROOT="$SANDBOX/tmp"
PATHSTUB="$SANDBOX/pathstub"
STUBBIN="$SANDBOX/stub/claude-stub"
mkdir -p "$RESULTS" "$TMPROOT" "$PATHSTUB" "$SANDBOX/stub"

PASS=0
FAIL=0
pass() { PASS=$((PASS + 1)); printf '✓ %s\n' "$1"; }
fail() { FAIL=$((FAIL + 1)); printf '✗ %s\n' "$1"; }
section() { printf -- '-- %s\n' "$1"; }

expect_exit() { # $1=want $2=got $3=label
    if [ "$1" -eq "$2" ]; then pass "$3 (exit $2)"; else fail "$3 (want exit $1, got $2)"; fi
}
expect_absent() { # $1=file $2=label
    if [ ! -e "$1" ]; then pass "$2"; else fail "$2 (marker exists: $1)"; fi
}

# PATH stub: any uninvoked `claude` lookup marks and fails the suite.
cat > "$PATHSTUB/claude" <<'STUB'
#!/bin/bash
printf 'invoked\n' >> "$(dirname "$0")/claude-invoked-marker"
exit 97
STUB
chmod +x "$PATHSTUB/claude"
MARKER="$PATHSTUB/claude-invoked-marker"
STUBBED_PATH="$PATHSTUB:$PATH"

# stub runner contract (see paired_eval.py docstring): receives the exact
# argv/cwd/env; writes the stream-json transcript to $PAIRED_EVAL_TRANSCRIPT_PATH.
# PAIRED_EVAL_STUB_MODE: ok (default) | empty | liar | canary
cat > "$STUBBIN" <<'STUB'
#!/bin/bash
# probe sessions carry the PAIRED_EVAL_PROBE marker in their -p prompt
PROMPT=""
prev=""
for a in "$@"; do
    if [ "$prev" = "-p" ]; then PROMPT="$a"; break; fi
    prev="$a"
done
OUT="${PAIRED_EVAL_TRANSCRIPT_PATH:-/dev/null}"
MODE="${PAIRED_EVAL_STUB_MODE:-ok}"
for a in "$@"; do
    if [ "$a" = "--help" ]; then exit 0; fi
done
if printf '%s' "$PROMPT" | grep -q "PAIRED_EVAL_PROBE"; then
    ID=$(printf '%s' "$PROMPT" | sed -n 's/.*id=\(P[0-9]*\).*/\1/p' | head -1)
    KIND=$(printf '%s' "$PROMPT" | sed -n 's/.*kind=\([a-z_]*\).*/\1/p' | head -1)
    TARGET=$(printf '%s' "$PROMPT" | sed -n 's/.*target=\([^ ]*\).*/\1/p' | head -1)
    PINNED=$(printf '%s' "$PROMPT" | sed -n 's/.*Pinned file: \([^ ]*\).*/\1/p' | head -1)
    T0="2026-11-11T10:00:00Z"; T1="2026-11-11T10:00:01Z"
    case "$KIND" in
        read_deny)
            if [ "$MODE" = "liar" ]; then
                printf '{"type":"assistant","timestamp":"%s","message":{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"%s"}}]}}\n{"type":"user","timestamp":"%s","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"file contents shown"}]}}\n' "$T0" "$TARGET" "$T1" > "$OUT"
            else
                printf '{"type":"assistant","timestamp":"%s","message":{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"%s"}}]}}\n{"type":"user","timestamp":"%s","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"Read denied by permissions"}]}}\n' "$T0" "$TARGET" "$T1" > "$OUT"
            fi
            ;;
        read_allow)
            printf '{"type":"assistant","timestamp":"%s","message":{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"%s"}}]}}\n{"type":"user","timestamp":"%s","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"module inventoryd"}]}}\n' "$T0" "$TARGET" "$T1" > "$OUT"
            ;;
        bash_allow)
            printf '{"type":"assistant","timestamp":"%s","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"cd /tmp/scratch && go test ./..."}}]}}\n{"type":"user","timestamp":"%s","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok  scratch"}]}}\n' "$T0" "$T1" > "$OUT"
            ;;
        bash_child_read_deny)
            printf '{"type":"assistant","timestamp":"%s","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"cat %s"}}]}}\n{"type":"user","timestamp":"%s","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"Bash command blocked: path outside allowed roots"}]}}\n{"type":"assistant","timestamp":"%s","message":{"content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-sandbox-probe"}}]}}\n{"type":"user","timestamp":"%s","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"{\\"probe\\":\\"summoner-sandbox-probe/v1\\",\\"effect\\":\\"refused\\",\\"detail\\":\\"write refused\\"}"}]}}\n' "$T0" "$TARGET" "$T1" "$T1" "$T1" > "$OUT"
            ;;
        producer_session_read_pair)
            printf '{"type":"assistant","timestamp":"%s","message":{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"%s"}}]}}\n{"type":"user","timestamp":"%s","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"session jsonl content"}]}}\n{"type":"assistant","timestamp":"%s","message":{"content":[{"type":"tool_use","id":"t2","name":"Read","input":{"file_path":"/sibling/session.jsonl"}}]}}\n{"type":"user","timestamp":"%s","message":{"content":[{"type":"tool_result","tool_use_id":"t2","is_error":true,"content":"Read denied by permissions"}]}}\n' "$T0" "$PINNED" "$T1" "$T1" "$T1" > "$OUT"
            ;;
        *)
            printf '{"type":"assistant","timestamp":"%s","message":{"content":[{"type":"text","text":"unknown probe kind %s"}]}}\n' "$T0" "$KIND" > "$OUT"
            ;;
    esac
    exit 0
fi
# main session transcripts
if [ "$MODE" = "empty" ]; then exit 0; fi
if [ "$MODE" = "canary" ]; then
    CANARY="$(cat "${PAIRED_EVAL_STUB_CANARY:-/dev/null}" 2>/dev/null || true)"
    printf '{"type":"system","subtype":"init","session_id":"stub-session","timestamp":"2026-11-11T10:00:00Z"}\n{"type":"assistant","timestamp":"2026-11-11T10:00:02Z","message":{"content":[{"type":"text","text":"I read the canary: %s"}]}}\n' "$CANARY" > "$OUT"
    exit 0
fi
cat > "$OUT" <<'JSONL'
{"type":"system","subtype":"init","session_id":"stub-session-main","timestamp":"2026-11-11T10:00:00Z"}
{"type":"assistant","timestamp":"2026-11-11T10:00:01Z","message":{"content":[{"type":"tool_use","id":"r1","name":"Read","input":{"file_path":"internal/report/report.go"}}]}}
{"type":"user","timestamp":"2026-11-11T10:00:02Z","message":{"content":[{"type":"tool_result","tool_use_id":"r1","content":"package report"}]}}
{"type":"assistant","timestamp":"2026-11-11T10:00:03Z","message":{"content":[{"type":"tool_use","id":"r2","name":"Read","input":{"file_path":"internal/report/report.go"}}]}}
{"type":"user","timestamp":"2026-11-11T10:00:04Z","message":{"content":[{"type":"tool_result","tool_use_id":"r2","content":"package report"}]}}
{"type":"assistant","timestamp":"2026-11-11T10:00:05Z","message":{"content":[{"type":"text","text":"Review findings (blocking): internal/report/report.go — ExportRaw joins the caller filename without sanitization, so ../ escapes the export directory; internal/api/handlers.go — ImportItems drops every per-item error and always returns nil; internal/api/handlers.go — QueryByOwner writes the raw owner email into the audit log (PII in logs)."}]}}
{"type":"assistant","timestamp":"2026-11-11T10:00:06Z","message":{"content":[{"type":"text","text":"No further defects found; contract constants are correct as documented."}]}}
{"type":"result","subtype":"success","session_id":"stub-session-main","timestamp":"2026-11-11T10:00:07Z"}
JSONL
exit 0
STUB
chmod +x "$STUBBIN"

# unit-check helper (fix round 1): probe-classification + parser repros run
# directly against paired_eval — no claude surface involved
cat > "$SANDBOX/unit-checks.py" <<'UNITPY'
import json
import os
import sys
import tempfile

sys.path.insert(0, os.path.join(sys.argv[1], "evals", "paired", "harness"))
import paired_eval as pe

problems = []
td = tempfile.mkdtemp(prefix="unit-checks.")


def transcript(name, is_error, content):
    path = os.path.join(td, name)
    with open(path, "w") as fh:
        fh.write(json.dumps({"type": "assistant", "timestamp": "2026-11-11T10:00:00Z",
                             "message": {"content": [{"type": "tool_use", "id": "t1", "name": "Read",
                                                      "input": {"file_path": "/ctl/truth.json"}}]}}) + "\n")
        fh.write(json.dumps({"type": "user", "timestamp": "2026-11-11T10:00:01Z",
                             "message": {"content": [{"type": "tool_result", "tool_use_id": "t1",
                                                      "is_error": is_error, "content": content}]}}) + "\n")
    return path


P1 = {"id": "P1", "kind": "read_deny", "target": "/ctl/truth.json", "expect": "denied"}
got = pe.evaluate_probe_transcript(P1, transcript("eisdir.jsonl", True, "File is a directory"))["status"]
if got != "FAIL":
    problems.append("EISDIR on a directory target must FAIL (got %s)" % got)
got = pe.evaluate_probe_transcript(P1, transcript("crash.jsonl", True, "tool crashed: unexpected EOF"))["status"]
if got != "FAIL":
    problems.append("crash-style error must FAIL (got %s)" % got)
got = pe.evaluate_probe_transcript(P1, transcript("denied.jsonl", True, "Read denied by permissions"))["status"]
if got != "PASS":
    problems.append("a proper denial must still PASS (got %s)" % got)
got = pe.evaluate_probe_transcript(P1, transcript("notallowed.jsonl", True,
                                                  "Request not allowed by sandbox policy"))["status"]
if got != "PASS":
    problems.append("'not allowed' marker must classify denied (got %s)" % got)

dup = os.path.join(td, "dup.yaml")
with open(dup, "w") as fh:
    fh.write("operator:\n  name: alice\n  name: mechanical\n")
try:
    pe.parse_yaml_subset(open(dup).read(), origin=dup)
    problems.append("duplicate key in one block must raise")
except ValueError as exc:
    if "name" not in str(exc):
        problems.append("duplicate-key error must name the key: %s" % exc)

tol = os.path.join(td, "tol-dup.yaml")
with open(tol, "w") as fh:
    fh.write("noninferiority:\n  blocking_recall_delta: 0.05\n  blocking_recall_delta: 0.5\n")
try:
    pe.parse_yaml_subset(open(tol).read(), origin=tol)
    problems.append("duplicated tolerances knob must raise")
except ValueError:
    pass

# N-3: a real-run environment cannot redirect platform session discovery.
old_stub = os.environ.pop("PAIRED_EVAL_CLAUDE_CMD", None)
os.environ["PAIRED_EVAL_PROJECTS_DIR"] = os.path.join(td, "fake-projects")
if pe.projects_dir() != os.path.abspath(os.path.expanduser("~/.claude/projects")):
    problems.append("real run honored the selftest projects-dir override")
os.environ["PAIRED_EVAL_CLAUDE_CMD"] = "stub-only"
if pe.projects_dir() != os.path.abspath(os.environ["PAIRED_EVAL_PROJECTS_DIR"]):
    problems.append("stub-runner projects-dir override stopped working")
if old_stub is None:
    os.environ.pop("PAIRED_EVAL_CLAUDE_CMD", None)
else:
    os.environ["PAIRED_EVAL_CLAUDE_CMD"] = old_stub
os.environ.pop("PAIRED_EVAL_PROJECTS_DIR", None)

# platform-suffix binary resolution (2026-11-17 CI RED): the unsuffixed tree
# binary is whatever OS cut the release (v0.3.0 shipped darwin-x86_64), so a
# linux runner cannot exec it — the resolver must prefer this platform's
# suffixed variant and only then fall back to the unsuffixed copy.
pd = os.path.join(td, "plug")
os.makedirs(os.path.join(pd, "hooks", "bin"), exist_ok=True)
bdir = os.path.join(pd, "hooks", "bin")
open(os.path.join(bdir, "session-start"), "w").close()
open(os.path.join(bdir, "session-start-linux-amd64"), "w").close()
open(os.path.join(bdir, "userpromptsubmit-darwin-arm64"), "w").close()
if pe.plugin_binary(pd, "session-start") != os.path.join(bdir, "session-start-linux-amd64") \
        and pe.IS_DARWIN is False and pe.sys.platform.startswith("linux"):
    problems.append("linux runner must prefer the -linux-amd64 variant")
if pe.IS_DARWIN:
    if pe.plugin_binary(pd, "userpromptsubmit") != os.path.join(bdir, "userpromptsubmit-darwin-arm64") \
            and pe.platform.machine() == "arm64":
        problems.append("darwin-arm64 host must prefer its variant")
    if pe.plugin_binary(pd, "stop") != os.path.join(bdir, "stop"):
        problems.append("no variant present -> unsuffixed fallback")
else:
    if pe.plugin_binary(pd, "stop") != os.path.join(bdir, "stop"):
        problems.append("no variant present -> unsuffixed fallback")
    # cross-check the suffix the resolver computes for a faked linux host
    real_machine = pe.platform.machine
    pe.platform.machine = lambda: "x86_64"
    if pe.plugin_binary(pd, "session-start") != os.path.join(bdir, "session-start-linux-amd64"):
        problems.append("linux x86_64 resolver picked %s" % pe.plugin_binary(pd, "session-start"))
    pe.platform.machine = real_machine

# launch-suppression aggregation (2026-11-17 CI RED): a platform-unavailable
# BLOCKED (P9 sealed tier on linux) is a DISCLOSURE, not an isolation failure
# — the launch must still run and p9_status still reads BLOCKED downstream,
# while genuine FAIL and operational BLOCKED still suppress the launch (7).
pr = {"preflight_phase": {"P4": {"status": "PASS"}, "P5": {"status": "PASS"}},
      "run_phase": {"P7": {"status": "PASS"},
                    "P9": {"status": "BLOCKED", "platform_unavailable": True,
                           "detail": "sealed tier unavailable on this platform"}}}
if pe.launch_suppressing_probes(pr) != []:
    problems.append("platform-unavailable BLOCKED must not suppress (got %s)"
                    % pe.launch_suppressing_probes(pr))
pr["run_phase"]["P6"] = {"status": "FAIL"}
if pe.launch_suppressing_probes(pr) != ["P6"]:
    problems.append("genuine FAIL must still suppress (got %s)"
                    % pe.launch_suppressing_probes(pr))
pr["run_phase"]["P9"] = {"status": "BLOCKED", "detail": "operational block"}
if pe.launch_suppressing_probes(pr) != ["P6", "P9"]:
    problems.append("operational BLOCKED without the flag must still suppress (got %s)"
                    % pe.launch_suppressing_probes(pr))

# native-validator resolution (2026-11-17 CI RED): v0.3.0's launcher script
# hardcodes the unsuffixed hooks/bin/validate-manifest (a Mach-O file at that
# tag) — "bash script" dies with Exec format error on linux. The validation
# surface is the Go BINARY: native_validator must resolve it platform-
# suffixed (plugin_binary semantics) and fail closed to None when no binary
# copy exists at all.
nv = os.path.join(td, "nvplug")
os.makedirs(os.path.join(nv, "hooks", "bin"), exist_ok=True)
os.makedirs(os.path.join(nv, "scripts"), exist_ok=True)
open(os.path.join(nv, "scripts", "validate-manifest.sh"), "w").close()
open(os.path.join(nv, "hooks", "bin", "validate-manifest"), "w").close()
open(os.path.join(nv, "hooks", "bin", "validate-manifest-linux-amd64"), "w").close()
real_is_darwin, real_sys_platform, real_machine2 = pe.IS_DARWIN, pe.sys.platform, pe.platform.machine
pe.IS_DARWIN = False
pe.sys.platform = "linux"
pe.platform.machine = lambda: "x86_64"
got_nv = pe.native_validator(nv)
if not (isinstance(got_nv, list) and got_nv[0].endswith("validate-manifest-linux-amd64")):
    problems.append("native_validator must pick the suffixed binary (got %s)" % got_nv)
pe.IS_DARWIN, pe.sys.platform, pe.platform.machine = real_is_darwin, real_sys_platform, real_machine2
got_nv = pe.native_validator(nv)
if pe.IS_DARWIN and not (isinstance(got_nv, list) and got_nv[0].endswith(os.sep + "validate-manifest")):
    problems.append("darwin host without its variant must fall back to unsuffixed (got %s)" % got_nv)
empty_plug = os.path.join(td, "nvempty")
os.makedirs(os.path.join(empty_plug, "scripts"), exist_ok=True)
open(os.path.join(empty_plug, "scripts", "validate-manifest.sh"), "w").close()
if pe.native_validator(empty_plug) is not None:
    problems.append("script-only plugin (no binary copy) -> None (fail-closed), got %s"
                    % pe.native_validator(empty_plug))

print("OK" if not problems else "; ".join(problems))
UNITPY

# user-filled gate policy / tolerances / budget for the stub pipeline
sed 's/^  name: ""$/  name: "selftest-operator"/' "$PAIRED/gate-policy.yaml" > "$SANDBOX/gate-policy.yaml"
export PAIRED_EVAL_GATE_POLICY="$SANDBOX/gate-policy.yaml"

cat > "$SANDBOX/tolerances.yaml" <<'TOL'
noninferiority:
  blocking_recall_delta: 0.05
  false_positive_delta: 1
  elapsed_delta: 600
  criteria_omission_delta: 0
critical_requirements:
  blocking_recall_floor: 0.9
  digest_alarm: "yes"
stop_conditions:
  max_cases_per_arm: 28
  max_budget_units: 1000
  stop_on_blocked: 2
TOL

cat > "$SANDBOX/budget.md" <<'BUD'
approved-by: selftest-operator
budget: stub-only pipeline test (zero model cost)
BUD

RUN() { PATH="$STUBBED_PATH" python3 "$PLUGIN_ROOT/evals/paired/harness/paired_eval.py" "$@"; }
WRAP() { PATH="$STUBBED_PATH" bash "$RUNNER" "$@"; }

# ---------------------------------------------------------------------------
section "1. corpus integrity"
if (cd "$PAIRED" && shasum -a 256 -c harness/MANIFEST.digest --status); then
    pass "MANIFEST.digest verifies every folded cases/ file"
else
    fail "MANIFEST.digest verification"
fi
for g4case in g4-a g4-b; do
    if (cd "$PAIRED/cases/g4/$g4case" && shasum -a 256 -c SHA256SUMS --status); then
        pass "$g4case SHA256SUMS verify against the folded tree"
    else
        fail "$g4case SHA256SUMS verification"
    fi
done
for pair in "15:g4-a" "40:g4-b"; do
    n="${pair%%:*}"; c="${pair##*:}"
    rm -rf "$SANDBOX/gen-$c" && mkdir -p "$SANDBOX/gen-$c"
    if python3 "$PAIRED/cases/g4/generate.py" "$n" "$SANDBOX/gen-$c/repo" >/dev/null 2>&1 \
       && cp "$PAIRED/cases/g4/$c/SHA256SUMS" "$SANDBOX/gen-$c/" \
       && (cd "$SANDBOX/gen-$c" && shasum -a 256 -c SHA256SUMS --status); then
        pass "$c regeneration byte-identical (generate.py $n, shasum -c)"
    else
        fail "$c regeneration mismatch"
    fi
done
WORKER_COPY_NAME=worker.yaml
if cmp -s "$PLUGIN_ROOT/agents/worker.md" "$PAIRED/agents/$WORKER_COPY_NAME"; then
    pass "worker.md byte-pinned into the paired-eval agents copy"
else
    fail "worker.yaml drifted from agents/worker.md"
fi

# ---------------------------------------------------------------------------
section "2. tolerances example + refusal gates"
if grep -q '[0-9]' "$PAIRED/tolerances.yaml.example"; then
    fail "tolerances example must contain no digits anywhere"
else
    pass "tolerances example: empty placeholders, no digits anywhere"
fi
OUT=$(WRAP run g1-a --arm baseline 2>&1); RC=$?
expect_exit 3 "$RC" "run refuses without tolerances/budget"
case "$OUT" in *"REFUSED"*) pass "refusal message printed";; *) fail "no REFUSED in refusal output";; esac
cp "$PAIRED/tolerances.yaml.example" "$SANDBOX/unfilled-tolerances.yaml"
OUT=$(WRAP run g1-a --arm baseline --tolerances "$SANDBOX/unfilled-tolerances.yaml" --budget "$SANDBOX/budget.md" 2>&1); RC=$?
expect_exit 3 "$RC" "run refuses a tolerances file identical to the example"
printf 'approved-by: <you>\nbudget: <later>\n' > "$SANDBOX/placeholder-budget.md"
OUT=$(WRAP run g1-a --arm baseline --tolerances "$SANDBOX/tolerances.yaml" --budget "$SANDBOX/placeholder-budget.md" 2>&1); RC=$?
expect_exit 3 "$RC" "run refuses a placeholder budget statement"
OUT=$(WRAP judge some-run-id 2>&1); RC=$?
expect_exit 3 "$RC" "judge refuses without tolerances/budget"
sed 's/^  blocking_recall_delta: 0.05$/  blocking_recall_delta: TBD/' "$SANDBOX/tolerances.yaml" > "$SANDBOX/tolerances-tbd.yaml"
OUT=$(WRAP run g1-a --arm baseline --tolerances "$SANDBOX/tolerances-tbd.yaml" --budget "$SANDBOX/budget.md" 2>&1); RC=$?
expect_exit 3 "$RC" "run refuses a placeholder-valued (TBD) tolerances knob"

# ---------------------------------------------------------------------------
section "3. --list snapshot with PATH-stub claude (zero invocations)"
OUT=$(WRAP --list 2>&1); RC=$?
expect_exit 0 "$RC" "--list succeeds"
expect_absent "$MARKER" "--list invoked zero claude calls"
case "$OUT" in
    *"14 cases, 28 default arm-runs"*) pass "--list enumerates all cases and arm-runs";;
    *) fail "--list matrix count line missing";;
esac
JSON=$(WRAP --list --json 2>/dev/null | PATH="$STUBBED_PATH" python3 -c '
import json, sys
data = json.load(sys.stdin)
rows = data["matrix"]
cases = sorted({r["case"] for r in rows})
groups = sorted({r["group"] for r in rows})
problems = []
if len(rows) != 28: problems.append("rows=%d" % len(rows))
if len(cases) != 14: problems.append("cases=%d" % len(cases))
if groups != [1, 2, 3, 4, 5]: problems.append("groups=%s" % groups)
for r in rows:
    for key in ("ref_commit", "manifest_variant", "channel", "required_tier", "strategy"):
        if not r.get(key): problems.append("%s/%s missing %s" % (r["case"], r["arm"], key))
    if r["arm"] == "baseline" and r["channel"] != "chat_prose": problems.append("baseline channel")
    if r["arm"] == "candidate" and r["channel"] != "askuserquestion": problems.append("candidate channel")
print("OK" if not problems else "; ".join(problems))
')
if [ "$JSON" = "OK" ]; then pass "--list --json: resolved configs complete for all rows"; else fail "--list --json: $JSON"; fi
expect_absent "$MARKER" "--list --json invoked zero claude calls"

# ---------------------------------------------------------------------------
section "4. arm resolution rules"
OUT=$(RUN prepare g4-a candidate 2>&1); RC=$?
expect_exit 2 "$RC" "g4 has no single candidate arm (strategy comparison)"
case "$OUT" in *"no single"*) pass "g4 arm-resolution message";; *) fail "g4 arm-resolution message missing";; esac
OUT=$(RUN prepare g1-a producer 2>&1); RC=$?
expect_exit 2 "$RC" "producer target only exists for group five"

# ---------------------------------------------------------------------------
section "5. prepare: layout, isolation, digests (PATH-stub claude)"
RUN prepare g1-a baseline --run-id g1-a.baseline.selftest1 --results-dir "$RESULTS" --tmp "$TMPROOT" >/dev/null 2>&1; RC=$?
expect_exit 0 "$RC" "prepare g1-a/baseline"
expect_absent "$MARKER" "prepare invoked zero claude calls"
RUN prepare g1-a candidate --run-id g1-a.candidate.selftest1 --results-dir "$RESULTS" --tmp "$TMPROOT" >/dev/null 2>&1; RC=$?
expect_exit 0 "$RC" "prepare g1-a/candidate"

CTL_B="$RESULTS/g1-a.baseline.selftest1"
CTL_C="$RESULTS/g1-a.candidate.selftest1"
RD_B=$(PATH="$STUBBED_PATH" python3 -c "import json;print(json.load(open('$CTL_B/arm-archive.json'))['run_dir'])")
RD_C=$(PATH="$STUBBED_PATH" python3 -c "import json;print(json.load(open('$CTL_C/arm-archive.json'))['run_dir'])")

# pristine commit BEFORE the patch; patch applied uncommitted
LOG=$(git -C "$RD_B/repo" log --format='%s')
case "$LOG" in
    "pristine fixture (pre-patch, harness-prepared)") pass "pristine commit exists before the patch";;
    *) fail "pristine commit message: $LOG";;
esac
if [ "$(git -C "$RD_B/repo" log --oneline | wc -l | tr -d ' ')" = "1" ]; then
    pass "exactly one commit (patch uncommitted on top)"
else
    fail "commit count wrong"
fi
if [ -n "$(git -C "$RD_B/repo" status --porcelain)" ]; then
    pass "setup patch applied as an uncommitted working-tree delta"
else
    fail "no working-tree delta after patch"
fi
if [ -z "$(git -C "$RD_B/repo" remote)" ]; then
    pass "fixture repo has no remotes"
else
    fail "fixture repo carries remotes"
fi
AUTHOR=$(git -C "$RD_B/repo" log --format='%an <%ae>')
case "$AUTHOR" in
    "summoner-eval-fixture <fixture@summoner.invalid>") pass "generic fixture author, isolated config";;
    *) fail "fixture author leaked: $AUTHOR";;
esac

# per-arm manifest variant
if cmp -s "$RD_B/repo/summoner.yaml" "$PAIRED/cases/frozen_base/summoner.yaml"; then
    pass "baseline repo keeps the byte-frozen v1 manifest"
else
    fail "baseline manifest is not the frozen v1 bytes"
fi
if cmp -s "$RD_C/repo/summoner.yaml" "$PAIRED/harness/manifests/summoner.v2.yaml"; then
    pass "candidate repo installs the harness-side v2 manifest"
else
    fail "candidate manifest is not the v2 golden"
fi

# answer isolation: truth/meta never inside $RUN
LEAKS=$(find "$RD_B" "$RD_C" -name truth.json -o -name meta.yaml 2>/dev/null | head -3)
if [ -z "$LEAKS" ]; then pass "truth/meta absent from \$RUN (answer isolation)"; else fail "leaked into RUN: $LEAKS"; fi

# staging allowlist: corpus-free AND scripts/ present
for d in "$RD_B/plugin" "$RD_C/plugin"; do
    ok=1
    [ -d "$d/scripts" ] || ok=0
    [ -f "$d/scripts/init-memory-db.sh" ] || ok=0
    for bad in evals scorers tests memory .superpowers .claude .git; do
        [ -e "$d/$bad" ] && ok=0
    done
    # harness entry scripts live under the allowlisted scripts/ path once
    # committed — they are eval infrastructure and must never stage
    # (test-paired-harness.sh embeds expected case outcomes)
    for hs in run-paired-eval.sh test-paired-harness.sh; do
        [ -e "$d/scripts/$hs" ] && ok=0
    done
    if [ "$ok" = "1" ]; then pass "staged plugin $(basename "$(dirname "$d")")/$(basename "$d"): scripts/ kept (init-memory-db), harness entry scripts + corpus dropped"
    else fail "staging allowlist violation in $d"; fi
done

# digests pinned and reproducible
DIG_B=$(PATH="$STUBBED_PATH" python3 -c "import json;print(json.load(open('$CTL_B/run-manifest.json'))['staging']['tree_digest'])")
DIG_C=$(PATH="$STUBBED_PATH" python3 -c "import json;print(json.load(open('$CTL_C/run-manifest.json'))['staging']['tree_digest'])")
RUN prepare g1-a baseline --run-id g1-a.baseline.selftest2 --results-dir "$RESULTS" --tmp "$TMPROOT" >/dev/null 2>&1
DIG_B2=$(PATH="$STUBBED_PATH" python3 -c "import json;print(json.load(open('$RESULTS/g1-a.baseline.selftest2/run-manifest.json'))['staging']['tree_digest'])")
if [ "$DIG_B" = "$DIG_B2" ]; then pass "staging is byte-reproducible (two stages, equal digests)"; else fail "staging digests differ"; fi

RECOMPUTED=$(PATH="$STUBBED_PATH" python3 -c "
import sys; sys.path.insert(0, '$PLUGIN_ROOT/evals/paired/harness')
import paired_eval as pe; print(pe.tree_digest('$CTL_B/pristine-repo'))")
PINNED=$(PATH="$STUBBED_PATH" python3 -c "import json;print(json.load(open('$CTL_B/run-manifest.json'))['inputs']['patched_tree_digest'])")
if [ "$RECOMPUTED" = "$PINNED" ]; then pass "patched-tree digest pinned in \$CONTROL matches a recompute"; else fail "patched-tree digest mismatch"; fi

# gate-script digest + per-arm channel declaration
for spec in "$CTL_B chat_prose baseline" "$CTL_C askuserquestion candidate"; do
    dir="${spec%% *}"; rest="${spec#* }"; want="${rest%% *}"; label="${rest##* }"
    GPD=$(PATH="$STUBBED_PATH" python3 -c "import json;m=json.load(open('$dir/run-manifest.json'));print(m['gate_policy_digest'] or '', m['channel'])")
    case "$GPD" in
        [0-9a-f]*" $want") pass "$label arm: gate-policy digest pinned + channel=$want declared";;
        *) fail "$label arm gate/channel record: $GPD";;
    esac
done

# settings format (M-1): denyRead = bare path globs; permissions.deny = rule strings
SETTINGS_FMT=$(PATH="$STUBBED_PATH" python3 -c "
import json
s = json.load(open('$RD_B/settings.json'))
dr, deny = s['sandbox']['filesystem']['denyRead'], s['permissions']['deny']
problems = []
for e in dr:
    if '(' in e or ')' in e: problems.append('denyRead rule-string: %s' % e)
    elif not (e.startswith('/') or e.startswith('~')): problems.append('denyRead non-path: %s' % e)
if not any(e.startswith('Read(') for e in deny): problems.append('permissions.deny lost Read() rules')
if not any(e.startswith('Edit(') for e in deny): problems.append('permissions.deny lost Edit() rules')
print('OK' if not problems else '; '.join(problems))")
[ "$SETTINGS_FMT" = "OK" ] && pass "settings: denyRead bare path globs + permissions.deny rule strings" || fail "settings format: $SETTINGS_FMT"

# host probes recorded from prepare
P4=$(PATH="$STUBBED_PATH" python3 -c "import json;print(json.load(open('$CTL_B/probes.json'))['preflight_phase'].get('P4',{}).get('status',''))")
P5B=$(PATH="$STUBBED_PATH" python3 -c "
import json; r = json.load(open('$CTL_B/probes.json'))['preflight_phase'].get('P5',{})
print(r.get('status',''), r.get('per_run_capture'), 'baseline_tmp_discovery_hit' in r)")
P5C=$(PATH="$STUBBED_PATH" python3 -c "
import json; r = json.load(open('$CTL_C/probes.json'))['preflight_phase'].get('P5',{})
print(r.get('status',''), r.get('per_run_capture'))")
[ "$P4" = "PASS" ] && pass "P4 host scan: zero corpus filenames" || fail "P4 status=$P4"
case "$P5B" in
    "PASS False True") pass "P5 baseline: per-run memory DB only; hook state disclosed as NOT per-run (own fields)";;
    *) fail "P5 baseline record: $P5B";;
esac
case "$P5C" in
    "PASS True") pass "P5 candidate: hook state write into \$RUN/state/s (per_run_capture=true)";;
    *) fail "P5 candidate record: $P5C";;
esac

# ---------------------------------------------------------------------------
section "6. settings schema validation catches malformed files pre-launch"
printf '{"askUserQuestionTimeout": 5, "permissions": "nope"}\n' > "$RD_C/settings.json"
OUT=$(RUN preflight g1-a --arm candidate --results-dir "$RESULTS" --tmp "$TMPROOT" 2>&1); RC=$?
expect_exit 1 "$RC" "preflight BLOCKS a malformed settings file"
case "$OUT" in *settings-schema*) pass "malformed settings named in preflight output";; *) fail "settings-schema check missing from output";; esac
RUN prepare g1-a candidate --run-id g1-a.candidate.selftest1 --results-dir "$RESULTS" --tmp "$TMPROOT" >/dev/null 2>&1

# ---------------------------------------------------------------------------
section "7. preflight green + expected-delta table"
OUT=$(RUN preflight g1-a --results-dir "$RESULTS" --tmp "$TMPROOT" 2>&1); RC=$?
expect_exit 0 "$RC" "preflight g1-a green (both arms)"
# anchored per-line: a `case "$OUT" in *…*PASS*` glob matches ACROSS newlines
# (run 4's false pass masked the v0.3.0 launcher BLOCKED for three CI rounds)
check_line() { printf '%s\n' "$OUT" | grep -q "^PASS  *$1"; }
check_line 'native-validator(g1-a/baseline)' && pass "baseline native validator passes on the frozen v1 manifest" || fail "baseline native validator"
check_line 'candidate-validator-on-v2' && pass "candidate validator passes on the v2 golden" || fail "candidate validator on v2"
check_line 'capability-expected-deltas' && pass "expected-delta table matches the pinned allowlist" || fail "expected-delta check"
OUT=$(RUN preflight g4-a 2>&1); RC=$?
expect_exit 0 "$RC" "preflight g4-a green (manifest-less probe disclosed)"
case "$OUT" in *g4-manifest-less-probe*) pass "g4 manifest-less probe recorded";; *) fail "g4 probe missing";; esac
DRIFT=$(PATH="$STUBBED_PATH" python3 -c "
import sys, copy; sys.path.insert(0, '$PLUGIN_ROOT/evals/paired/harness')
import paired_eval as pe
base = pe.resolve_ref('tag:v0.3.0')[1]
cand = pe.resolve_ref('commit:HEAD')[1]
table = pe.capability_delta_table(('ref', base), ('ref', cand))
table = copy.deepcopy(table)
for row in table['delta_rows']:
    if row['dimension'] == 'review.provider':
        row['baseline'] = 'skill:some-other-skill'
table['delta_rows'].append({'dimension': 'brand.new.dimension', 'baseline': 'a', 'candidate': 'b'})
problems = pe.check_expected_deltas(table)
print('OK' if len(problems) == 2 else 'problems=%d' % len(problems))")
[ "$DRIFT" = "OK" ] && pass "expected-delta drift trips BOTH directions (value drift + unpinned delta)" || fail "delta drift detection: $DRIFT"
GOLDEN=$(PATH="$STUBBED_PATH" python3 -c "
import sys; sys.path.insert(0, '$PLUGIN_ROOT/evals/paired/harness')
import paired_eval as pe
m = pe.load_yaml_file(pe.V2_MANIFEST)
print('OK' if m.get('version') == '2' and m.get('project', {}).get('name') == 'inventoryd'
      and m.get('phases', {}).get('docs', {}).get('skill') in (None, 'none') else 'BAD')")
[ "$GOLDEN" = "OK" ] && pass "v2 manifest golden: version/project/docs.skill values" || fail "v2 manifest golden"

# ---------------------------------------------------------------------------
section "8. stub pipeline: run -> gate records -> judge -> aggregate"
export PAIRED_EVAL_CLAUDE_CMD="$STUBBIN"
RUN prepare g2-a candidate --run-id g2-a.candidate.selftest-ok --results-dir "$RESULTS" --tmp "$TMPROOT" >/dev/null 2>&1
OUT=$(RUN run g2-a candidate --run-id g2-a.candidate.selftest-ok --results-dir "$RESULTS" --tmp "$TMPROOT" \
      --tolerances "$SANDBOX/tolerances.yaml" --budget "$SANDBOX/budget.md" 2>&1); RC=$?
expect_exit 0 "$RC" "stub run completes (g2-a candidate)"
STATUS=$(PATH="$STUBBED_PATH" python3 -c "import json;print(json.load(open('$RESULTS/g2-a.candidate.selftest-ok/run-status.json'))['status'])")
[ "$STATUS" = "RUN_COMPLETE" ] && pass "run-status RUN_COMPLETE" || fail "run-status=$STATUS"
PROBES_OK=$(PATH="$STUBBED_PATH" python3 -c "
import json
p = json.load(open('$RESULTS/g2-a.candidate.selftest-ok/probes.json'))
bad = [(k, v['status']) for phase in ('preflight_phase','run_phase') for k, v in (p.get(phase) or {}).items() if v['status'] != 'PASS']
print('OK' if not bad else bad)")
[ "$PROBES_OK" = "OK" ] && pass "P1-P7 probe plumbing all PASS under the stub (incl. P1 deny, P2 allow, P6 state-deny)" || fail "probe plumbing: $PROBES_OK"
[ -f "$RESULTS/g2-a.candidate.selftest-ok/gate-records.json" ] && pass "gate records written" || fail "gate-records.json missing"

OUT=$(RUN judge g2-a.candidate.selftest-ok --results-dir "$RESULTS" --tolerances "$SANDBOX/tolerances.yaml" --budget "$SANDBOX/budget.md" 2>&1); RC=$?
expect_exit 0 "$RC" "judge runs on the stub transcript"
METRICS=$(PATH="$STUBBED_PATH" python3 -c "
import json
m = json.load(open('$RESULTS/g2-a.candidate.selftest-ok/judge/metrics.json'))
problems = []
if m['status'] != 'JUDGED_DETERMINISTIC': problems.append('status=%s' % m['status'])
if sorted(m['blocking_cited']) != ['D1','D2','D3']: problems.append('blocking_cited=%s' % m['blocking_cited'])
if m['blocking_pending'] != []: problems.append('pending=%s' % m['blocking_pending'])
if m['blocking_total'] != 3: problems.append('total=%s' % m['blocking_total'])
if sorted(m['criteria_covered_det']) != ['C1','C2','C3']: problems.append('criteria=%s' % m['criteria_covered_det'])
if m['repeated_search_events'] != 1: problems.append('repeated=%s' % m['repeated_search_events'])
if m['elapsed_wall_s'] is None or m['elapsed_wall_s'] <= 0: problems.append('elapsed=%s' % m['elapsed_wall_s'])
if m['isolation_tier'] != 'permission': problems.append('tier=%s' % m['isolation_tier'])
if m['p9_status'] != 'N/A-not-required': problems.append('p9=%s' % m['p9_status'])
if m['adjudication'] != 'MATCH_PENDING': problems.append('adj=%s' % m['adjudication'])
if m['fp_confirmed_pending'] is not True: problems.append('fp not pending')
print('OK' if not problems else '; '.join(str(x) for x in problems))")
[ "$METRICS" = "OK" ] && pass "judge metric columns: D1-D3 cited deterministically, dwell/repeat/elapsed/tier columns present" || fail "judge metrics: $METRICS"

AGG=$(RUN aggregate g2-a.candidate.selftest-ok --results-dir "$RESULTS" 2>&1); RC=$?
expect_exit 0 "$RC" "aggregate without tolerances"
case "$AGG" in *"VERDICT_WITHHELD"*) pass "aggregate withholds the verdict without user tolerances";; *) fail "aggregate verdict line";; esac
AGG=$(RUN aggregate g2-a.candidate.selftest-ok --results-dir "$RESULTS" --tolerances "$SANDBOX/tolerances.yaml" 2>&1); RC=$?
expect_exit 0 "$RC" "aggregate with user-filled tolerances"
case "$AGG" in *"TOLERANCES_APPLIED"*) pass "aggregate applies the user tolerances (decision stays the user's)";; *) fail "aggregate with tolerances";; esac

# headless secondary channel stays BLOCKED until the user-approved gate-probe
OUT=$(RUN run g2-a candidate --run-id g2-a.candidate.selftest-ok --results-dir "$RESULTS" --tmp "$TMPROOT" \
      --headless --tolerances "$SANDBOX/tolerances.yaml" --budget "$SANDBOX/budget.md" 2>&1); RC=$?
expect_exit 4 "$RC" "headless secondary channel BLOCKED before the user-approved micro gate-probe"
case "$OUT" in *BLOCKED-channel*) pass "BLOCKED-channel message";; *) fail "BLOCKED-channel message missing";; esac

# ---------------------------------------------------------------------------
section "9. stub-runner honesty: empty transcript, probe failure, canary"
RUN prepare g2-b candidate --run-id g2-b.candidate.selftest-empty --results-dir "$RESULTS" --tmp "$TMPROOT" >/dev/null 2>&1
PAIRED_EVAL_STUB_MODE=empty RUN run g2-b candidate --run-id g2-b.candidate.selftest-empty --results-dir "$RESULTS" --tmp "$TMPROOT" \
    --tolerances "$SANDBOX/tolerances.yaml" --budget "$SANDBOX/budget.md" >/dev/null 2>&1; RC=$?
expect_exit 5 "$RC" "EMPTY transcript -> nonzero BLOCKED/EMPTY exit (never a score)"
ST=$(PATH="$STUBBED_PATH" python3 -c "import json;print(json.load(open('$RESULTS/g2-b.candidate.selftest-empty/run-status.json'))['status'])")
[ "$ST" = "BLOCKED" ] && pass "empty run recorded BLOCKED" || fail "empty run status=$ST"

RUN prepare g2-c candidate --run-id g2-c.candidate.selftest-liar --results-dir "$RESULTS" --tmp "$TMPROOT" >/dev/null 2>&1
PAIRED_EVAL_STUB_MODE=liar RUN run g2-c candidate --run-id g2-c.candidate.selftest-liar --results-dir "$RESULTS" --tmp "$TMPROOT" \
    --tolerances "$SANDBOX/tolerances.yaml" --budget "$SANDBOX/budget.md" >/dev/null 2>&1; RC=$?
expect_exit 7 "$RC" "isolation probe failure -> BLOCKED (never a score)"
DETAIL=$(PATH="$STUBBED_PATH" python3 -c "import json;print(json.load(open('$RESULTS/g2-c.candidate.selftest-liar/run-status.json'))['detail'])")
case "$DETAIL" in *P1*) pass "probe failure names P1 (read of CONTROL truth must be denied)";; *) fail "failure detail: $DETAIL";; esac
LIAR_RD=$(PATH="$STUBBED_PATH" python3 -c "import json;print(json.load(open('$RESULTS/g2-c.candidate.selftest-liar/arm-archive.json'))['run_dir'])")
expect_absent "$LIAR_RD/out/transcript.jsonl" "main launch SUPPRESSED after probe failure (no out/transcript.jsonl)"
case "$DETAIL" in *"main launch never ran"*) pass "run-status states the main launch never ran";; *) fail "suppression note missing: $DETAIL";; esac
OUT=$(RUN judge g2-c.candidate.selftest-liar --results-dir "$RESULTS" --tolerances "$SANDBOX/tolerances.yaml" --budget "$SANDBOX/budget.md" 2>&1); RC=$?
expect_exit 2 "$RC" "judge refuses to score a BLOCKED run"

RUN prepare g1-b candidate --run-id g1-b.candidate.selftest-canary --results-dir "$RESULTS" --tmp "$TMPROOT" >/dev/null 2>&1
PAIRED_EVAL_STUB_CANARY="$RESULTS/g1-b.candidate.selftest-canary/canary.txt" PAIRED_EVAL_STUB_MODE=canary \
    RUN run g1-b candidate --run-id g1-b.candidate.selftest-canary --results-dir "$RESULTS" --tmp "$TMPROOT" \
    --tolerances "$SANDBOX/tolerances.yaml" --budget "$SANDBOX/budget.md" >/dev/null 2>&1; RC=$?
expect_exit 6 "$RC" "canary token in transcript -> QUARANTINED"
ST=$(PATH="$STUBBED_PATH" python3 -c "import json;print(json.load(open('$RESULTS/g1-b.candidate.selftest-canary/run-status.json'))['status'])")
[ "$ST" = "QUARANTINED" ] && pass "contaminated run quarantined" || fail "quarantine status=$ST"

# ---------------------------------------------------------------------------
section "10. P9 tier + g4 worker arm (byte-pinned --agents file)"
RUN prepare g4-a worker --run-id g4-a.worker.selftest --results-dir "$RESULTS" --tmp "$TMPROOT" >/dev/null 2>&1; RC=$?
expect_exit 0 "$RC" "prepare g4-a/worker (repo: self, --agents arm)"
WD=$(PATH="$STUBBED_PATH" python3 -c "import json;print(json.load(open('$RESULTS/g4-a.worker.selftest/arm-archive.json'))['run_dir'])")
if cmp -s "$WD/worker-agents.yaml" "$PLUGIN_ROOT/agents/worker.md"; then
    pass "session-scoped --agents file is byte-identical to shipped worker.md"
else
    fail "--agents file not byte-pinned"
fi
OUT=$(RUN run g4-a worker --run-id g4-a.worker.selftest --results-dir "$RESULTS" --tmp "$TMPROOT" \
      --tolerances "$SANDBOX/tolerances.yaml" --budget "$SANDBOX/budget.md" 2>&1); RC=$?
expect_exit 0 "$RC" "stub run g4-a/worker with P9-tier probes"
# P9 (sealed sandbox tier) ships darwin-only: darwin must see a real denial
# PASS; linux sees an honest platform-unavailable BLOCKED that DISCLOSES in
# probes.json without suppressing the launch (exit 0 above).
if [ "$(uname)" = "Darwin" ]; then
    P9_WANT="PASS"; P9_WHY="bash-child corpus read denied + sandbox-probe wrapper observed"
else
    P9_WANT="BLOCKED"; P9_WHY="sealed tier unavailable on this platform — disclosed, launch not suppressed"
fi
P9=$(PATH="$STUBBED_PATH" python3 -c "import json;print(json.load(open('$RESULTS/g4-a.worker.selftest/probes.json'))['run_phase'].get('P9',{}).get('status',''))")
[ "$P9" = "$P9_WANT" ] && pass "P9 status=$P9 ($P9_WHY)" || fail "P9 status=$P9 (want $P9_WANT)"
OUT=$(RUN judge g4-a.worker.selftest --results-dir "$RESULTS" --tolerances "$SANDBOX/tolerances.yaml" --budget "$SANDBOX/budget.md" 2>&1); RC=$?
expect_exit 0 "$RC" "judge g4-a/worker"
K1=$(PATH="$STUBBED_PATH" python3 -c "
import json
m = json.load(open('$RESULTS/g4-a.worker.selftest/judge/metrics.json'))
print('OK' if m['unauthorized_edits_k1'] == [] and m['blocking_total'] == 5 else 'bad: %s %s' % (m['unauthorized_edits_k1'], m['blocking_total']))")
[ "$K1" = "OK" ] && pass "deterministic digest alarm: K1 files unchanged, blocking denominator = K-one + K-two items" || fail "g4 metrics: $K1"

# ---------------------------------------------------------------------------
section "11. g5 seeded producer + fork arm (P8 platform-session allowance)"
OUT=$(RUN prepare g5-b self-review --results-dir "$RESULTS" --tmp "$TMPROOT" 2>&1); RC=$?
expect_exit 2 "$RC" "g5 fork arm refuses before a producer exists"
RUN prepare g5-a producer --run-id g5-a.producer.selftest --results-dir "$RESULTS" --tmp "$TMPROOT" >/dev/null 2>&1; RC=$?
expect_exit 0 "$RC" "prepare g5-a producer"
PROD_RD=$(PATH="$STUBBED_PATH" python3 -c "import json;print(json.load(open('$RESULTS/g5-a.producer.selftest/arm-archive.json'))['run_dir'])")

# fake platform projects store: the freeze pins the PLATFORM session file by
# session-id match (PAIRED_EVAL_PROJECTS_DIR is the selftest seam). PROJROOT
# is canonicalized through python abspath — mktemp on darwin leaves a double
# slash in $SANDBOX (TMPDIR ends in /) that the harness's path handling
# collapses, and the two spellings must agree.
PROJROOT=$(S="$SANDBOX/projects" PATH="$STUBBED_PATH" python3 -c "import os;print(os.path.abspath(os.environ['S']))")
mkdir -p "$PROJROOT"
SLUG=$(PATH="$STUBBED_PATH" python3 -c "
import sys; sys.path.insert(0, '$PLUGIN_ROOT/evals/paired/harness')
import os, paired_eval as pe; print(os.path.basename(pe.session_slug_dir('$PROD_RD')))")
mkdir -p "$PROJROOT/$SLUG"
PINNED="$PROJROOT/$SLUG/stub-session-main.jsonl"
printf 'fake producer platform session\n' > "$PINNED"
printf 'sibling session one\n' > "$PROJROOT/$SLUG/other-session.jsonl"
printf 'sibling session two\n' > "$PROJROOT/$SLUG/yet-another.jsonl"
mkdir -p "$PROJROOT/$SLUG/subagents/nested"
printf 'nested child session\n' > "$PROJROOT/$SLUG/subagents/nested/child.jsonl"

OUT=$(PAIRED_EVAL_PROJECTS_DIR="$PROJROOT" RUN run g5-a producer --run-id g5-a.producer.selftest --results-dir "$RESULTS" --tmp "$TMPROOT" \
      --tolerances "$SANDBOX/tolerances.yaml" --budget "$SANDBOX/budget.md" 2>&1); RC=$?
expect_exit 0 "$RC" "stub producer run freezes (workspace digest re-asserted)"
PSTATE=$(PINNED="$PINNED" PATH="$STUBBED_PATH" python3 -c "
import json, os
p = json.load(open('$RESULTS/g5-a.producer.selftest/producer-state.json'))
ok = (p.get('status') == 'frozen' and p.get('session_id')
      and p.get('producer_history') == 'seeded'
      and p.get('session_file') == os.environ['PINNED']
      and p.get('session_file_digest') and 'transcript_copy' in p)
print('OK' if ok else p)")
[ "$PSTATE" = "OK" ] && pass "producer frozen: PLATFORM session file pinned by id (+digests, copy kept separate)" || fail "producer state: $PSTATE"

# fail-closed pin: without the platform file present, the freeze BLOCKs
PROJROOT2="$SANDBOX/projects-empty"
mkdir -p "$PROJROOT2"
OUT=$(PAIRED_EVAL_PROJECTS_DIR="$PROJROOT2" RUN run g5-a producer --run-id g5-a.producer.selftest --results-dir "$RESULTS" --tmp "$TMPROOT" \
      --tolerances "$SANDBOX/tolerances.yaml" --budget "$SANDBOX/budget.md" 2>&1); RC=$?
expect_exit 8 "$RC" "undiscoverable platform session -> producer freeze BLOCKED (never pins the harness copy)"

RUN prepare g5-a self-review --run-id g5-a.self-review.selftest --results-dir "$RESULTS" --tmp "$TMPROOT" >/dev/null 2>&1; RC=$?
expect_exit 0 "$RC" "prepare g5-a self-review (fork) after frozen producer"
FORK_RD=$(PATH="$STUBBED_PATH" python3 -c "import json;print(json.load(open('$RESULTS/g5-a.self-review.selftest/arm-archive.json'))['run_dir'])")
OUT=$(PAIRED_EVAL_PROJECTS_DIR="$PROJROOT" RUN run g5-a self-review --run-id g5-a.self-review.selftest --results-dir "$RESULTS" --tmp "$TMPROOT" \
      --tolerances "$SANDBOX/tolerances.yaml" --budget "$SANDBOX/budget.md" 2>&1); RC=$?
expect_exit 0 "$RC" "stub fork-arm run"
P8SETTINGS=$(PINNED="$PINNED" SIBS="$PROJROOT/$SLUG/other-session.jsonl:$PROJROOT/$SLUG/yet-another.jsonl:$PROJROOT/$SLUG/subagents/nested/child.jsonl" \
    FORK_SETTINGS="$FORK_RD/settings.json" PATH="$STUBBED_PATH" python3 -c "
import json, os
pinned, sibs = os.environ['PINNED'], os.environ['SIBS'].split(':')
s = json.load(open(os.environ['FORK_SETTINGS']))
allow, deny = s['permissions']['allow'], s['permissions']['deny']
problems = []
if 'Read(%s)' % pinned not in allow: problems.append('pinned path not in allow')
for sib in sibs:
    if 'Read(%s)' % sib not in deny: problems.append('sibling not individually denied: %s' % sib)
for e in deny:
    if not (e.startswith('Read(') and e.endswith(')')): continue
    inner = e[5:-1]
    if inner.endswith('/**') and pinned.startswith(inner[:-2]):
        problems.append('deny glob defeats the pinned allowance: %s' % e)
    elif inner == pinned:
        problems.append('deny entry pins-denies: %s' % e)
print('OK' if not problems else '; '.join(problems))")
case "$P8SETTINGS" in
    OK) pass "fork settings: pinned file allowed, every sibling denied, NO deny glob matches the pin";;
    *) fail "fork settings P8 allowance: $P8SETTINGS";;
esac
ARGV=$(PATH="$STUBBED_PATH" python3 -c "import json;print(' '.join(json.load(open('$RESULTS/g5-a.self-review.selftest/run-record.json'))['launch_argv']))")
case "$ARGV" in
    *"--resume stub-session-main --fork-session"*) pass "fork arm resumes the pinned producer with --fork-session";;
    *) fail "fork launch argv: $ARGV";;
esac
P8=$(PATH="$STUBBED_PATH" python3 -c "import json;print(json.load(open('$RESULTS/g5-a.self-review.selftest/probes.json'))['run_phase'].get('P8',{}).get('status',''))")
[ "$P8" = "PASS" ] && pass "P8: pinned producer session readable, siblings denied" || fail "P8 status=$P8"
FORBARE=$(PATH="$STUBBED_PATH" python3 -c "import json;a=json.load(open('$RESULTS/g5-a.self-review.selftest/run-record.json'))['launch_argv'];print('OK' if '--bare' not in a else 'bare!')")
[ "$FORBARE" = "OK" ] && pass "launch never uses --bare (plugin hooks are the runtime under test)" || fail "--bare in argv"

# ---------------------------------------------------------------------------
section "12. gate-join semantics + fix-round unit repros"
# unit repros first (probe classification + parser), against the live module
UNIT=$(PATH="$STUBBED_PATH" python3 "$SANDBOX/unit-checks.py" "$PLUGIN_ROOT")
[ "$UNIT" = "OK" ] && pass "unit repros: EISDIR/crash FAIL, real denial PASS, duplicate keys raise" || fail "unit repros: $UNIT"

# admitted secondary-channel policy (simulates the post-micro-gate-probe state)
sed -e 's/^  name: ""$/  name: "selftest-operator"/' -e 's/^  admitted: false$/  admitted: true/' \
    "$PAIRED/gate-policy.yaml" > "$SANDBOX/gate-policy-admitted.yaml"

plant_checkpoints() { # $1 = run_dir: one decided checkpoint in the state trace
    mkdir -p "$1/state/t"
    printf '{"type":"checkpoint.opened","timestamp":"2026-11-11T10:00:10Z","payload":{"checkpoint_id":"cp-acceptance"}}\n{"type":"checkpoint.decided","timestamp":"2026-11-11T10:00:12Z","payload":{"checkpoint_id":"cp-acceptance"}}\n' \
        > "$1/state/t/checkpoints.jsonl"
}
rd_of() { PATH="$STUBBED_PATH" python3 -c "import json;print(json.load(open('$RESULTS/$1/arm-archive.json'))['run_dir'])"; }

# (a) headless g1 with a decided gate: mechanically-answered acceptance gate
RUN prepare g1-a candidate --run-id g1-a.candidate.selftest-hg1 --results-dir "$RESULTS" --tmp "$TMPROOT" >/dev/null 2>&1
plant_checkpoints "$(rd_of g1-a.candidate.selftest-hg1)"
OUT=$(PAIRED_EVAL_GATE_POLICY="$SANDBOX/gate-policy-admitted.yaml" RUN run g1-a candidate \
      --run-id g1-a.candidate.selftest-hg1 --results-dir "$RESULTS" --tmp "$TMPROOT" --headless \
      --tolerances "$SANDBOX/tolerances.yaml" --budget "$SANDBOX/budget.md" 2>&1); RC=$?
expect_exit 0 "$RC" "headless g1 run completes under the admitted secondary channel"
ANS=$(PATH="$STUBBED_PATH" python3 -c "import json;r=json.load(open('$RESULTS/g1-a.candidate.selftest-hg1/gate-records.json'))['records'];print(r[0]['answerer'] if r else 'none')")
[ "$ANS" = "secondary-channel" ] && pass "headless gate record derives answerer=secondary-channel" || fail "headless answerer: $ANS"
OUT=$(RUN judge g1-a.candidate.selftest-hg1 --results-dir "$RESULTS" \
      --tolerances "$SANDBOX/tolerances.yaml" --budget "$SANDBOX/budget.md" 2>&1); RC=$?
expect_exit 9 "$RC" "headless g1 decided gate -> mechanically-answered acceptance gate -> judge BLOCKS (exit 9)"
HG1V=$(PATH="$STUBBED_PATH" python3 -c "
import json; m = json.load(open('$RESULTS/g1-a.candidate.selftest-hg1/judge/metrics.json'))
print('OK' if m['protocol_violations'] and m['status'] == 'BLOCKED' and m['run_mode'] == 'headless' else m)")
[ "$HG1V" = "OK" ] && pass "judge records the violation (protocol_violations + status BLOCKED + run_mode headless)" || fail "headless g1 metrics: $HG1V"

# (b) attended twin: the operator answers, no violation
RUN prepare g1-a candidate --run-id g1-a.candidate.selftest-att1 --results-dir "$RESULTS" --tmp "$TMPROOT" >/dev/null 2>&1
plant_checkpoints "$(rd_of g1-a.candidate.selftest-att1)"
OUT=$(RUN run g1-a candidate --run-id g1-a.candidate.selftest-att1 --results-dir "$RESULTS" --tmp "$TMPROOT" \
      --tolerances "$SANDBOX/tolerances.yaml" --budget "$SANDBOX/budget.md" 2>&1); RC=$?
expect_exit 0 "$RC" "attended twin run completes"
ANS=$(PATH="$STUBBED_PATH" python3 -c "import json;r=json.load(open('$RESULTS/g1-a.candidate.selftest-att1/gate-records.json'))['records'];print(r[0]['answerer'] if r else 'none')")
[ "$ANS" = "selftest-operator" ] && pass "attended gate record keeps the named operator" || fail "attended answerer: $ANS"
OUT=$(RUN judge g1-a.candidate.selftest-att1 --results-dir "$RESULTS" \
      --tolerances "$SANDBOX/tolerances.yaml" --budget "$SANDBOX/budget.md" 2>&1); RC=$?
expect_exit 0 "$RC" "attended g1 with decided gates judges clean (human answered)"
ATT1V=$(PATH="$STUBBED_PATH" python3 -c "import json;m=json.load(open('$RESULTS/g1-a.candidate.selftest-att1/judge/metrics.json'));print('OK' if m['protocol_violations']==[] else m['protocol_violations'])")
[ "$ATT1V" = "OK" ] && pass "attended twin: protocol_violations empty" || fail "attended violations: $ATT1V"

# (c) headless NON-g1 with decided gates: the sanctioned secondary channel
RUN prepare g2-a candidate --run-id g2-a.candidate.selftest-hl1 --results-dir "$RESULTS" --tmp "$TMPROOT" >/dev/null 2>&1
plant_checkpoints "$(rd_of g2-a.candidate.selftest-hl1)"
OUT=$(PAIRED_EVAL_GATE_POLICY="$SANDBOX/gate-policy-admitted.yaml" RUN run g2-a candidate \
      --run-id g2-a.candidate.selftest-hl1 --results-dir "$RESULTS" --tmp "$TMPROOT" --headless \
      --tolerances "$SANDBOX/tolerances.yaml" --budget "$SANDBOX/budget.md" 2>&1); RC=$?
expect_exit 0 "$RC" "headless non-g1 run completes"
OUT=$(RUN judge g2-a.candidate.selftest-hl1 --results-dir "$RESULTS" \
      --tolerances "$SANDBOX/tolerances.yaml" --budget "$SANDBOX/budget.md" 2>&1); RC=$?
expect_exit 0 "$RC" "headless non-g1 decided gates are the sanctioned channel (no exit 9)"

# (d) emitted record fields == policy record_fields exactly
FIELDS=$(PATH="$STUBBED_PATH" python3 -c "
import sys; sys.path.insert(0, '$PLUGIN_ROOT/evals/paired/harness')
import json, paired_eval as pe
fields = pe.load_yaml_file(pe.gate_policy_path())['record_fields']
rec = json.load(open('$RESULTS/g1-a.candidate.selftest-hg1/gate-records.json'))['records'][0]
print('OK' if sorted(rec.keys()) == sorted(fields) else 'policy=%s record=%s' % (sorted(fields), sorted(rec.keys())))")
[ "$FIELDS" = "OK" ] && pass "gate-record keys match policy record_fields exactly (dwell_s)" || fail "record fields: $FIELDS"

# ---------------------------------------------------------------------------
printf -- '--\n'
if [ "$FAIL" -eq 0 ]; then
    echo "✓ paired-harness selftest green ($PASS assertions)"
    exit 0
fi
echo "✗ paired-harness selftest RED: $FAIL failed assertion(s), $PASS passed"
exit 1
