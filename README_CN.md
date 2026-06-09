<p align="center">
  <img src="https://img.shields.io/badge/Summoner-AI%20编排框架-8b5cf6?style=for-the-badge&labelColor=1a1a2e" alt="Summoner">
</p>

<p align="center">
  <strong>像 Makefile 一样定义 AI 工作流。</strong><br>
  <sub>框架动词固定。项目 skill 可替换。</sub>
</p>

<p align="center">
  <a href="https://github.com/johnson-xue/summoner/stargazers"><img src="https://img.shields.io/github/stars/johnson-xue/summoner?color=8b5cf6" alt="stars"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-green" alt="license"></a>
  <a href="https://github.com/johnson-xue/summoner/releases"><img src="https://img.shields.io/github/v/release/johnson-xue/summoner?color=blue" alt="release"></a>
  <a href="#"><img src="https://img.shields.io/badge/macOS-✅-black?logo=apple" alt="macOS"></a>
  <a href="#"><img src="https://img.shields.io/badge/Linux-✅-FCC624?logo=linux&logoColor=black" alt="Linux"></a>
</p>

<p align="center">
  <a href="README.md">English</a> ·
  <a href="https://johnson-xue.github.io/summoner/">文档</a> ·
  <a href="https://github.com/johnson-xue/summoner/releases">版本</a>
</p>

---

AI coding agent 能力很强，但缺乏纪律——跳过诊断、忘记审查、重复犯错。**Summoner 加上流程层**：Phase 间强制暂停（checkpoint）、赛后复盘（post-game review）、自动检索历史经验的记忆链（Memory Chain）。

---

## 快速开始

在 Claude Code 中：

```
/plugin marketplace add johnson-xue/summoner
/plugin install summoner@summoner-marketplace
```

然后在你的项目中（hooks 开箱即用——预编译了 macOS/Linux 二进制）：

```bash
cd your-project
~/.claude/plugins/summoner/scripts/summoner-init.sh
~/.claude/plugins/summoner/scripts/init-memory-db.sh $(grep name: summoner.yaml | head -1 | awk '{print $2}')
```

> **替代方式：** `git clone https://github.com/johnson-xue/summoner.git ~/.claude/plugins/summoner/` — 效果相同，无需 marketplace。非主流平台需编译：`cd ~/.claude/plugins/summoner/hooks && make build`。

---

## 工作方式

```
/summoner:fix "线上报错 SC_ErrInnerLogic..."

Phase 0   记忆检索 — 自动匹配历史 bug pattern
Phase 1   诊断根因 — 铁律：禁止跳过
   ⏸️     Checkpoint → [enter] 继续  [skip] 跳过  [recall] 回城  [stop] 停
Phase 2   复现测试 — Prove-It 模式（纯配置修复自动跳过）
Phase 3   修复
Phase 4   验证 — 跑测试套件
Phase 5   审查 — 代码审查
   📋     赛后复盘 — 5 种问卷，自动写入 SQLite
```

---

## 命令

| 命令 | 用途 |
|:------|:-----|
| `/summoner:fix` | 修 Bug：诊断 → 复现 → 修复 → 验证 → 审查 |
| `/summoner:new` | 新功能：定义 → 计划 → 实现 → 测试 → 审查 |
| `/summoner:ship` | 发版审查：并行 1-3 个 reviewer → 合并决策 |
| `/summoner:debug` | 仅诊断——不改代码 |
| `/summoner:ops` | 运维操作（委托给项目 skill） |
| `/summoner:review` | 独立代码审查 |

---

## 平台支持

| | 命令 | 记忆 | Hooks |
|:--|:--|:--|:--|
| **Claude Code** | ✅ | ✅ SQLite | ✅ Go |
| **Gemini CLI** | ✅ | ✅ bash | — |
| **OpenCode** | ✅ | ✅ bash | — |
| **Cursor / Windsurf / Copilot / Aider** | ✅ | ✅ bash | — |

---

## Token 成本

| 工作流 | Tokens | 相对直接调用 |
|:-------|:------:|:----------|
| `/summoner:fix`（记忆命中） | ~9,300 | +86% |
| `/summoner:fix`（无记忆命中） | ~8,300 | +66% |
| `/summoner:debug`（仅诊断） | ~4,300 | +35% |

> 多步骤工作流 → Summoner。单步骤任务 → 直接调 skill。

---

## 安装体积

63 个文件 · 19 个核心 · 编译后 hooks ~7.5 MB · 零外部依赖（Go + SQLite3 除外）

---

<p align="center">
  <a href="https://star-history.com/#johnson-xue/summoner&Date">
    <img src="https://api.star-history.com/svg?repos=johnson-xue/summoner&type=Date" width="480">
  </a>
</p>

## 相关项目

- [anthropics/skills](https://github.com/anthropics/skills) — 官方 skill 示例
- [obra/superpowers](https://github.com/obra/superpowers) — 通用 Claude Code 技能库
- [addyosmani/agent-skills](https://github.com/addyosmani/agent-skills) — 工程级 skill 集合

---

MIT © [Jingshan Xue](https://github.com/johnson-xue)
