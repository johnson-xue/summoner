# Summoner — GitHub Copilot Integration

## Installation

1. Clone Summoner:
   ```bash
   git clone https://github.com/johnson-xue/summoner.git ~/.claude/plugins/summoner/
   ```

2. Add to `.github/copilot-instructions.md` in your project:
   ```markdown
   ## Summoner Workflow Framework
   
   For structured bug fixes and feature development, follow Summoner.
   See ~/.claude/plugins/summoner/summoner.md for the complete workflow.
   
   Core rules:
   - Phase 1 Iron Law: diagnose before coding
   - Checkpoint Protocol: pause after each phase
   - Post-Game Review: complete at workflow end
   ```

3. Copilot auto-reads `.github/copilot-instructions.md` when it exists.

## Usage

Use natural language: "Using the Summoner workflow, diagnose and fix this bug."
