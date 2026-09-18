# No-Manifest Flow

Loaded on demand — only when `summoner.yaml` is NOT found at the project root.
After setup this file is never read again. (Auto-setup: the SessionStart hook
warns about the missing manifest; the `auto-setup-summoner` skill handles the
guided path.)

**Iron Law:** Never silently fall back to generic execution when the manifest
is missing. Always surface the choice to the user.

## Behavior when manifest is missing

- Phase 0 (Memory Retrieval) is auto-skipped — it cannot resolve
  `project.name` to locate the memory DB.
- Phases 1-2 (define, plan) use superpowers defaults (always generic:
  define → `superpowers:brainstorming`, plan → `superpowers:writing-plans`).
- At Phase 3 of `/summoner:new`: present the interactive menu below.
- For `/summoner:fix` Phase 3 (freeform fix): skip the menu — use the standard
  freeform fix flow.

## The No-Manifest menu (/summoner:new Phase 3 only)

```
┌────────────────────────────────────────┐
│  WARNING:  No summoner.yaml found.            │
│                                        │
│  Phase 3 needs to know which project   │
│  skill to use for implementation:      │
│                                        │
│  [1] Pause — let me create             │
│      summoner.yaml first               │
│      (Recommended — runs               │
│       summoner-init.sh)                │
│                                        │
│  [2] Manually specify a skill name     │
│      (e.g. my-subsystem, my-rpc)       │
│                                        │
│  [3] Use generic skill                 │
│      (no project conventions, no       │
│       LSP, no codebase-memory)         │
└────────────────────────────────────────┘
```

- **Option 1:** Pause the workflow. Tell the user to open a separate terminal and run one of (locate the installed plugin dir — in Claude Code it's `${CLAUDE_PLUGIN_ROOT}`, typically `~/.claude/plugins/cache/summoner-marketplace/summoner/<version>/`):
  - **Quick (推荐):** `${CLAUDE_PLUGIN_ROOT}/scripts/summoner-init.sh 2` — generates `summoner.yaml` with all recommended defaults in 3 seconds, zero interaction.
  - **BP 阵容选择:** `${CLAUDE_PLUGIN_ROOT}/scripts/summoner-init.sh 1` — interactive champion select: pick a skill for each phase from the curated roster (like LoL champion select).
  - Also run `${CLAUDE_PLUGIN_ROOT}/scripts/init-memory-db.sh <project-name>` afterwards.
  - Wait for user to confirm manifest is ready, then reload and resolve phases normally.
- **Option 2:** Ask the user for the skill name (e.g. `my-subsystem`, `my-rpc-skill`).
  1. **Scan for available skills first:** Run `find .claude/skills skills ~/.claude/skills -name "SKILL.md" 2>/dev/null | head -20` and `grep '^name:'` on each to build a list of locally-installed skill names. Present these as suggestions before asking for input.
  2. **Validate the entered name:** After the user provides a skill name, verify it exists:
     - If the name is a local skill (found in the scan): use it directly.
     - If the name contains a `:` (e.g. `superpowers:subagent-driven-development`): check if it's a known plugin skill by looking for it in `~/.claude/plugins/` or the session's available skills list.
     - If the name is NOT found anywhere: warn the user: "Skill 'X' was not found. It may fail at invocation. Available skills: [list top 5]. Use it anyway? [y/N]"
  3. Use the validated skill for this session only — remind the user that creating a `summoner.yaml` will make this permanent.
- **Option 3:** Continue with generic `superpowers:subagent-driven-development` for implementation. Explicitly warn: "Proceeding without project conventions — no LSP, no codebase-memory, no project-specific toolchains." Continue with the superpowers chain below for all remaining phases.

## Post-menu phase execution (manifest NOT available)

Phase 1-2 always use superpowers defaults. Phase 3+ depends on the menu choice:
- Option 1 (pause): manifest created → reload → continue with normal Phase Execution (SKILL.md §3).
- Option 2 (manual skill): use the specified skill for Phase 3, superpowers defaults for remaining.
- Option 3 (generic): use this superpowers chain for all remaining phases:
  1. Phase 3 (implement): invoke `superpowers:subagent-driven-development`
     - WARNING: Remind user: no project conventions active (LSP, codebase-memory, project toolchains unavailable)
  2. Phase 4 (test): invoke `superpowers:test-driven-development` or skip if no tests
  3. Phase 5 (review): invoke `superpowers:requesting-code-review`
