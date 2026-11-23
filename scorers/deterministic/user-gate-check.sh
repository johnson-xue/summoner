#!/bin/bash
# user-gate-check.sh — checkpoint user-gate scorer (spec §1.3/1.4, §4).
#
# Audits the human-gate semantics of a v2 trace:
#   - 展示绑定 (display binding): every checkpoint.decided has an EARLIER
#     checkpoint.displayed for the same checkpoint_id — a decision on a
#     checkpoint that was never rendered is invalid
#   - 决策绑定 (decision binding): every checkpoint.decided binds a
#     tool_use_id that is a recorded AskUserQuestion tool.started earlier in
#     the trace — a decision not carried by a platform AskUserQuestion call
#     is an injected answer (注入 answers), never a human decision
#   - nonce: when a checkpoint.opened exists for a decided checkpoint, the
#     opened nonce must be non-empty and equal the displayed nonce (the
#     question-side nonce presence is platform input audited by the hooks'
#     contract tests; the trace-side correlate is nonce presence + equality
#     here)
#   - 幂等 (idempotency): one decision per checkpoint (a replayed callback
#     is a silent no-op at the reducer — a second checkpoint.decided for the
#     same id means the recording is not idempotent), and one platform call
#     (tool_use_id) decides at most one checkpoint
#   - stale 渲染: no change.delta_applied lands between a checkpoint's
#     display and its decision — deciding pre-change content is stale
#   - ride-along answers: criterion.dropped / budget.confirmed /
#     budget.extended bind the SAME answering AskUserQuestion tool_use_id;
#     drops are per-criterion-id and recorded once; budget.confirmed applies
#     at most once per run (replays are silent)
#   - auto-passed checkpoints (0.7.0 adaptive checkpoints): checkpoint.auto is
#     the framework accepting a phase-submit checkpoint with NO user decision.
#     Legal only on the phases the policy exempts from the human gate
#     (context / reproduce / verify / regression), only for a CONFIRMED
#     submission, at most once per checkpoint, and it must be the
#     checkpoint's ONLY conclusion — a checkpoint that was displayed or
#     decided can never also be auto-passed, and nothing rides it (no
#     criterion drop, no budget extension). Its shape is exact (the trace
#     protocol): opened → auto — the auto names a checkpoint that was
#     OPENED before, for the same phase; it never rides an AskUserQuestion
#     call (that id belongs to a human answer); and a second auto-pass
#     needs a phase.started in between (every auto-pass closes a phase that
#     was opened first).
#
# POSITIONAL CONTRACT (T10 carry, binding): the change-confirm checkpoint
# (and review-result / recovery checkpoints) emits NO checkpoint.opened —
# only the phase-submit path opens checkpoints. The change-confirm
# checkpoint is identified POSITIONALLY: its id is the one change.resumed
# names. This scorer never requires a checkpoint.opened pairing for it
# (displayed-before-decided still applies).
#
# Exit codes: 0 PASS · 1 FAIL · 2 N/A (justified) · 3 MISSING (evidence).

set -u

if [[ $# -ne 1 || ! -f "$1" ]]; then
  echo "usage: user-gate-check.sh <trace.jsonl>"
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

# The phases whose checkpoint the framework may pass without a human
# decision — the policy mirror of shared.AutoPassablePhase (0.7.0 D3).
AUTO_PASS_PHASES = {"context", "reproduce", "verify", "regression"}

# --- collect the gate surface ---------------------------------------------------
opened = {}      # checkpoint_id -> (seq, nonce, phase_id)
displayed = {}   # checkpoint_id -> seq (first display wins)
decided = []     # (seq, checkpoint_id, tool_use_id)
autos = []       # (seq, checkpoint_id, phase_id, outcome, tool_use_id)
phase_started_seqs = [] # seqs of phase.started
ask_calls = {}   # tool_use_id -> seq of AskUserQuestion tool.started
delta_seqs = []  # change.delta_applied seqs
change_seqs = [] # change.opened/delta_applied seqs (supersession evidence)
resumed_cps = set()
drops = []       # (seq, checkpoint_id, criterion_id, tool_use_id)
budget_confirmed = []
budget_extended = []
for seq, ev in events:
    typ = ev.get("type")
    pl = payload(ev)
    if typ == "checkpoint.opened":
        cid = pl.get("checkpoint_id")
        if cid:
            if cid in opened:
                fail(f"checkpoint.opened for {cid} recorded twice — a "
                     "checkpoint opens once")
            opened[cid] = (seq, pl.get("nonce"), pl.get("phase_id"))
    elif typ == "checkpoint.displayed":
        cid = pl.get("checkpoint_id")
        if cid and cid not in displayed:
            displayed[cid] = seq
    elif typ == "checkpoint.decided":
        decided.append((seq, pl.get("checkpoint_id"), ev.get("tool_use_id")))
    elif typ == "checkpoint.auto":
        autos.append((seq, pl.get("checkpoint_id"), pl.get("phase_id"),
                      pl.get("outcome"), ev.get("tool_use_id")))
    elif typ == "phase.started":
        phase_started_seqs.append(seq)
    elif typ == "tool.started" and pl.get("tool_name") == "AskUserQuestion":
        tid = ev.get("tool_use_id")
        if tid:
            ask_calls[tid] = seq
    elif typ == "change.delta_applied":
        delta_seqs.append(seq)
        change_seqs.append(seq)
    elif typ == "change.opened":
        change_seqs.append(seq)
    elif typ == "change.resumed":
        cid = pl.get("checkpoint_id")
        if cid:
            resumed_cps.add(cid)
    elif typ == "criterion.dropped":
        drops.append((seq, pl.get("checkpoint_id"),
                      pl.get("criterion_id"), ev.get("tool_use_id")))
    elif typ == "budget.confirmed":
        budget_confirmed.append((seq, pl.get("checkpoint_id"),
                                 ev.get("tool_use_id")))
    elif typ == "budget.extended":
        budget_extended.append((seq, pl.get("checkpoint_id"),
                                ev.get("tool_use_id")))

if not decided and not opened and not displayed and not autos:
    print("N/A — no checkpoint decisions, openings or displays recorded: "
          "no user gate was crossed in this trace, so there is no gate "
          "evidence to audit")
    sys.exit(2)

# --- auto-passed checkpoints -------------------------------------------------------
# The human gate is skipped ONLY where the policy says no judgment is needed
# and the submission was CONFIRMED: an unclear or failed self-report owes the
# gate. An auto-pass is terminal for its checkpoint — never together with a
# display or a decision of the same id, never twice.
auto_passed = {}   # checkpoint_id -> seq
prev_auto_seq = None
for seq, cid, phase, outcome, tool_id in autos:
    if not cid or not phase:
        fail(f"checkpoint.auto at event #{seq} is malformed: needs "
             "payload.checkpoint_id and payload.phase_id")
    if phase not in AUTO_PASS_PHASES:
        fail(f"checkpoint.auto at event #{seq} names phase {phase!r}, which "
             f"never auto-passes (only {sorted(AUTO_PASS_PHASES)} may — "
             "diagnosis, fix and every other phase owe the human gate)")
    if outcome != "confirmed":
        fail(f"checkpoint.auto at event #{seq} carries outcome "
             f"{outcome!r} — only a confirmed submission may auto-pass (an "
             "unclear or failed report owes the human gate)")
    if cid in auto_passed:
        fail(f"idempotency: checkpoint {cid} auto-passed twice (events #"
             f"{auto_passed[cid]} and #{seq})")
    if cid in displayed:
        fail(f"checkpoint {cid} is both auto-passed (event #{seq}) and "
             f"displayed (event #{displayed[cid]}) — an auto-passed "
             "checkpoint is never shown for decision, and an already "
             "displayed checkpoint is decided by a human answer")
    if cid not in opened:
        fail(f"checkpoint.auto at event #{seq} names checkpoint {cid} with no "
             "checkpoint.opened before it — opened → auto is an auto-pass's "
             "whole shape")
    oseq, _, ophase = opened[cid]
    if oseq >= seq:
        fail(f"ordering: checkpoint {cid} auto-passed at event #{seq} before "
             f"it opened at event #{oseq}")
    if ophase != phase:
        fail(f"checkpoint.auto at event #{seq} names phase {phase!r} but "
             f"checkpoint {cid} was opened for phase {ophase!r} — an auto-pass "
             "settles the checkpoint of its own phase")
    if tool_id in ask_calls:
        fail(f"checkpoint.auto at event #{seq} rides tool_use_id {tool_id!r}, "
             "an AskUserQuestion call — that id belongs to a human answer; "
             "an auto-pass is the framework's own act")
    if prev_auto_seq is not None and not any(
            prev_auto_seq < ps < seq for ps in phase_started_seqs):
        fail(f"checkpoint.auto at event #{seq} follows another auto-pass "
             f"(event #{prev_auto_seq}) with no phase.started between — every "
             "auto-pass closes a phase that was opened first")
    prev_auto_seq = seq
    auto_passed[cid] = seq

# --- decisions --------------------------------------------------------------------
seen_decided = {}
seen_tool_decided = {}
for seq, cid, tid in decided:
    if not cid or not tid:
        fail(f"checkpoint.decided at event #{seq} is malformed: needs both "
             "payload.checkpoint_id and the platform tool_use_id")
    if cid in seen_decided:
        fail(f"idempotency: checkpoint {cid} decided twice (events #"
             f"{seen_decided[cid]} and #{seq}) — a replayed decision "
             "callback must be a silent no-op, never a second recorded "
             "decision")
    if tid in seen_tool_decided:
        fail(f"one platform call, one decision: tool_use_id {tid} decided "
             f"both {seen_tool_decided[tid]} and {cid} — a single "
             "AskUserQuestion answer carries one decision")
    if cid in auto_passed:
        fail(f"checkpoint {cid} was auto-passed at event #{auto_passed[cid]} "
             f"and is decided again at event #{seq} — an auto-pass is "
             "terminal, it is never followed by a human decision")
    if cid not in displayed or displayed[cid] >= seq:
        fail(f"display binding: checkpoint {cid} decided at event #{seq} "
             "was never displayed before the decision — 生成文本 ≠ 已展示; "
             "a decision without a prior render is invalid")
    if cid in opened:
        oseq, ononce, _ = opened[cid]
        dnonce = None
        for s, ev in events:
            if s == displayed[cid]:
                dnonce = payload(ev).get("nonce")
        if not isinstance(ononce, str) or not ononce:
            fail(f"nonce: checkpoint {cid} opened without a nonce — the "
                 "decision question must carry the checkpoint nonce")
        if ononce != dnonce:
            fail(f"nonce: checkpoint {cid} opened with nonce {ononce!r} but "
                 f"displayed with {dnonce!r} — the render and the question "
                 "must bind the same nonce")
        if oseq >= displayed[cid]:
            fail(f"ordering: checkpoint {cid} opened at event #{oseq} but "
                 f"displayed at #{displayed[cid]} — display follows the open")
    # elif: change-confirm / review-result / recovery checkpoints emit NO
    # checkpoint.opened (positional contract, see header) — displayed-before-
    # decided above is the binding that applies to them.
    if tid not in ask_calls or ask_calls[tid] >= seq:
        fail(f"decision binding: checkpoint {cid} decided at event #{seq} by "
             f"tool_use_id {tid!r}, which is not a recorded AskUserQuestion "
             "call before the decision — decisions come only from platform "
             "AskUserQuestion responses (注入 answers)")
    for dseq in delta_seqs:
        if displayed[cid] < dseq < seq:
            fail(f"stale render: change.delta_applied (event #{dseq}) landed "
                 f"between the display (#{displayed[cid]}) and the decision "
                 f"(#{seq}) of checkpoint {cid} — the decision bound "
                 "pre-change content (stale 渲染)")
    seen_decided[cid] = seq
    seen_tool_decided[tid] = cid

# --- ride-along answers (same-callback binding) ------------------------------------
# I6 (recovery round): drop replay identity is per (criterion_id, tool_use_id)
# — the same answering call replayed, or the same criterion dropped twice at
# ONE checkpoint, is a true duplicate; the same criterion dropped again at a
# LATER checkpoint with a FRESH answering call is the LEGAL re-drop shape —
# and only legal with the re-acceptance AUDITED (F7, re-review): a
# phase.started strictly between the two drops is the observable evidence
# that the criterion re-entered an acceptance view (the phase re-opened and
# re-accepted it); without it a second drop strips an already-dropped
# criterion — laundering, not a re-drop.
phase_started_seqs = [s for s, ev in events if ev.get("type") == "phase.started"]
seen_drop_pairs = set()   # (criterion_id, tool_use_id)
drop_checkpoint = {}      # criterion_id -> checkpoint_id of its latest drop
drop_seq = {}             # criterion_id -> seq of its latest drop
for seq, cid, crit, tid in drops:
    if not cid or not crit or not tid:
        fail(f"criterion.dropped at event #{seq} is malformed: needs "
             "checkpoint_id, criterion_id and the answering tool_use_id")
    if tid not in ask_calls or ask_calls[tid] >= seq:
        fail(f"decision binding: criterion.dropped ({crit}, event #{seq}) "
             f"binds tool_use_id {tid!r}, which is not a recorded "
             "AskUserQuestion call — drops ride a human answer, they are "
             "not self-reported")
    if (crit, tid) in seen_drop_pairs:
        fail(f"criterion.dropped for {crit} recorded twice on the same "
             f"answering call {tid!r} — per-(criterion, call) drops are "
             "idempotent at the reducer (replays are silent)")
    if crit in drop_checkpoint and drop_checkpoint[crit] == cid:
        fail(f"criterion.dropped for {crit} recorded twice at checkpoint "
             f"{cid} — one checkpoint settles each criterion once")
    if crit in drop_seq and not any(
            drop_seq[crit] < ps < seq for ps in phase_started_seqs):
        fail(f"criterion.dropped for {crit} re-dropped at event #{seq} with "
             f"no phase re-acceptance in between (no phase.started between "
             f"#{drop_seq[crit]} and #{seq}) — a dropped criterion re-enters "
             "the acceptance view only through a fresh phase acceptance; a "
             "second drop without one is laundering, not a re-drop")
    seen_drop_pairs.add((crit, tid))
    drop_checkpoint[crit] = cid
    drop_seq[crit] = seq
    if cid in auto_passed:
        fail(f"criterion.dropped for {crit} (event #{seq}) rides checkpoint "
             f"{cid}, which was auto-passed (event #{auto_passed[cid]}) — an "
             "auto-pass takes no ride-along answers")
    if cid in seen_decided and seq > seen_decided[cid]:
        fail(f"criterion.dropped for {crit} (event #{seq}) landed after "
             f"checkpoint {cid} was decided (#{seen_decided[cid]}) — drops "
             "are recorded before the decision they ride")
for seq, cid, tid in budget_confirmed:
    if tid not in ask_calls or ask_calls[tid] >= seq:
        fail(f"decision binding: budget.confirmed at event #{seq} binds "
             f"tool_use_id {tid!r}, which is not a recorded AskUserQuestion "
             "call — the run-start budget settles with a human answer")
    if cid in seen_decided and seq > seen_decided[cid]:
        fail(f"budget.confirmed (event #{seq}) landed after checkpoint {cid} "
             f"was decided (#{seen_decided[cid]}) — the settlement is "
             "recorded before the decision it shares the callback with")
if len(budget_confirmed) > 1:
    fail(f"budget.confirmed recorded {len(budget_confirmed)} times — the "
         "run-start settlement applies once (already-confirmed callbacks "
         "are silent no-ops)")
for seq, cid, tid in budget_extended:
    if tid not in ask_calls or ask_calls[tid] >= seq:
        fail(f"decision binding: budget.extended at event #{seq} binds "
             f"tool_use_id {tid!r}, which is not a recorded AskUserQuestion "
             "call — extensions consume a fresh human answer")
    if cid in auto_passed:
        fail(f"budget.extended at event #{seq} rides checkpoint {cid}, which "
             f"was auto-passed (event #{auto_passed[cid]}) — an auto-pass is "
             "refused while an extension question is owed")
    if cid in seen_decided and seq > seen_decided[cid]:
        fail(f"budget.extended (event #{seq}) landed after checkpoint {cid} "
             f"was decided (#{seen_decided[cid]}) — the extension is "
             "recorded before the decision it shares the callback with")

# --- opened-but-never-displayed (superseded checkpoints are legitimate) ------------
last_change = max(change_seqs) if change_seqs else 0
for cid, (oseq, _, _) in opened.items():
    if cid in displayed:
        continue
    if cid in auto_passed:
        continue  # auto-passed: no render is owed, no decision can land
    if resumed_cps and cid in resumed_cps:
        continue  # defensive: resumed confirm checkpoints display explicitly
    if oseq < last_change:
        continue  # superseded by a later requirement change — re-rendered
    print(f"MISSING — checkpoint {cid} opened at event #{oseq} but never "
          "displayed by end of trace: the render evidence is absent (the "
          "stop hook binds the display before any decision can land)")
    sys.exit(3)

if not decided:
    if not auto_passed:
        print("N/A — checkpoints were opened/displayed but no decision was "
              "recorded (the run is waiting on the user): no decision evidence "
              "to audit")
        sys.exit(2)
    print(f"PASS — {len(auto_passed)} checkpoint(s) auto-passed within "
          "policy (confirmed submissions on auto-pass phases, display-free, "
          "idempotent); no human decision recorded")
    sys.exit(0)

extra = ""
if drops or budget_confirmed or budget_extended or auto_passed:
    extra = (f", {len(drops)} criterion drop(s), {len(budget_confirmed)} "
             f"budget confirmation(s), {len(budget_extended)} "
             f"extension(s), {len(auto_passed)} auto-pass(es)")
print(f"PASS — {len(decided)} checkpoint decision(s) display-bound, "
      f"decision-bound to AskUserQuestion calls, nonce-consistent and "
      f"idempotent{extra}")
sys.exit(0)
PYEOF
