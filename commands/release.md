---
description: 版本发布全流程 — 版本规划 → changelog 生成 → 发布执行。确保 plugin.json 和 marketplace.json 版本同步。
phase_checkpoints: after_each
end_action: post_game_review
skill_implementation: skills/release/SKILL.md
---

# /summoner:release

Automated version release workflow with checkpoint-based execution.

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

## What This Command Does

**Problem it solves:** Version drift between `plugin.json` and `marketplace.json` causes incomplete releases.

**Solution:** Single automated workflow that guarantees both files stay synchronized through three checkpoint phases.

## Three-Phase Workflow

### Phase 1: Version Planning
- Detects current version from plugin.json
- Validates marketplace.json consistency (warns if mismatch)
- Determines new version (auto-increment or manual)
- Validates semver format and checks for tag conflicts
- **Checkpoint:** User confirms version before proceeding

### Phase 2: Changelog Generation  
- Analyzes commits since last git tag
- Classifies by conventional commits (feat/fix/docs/security/chore/perf/refactor/test)
- Generates markdown with emoji category headers
- **Checkpoint:** User reviews/edits changelog

### Phase 3: Release Execution
- Updates plugin.json and marketplace.json (atomic)
- Updates or creates CHANGELOG.md
- Creates git commit: `release: vX.Y.Z`
- Creates annotated git tag: `vX.Y.Z`
- Pushes to remote (unless --no-push)
- Optionally creates GitHub Release (asks if gh CLI available)
- **Checkpoint:** Shows success summary

## Safety Features

**Automatic Rollback:**
- If Phase 3 fails after commit, automatically resets
- If Phase 3 fails after tag, automatically deletes tag
- Always preserves modified files for inspection
- No rollback after successful push (manual intervention required)

**Pre-Flight Checks:**
- Verifies git repository
- Checks jq installation
- Warns about uncommitted changes (optional continue)

**Validation:**
- Semver format enforcement
- New version must be > current version
- Git tag must not already exist

## Examples

```bash
# Standard patch release
/summoner:release --patch

# Minor version bump with dry-run preview
/summoner:release --minor --dry-run

# Major version with custom number
/summoner:release --version 2.0.0

# Local-only release (no push)
/summoner:release --patch --no-push
```

## Implementation

The AI executing this command should:

1. **Read the detailed implementation:** `skills/release/SKILL.md`
2. **Follow checkpoint protocol:** Use exact format from `references/checkpoint-protocol.md`
3. **Handle user feedback:** Process feedback at checkpoints, then re-output checkpoint
4. **Execute phases sequentially:** Complete each phase fully before checkpoint
5. **Manage context:** Save to summoner-ctx after successful release (if available)

## Requirements

- Git repository
- `jq` (JSON processor) - install with `brew install jq`
- `gh` CLI (optional, for GitHub releases)

## Integration

**With /summoner:ship:**
```bash
# Recommended for major releases:
/summoner:ship         # Quality gate first
/summoner:release --minor  # Then release if GO
```

**With summoner-ctx:**
Release metadata is automatically saved to context database if summoner-ctx is available.

## Troubleshooting

**"jq not installed"**
```bash
brew install jq
```

**"Tag already exists"**
```bash
# Delete existing tag or choose different version
git tag -d v0.1.5
```

**"Version mismatch detected"**
- Warning only, uses plugin.json as source of truth
- Both files will be synchronized after release

**Push failed**
- Automatic rollback removes tag and commit
- Files preserved for inspection
- Fix authentication and retry

## Post-Game Review

After completion, triggers Type 4 (流程评价) review covering:
- Version planning clarity
- Changelog generation quality
- Checkpoint flow appropriateness
- Execution reliability
- Overall experience
