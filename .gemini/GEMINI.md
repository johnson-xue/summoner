# Summoner — Gemini CLI Integration

Summoner is an AI Agent Orchestration Framework. When loaded in Gemini CLI, it provides structured workflows with checkpoint-based interruption and post-game review.

## How Skills Load

Gemini CLI uses the `activate_skill` tool. Summoner's meta-skill at `skills/summoner/SKILL.md` is the routing hub — it reads `summoner.yaml` from your project and dispatches to domain skills.

## Commands

Available via slash commands in `.gemini/commands/`:

| Command | Pipeline |
|---------|----------|
| `/summoner:fix` | diagnose→reproduce→fix→verify→review |
| `/summoner:new` | define→plan→implement→test→review |
| `/summoner:ship` | fan-out(1-3 personas)→merge→decision |
| `/summoner:debug` | diagnose only |
| `/summoner:ops` | ops skill (delegated) |
| `/summoner:review` | code review standalone |

## Setup

1. Add `summoner.yaml` to your project root
2. Run `scripts/init-memory-db.sh <project-name>` (optional)
3. Use `/summoner:fix` or any Summoner command
