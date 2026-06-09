---
name: summoner
description: Routes user intent through summoner.yaml manifest to domain skills. Enforces checkpoint protocol and post-game review. Use when user invokes any /summoner:* command or expresses intent matching a Summoner workflow.
when_to_use:
  - User invokes /summoner:fix, /summoner:new, /summoner:ship, /summoner:debug, /summoner:ops, or /summoner:review
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

- User invokes `/summoner:fix`, `/summoner:new`, `/summoner:ship`, `/summoner:debug`, `/summoner:ops`, or `/summoner:review`
- User expresses intent that matches a Summoner workflow (e.g., "帮我排查这个线上 bug" → suggest `/summoner:fix`)

**When NOT to use:**
- User explicitly says "直接用 my-debug-skill" or names a specific domain skill → route directly, skip Summoner orchestration
- Pure Q&A, no workflow needed → respond directly

## Core Operating Behaviors

### 0. Memory Retrieval (Phase 0)

Before starting any workflow, check Summoner Memory for relevant historical patterns.

1. Read `project.name` from the project's `summoner.yaml`
2. Check if memory database exists: `memory/{project-name}.db`
   - If not: run `scripts/init-memory-db.sh {project-name}` to create and seed it
3. Extract features from user input:
   - error_codes: scan for SC_Err*, "panic", "nil pointer", "index out of range"
   - module: infer from log file paths (e.g. player/task/task.go → task)
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
8. Token budget:
   - Normal: Top 5 patterns, full summaries (≤1200 tokens)
   - Level 1: Top 3 patterns, truncated summaries if near cap (≤700 tokens)
   - Level 2: Top 1 pattern, short summary if still overflowing (≤300 tokens)
   - Skip: Phase 0 skipped if even Level 2 would overflow (0 tokens)
   - Degradation is automatic — always respect the 1500 token hard cap
9. Platform compatibility:
   - Claude Code: Memory DB path resolved via hook-injected context
   - Other platforms (Gemini, OpenCode, Aider, etc.): derive DB path manually:
     DB_FILE="${HOME}/.claude/plugins/summoner/memory/${PROJECT_NAME}.db"
     where PROJECT_NAME is read from summoner.yaml project.name field
   - If DB file doesn't exist at the expected path: skip Phase 0, proceed to Phase 1

### 1. Skill Resolution

On receiving a command, read the project's `summoner.yaml`:

```
1. Locate summoner.yaml at project root
2. If not found: "This project has no summoner.yaml. Summoner needs a manifest to know which skills to use. Create one? [y/n]"
3. If found: resolve each phase in the workflow to its skill
4. If a phase has skill: "none": skip that phase (explicit no-capability)
5. If a phase is not in the manifest: use superpowers default (define → brainstorming, plan → writing-plans, review → requesting-code-review)
```

### 2. Checkpoint Enforcement

After each phase completes:
1. Output the SUMMONER CHECKPOINT block (exact format in `references/checkpoint-protocol.md`)
2. Wait for user response
3. Scan for interrupt signals (per `references/checkpoint-protocol.md` interrupt signal grammar)
4. Execute the selected action: continue / skip / done / recall / stop

**Iron Law:** Never auto-advance past a checkpoint. Never assume the user wants to continue.

### 3. Phase Execution

For each phase in the workflow chain:
1. Read the phase's skill from manifest
2. Invoke the skill via the Skill tool: `Skill(skill="<skill-name>", args="<user's original input>")`
3. The skill runs its internal workflow and returns results
4. Summoner extracts: what was accomplished, artifacts produced, issues found
5. Output checkpoint

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

(M) = Mandatory — iron law, cannot be skipped.

## References

- `commands/<name>.md` — Workflow-specific rules and rationalizations
- `references/workflow-reference.md` — Full workflow definitions, auto-skip, rationalizations, red flags, verification
- `references/checkpoint-protocol.md` — Checkpoint output format and interrupt signal grammar
- `references/post-game-review.md` — 5-type questionnaire, journal, and memory write protocol
- `references/memory-chain.md` — Memory retrieval and write protocols
- `references/manifest-spec.md` — summoner.yaml field specification
- `references/persona-composition.md` — Persona composition rules and anti-patterns
