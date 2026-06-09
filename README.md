<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://img.shields.io/badge/Summoner-AI%20Orchestration-8b5cf6?style=for-the-badge&logo=leagueoflegends&logoColor=white">
    <img src="https://img.shields.io/badge/Summoner-AI%20Orchestration-6d28d9?style=for-the-badge&logo=leagueoflegends&logoColor=white" alt="Summoner">
  </picture>
</p>

<p align="center">
  <strong>Define AI workflows like Makefile targets. Framework verbs fixed — project skills replaceable.</strong>
</p>

<p align="center">
  <a href="https://github.com/johnson-xue/summoner/stargazers"><img src="https://img.shields.io/github/stars/johnson-xue/summoner?style=flat&color=8b5cf6" alt="stars"></a>
  <a href="https://github.com/johnson-xue/summoner/blob/master/LICENSE"><img src="https://img.shields.io/badge/license-MIT-green?style=flat" alt="license"></a>
  <a href="https://github.com/johnson-xue/summoner/releases"><img src="https://img.shields.io/badge/version-0.1.0-blue?style=flat" alt="version"></a>
  <a href="#"><img src="https://img.shields.io/badge/platform-Claude%20Code-purple?style=flat" alt="platform"></a>
  <a href="#"><img src="https://img.shields.io/badge/hooks-Go-00ADD8?style=flat&logo=go&logoColor=white" alt="go"></a>
  <a href="#"><img src="https://img.shields.io/badge/macOS-000000?style=flat&logo=apple&logoColor=white" alt="macOS"></a>
  <a href="#"><img src="https://img.shields.io/badge/Linux-FCC624?style=flat&logo=linux&logoColor=black" alt="Linux"></a>
</p>

<p align="center">
  <a href="README.md">English</a> ·
  <a href="README_CN.md">中文</a> ·
  <a href="https://johnson-xue.github.io/summoner">Documentation</a>
</p>

---

## What is Summoner?

AI coding agents are powerful but undisciplined. Without structure, they skip diagnosis, forget reviews, and produce work that passes tests but fails in production.

**Summoner is the process layer.** Inspired by League of Legends — choose your champion (skill), know when to engage (execute), and when to B-recall (checkpoint). After every match, review what happened (post-game review). Patterns accumulate. Every session gets better.

### The Problem Summoner Solves

| Without Summoner | With Summoner |
|------------------|---------------|
| AI jumps to code before understanding the bug | **Phase 1 Iron Law** — no changes before root cause is confirmed |
| No way to correct direction mid-flight | **Checkpoint Protocol** — pause at every phase (continue/skip/recall/stop) |
| Lessons learned are forgotten next session | **Memory Chain** — SQLite Phase 0 retrieval of past patterns |
| Code review "should happen" but often doesn't | **Post-Game Review** — 5-type questionnaire, hook-enforced |
| Direct skill invocation misses related skills | **Command Orchestration** — `/summoner:fix` chains debug→test→review |
| Different projects need different skills | **summoner.yaml Manifest** — each project declares its own skill mapping |
| Markdown instructions lack consistency | **Go Lifecycle Hooks** — programmatic enforcement, AI writes nothing |

### Token Cost (Honest)

| Scenario | Tokens | vs Direct Skill |
|----------|:------:|:---:|
| `/summoner:fix` (complex bug, memory hits) | ~9,300 | +4,300 |
| `/summoner:fix` (simple, no memory match) | ~8,300 | +3,300 |
| `/summoner:debug` (diagnose only) | ~4,300 | +1,300 |

> **Rule of thumb:** Use Summoner for multi-step workflows (bugs, features, reviews). Use direct skills for single-step tasks (rename a variable, change a config value).

---

## Platform Support

Summoner's core workflow (SKILL.md routing + checkpoint protocol + post-game review) works on any AI coding platform. Advanced features (hooks, Memory Chain SQLite) are platform-dependent.

### Feature Matrix

| Platform | Commands | Memory Chain | Hooks | Personas | Setup |
|:---------|:--------:|:------------:|:-----:|:--------:|:-----:|
| **Claude Code** | ✅ Slash | ✅ SQLite | ✅ Go | ✅ fan-out | `plugin.json` |
| **Gemini CLI** | ✅ TOML | ✅ bash | — | ✅ | `.gemini/commands/` |
| **OpenCode** | ✅ AGENTS.md | ✅ bash | — | ✅ | `skills/` |
| **Cursor** | ✅ Rules | ✅ bash | — | — | `.cursor/rules/` |
| **Windsurf** | ✅ Rules | ✅ bash | — | — | `.windsurfrules` |
| **Copilot** | ✅ Instructions | ✅ bash | — | — | `.github/copilot-instructions.md` |
| **Aider** | ✅ Conventions | ✅ bash | — | — | `CONVENTIONS.md` |
| **Codex** | ⚠️ Manual | ⚠️ bash | — | — | Prompt |

✅ = native support  ✅ bash = works via shell commands  — = not available

### Operating Systems

| OS | Status | Notes |
|:---|:------:|:------|
| **macOS** | ✅ Full | All features supported. Go hooks compile natively. |
| **Linux** | ✅ Full | All features supported. Requires `sqlite3` in PATH. |
| **Windows** | ⚠️ WSL | Shell scripts require WSL/Git Bash. Go hooks compile in WSL. Native PowerShell fallback not yet implemented. |

### Feature Tiers by Platform

| Tier | Platforms | What You Get |
|:-----|:----------|:-------------|
| **Full** | Claude Code | Slash commands + Go hooks (auto state tracking) + SQLite Memory Chain + Persona fan-out + Checkpoint enforcement |
| **Standard** | Gemini CLI, OpenCode | Slash commands or intent routing + SQLite Memory Chain (markdown-driven) + Personas |
| **Basic** | Cursor, Windsurf, Copilot, Aider, Codex | Markdown instructions + SQLite Memory Chain (bash-driven) + Checkpoint protocol |

## Quick Start

```bash
# 1. Install
git clone https://github.com/johnson-xue/summoner.git ~/.claude/plugins/summoner/
cd ~/.claude/plugins/summoner/hooks && make build

# 2. Add summoner.yaml to your project
cd your-project
~/.claude/plugins/summoner/scripts/summoner-init.sh

# 3. Initialize memory database (optional but recommended)
~/.claude/plugins/summoner/scripts/init-memory-db.sh your-project-name

# 4. Validate
~/.claude/plugins/summoner/scripts/validate-manifest.sh summoner.yaml
```

---

## Commands

| Command | Pipeline | When to Use |
|---------|----------|-------------|
| `/summoner:fix` | diagnosis→reproduce→fix→verify→review | Bug fixing |
| `/summoner:new` | define→plan→implement→test→review | New features |
| `/summoner:ship` | fan-out(1-3 personas)→merge→decision | Pre-launch review |
| `/summoner:debug` | diagnosis only | Quick investigation |
| `/summoner:ops` | ops skill (delegated) | Server operations |
| `/summoner:review` | code review only | Standalone review |

---

## Architecture

```
┌──────────────────────────────────────────────────┐
│  summoner.yaml  (per project)                     │
│  debug→my-debug-skill, test→my-test-skill          │
├──────────────────────────────────────────────────┤
│  Summoner Plugin                                 │
│  ┌───────────┐ ┌──────────────┐ ┌─────────────┐  │
│  │ commands/ │ │ summoner/    │ │ hooks/ (Go) │  │
│  │ 6 entries │ │ SKILL.md     │ │ SessionStart│  │
│  │ (/summoner│ │ meta-skill   │ │ PreToolUse  │  │
│  │ :fix,...) │ │ routing hub  │ │ Stop        │  │
│  └───────────┘ └──────────────┘ └─────────────┘  │
│  ┌───────────┐ ┌──────────────┐ ┌─────────────┐  │
│  │ agents/   │ │ memory/      │ │ references/ │  │
│  │ 3 personas│ │ SQLite db    │ │ 7 protocols │  │
│  └───────────┘ └──────────────┘ └─────────────┘  │
├──────────────────────────────────────────────────┤
│  Existing Skills (unchanged)                      │
│  Superpowers + your project domain skills         │
└──────────────────────────────────────────────────┘
```

### Best Practices

1. **Prefer `/summoner:fix` over direct skill invocation.** Phase 0 loads relevant past patterns before the first diagnostic step.
2. **Don't skip Phase 1.** Even obvious bugs benefit from structured diagnosis. The Memory Chain often surfaces non-obvious connections.
3. **Use the checkpoint.** Wrong direction? `recall`. Already know the fix? `skip` the reproduce phase.
4. **Complete post-game reviews.** A 1-minute review feeds the Memory Chain and saves 10 minutes the next time.
5. **Set `project.name` deliberately.** Same name across branches → shared experience. Different name for divergent branches → isolated memory.
6. **Rebuild hooks after updates.** `cd hooks && make build` after pulling changes.

### File Map

```
summoner/
├── plugin.json                # Claude Code plugin declaration
├── hooks/                     # Go lifecycle hooks
│   ├── bin/                   # Compiled binaries (make build)
│   ├── shared/                # Shared Go utilities
│   ├── session-start/         # Context injection
│   ├── pretooluse-skill/      # State tracking
│   ├── stop/                  # Review reminder
│   └── Makefile
├── skills/summoner/SKILL.md   # Meta-skill: routing hub
├── commands/    (6 md)        # Slash command definitions
├── agents/      (3 md)        # Reusable personas
├── references/  (7 md+json)   # Protocol specs + schema
├── scripts/     (3 sh)        # init, validate, wizard
├── memory/                    # SQLite databases (runtime)
├── .gemini/commands/  (6 toml) # Gemini CLI slash commands
├── .opencode/                  # OpenCode integration guide
└── docs/                       # Design docs + Codex setup guide```

---

## Related

- [anthropics/skills](https://github.com/anthropics/skills) — Official Anthropic skill examples
- [obra/superpowers](https://github.com/obra/superpowers) — General-purpose Claude Code skills
- [addyosmani/agent-skills](https://github.com/addyosmani/agent-skills) — Production-grade engineering skills
- [claude-mem](https://github.com/yoloshii/ClawMem) — Agent memory with hybrid RAG

## License

MIT © [Jingshan Xue](https://github.com/johnson-xue)
