---
name: summoner-setup
description: Initialize or repair a project's Summoner configuration when the user explicitly requests it, or guide them when they ask about the missing-manifest warning; never auto-initialize solely because SessionStart reports missing configuration.
when_to_use:
  - User says "setup summoner" or "initialize summoner"
  - User wants to onboard a new project to Summoner
  - User asks "how to use summoner in this project"
  - User asks about the missing summoner.yaml warning or says Summoner is not initialized
allowed-tools:
  - Bash
  - Read
---

# Summoner Setup Skill

Thin entry: the setup wizard is the installed script. Run it — do not
reimplement setup, manifest generation, or memory initialization here.

## What missing configuration means

A missing `summoner.yaml` means **未配置项目专属能力**, not "Summoner cannot
run." Disclosed built-in inline capabilities use the **same reducer** and
checkpoint protocol (see `../../references/no-manifest-flow.md`). Do not
introduce a second execution chain, an initialization precondition in
commands, or a silent fallback for a present but invalid manifest.

## Inspect before writing

1. Locate the actual installed plugin root from `CLAUDE_PLUGIN_ROOT` or the
   plugin location supplied by the session. Do not assume a manual clone path
   or guess a cached version.
2. Read an existing manifest first. Validate with
   `${CLAUDE_PLUGIN_ROOT}/scripts/validate-manifest.sh summoner.yaml`.
   Only string `version: "2"` is accepted. A v1 file needs explicit
   migration according to `../../references/manifest-spec.md`; changing its
   version alone is insufficient.
3. Distinguish missing capabilities from missing memory. Memory belongs at
   `${SUMMONER_MEMORY_DIR:-$HOME/.summoner/memory}`, not inside a plugin
   version's cache. Use the validated `project.name` as its namespace.

## Offer the smallest appropriate action

- **No manifest:** explain that built-in defaults remain usable, and offer
  quick setup, interactive capability selection, or leaving the project
  unconfigured. Do not create files just because SessionStart emitted a
  warning.
- **Existing valid v2:** preserve it; setup can initialize/repair missing
  memory without overwriting its project capabilities.
- **Invalid/v1 manifest:** stop and explain the migration error. Preserve
  custom bindings and obtain explicit consent before replacement. `--force`
  replaces the whole manifest; it does not migrate or merge legacy
  workflows.

## Run only after the user chooses setup

Run from the project directory. Quick mode is genuinely non-interactive:

```bash
"${CLAUDE_PLUGIN_ROOT}/scripts/summoner-setup.sh" --quick --project sample-project
```

Use the user's chosen portable project namespace, not the literal example.
Without `--project`, quick mode uses a safe directory-name default. For an
interactive terminal, run `../../scripts/summoner-setup.sh` without
`--quick`; init modes 1/2/3 mean choose skills / quick defaults / manual
names. Do not hang an unattended tool call on interactive prompts: offer
the terminal command or collect the desired inputs first.

`CLAUDE_PLUGIN_ROOT` comes from the installed plugin runtime — never a
hardcoded `~/.claude/plugins/...` path.

## What the wizard does (v2 semantics)

1. Generates a v2 `summoner.yaml` capability map — atomic: the candidate is
   validated by `../../scripts/validate-manifest.sh` before rename, so any
   failure leaves an existing manifest untouched. `--quick` never reads
   stdin; `--force` replaces explicitly (no legacy merge).
2. Validates an EXISTING manifest — no silent v1 repair or merge.
3. Initializes the version-independent memory DB (honors
   `SUMMONER_MEMORY_DIR`).

The three setup entry points share one v2 emitter. It validates a temporary
candidate before atomic rename. Any failure leaves the existing manifest
unchanged. There is no v1 emission, merge, grep-validator fallback or
dry-run flag. Field reference: `../../references/manifest-spec.md`.

## Explain the resulting configuration honestly

- `phases` maps capabilities to inline providers. Each has one default and
  optional explicitly named alternates, not job-role handoffs.
- `phases.review.verification_spec` adds criteria, registered check ids,
  readable scope and a rubric pointer. It is not a review skill or a fixed
  reviewer team.
- `check_registry` uses the shipped CheckDef fields and verified definition
  digests. Mechanical envelope references freeze `check_id@digest`.
- Tool scope never grants permissions. A written `suggested_budgets`
  applies as each run's starting limits (no confirmation question since
  0.6.1); omitting it runs unlimited.
- Project checks may honestly be `BLOCKED` when the required sandbox is
  absent. A successfully written manifest is not proof of runtime isolation.
- Restart is needed only if the plugin/hooks themselves were installed or
  changed, not merely because a manifest was created.

Setup consent does not advance a running workflow. Keep the same run state
and any pending checkpoint; only its bound AskUserQuestion decision can
advance it. All diagnosis, evidence and post-game obligations remain in
effect.

## Troubleshooting

- Setup errors: read the wizard's own output — it names the failing step
  (manifest format, validation, memory init).
- Hooks inactive: enforcement is **opt-in** (0.7.1) — set
  `SUMMONER_HOOKS_ENFORCE=1` in the environment Claude Code starts with (the
  `env` block of the user or project settings) and restart. Then verify the
  plugin is installed/enabled; the `hooks/bin/` binaries ship with it (no
  manual build).
- If manifest validation succeeds but memory initialization fails, report
  the separate failure and rerun `../../scripts/init-memory-db.sh` with the
  validated namespace after addressing the cause. A failing memory DB is NOT
  fixed by deleting it — memory holds run patterns and journals
  (`../../references/memory-chain.md`).

## Related

- `../../scripts/summoner-setup.sh` — the wizard (delegates to the single v2
  emitter `../../scripts/summoner-bp.sh`)
- `../../scripts/summoner-init.sh` — interactive init modes 1/2/3
- `../../scripts/init-memory-db.sh` — memory DB initialization
