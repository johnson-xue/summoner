# Capability Map — 能力归属唯一权威表

> Spec §2 四层职责与能力去重（2026-11-10 增补）。Every skill/agent/command
> registration in `plugin.json` + `.claude-plugin/plugin.json` has exactly one
> row below; nothing registered is marked deleted, nothing marked deleted is
> (still) registered beyond its recorded execution task. The example
> capability map `skills/roster.yaml` and this table are consistent — update
> them together, never one after the other.

## 四层职责（spec §2）

| 层 | 负责 | Summoner 的用法 |
|---|---|---|
| Claude Code 内置/捆绑 | 通用工具操作、局部任务（code-review、security-review、init、run…） | 能满足就直接复用，不包装 |
| superpowers 等方法论插件 | brainstorming、writing-plans、executing-plans、TDD、systematic-debugging、verification… | 按能力选用，不复制流程正文 |
| 项目 skills | 业务规则、约定、项目测试/部署 | 领域工作交给它们 |
| **Summoner** | 检查点、需求版本与变更闭环、暂停续做、验收证据、预算、恢复、复盘 | **只拥有现成 skills 不提供的控制能力** |

薄入口原则：`/summoner:*` 命令定目标、选能力、管闭环；方法论正文一律由所选
provider skill 承载。Summoner 不新增与现成 skills 重复的方法论 skill。

## 解析顺序与优先级（capability → provider）

1. **显式绑定优先（authoritative）**：项目 manifest
   `phases.<capability>.skill` 的 `default`（或用户在检查点显式选择的
   `alternate`）是权威——框架动词固定、项目 skill 可替换；已配置的项目
   provider 绝不因存在通用内置/捆绑替代而被替换。
2. **未显式绑定才按层发现（preference, not override）**：内置/捆绑 →
   superpowers 方法论 → 项目 skills → 无 manifest 时的 disclosed built-in
   defaults（`references/no-manifest-flow.md`）。
3. **缺失即诚实阻断**：显式配置的 provider 不可用 → 主会话停下解释；
   绝不静默回退到其他 provider。`skill: none` 是显式无能力，不是跳过
   诊断或自动推进的授权。

平台事实：插件 skills 有命名空间，同名不互斥、可同时加载——语义重叠必须由
本表 + manifest 路由层裁决，不能指望平台名称优先级自动去重。

## 命名约定（capability key ≠ route phase id）

manifest 能力键命名**能力**；服务于七动词之一且恰有同名能力时，键就是动词
（`phases.fix` / `phases.review` / `phases.ops` / `phases.release`）。
route phase id（context、diagnosis、…、operate、planning、changelog、
publish、merge、decide）是命令内部时序，唯一来源是
`hooks/shared/workflow.go`，manifest 永不出现。两者永不混写：ops 能力键是
`phases.ops`，ops 路由的域内 phase id 是 `operate`——不存在
`phases.operate`。

## 能力 → 唯一主选 provider（dedup 审计表）

| 能力 | 主选 provider (`default: true`) | 被删除/降级的 Summoner 重复实现 | 触发措辞 |
|---|---|---|---|
| requirements | `superpowers:brainstorming` | roster alternate `grill-me`（T11 删——与 brainstorming 同义的通用方法论） | 无独立 Summoner skill；仅在 /summoner:new 的 requirements phase 内解析 |
| plan | `superpowers:writing-plans` | roster alternate `planning-with-files`（T11 删） | plan phase 内解析 |
| implement / fix | `superpowers:executing-plans` | —（Summoner 从未自带实现方法论 skill） | implement/fix phase 内解析 |
| test / reproduce | `superpowers:test-driven-development` | roster alternate `tdd`（T11 删——同义双注册） | test/reproduce phase 内解析 |
| diagnosis | `superpowers:systematic-debugging` | walker 岗位 persona 花名册（diagnosis 方法论岗位）已随 2026-11 单模式切换删除 | debug/diagnosis phase 内解析；/summoner:debug 只诊断不改码 |
| verify / regression | `superpowers:verification-before-completion` | — | verify/regression phase 内解析 |
| ops | 无通用 provider（roster 示例 `default: none`；项目必须配 inline skill） | — | /summoner:ops 路由；`phases.ops` 绑定（键=动词，phase id 是 operate） |
| review（⑤） | `phases.review.verification_spec` + 单一 `reviewer` Agent（`agents/reviewer.md`） | 多岗位 fan-out persona 花名册已随 2026-11 单模式切换删除——crit-correctness/idiom/architecture/security/impact/prove-it 是 verification_spec 的 criteria，不是岗位 | /summoner:review「这份变更有哪些问题」（findings+evidence，不改码） |
| release | `skills/summoner-release/SKILL.md`——框架默认发布实现，**可替换能力**（spec §6.11），不是待删重复；项目可在 `phases.release` 覆盖 | — | /summoner:release『发版』 |

**review vs ship 边界（成文）**：review 回答「这份变更有哪些问题」（findings
+ evidence，不决策）；ship 回答「能否发布」——引用 review/验证证据做
GO/NO-GO，不重复同一审查。`commands/review.md` 与 `commands/ship.md` 均已
成文并互相指向。

## Summoner 注册面（每注册恰一行）

| 注册 | 层 | user-invocable | 触发措辞（精确 description，防自动触发竞争） |
|---|---|---|---|
| skill `summoner-setup` | 控制 | yes | "Initialize or repair a project's Summoner configuration when the user explicitly requests it, or guide them when they ask about the missing-manifest warning; never auto-initialize solely because SessionStart reports missing configuration."——薄指针 → `${CLAUDE_PLUGIN_ROOT}/scripts/summoner-setup.sh`（T11 收敛 plugin.json 描述与 SKILL.md 一致；0.6.0 起并入原 auto-setup 入口的触发纪律） |
| skill `summoner-release` | 控制（默认实现） | yes | "Version release automation … Keeps ALL THREE version manifests synchronized … Use when user invokes /summoner:release." |
| skill `phase-submit` | 控制通道 | **false** | "ONLY to submit a completed phase result — invoke when a phase's work is done; never for anything else" |
| skill `change` | 控制通道 | **false** | "ONLY to open or apply a requirement change in a live Summoner run; never for anything else" |
| skill `review-submit` | 控制通道 | **false** | "ONLY to register one review (identity-only form) in a live Summoner run, or to recover a wedged RUNNING review attempt — invoke when the review must start or the reviewer's child died mid-review; never for anything else" |
| skill `probe-submit` | 控制通道 | **false** | "ONLY to resolve an OUTCOME_UNKNOWN episode of a live Summoner run; never for anything else"（判定由 PostToolUse matcher 机械计算，wire 上不收 outcome） |
| agent `reviewer` | ⑤契约 | — | 单一 reviewer 定义：黑盒重导出 + evidence_tool_call_ids（空证据的 PASS 是 rubber-stamp 协议错误） |
| agent `worker` | 受限 worker | — | 在盘未注册（T15 评测放行后再决定注册；validate-repo PENDING_AGENTS 豁免保留） |
| commands ×8 | 薄入口 | — | fix/new/debug/ops/review/ship/release/status——定目标、选能力、管闭环；方法论正文由 provider skill 承载 |

内部通道用 `user-invocable: false` 而非 `disable-model-invocation: true`：
后者会让平台直接拦截模型的 Skill 调用，而 phase-submit/change/review-submit/
probe-submit 恰是模型的提交通道；`user-invocable: false` 隐藏用户菜单、保留
模型调用、description 常驻上下文（spec §2 2026-11-10 平台事实修正）。

## 防回潮基线（Step 4 grep）

skills 与 commands 的**注册字段**无 superpowers/内置同义方法论（T11 执行后
应为 0 命中；marketplace 检索 keywords 是搜索元数据而非注册，单模式切换时
已随 manifest 收敛复核——`personas` 检索词已移除）：

```bash
grep -nEi '"(name|path)": *"[^"]*(brainstorm|writing-plan|executing-plan|test-driven|tdd|systematic-debug|verification-before|grill-me|planning-with-files)[^"]*"' \
  plugin.json .claude-plugin/plugin.json
```

`skills/roster.yaml` 的 superpowers:* 条目是上表的主选路由行本身（唯一
default），不是重复注册。agents 面的多岗位 fan-out persona 花名册已随
2026-11 单模式切换删除（文件与注册项一并移除）；存留的 agent 定义只有
`agents/reviewer.md`（已注册）与 `agents/worker.md`（在盘，注册待行为
批准，见注册面行）。

可用使用信号（如 skill_activated 遥测，可选）复核：未观测到使用的推荐项
降级或移除。
