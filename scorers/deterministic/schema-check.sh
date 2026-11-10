#!/bin/bash
# schema-check.sh — v2 trace schema scorer (spec §7 单一 trace schema).
#
# Audits the EVENT-SET and CONTROL-EVENT semantics of a v2 JSONL trace:
#   - every line is valid JSON with schema_version "2" and one stable run_id
#   - every `type` is in the v2 allow-list (mirrored from
#     hooks/shared/trace.go allEventTypes — the single sanctioned set; keep
#     the two lists in sync deliberately: the Go slice is authority, this is
#     the audit mirror)
#   - seq is 1..N strictly increasing, workflow.started is the first event
#   - terminal discipline: post_game.completed and workflow.ended appear at
#     most once each (T18: the CLOSED writer emits each exactly once), and
#     ended+aborted never coexist
#   - prev_hash/hash fields are PRESENT on every line (values are Go-owned —
#     this scorer never recomputes the chain)
#
# Scorers do NOT verify the hash chain: crypto verification is Go-owned
# (shared.VerifyChain). This is the semantic audit layer.
#
# Exit codes: 0 PASS · 1 FAIL · 2 N/A (justified) · 3 MISSING (evidence).

set -u

if [[ $# -ne 1 || ! -f "$1" ]]; then
  echo "usage: schema-check.sh <trace.jsonl>"
  exit 1
fi

python3 - "$1" <<'PYEOF'
import json, sys

# The v2 allow-list, mirroring hooks/shared/trace.go allEventTypes (38 types:
# 33 spec §7 originals + checkpoint.decision_rejected + hooks.disabled +
# criterion.dropped + budget.confirmed + budget.extended).
ALLOWED = {
    "workflow.started", "workflow.ended", "workflow.aborted",
    "phase.started", "phase.closed", "phase_submitted.rejected",
    "checkpoint.opened", "checkpoint.displayed", "checkpoint.decided",
    "checkpoint.decision_rejected", "criterion.dropped",
    "recall.applied",
    "change.opened", "change.delta_applied", "change.resumed",
    "tool.started", "tool.result", "tool.failed",
    "tool.outcome_unknown", "tool.resolved",
    "check.result",
    "review.prepared", "review.started", "review.verdict",
    "review.failed", "review.blocked", "review.superseded",
    "delegation.started", "delegation.completed", "delegation.failed",
    "parallel.opened", "parallel.joined",
    "agent.telemetry",
    "post_game.started", "post_game.completed",
    "budget.confirmed", "budget.extended",
    "hooks.disabled",
}

path = sys.argv[1]
events = []          # (line_no, event_dict)
with open(path, encoding="utf-8") as f:
    for n, raw in enumerate(f, 1):
        line = raw.strip()
        if not line:
            continue
        try:
            ev = json.loads(line)
        except json.JSONDecodeError as e:
            print(f"FAIL — line {n} is not valid JSON: {e}")
            sys.exit(1)
        if not isinstance(ev, dict):
            print(f"FAIL — line {n} is not a JSON object")
            sys.exit(1)
        events.append((n, ev))

if not events:
    print("N/A — trace is empty (nothing has happened or capture has not started)")
    sys.exit(2)

def fail(msg):
    print(f"FAIL — {msg}")
    sys.exit(1)

# --- per-line shape ---------------------------------------------------------
run_ids = set()
for n, ev in events:
    typ = ev.get("type")
    if typ not in ALLOWED:
        fail(f'line {n}: event type "{typ}" is not in the v2 allow-list '
             f"(see hooks/shared/trace.go allEventTypes)")
    if ev.get("schema_version") != "2":
        fail(f'line {n} ({typ}): schema_version must be "2" '
             f'(got {ev.get("schema_version")!r})')
    if not ev.get("run_id"):
        fail(f"line {n} ({typ}): run_id is missing")
    run_ids.add(ev.get("run_id"))
    if "prev_hash" not in ev or "hash" not in ev:
        fail(f"line {n} ({typ}): prev_hash/hash chain fields must be present "
             "(values are verified by Go, presence is part of the line shape)")
    if not ev.get("timestamp"):
        fail(f"line {n} ({typ}): timestamp is missing")

if len(run_ids) > 1:
    fail(f"trace mixes {len(run_ids)} run_ids ({sorted(run_ids)}) — "
         "one file is one run")

# --- seq discipline ----------------------------------------------------------
seqs = [ev.get("seq") for _, ev in events]
for i, (n, ev) in enumerate(events):
    want = i + 1
    if ev.get("seq") != want:
        fail(f"line {n} ({ev.get('type')}): seq must count 1..N strictly "
             f"increasing (got {ev.get('seq')!r}, want {want})")

# --- key control events --------------------------------------------------------
first_type = events[0][1].get("type")
if first_type != "workflow.started":
    counts = sum(1 for _, ev in events if ev.get("type") == "workflow.started")
    if counts == 0:
        fail(f"trace has {len(events)} events but no workflow.started "
             "(a run records its start before anything else)")
    fail("workflow.started must be the first event of the trace")

ended = [n for n, ev in events if ev.get("type") == "workflow.ended"]
aborted = [n for n, ev in events if ev.get("type") == "workflow.aborted"]
completed = [n for n, ev in events if ev.get("type") == "post_game.completed"]
if len(ended) > 1:
    fail("workflow.ended must appear exactly once "
         f"(the CLOSED writer emits it once; found {len(ended)})")
if len(aborted) > 1:
    fail(f"workflow.aborted must appear at most once (found {len(aborted)})")
if len(completed) > 1:
    fail("post_game.completed must appear exactly once "
         f"(the CLOSED writer emits it once; found {len(completed)})")
if ended and aborted:
    fail("workflow.ended and workflow.aborted never coexist "
         "(a run closes through the post-game writer or aborts — not both)")

# --- terminal presence: N/A vs MISSING strictly distinguished ------------------
if not (ended or aborted or completed):
    started_pg = [ev for _, ev in events if ev.get("type") == "post_game.started"]
    if started_pg and started_pg[-1].get("payload", {}).get("kind") == "normal":
        print("MISSING — post_game.started(kind=normal) is recorded but no "
              "terminal event (post_game.completed / workflow.ended / "
              "workflow.aborted) closes the run: closure evidence is missing")
        sys.exit(3)
    if started_pg:
        print("N/A — emergency stop left the run POST_GAME_PENDING "
              "(post_game.started kind=emergency_minimal, no terminal yet — "
              "the questionnaire is answered on resume, by design)")
        sys.exit(2)
    print("N/A — no terminal event yet: the run is in flight or crashed "
          "(the recorded events are otherwise schema-sound)")
    sys.exit(2)

print(f"PASS — {len(events)} events, all in the v2 allow-list; "
      "workflow.started first; terminal discipline holds")
sys.exit(0)
PYEOF
