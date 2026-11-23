# Summoner — AI Agent Development Guidelines

## If You Are an AI Agent

This is the Summoner orchestration framework. It is a Claude Code plugin designed to be portable across projects.

## When Working on This Plugin

### Code You Touch
- `references/single-loop.md` — the loop contract + suite routing table. Each
  `commands/<name>.md` is its suite's self-sufficient entry.
- `commands/*.md` — slash command definitions. Keep them thin (50-80 lines).
- `agents/reviewer.md` — the independent ⑤ reviewer contract (frozen acceptance scope, evidence-bound verdicts). `agents/worker.md` ships on disk but unregistered, pending the evaluation gate.
- `references/*.md` — protocol specs. Reference material, not workflows.
- `scripts/*.sh` — bash utilities. Must use `#!/bin/bash` and `set -e`.

### Hard Rules
1. **Never hardcode project names.** No domain-specific project names or paths in framework code. Use `my-project`, `my-debug-skill` as illustrative placeholders in examples.
2. **Commands are thin.** They declare workflow phases and rules. The meta-skill does the heavy lifting.
3. **Review is a single contract.** `agents/reviewer.md` is the one review agent; sensitive changes add criteria, not reviewers. Adding or registering another agent is a spec-level decision, not a per-task one.
4. **References are specs, not skills.** They define contracts, not workflows.
5. **Checkpoint protocol is sacred.** Don't modify the checkpoint format without updating the spec.

### Adding a New Command
1. Create `commands/<name>.md` with frontmatter (description, phase_checkpoints, end_action)
2. Follow the template: Workflow → Rules → Auto-Skip → Rationalizations → Post-Game Review
3. Add the command to `plugin.json`
4. If the workflow uses new phases, update `references/manifest-spec.md`

### Reviewer / Worker Contracts
1. `agents/reviewer.md` frontmatter and body follow spec §3 verbatim (name, description, disallowed tools); the reviewer consumes a prepared review envelope and must bind every verdict to reconciled evidence tool_use ids
2. `agents/worker.md` is intentionally NOT registered in `plugin.json`; registration is gated on the behavior-evaluation outcome
3. Do not add ad-hoc review agents — route review needs through the reviewer contract and the criteria ladder instead

### Testing
- Validate manifest: `scripts/validate-manifest.sh <path-to-summoner.yaml>`
- Manual: invoke each `/summoner:*` command and verify checkpoint output
- Check that interrupting at each checkpoint type works correctly
