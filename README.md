# Summoner — AI Agent Orchestration Framework

> 像 Makefile 定义构建步骤一样定义 AI 工作流。框架动词固定，项目 skill 可替换。

Summoner 是一个可移植、项目无关的 Claude Code 插件，为 AI coding agent 提供结构化工作流编排。灵感来自 LoL 的召唤师——选择召唤哪个英雄（skill），知道什么时候进场、什么时候 B 键回城（checkpoint）。

## 核心能力

- **6 个开箱即用的工作流**: `/summoner:fix` `/summoner:new` `/summoner:ship` `/summoner:debug` `/summoner:ops` `/summoner:review`
- **中断友好的 Checkpoint**: 每个 Phase 结束暂停，用户可继续/跳过/回城/停止，不丢产物
- **赛后复盘**: 5 种问卷类型，每次 session 结束自动触发
- **Memory Chain**: SQLite 驱动的跨 session 记忆检索，Phase 0 自动匹配历史经验
- **fan-out 并行审查**: `/summoner:ship` 并行启动 code-reviewer + security-auditor + test-engineer
- **项目无关**: 通过 `summoner.yaml` manifest 声明项目 skill，框架无硬编码

## 架构

```
┌──────────────────────────────────────────────┐
│  summoner.yaml (项目端)                       │
│  声明: debug→antia-debug, test→antia-test...  │
├──────────────────────────────────────────────┤
│  Summoner Plugin (框架端)                     │
│  commands/  用户入口 (/summoner:*)            │
│  skills/    路由中枢 (Phase 0→1→...→checkpoint)│
│  agents/    通用 personas (code-reviewer...)  │
│  memory/    SQLite 跨 session 记忆            │
├──────────────────────────────────────────────┤
│  Existing Skills (不动)                       │
│  Superpowers + 项目领域 skills                │
└──────────────────────────────────────────────┘
```

## 快速开始

### 安装

```bash
git clone <this-repo> ~/.claude/plugins/summoner/
```

### 项目接入

在项目根目录创建 `summoner.yaml`：

```yaml
version: "1"
project:
  name: my-project

phases:
  debug:
    skill: my-debug-skill
  test:
    skill: my-test-skill
  ops:
    skill: my-ops-skill
  security:
    skill: none              # 显式无此能力

workflows:
  bugfix:
    chain: [debug, reproduce, fix, verify, review]
    checkpoints: after_each
  ship:
    fan_out:
      - persona: code-reviewer
      - persona: security-auditor
      - persona: test-engineer
    merge: review
    checkpoints: after_merge
```

### 初始化 Memory

```bash
~/.claude/plugins/summoner/scripts/init-memory-db.sh my-project
```

### 验证 Manifest

```bash
~/.claude/plugins/summoner/scripts/validate-manifest.sh summoner.yaml
```

## 使用

```
/summoner:fix     修 Bug 全链路（诊断→复现→修复→验证→审查）
/summoner:new     新增功能全链路（定义→计划→实现→测试→审查）
/summoner:ship    发版前审查（并行 fan-out → go/no-go 决策）
/summoner:debug   仅诊断，不修复
/summoner:ops     运维操作
/summoner:review  独立代码审查
```

## 文件结构

```
summoner/
├── plugin.json                  # Claude Code 插件声明
├── CLAUDE.md                    # Agent 自举引导
├── AGENTS.md                    # 开发规范
├── skills/summoner/SKILL.md     # 路由中枢 (meta-skill)
├── commands/                    # 6 个 slash commands
├── agents/                      # 3 个通用 personas
├── references/                  # 5 个协议规范
├── scripts/                     # init-memory-db.sh, validate-manifest.sh
├── memory/                      # SQLite 数据库 (运行时生成)
└── docs/                        # 设计文档
```

## 设计文档

- [Spec](docs/specs/2026-06-08-summoner-framework-design.md) — 完整设计规范
- [Plan](docs/2026-06-08-summoner-framework.md) — 实现计划 (26 tasks)

## 与现有 Skill 体系的关系

Summoner 不与 superpowers 或项目领域 skills 冲突。它是编排层：

```
用户 → /summoner:fix
         → Phase 0: Memory 检索
         → Phase 1: 读 summoner.yaml → 路由到 antia-debug
         → Phase 2-5: 同样的路由机制
         → Checkpoint (每个 Phase 暂停)
         → Post-Game Review
```

项目原有的直接调用 `antia-debug` 继续可用。Summoner 是可选增强，不是替代。

## License

MIT
