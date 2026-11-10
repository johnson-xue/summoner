---
name: review-submit
description: ONLY to register one review envelope in a live Summoner run (REVIEW_PENDING or the terminal default position) — invoke when the review's scope is frozen and the reviewer must be dispatched; never for anything else
user-invocable: false
when_to_use:
  - The run sits in REVIEW_PENDING (a [v] obligation or an in-flight review) or at the terminal default position (ACTIVE right after the review phase's acceptance), and the review envelope must be registered
  - Never to submit a phase, render a checkpoint, dispatch the reviewer (that is Agent), or bypass a decision
---

# review-submit — the ONLY envelope-registration channel

Zero logic lives here. This skill exists so the PreToolUse gate can observe and
validate the review envelope (spec §3 ⑤ 审查信封): the main session MUST
register every review through `Skill(summoner:review-submit, args=<envelope
JSON>)`. The channel's two legal entries: REVIEW_PENDING — a `[v]` decision's
obligation (consumed by the registration), a same-envelope re-send, or an
in-flight review — and the TERMINAL DEFAULT position (spec §1 终局⑤默认强制):
ACTIVE immediately after the run's review phase took a plain `continue:` at
its checkpoint, which is the default way a terminal review starts with NO
[v] opt-in. An explicit `skip:` of the review phase (UNREVIEWED) closes the
default. The gate decodes the args strictly (unknown fields are rejected),
validates the envelope against the run's frozen acceptance view minus
recorded user drops and the project's check registry (`summoner.yaml`), then
registers the review PREPARED and traces `review.prepared`. A rejection
(exit 2) names the reason and is never traced.

## args = one review-envelope JSON object

```json
{
  "schema_version": "2",
  "run_id": "run-0192…",
  "review_id": "rev-0192…",
  "artifact_set": [
    {"path": "docs/reviews/2026-11-10-npe.md", "digest": "<sha256 of the file bytes>", "kind": "report"}
  ],
  "requirements": {"version": 1, "requirements": []},
  "exit_criteria": [
    {"id": "crit-root-cause-identified", "method": "judgment"},
    {"id": "crit-go-test", "method": "mechanical", "check_id": "go-test"}
  ],
  "verification_scope": {
    "readable": ["docs/reviews/2026-11-10-npe.md"],
    "check_ids": ["go-test@<definition digest>"]
  }
}
```

Field rules:

- `exit_criteria` — MUST equal the frozen acceptance view minus recorded user
  drops, BOTH directions: a missing id is a producer trim (the producer cannot
  trim the frozen view), an invented id is rejected. The user's per-id `drop:`
  answers at decision checkpoints are the only legal way a criterion leaves
  the set.
- `artifact_set` — the artifact versions the review re-derives; non-empty.
- mechanical criteria must reference a registered `check_id` and — with a
  registry present — freeze its definition digest in
  `verification_scope.check_ids` as `check_id@digest`.
- An empty frozen view with nothing dropped is the vacuous case: rejected —
  nothing has been accepted yet, no review is due.
- One review at a time: while a review is PREPARED/RUNNING, only a re-send of
  ITS envelope is legal (idempotent); a new review id is refused.

On success the gate consumes any outstanding [v] obligation into the review
record (PREPARED; the terminal default path records none) and the reviewer
delegation becomes admissible: dispatch with `Agent(subagent_type=
summoner:reviewer)` carrying the PREPARED `review_id` in the prompt — the
gate admits the plugin spelling and the bare `reviewer`, nothing else. The
reviewer returns a structured ReviewResult; SubagentStop reconciles its
claimed evidence ids against the reviewer's own transcript before accepting
the verdict (a forged id fails the review, never the run — the user decides
at the routed checkpoint).
