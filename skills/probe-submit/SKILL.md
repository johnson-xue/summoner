---
name: probe-submit
description: ONLY to resolve an OUTCOME_UNKNOWN episode of a live Summoner run — invoke when the crash scan marked unresolved tool outcomes and the bound probes have run; never for anything else
user-invocable: false
when_to_use:
  - The run sits in OUTCOME_UNKNOWN (a session restart or turn end marked mutating intents whose results never landed) and every marked unknown's probe has run with an observed result
  - Never to mutate anything, replay a crashed action, submit an outcome verdict, or resolve an unknown without its own bound probe
---

# probe-submit — the ONLY OUTCOME_UNKNOWN resolution channel

Zero logic lives here. This skill exists so the PreToolUse gate can observe
and validate the resolution batch (spec §5 OUTCOME_UNKNOWN 与副作用恢复): the
main session MUST resolve every episode through `Skill(summoner:probe-submit,
args=<batch JSON>)`. Legal ONLY while the run is OUTCOME_UNKNOWN. Mutating
actions are denied during the episode and NEVER auto-replayed — a bound probe
per unknown, then this channel, is the whole recovery story.

## How an episode resolves

1. The crash scans (SessionStart after a restart: tools + dead worker
   delegations; Stop at turn end: tools only — a live delegation is not a
   crash) mark each dangling mutating intent `tool.outcome_unknown` WITH its
   frozen mutation declaration (capability, target, preimage digest, expect
   digest).
2. For EVERY marked id, run its EXACT probe command — `/summoner:status`
   serves the bytes in the "outcome unknowns" section:
   - write/edit capability: `shasum -a 256 '<target>'` — run it BYTE-EXACT.
     The first byte-exact call binds the one-shot ticket at admission; the
     PostToolUse matcher records the verdict from the observed stdout
     (digest == expect → `accepted`; == preimage → `failed`; anything else
     or no result → `indeterminate`). A lost observation resolves
     `indeterminate` — there is never a second free probe.
   - unsupported capability: no exact probe exists; the entry resolves
     `indeterminate`-only.
3. Submit the whole batch. The gate validates every pair against the run's
   trace — marking freshness, probe-evidence receipts, and that each probe
   call is the BOUND byte-exact command — BEFORE any transition; one bad
   pair resolves nothing.
4. All `accepted` → the run restores its pre-episode status. Any `failed` or
   `indeterminate` → a RECOVERY checkpoint opens and the user decides
   (CONTINUE accepts the status quo / RECALL / STOP).

## args = the resolution batch (one JSON object)

```json
{
  "op": "resolve",
  "unknown_tool_use_ids": ["toolu_01ABC…", "toolu_01DEF…"],
  "probe_tool_use_ids": ["toolu_01probe1…", "toolu_01probe2…"]
}
```

Field rules:

- `op` MUST be `"resolve"`.
- There is NO outcome field. Outcomes are no longer accepted on the wire —
  the PostToolUse matcher computes every verdict mechanically from the
  observed probe stdout; an `outcome`/`outcomes` field is rejected.
- `unknown_tool_use_ids` and `probe_tool_use_ids` are equal-length arrays,
  positionally joined — every unknown resolves through its OWN bound probe;
  one probe for two unknowns is rejected, and so is a reused or unbound
  probe call.
- A re-delivered identical batch is an idempotent no-op; a different probe
  for an already-resolved id is rejected.

On success each newly resolved entry traces `tool.resolved` with its probe
receipt. Rejections (exit 2) name the reason and are never traced.
