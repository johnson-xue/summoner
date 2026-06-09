# Summoner — AI Agent Orchestration Framework

> Define AI workflows like Makefile targets. Framework verbs fixed — project skills replaceable.

## Quick Start (Any Platform)

1. Add `summoner.yaml` to your project root declaring your skill mappings
2. When you encounter a bug: invoke the `fix` workflow below
3. When adding a feature: invoke the `new` workflow below

## Workflows

### fix — Bug Repair Pipeline
Phase 0 (Memory Chain) -> Phase 1 diagnose (MANDATORY) -> reproduce -> fix -> verify -> review

### new — Feature Pipeline
Phase 0 (Memory Chain) -> define -> plan -> implement -> test -> review

### ship — Pre-Launch Review
Phase 0 (Memory Chain) -> fan-out(1-3 personas) -> merge -> go/no-go

### debug — Diagnosis Only
Phase 0 (Memory Chain) -> diagnose, no code changes

### ops — Operations
Phase 0 (Memory Chain) -> delegate to project ops skill

### review — Code Review
Phase 0 (Memory Chain) -> standalone 5-axis review

## Memory Chain (Phase 0)

Before starting any workflow, check the project's memory database for relevant past patterns:

```bash
# Derive project name from summoner.yaml
PROJECT_NAME=$(grep 'name:' summoner.yaml | head -1 | sed 's/.*name: *//')
DB_FILE="${HOME}/.claude/plugins/summoner/memory/${PROJECT_NAME}.db"

# Query for matching patterns
if [ -f "$DB_FILE" ]; then
  sqlite3 "$DB_FILE" "SELECT name, summary FROM patterns WHERE priority != 'low' LIMIT 5;"
fi
```

If memory DB doesn't exist, skip Phase 0 and proceed directly to Phase 1.

## Phase 1 — Iron Law

**Never skip diagnosis.** Root cause must be confirmed before any code changes. For `fix` and `debug` workflows, this is mandatory and cannot be skipped.

## Checkpoint Protocol

After each phase, output:
```
 SUMMONER — Phase {N}/{Total}: {phase_name}
 Completed: {what was accomplished}
 Artifacts: {files or findings}
[enter] Continue  [skip] Skip next  [done] Finish  [recall] Redo  [stop] Abort
```

Wait for user input before advancing. Never auto-continue.

## Post-Game Review

At workflow end, ask these questions:
1. Was the workflow smooth? (1-5)
2. Which phase was most useful?
3. Which phase was least useful?
4. Quality of output? (production-ready / needs fixes / wrong direction)

Write the review to the memory database for future Phase 0 retrieval.
