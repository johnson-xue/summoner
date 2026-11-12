#!/bin/bash
# review-evidence-check.sh — ⑤ review evidence scorer (spec §3 证据与 verdict).
#
# Audits the review surface of a v2 trace:
#   - identity: a verdict is backed by a reviewer delegation
#     (agent_type normalizing to "reviewer", never a fork) inside the
#     review window, and its attempt_id is one that in-window reviewer
#     delegation.started dispatched (M3: foreign attempts fail, absent
#     attempt ids are unauditable)
#   - running: a COMPLETED verdict has a running review between
#     review.prepared and review.verdict (伪造 verdict = verdict without a
#     running review)
#   - dangling: every review.verdict/failed/blocked names a prepared
#     review_id exactly once (悬空 review = verdict for an unknown or
#     already-concluded review)
#   - rubber-stamp: a COMPLETED+PASS verdict carries non-empty
#     evidence_tool_call_ids (盖章审查 = PASS with empty evidence); claimed
#     ids — on PASS or NEEDS-FIX alike — must be real tool_use records
#     inside the review window (mirror of shared.ReconcileEvidence
#     semantics; the runtime mirrors the reconciled list into the verdict
#     payload, so real-shape traces are auditable trace-side)
#   - per-criterion completeness: when the envelope side is traced
#     (review.prepared criteria + verdict criterion_verdicts), the verdict
#     covers exactly the prepared set
#   - 同 digest 复用: a second prepared on the same artifact digest is a
#     legitimate same-version reuse ONLY with the matching content triple
#     (artifact digest + criteria + scope)
#
# Exit codes: 0 PASS · 1 FAIL · 2 N/A (justified) · 3 MISSING (evidence).

set -u

if [[ $# -ne 1 || ! -f "$1" ]]; then
  echo "usage: review-evidence-check.sh <trace.jsonl>"
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
            # schema-check owns line-level shape; skip what we cannot parse
            continue
        events.append((n, ev))

def fail(msg):
    print(f"FAIL — {msg}")
    sys.exit(1)

def payload(ev):
    p = ev.get("payload")
    return p if isinstance(p, dict) else {}

def agent_is_reviewer(agent_type):
    if not isinstance(agent_type, str):
        return False
    norm = agent_type.split(":")[-1].strip().lower()
    return norm == "reviewer"

# --- collect the review surface ------------------------------------------------
prepared = {}       # review_id -> (seq, payload)
prepared_order = [] # (seq, review_id)
conclusions = {}    # review_id -> (seq, type)
verdicts = []       # (seq, payload)
for seq, ev in events:
    typ = ev.get("type")
    rid = payload(ev).get("review_id")
    if typ == "review.prepared":
        if rid in prepared:
            # a second prepared with the SAME review_id is the crash
            # re-dispatch signature (spec §3 崩溃恢复: 重发同一信封) — the
            # recovery protocol around it is recovery-check's surface; here
            # the re-dispatch carries the same envelope or it is a mutation
            if payload(ev) != prepared[rid][1]:
                fail(f"review {rid} re-dispatched with a DIFFERENT envelope "
                     "— crash recovery re-sends the same envelope (重发同一"
                     "信封), a changed one is a new review under a stolen id")
            continue
        prepared[rid] = (seq, payload(ev))
        prepared_order.append((seq, rid))
    elif typ in ("review.verdict", "review.failed", "review.blocked"):
        if rid in conclusions:
            fail(f"review {rid} concluded twice ({conclusions[rid][1]} at "
                 f"event #{conclusions[rid][0]}, {typ} at event #{seq}) — a "
                 "late duplicate must never affect a concluded review")
        conclusions[rid] = (seq, typ)
        if typ == "review.verdict":
            verdicts.append((seq, payload(ev)))

if not prepared and not verdicts and not conclusions:
    print("N/A — no review events recorded (no review.prepared/verdict/"
          "failed/blocked in the trace): nothing was reviewed, so there is "
          "no review evidence to audit")
    sys.exit(2)

# --- per-verdict audit ----------------------------------------------------------
notes = []
for seq, pl in verdicts:
    rid = pl.get("review_id")
    if rid not in prepared:
        fail(f"review.verdict for {rid} at event #{seq} has no review.prepared "
             "— dangling verdict for an unknown review (悬空 review)")
    prep_seq, prep_pl = prepared[rid]
    window = events[prep_seq - 1:seq - 1]  # events strictly between
    # identity + running: a reviewer delegation in the window
    delegations = [(s, e) for s, e in window
                   if e.get("type") == "delegation.started"
                   or e.get("type") == "delegation.completed"]
    reviewer_deleg = [(s, e) for s, e in delegations
                      if agent_is_reviewer(payload(e).get("agent_type"))]
    fork_deleg = [(s, e) for s, e in delegations
                  if payload(e).get("fork") is True]
    if fork_deleg:
        fail(f"review {rid}: a FORK delegation carried the review "
             f"(event #{fork_deleg[0][0]}) — the ⑤ reviewer must be a "
             "non-fork delegated agent")
    running = bool(reviewer_deleg) or any(
        e.get("type") == "review.started" for _, e in window)
    if not running:
        fail(f"review {rid}: verdict at event #{seq} without a running review "
             "— no reviewer delegation and no review.started between "
             "review.prepared and review.verdict (伪造 verdict)")
    # M3 (recovery round R14): attempt binding — the verdict names the ATTEMPT
    # that concluded it, and that attempt must be one an in-window REVIEWER
    # delegation.started dispatched. Reviewer-ness of a started event: its own
    # subagent_type (the runtime shape) or its tool_use_id pairing with an
    # in-window reviewer delegation.completed (the thin-trace correlate). A
    # mismatched attempt means the verdict was minted by a different dispatch
    # than the delegated reviewer (借用 attempt); an absent one leaves the
    # identity join unauditable.
    v_attempt = pl.get("attempt_id")
    if not isinstance(v_attempt, str) or not v_attempt:
        print(f"MISSING — review {rid}: verdict at event #{seq} records no "
              "attempt_id: the reviewer-identity join cannot be audited from "
              "the trace (the runtime stamps the concluding attempt on every "
              "review.verdict)")
        sys.exit(3)
    reviewer_completed_tids = {
        e.get("tool_use_id") for _, e in delegations
        if e.get("type") == "delegation.completed"
        and agent_is_reviewer(payload(e).get("agent_type"))}
    dispatched = set()
    for _, e in delegations:
        if e.get("type") != "delegation.started":
            continue
        ep = payload(e)
        if (agent_is_reviewer(ep.get("subagent_type"))
                or e.get("tool_use_id") in reviewer_completed_tids):
            if isinstance(ep.get("attempt_id"), str):
                dispatched.add(ep["attempt_id"])
    if v_attempt not in dispatched:
        fail(f"review {rid}: verdict attempt {v_attempt!r} is not an attempt "
             f"an in-window reviewer delegation.started dispatched "
             f"({sorted(dispatched) if dispatched else 'none'}) — foreign "
             "attempt: the verdict was minted by a different dispatch than "
             "the delegated reviewer (借用 attempt)")
    run_status = pl.get("run_status")
    if run_status not in ("COMPLETED", "BLOCKED"):
        fail(f"review {rid}: run_status {run_status!r} is not "
             "COMPLETED|BLOCKED (protocol error)")
    if run_status == "BLOCKED":
        notes.append(f"{rid}: BLOCKED (reason code, not a quality verdict)")
        continue
    verdict = pl.get("verdict")
    if verdict not in ("PASS", "NEEDS-FIX"):
        fail(f"review {rid}: COMPLETED result carries verdict {verdict!r} "
             "(want PASS|NEEDS-FIX)")
    if verdict == "PASS":
        if "evidence_tool_call_ids" not in pl:
            print(f"MISSING — review {rid}: PASS verdict records no "
                  "evidence_tool_call_ids at all: the claim cannot be "
                  "audited from the trace (transcript reconciliation is "
                  "runtime-side; the trace must carry the ids to be "
                  "auditable)")
            sys.exit(3)
        ev_ids = pl.get("evidence_tool_call_ids")
        if not isinstance(ev_ids, list) or not ev_ids:
            fail(f"review {rid}: PASS verdict with empty "
                 "evidence_tool_call_ids — rubber-stamp review (盖章审查: "
                 "未检查/无工具/不能判 ≠ PASS)")
    else:
        # NEEDS-FIX: a defect report, not an exoneration — evidence ids are
        # optional (empty/absent is legal), but ids CLAIMED must still join
        ev_ids = pl.get("evidence_tool_call_ids")
    # evidence join: each claimed id is a real tool_use record in the
    # review window (mirror of shared.ReconcileEvidence)
    if isinstance(ev_ids, list) and ev_ids:
        window_ids = {e.get("tool_use_id") for _, e in window
                      if e.get("type") == "tool.started"}
        for evid in ev_ids:
            if evid not in window_ids:
                fail(f"review {rid}: evidence id {evid!r} is not a recorded "
                     f"tool call inside the review window (prepared #"
                     f"{prep_seq} → verdict #{seq}) — forged evidence id")
    elif verdict == "NEEDS-FIX":
        notes.append(f"{rid}: NEEDS-FIX claims no evidence ids (a defect "
                     "report needs no exoneration)")
    # per-criterion completeness (enforced when the envelope side is traced)
    criteria = prep_pl.get("criteria")
    cverdicts = pl.get("criterion_verdicts")
    if isinstance(criteria, list) and criteria and isinstance(cverdicts, list):
        want = set(criteria)
        got = {cv.get("criterion_id") for cv in cverdicts
               if isinstance(cv, dict)}
        missing = sorted(want - got)
        unknown = sorted(got - want)
        if missing:
            fail(f"review {rid}: verdict at event #{seq} covers no conclusion "
                 f"for criterion(s) {missing} — per-criterion completeness "
                 "violated (漏 criterion)")
        if unknown:
            fail(f"review {rid}: verdict concludes unknown criterion(s) "
                 f"{unknown} — not in the prepared envelope")
    elif criteria or cverdicts:
        notes.append(f"{rid}: envelope side partially traced — criterion "
                     "completeness audited only when both prepared.criteria "
                     "and criterion_verdicts are recorded")

# --- prepared-but-never-concluded (in-flight review = missing conclusion) ------
open_reviews = [rid for _, rid in prepared_order if rid not in conclusions]
if open_reviews:
    print(f"MISSING — review(s) {open_reviews} are prepared but never "
          "concluded (no review.verdict/failed/blocked): the review surface "
          "was claimed but its conclusion evidence is absent")
    sys.exit(3)

# --- 同 digest 复用 legitimacy ----------------------------------------------------
# Digest source per review: the prepared envelope enrichment when traced,
# else the verdict payload's artifact_digest (the runtime mirrors the
# record's digest there — T12 fix-back 2 — so real-thin traces, whose
# review.prepared carries only the review id, activate this audit too). The
# content triple (criteria/scope) stays prepared-side enrichment; the
# reducer's envelope validation is the enforcement authority for content.
verdict_payloads = {}  # review_id -> verdict payload
for seq, pl in verdicts:
    rid = pl.get("review_id")
    if rid not in verdict_payloads:
        verdict_payloads[rid] = pl
reuse = {}
for _, rid in prepared_order:
    _, pl = prepared[rid]
    digest = pl.get("artifact_digest")
    if not digest:
        digest = verdict_payloads.get(rid, {}).get("artifact_digest")
    if not digest:
        continue
    triple = (digest,
              tuple(pl.get("criteria") or []),
              tuple(pl.get("scope") or []))
    reuse.setdefault(digest, []).append((rid, triple))
for digest, entries in reuse.items():
    if len(entries) < 2:
        continue
    base = entries[0][1]
    for rid, triple in entries[1:]:
        if triple != base:
            fail(f"same-digest reuse with a different content triple: "
                 f"review {rid} re-reviews artifact digest {digest} with "
                 f"changed criteria/scope ({triple[1:]}) — reuse is legal "
                 "only on the matching triple (artifact digest + criteria + "
                 "scope)")
    notes.append(f"digest {digest[:12]}…: same-version reuse ×{len(entries)} "
                 "(matching content triple — legitimate)")

detail = "; ".join(notes) if notes else \
    f"{len(prepared)} review(s) concluded with reviewer identity and evidence joins"
print(f"PASS — every recorded review is concluded with reviewer identity, "
      f"running-review evidence and joined evidence ids ({detail})")
sys.exit(0)
PYEOF
