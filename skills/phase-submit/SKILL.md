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
  "summary": "nil deref in the task loader",
  "outcome": "unclear",
  "conclusion": {
    "summary": "TaskModule.handle() dereferences player.SubTask without a nil check",
    "evidence": ["task/module.go:88", "task/module_test.go:41 reproduces"],
    "approach": "read the call chain from the error site, then ran the existing test",
    "open_questions": ["does the team-mode hero ban apply to sub-tasks?"],
    "impact": "task-list loads for players with a completed sub-task"
  },
  "artifacts": [
    {"path": "docs/reviews/2026-11-10-npe.md", "digest": "<sha256 of the file bytes>", "kind": "report"}
  ],
  "criteria_ids": ["crit-root-cause-identified", "crit-evidence-cited"],
  "check_refs": ["check://has-readme", "check://unit"]
}
```

## The conclusion comes first

The user's checkpoint shows a `结论` block built from `conclusion` and
little else (`../../references/checkpoint-protocol.md` is the render
contract). Write it for someone who never saw the search:

- `conclusion.summary` — what the phase established, stated as a
  conclusion: for a diagnosis the root cause, for an implement phase what
  now works. A narrative of the search belongs in `approach`.
- `conclusion.evidence` — what establishes the summary: `file:line`, test
  names, command results the user can re-check, heaviest first.
- `conclusion.approach` — how you established it: where you looked, why
  there, what you ran.
- `conclusion.open_questions` — what stays undecided and needs the user's
  judgment. List every real one; an empty list is the claim that nothing is
  open.
- `conclusion.impact` — who or what the result affects.
- `outcome` — `confirmed` (established on the evidence above), `failed`
  (the phase's goal was not met), `unclear` (holes remain). Omitted reads
  `unclear`. A listed open question stores `unclear` whatever was claimed.

Done means: a reader who sees only the `结论` block can say what was found,
why it is believed, and what is still undecided.

## Field rules

- `phase_id` — MUST equal the currently open phase (a mismatch is rejected).
- `conclusion.summary` + `conclusion.approach` — REQUIRED on a diagnosis
  submission (the root cause and how it was reached); the gate rejects a
  diagnosis without them and names the missing field. Other phases may omit
  the conclusion — the checkpoint then shows each missing part as 未提供.
- `outcome` — one of `confirmed`, `failed`, `unclear`.
- `summary` — a short title (≤80 chars ideal): the acceptance record's
  content and the `经过` line of later checkpoints. The checkpoint body
  shows the conclusion, so keep this to the title.
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
(WAIT_USER). Then:

1. Run `${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status` and paste the
   checkpoint message it ends with, verbatim. A short explanation of the
   conclusion may precede it (`../../references/checkpoint-protocol.md` is
   the render contract).
2. END the turn at once. The Stop hook binds the display and hands back the
   exact decision question — raise it with AskUserQuestion in the continued
   turn (nonce in the question, options = the labels it listed). Raise it
   before ending the turn again.

Submit once per phase; a submission while a checkpoint is pending or a
requirement change is open (CHANGE_OPEN) is rejected.
