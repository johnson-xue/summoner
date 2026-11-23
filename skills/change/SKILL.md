---
name: change
description: ONLY to open or apply a requirement change in a live Summoner run — invoke when the user adjusts requirements mid-run; never for anything else
user-invocable: false
when_to_use:
  - The user adjusts requirements while a run is live (mid-phase or at a checkpoint) and the change loop must open
  - A delta plus its impact assessment must be applied to an already-open requirement change
  - Never to submit a phase, render a checkpoint, bypass a decision, or advance the run
---

# change — the ONLY requirement-change channel

Zero logic lives here. This skill exists so the PreToolUse gate can observe
and validate the requirement-change loop's two transitions (spec §1
需求变更闭环): a free-text change request is CONTENT, not an advancement
channel — it opens this sub-loop; the main session MUST drive every
requirement change through `Skill(summoner:change, args=<JSON>)`. Saying "the
requirements changed" in prose opens nothing. The gate validates the payload
against the run state and runs the reducer transition; a rejection (exit 2)
names the reason and is never traced.

## args = one JSON object with a subcommand

`open` — freeze the development position and enter CHANGE_OPEN (legal from
ACTIVE mid-phase, and from WAIT_USER — the explicit checkpoint entry; the
pending checkpoint is superseded and re-rendered after the change closes):

```json
{
  "subcommand": "open",
  "note": "user wants logout flow covered too",
  "position": {
    "current_task": "diagnosis: nil deref at login.go:88",
    "test_progress": "repro test drafted, not yet run",
    "pending_verifications": ["reproduce run"]
  }
}
```

`apply` — submit the delta plus its impact assessment (legal only while a
change is open; bumps RequirementsVersion, marks the affected ids stale,
opens the single-point confirm checkpoint):

```json
{
  "subcommand": "apply",
  "note": "scope: login → login+logout",
  "changed": ["crit-login-repro"],
  "affected": ["crit-evid"]
}
```

Field rules:

- `note` — required both ways: what the user wants changed (open), what
  changed this time (apply). It becomes the change record / confirm note.
- `position` — required by `open`: where implementation stood (current task,
  test progress, pending verifications). Model-proposed CONTENT, never
  authorization; `current_task` must be non-empty. It is frozen exactly as
  given and restored on accept.
- `changed` / `affected` — the delta: which requirement/criterion ids the
  change rewrote, and which existing ones it touches (those go stale). The
  assessment METHOD belongs to the chosen requirement/plan skill — this
  payload only carries its conclusion.
- Unknown or mistyped fields are rejected (strict decode, fail-closed), as is
  any call outside the legal states.

On `apply` the gate opens one CHANGE_CONFIRM checkpoint — render it with
`${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status`, paste the checkpoint
verbatim and end the turn (the Stop hook binds the display and hands back
the question); raise it, and the user closes the loop with
`accept: 接受变更并恢复开发` (ACCEPT_REQUIREMENT_CHANGE), which restores the
frozen position in place —
no phase-by-phase reopen, no re-asking untouched phases.
