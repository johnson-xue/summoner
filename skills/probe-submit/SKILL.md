---
name: probe-submit
description: ONLY to resolve an OUTCOME_UNKNOWN episode of a live Summoner run — invoke when the crash scan marked unresolved tool outcomes and the read-only probes have run; never for anything else
user-invocable: false
when_to_use:
  - The run sits in OUTCOME_UNKNOWN (a session restart or turn end marked mutating intents whose results never landed) and every marked unknown has been probed with a read-only Bash call whose result was observed
  - Never to mutate anything, replay a crashed action, or resolve an unknown without its own observed probe
---

# probe-submit — the ONLY OUTCOME_UNKNOWN resolution channel

Zero logic lives here. This skill exists so the PreToolUse gate can observe
and validate the resolution batch (spec §5 OUTCOME_UNKNOWN 与副作用恢复): the
main session MUST resolve every episode through `Skill(summoner:probe-submit,
args=<batch JSON>)`. Legal ONLY while the run is OUTCOME_UNKNOWN. Mutating
actions are denied during the episode and NEVER auto-replayed — a read-only
probe per unknown, then this channel, is the whole recovery story.

## How an episode resolves

1. The crash scans (SessionStart after a restart: tools + dead worker
   delegations; Stop at turn end: tools only — a live delegation is not a
   crash) mark each dangling mutating intent `tool.outcome_unknown`.
2. For EVERY marked id, run one read-class Bash probe (e.g. `ls`, `cat`,
   `git status`) that OBSERVES the side effect the intent may have caused,
   and let it complete — the observed result is the evidence. One probe per
   unknown; a probe is never reused.
3. Submit the whole batch: the gate validates every pair against the run's
   trace (read-class Bash, observed result, recorded after the marking, not
   failed, never reused) BEFORE any transition — a batch with one bad pair
   resolves nothing.
4. All `accepted` → the run restores its pre-episode status. Any `failed` or
   `indeterminate` → a RECOVERY checkpoint opens and the user decides
   (CONTINUE accepts the status quo / RECALL / STOP).

## args = the resolution batch (one JSON object)

```json
{
  "unknown_tool_use_ids": ["toolu_01ABC…", "toolu_01DEF…"],
  "outcome": "accepted",
  "probe_tool_use_ids": ["toolu_01probe1…", "toolu_01probe2…"]
}
```

Field rules:

- `unknown_tool_use_ids` and `probe_tool_use_ids` are equal-length arrays,
  positionally joined — every unknown resolves through its OWN probe; one
  probe for two unknowns is rejected, and so is a reused probe from any
  earlier episode.
- `outcome` is what the probes ACTUALLY showed, for the whole batch:
  `accepted` (the effect is fine / never landed harmfully), `failed` (the
  effect broke something), or `indeterminate` (the probes could not tell).
- A re-delivered identical batch is an idempotent no-op; a different outcome
  or probe for an already-resolved id is rejected.

On success each newly resolved entry traces `tool.resolved` with its probe
receipt. Rejections (exit 2) name the reason and are never traced.
