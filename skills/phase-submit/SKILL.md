---
name: phase-submit
description: ONLY to submit a completed phase result — invoke when a phase's work is done; never for anything else
user-invocable: false
when_to_use:
  - A phase's work is complete and its result must reach the user's checkpoint decision
  - Never to open a phase, ask a question, report progress, or bypass a pending checkpoint
---

# phase-submit — the ONLY phase-submission channel

Zero logic lives here. This skill exists so the PreToolUse gate can observe and
validate one structured submission event per phase (spec §1 阶段提交): the main
session MUST submit every completed phase through `Skill(summoner:phase-submit,
args=<phase-result JSON>)`. Any other way of declaring a phase "done" — plain
prose, another skill, waiting silently — advances nothing. The gate validates
the payload against the run state; a rejection (exit 2) names the reason and
records `phase_submitted.rejected`.

## args = one phase-result JSON object

```json
{
  "phase_id": "diagnosis",
  "summary": "one paragraph — what the phase concluded, not how",
  "artifacts": [
    {"path": "docs/reviews/2026-11-10-npe.md", "digest": "<sha256 of the file bytes>", "kind": "report"}
  ],
  "criteria_ids": ["crit-root-cause-identified", "crit-evidence-cited"],
  "check_refs": ["check://has-readme", "check://unit"]
}
```

Field rules:

- `phase_id` — MUST equal the currently open phase (a mismatch is rejected).
- `summary` — the checkpoint note the user reads; declarative, ≤80 chars ideal.
- `artifacts` — every file the phase produced, each with its content digest at
  submission time (the checkpoint freezes these versions; changing an artifact
  after submission invalidates a review of it). No artifacts → omit or `[]`.
  Never list `summoner.yaml` when citing checks — a definition inside the
  artifacts under review carries no authority.
- `criteria_ids` — the exit criteria this phase claims, exactly as the
  acceptance view / plan froze them (the user accepts or rejects THIS list).
- `check_refs` — citations of registered mechanical checks that back the
  claim, each `check://<check_id>`. `trust: framework` (L1) checks re-run
  in-gate at submit (shipped adapter ops only: file-contains, digest-match,
  grep-count); `trust: project` (L2) checks need their newest observed
  result to be a PASS under the CURRENT definition — run the exact
  registered Bash line (after the in-band sandbox probe) before citing.
  Omit when the phase ran no mechanical checks.

On success the gate atomically closes the phase and opens its checkpoint
(WAIT_USER). The next step is rendering the checkpoint — run
`${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status` and paste its output
verbatim (`references/checkpoint-protocol.md` is the render contract). Never
submit twice for one phase; never submit while a checkpoint is pending or a
requirement change is open (CHANGE_OPEN) — both are rejected.
