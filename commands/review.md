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

1. 只审查，不修改代码。
2. 使用 `agents/code-reviewer.md` 的 5-axis 审查框架。
3. 输出按 Critical / Important / Suggestion 分级。

## Auto-Skip

如果审查范围内没有 diff（空提交或只有 merge commit）→ 报告 "Nothing to review" 并退出。

## Post-Game Review

触发 Type 4 (流程评价)。
