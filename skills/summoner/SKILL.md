---
name: summoner
description: Routes user intent through summoner.yaml manifest to domain skills. Enforces checkpoint protocol and post-game review. Use when user invokes any /summoner:* command or expresses intent matching a Summoner workflow.
when_to_use:
  - User invokes /summoner:fix, /summoner:new, /summoner:ship, /summoner:debug, /summoner:ops, /summoner:review, or /summoner:release
  - User says "帮我排查这个 bug" or "add a new feature" and needs structured workflow
  - User wants checkpoint-based review (stop/recall/skip at any phase)
allowed-tools:
  - Skill
  - Read
  - Write
  - Edit
  - Bash
  - Agent
  - TaskCreate
  - TaskUpdate
---

# Summoner — AI Agent Orchestration Framework

## Overview

Summoner is the routing hub. It reads the project's `summoner.yaml` manifest, resolves phase names to domain skills, enforces checkpoint pauses between phases, and triggers post-game review at workflow end. It does NOT implement any domain logic itself — that lives in project skills.

## When to Use

- User invokes `/summoner:fix`, `/summoner:new`, `/summoner:ship`, `/summoner:debug`, `/summoner:ops`, `/summoner:review`, or `/summoner:release`
- User expresses intent that matches a Summoner workflow (e.g., "帮我排查这个线上 bug" → suggest `/summoner:fix`)

**When NOT to use:**
- User explicitly says "直接用 my-debug-skill" or names a specific domain skill → route directly, skip Summoner orchestration
- Pure Q&A, no workflow needed → respond directly

## Core Operating Behaviors

### 0. Memory Retrieval (Phase 0)

Before starting any workflow, check Summoner Memory for relevant historical patterns.

1. Read `project.name` from the project's `summoner.yaml`
2. Check if memory database exists: `memory/{project-name}.db` (under Summoner plugin root — the installed plugin dir, resolved via `${CLAUDE_PLUGIN_ROOT}` in Claude Code; the actual path is typically `~/.claude/plugins/cache/summoner-marketplace/summoner/<version>/memory/`)
   - If not: run `${CLAUDE_PLUGIN_ROOT}/scripts/init-memory-db.sh {project-name}` to create and seed it (in Claude Code). On other platforms, locate the installed plugin dir (contains `scripts/` + `memory/`) — do NOT use `~/.claude/plugins/summoner/` if that path lacks a `scripts/` dir.
3. Extract features from user input:
   - error_codes: scan for SC_Err*, "panic", "nil pointer", "index out of range"
   - module: infer from log file paths (e.g. `player/task/task.go` → try both `player` (the top subsystem dir, usually the right token) and `task`; `internal/module/game/player/guild/...` → `player` or `guild`). The inferred token is matched against the patterns' `modules` column via LIKE. If the most specific token (e.g. `task`) returns nothing, fall back to the broader subsystem token (`player`) before concluding "no match".
   - keywords: tokenize Chinese phrases and English words
4. Query patterns table with each extracted feature:
   ```
   SELECT name, type, summary, hits, priority
   FROM patterns
   WHERE (error_codes LIKE '%' || ? || '%'
          OR modules LIKE '%' || ? || '%'
          OR keywords LIKE '%' || ? || '%')
     AND priority != 'low'
   ORDER BY CASE priority WHEN 'high' THEN 0 WHEN 'medium' THEN 1 ELSE 2 END,
            hits DESC
   LIMIT 5
   ```
5. If matches found, present to user:

```
┌──────────────────────────────────────────────┐
│  📚 Summoner Memory — 匹配到 {N} 条历史经验    │
│                                              │
│  {type_emoji} {name} (匹配: {star_rating})    │
│     {summary}                                │
│     [hits: {hits}]                           │
│                                              │
│  [enter] 加载经验继续  [no] 忽略              │
└──────────────────────────────────────────────┘
```

   - Star rating: ★★★★★ (exact error_code + module match), ★★★★ (same module), ★★★ (keyword match)
   - Type emoji: 🐛 correction, ⚡ skip, 💡 knowledge, 🎨 style
6. If user selects "no" or no patterns match: skip, proceed to Phase 1 (zero token cost)
7. If user selects "enter": loaded patterns inform the diagnosis and repair strategy
8. Token budget (hard cap: 1500 tokens for Phase 0 output):
   - Estimate token cost before each level: `estimated_tokens = pattern_count * avg_summary_length / 3`
   - Normal (≤1200 tokens estimated): Top 5 patterns, full summaries
     → Trigger: when estimated < 1000, safe to show all
   - Level 1 (≤700 tokens estimated): Top 3 patterns, truncated summaries
     → Trigger: when estimated ≥ 1000 OR total conversation context > 70% of model limit
   - Level 2 (≤300 tokens estimated): Top 1 pattern, short summary
     → Trigger: when estimated > 700 OR conversation context > 85% of model limit
   - Skip (0 tokens): Phase 0 skipped entirely
     → Trigger: when even Level 2 would push total context > 90% of model limit
   - Degradation is automatic and silent — do not explain the choice, just apply it.
   - When in doubt, degrade one level lower than you think. Erring on the side of less output is always correct.
9. Platform compatibility:
   - Claude Code: Memory DB path resolved via hook-injected context (`${CLAUDE_PLUGIN_ROOT}/memory/{project-name}.db`)
   - Other platforms (Gemini, OpenCode, Aider, etc.): derive DB path manually — find the installed Summoner plugin dir (the one containing both `scripts/` and `memory/`, e.g. `~/.claude/plugins/cache/summoner-marketplace/summoner/<version>/`):
     DB_FILE="${SUMMONER_PLUGIN_ROOT}/memory/${PROJECT_NAME}.db"
     where PROJECT_NAME is read from summoner.yaml project.name field and SUMMONER_PLUGIN_ROOT is the installed plugin root. Avoid `~/.claude/plugins/summoner/` if it only has `.git/` + `memory/` but no `scripts/` (that's an abandoned manual clone, scripts live in the cache install).
   - If DB file doesn't exist at the expected path: skip Phase 0, proceed to Phase 1

### 1. Skill Resolution

On receiving a command, read the project's `summoner.yaml`:

```
1. Locate summoner.yaml at project root
2. If found: resolve each phase in the workflow to its skill → proceed to Phase Execution
3. If a phase has skill: "none": skip that phase (explicit no-capability)
4. If a phase is not in the manifest: use superpowers default (define → brainstorming, plan → writing-plans, review → requesting-code-review)
5. If NOT found: set MANIFEST_MISSING = true. Phase 0 is auto-skipped (cannot resolve project.name). Phases 1-2 use superpowers defaults. At Phase 3 of /summoner:new, present the No Manifest menu below. For /summoner:fix Phase 3 (freeform fix), skip the menu — use the standard freeform fix flow.
```

**No Manifest Handling (Hybrid A+B) — /summoner:new Phase 3 only:**

When `summoner.yaml` is not found, do NOT silently fall back to generic skills. Phase 0 (Memory Retrieval) is auto-skipped — it cannot resolve `project.name` to locate the memory DB. Phases 1-2 (define, plan) use superpowers defaults since they are always generic. At Phase 3 (implementation) start, present this interactive menu:

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
│      (e.g. antia-subsystem, my-rpc)    │
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
- **Option 2:** Ask the user for the skill name (e.g. `antia-subsystem`, `my-rpc-skill`).
  1. **Scan for available skills first:** Run `find .claude/skills skills ~/.claude/skills -name "SKILL.md" 2>/dev/null | head -20` and `grep '^name:'` on each to build a list of locally-installed skill names. Present these as suggestions before asking for input.
  2. **Validate the entered name:** After the user provides a skill name, verify it exists:
     - If the name is a local skill (found in the scan): use it directly.
     - If the name contains a `:` (e.g. `superpowers:subagent-driven-development`): check if it's a known plugin skill by looking for it in `~/.claude/plugins/` or the session's available skills list.
     - If the name is NOT found anywhere: warn the user: "Skill 'X' was not found. It may fail at invocation. Available skills: [list top 5]. Use it anyway? [y/N]"
  3. Use the validated skill for this session only — remind the user that creating a `summoner.yaml` will make this permanent.
- **Option 3:** Continue with generic `superpowers:subagent-driven-development` for implementation. Explicitly warn: "Proceeding without project conventions — no LSP, no codebase-memory, no project-specific toolchains." Continue with superpowers defaults for remaining phases.

**Iron Law:** Never silently fall back to generic execution when the manifest is missing. Always surface the choice to the user.

### 2. Checkpoint Enforcement

Each phase has a **START block** (entering) and a **CHECKPOINT block** (end) — both defined in `references/checkpoint-protocol.md`.

**Entering a phase:**
1. Output the PHASE START block (lightweight 3-line plain-text: Workflow + Phase N/Total + 任务 + Skill) — gives the user continuous context of "which phase, doing what task"

**After each phase completes:**
1. Output the SUMMONER CHECKPOINT block (exact format in `references/checkpoint-protocol.md`)
2. Wait for user response
3. Scan for interrupt signals (per `references/checkpoint-protocol.md` interrupt signal grammar)
4. If the reply is content feedback (not a pure flow decision), handle the feedback first, then re-output the CHECKPOINT block — do NOT auto-advance
5. Execute the selected action: continue / skip / done / recall / stop

**Iron Law:** Never auto-advance past a checkpoint. Never assume the user wants to continue. Never misread content feedback as CONTINUE.

### 3. Phase Execution

For each phase in the workflow chain:

**When manifest IS available (or user specified skill via Option 2):**
1. Output PHASE START block (Workflow/Phase N/Total/任务/Skill)
2. Read the phase's skill from manifest (or use the manually-specified skill for Phase 3)
3. Invoke the skill via the Skill tool: `Skill(skill="<skill-name>", args="<user's original input>")`
4. The skill runs its internal workflow and returns results
5. Summoner extracts: what was accomplished, artifacts produced, issues found
6. Output CHECKPOINT block (field spec + anti-examples in `references/checkpoint-protocol.md`)

**When manifest is NOT available — after No Manifest menu resolution:**

Phase 1-2 always use superpowers defaults (define → `superpowers:brainstorming`, plan → `superpowers:writing-plans`). Phase 3 behavior depends on the user's menu choice:
- Option 1 (pause): manifest created → reload → continue with normal Phase Execution
- Option 2 (manual skill): use the specified skill for Phase 3, superpowers defaults for remaining
- Option 3 (generic): use the superpowers chain below for all remaining phases:
  1. Phase 3 (implement): invoke `superpowers:subagent-driven-development`
     - WARNING: Remind user: no project conventions active (LSP, codebase-memory, project toolchains unavailable)
  2. Phase 4 (test): invoke `superpowers:test-driven-development` or skip if no tests
  3. Phase 5 (review): invoke `superpowers:requesting-code-review`

**Phase 3 Routing (when manifest is available):**

At Phase 3 of `/summoner:new`, determine the implementation skill based on function type:
- New subsystem / module → `phase.subsystem` skill
- New RPC interface → `phase.rpc` skill
- GM tool / admin command → `phase.gmt` skill
- If the relevant phase is not in the manifest: ask user which skill to use (one-time), then apply for this session
- If multiple match: ask user to clarify the primary function type

For the `fix` phase (freeform — no skill mapping):
1. Present the diagnosis from Phase 1
2. Ask the user: "How would you like to fix this? I can implement the fix, or you can make changes yourself."
3. If user implements: wait for them to confirm changes are done
4. If agent implements: apply changes, show diff

### 4. Post-Game Review

**Enforcement:** The PreToolUse hook automatically detects Summoner invocation and writes a state file. The Stop hook checks this file at session end and warns the user if they may have forgotten the review. You don't need to track state — the hooks handle it.

At workflow end (user says "done" or all phases complete):
1. Determine the review type based on session events (corrections, skips, injections, verbosity complaints)
2. Present the appropriate questionnaire from `references/post-game-review.md`
3. Collect answers and write journal entry
4. Agent self-reflection: one paragraph on what could have been done better

After collecting review answers:
5. Write journal entry to SQLite: INSERT into journal table
6. Classify pattern per `references/post-game-review.md` Memory Write Protocol
7. If pattern meets persistence criteria:
   - Extract features (error_codes, modules, keywords, summary)
   - Check for existing pattern by name → UPDATE hits or INSERT new
   - Report: "📝 Memory updated: {pattern_name} (hits: {N})"
8. If pattern matches existing: "📝 Memory: {pattern_name} hits now {N}"
9. Report new hit counts for any updated patterns

## Workflow Quick Reference

Full definitions: Read `references/workflow-reference.md` (workflow diagrams, auto-skip conditions, rationalizations, red flags, verification checklist). Read the specific command file (e.g., `commands/fix.md`) for workflow-specific rules.

| Command | Phase Chain |
|---------|------------|
| `/summoner:fix` | debug(M) → reproduce → fix → verify → review |
| `/summoner:new` | define → plan → implement → test → review |
| `/summoner:ship` | fan-out(1-3 personas) → merge |
| `/summoner:debug` | debug only, no code changes |
| `/summoner:ops` | ops skill (delegated) |
| `/summoner:review` | code review only |
| `/summoner:release` | version-plan → changelog → release-execution (self-contained, no manifest routing) |

(M) = Mandatory — iron law, cannot be skipped.

## Available Personas (dispatch as subagents)

| Persona | Use for |
|---------|---------|
| `summoner:code-reviewer` | Standalone code review, ship fan-out |
| `summoner:security-auditor` | Security vulnerability audit, ship fan-out |
| `summoner:test-engineer` | Test coverage analysis, Prove-It checks, ship fan-out |
| `summoner:debug-agent` | Root cause analysis, stack trace diagnosis, error pattern matching |

## References

- `commands/<name>.md` — Workflow-specific rules and rationalizations
- `references/workflow-reference.md` — Full workflow definitions, auto-skip, rationalizations, red flags, verification
- `references/checkpoint-protocol.md` — Checkpoint output format and interrupt signal grammar
- `references/post-game-review.md` — 5-type questionnaire, journal, and memory write protocol
- `references/memory-chain.md` — Memory retrieval and write protocols
- `references/manifest-spec.md` — summoner.yaml field specification
- `references/persona-composition.md` — Persona composition rules and anti-patterns
