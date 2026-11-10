# Summoner Trace Protocol (v2)

The durable, append-only evidence record of a Summoner run. One
self-contained JSONL file per run; every workflow (fix/new/debug/review/ship/
ops/release) writes the same schema through the same writer. There is no mode
field, no graph event set, and no fallback vocabulary — spec §7 (单一 trace
schema) is the authority: `docs/superpowers/specs/2026-11-09-single-loop-checkpoint-design.md`.

## Where traces live

`$SUMMONER_TRACE_DIR` (or `~/.summoner/traces-v2`) / `{project_key}` /
`{run_id}.jsonl` — resolved and written by the Go hooks
(`hooks/shared/trace.go`), never by model-authored shell echoes. The model is
not a trace writer: control events come only from platform observations
(hook inputs carrying `tool_use_id`), and everything a model produces is a
proposal the hooks validate before recording.

## Line shape

Every line is one JSON object in the canonical field order of the Go
`TraceEvent` struct (write and verify share this one form):

```jsonl
{"schema_version":"2","run_id":"run-x01","session_id":"sess-x01","project_root_hash":"1a2b3c4d5e6f0770","type":"checkpoint.decided","seq":10,"timestamp":"2026-11-11T00:00:00Z","source":"posttooluse","tool_use_id":"toolu_x01","payload":{"checkpoint_id":"cp-x01","tool_use_id":"toolu_x01"},"prev_hash":"…","hash":"…"}
```

| Field | Meaning |
|---|---|
| `schema_version` | always `"2"` |
| `run_id` / `session_id` | run and session identity (one file = one run) |
| `project_root_hash` | normalized project-root hash, so a line names its project even when copied out of the tree |
| `type` | one of the allow-list below — unknown types are rejected at the door |
| `seq` | 1..N, strictly increasing (line number) |
| `timestamp` | RFC3339 UTC |
| `source` | writing hook (`userpromptsubmit`, `pretooluse-gate`, `posttooluse`, `stop`, `session-start`, `subagentstop`) |
| `prompt_id` / `tool_use_id` | platform correlation keys; `tool_use_id` is what binds decisions, evidence and delegations to real platform calls |
| `payload` | per-type fields (table below) |
| `prev_hash` / `hash` | the chain: `prev_hash` = sha256 of the previous line's canonical bytes (first line: sha256 of the empty string); `hash` = sha256 of this line's canonical bytes including `prev_hash` |

**Who verifies what:** the Go layer (`VerifyChain`, hardened in T7) verifies
the hash chain — strict decoding, seq equality, byte-identical canonical
form — and rejects tampering. The shell scorers (`scorers/deterministic/`)
do NOT recompute hashes: they audit the event-set, ordering, binding and
completeness **semantics**. Both layers are required; neither substitutes
for the other.

## Event vocabulary (the 38-type allow-list)

The single sanctioned set, mirrored 1:1 from `hooks/shared/trace.go`
(`allEventTypes`). A new event type is a spec §7 change, not a scorer edit.

| Type | Payload contract (audited fields in **bold**) |
|---|---|
| `workflow.started` | `{"workflow": "fix|new|debug|…"}` — first event of every run |
| `workflow.ended` | `{"status":"closed"}` — emitted exactly once, by the CLOSED writer only, after `post_game.completed` |
| `workflow.aborted` | abort terminal |
| `phase.started` / `phase.closed` | `{"phase_id": …}` |
| `phase_submitted.rejected` | `{"reason": …, "phase_id"?}` — a rejected submit records the rejection, never a phase result |
| `checkpoint.opened` | `{"checkpoint_id", "phase_id", "nonce"}` — **phase-submit checkpoints only** (see positional contract) |
| `checkpoint.displayed` | `{"checkpoint_id", "nonce"}` — the Stop hook binds the render; **nonce must equal the opened nonce** |
| `checkpoint.decided` | `{"checkpoint_id", "tool_use_id"}` — **tool_use_id must be a recorded AskUserQuestion call** (decision binding) |
| `checkpoint.decision_rejected` | `{"checkpoint_id", "reason"}` — a failed decision attempt; the answer is never recorded |
| `criterion.dropped` | `{"checkpoint_id", "criterion_id"}` — per-id user drop; rides the same AskUserQuestion call as the decision |
| `budget.confirmed` / `budget.extended` | `{"checkpoint_id", "limits"|"delta"}` — bind the answering `tool_use_id`; confirmed applies once per run |
| `recall.applied` | `{"from_phase_id", "target_phase_id", "newly_stale"?}` — target must be real history; a new visit follows |
| `change.opened` / `change.delta_applied` / `change.resumed` | change loop (§1): frozen position / `{"requirements_version", "newly_stale"?, "preserved"?}` / `{"checkpoint_id"}` |
| `tool.started` | `{"tool_name", "read_class"?, "sandbox_probe"?, "l2_check"?}` — the pre-execution intent record |
| `tool.result` / `tool.failed` | observed outcome (`{"tool_name","response_sha256"}` / failure fields) |
| `tool.outcome_unknown` | `{"tool_use_id", "detected_by": "session-start|stop"}` — side effect happened, result never landed |
| `tool.resolved` | `{"tool_use_id", "outcome": "accepted|failed|indeterminate", "probe_tool_use_id"}` — one-shot; the probe must be a recorded read-class call |
| `check.result` | `{"check_id", "trust": "framework|project", "status": "pass|fail|blocked", "digest", "tier"?, "reason"?, "exit"?}` — BLOCKED is a refusal to claim, never a quality verdict |
| `review.prepared` | `{"review_id"}` — the runtime emission is thin (the envelope itself lives in run state); `artifact_digest`/`criteria`/`scope` are scorer-recognized optional enrichments. Re-dispatch after a crash re-sends the SAME envelope |
| `review.started` | `{"review_id"}` — reviewer dispatched |
| `review.verdict` | `{"review_id", "run_status": "COMPLETED\|BLOCKED", "verdict"?, "evidence_tool_call_ids"?, "envelope_digest"?, "artifact_digest"?}` — the runtime emission; the reconciled evidence list is MIRRORED into the payload (T12 fix-back), so COMPLETED+PASS always carries a non-empty list (auditable non-rubber-stamp), while NEEDS-FIX/BLOCKED carry it only when the reviewer claimed ids. The record's digests mirror too (T12 fix-back 2): `envelope_digest` (registration identity, always present on a concluded record) and `artifact_digest` (the claimed artifact set, empty for a reason-code BLOCKED) — digests-in-trace, never content, same §7 precedent as `tool.result`'s `response_sha256`. `criterion_verdicts` is enrichment-only BY DESIGN, not a gap: criterion CONTENT lives in run state / the envelope, the reducer's envelope validation (both-directions rejection) is the enforcement authority, and the trace-side per-criterion audit fires only on traces that carry the enrichment — do not "fix" it into the payload |
| `review.failed` / `review.blocked` / `review.superseded` | `{"review_id", "reason"?}` — protocol-error / honest-block / superseded conclusions |
| `delegation.started` / `delegation.completed` / `delegation.failed` | `{"agent_type", "fork"?, "review_id"?, "transcript_path"?}` — reviewer delegations are non-fork `agent_type` `summoner:reviewer` |
| `parallel.opened` / `parallel.joined` | approved batch boundaries |
| `agent.telemetry` | `{"final_request_tokens"…}` — proxy metric only; V5 口径 declared, no total-token budget |
| `post_game.started` | `{"kind": "normal|emergency_minimal"}` — emergency = minimal journal + POST_GAME_PENDING |
| `post_game.completed` | `{"kind"}` — emitted exactly once, immediately before `workflow.ended`, by the CLOSED writer |
| `hooks.disabled` | the `SUMMONER_HOOKS_DISABLE` escape hatch used on a live run — a recorded contract violation, never a silent bypass |

### Positional contract

Only the phase-submit path emits `checkpoint.opened`. The change-confirm
checkpoint (its id is the one `change.resumed` names), review-result
checkpoints and recovery checkpoints emit NO `checkpoint.opened` — for every
kind, `checkpoint.displayed` is the trace-visible user-gate act, and
`user-gate-check.sh` requires an opened→displayed nonce pairing only when an
`opened` exists.

## 落盘边界 (write boundary)

The trace is authoritative evidence written only by the Go hooks. Scoring is
a DERIVED view: `scripts/score-trace.sh` writes `<trace>.score.json` next to
the trace and never appends into the trace itself. A scored run therefore
differs from an unscored one by a sidecar file, not by trace content.

## Auditing a trace by hand

```bash
# every checkpoint decision and the platform call it binds
jq -c 'select(.type=="checkpoint.decided") | {seq, cp: .payload.checkpoint_id, tool: .tool_use_id}' \
  ~/.summoner/traces-v2/<project>/<run>.jsonl

# a verdict's claimed evidence, ready to join against tool.started ids
jq -c 'select(.type=="review.verdict") | {seq, id: .payload.review_id, evidence: .payload.evidence_tool_call_ids}' \
  ~/.summoner/traces-v2/<project>/<run>.jsonl
```

The five scorers are the mechanical audit; behavioral/model evaluation is a
separate, budget-gated surface (spec §7 行为评测放行门, task T15) — the
deterministic layer invents no LLM judges.

## Related

- `scoring-system.md` — the five scorers, the four statuses, the sidecar
- `checkpoint-protocol.md` — the user-facing checkpoint render (the nonce carrier)
- `post-game-review.md` — the questionnaire that closes a run
- spec §7 — the event set's authority
