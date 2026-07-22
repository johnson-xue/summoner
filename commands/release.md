---
description: 版本发布全流程 — 版本规划 → changelog 生成 → 发布执行。确保 plugin.json 和 marketplace.json 版本同步。
phase_checkpoints: after_each
end_action: post_game_review
---

# /summoner:release

Version release workflow with checkpoint-based execution. Three phases — version planning, changelog generation, release execution — that guarantee `plugin.json` and `marketplace.json` stay synchronized, plus an automated changelog and git tag.

This is a **framework-internal, self-contained workflow** — it does not route through the project's `summoner.yaml` manifest. The meta-skill delegates directly to the `summoner-release` skill for implementation.

## Quick Reference

```bash
/summoner:release [OPTIONS]

OPTIONS:
  --major              Increment major version (X.0.0)
  --minor              Increment minor version (0.X.0)
  --patch              Increment patch version (0.0.X)
  --version X.Y.Z      Specify exact version number
  --dry-run            Preview mode, no git operations
  --no-push            Local only (no remote push)
  --skip-changelog     Skip changelog generation
```

## Workflow

| Phase | Task | Checkpoint |
|-------|------|------------|
| 1/3 Version Planning | Detect current version, validate consistency, determine + validate new version | User confirms version |
| 2/3 Changelog Generation | Analyze commits since last tag, classify by conventional-commit type | User reviews/edits changelog |
| 3/3 Release Execution | Update manifests + CHANGELOG, commit + tag, push, optional GitHub Release | Success summary |

**Safety:** automatic rollback (delete tag / reset commit on failure); pre-flight checks (git repo, `jq`, uncommitted-change warning); validation (semver, new > current, no tag conflict).

## Implementation

Full implementation lives in `skills/release/SKILL.md`. The AI executing this command should:

1. **Delegate to the skill:** invoke the `summoner-release` skill for all three phases.
2. **Follow checkpoint protocol:** use the exact format from `references/checkpoint-protocol.md` — output a PHASE START block on entry and a CHECKPOINT block at each phase end; never auto-advance.
3. **Handle user feedback:** if a checkpoint reply is content feedback (not a pure flow decision), handle it first, then re-output the CHECKPOINT block.

## Requirements

- Git repository
- `jq` (JSON processor) — install with `brew install jq`
- `gh` CLI (optional, for GitHub releases)

## Integration

With `/summoner:ship` (recommended for major releases — quality gate first, then release if GO):
```bash
/summoner:ship
/summoner:release --minor
```

## Post-Game Review

After completion, triggers Type 4 (流程评价) review covering version planning clarity, changelog quality, checkpoint flow, and execution reliability.
