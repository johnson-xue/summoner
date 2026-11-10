# Missing Project Manifest

Read only when the project root has no `summoner.yaml`. There is **one execution
loop**, not a special no-manifest workflow. Main-session inline capabilities
use the same Go reducer, phase routes, evidence checks and user gates as a
configured project.

## Disclose the configuration state

Say **“未配置项目专属能力”** (project-specific capabilities are not configured).
Built-in providers do not supply the project's conventions, test commands,
registered checks or deployment procedures. A missing manifest is not evidence
that those requirements do not exist.

Offer setup without making it a prerequisite for the existing workflow:

- Configure now, on the user's explicit request, using
  `${CLAUDE_PLUGIN_ROOT}/scripts/summoner-setup.sh --quick` or the interactive
  setup. It creates a v2 capability map and initializes memory.
- Work with the disclosed built-in inline capabilities in the **same** run.
  The provider defaults and lack-of-capability rules are in
  `references/manifest-spec.md`. There is no late implementation menu and no
  separate execution chain.
- Stop or pause when project-specific context is necessary. Do not invent a
  capability, silently substitute a missing skill, or claim an unperformed
  check passed.

Setup consent is not workflow advancement. If a run is already waiting, its
checkpoint decision still comes from the normal bound AskUserQuestion channel.
Creating or reloading a manifest neither clears the wait nor starts a new run.
SessionStart reports readiness and a setup pointer; it never initializes files
without consent. `skills/auto-setup-summoner/SKILL.md` handles requested setup.

## Same reducer, same obligations

- The fixed framework route still applies; every phase ends at its checkpoint.
- Without `project.name`, project memory retrieval is unavailable. Record that
  fact in the context phase, submit it, and keep its normal checkpoint. Do not
  auto-advance or silently drop the phase.
- Diagnosis in fix/debug remains mandatory and must be accepted before code
  changes. Built-in diagnosis happens in the main session. `skill: none`, a
  missing provider or no manifest never grants write permission or a skip.
- Implementation and tests share the main session. No provider may create an
  unapproved Agent batch or replace the run with its own orchestration loop.
- Review uses the same frozen acceptance envelope and one restricted,
  independent reviewer. No project check registry means no invented mechanical
  evidence. Shipped L1 checks remain available; unavailable L2 isolation remains
  honestly `BLOCKED`. Explicitly skipped review is `UNREVIEWED`, never PASS.
- User gates, recall, budget confirmation, stop/recovery and mandatory post-game
  handling are unchanged. An emergency stop still leaves `POST_GAME_PENDING`.

After setup, validate and reload the capabilities for the existing run. Preserve
accepted requirements, state and checkpoints; any proposed change to accepted
criteria follows the normal versioned acceptance mechanism. A present but
invalid/v1 manifest is an error with a migration hint, **not** permission to
enter the missing-manifest defaults.
