---
description: 运维操作 — 委托给项目 ops skill 的阶段化执行
phase_checkpoints: after_each
end_action: post_game_review
---

# /summoner:ops

Invoke the project's ops skill from `summoner.yaml` `phases.ops`.

## Workflow

```
Phase 1 ──→ 运维操作 (phase.ops)
```

## Rules

1. ops skill 内部可能有自己的阶段化执行（如 my-ops-skill 的 5 阶段）。Summoner 不干涉 skill 内部流程。
2. ops skill 执行完毕后，输出 SUMMONER CHECKPOINT。
3. 如果 ops skill 失败，输出错误信息，用户决定重试或退出。

## Post-Game Review

触发 Type 4 (流程评价)。
