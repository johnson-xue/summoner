# Summoner — AI Agent Development Guidelines

## If You Are an AI Agent

This is the Summoner orchestration framework. It is a Claude Code plugin designed to be portable across projects.

## When Working on This Plugin

### Code You Touch
- `skills/summoner/SKILL.md` — the meta-skill. This is the routing brain.
- `commands/*.md` — slash command definitions. Keep them thin (50-80 lines).
- `agents/*.md` — persona definitions. Single role, single output format.
- `references/*.md` — protocol specs. Reference material, not workflows.
- `scripts/*.sh` — bash utilities. Must use `#!/bin/bash` and `set -e`.

### Hard Rules
1. **Never hardcode project names.** No domain-specific project names or paths in framework code. Use `my-project`, `my-debug-skill` as illustrative placeholders in examples.
2. **Commands are thin.** They declare workflow phases and rules. The meta-skill does the heavy lifting.
3. **Personas are single-role.** Don't add a second role to an existing persona. Create a new one.
4. **References are specs, not skills.** They define contracts, not workflows.
5. **Checkpoint protocol is sacred.** Don't modify the checkpoint format without updating the spec.

### Adding a New Command
1. Create `commands/<name>.md` with frontmatter (description, phase_checkpoints, end_action)
2. Follow the template: Workflow → Rules → Auto-Skip → Rationalizations → Post-Game Review
3. Add the command to `plugin.json`
4. If the workflow uses new phases, update `references/manifest-spec.md`

### Adding a New Persona
1. Create `agents/<role>.md` with name + description in frontmatter
2. Define: Role, Scope, Review Dimensions, Output Format, Composition rules
3. Add to `plugin.json` agents array
4. Persona must NOT call another persona
5. Persona MAY invoke skills

### Testing
- Validate manifest: `scripts/validate-manifest.sh <path-to-summoner.yaml>`
- Manual: invoke each `/summoner:*` command and verify checkpoint output
- Check that interrupting at each checkpoint type works correctly
