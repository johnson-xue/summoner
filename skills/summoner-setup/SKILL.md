---
name: summoner-setup
description: Initialize Summoner for this project (one-command setup)
when_to_use:
  - User says "setup summoner" or "initialize summoner"
  - User wants to onboard a new project to Summoner
  - User asks "how to use summoner in this project"
allowed-tools:
  - Bash
  - Read
---

# Summoner Setup Skill

Thin entry: the setup wizard is the installed script. Run it — do not
reimplement setup, manifest generation, or memory initialization here.

## Usage

```bash
"${CLAUDE_PLUGIN_ROOT}/scripts/summoner-setup.sh"            # interactive
"${CLAUDE_PLUGIN_ROOT}/scripts/summoner-setup.sh" --quick    # CI / all defaults
```

`CLAUDE_PLUGIN_ROOT` comes from the installed plugin runtime — never a
hardcoded `~/.claude/plugins/...` path. There is no `/summoner:setup`
command; this skill IS the setup entry (say "setup summoner" to trigger it).
After setup, restart Claude Code if the plugin/hooks were newly installed,
then try `/summoner:fix "your bug description"`.

## What the wizard does (v2 semantics)

1. Generates a v2 `summoner.yaml` capability map — atomic: the candidate is
   validated by `scripts/validate-manifest.sh` before rename, so any failure
   leaves an existing manifest untouched. `--quick` never reads stdin;
   `--force` replaces explicitly (no legacy merge).
2. Validates an EXISTING manifest — no silent v1 repair or merge.
3. Initializes the version-independent memory DB (honors
   `SUMMONER_MEMORY_DIR`).

Field reference: `references/manifest-spec.md`. Missing-manifest semantics
(built-in defaults in the same reducer, no auto-init): see
`references/no-manifest-flow.md` and `skills/auto-setup-summoner/SKILL.md`.

## Troubleshooting

- Setup errors: read the wizard's own output — it names the failing step
  (manifest format, validation, memory init). A v1 file needs explicit
  migration, not a version bump.
- Hooks inactive after restart: verify the plugin is installed/enabled; the
  `hooks/bin/` binaries ship with it (no manual build).
- A failing memory DB is NOT fixed by deleting it — memory holds run
  patterns and journals. Address the reported cause and rerun
  `scripts/init-memory-db.sh`; see `references/memory-chain.md`.

## Related

- `scripts/summoner-setup.sh` — the wizard (delegates to the single v2
  emitter `scripts/summoner-bp.sh`)
- `scripts/summoner-init.sh` — interactive init modes 1/2/3
- `scripts/init-memory-db.sh` — memory DB initialization
