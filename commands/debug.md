---
description: 仅诊断 — 根因分析，不出手修复。快速、轻量、单 Phase。
phase_checkpoints: after_each
end_action: post_game_review
---

# /summoner:debug

Invoke the project's debug skill for diagnosis only. No code changes. Can also dispatch `summoner:debug-agent` persona for standalone root cause analysis.

## Phase 0.5 — 基础设施 vs 业务码分流（Phase 1 前先判）

诊断前先判症状归属，路由到不同 skill——基础设施问题不是业务码 bug，走 `phase.debug` 会查错地方：

| 症状关键词 | 归属 | 路由到 |
|-----------|------|--------|
| `connection refused` / `bind: address already in use` / `dial tcp` / `127.0.0.1:6379` / `127.0.0.1:3306` | 基础设施 | `phase.diagnose` = antia-diagnose（Docker MySQL / Redis / 端口冲突 / 启动失败） |
| 启动失败 / 进程起不来 / `panic` in init / `dbproxy` timeout / `i/o timeout` | 基础设施 | `phase.diagnose` |
| `make conf` 后 `dbstate` 数据误删 / 生成产物异常 | 基础设施（运维产物） | `phase.diagnose`（→ antia-ops make-conf 路径） |
| `SC_Err*` / `nil pointer` in 业务逻辑 / `index out of range` / 运行时 panic in dealer/subsys | 业务码 | `phase.debug` = antia-debug |
| 配置表缺失 / `SC_NotFoundInConf` / 字段不匹配 | 配置 | `phase.config` = antia-config |
| 混合（业务码报错根因是配置/infra） | 以根因 | 先 phase.debug 定位，按根因转 config/diagnose |

分流后默认仍走 `phase.debug`，但若判定为基础设施/配置，先向用户说明并转 `phase.diagnose` / `phase.config`，不在 antia-debug 里硬查 infra 问题。

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
