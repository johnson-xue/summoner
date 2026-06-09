# Summoner — Cursor Integration

## Installation

1. Clone Summoner:
   ```bash
   git clone https://github.com/johnson-xue/summoner.git ~/.claude/plugins/summoner/
   ```

2. Add to Cursor rules. Create `.cursor/rules/summoner.md`:
   ```markdown
   # Summoner Workflow Framework
   
   When fixing bugs or adding features, follow the Summoner workflow.
   Read ~/.claude/plugins/summoner/summoner.md for full instructions.
   
   Key rules:
   - Always diagnose root cause before writing code (Phase 1 Iron Law)
   - Pause after each phase for user confirmation
   - Complete post-game review at workflow end
   ```

3. Add `summoner.yaml` to your project root.

## Usage

Cursor auto-loads `.cursor/rules/`. Say "Follow the Summoner workflow to fix this bug" to trigger.
