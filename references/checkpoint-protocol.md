# Summoner Checkpoint Protocol

## Purpose

Checkpoints are the core interruption mechanism in Summoner. After each phase completes, the framework pauses and presents a structured status block. The user chooses how to proceed.

## Checkpoint Output Format

Every phase end MUST output this exact format:

```
┌──────────────────────────────────────────────┐
│  ⚡ SUMMONER — Phase {N}/{Total}: {phase名}   │
│                                              │
│  ✅ 完成内容: {what this phase produced}       │
│  📋 产物: {files / solutions / test results}  │
│  ⚠️ 发现: {issues worth noting}               │
│                                              │
│  Next:                                       │
│  [enter] 继续下一 Phase                       │
│  [skip]  跳过下一 Phase                       │
│  [done]  完成，退出框架                       │
│  [recall] B 键回城 — 回到之前 Phase 重新来过   │
│  [stop]  紧急停止 — 保留产物，立即退出          │
└──────────────────────────────────────────────┘
```

## Field Requirements

- **完成内容**: 1 paragraph max. What was accomplished.
- **产物**: Comma-separated list of concrete artifacts (file paths, code snippets, decision records). Never empty — if nothing was produced, state "No artifacts — analysis only."
- **发现**: Issues, risks, or open questions. "None" if clean.

## Interrupt Signal Grammar

The framework scans EVERY user reply after checkpoint for these signals. Matching is case-insensitive and whitespace-tolerant.

| Signal | Keywords | Action |
|--------|----------|--------|
| CONTINUE | enter, 继续, next, proceed, yes, ok, go | Advance to next phase |
| SKIP | skip, 跳过, 不用, 不需要, skip this | Skip the NEXT phase (not the current one) |
| DONE | done, 够了, 可以了, 完成, finish, good | Mark workflow complete, trigger post-game review, exit |
| RECALL | recall, 回城, 方向不对, 换个思路, go back, redo | Return to previous phase, discard current phase output |
| STOP | stop, 停, 我自己来, 退出, quit, abort | Exit framework immediately, preserve all artifacts, NO post-game review |
| VERBOSE | 别废话, 简洁点, 太啰嗦, too verbose, be brief, tldr | Record Type 5 complaint, condense current and future output |

### Ambiguity Resolution

If user input matches multiple signals:
- STOP > RECALL > DONE > SKIP > CONTINUE (safety-first)
- "stop 方向不对" → STOP wins (highest priority)
- "skip 我自己来" → STOP wins (STOP > SKIP)

If no signal is detected and input doesn't look like a workflow decision:
- Treat as CONTINUE with user feedback (the input may be additional context for the next phase)

## Recovery

If the conversation is interrupted by timeout or disconnection:

1. On reconnect, framework reads the LAST checkpoint block in the conversation
2. Framework restores: current phase number, total phases, completed artifacts
3. Framework outputs: "⚡ SUMMONER RECONNECT — Resuming from Phase {N}/{Total}: {phase名}. Previous artifacts preserved."
4. User can continue, recall, or stop from there.

Artifact preservation: all files written to disk survive. In-memory state (current analysis results) must be reconstructable from the checkpoint block text.
