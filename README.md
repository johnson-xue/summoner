<p align="center">
  <img src="https://img.shields.io/badge/Summoner-AI%20Orchestration-8b5cf6?style=for-the-badge&labelColor=1a1a2e" alt="Summoner">
</p>

<p align="center">
  <strong>Define AI workflows like Makefile targets.</strong><br>
  <sub>Framework verbs are fixed. Project skills are replaceable.</sub>
</p>

<p align="center">
  <a href="https://github.com/johnson-xue/summoner/stargazers"><img src="https://img.shields.io/github/stars/johnson-xue/summoner?color=8b5cf6" alt="stars"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-green" alt="license"></a>
  <a href="https://github.com/johnson-xue/summoner/releases"><img src="https://img.shields.io/github/v/release/johnson-xue/summoner?color=blue" alt="release"></a>
  <a href="#"><img src="https://img.shields.io/badge/macOS-✅-black?logo=apple" alt="macOS"></a>
  <a href="#"><img src="https://img.shields.io/badge/Linux-✅-FCC624?logo=linux&logoColor=black" alt="Linux"></a>
</p>

<p align="center">
  <a href="README_CN.md">中文</a> ·
  <a href="https://johnson-xue.github.io/summoner/">Docs</a> ·
  <a href="https://github.com/johnson-xue/summoner/releases">Releases</a>
</p>

---

AI coding agents skip diagnosis, forget reviews, and repeat mistakes. **Summoner adds a process layer** — checkpoints between phases, post-game reviews, and a memory chain that auto-recalls past fixes.

---

## Quick Start

In Claude Code:

```
/plugin marketplace add johnson-xue/summoner
/plugin install summoner@summoner-marketplace
```

Then in your project (hooks work immediately — pre-compiled for macOS/Linux):

```bash
cd your-project
~/.claude/plugins/summoner/scripts/summoner-init.sh
~/.claude/plugins/summoner/scripts/init-memory-db.sh $(grep -A2 '^project:' summoner.yaml | grep 'name:' | head -1 | awk '{print $2}')
```

> **Alternative:** `git clone https://github.com/johnson-xue/summoner.git ~/.claude/plugins/summoner/` — same result, no marketplace needed. If on an unsupported platform: `cd ~/.claude/plugins/summoner/hooks && make build`.

---

## How It Works

```
/summoner:fix "线上报错 SC_ErrInnerLogic..."

Phase 0   Memory — auto-recalls past bug patterns
Phase 1   Diagnosis — root cause (iron law: never skip)
   ⏸️     Checkpoint → [enter] continue  [skip] skip  [recall] redo  [stop]
Phase 2   Reproduce — Prove-It test (auto-skipped for config-only fixes)
Phase 3   Fix — apply the fix
Phase 4   Verify — run test suite
Phase 5   Review — code review
   📋     Post-Game Review — 5-type questionnaire, auto-journaled to SQLite
```

---

## Commands

| Command | Does |
|:--------|:-----|
| `/summoner:fix` | Bug fix: diagnose → reproduce → fix → verify → review |
| `/summoner:new` | Feature: define → plan → implement → test → review |
| `/summoner:ship` | Pre-launch: fan-out 1-3 reviewers → merge → go/no-go |
| `/summoner:debug` | Diagnosis only — no code changes |
| `/summoner:ops` | Server operations (delegated to project skill) |
| `/summoner:review` | Standalone code review |

---

## Platform Support

| | Commands | Memory | Hooks |
|:--|:--|:--|:--|
| **Claude Code** | ✅ | ✅ SQLite | ✅ Go |
| **Gemini CLI** | ✅ | ✅ bash | — |
| **OpenCode** | ✅ | ✅ bash | — |
| **Cursor / Windsurf / Copilot / Aider** | ✅ | ✅ bash | — |

---

## Token Cost

| Workflow | Tokens | vs Direct |
|:---------|:------:|:---------|
| `/summoner:fix` (with memory) | ~9,300 | +86% |
| `/summoner:fix` (no memory hit) | ~8,300 | +66% |
| `/summoner:debug` (diagnose only) | ~4,300 | +35% |

> Multi-step workflows → Summoner. Single-step tasks → direct skill.

---

## Install Size

63 files · 19 core · compiled hooks ~7.5 MB · zero external deps beyond Go + SQLite3

---

## Feedback Needed

Summoner is in early development (v0.2.0). Your feedback shapes its direction.

| What | Where |
|:-----|:------|
| **Bug report** | [GitHub Issues](https://github.com/johnson-xue/summoner/issues/new?template=bug_report.yml) — include version, platform, and what you expected |
| **Feature request** | [GitHub Issues](https://github.com/johnson-xue/summoner/issues/new?template=feature_request.yml) — tell us your use case and current workaround |
| **Questions / Ideas** | [GitHub Discussions](https://github.com/johnson-xue/summoner/discussions) — Q&A, ideas, show your setup |
| **Known Issues** | See [v0.2.0 Release Notes](https://github.com/johnson-xue/summoner/releases) for current limitations |

**Why report?** Every issue you file teaches Summoner what real-world AI workflows need. Even a single sentence helps — "I tried X, expected Y, got Z."

---

<p align="center">
  <a href="https://star-history.com/#johnson-xue/summoner&Date">
    <img src="https://api.star-history.com/svg?repos=johnson-xue/summoner&type=Date" width="480">
  </a>
</p>

## Related

- [anthropics/skills](https://github.com/anthropics/skills) — official skill examples
- [obra/superpowers](https://github.com/obra/superpowers) — general-purpose Claude Code skills
- [addyosmani/agent-skills](https://github.com/addyosmani/agent-skills) — production engineering skills

---

MIT © [Jingshan Xue](https://github.com/johnson-xue)
