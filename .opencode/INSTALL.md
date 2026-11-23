# Summoner — OpenCode Integration

## Installation

1. Copy the Summoner surface files to your project:
   ```bash
   mkdir -p .claude/summoner
   cp -r commands references skills .claude/summoner/
   ```

2. Ensure `AGENTS.md` is in your project root. Summoner's AGENTS.md provides intent→skill routing rules compatible with OpenCode's skill-driven execution model.

3. Add `summoner.yaml` to your project root declaring your skill mappings.

4. Initialize memory (optional):
   ```bash
   scripts/init-memory-db.sh <project-name>
   ```

## How It Works

OpenCode uses the `skill` tool to invoke skills. Summoner's `AGENTS.md` tells the agent to:
- Detect when a Summoner workflow applies (bug fix → fix suite)
- Read the loop contract (`.claude/summoner/references/single-loop.md`) and the suite's command (`.claude/summoner/commands/<name>.md`)
- Follow them exactly

## Usage

Use natural language — the agent auto-routes:
- "Fix this bug" → fix suite
- "Add a new feature" → new suite
- "Review before launch" → ship suite

## Limitations

- No native slash commands (OpenCode uses intent mapping via AGENTS.md)
- No Go hooks (OpenCode doesn't support Claude Code-style lifecycle hooks)
- State tracking and post-game review reminders rely on markdown instructions only
