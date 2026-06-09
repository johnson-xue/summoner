# Contributing to Summoner

Thanks for your interest in contributing! Summoner is a Claude Code plugin that provides structured AI agent workflows.

## Getting Started

1. Fork the repository
2. Clone your fork: `git clone https://github.com/YOUR_USERNAME/summoner.git`
3. Install the plugin locally for testing:
   ```bash
   cp -r summoner ~/.claude/plugins/summoner/
   cd ~/.claude/plugins/summoner/hooks && make build
   ```

## Development

### Project Structure
```
hooks/       Go lifecycle hooks (SessionStart, PreToolUse, Stop)
skills/      Meta-skill routing hub (SKILL.md)
commands/    Slash command definitions (6 entries)
agents/      Reusable persona definitions
references/  Protocol specifications + JSON Schema
scripts/     Shell utilities (Bash)
```

### Building Hooks
```bash
cd hooks && make build    # Compile Go binaries to hooks/bin/
make clean                # Remove compiled binaries
```

### Validating Changes
```bash
# Validate shell scripts
bash -n scripts/validate-manifest.sh scripts/summoner-init.sh scripts/init-memory-db.sh

# Build and test Go hooks
cd hooks && go build ./... && make build

# Validate JSON Schema
python3 -c "import json; json.load(open('references/summoner.schema.json')); print('OK')"
```

## Pull Request Process

1. **One change per PR.** Don't bundle unrelated changes.
2. **Update CHANGELOG.md** if your change affects user-facing behavior.
3. **Test your changes.** At minimum: build hooks, validate schema, run bash syntax checks.
4. **Follow existing patterns.** Markdown files follow the SKILL.md convention. Go code follows standard Go style.
5. **Commit messages** use the format: `type(scope): description` (e.g., `feat(hooks): add PreToolUse state tracking`).

### PR Checklist
- [ ] Hooks compile: `cd hooks && make build`
- [ ] Shell scripts pass syntax check: `bash -n scripts/*.sh`
- [ ] JSON Schema is valid
- [ ] No hardcoded project names in framework code
- [ ] CHANGELOG.md updated (if user-facing)
- [ ] README updated (if behavior changes)

## Reporting Bugs

Use the Bug Report issue template. Include:
- Your environment (OS, Claude Code version)
- Steps to reproduce
- Expected vs actual behavior
- Any error messages or logs

## Feature Requests

Use the Feature Request issue template. Describe:
- The problem you're trying to solve
- Your proposed solution (if any)
- Alternatives you've considered

## Code of Conduct

This project follows the [Contributor Covenant](https://www.contributor-covenant.org/version/2/1/code_of_conduct/). Report issues to the repository maintainer.

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
