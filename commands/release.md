---
description: 版本发布全流程 — 版本规划 → changelog 生成 → 发布执行。三份版本清单同步（plugin.json / .claude-plugin/plugin.json / marketplace.json）。
phase_checkpoints: after_each
end_action: post_game_review
---

# /summoner:release

Framework-internal, self-contained workflow（不经项目 `summoner.yaml` 路由）：三 Phase —— 版本规划 → changelog 生成 → 发布执行。

**Full implementation lives in `skills/summoner-release/SKILL.md`.** The AI executing this command should:

1. **Delegate to the skill:** invoke the `summoner-release` skill for all three phases (options: `--major/--minor/--patch/--version X.Y.Z/--dry-run/--no-push/--skip-changelog`).
2. **Follow checkpoint protocol:** exact format from `references/checkpoint-protocol.md` — PHASE START on entry, CHECKPOINT at each phase end; never auto-advance.
3. **Handle user feedback:** content feedback first, then re-output the CHECKPOINT block.

**Safety:** 自动回滚（失败删 tag / reset commit）；pre-flight（git repo、`jq`、未提交变更警告）；校验（semver、new > current、tag 无冲突）。

**Requirements:** git 仓库、`jq`（`brew install jq`）、`gh` CLI（可选）。

**Integration:** 大版本建议先 `/summoner:ship` 质量门禁，GO 后再 `/summoner:release --minor`。

## Post-Game Review

Type 4 (流程评价)：版本规划清晰度、changelog 质量、checkpoint 流程、执行可靠性。
