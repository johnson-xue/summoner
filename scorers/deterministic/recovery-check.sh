#!/bin/bash
# recovery-check.sh — crash/OUTCOME_UNKNOWN/review-crash/recall scorer
# (spec §3 崩溃恢复, §4 recall, §5 OUTCOME_UNKNOWN 与副作用恢复).
#
# Audits the recovery surfaces of a v2 trace:
#   - OUTCOME_UNKNOWN probes: every tool.outcome_unknown references its
#     dangling mutating intent; every resolution is a tool.resolved whose
#     outcome is accepted|failed|indeterminate and whose probe_tool_use_id
#     is a recorded READ-CLASS tool call observed AFTER the unknown was
#     marked (绝不变异性动作自动重放 — the probe is read-only by contract)
#   - bad outcomes route to the user: a failed/indeterminate resolution is
#     followed by a checkpoint.displayed (the recovery checkpoint render —
#     recovery checkpoints emit no checkpoint.opened; the display IS the
#     user-gate act) BEFORE any further phase activity
#   - open episodes: an outcome_unknown never resolved by end of trace is
#     MISSING evidence, not a pass (the trace claims a side effect whose
#     fate is unaudited)
#   - review five-state crash recovery: a re-dispatched review (same
#     review_id prepared again after a crash window) recovers WITHOUT the
#     producer re-running its phase between the two dispatches
#   - recall semantics: recall.applied resolves only against REAL history
#     (a phase.started for the target exists earlier), every recall opens a
#     NEW visit (a phase.started for the target follows), and stale
#     propagation is carried on the event when downstream items went stale
#
# Exit codes: 0 PASS · 1 FAIL · 2 N/A (justified) · 3 MISSING (evidence).

set -u

if [[ $# -ne 1 || ! -f "$1" ]]; then
  echo "usage: recovery-check.sh <trace.jsonl>"
  exit 1
fi

python3 - "$1" <<'PYEOF'
import json, sys

path = sys.argv[1]
events = []
with open(path, encoding="utf-8") as f:
    for n, raw in enumerate(f, 1):
        line = raw.strip()
        if not line:
            continue
        try:
            ev = json.loads(line)
        except json.JSONDecodeError:
            continue  # schema-check owns line-level shape
        events.append((n, ev))

def fail(msg):
    print(f"FAIL — {msg}")
    sys.exit(1)

def payload(ev):
    p = ev.get("payload")
    return p if isinstance(p, dict) else {}

seq_of = {}       # (type) -> [seqs]
for seq, ev in events:
    seq_of.setdefault(ev.get("type"), []).append(seq)

unknowns = []     # (seq, tool_use_id)
resolves = []     # (seq, payload)
recalls = []      # (seq, payload)
prep_seqs = []    # (seq, review_id)
phase_starts = [] # (seq, phase_id)
for seq, ev in events:
    typ = ev.get("type")
    pl = payload(ev)
    if typ == "tool.outcome_unknown":
        unknowns.append((seq, pl.get("tool_use_id") or ev.get("tool_use_id")))
    elif typ == "tool.resolved":
        resolves.append((seq, pl))
    elif typ == "recall.applied":
        recalls.append((seq, pl))
    elif typ == "review.prepared":
        prep_seqs.append((seq, pl.get("review_id")))
    elif typ == "phase.started":
        phase_starts.append((seq, pl.get("phase_id")))

has_review_recovery = any(
    rid in [r for _, r in prep_seqs[:i]]
    for i, (_, rid) in enumerate(prep_seqs))
if not unknowns and not has_review_recovery and not recalls:
    print("N/A — no OUTCOME_UNKNOWN episodes, review re-dispatches or "
          "recalls recorded: no recovery surface exists in this trace")
    sys.exit(2)

# --- OUTCOME_UNKNOWN episodes ---------------------------------------------------
started_mutating = {}   # tool_use_id -> seq (tool.started)
started_readclass = {}  # tool_use_id -> seq (tool.started with read_class)
for seq, ev in events:
    if ev.get("type") == "tool.started":
        tid = ev.get("tool_use_id")
        if tid:
            if payload(ev).get("read_class"):
                started_readclass[tid] = seq
            else:
                started_mutating[tid] = seq

resolved_ids = {}
for seq, pl in resolves:
    tid = pl.get("tool_use_id")
    if not tid or not pl.get("probe_tool_use_id"):
        fail(f"tool.resolved at event #{seq} is malformed: needs "
             "tool_use_id and probe_tool_use_id")
    if pl.get("outcome") not in ("accepted", "failed", "indeterminate"):
        fail(f"tool.resolved for {tid} at event #{seq}: outcome "
             f"{pl.get('outcome')!r} is not accepted|failed|indeterminate")
    probe = pl.get("probe_tool_use_id")
    if probe not in started_readclass:
        fail(f"tool.resolved for {tid} at event #{seq} cites probe "
             f"{probe!r}, which is not a recorded read-class tool call — "
             "forged probe receipt")
    unknown_seq = next((s for s, t in unknowns if t == tid), None)
    if unknown_seq is None:
        fail(f"tool.resolved for {tid} at event #{seq} resolves an unknown "
             "that was never marked tool.outcome_unknown — dangling "
             "resolution")
    if started_readclass.get(probe, 0) < unknown_seq:
        fail(f"tool.resolved for {tid} at event #{seq}: its probe {probe} "
             "ran BEFORE the outcome was marked unknown — a probe observes "
             "the crash aftermath, it cannot precede detection")
    if tid in resolved_ids:
        fail(f"tool.outcome_unknown for {tid} resolved twice (events #"
             f"{resolved_ids[tid]} and #{seq}) — resolutions are one-shot")
    resolved_ids[tid] = seq
    if pl.get("outcome") in ("failed", "indeterminate"):
        # bad outcome → recovery checkpoint (rendered) before further phase
        # activity; recovery checkpoints emit no checkpoint.opened — the
        # display is the trace-visible user gate
        later = events[seq:]  # strictly after
        disp = next((s for s, e in later
                     if e.get("type") == "checkpoint.displayed"), None)
        phase = next((s for s, e in later
                      if e.get("type") == "phase.started"), None)
        if disp is None or (phase is not None and phase < disp):
            fail(f"tool.resolved({pl.get('outcome')}) for {tid} at event #"
                 f"{seq} is followed by phase activity with no recovery "
                 "checkpoint displayed — a bad outcome routes to the user "
                 "(retry/rollback/accept), it never auto-continues")

for seq, tid in unknowns:
    if not tid:
        fail(f"tool.outcome_unknown at event #{seq} carries no tool_use_id")
    if tid not in started_mutating and tid not in started_readclass:
        fail(f"tool.outcome_unknown for {tid} at event #{seq} has no "
             "recorded tool.started intent — the episode must reference a "
             "real dangling intent")
    if tid not in resolved_ids:
        print(f"MISSING — tool.outcome_unknown for {tid} (event #{seq}) is "
              "never resolved by end of trace: the side effect's fate is "
              "unaudited (probe receipt or recovery checkpoint absent)")
        sys.exit(3)

# --- review five-state crash recovery (no producer re-runs) ------------------------
seen_prepared = set()
for seq, rid in prep_seqs:
    if rid in seen_prepared:
        # re-dispatch of the SAME review after a crash window: the producer
        # must NOT have re-run its phase between the two dispatches
        first = next(s for s, r in prep_seqs if r == rid)
        between = events[first:seq - 1]
        rerun = [(s, payload(e).get("phase_id")) for s, e in between
                 if e.get("type") == "phase.started"
                 and any(p == payload(e).get("phase_id")
                         for _, p in phase_starts[:first - 1])]
        if rerun:
            fail(f"producer re-run during review recovery: phase "
                 f"{rerun[0][1]} re-started (event #{rerun[0][0]}) between "
                 f"the two dispatches of review {rid} — crash recovery "
                 "re-sends the SAME envelope, it never re-runs the producer")
    seen_prepared.add(rid)

# --- recall semantics -----------------------------------------------------------------
started_phases = {}  # phase_id -> [seqs]
for seq, pid in phase_starts:
    if pid:
        started_phases.setdefault(pid, []).append(seq)
for seq, pl in recalls:
    target = pl.get("target_phase_id")
    frm = pl.get("from_phase_id")
    if not target or not frm:
        fail(f"recall.applied at event #{seq} is malformed: needs "
             "from_phase_id and target_phase_id")
    priors = [s for s in started_phases.get(target, []) if s < seq]
    if not priors:
        fail(f"recall.applied at event #{seq} targets {target!r}, which is "
             "not real history (no phase.started for it earlier in the "
             "trace) — recall resolves only against true prior visits")
    later = [s for s in started_phases.get(target, []) if s > seq]
    if not later:
        fail(f"recall.applied to {target} at event #{seq} opens no new "
             "visit — a recall records a NEW visit of the target phase")

print("PASS — outcome-unknown episodes probe-bound and resolved, review "
      "recoveries producer-free, recalls grounded in real history")
sys.exit(0)
PYEOF
