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

**fix vs debug disambiguation**（"排查/排查一下/look into" 同时命中两者时的裁决规则）：
- 意图包含修复期望（"修好/修一下/fix it/然后把它解决"）或未说明只要原因 → `/summoner:fix`（repair-bias 默认）
- 意图明确只要原因、不要改码（"只告诉我原因/先别动代码/don't change anything/why does this happen"）→ `/summoner:debug`
- 两可且用户明确表达犹豫 → 问一句再路由，不猜。此规则由 `evals/cases/routing.json` 固化并被 `scripts/run-routing-evals.sh`（opt-in）验证。

**When NOT to use:**
- User explicitly says "直接用 my-debug-skill" or names a specific domain skill → route directly, skip Summoner orchestration
- Pure Q&A, no workflow needed → respond directly

## Core Operating Behaviors

### 0. Memory Retrieval (Phase 0)

Before starting any workflow, check Summoner Memory for relevant historical patterns.

1. Read `project.name` from the project's `summoner.yaml`; resolve the memory DB path (`$SUMMONER_MEMORY_DIR` or `~/.summoner/memory/{project-name}.db` — version-independent; details + legacy migration in `references/memory-chain.md` §DB Path Resolution). If the DB doesn't exist: in Claude Code run `${CLAUDE_PLUGIN_ROOT}/scripts/init-memory-db.sh {project-name}`; otherwise skip Phase 0 and proceed to Phase 1.
2. **Read `references/memory-chain.md` §Phase 0: Retrieval Protocol** and follow it exactly: feature extraction (error_codes / module with subsystem-fallback / keywords), the Top-5 SQL query, the match-presentation box, and the 1500-token hard cap with silent graceful degradation (Top 5 → Top 3 → Top 1 → skip).
3. User picks `[enter]` (loaded patterns inform the workflow) or `[no]` (proceed, zero token cost).

### 1. Skill Resolution

On receiving a command, read the project's `summoner.yaml`:

```
1. Locate summoner.yaml at project root
2. If found: resolve each phase in the workflow to its skill → proceed to Phase Execution
3. If a phase has skill: "none": skip that phase (explicit no-capability)
4. If a phase is not in the manifest: use superpowers default (define → brainstorming, plan → writing-plans, review → requesting-code-review)
5. If NOT found: read `references/no-manifest-flow.md` and follow it (Phase 0 auto-skipped; Phases 1-2 use superpowers defaults; interactive menu at Phase 3 of /summoner:new; /summoner:fix Phase 3 uses the freeform flow without the menu). Iron Law: never silently fall back to generic execution.
```

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

Follow `references/no-manifest-flow.md` §Post-menu phase execution (Phase 1-2 superpowers defaults; Phase 3+ by menu choice: pause→reload / manual skill / generic superpowers chain).

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

### Graph-Mode Phase Execution (when the plan carries a `summoner-task-graph` block)

If the active plan artifact contains a fenced ` ```yaml summoner-task-graph ` block, drive the graph walker instead of the linear phase chain: **read `references/graph-mode.md`** and follow its directive loop (`summoner-walker next` → RUN_NODE / RUN_REVIEW → record → route; node-snapshot save/restore on directive; CHECKPOINT renders `walker explain`; HALT surfaces budget exhaustion). The walker is the router + bookkeeper — it never executes agents or touches the working tree. If the plan has NO task-graph block → chain fallback (§3 above; backward compat).

### Trace Emission (chain mode — feeds the deterministic scorers)

The scorers (`build-check` / `lint-check` / `test-pass-rate` / `iron-law-check`) only audit runs that leave trace data — graph mode gets it from the walker; chain mode needs these two appends per phase (shell `echo >> one line`, never rewrite). File: `${TMPDIR}/summoner-traces/<project-name>/<workflow>-$(date +%Y%m%d-%H%M%S).jsonl`

- At each PHASE START: `{"type":"phase_start","timestamp":"<ISO8601>","workflow":"<w>","phase":<n>,"name":"<phase>","skill":"<skill|none>"}`
- At each CHECKPOINT: `{"type":"phase_end","timestamp":"<ISO8601>","workflow":"<w>","phase":<n>,"status":"completed","artifacts":["..."]}`
- After build/test/lint commands: `{"type":"tool_call","timestamp":"<ISO8601>","tool":"Bash","args":{"command":"<cmd>"},"result":"<success|error>"}`

Event schema: `references/trace-protocol.md`. Scoring is post-hoc (`scripts/score-trace.sh`) — emission never blocks the workflow.

### 4. Post-Game Review

**Enforcement:** The PreToolUse hook automatically detects Summoner invocation and writes a session-keyed state file (parallel sessions don't clobber each other). The same hook enforces the chain-mode iron law: Edit/Write are denied (exit 2) until a phase skill has run in the session — no code changes before Phase 0-1. Escape hatches: an all-`skill: none` manifest pre-advances; `SUMMONER_IRON_LAW=off` disables the gate. The Stop hook checks the state file at session end and warns if the review may have been forgotten. You don't need to track state — the hooks handle it.

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
- `references/memory-chain.md` — Memory retrieval (read at Phase 0) and write protocols
- `references/no-manifest-flow.md` — No-manifest menu + post-menu execution (read only when summoner.yaml is missing)
- `references/graph-mode.md` — Walker directive loop (read only when the plan carries a summoner-task-graph block)
- `references/manifest-spec.md` — summoner.yaml field specification
- `references/persona-composition.md` — Persona composition rules and anti-patterns
