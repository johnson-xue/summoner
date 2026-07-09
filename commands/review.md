---
description: 独立代码审查 — 使用 code-reviewer persona，不改代码
phase_checkpoints: after_merge
end_action: post_game_review
---

# /summoner:review

Standalone code review using the code-reviewer persona. No other phases.

## Workflow

```
Phase 1 ──→ 代码审查 (code-reviewer persona)
           → 输出审查报告
           → 结束
```

## Rules

1. 每个 Phase 开始输出 **PHASE START** 块 + 结束输出 **SUMMONER CHECKPOINT** 块（格式与字段规约见 `references/checkpoint-protocol.md`），等待用户选择。内容反馈先处理再重问。
2. 只审查，不修改代码。
3. 使用 `agents/code-reviewer.md` 的 5-axis 审查框架。
4. 输出按 Critical / Important / Suggestion 分级。

## Auto-Skip

如果审查范围内没有 diff（空提交或只有 merge commit）→ 报告 "Nothing to review" 并退出。

## Post-Game Review

触发 Type 4 (流程评价)。
