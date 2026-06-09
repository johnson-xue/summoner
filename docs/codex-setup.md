# Summoner — Codex (OpenAI) Integration

Codex CLI doesn't have a plugin system like Claude Code. Summoner integrates via custom instructions.

## Installation

1. Copy Summoner's SKILL.md content into your Codex instructions:
   ```bash
   mkdir -p .codex
   cp ~/.claude/plugins/summoner/skills/summoner/SKILL.md .codex/summoner-instructions.md
   ```

2. Create a `.codex/instructions.md` that references Summoner:
   ```markdown
   ## Summoner Workflow Framework
   
   This project uses the Summoner AI Agent Orchestration Framework.
   Read `.codex/summoner-instructions.md` for the full workflow definition.
   
   Key rules:
   - Before fixing any bug, diagnose the root cause first (Phase 1 Iron Law)
   - After each phase, pause for user confirmation (Checkpoint Protocol)
   - At workflow end, complete a post-game review
   ```

3. Add `summoner.yaml` to your project root.

## Usage

Codex doesn't support slash commands. Use natural language prompts:
- "Diagnose this bug using Summoner workflow"
- "Review this change with the Summoner ship workflow"

## Limitations

- No lifecycle hooks (PreToolUse/Stop not available)
- No checkpoint protocol enforcement (markdown instructions only)
- No auto-routing (user must explicitly mention Summoner)
- Memory Chain requires manual SQLite queries
