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

1. 每个 Phase 开始输出 **PHASE START** 块 + 结束输出 **SUMMONER CHECKPOINT** 块（格式与字段规约见 `references/checkpoint-protocol.md`），等待用户选择。内容反馈先处理再重问。
2. ops skill 内部可能有自己的阶段化执行（如 my-ops-skill 的 5 阶段）。Summoner 不干涉 skill 内部流程。
3. ops skill 执行完毕后，输出 SUMMONER CHECKPOINT。
4. 如果 ops skill 失败，输出错误信息，用户决定重试或退出。

## Post-Game Review

触发 Type 4 (流程评价)。
