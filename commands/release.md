---
description: 版本发布全流程 — 版本规划 → changelog 生成 → 发布执行。确保 plugin.json 和 marketplace.json 版本同步。
phase_checkpoints: after_each
end_action: post_game_review
---

# /summoner:release

Automated version release workflow with three checkpoint phases. Ensures zero version drift between plugin.json and marketplace.json.

## Command Signature

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

Phase 1: Version Planning → Phase 2: Changelog Generation → Phase 3: Release Execution

## Rules

1. Each phase: output **PHASE START** block at entry + **SUMMONER CHECKPOINT** block at end (format + field spec in `references/checkpoint-protocol.md`). Wait for user input.
2. Checkpoint options: continue / skip / done / recall / stop. If user reply is content feedback, handle it first then re-output CHECKPOINT.
3. All file updates must preserve formatting (2-space JSON indentation).
4. Git operations use error trapping for automatic rollback.
5. Phase 3 asks about GitHub Release creation if `gh` CLI is available.

## Post-Game Review

Mandatory Type 4 (流程评价) review at workflow end.
