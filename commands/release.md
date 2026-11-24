---
description: 版本发布全流程 — planning → changelog → publish，三份版本清单同步（plugin.json / .claude-plugin/plugin.json / marketplace.json）；publish 是变更阶段，必须先声明只读对账探针（tag-已存在/版本-已一致）。「发版」「publish 0.x.y」类意图路由到这里。
allowed-tools:
  - Skill
  - Read
  - Edit
  - Bash
  - AskUserQuestion
---

# /summoner:release

Version release on the framework route **planning → changelog → publish**
(fixed in `hooks/shared/workflow.go`; this command describes its own route
only — the manifest maps capabilities, never routes). User gates, trace,
budgets and post-game run through the SAME single-loop reducer as every
workflow: phase closes → checkpoint render → AskUserQuestion decision
(`${CLAUDE_PLUGIN_ROOT}/references/checkpoint-protocol.md`); never auto-advance. Red flags /
auto-skip table: `${CLAUDE_PLUGIN_ROOT}/references/workflow-reference.md`.

## Capability

`phases.release` in `summoner.yaml` may bind a project provider to replace
the default framework implementation (`${CLAUDE_PLUGIN_ROOT}/skills/summoner-release/SKILL.md` —
version planning, changelog from conventional commits, publish with rollback;
options `--major/--minor/--patch/--version X.Y.Z/--dry-run/--no-push/
--skip-changelog/--gh-release`). No manifest → the default, disclosed「未配置项目专属能力」.

## Rules

1. Loop per `${CLAUDE_PLUGIN_ROOT}/references/single-loop.md`: work → `summoner:phase-submit`
   with a conclusion (the only submission channel) → paste the checkpoint and end
   the turn → the Stop hook hands back the question → AskUserQuestion. An auto-passed
   phase shows no checkpoint: go straight on.
   Hooks dormant (no `SUMMONER_HOOKS_ENFORCE=1`)? Replace this loop with
   plain AskUserQuestion checkpoints — single-loop.md §2 (0.7.1).
2. **对账探针是 publish 的准入条件** — publish mutates (version writes,
   commit, tag, push), so the capability list must declare the read-only
   reconciliation probe set (tag-已存在 / 版本-已一致) BEFORE the release
   enters it: 无探针声明不得进变更阶段. Probes are read-only 对账 resolved
   only via `summoner:probe-submit`; 不一致/未生效 route to the recovery
   checkpoint (CONTINUE/RECALL/STOP) — never a mutating fix, never silently
   passed (`${CLAUDE_PLUGIN_ROOT}/skills/summoner-release/SKILL.md` §只读对账探针; fixtures
   `${CLAUDE_PLUGIN_ROOT}/tests/fixtures/traces/valid-release-probe-*.jsonl`).
3. Safety lives in the release skill and its scripts: auto rollback (delete
   tag / reset commit), pre-flight (git repo, `jq`, uncommitted-change
   notice), validation (semver, new > current, tag-conflict).

## Requirements

git 仓库、`jq`（`brew install jq`）、`gh` CLI（可选）。大版本建议先
`/summoner:ship` 质量门禁，GO 后再 `/summoner:release --minor`。

## Post-Game Review

Mandatory — Type 4 (流程评价) per `${CLAUDE_PLUGIN_ROOT}/references/post-game-review.md`: `done`
lands POST_GAME_PENDING; CLOSED only after the `[pg:q1..q4]` questionnaire +
`.summoner/journal/<run_id>.md` persist (CompletePostGame is the sole CLOSED
writer). No release exemption.
