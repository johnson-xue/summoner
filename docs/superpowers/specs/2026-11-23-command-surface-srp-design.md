# Command/Skill 单一职责重构 — 设计（0.6.0）

- 状态: 草案 rev1（待独立评审 → 用户批准）
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
| 2 | 单循环协议六步（run-start/phase-work/submit/render/decide/repeat） | 否 — 框架法，已由 Go hooks 机械化 |
| 3 | 铁律汇总 | 否 — 机制执行 + reference |
| 4 | 按需审查（review:）协议 | 否 — review-submit/SKILL.md + reviewer.md 已是权威 |
| 5 | recall 协议 | 否 — workflow-reference.md 域 |
| 6 | 停止/急停/恢复 | 否 — 机制 + reference |
| 7 | 需求变更闭环 | 否 — change/SKILL.md 已是权威 |
| 8 | 两轮决策/分页菜单 | 否 — checkpoint-protocol.md 已是权威 |
| 9 | 预算生命周期 | 否 — checkpoint-protocol.md T18 已是权威 |
| 10 | post-game 关闭 | 否 — post-game-review.md 已是权威 |
| 11 | Phase 0 记忆 / 能力解析优先级 | 否 — memory-chain.md / capability-map.md 已是权威 |

11 项职责中 10 项在 references/ 或控制面 skill 里已有权威文本，god-skill 是
第三份拷贝；commands 反而依赖它（`fix.md:23`、`debug.md:23`、`new.md:46`
均写 "Loop per skills/summoner/SKILL.md"）——删除它之前必须先给协议一个
唯一权威落点。

### 1.2 release skill：编排 + 实现 + 探针协议三合一

`skills/summoner-release/SKILL.md`（275 行）内嵌三段完整 bash 程序（版本
规划 / changelog 生成 / publish+回滚 trap）。两个后果：

- **CI 盲区**：scripts/ 之外的 shell 永远进不了 lane CI 的 shellcheck
  （0.5.2 的 SC2181 教训）。
- **协议旁路**：脚本内 `read -p`（增量选择、gh release 确认、未提交确认）
  绕过检查点通道 —— 用户决策没有走 AskUserQuestion 两轮协议，也不留痕。

### 1.3 setup 双胞胎

`skills/summoner-setup/` 与 `skills/auto-setup-summoner/` 两个 skill 干同一
件事。auto 版触发纪律更好（仅显式请求，T11 收敛过），setup 版名字更对齐
（与 `scripts/summoner-setup.sh` 一致）。

### 1.4 roster 方法论默认值指向已停用的 superpowers

`skills/roster.yaml` 全部方法论能力 default 指向 `superpowers:*` — 插件已
停用，全是死指针。框架携带方法论默认值本身违反「框架动词固定 — 方法论项目
自有」原则。

### 1.5 god-skill 与 auto-setup 的完整引用面（grep 实测）

god-skill（`skills/summoner/`）被引用于: commands/{debug,fix,new}.md、
AGENTS.md:10、根 summoner.md、evals/README.md:7、
scripts/run-routing-evals.sh:7、scripts/validate-repo.sh:515-516（eval toml
pointer-form 规则）、scripts/install-smoke-test.sh:85,140、CLAUDE.md 结构图。

auto-setup-summoner 被引用于: plugin.json:25-26、.claude-plugin/plugin.json
skills 数组、references/no-manifest-flow.md:32、references/capability-map.md:73、
scripts/validate-repo.sh:7（注释）、scripts/install-smoke-test.sh:140、
skills/summoner-setup/SKILL.md:43。CHANGELOG 历史条目不改。

## 2. 目标 / 非目标

**目标**

1. 每个表面单元恰好一个职责；不存在编排多套的 skill。
2. 协议散文唯一权威落点，与机制（hooks）和各域权威文档去重。
3. 每个 command 自足编排恰好一套工作流。
4. shell 实现全部进 scripts/（shellcheck 门内）。
5. setup 合一；roster 默认去方法论化。
6. 全电池绿（build / go test 含 contract / validate-repo / evals / lane CI）。

**非目标**

- Go hooks、workflow.go 路由、gate 语义、状态机、检查点协议机制：零改动。
- 控制面原子 skill（phase-submit / probe-submit / review-submit / change）
  不动 — 它们本来就各管一个动词。
- inband-narrative（0.7.0）设计不重开。
- 不新增任何 skill。

## 3. 设计决策

### D1 唯一协议权威 `references/single-loop.md`

- **独占内容**（仅此文档有）：单循环六步、套件路由表（含 fix/debug 消歧、
  「未命中即纯问答」边界）、铁律摘要。
- **摘要+指针**（≤10 行/节，权威在别处）：按需审查 → review-submit；recall
  → workflow-reference；停止/恢复 → workflow-reference；变更闭环 →
  change/SKILL.md；两轮决策/分页/预算 → checkpoint-protocol（T18）；post-game
  → post-game-review；Phase 0 → memory-chain；能力解析 → capability-map；
  OUTCOME_UNKNOWN → manifest-spec §5。
- **防复胖判据**：任何小节要么是独占内容，要么是摘要+指针；六份现有
  reference 的权威域不迁移、不复制。single-loop.md 目标 ≤120 行。

### D2 command 即套件唯一编排者

8 个 command 全部改为自足结构：

```
frontmatter: description（含自然语言意图短语）+ allowed-tools
正文: Route（框架路线）→ 各阶段能力（manifest 映射 + 无清单回退）
     → 套件规则（跳过/铁律/变更入口）
     → Loop 一行（per references/single-loop.md: work → phase-submit
        → 渲染逐字粘贴 → AskUserQuestion）
```

- 自然语言路由由 description 承载（fix.md 已示范「排查」默认 repair-bias；
  debug/new/ops/review/ship/release/status 同样补齐意图短语）。
- 8 个 command 统一补 frontmatter allowed-tools（今天只有 status.md 有）。
- 指针一律 `${CLAUDE_PLUGIN_ROOT}/references/...` 前缀（现行 command 规范，
  pointer lint 已覆盖该深度规则）。

### D3 删除 god-skill

- 删 `skills/summoner/` 目录；引用面按 §1.5 清单逐一迁移：
  - 三个 command 的 Loop 行 → D2 重写（全部 8 个）。
  - AGENTS.md:10「routing brain」→ 改述为「commands 即套件入口，协议在
    references/single-loop.md」。
  - 根 summoner.md 指针文档同步。
  - evals/README.md:7 + run-routing-evals.sh:7 指针改 single-loop.md。
  - validate-repo.sh:515-516 eval toml pointer-form 规则：目标从
    `skills/summoner/SKILL.md` 改为 `references/single-loop.md`。
  - install-smoke-test.sh:85（文件比对对象删除）、:140（skill 名单去
    summoner）。
  - CLAUDE.md 结构图去 skills/summoner 行。
  - 两个 plugin.json 的 skills 数组去 `./skills/summoner`（见 D7 数组断言）。

### D4 setup 合一

- 保留目录名 `skills/summoner-setup/`（对齐 scripts/summoner-setup.sh），
  语义采用 auto 版触发纪律：仅显式请求 / 询问缺失告警时引导，绝不因
  SessionStart 报告缺失而自动初始化。
- 删 `skills/auto-setup-summoner/`；引用迁移：两个 plugin.json 数组、
  no-manifest-flow.md:32、capability-map.md:73 表行改名、validate-repo.sh:7
  注释、install-smoke-test.sh:140、summoner-setup/SKILL.md:43 自引用改
  no-manifest-flow.md。

### D5 release 瘦身（协议一致性修复 + CI 覆盖）

- 三段 bash 抽为 `scripts/release-plan.sh`、`scripts/release-changelog.sh`、
  `scripts/release-publish.sh`：单用途、flags 入参、bash 3.2 兼容、进 lane CI
  shellcheck 覆盖（scripts/ 已在范围）。
- **去交互化**：`read -p` 全部移除，决策归还检查点通道 —
  - 版本增量选择 → planning 阶段检查点决策（AskUserQuestion）；
  - 未提交变更是否继续 → planning 阶段首个决策项；
  - gh release 是否创建 → publish 阶段范围决策。
  脚本只接收已定参数（`--version X.Y.Z` 等），不再问人。
- 回滚 trap 保留在 release-publish.sh 内（tag/commit 删除、文件保全）。
- SKILL.md 变调用契约：when_to_use、flags、每阶段用哪个脚本、phase-submit
  payload 形状、探针必声明（L1 grep-count / git tag 只读探针）、post-game
  指针。目标 ≤90 行、零内嵌 bash 程序。
- 测试场景不变：`--patch --dry-run` / `--minor`（真实）/ `--version 2.0.0` /
  publish 中断回滚 / 探针三分支。

### D6 roster 去方法论化

- 方法论能力（requirements/plan/implement/fix/test/reproduce/diagnosis/
  verify/regression）default 全改 `none` + 注释「绑定项目自有 skill；
  未绑定 → 内置默认能力并明示」。superpowers 引用清除。
- capability-map.md 层级行走保留（描述可能性层级，不再是任何默认值）。

### D7 校验加固

- install-smoke-test.sh 新增断言：两个 plugin.json 的 skills 数组 ==
  skills/ 实际目录集合（防 1.3 类漏改复发）。
- validate-repo.sh §3b pointer lint matrix 不变；新拓扑（command →
  `${CLAUDE_PLUGIN_ROOT}/references/single-loop.md`）沿用既有前缀/深度规则，
  保持 matched>0。

### D8 hooks 零改动（风险兜底声明）

Stop 显示验证、WAIT_USER 写门、预算拒写、phase-submit 唯一提交通道等机制
不受本重构影响 — prompt 纪律文本的载体变化由机制兜底。这是方案 A 成立的
前提，设计上显式声明而不是隐含。

## 4. 方案对比（已裁决）

| 方案 | 形态 | 弃因 |
|------|------|------|
| **A 协议下沉（采纳）** | 删 god-skill；协议唯一权威 reference；command 即套件编排者 | — |
| B 原子 phase 命令 | /summoner:diagnose 等，hooks 全权排序 | 丢「一条命令一条流水线」入口；UserPromptSubmit run-start 形状与 evals 全重写 |
| C 每套双轨 | 8 mini-skill + 8 command | 表面翻倍，比现状更冗余 |

## 5. 风险与缓解

- **R1 自然语言路由回归**：god-skill 的 when_to_use 是隐性入口捕获器。
  缓解：command description 承载意图短语（D2）+ evals/cases/routing.json
  （已 pin fix/debug 消歧）必须全绿。
- **R2 prompt 纪律弱化**：god-skill 曾在每次 /summoner:* 调用注入纪律文本。
  缓解：机制兜底（D8）+ command Loop 行保留紧凑纪律 + single-loop.md 摘要；
  残余风险 = 渲染逐字粘贴等纯文本纪律，由 Stop 显示验证机制实际强制。
- **R3 release 脚本化回归**：shellcheck/bash 3.2/去交互改造引入新 bug。
  缓解：lane CI 前置 + D5 测试场景矩阵照旧 + 与 0.5.2 相同「scripts/ 改动
  lane CI 先于 tag」纪律。
- **R4 双 plugin.json 漏改**：D7 数组断言。

## 6. 验收标准

1. `grep -r "skills/summoner/" commands/ references/ scripts/ evals/ *.md
   .claude-plugin/ plugin.json` 仅剩 CHANGELOG 历史与 docs/superpowers 归档。
2. `grep -r auto-setup-summoner` 同上，且 skills/ 下无该目录。
3. references/single-loop.md 存在且 ≤120 行；六份现有 reference 权威域
   内容未迁移（diff 确认）。
4. 8 个 command 均含：意图 description、allowed-tools、Route、能力映射、
   Loop 指针（single-loop.md）。
5. skills/summoner-release/SKILL.md ≤90 行、零内嵌 bash 程序块；三个
   release-*.sh 存在且 shellcheck 干净（lane CI）。
6. roster.yaml 无 superpowers 引用；方法论 default 均为 none。
7. 全电池：`make -C hooks build release` → `go test ./...`（含 contract）
   → `scripts/validate-repo.sh` → routing evals → lane CI 绿。
8. install-smoke-test 数组断言生效并绿。

## 7. 与 0.7.0 的关系

inband-narrative（origin/inband-narrative @4f80ced，spec rev4 + plan rev2）
设计内容不变，版本号顺延为 0.7.0；其 SessionStart in-band 注入上线后将
进一步取代 single-loop.md 的模型侧阅读需求。两版各自独立可审。
