#!/bin/bash
# post-game-check.sh — post-game closure scorer (spec §0 裁决 4, §5 post-game
# 不豁免, §7 post_game.*).
#
# Audits the closure semantics of a v2 trace:
#   - CLOSED only through the post-game: workflow.ended requires an earlier
#     post_game.completed (the CLOSED writer CompletePostGame emits
#     post_game.completed THEN workflow.ended, each exactly once)
#   - emergency stop stays POST_GAME_PENDING: post_game.started
#     kind=emergency_minimal writes the minimal exit journal and WAITS —
#     closing it requires the resumed questionnaire evidence (an
#     AskUserQuestion answer) between the emergency start and
#     post_game.completed; an instant close is a PENDING violation
#   - post_game.started carries kind normal|emergency_minimal and every
#     post_game.completed has a preceding post_game.started
#   - a normal post-game that never completes by end of trace is MISSING
#     closure evidence; an emergency one still PENDING is the designed
#     honest state (PASS — 补答 happens on resume)
#
# Exit codes: 0 PASS · 1 FAIL · 2 N/A (justified) · 3 MISSING (evidence).

set -u

if [[ $# -ne 1 || ! -f "$1" ]]; then
  echo "usage: post-game-check.sh <trace.jsonl>"
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

pg_started = []   # (seq, kind)
pg_completed = [] # seq
wf_ended = []     # seq
wf_aborted = []   # seq
ask_seqs = []     # AskUserQuestion tool.started seqs
for seq, ev in events:
    typ = ev.get("type")
    if typ == "post_game.started":
        pg_started.append((seq, payload(ev).get("kind")))
    elif typ == "post_game.completed":
        pg_completed.append(seq)
    elif typ == "workflow.ended":
        wf_ended.append(seq)
    elif typ == "workflow.aborted":
        wf_aborted.append(seq)
    elif typ == "tool.started" and payload(ev).get("tool_name") == "AskUserQuestion":
        ask_seqs.append(seq)

if not pg_started and not pg_completed and not wf_ended and not wf_aborted:
    print("N/A — no post-game or terminal events recorded (the run is in "
          "flight): the closure surface does not exist yet in this trace")
    sys.exit(2)

# --- started shape ----------------------------------------------------------------
for seq, kind in pg_started:
    if kind not in ("normal", "emergency_minimal"):
        fail(f"post_game.started at event #{seq} carries kind {kind!r} — "
             "want normal|emergency_minimal")

# --- completed requires a started -------------------------------------------------
for cseq in pg_completed:
    prior = [(s, k) for s, k in pg_started if s < cseq]
    if not prior:
        fail(f"post_game.completed at event #{cseq} has no preceding "
             "post_game.started — the ledger's kind is authoritative before "
             "any closure")
    elif prior[-1][1] == "emergency_minimal":
        # 补答: the resumed session answers the questionnaire (q1..q4 via
        # platform AskUserQuestion) before the pending post-game may close
        answered = [s for s in ask_seqs if prior[-1][0] < s < cseq]
        if not answered:
            fail(f"post_game.completed at event #{cseq} closes an emergency "
                 "stop with no questionnaire answer in between — an "
                 "emergency stop leaves POST_GAME_PENDING and closes only "
                 "after the resumed questionnaire (最小 journal + 补答)")

# --- workflow.ended requires the post-game writer -----------------------------------
for eseq in wf_ended:
    prior = [s for s in pg_completed if s < eseq]
    if not prior:
        fail(f"workflow.ended at event #{eseq} without a preceding "
             "post_game.completed — CLOSED comes only through the post-game "
             "writer (正常结束须问卷+journal 持久化后才 CLOSED)")

# --- pending discipline at end of trace ----------------------------------------------
if wf_ended:
    kind_note = ""
    if pg_started:
        kind_note = f" (post-game kind: {pg_started[-1][1]})"
    print(f"PASS — run terminated through the post-game writer{kind_note}: "
          "post_game.completed precedes workflow.ended")
    sys.exit(0)
if wf_aborted:
    print("PASS — run aborted (workflow.aborted terminal): the abort path "
          "carries no post-game requirement in the trace")
    sys.exit(0)

if pg_started and pg_started[-1][1] == "normal" and not pg_completed:
    print("MISSING — post_game.started(kind=normal) is recorded but the run "
          "never closes (no post_game.completed): the questionnaire + "
          "journal persistence evidence is missing")
    sys.exit(3)

if pg_started and pg_started[-1][1] == "emergency_minimal" and not pg_completed:
    print("PASS — emergency stop left the run POST_GAME_PENDING with the "
          "minimal exit journal: closure honestly deferred to the resumed "
          "questionnaire (补答)")
    sys.exit(0)

if not pg_started and not pg_completed:
    print("N/A — no post-game events recorded (the run is in flight): the "
          "closure surface does not exist yet in this trace")
    sys.exit(2)

print("PASS — post-game closure discipline holds "
      f"(started ×{len(pg_started)}, completed ×{len(pg_completed)})")
sys.exit(0)
PYEOF
