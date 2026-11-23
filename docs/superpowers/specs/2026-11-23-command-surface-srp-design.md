# Command/Skill 单一职责重构 — 设计（0.6.0）

- 状态: rev2（独立评审 1×P0 + 3×P1 + 6×P2 已全部并入；待用户批准）
- 日期: 2026-11-23
- 裁决: 用户 2026-11-23 — 「skill 必须仅负责一件事，即使是负责编排 skill 的
  skill 也只应负责一套而不是多套组合，这点需要从根本上解决」。方向 A（协议
  下沉）+ 独立发布 0.6.0；inband-narrative（原 v0.6 设计）顺延 0.7.0。
- 基线: master 8525cfc（0.5.3）

## 1. 现状诊断（四类违规）

### 1.1 god-skill：一套 skill 编排八套工作流

`skills/summoner/SKILL.md`（245 行）同时承担：

| # | 职责 | 是否该归 skill |
|---|------|----------------|
| 1 | fix/new/debug/ops/review/ship/release/status 八套总路由 | 否 — 多套组合 |
| 2 | 单循环协议六步（run-start/phase-work/submit/render/decide/repeat） | 否 — 框架法，已由 Go hooks 机械化；步骤级权威见 D1 映射 |
| 3 | 铁律汇总 | 否 — workflow-reference.md (M) 标记 + Red Flags 已是权威 |
| 4 | 按需审查（review:）协议 | 否 — review-submit/SKILL.md + reviewer.md 已是权威 |
| 5 | recall 协议 | 否 — workflow-reference.md 域 |
| 6 | 停止/急停/恢复 | 否 — 机制 + reference |
| 7 | 需求变更闭环 | 否 — change/SKILL.md 已是权威 |
| 8 | 两轮决策/分页菜单 | 否 — checkpoint-protocol.md 已是权威 |
| 9 | 预算生命周期 | 否 — checkpoint-protocol.md T18 已是权威 |
| 10 | post-game 关闭 | 否 — post-game-review.md 已是权威 |
| 11 | Phase 0 记忆 / 能力解析优先级 | 否 — memory-chain.md / capability-map.md 已是权威 |

11 项职责全部在 references/、控制面 skill 或 hooks 机制里有权威落点，
god-skill 是第三份拷贝；commands 反而依赖它（`fix.md:23`、`debug.md:23`、
`new.md:46` 均写 "Loop per skills/summoner/SKILL.md"）——删除它之前必须先
给协议一个唯一权威落点。

### 1.2 release skill：编排 + 实现 + 探针协议三合一

`skills/summoner-release/SKILL.md`（275 行）内嵌 **6 个 ```bash fence**
（:30 前置校验、:53 版本规划、:97 changelog 生成、:149 回滚 trap、:171
publish 核心、:249 summoner-ctx 持久化）。两个后果：

- **CI 盲区**：scripts/ 之外的 shell 永远进不了 lane CI 的 shellcheck
  （ci.yml:85 `shellcheck scripts/*.sh` 证实 scripts/ 已在门内；0.5.2 的
  SC2181 教训）。
- **协议旁路**：`read -p` 共 4 处（:38 未提交继续、:72 增量选择、:77 自定义
  版本号（嵌在 :72 的 D 分支）、:222 gh release 确认）绕过检查点通道 —
  用户决策没有走 AskUserQuestion 两轮协议，也不留痕。

### 1.3 setup 双胞胎

`skills/summoner-setup/` 与 `skills/auto-setup-summoner/` 两个 skill 干同一
件事。auto 版触发纪律更好（仅显式请求，T11 收敛过，与 plugin.json:30-33
描述一致），setup 版名字更对齐（与 `scripts/summoner-setup.sh` 一致）。

### 1.4 roster 方法论默认值指向已停用的 superpowers

`skills/roster.yaml:13-47` 恰 9 个方法论能力 default 全指 `superpowers:*`
（:5 注释亦含）— 插件已停用，全是死指针。框架携带方法论默认值本身违反
「框架动词固定 — 方法论项目自有」原则。

### 1.5 god-skill 与 auto-setup 的完整引用面（评审核实版）

**god-skill（`skills/summoner/`）活引用**：

| 引用点 | 性质 |
|--------|------|
| hooks/session-start/main.go:219（+:185 注释） | **活代码**：每会话注入 "Workflows: read <root>/skills/summoner/SKILL.md" |
| hooks/session-start/main_test.go:56 | 测试断言该字符串 |
| .gemini/commands/summoner-*.toml ×8（如 summoner-status.toml:11） | 各自第 1 步指示读 god-skill |
| .gemini/GEMINI.md:7,25 | 称其为 routing hub |
| .opencode/INSTALL.md:7 | `cp -r skills/summoner` 安装指令，删除后直接失败 |
| docs/codex-setup.md:10 | 复制指令，同上 |
| commands/{debug,fix,new}.md:23/:23/:46 | Loop 行（全部 8 个 command 重写） |
| scripts/validate-repo.sh:515-516 | eval toml pointer-form 规则（:175 历史注释含旧路径字样，需改措辞） |
| scripts/run-routing-evals.sh:7、evals/README.md:7、evals/cases/routing.json:14（note 裸提） | 评测侧指针 |
| scripts/install-smoke-test.sh:85（cmp 比对对象）,140（名单） | 冒烟断言 |
| AGENTS.md:10、根 summoner.md、CLAUDE.md 结构图、plugin.json:10-11、.claude-plugin/plugin.json:22 | 文档/清单 |
| docs/FFI_BOUNDARY.md:18,50,120,213、docs/CODE_REVIEW_PR8.md:199、docs/2026-06-08-summoner-framework.md:195 | **冻结历史**（见 D3 裁决） |

已验证干净：README.md / README_CN.md / .claude-plugin/marketplace.json /
evals toml 路径引用（routing.json 仅 note 措辞）。

**auto-setup-summoner 活引用**（7 项，评审确认无遗漏）: plugin.json:25-26、
.claude-plugin/plugin.json:25、references/no-manifest-flow.md:32、
references/capability-map.md:73、scripts/validate-repo.sh:7（注释）、
scripts/install-smoke-test.sh:140、skills/summoner-setup/SKILL.md:43。

CHANGELOG 历史条目与 docs/superpowers 归档不改。

## 2. 目标 / 非目标

**目标**

1. 每个表面单元恰好一个职责；不存在编排多套的 skill。
2. 协议散文唯一权威落点，与机制（hooks）和各域权威文档去重。
3. 每个 command 自足编排恰好一套工作流。
4. shell 实现全部进 scripts/（shellcheck 门内）。
5. setup 合一；roster 默认去方法论化。
6. 全电池绿（build / go test 含 contract / validate-repo / evals / lane CI）。

**非目标**

- Go hooks 机制（workflow.go 路由、gate 语义、状态机、检查点协议机制）：
  零改动。唯一例外见 D8 开洞：session-start 注入的指针字符串及其测试。
- 控制面原子 skill（phase-submit / probe-submit / review-submit / change）
  不动 — 它们本来就各管一个动词。
- inband-narrative（0.7.0）设计不重开。
- 不新增任何 skill。

## 3. 设计决策

### D1 唯一协议权威 `references/single-loop.md`

**独占内容**（仅此文档有，评审确认现有六份 reference 无权威版本）：

- 六步循环的**序列骨架** — 每步一行 + 指针（见下表，步骤细节不复制）；
- 套件路由表（含 fix/debug 消歧、「未命中即纯问答」边界）。

**步骤级权威映射**（rev2 修正 — 防止把步骤细节搬进来造成第二权威）：

| 步骤 | 权威落点 |
|------|----------|
| 1 run-start / 2 phase-work | single-loop.md 骨架行（机制在 UserPromptSubmit hook） |
| 3 submit | `skills/phase-submit/SKILL.md`（通道/payload） |
| 4 render | `references/checkpoint-protocol.md` §render（逐字粘贴/display verification） |
| 5 decide | `references/checkpoint-protocol.md` §two-turn（两轮/nonce/分页） |
| 6 repeat | single-loop.md 骨架行 |

**摘要+指针节**（≤10 行/节）: 按需审查 → review-submit；recall + 铁律 →
workflow-reference（(M) 标记 + Red Flags，**不再写铁律摘要**）；停止/恢复
→ workflow-reference；变更闭环 → change/SKILL.md；两轮/分页/预算 →
checkpoint-protocol（T18）；post-game → post-game-review；Phase 0 →
memory-chain；能力解析 → capability-map；OUTCOME_UNKNOWN → manifest-spec §5。

**防复胖判据**：任何小节要么是独占内容，要么是摘要+指针；六份现有
reference 的权威域内容不迁移、不复制（验收 3 对 8525cfc diff 确认）。
single-loop.md 目标 ≤120 行。

### D2 command 即套件唯一编排者

8 个 command 全部改为自足结构（遵守 AGENTS.md:11 的 50-80 行预算）：

```
frontmatter: description（含自然语言意图短语）+ allowed-tools
正文: Route（框架路线）→ 各阶段能力（manifest 映射 + 无清单回退）
     → 套件规则（跳过/铁律/变更入口）
     → Loop 行（完整纪律密度，见下）
```

- **Loop 行规范**（rev2 修正）：保留 fix.md:23 现有全部子句 — 唯一提交通道、
  渲染逐字粘贴、nonce 入题、keyword 引导的选项标签 — 并补两轮短语（渲染轮
  收轮、下一轮再问）。五要素缺一不可，验收 4 逐条 grep。
- 自然语言路由由 description 承载（fix.md 已示范「排查」默认 repair-bias；
  debug/new/ops/review/ship/release/status 同样补齐意图短语）。
- 8 个 command 统一补 frontmatter allowed-tools（今天只有 status.md:3 有）。
- 指针一律 `${CLAUDE_PLUGIN_ROOT}/references/...` 前缀（现行 command 规范，
  pointer lint 已覆盖该深度规则）。

### D3 删除 god-skill

删 `skills/summoner/` 目录；引用面按 §1.5 清单逐一迁移：

- **hooks/session-start**: main.go:219 注入串与 :185 注释重指向
  `references/single-loop.md`（纯字符串，≤300B 预算不受影响）；
  main_test.go:56 断言同步（见 D8 开洞）。
- **.gemini/ 适配面**: 8 个 summoner-*.toml 第 1 步读取目标改
  `<plugin-root>/references/single-loop.md`；GEMINI.md:7,25 改述（commands
  即套件入口，协议在 single-loop.md）。validate-repo.sh:515-516 的
  pointer-form 规则目标同步改为 `references/single-loop.md`。
- **安装指令**: .opencode/INSTALL.md:7 与 docs/codex-setup.md:10 的复制
  指令删除 god-skill 条目（setup/控制面 skill 照旧复制）。
- **评测侧**: evals/README.md:7、run-routing-evals.sh:7、routing.json:14
  note 措辞 → single-loop.md。
- **冒烟**: install-smoke-test.sh:85 比对对象改为
  `references/single-loop.md`（保持分发内容 byte-identity 采样）；
  :140 名单去 summoner/auto-setup，:143/:146 的 "all 8/eight" 文案同步改 6。
- **文档/清单**: AGENTS.md:10「routing brain」改述；根 summoner.md、
  CLAUDE.md 结构图、两个 plugin.json 数组同步。
- **validate-repo.sh:175 历史注释**: 改写措辞（保留 0.5.2 事故描述，不再
  含 `skills/summoner/` 字面路径），避免验收 1 grep 永假。
- **冻结历史裁决**: docs/FFI_BOUNDARY.md、docs/CODE_REVIEW_PR8.md、
  docs/2026-06-08-summoner-framework.md 与 docs/superpowers 归档同列 —
  不更新（历史快照），验收 grep 显式排除。

### D4 setup 合一

- 保留目录名 `skills/summoner-setup/`（对齐 scripts/summoner-setup.sh），
  语义采用 auto 版触发纪律：仅显式请求 / 询问缺失告警时引导，绝不因
  SessionStart 报告缺失而自动初始化。
- 删 `skills/auto-setup-summoner/`；引用迁移：两个 plugin.json 数组、
  no-manifest-flow.md:32、capability-map.md:73 表行改名、validate-repo.sh:7
  注释、install-smoke-test.sh:140、summoner-setup/SKILL.md:43 自引用改
  no-manifest-flow.md。

### D5 release 瘦身（协议一致性修复 + CI 覆盖）

**脚本归属**（rev2 明确 — 6 个 fence 逐一下放，无孤儿）：

| 脚本 | 吸收的 fence | 职责 |
|------|--------------|------|
| `scripts/release-plan.sh` | :30, :53 | 前置校验（非交互化）、版本计算/校验、tag 查重；输出提议版本+类型（可 dry-run） |
| `scripts/release-changelog.sh` | :97 | commit 区间收集、分类、changelog 内容生成（纯 stdout） |
| `scripts/release-publish.sh` | :149, :171, :249 | **三份清单版本写入（root plugin.json / .claude-plugin/plugin.json / marketplace.json）**、CHANGELOG 插入、commit/tag/push、回滚 trap、summoner-ctx 持久化（可选路径全部 warn-continue）、gh release（flag 门控） |

- 单用途、flags 入参、bash 3.2 兼容、进 lane CI shellcheck 覆盖。
- **去交互化**：4 处 `read -p` 全部移除，决策归还检查点通道 —
  - 版本增量选择（:72）与自定义版本号（:77）→ planning 阶段检查点决策
    （AskUserQuestion），脚本接收已定 `--version X.Y.Z`；
  - 未提交变更是否继续（:38）→ planning 阶段首个决策项；
  - gh release 是否创建（:222）→ publish 阶段范围决策，flag 传入。
  脚本不再问人。
- 回滚 trap 保留在 release-publish.sh 内（tag/commit 删除、文件保全）。
- SKILL.md 变调用契约：when_to_use、flags、每阶段用哪个脚本（上表）、
  phase-submit payload 形状、探针必声明（L1 grep-count / git tag 只读探针）、
  post-game 指针。目标 ≤90 行、零内嵌 bash fence。
- 测试场景不变：`--patch --dry-run` / `--minor`（真实）/ `--version 2.0.0` /
  publish 中断回滚 / 探针三分支。

### D6 roster 去方法论化

- 9 个方法论能力（requirements/plan/implement/fix/test/reproduce/diagnosis/
  verify/regression）default 全改 `none` + 注释「绑定项目自有 skill；
  未绑定 → 内置默认能力并明示」（:5 注释同步去 superpowers）。
- capability-map.md 层级行走保留（描述可能性层级，不再是任何默认值）。

### D7 校验加固

- install-smoke-test.sh 新增断言：两个 plugin.json 的 skills 数组 ==
  skills/ 实际目录集合（防 1.3 类漏改复发）。
- **归一化前置**（rev2 补）：root plugin.json 数组是 name/path 对象形态、
  .claude-plugin/plugin.json 是 `"./skills/x"` 字符串形态，断言先归一化再比
  （现状 validate-repo.sh:133-135 仅覆盖后者字符串形态；root 侧 93-115 已有
  双向校验，增量在 .claude-plugin 侧）。
- validate-repo.sh §3b pointer lint matrix 不变；新拓扑（command →
  `${CLAUDE_PLUGIN_ROOT}/references/single-loop.md`）沿用既有前缀/深度规则，
  保持 matched>0。

### D8 hooks 机制零改动（含一个显式开洞）

Stop 显示验证、WAIT_USER 写门、预算拒写、phase-submit 唯一提交通道等机制
不受本重构影响 — prompt 纪律文本的载体变化由机制兜底。这是方案 A 成立的
前提。

**开洞**：session-start 的注入指针字符串（main.go:219）与其测试
（main_test.go:56）必须随 D3 重指向 single-loop.md — 纯字符串替换，不触
机制逻辑；这是删除 god-skill 后模型稳定可见的纪律入口（R2 的最强载体）。

## 4. 方案对比（已裁决）

| 方案 | 形态 | 弃因 |
|------|------|------|
| **A 协议下沉（采纳）** | 删 god-skill；协议唯一权威 reference；command 即套件编排者 | — |
| B 原子 phase 命令 | /summoner:diagnose 等，hooks 全权排序 | 丢「一条命令一条流水线」入口；UserPromptSubmit run-start 形状与 evals 全重写 |
| C 每套双轨 | 8 mini-skill + 8 command | 表面翻倍，比现状更冗余 |

## 5. 风险与缓解

- **R1 自然语言路由回归**：god-skill 的 when_to_use 是隐性入口捕获器。
  缓解：command description 承载意图短语（D2）+ evals/cases/routing.json
  （已 pin fix/debug 消歧，评审确认无路径依赖）必须全绿。
- **R2 prompt 纪律弱化**：god-skill 曾在每次 /summoner:* 调用注入纪律文本。
  缓解（rev2 补强）：**session-start 注入重指向 single-loop.md（每会话稳定
  可见，最强载体）** + command Loop 行五要素密度（D2）+ 机制兜底（D8）；
  渲染逐字粘贴由 Stop 显示验证实际强制，同轮作答由 decision_rejected 拒绝。
- **R3 release 脚本化回归**：shellcheck/bash 3.2/去交互改造引入新 bug。
  缓解：lane CI 前置 + D5 测试场景矩阵照旧 + 与 0.5.2 相同「scripts/ 改动
  lane CI 先于 tag」纪律。
- **R4 双 plugin.json 漏改**：D7 归一化数组断言。

## 6. 验收标准

1. `git grep -nE 'skills/summoner($|/)'` 全仓零命中，显式排除表：
   `:(exclude)CHANGELOG.md`、`:(exclude)docs/`（含冻结历史与 superpowers
   归档）。（rev2 改为全仓 grep + 排除表 — 原路径清单式 grep 有盲区且
   :175 注释永假。）
2. `git grep -n 'auto-setup-summoner'` 同排除表下零命中，skills/ 下无该目录。
3. references/single-loop.md 存在且 ≤120 行；六份现有 reference 对 8525cfc
   的 diff 为零（权威域未迁移）。
4. 8 个 command 均含：意图 description、allowed-tools、Route、能力映射、
   Loop 行五要素（唯一通道/逐字粘贴/nonce/keyword 标签/两轮 — 逐条 grep）。
5. skills/summoner-release/SKILL.md ≤90 行、零 ```bash fence；三个
   release-*.sh 存在且 shellcheck 干净（lane CI）。
6. roster.yaml 无 superpowers 引用；9 个方法论 default 均为 none。
7. 全电池：`make -C hooks build release` → `go test ./...`（含 contract，
   含 session-start 指针测试改后绿）→ `scripts/validate-repo.sh` →
   routing evals → lane CI 绿。
8. install-smoke-test 归一化数组断言生效并绿。

## 7. 与 0.7.0 的关系

inband-narrative（origin/inband-narrative @4f80ced，spec rev4 + plan rev2）
设计内容不变，版本号顺延为 0.7.0；其 SessionStart in-band 注入上线后将
进一步取代 single-loop.md 的模型侧阅读需求（本版 D8 开洞的指针届时并入其
in-band 协议块）。两版各自独立可审。

## 修订记录

- rev1（274fe3a）: 初稿。
- rev2: 并入独立评审 — P0 session-start 活引用与 D8 开洞；P1 .gemini
  8 toml + GEMINI.md 迁移、D1 步骤级权威映射（3/4/5 步不复制）、验收 1
  改全仓 grep+排除表；P2 ×6（安装指令、:175 注释措辞、冒烟比对对象与
  文案、Loop 行五要素密度、fence 计数与脚本归属、数组归一化、routing.json
  note）。
