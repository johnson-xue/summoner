# Summoner 单循环 — 序列骨架与套件路由（唯一权威索引）

定位：本文件只拥有两样东西 — 单循环的**序列骨架**与**套件路由表**。每个
领域的权威文本住在它自己的 reference / 控制面 skill 里；机制（状态机、
检查点、预算、证据、recall 历史）由 Go hooks 执行。本文任何小节都不复制
权威内容 — 要么是别处没有的骨架，要么是指针。

## 1. 套件路由表（意图 → 入口）

| 入口 | 何时用 |
|------|--------|
| `/summoner:fix` | 修 Bug 全链路；「排查」类短语默认（repair-bias） |
| `/summoner:debug` | 仅诊断 — 明确只要原因、不要改码 |
| `/summoner:new` | 新增功能全链路（spec/plan 先行） |
| `/summoner:ops` | 运维操作（委托项目 ops skill） |
| `/summoner:review` | 独立代码审查（不改代码） |
| `/summoner:ship` | 发版前审查（fan-out 审查 → go/no-go） |
| `/summoner:release` | 版本发布（规划 → changelog → publish） |
| `/summoner:status` | 只读状态视图 |

**fix vs debug 消歧**（「排查」类短语同时命中两者时）：

- 意图包含修复期望或未说明只要原因 → `/summoner:fix`（repair-bias 默认）
- 意图明确只要原因、不要改码 → `/summoner:debug`
- 两可且用户明确犹豫 → 问一句再路由

**边界**：用户点名某个领域 skill → 直达该 skill；纯问答 → 直接回答；
不硬套工作流。每个套件的阶段路线以各自 command 文件为准（机制权威在
`hooks/shared/workflow.go`）。

## 2. 单循环六步（骨架；细节见各步指针）

1. **Run start** — 用户提交 `/summoner:<套件>`；UserPromptSubmit hook
   记录 run（唯一 run 启动者，你无需也无权开 run）。一个会话至多一个
   活 run。
2. **Phase work** — 阶段按框架固定顺序开；能力来自 manifest
   （`phases.<capability>.skill`），无 manifest → 内置默认能力并明示
   「未配置项目专属能力」。工作在本会话完成；只可委派类型化的
   reviewer/worker Agent。`version: "1"` manifest 免迁移（hooks 内存
   翻译，按 SessionStart 打印的 v2 视图路由）。
3. **Submit** — 阶段工作完成 → `Skill(summoner:phase-submit)` —
   唯一提交通道。payload 与规则权威：`../skills/phase-submit/SKILL.md`。
4. **Render** — 运行 summoner-status 二进制，输出**逐字**粘贴进回复。
   渲染与显示验证权威：`checkpoint-protocol.md`。
5. **Decide** — AskUserQuestion（nonce 入题；选项 = 已持久化菜单页的
   逐字标签）。两轮决策与分页权威：`checkpoint-protocol.md`。
6. **Repeat** — 直到用户决定 done/stop 或路线落定。

## 3. 指针节（权威在别处，此处只指路）

- **铁律 / 跳过 / Red Flags**：`workflow-reference.md`（(M) 标记与
  auto-skip 表是权威 — 跳过的阶段标 UNVERIFIED/UNREVIEWED，永不装 PASS）。
- **recall**（回城重做）：`workflow-reference.md`。
- **停止 / 急停 / 恢复**：`workflow-reference.md`（停止永远可行，推进
  永远难；`SUMMONER_HOOKS_DISABLE=1` 仅解砖，用于活 run 记违约）。
- **需求变更闭环**：`../skills/change/SKILL.md`（open/apply 通道、
  增量恢复、不重跑流水线）。
- **检查点渲染 / 两轮决策 / 分页菜单 / 预算生命周期**：
  `checkpoint-protocol.md`（预算 = T18：确认 → 耗尽 → 追加）。
- **post-game 关闭**：`post-game-review.md`（done/stop 落
  POST_GAME_PENDING；问卷 + journal 双全才能 CLOSED）。
- **Phase 0 记忆**：`memory-chain.md` §Phase 0（Top-5 检索，1500 token
  帽；无 DB → 说明后继续）。
- **能力 → 提供者解析**：`capability-map.md`（四层 + 优先级；显式绑定
  永不被通用内置顶替）。manifest 字段：`manifest-spec.md`。
- **OUTCOME_UNKNOWN 恢复与检查信任阶梯**：`manifest-spec.md` §5
  （崩溃扫描、探针对账、L1/L2/L3 阶梯）。
- **按需审查（review:）**：`../skills/review-submit/SKILL.md` +
  `../agents/reviewer.md`（身份胶囊冻结 check_id@digest）。

## 4. 表面单一职责（本重构的立约）

- 每个 `/summoner:<套件>` command 恰好编排一套工作流，自足可用。
- 控制面 skill 各管一个动词（phase-submit / probe-submit /
  review-submit / change）。
- 不存在编排多套的 skill；协议散文唯一权威落本文件与各域 reference，
  与 hooks 机制去重。
