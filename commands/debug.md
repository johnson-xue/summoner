---
description: 仅诊断 — context → diagnosis，止于诊断检查点，不改任何代码。快速、轻量。仅当用户明确只要原因、不要改码（'先别改代码'/'只告诉我原因'/'我自己来修'）时使用；未说明的「排查」类短语默认走 /summoner:fix。
allowed-tools:
  - Skill
  - Read
  - Bash
  - AskUserQuestion
  - Agent
---

# /summoner:debug

Diagnosis only. The run ends at the diagnosis checkpoint; the report is the
deliverable the user can act on themselves (or hand to `/summoner:fix`).

## Route

context → diagnosis → 检查点（结束）

- **context**: memory retrieval (`${CLAUDE_PLUGIN_ROOT}/references/memory-chain.md` §Phase 0).
- **diagnosis**: root cause + trigger conditions + location + evidence, via
  the manifest-mapped debug/diagnose skill. Infra vs 业务码分流: infra
  symptoms (端口/启动/中间件) → `phases.diagnose`; config mismatches →
  `phases.config`; business-code errors → `phases.debug`. All resolved from
  `summoner.yaml`; no manifest → built-in defaults, declared.

## Rules

1. Loop per `${CLAUDE_PLUGIN_ROOT}/references/single-loop.md`: work → `summoner:phase-submit`
   with a conclusion (the only submission channel) → paste the checkpoint and end
   the turn → the Stop hook hands back the question → AskUserQuestion. An auto-passed
   phase shows no checkpoint: go straight on.
2. NO code changes, no test files — the write gate enforces this until
   diagnosis is accepted, and the workflow ends there anyway.
3. 诊断报告 = 根因、影响范围、修复建议（给用户的输入，不是修复承诺）。
4. "问题很简单，顺手修了吧" → 不行。合约是只诊断；要修用 `/summoner:fix`
   （recall/新开 run 由用户决定）。

## Post-Game Review

Mandatory — Type 4 (流程评价) per `${CLAUDE_PLUGIN_ROOT}/references/post-game-review.md`.
