# Summoner — Windsurf (Codeium) Integration

## Installation

1. Clone Summoner:
   ```bash
   git clone https://github.com/johnson-xue/summoner.git ~/.claude/plugins/summoner/
   ```

2. Create `.windsurfrules` in your project root:
   ```markdown
   Use the Summoner workflow framework for structured bug fixes and features.
   See ~/.claude/plugins/summoner/summoner.md for the complete workflow.
   
   Key rules:
   - Phase 1 (diagnosis) is iron law — never skip
   - Pause at each checkpoint for user confirmation
   - Complete post-game review at workflow end
   ```

3. Build hooks (Claude Code users only):
   ```bash
   cd ~/.claude/plugins/summoner/hooks && make build
   ```
