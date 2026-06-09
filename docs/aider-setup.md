# Summoner — Aider Integration

## Installation

1. Clone Summoner:
   ```bash
   git clone https://github.com/johnson-xue/summoner.git ~/.claude/plugins/summoner/
   ```

2. Add Summoner instructions to Aider's conventions file (`.aider.conf.yml` or `CONVENTIONS.md`):
   ```yaml
   # .aider.conf.yml
   read: [~/.claude/plugins/summoner/summoner.md]
   ```

   Or in `CONVENTIONS.md`:
   ```markdown
   ## Summoner Workflow
   
   For structured fixes: diagnose first (Phase 1 Iron Law), checkpoint after each phase, post-game review at end.
   Full workflow: ~/.claude/plugins/summoner/summoner.md
   ```

3. Aider reads these files on startup.

## Usage

Aider sessions will reference Summoner conventions from the loaded files.
