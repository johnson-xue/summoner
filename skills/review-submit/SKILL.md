---
name: review-submit
description: ONLY to register one review (identity-only) in a live Summoner run, or to recover a wedged RUNNING review attempt — invoke when the review must start or the reviewer's child died mid-review; never for anything else
user-invocable: false
when_to_use:
  - The run sits in REVIEW_PENDING (a [v] obligation or an in-flight review) or at the terminal default position (ACTIVE right after the review phase's acceptance), and the review must be registered
  - A RUNNING review's child delegation died (no SubagentStop result, no servable capsule) and the run is wedged — the recovery form opens the retry checkpoint
  - Never to submit a phase, render a checkpoint, dispatch the reviewer (that is Agent), or bypass a decision
---

# review-submit — the ONLY review-registration (and wedged-attempt recovery) channel

Zero logic lives here. This skill exists so the PreToolUse gate can observe
the registration (spec §3 ⑤ 审查信封): the main session MUST register every
review through `Skill(summoner:review-submit, args=<JSON>)`. The gate decodes
the args STRICTLY (unknown fields rejected) into one of two forms.

## form 1 — register a review (IDENTITY-ONLY)

```json
{"schema_version": "2", "run_id": "run-0192…", "review_id": "rev-0192…"}
```

- IDENTITY-ONLY (C1): the reducer MINTS the complete envelope from the pinned
  acceptance snapshot. `artifact_set` / `exit_criteria` /
  `verification_scope` / any producer-authored content on the wire is
  REJECTED as an unknown field — the producer can neither trim nor extend
  the frozen view; the user's recorded per-id `drop:` answers are the only
  subtractions the mint applies.
- Legal entries: REVIEW_PENDING — a `[v]` decision's obligation (consumed by
  the registration), an idempotent same-id re-send, or an in-flight review —
  and the TERMINAL DEFAULT position (ACTIVE immediately after the review
  phase took a plain `continue:`; an explicit `skip:` closes it UNREVIEWED).
- One review at a time: while a review is PREPARED/RUNNING, only a re-send
  of ITS id is legal; a new review id is refused.
- An empty frozen view with nothing dropped is rejected — nothing accepted,
  no review due.

## dispatch — the launch capsule, VERBATIM

After PREPARED, run `/summoner:status` (view:review) and copy the EXACT
launch capsule it serves for the active attempt. The reviewer dispatch is
`Agent(subagent_type=summoner:reviewer)` whose prompt EQUALS the capsule
bytes — whole-message equality. Prose prompts, prefixes, suffixes or partial
payloads never bind (denied fail-closed). `summoner:reviewer` or bare
`reviewer` only; forks and general-purpose agents are refused.

## form 2 — recover a wedged RUNNING attempt (C3)

A RUNNING review whose child died is unrecoverable by re-send (no-ops), by
re-dispatch (not PREPARED) or by a new review id (one at a time). The
recovery form is the identity triple plus the WEDGED attempt:

```json
{"schema_version": "2", "run_id": "run-0192…", "review_id": "rev-0192…", "expected_attempt_id": "att-0192…"}
```

- `expected_attempt_id` must equal the RUNNING attempt (the same
  `attempt_id` the delegation.started carried — review.verdict joins on it).
- Budget-exempt: legal with EVERY budget exhausted.
- Opens a REVIEW_RESULT checkpoint offering `ReviewRetry(A)`; the status
  render shows the offer. `continue:` there atomically retires A and appends
  PREPARED B (fresh token) → REVIEW_PENDING; view:review then serves B's
  capsule and one Agent dispatch with it binds and charges EXACTLY once. If
  the delegation limit is exhausted at that checkpoint, the answer must ride
  an extension — a bare `continue:` is rejected and no budget number is
  invented.
- A recovery never registers or mints anything: it only opens the checkpoint.

Every rejection (exit 2) names the reason and is never traced. The reviewer
returns a structured ReviewResult; SubagentStop verifies child ownership
(the transcript's initial record must be the exact admitted capsule),
reconciles the claimed evidence ids against the reviewer's own transcript,
and stamps the concluding `attempt_id` on review.verdict.
