---
description: 新增功能全链路 — 需求定义 → 任务拆解 → 实现 → 测试 → 审查
phase_checkpoints: after_each
end_action: post_game_review
---

# /summoner:new

Invoke define → plan → implement → test → review phases from `summoner.yaml`. Phase 3 routes to subsystem / rpc / gmt skill based on function type.

## Workflow

```
Phase 0 ──→ 记忆检索 (Memory Retrieval, automatic)
Phase 1 ──→ 需求定义   (phase.define, brainstorming)
Phase 2 ──→ 任务拆解   (phase.plan, writing-plans)
Phase 3 ──→ 实现       (phase.subsystem or phase.rpc or phase.gmt)
Phase 4 ──→ 测试       (phase.test)
Phase 5 ──→ 审查       (phase.review)
```

## Rules

1. 每个 Phase 开始输出 **PHASE START** 块 + 结束输出 **SUMMONER CHECKPOINT** 块（格式与字段规约见 `references/checkpoint-protocol.md`），等待用户选择。
2. 用户回复若是内容反馈（方案不对/漏了边界/方向有问题），先处理反馈再重新输出 CHECKPOINT——勿当 CONTINUE 推进。
3. Phase 1-2 不可跳过（没 spec 不动工，没 plan 不写码）。
4. Phase 3 根据功能类型选择对应 skill（subsystem / rpc / gmt）。如果项目没有 summoner.yaml → 触发 No Manifest 菜单（见 SKILL.md §1）。
5. 用户可在 Phase 1 后选择 "方向不对" → 回到 brainstorming 重新定义。

## Auto-Skip Conditions

Phase 5 (审查):
- 单文件 < 100 行且无新增 public API
- 用户说"不用 review"

## Rationalizations

| AI 会想… | 现实 |
|----------|------|
| "用户描述得很清楚了，直接写码就行" | 没 spec = 假设当需求。Phase 1 必须出书面设计。 |
| "这个功能简单，plan 和 spec 合并为一步就行" | Spec ≠ Plan。Spec 定义 what，Plan 定义 how。跳过 Plan = 实现顺序靠猜。 |

## Post-Game Review

流程结束后触发复盘问卷。类型规则同 `/summoner:fix`。
