---
description: 新增功能全链路 — 需求定义 → 任务拆解 → 实现 → 测试 → 审查
phase_checkpoints: after_each
end_action: post_game_review
---

# /summoner:new

Invoke define → plan → implement → test → review phases from `summoner.yaml`. Phase 3 routes to subsystem / rpc / gmt / migrate skill based on function type.

## Workflow

```
Phase 0 ──→ 记忆检索 (Memory Retrieval, automatic)
Phase 1 ──→ 需求定义   (phase.define, brainstorming)
Phase 2 ──→ 任务拆解   (phase.plan, writing-plans)
Phase 3 ──→ 实现       (phase.subsystem | phase.rpc | phase.gmt | phase.migrate)
Phase 4 ──→ 测试       (phase.test)
Phase 5 ──→ 审查       (phase.review)
```

## Rules

1. 每个 Phase 开始输出 **PHASE START** 块 + 结束输出 **SUMMONER CHECKPOINT** 块（格式与字段规约见 `references/checkpoint-protocol.md`），等待用户选择。
2. 用户回复若是内容反馈（方案不对/漏了边界/方向有问题），先处理反馈再重新输出 CHECKPOINT——勿当 CONTINUE 推进。
3. Phase 1-2 不可跳过（没 spec 不动工，没 plan 不写码）。
4. **Phase 3 按功能类型路由**（四选一）：
   - 全新子系统（玩家特性/玩法系统，需注册枚举+建目录+SubSystem 接口）→ `phase.subsystem` = antia-subsystem
   - 既有子系统加对外接口（proto + handler + 业务方法联动）→ `phase.rpc` = antia-rpc
   - GMT 后台 HTTP 接口（直接操作 DB / 踢玩家下线）→ `phase.gmt` = antia-gmt
   - 新增 DB 表 / 改 XDB XML schema / 迁移（数据层先行，无对外接口）→ `phase.migrate` = antia-migrate
   - 多类型混合（如新子系统含新表）→ 取主类型，次要类型作为该 skill 内的子步（如 antia-subsystem 内可选调 antia-migrate）
   - 判定不清 → 停下问用户，不在轨道上硬猜
5. 如果项目没有 summoner.yaml → 触发 No Manifest 菜单（见 SKILL.md §1）。
6. 用户可在 Phase 1 后选择 "方向不对" → 回到 brainstorming 重新定义。

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
