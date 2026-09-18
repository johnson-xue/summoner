# Summoner — AI Agent Orchestration Framework

> Define AI workflows like Makefile targets. Framework verbs fixed — project skills replaceable.

**This file is a POINTER, not a copy.** All protocols (checkpoint format, memory chain,
post-game review) live exactly once in the installed plugin's `references/` and are
loaded on demand. Do not execute a paraphrased protocol from this page — executing a
stale paraphrase is how pre-August-2026 integrations drifted.

## Quick Start (Any Platform)

1. Install the plugin (or copy the repo) and locate the plugin root — the directory
   containing both `scripts/` and `memory/` (in Claude Code:
   `~/.claude/plugins/cache/summoner-marketplace/summoner/<version>/`).
2. Add `summoner.yaml` to your project root declaring your phase→skill mappings
   (field spec: `<plugin-root>/references/manifest-spec.md`).
3. Optional: `<plugin-root>/scripts/init-memory-db.sh <project-name>`
4. Invoke a workflow (below). The routing hub is
   `<plugin-root>/skills/summoner/SKILL.md` — read it first; it dispatches phases,
   enforces checkpoints, and names the references to load per phase.

## Workflows (canonical chains — full rules in `<plugin-root>/commands/<name>.md`)

| Workflow | Chain |
|----------|-------|
| `fix` | debug(M) → reproduce → fix → verify → review |
| `new` | define → plan → implement → test → review |
| `ship` | fan-out(1-3 personas) → merge → GO/NO-GO |
| `debug` | diagnose only, no code changes |
| `ops` | ops skill (delegated) |
| `review` | code review only |
| `release` | version-plan → changelog → release-execution (self-contained) |

(M) = Mandatory — iron law.

## The Three Protocols (all pointer, zero restatement)

- **Checkpoint Protocol** — after every phase, render the exact box from
  `<plugin-root>/references/checkpoint-protocol.md` (5 mandatory fields, fixed
  `Next:` grammar: enter/skip/done/recall/stop) and wait for user input.
  Never auto-advance; content feedback is handled before re-asking.
- **Memory Chain (Phase 0)** — follow
  `<plugin-root>/references/memory-chain.md` §Phase 0 (feature extraction,
  Top-5 SQL, 1500-token cap with silent degradation). DB path resolution for
  non-Claude platforms is specified there.
- **Post-Game Review** — at workflow end, run the 5-type questionnaire from
  `<plugin-root>/references/post-game-review.md` and write the journal entry.

## Iron Law

For `fix` and `debug`: Phase 1 (diagnosis) is mandatory — root cause confirmed
before any code change.
