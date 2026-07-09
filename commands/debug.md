---
description: 仅诊断 — 根因分析，不出手修复。快速、轻量、单 Phase。
phase_checkpoints: after_each
end_action: post_game_review
---

# /summoner:debug

Invoke the project's debug skill for diagnosis only. No code changes. Can also dispatch `summoner:debug-agent` persona for standalone root cause analysis.

## Workflow

```
Phase 0 ──→ 记忆检索 (Memory Retrieval, automatic)
Phase 1 ──→ 诊断根因 (phase.debug)
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
