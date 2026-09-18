---
description: 仅诊断 — 根因分析，不出手修复。快速、轻量、单 Phase。仅当用户明确只要原因、不要改码（'先别改代码'/'只告诉我原因'/'我自己来修'）时使用；未说明的「排查」类短语默认走 /summoner:fix。
phase_checkpoints: after_each
end_action: post_game_review
---

# /summoner:debug

Invoke the project's debug skill for diagnosis only. No code changes. Can also dispatch `summoner:debug-agent` persona for standalone root cause analysis.

## Phase 0.5 — 基础设施 vs 业务码分流（Phase 1 前先判）

诊断前先判症状归属再路由（infra 问题走 `phase.debug` 会查错地方）：

| 症状关键词 | 归属 | 路由到 |
|-----------|------|--------|
| `connection refused` / `bind: address already in use` / `dial tcp` / 数据库/缓存端口不可达 | 基础设施 | `phase.diagnose`（容器 / 中间件 / 端口冲突 / 启动失败） |
| 启动失败 / 进程起不来 / `panic` in init / 依赖服务 timeout / `i/o timeout` | 基础设施 | `phase.diagnose` |
| 配置/代码生成产物异常（`make` 后产物误删或错乱等） | 基础设施（运维产物） | `phase.diagnose`（或项目 ops 路径，按 manifest） |
| 业务错误码 / `nil pointer` in 业务逻辑 / `index out of range` / 运行时 panic in 业务模块 | 业务码 | `phase.debug` |
| 配置表缺失 / 配置项找不到 / 字段不匹配 | 配置 | `phase.config` |
| 混合（业务码报错根因是配置/infra） | 以根因 | 先 phase.debug 定位，按根因转 config/diagnose |

路由到的 `phase.*` 一律从 `summoner.yaml` `phases` 解析（未声明 → No-Manifest 流程）。默认仍走 `phase.debug`；判定为 infra/配置时先说明再转 `phase.diagnose` / `phase.config`。

## Workflow

```
Phase 0 ──→ 记忆检索 (Memory Retrieval, automatic)
Phase 0.5 ──→ 基础设施 vs 业务码分流（判定路由，见上表）
Phase 1 ──→ 诊断根因 (phase.debug，或按分流转 phase.diagnose / phase.config)
           → 输出根因分析报告
           → 如涉及配置问题，触发 phase.config
           → 结束（不进入修复）
```

## Rules

1. 每个 Phase 开始输出 **PHASE START** 块 + 结束输出 **SUMMONER CHECKPOINT** 块（格式与字段规约见 `references/checkpoint-protocol.md`），等待用户选择。内容反馈先处理再重问。
2. Phase 1 结束时输出完整诊断报告：根因、影响范围、修复建议。
3. 不写任何代码。不创建任何测试文件。
4. 诊断报告 = 给用户的输入，用户可以拿着它自己修，或手动 `/summoner:fix`。
5. 如果诊断过程中发现需要配置检查，自动触发 `phases.debug.triggers` 中声明的 phase（如 config）。

## Rationalizations

| AI 会想… | 现实 |
|----------|------|
| "问题很简单，我顺手修了吧" | `/summoner:debug` 的合约是只诊断。想修用 `/summoner:fix`。 |

## Post-Game Review

触发 Type 4 (流程评价)。
