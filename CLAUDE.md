# Summoner — AI Agent Orchestration Framework

Summoner is a portable, project-agnostic orchestration layer for AI coding agents. It sits between user intent and domain skills, providing structured workflows with checkpoint-based interruption.

**When entering a Summoner-enabled project:** read the project's `summoner.yaml` manifest first. This tells you which skills are available for each workflow phase. All Summoner commands (`/summoner:fix`, `/summoner:new`, etc.) read this manifest to route to the correct domain skills.

## How Summoner Works

1. User invokes a `/summoner:*` command (e.g. `/summoner:fix`) or expresses intent
2. Summoner reads the project's `summoner.yaml` manifest to discover available skills
3. Summoner executes phases in order, pausing at each checkpoint for user confirmation
4. At workflow end, Summoner triggers a post-game review questionnaire

## Key Design Principles

- **Framework verbs are fixed — project skills are replaceable.** Like a Makefile.
- **Every phase ends with a checkpoint.** The user can continue, skip, recall, or stop.
- **Phase 1 is iron law.** For fix/debug, root cause must be found before any code changes.
- **Post-game review is mandatory.** Every session produces a journal entry that feeds back into skill improvement.

## Plugin Structure

```
skills/summoner/SKILL.md    → Meta-skill: reads manifest, routes, enforces protocol
commands/                   → Slash command entry points (thin — just workflow + rules)
agents/                     → Reusable personas (code-reviewer, security-auditor, test-engineer)
references/                 → Protocol specifications (manifest, checkpoint, review, personas)
scripts/                    → Validation and utility scripts
memory/                     → Per-project SQLite databases (patterns + journal)
journal/                    → Post-game review artifacts (deprecated, moved to SQLite)
```

## Boundaries

- Never hardcode any project name or domain-specific path in the framework
- Never auto-advance past a checkpoint without user confirmation
- Never skip Phase 1 (diagnosis) in fix/debug workflows
- Project CLAUDE.md integration is the project's responsibility, not the framework's
