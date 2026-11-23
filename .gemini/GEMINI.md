# Summoner — Gemini CLI Integration

Summoner is an AI Agent Orchestration Framework. When loaded in Gemini CLI, it provides structured workflows with checkpoint-based interruption and post-game review.

## How Skills Load

Gemini CLI uses the `activate_skill` tool. The loop contract lives at
`references/single-loop.md`; each `commands/<name>.md` is its own suite's
entry — the agent reads `summoner.yaml` from your project and dispatches to
domain skills.

## Commands

Available via slash commands in `.gemini/commands/`:

| Command | Pipeline |
|---------|----------|
| `/summoner:fix` | context→diagnosis→reproduce→fix→regression→review |
| `/summoner:new` | context→requirements→plan→implement→verify→review |
| `/summoner:ship` | single reviewer (full rubric)→merge report→go/no-go |
| `/summoner:debug` | context→diagnosis (no code changes) |
| `/summoner:ops` | ops skill (delegated) |
| `/summoner:review` | code review standalone |
| `/summoner:release` | version-plan→changelog→release-execution |
| `/summoner:status` | read-only run status view |

Command bodies in `.gemini/commands/*.toml` are POINTERS to the canonical
`references/single-loop.md` + `commands/<name>.md` — never a paraphrase.

## Setup

1. Add `summoner.yaml` to your project root
2. Run `scripts/init-memory-db.sh <project-name>` (optional)
3. Use `/summoner:fix` or any Summoner command
