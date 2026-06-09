<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://img.shields.io/badge/Summoner-AI%20编排框架-8b5cf6?style=for-the-badge&logo=leagueoflegends&logoColor=white">
    <img src="https://img.shields.io/badge/Summoner-AI%20编排框架-6d28d9?style=for-the-badge&logo=leagueoflegends&logoColor=white" alt="Summoner">
  </picture>
</p>

<p align="center">
  <strong>像 Makefile 定义构建步骤一样定义 AI 工作流。框架动词固定——项目 skill 可替换。</strong>
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
  <a href="https://johnson-xue.github.io/summoner">文档站点</a>
</p>

---

## Summoner 是什么？

AI coding agent 能力很强，但缺乏纪律。没有结构化流程时，它们会跳过诊断直接改代码、忘记复盘、声称完成但从未验证。问题不是能力——是流程。

**Summoner 就是流程层。** 灵感来自英雄联盟——选择英雄（skill），知道何时进场（execute），何时 B 键回城（checkpoint）。每场比赛结束复盘（post-game review），经验积累，越用越强。

### 解决的核心问题

| 没有 Summoner | 有 Summoner |
|--------------|-----------|
| AI 跳过诊断直接改代码 | **Phase 1 铁律** — 根因未确认前禁止修改代码 |
| 中途发现方向错了改不了 | **Checkpoint 协议** — 每个 Phase 暂停，继续/跳过/回城/停止 |
| 上次踩的坑这次还踩 | **Memory Chain** — SQLite 驱动的 Phase 0 历史 pattern 检索 |
| 代码审查"应该做"但总忘 | **Post-Game Review** — 5 种复盘问卷，hook 程序化强制 |
| 直接调 skill 漏了关联 skill | **Command 编排** — `/summoner:fix` 自动链接 debug→test→review |
| 不同项目 skill 不同 | **summoner.yaml Manifest** — 每个项目声明自己的 skill 映射 |
| 纯 .md 指令缺乏一致性 | **Go 生命周期 Hooks** — 程序化约束，AI 零配合 |

### Token 成本（诚实）

| 场景 | Tokens | vs 直接调 skill |
|------|:------:|:---:|
| `/summoner:fix`（复杂 bug，memory 命中） | ~9,300 | +4,300 |
| `/summoner:fix`（简单，无 memory 匹配） | ~8,300 | +3,300 |
| `/summoner:debug`（仅诊断） | ~4,300 | +1,300 |

> **原则：** 多步骤工作流（修 Bug、新功能、发版审查）用 Summoner。单步骤任务（改名、改配置值）直接用领域 skill。

---

## 平台支持

Summoner 的核心工作流（SKILL.md 路由 + checkpoint 协议 + 赛后复盘）适用于任何 AI 编程平台。高级功能（hooks 程序化约束、Memory Chain SQLite 检索）因平台而异。

### 功能矩阵

| 平台 | 命令入口 | Memory Chain | Hooks | Personas | 接入方式 |
|:------|:--------:|:------------:|:-----:|:--------:|:-----:|
| **Claude Code** | ✅ 斜杠命令 | ✅ SQLite | ✅ Go | ✅ 并行审查 | `plugin.json` |
| **Gemini CLI** | ✅ TOML | ✅ bash | — | ✅ | `.gemini/commands/` |
| **OpenCode** | ✅ 意图路由 | ✅ bash | — | ✅ | `skills/` |
| **Cursor** | ✅ 规则 | ✅ bash | — | — | `.cursor/rules/` |
| **Windsurf** | ✅ 规则 | ✅ bash | — | — | `.windsurfrules` |
| **Copilot** | ✅ 指令 | ✅ bash | — | — | `.github/copilot-instructions.md` |
| **Aider** | ✅ 约定 | ✅ bash | — | — | `CONVENTIONS.md` |
| **Codex** | ⚠️ 手动 | ⚠️ bash | — | — | 提示词触发 |

✅ = 原生支持  ✅ bash = 通过 shell 命令  — = 不支持

### 操作系统

| 操作系统 | 状态 | 备注 |
|:--------|:---:|:------|
| **macOS** | ✅ 完整 | 全部功能支持，Go hooks 原生编译 |
| **Linux** | ✅ 完整 | 全部功能支持，需要 `sqlite3` 在 PATH 中 |
| **Windows** | ⚠️ WSL | Shell 脚本需要 WSL/Git Bash，Go hooks 在 WSL 内编译 |

### 功能层级

| 层级 | 平台 | 获得的能力 |
|:-----|:-----|:----------|
| **完整** | Claude Code | 斜杠命令 + Go hooks（自动状态追踪）+ SQLite 记忆链 + Persona 并行审查 + Checkpoint 程序化约束 |
| **标准** | Gemini CLI, OpenCode | 命令/路由 + SQLite 记忆链（markdown 驱动）+ Personas |
| **基础** | Cursor, Windsurf, Copilot, Aider, Codex | Markdown 指令 + SQLite 记忆链（bash 驱动）+ Checkpoint 协议 |

## 快速开始

```bash
# 1. 安装
git clone https://github.com/johnson-xue/summoner.git ~/.claude/plugins/summoner/
cd ~/.claude/plugins/summoner/hooks && make build

# 2. 项目接入
cd your-project
~/.claude/plugins/summoner/scripts/summoner-init.sh

# 3. 初始化记忆数据库（推荐）
~/.claude/plugins/summoner/scripts/init-memory-db.sh your-project-name

# 4. 验证配置
~/.claude/plugins/summoner/scripts/validate-manifest.sh summoner.yaml
```

---

## 命令速查

| 命令 | 链路 | 场景 |
|------|------|------|
| `/summoner:fix` | 诊断→复现→修复→验证→审查 | 修 Bug |
| `/summoner:new` | 定义→计划→实现→测试→审查 | 新功能 |
| `/summoner:ship` | fan-out(1-3 personas)→合并→决策 | 发版审查 |
| `/summoner:debug` | 仅诊断 | 快速排查 |
| `/summoner:ops` | 运维 skill（委托） | 服务器操作 |
| `/summoner:review` | 仅代码审查 | 独立审查 |

---

## 架构

```
┌──────────────────────────────────────────────────┐
│  summoner.yaml  (项目端)                          │
│  debug→my-debug-skill, test→my-test-skill          │
├──────────────────────────────────────────────────┤
│  Summoner 插件                                   │
│  ┌───────────┐ ┌──────────────┐ ┌─────────────┐  │
│  │ commands/ │ │ summoner/    │ │ hooks/ (Go) │  │
│  │ 6 个命令  │ │ SKILL.md     │ │ SessionStart│  │
│  │ (/summoner│ │ 路由中枢     │ │ PreToolUse  │  │
│  │ :fix,...) │ │              │ │ Stop        │  │
│  └───────────┘ └──────────────┘ └─────────────┘  │
│  ┌───────────┐ ┌──────────────┐ ┌─────────────┐  │
│  │ agents/   │ │ memory/      │ │ references/ │  │
│  │ 3 personas│ │ SQLite db    │ │ 7 个协议文档 │  │
│  └───────────┘ └──────────────┘ └─────────────┘  │
├──────────────────────────────────────────────────┤
│  现有 Skills（不动）                              │
│  Superpowers + 项目领域 skills                    │
└──────────────────────────────────────────────────┘
```

### 最佳实践

1. **优先用 `/summoner:fix` 而非直接调 skill。** Phase 0 在诊断第一步前加载历史经验。
2. **不要跳过 Phase 1。** 再明显的 bug 也值得结构化诊断。Memory Chain 经常发现非直觉关联。
3. **善用 checkpoint。** 方向偏了 `recall` 比 undo 代码便宜。已知怎么修 `skip` 跳过复现。
4. **每次复盘都做。** 1 分钟复盘喂养 Memory Chain，下次触发时省 10 分钟。
5. **有意识地设置 `project.name`。** 同一名称跨分支共享经验，不同名称隔离记忆。
6. **源码更新后重建 hooks。** `cd hooks && make build` 后生效。

### 文件结构

```
summoner/
├── plugin.json                # Claude Code 插件声明
├── hooks/                     # Go 生命周期 hooks
│   ├── bin/                   # 编译产物 (make build)
│   ├── shared/                # 公共 Go 工具
│   ├── session-start/         # 上下文注入
│   ├── pretooluse-skill/      # 状态追踪
│   ├── stop/                  # 复盘提醒
│   └── Makefile
├── skills/summoner/SKILL.md   # 路由中枢
├── commands/    (6 md)        # 命令定义
├── agents/      (3 md)        # 通用 personas
├── references/  (7 md+json)   # 协议规范
├── scripts/     (3 sh)        # 初始化和验证
├── memory/                    # SQLite (运行时)
├── .gemini/commands/  (6 toml) # Gemini CLI 斜杠命令
├── .opencode/                  # OpenCode 集成指南
└── docs/                       # 设计文档 + Codex 配置指南```

---

## 相关项目

- [anthropics/skills](https://github.com/anthropics/skills) — 官方 Anthropic skill 示例
- [obra/superpowers](https://github.com/obra/superpowers) — 通用 Claude Code 技能库
- [addyosmani/agent-skills](https://github.com/addyosmani/agent-skills) — 工程级 skill 集合
- [claude-mem](https://github.com/yoloshii/ClawMem) — 混合 RAG 智能体记忆

## License

MIT © [Jingshan Xue](https://github.com/johnson-xue)
