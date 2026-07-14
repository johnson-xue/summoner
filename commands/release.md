---
description: 版本发布全流程 — 版本规划 → changelog 生成 → 发布执行。确保 plugin.json 和 marketplace.json 版本同步。
phase_checkpoints: after_each
end_action: post_game_review
---

# /summoner:release

Automated version release workflow. Ensures zero version drift between plugin.json and marketplace.json.

## Usage

```bash
/summoner:release [--major|--minor|--patch] [--version X.Y.Z] [--dry-run] [--no-push]
```

## Workflow Description

This command implements a three-phase checkpoint workflow:

**Phase 1: Version Planning**
- Read current version from `.claude-plugin/plugin.json`
- Validate consistency with `.claude-plugin/marketplace.json`
- Determine new version (auto-increment or user-specified)
- Verify git tag doesn't already exist

**Phase 2: Changelog Generation**
- Analyze commits since last git tag
- Classify by conventional commits (feat/fix/docs/security/chore)
- Generate markdown changelog with emoji categories

**Phase 3: Release Execution**
- Update both `plugin.json` and `marketplace.json`
- Update or create `CHANGELOG.md`
- Create git commit and annotated tag
- Push to remote (unless `--no-push`)
- Optionally create GitHub Release (if `gh` CLI available)

## Implementation

Execute the following workflow when invoked:

### Pre-Flight Checks

```bash
# Verify git repository
if ! git rev-parse --git-dir > /dev/null 2>&1; then
    echo "❌ Error: Not a git repository"
    exit 1
fi

# Check for jq
if ! command -v jq &> /dev/null; then
    echo "❌ Error: jq not installed. Install with: brew install jq"
    exit 1
fi

# Parse arguments
INCREMENT_MAJOR=false
INCREMENT_MINOR=false  
INCREMENT_PATCH=false
VERSION_ARG=""
DRY_RUN=false
NO_PUSH=false
SKIP_CHANGELOG=false

# [Parse command-line args here]
```

### Phase 1: Version Planning

Output PHASE START block:
```
⚡ SUMMONER START — Workflow=release Phase 1/3: Version Planning
🎯 任务: 确定新版本号并验证版本文件一致性
🔧 Skill: none (版本管理逻辑)
```

1. Read current version from `.claude-plugin/plugin.json`
2. Check `.claude-plugin/marketplace.json` consistency  
3. Determine new version based on args or interactive prompt
4. Validate semver format and that new > current
5. Check git tag doesn't exist

Output CHECKPOINT block and wait for user action.

### Phase 2: Changelog Generation

Output PHASE START block:
```
⚡ SUMMONER START — Workflow=release Phase 2/3: Changelog Generation
🎯 任务: 从 git commits 生成 changelog 并分类
🔧 Skill: none (changelog 生成逻辑)
```

1. Get commit range (last tag..HEAD)
2. Classify commits by prefix (feat:/fix:/docs:/security:/chore:)
3. Format as markdown with emoji section headers
4. Show preview

Output CHECKPOINT block and wait for user action.

### Phase 3: Release Execution

Output PHASE START block:
```
⚡ SUMMONER START — Workflow=release Phase 3/3: Release Execution
🎯 任务: 更新版本文件、CHANGELOG 并执行 git 操作
🔧 Skill: none (文件更新和 git 操作)
```

1. Setup error trap for rollback
2. Update `plugin.json` version using jq
3. Update `marketplace.json` plugins[0].version using jq
4. Update/create `CHANGELOG.md`
5. Git add, commit, tag
6. Push (unless --no-push or --dry-run)
7. Ask about GitHub Release (if gh available)

Output CHECKPOINT block with success summary.

### Rollback Strategy

On error in Phase 3:
- Delete local tag if created: `git tag -d vX.Y.Z`
- Reset commit if created: `git reset HEAD~1`
- Preserve modified files for inspection

### Post-Game Review

Trigger Type 4 (流程评价) review covering:
- Version planning phase clarity
- Changelog generation quality
- Checkpoint flow appropriateness
- Execution reliability
- Overall experience

## Notes for AI Implementation

When implementing this command:

1. **Follow checkpoint protocol exactly** - Use format from `references/checkpoint-protocol.md`
2. **Handle user feedback** - If user provides content feedback at checkpoint, process it and re-output checkpoint
3. **Preserve JSON formatting** - Use `jq` with proper indentation
4. **Include Co-Authored-By** - Add to commit message
5. **Error handling** - Trap errors and rollback git operations
6. **Test first** - In --dry-run mode, show what would be done

This is a coordination-heavy workflow. The AI should:
- Read and understand the full spec before starting
- Execute each phase completely before checkpoints
- Handle interactive prompts for version selection
- Manage git operations carefully with rollback
- Provide clear status updates throughout
