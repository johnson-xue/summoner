# Summoner 单循环检查点设计（Single-Loop Checkpoint Design）

日期：2026-11-09
状态：已获用户批准方向（对抗小组 Borda 胜出案 + 用户四项裁决）
来源：4 视角对抗小组（fold-first 胜出 11:9:4:0；verify-first/human-gate/debt-first 的 graft 已并入）；文章原则 P1-P9 与时效裁决 T1/T2 见 §0；平台事实 V1-V6 于 2026-11-09 对 Claude Code 2.1.319 逐一核验。

## 0. 前提与铁律

**用户已裁决的四项分叉：**
1. **删除 summoner-walker 二进制**——记账/放行职责全部并入 Go hooks（事件 reducer）。
2. **终局⑤默认强制 + 可显式跳过**——跳过时产物标 `UNREVIEWED`，不能伪造 PASS。
3. **接受开箱 BLOCKED**——未安装推荐沙箱 settings 时项目登记检查（L2）不运行；框架内置检查（L1）任何环境可用。
4. **紧急 stop = 最小 journal + 补答**——立即停止，写最小退出 journal，状态留 `POST_GAME_PENDING`，下次会话补问卷。

**框架沉降的默认值（我的裁决，可被推翻）：**
- headless/`-p` 场景工作流停在首个检查点（无推进通道）——C3 的诚实代价，不建预授权通道（v1）。
- 对手边界：本地同用户场景下权威日志无绝对写保护（分层抬高门槛+可检篡改，见 §5）；容器级隔离超出 C4，不做。
- 行为评测的预算/非劣容忍度/关键缺陷要求：在评测任务检查点由用户当场裁决（在此之前一切收益主张是待测假设）。

**不弱化 C3 任何铁律**：fix/debug 先诊断后改码；检查点须用户确认、绝不自动推进；post-game review 强制；框架动词固定、项目 skill 可替换；不硬编码项目名/领域路径。本设计把这些铁律从 prompt 纪律升级为机制，均为收紧。

**时效裁决（贯穿全程）：**
- T1：文章经验数字（3-10× token、15-20/30-50 工具阈值、1000 tokens、80% 缩减、2× 速度、12 subagent、10 样例）一律为历史观察，不作设计依据。
- T2：「模型够强所以可折叠/可自评」类论断不成立——折叠幅度与验证保留一律以 fold-and-measure 评测裁决，设计不绑定模型版本。
- 平台断言记核验日期；Claude Code 升级重跑能力契约测试，模型/提示变更重跑行为对照。

## 1. 模式形态

### 唯一执行器

主会话 + inline skills + Go hook 事件 reducer（gatekeeper）。无 mode 字段、无图块检测、无 chain fallback、无兼容 parser。

- 主会话是唯一 LLM 编排者与默认唯一 LLM 执行者：理解意图、读 manifest、选能力、做工作、跑登记检查、产出结果、处理反馈。
- 固定命令定义阶段顺序；项目清单只替换能力映射，不编写执行拓扑。独立委派是阶段内工作批次，不是第二模式。
- Go reducer：阶段状态、用户授权、证据、审查生命周期、recall 历史、预算。校验合法转换、冻结验收视图、渲染检查点——不解释 DAG、不下发指令、不做领域工作。

### 真实执行协议（平台事件锚定，全部为本机 2.1.319 现有能力）

1. **阶段开启**：命令展开后首个阶段 Skill 调用被 PreToolUse(Skill) 观测，reducer 置 ACTIVE。
2. **阶段提交**：零逻辑微 skill `summoner:phase-submit`。主会话以 `Skill(summoner:phase-submit, args=phase-result JSON)` 提交；PreToolUse 校验 payload 与 reducer 状态（阶段 id、产物集、标准 id、检查结果引用），合法则原子持久化 `phase.closed` + `checkpoint.opened(checkpoint_id, 产物版本)` 并置 WAIT_USER；不合法 exit 2 并说明。跳过提交直接闲聊不推进任何状态。
3. **展示核验**：Stop hook 收 `last_assistant_message` 检索该 checkpoint_id 的渲染块；缺失则 `decision:block, reason:"渲染 checkpoint <id>"`。生成文本 ≠ 已展示。
4. **用户裁决**：推进只能经 **AskUserQuestion 工具**（选项含 checkpoint 短 nonce 与 continue/skip/done/recall/stop/[v]独立审查）。PostToolUse(AskUserQuestion) 收平台注入的 tool_response（模型无法伪造），校验问题负载携带当前 checkpoint_id 且选择合法，才写 `checkpoint.decided`。UserPromptSubmit 在定时任务/后台 subagent 回报/跨会话消息时也触发（无 origin 字段）——**不能当人类来源证明**，自由文本一律不授予推进。
5. **AskUserQuestion 注入守卫**（human-gate 投票人抓的洞）：任何 PreToolUse hook 可经 `updatedInput` 程序化注入 answers（平台文档明示）。守卫=安装契约断言 `askUserQuestionTimeout: never` + 契约测试覆盖注入面 + 决策幂等/nonce 绑定；平台无法区分注入与真人点击的部分作为已声明残余记录在档。
6. **停止不对称**：stop 语义随时生效——自由文本含停止信号即结束当回合（Stop hook 保留状态），不加对话框。最容易的永远是停下，最难的是推进。

### manifest 与计划文档

- manifest 只表达：`phases` 能力映射（review→verification_spec，见 §3）、阶段内 skill 变体、登记检查（check registry）、工具范围、有限预算。
- 计划文档表达目标/交付物/任务清单/依赖/验收标准，**不是可执行拓扑**。计划检查点提交结构化任务索引与标准，用户确认后 hooks 冻结文档摘要、criterion id、产物范围与版本。
- 无 manifest 项目用插件内置 inline 默认能力进同一 reducer（删除旧 No-Manifest 链）；manifest 显式 skill 缺失即停在主会话解释；`skill: none` 不构成跳过诊断或自动推进的授权。状态按规范化项目根散列 + session id + run id 隔离。

### 完整流程

每个箭头受前一检查点用户决定约束，包括被显式跳过的阶段。

- `/summoner:fix`：P0 只读检索记忆→检查点；P1 主会话调 debug skill 产出根因+触发条件+定位+证据→检查点（诊断被用户接受前 Edit/Write/Bash 写入被 gate 阻断）；P2 同上下文复现测试→检查点；P3 选 fix skill 修复并同上下文局部测试→检查点；P4 同上下文回归+集成→检查点；P5 默认一次独立⑤审查（可显式跳过→UNREVIEWED）→检查点；post-game 收尾。诊断未知只能继续调查或停止。P2-P5 可显式跳过并标 UNVERIFIED/UNREVIEWED，不能伪造 PASS。
- `/summoner:new`：P0→P1 定义+标准→P2 计划冻结→P3 同上下文实现+局部测试→P4 跨组件与负向验证→P5 ⑤→复盘。保留 spec/plan 先行。
- `/summoner:debug` 止于诊断检查点。review/ship 用同一⑤契约；ops/release 领域步骤由项目能力承担，但用户门、trace、复盘走同一 reducer。

### 需求变更闭环（requirement-change loop，2026-11-10 增补）

业务开发中需求频繁调整，流程必须**单点闭环**：实现/测试进行中可立刻进入需求调整，调整后增量恢复，不重跑流水线。

- **触发**：任意时刻（含 ACTIVE 实现阶段）自由文本提出需求调整——它是内容反馈，开启变更子环，**不是推进通道**；检查点也可显式进入。
- **reducer 子状态 `CHANGE_OPEN`**（ACTIVE 的子态）：`OpenRequirementChange` 先冻结开发位置（当前任务、测试进度、未决验证项）再处理变更；CHANGE_OPEN 期间当前阶段的 phase-submit 被拒（防半变更状态提交）。
- **增量处理**：明确 delta（这次改了什么）→ 影响评估（哪些需求/标准/产物/任务/测试受影响——评估方法由所选需求/计划 skill 承担，框架不另造方法论）→ `ApplyRequirementDelta`：`RequirementsVersion++`，按依赖把受影响 criterion/验证/审查结果标 **stale**；**未受影响的已接受结果与成果保持有效**。
- **单点闭环确认**：一次 AskUserQuestion「接受变更并恢复开发」（`ACCEPT_REQUIREMENT_CHANGE`），确认后回到保存的开发位置继续实现与测试。不重走 P1-P2，不重复询问未受影响阶段；旧检查点不逐一重开。
- **边界**：变更触及公共接口/架构前提时，必要的重新规划不可省——由影响评估结论决定并在确认点呈现，不由用户手动判断。
- **与 recall 的区分**（成文防混用）：recall = 方向错了回城重做（§4 语义）；需求变更 = 基线移动、增量处理、原地恢复。两者预算与 stale 传播不同。
- **命令拆分**：`/summoner:new` 保持**单一运行入口**，内部拆分为「需求管理」与「开发交付」两个能力职责（各自可绑定项目 skill）；共享同一 run 状态与主会话上下文——**拆能力，不拆上下文，不拆状态，不重新引入第二执行模式**。

## 2. agent-vs-skill 边界

| 工作 | 所在上下文 | 边界理由 |
|---|---|---|
| 诊断、定义、计划、实现、局部测试、集成测试、反馈修订、合并报告、复盘 | 主会话 + skill | 用户要看见并掌舵；实现与测试共享上下文（P2/P5）。 |
| 最终产物⑤黑盒审查 | 一个 non-fork、工具受限的 reviewer | 输入仅需需求、产物、验收标准；独立重查是流程价值，不依赖模型强度（P3/P4/P7/P9，T2）。 |
| 中途产物的接受门 | 检查点上的人类 | 人是最强的独立验证器（P5 掌舵理由）；阶段内另有机械登记检查；检查点菜单提供 `[v]` 按需对该阶段产物发起同一⑤契约的独立审查（预算记账，非新模式）。 |
| 真正独立的研究/组件分片 | 经批准的阶段内委派批次 | 干净接口、无输出依赖、覆盖收益经评测（P1/P2/P8，T2）。实现分片与其测试交同一 worker。 |
| 上下文膨胀隔离 | 同协议受限 worker | 先观测主循环退化并与折叠版对照，不按 token/工具数阈值自动触发（P1，T1/T2）。 |

- 删除 debug-agent/code-reviewer/security-auditor/test-engineer 岗位定义（审查维度并入唯一 rubric：5 轴、安全维度、Prove-It 各成 criterion 模板）；保留一份 reviewer 定义（新契约）+ 一份通用受限 worker 定义（对照评测放行后才启用）。
- ship 默认一个 fresh reviewer 覆盖完整 rubric；敏感变更增加标准不增加 persona；仅可独立验收的分片或评测证实净收益时并行。
- manifest 的 `phases.review` 映射为 **verification_spec**（追加 criteria、登记 check id、可读范围、rubric 指针）：项目定义"验什么"，reviewer 在新上下文执行"怎么验"。
- inline 纪律：主循环 skill 不得 `context: fork` 换上下文、不得自行开未经批准的 Agent 批次；hooks 对真实 Agent 调用登记与门控。

### 四层职责与能力去重（skill dedup，2026-11-10 增补）

当前环境同时存在 Claude Code 内置/捆绑 skills、superpowers 方法论 skills、项目专属 skills 与 Summoner 自身——语义重叠大量存在，自动触发互相竞争。Summoner 收缩为**控制层**，不做第四套通用技能库。

| 层 | 负责 | Summoner 的用法 |
|---|---|---|
| Claude Code 内置/捆绑 | 通用工具操作、局部任务 | 能满足就直接复用，不包装 |
| superpowers 等 | 方法论（brainstorming、writing-plans、TDD、调试…） | 按能力选用，不复制流程正文 |
| 项目 skills | 业务规则、约定、项目测试/部署 | 领域工作交给它们 |
| **Summoner** | 检查点、需求版本与变更闭环、暂停续做、验收证据、预算、恢复、复盘 | **只拥有现成 skills 不提供的控制能力** |

- **薄入口原则**：`/summoner:*` 命令定目标、选能力、管闭环；方法论正文一律由所选 provider skill 承载。Summoner 不新增与现成 skills 重复的方法论 skill。
- **capability→provider 唯一主选**：manifest 中每个能力恰好一个 `default` provider，其余显式标 `alternate`——不并列默认，消除自动触发竞争。roster 大而全的推荐清单随之收敛。
- **平台事实**：插件 skills 有命名空间，同名不互斥、可同时加载；语义重叠必须由路由层（manifest）裁决，不能指望平台名称优先级自动去重。
- **触发面收缩**：内部辅助 skill（phase-submit 等）`disable-model-invocation: true`；对外 skill 描述写精确触发条件。
- **去重审计交付物**：`references/capability-map.md`——能力 | 主选 provider | 被删除的 Summoner 重复实现 | 触发措辞。可用使用信号（如 skill_activated 遥测，可选）复核：未观测到使用的推荐项降级或移除。
- **review/ship 边界成文**：review 回答「这份变更有哪些问题」，ship 回答「能否发布」；ship 引用 review/验证证据做 GO/NO-GO 决策，不重复同一审查。

## 3. ⑤审查契约

### 唯一输入 schema（严格 allow-list 拒未知字段）

信封字段：`schema_version`、`run_id`、`review_id`；`artifact_set`（非空 {path, digest, kind}，代码审查含明确基线+快照，无 Git 用文件摘要基线）；`requirements`（冻结的已接受需求视图）；`exit_criteria`（唯一 id；目标、范围、`method: mechanical|judgment`、预期结果、必需性、边界/应失败输入；mechanical 引用登记 check_id，judgment 引用 rubric 与客观定位要求）；`verification_scope`（可读产物与规范、允许 check id、输出 schema；运行权限由 hooks 实际登记决定，不自授）。不含 factual_claim、producer verdict、reasoning trace、attempt_history、budget、stripped、node id。

- **冻结验收视图**：需求被用户接受那一刻由 hooks 生成并冻结（requirement id → criterion id → artifact 范围），生产者不能裁剪。信封 criteria 集 = 冻结集 − 有记录的用户显式 drop（逐 id、逐次决定）；reducer 拒绝缺 id 的信封。
- **check registry**：每项 {check_id, command, args, cwd, env 白名单, 定义摘要, trust: framework|project}。信封创建时冻结所用 check 定义摘要；执行时 reducer 复核摘要，变更即信封版本升级。机械规则：check 定义文件出现在待验 diff 内 → 该 criterion 降级为 judgment，除非 reviewer 显式对照基线验证了检查本身有效性。

### 隔离与只读机制（V2/V3）

reviewer 定义 frontmatter：

```yaml
tools: Read, Grep, Glob, Bash
disallowedTools: Write, Edit, NotebookEdit, Agent, Skill, AskUserQuestion, TaskCreate, TaskUpdate, WebFetch, WebSearch
omitClaudeMd: true
```

主会话只能发起指定 reviewer；全局 PreToolUse 校验 Agent 类型、non-fork、信封摘要与 prompt allow-list；不复制父历史/诊断/成功宣言。reviewer 不加载技能与个人记忆；项目规范经已批准的中性输入提供。

**Bash 副作用信任阶梯：**
1. **L1 框架内置检查**：随插件发布、摘要固定，Go 适配器执行（Read/Grep 类与摘要比对），任何环境可用。
2. **L2 项目登记检查**：仅当原生沙箱可用时执行——沙箱对 Bash 及全部子进程做 OS 级 filesystem 限制（macOS Seatbelt / Linux bubblewrap；hook 程序本身不在沙箱内）。安装契约：插件附推荐 settings 片段（`sandbox.enabled` + 产物树 denyWrite + transcript/state 目录 denyRead/denyWrite + 关闭项）；契约测试检测沙箱是否实际生效，未生效则项目检查不运行（**用户已裁决：接受开箱 BLOCKED**）。
3. **L3 不可隔离**：检查需要沙箱无法提供的能力（网络、宿主服务）→ 返回 `BLOCKED`，升级主会话/用户，不降级为"跑了吧"。

不宣称绝对只读；沙箱档位如实展示在检查点。

### 证据与 verdict

reviewer 返回：`review_id`、`artifact_digest`、run_status: `COMPLETED|BLOCKED`（BLOCKED 带 reason_code，**与质量 verdict 分离**，不冒充质量结论、不耗返工预算）、`verdict: PASS|NEEDS-FIX`（仅 COMPLETED 时）、逐 criterion 结论、findings（criterion id、path:line、失败情形、建议；不含下一阶段选择）、`evidence_tool_call_ids`。

证据对账三级：Pre/PostToolUse 记录真实 tool id；**SubagentStop 的 `agent_transcript_path` 提供子代理自身 transcript**，evidence id 与子 transcript 中真实 tool_use 记录对账；覆盖关系（一检查覆盖多标准）须预先声明。PASS 须全部必需项完成、机械结果与退出态一致、judgment 有直接证据；未检查/无工具/不能判≠PASS；grep 命中只是定位。非法枚举、空证据、冒名 id、未知 scope、过期摘要 = `review.failed` 协议错误。PASS 后仍 WAIT_USER；NEEDS-FIX 生成 rework 建议并开检查点；产物在审查完成前变更则结果作废重审。同版本验收复用：产物 digest、冻结判据与验证环境相同且结果仍有效时复用一次⑤，不审查审查报告、不递归。

### 崩溃恢复

审查状态 `PREPARED→RUNNING→REVIEWED/FAILED/BLOCKED`，每步先持久化关联键。重连重放：PREPARED/RUNNING 只能恢复或重发**同一信封**的审查（消耗剩余委派预算），不重跑 producer；能从子 transcript 找到有效结果就补记；REVIEWED 未决则恢复 WAIT_USER；按真实调用 id 去重（迟到结果不得影响新代次）。

## 4. 检查点与 recall

检查点节奏固定：每阶段结束（含失败、耗尽、BLOCKED）；不为内部工具调用加人工门。reducer 状态机：`ACTIVE / WAIT_USER / REVIEW_PENDING / OUTCOME_UNKNOWN / POST_GAME_PENDING / CLOSED`；阶段闭合即 WAIT_USER，主会话"完成"、skill 启动、reviewer PASS 都不是授权。

- **展示绑定**：Stop 展示核验 + checkpoint nonce；未展示或版本不符时，任何"继续"不被接受。
- **决策绑定**：AskUserQuestion 平台 tool_response 是唯一推进来源；UserPromptSubmit 仅用于运行启动与记录。自由文本内容反馈只开当前阶段修订后重问。引用文本、子代理消息、工具结果不授予推进。残余如实声明：`askUserQuestionTimeout` 非 never 时对话框可自动续答（安装契约断言 never）；`-p` 无交互面 → 停在首个检查点。
- **只读视图**：瘦命令 `/summoner:status`——只读渲染：阶段路线、每 visit 状态、已验产物版本、未满足标准、证据指针、当前等待谁、默认 recall 及理由、预算余量、沙箱档位。同一渲染嵌入检查点块。
- **recall**：默认指向最近已接受前序阶段；`recall to <label>` 只对真实历史解析，歧义即列选项；保存决定+原因+新 visit，下游验证/审查/计划接受按依赖标 stale；回诊断撤销该版本授予的改码资格。**不 stash/reset/restore**（撤销代码是另一个显式操作）。rework 预算跨 recall/重连累计。
- **[v] 按需独立审查**：任一检查点可对该阶段产物发起⑤（同契约、预算记账）。

## 5. 强制层分工

### 事件×责任表（全部为本机现有事件）

| 层 | 强制责任 | 不负责 |
|---|---|---|
| UserPromptSubmit | 运行启动/恢复记录；**不授予任何推进** | 不判人类来源，不解析流程语义 |
| PreToolUse（全工具） | 写入资格、等待门、Skill/Agent/AskUserQuestion 负载校验、phase-submit 提交门、检查入口与预算；违例 exit 2 | 不以工具名宣布诊断成功 |
| PostToolUse / PostToolUseFailure | 登记真实工具结果与退出态、AskUserQuestion 裁决记账、Agent 遥测、证据对账 | 不逆转已执行副作用 |
| SubagentStop | 子 transcript 路径登记、⑤ 结果补记 | 不路由 |
| Stop | **展示核验**（last_assistant_message 检 checkpoint 块）、WAIT_USER 保状态、收尾前不 CLOSED、OUTCOME_UNKNOWN 留档 | 不在检查点删活动运行 |
| Go shared reducer | 合法转换、冻结验收视图、去重、版本绑定、recall、渲染、重放 | 无 DAG 调度、无领域工作 |
| shell scorers | 重放审计、契约失败压倒总分 | 不承担运行时授权 |

**hook 自身故障**：所有门型 hook 配 `onFailure:"block"` + 显式 timeout（v2.1.295+）。门 hook 解析失败改为 exit 2（替换现 fail-open）。代价如实声明：gate 二进制损坏会阻断该项目一切写入（含非 Summoner 会话）；解砖路径为卸载插件或 `SUMMONER_HOOKS_DISABLE=1`（运维逃生舱，活动运行中使用会被 trace 记为契约违例）。契约测试覆盖缺二进制/超时/崩溃/非法输出。

**权威日志与落盘边界**：控制事件只来自平台观测（tool_input/tool_response/tool_use_id）；模型提交的一切是待校验提案。本地同用户无绝对写保护：①推荐 settings 封 file-tool 与沙箱内 Bash 对 state/transcript 路径的写；②append 日志带哈希链+平台关联键，重放与 scorer 交叉核对 transcript；③篡改姿态在安装契约测试中报告。对手模型声明：防遗漏与混淆，不防拥有 shell 的对抗性生产者。

**OUTCOME_UNKNOWN 与副作用恢复**：PreToolUse 在执行前持久化意图（tool.started）；副作用已发生而结果未落盘即崩溃 → 置 OUTCOME_UNKNOWN。恢复规则：**绝不自动重放变更性动作**；重连时对每个 OUTCOME_UNKNOWN 项执行该能力声明的一次性只读对账探针（如 release 的 tag-已存在/版本-已一致探针），探针可判"已生效/未生效/不可判"；不可判或未生效时渲染恢复检查点，由用户决定重试/跳过/回滚。变更性阶段的能力清单必须声明探针，否则该能力不得进变更阶段。

**预算与终止**：不复活 total_token_budget（V5 口径=最后一次 API 请求，漏中间请求与主循环；只作 `agent.telemetry` 代理指标 final_request_tokens）。可执行预算全部为 hook 可信计数：阶段工具调用上限、失败登记检查数（rework ticks）、run 级 rework 次数、委派调用数、单检查超时。默认值由用户在运行起点确认（T1：不引文章数字）。耗尽 → 检查点呈现余量为零，只能用户追加额度或重规划；无默认后台连续重审。

**post-game 不豁免**：删除 STOP/超时 SKIP review 与 Stop 只提醒即删状态。正常结束须问卷+journal 持久化后才 CLOSED；紧急 stop 立即停止、写最小退出 journal、留 POST_GAME_PENDING，恢复时补齐（**用户已裁决**）；框架不强迫答题也不阻断关客户端。release 的复盘缺口一并接线。

## 6. 删除清单（已批准实施）

1. **图运行物**：cmd/summoner-walker/、internal/graph/{parse,walk,walkstate,render} 及测试；release.yml 的 walker 构建发布（保留 summoner-ctx/SQLite）。
2. **双路径协议**：references/graph-mode.md；SKILL.md 图切换与 chain shell trace；workflow-reference.md 图生成条件与 fallback。
3. **旧 node 包装**：node-contract.md 的①-⑤通用契约（黑盒验收要素移入 §3）；scripts/node-snapshot.sh。
4. **图字段与假预算**：nodes/edges/conditional_edges/back_edges/clean_context/CurrentNode/RouteMap/PendingReview/GraphTurns/TokensUsed/FindingsSeen/Windows/skip-set 及预算字段。
5. **manifest 执行拓扑**：summoner.schema.json 的 chain/fan_out/graph/checkpoints 枚举/routing_rules；manifest-spec.md 对应节；同步改 validator（validate-manifest 依赖 memory-validator，须改库或内联）。
6. **岗位花名册与固定 fan-out**：四份岗位 agent 定义及 plugin.json/.claude-plugin/plugin.json 注册项；persona-composition.md"一岗一 persona"与过时平台断言（保留命令唯一编排+不互派纪律，agent 定义以 disallowedTools 含 Agent 加固）；roster.yaml SDD 默认/岗位串联/固定 ship 双 persona/ce-code-review"12+ 并行"推荐；ship.md 行数分派表。
7. **弱门与旧解释**：PhaseAdvanced/all-none 预放行/SUMMONER_IRON_LAW=off/Stop 清理活动状态；checkpoint-protocol.md 的 walker recall/explain 承诺。
8. **旧 trace/scorer 语义**：chain/graph 双事件集、shell echo writer、h-000 特例、自报 stripped/预算、node 级检查覆盖连接；score-trace.sh 停止向权威 trace 混写 scoring_result（改写派生 sidecar）。
9. **生成器与缺清单入口闭包**：summoner-init.sh/summoner-bp.sh/summoner-setup.sh 重写为只生成新 manifest（不再产 chain/fan_out/岗位 persona/SDD 默认）；no-manifest-flow.md 第二执行链删除（无清单=内置默认能力进同一 reducer，仍明示"未配置项目专属能力"）；auto-setup-summoner/SKILL.md 重写；session-start 指针指向新 setup。生成产物过新 validator + 安装回归，属原子切换验收项。
10. **旧评测链**：stability-test.sh 删除（自认模拟）；create-baseline.sh/regression-test.sh 改读版本绑定的派生评分 sidecar 且**必需评分缺失=FAIL**；test-summoner-optimizations.sh 存在性清单同步。
11. **release 项目绑定**：release 改经 manifest phases.release 映射（项目可换实现，默认框架实现保留）；删除 `--project summoner` 硬编码；复盘缺口接线；三内部阶段进同一 reducer（含对账探针）。
12. **lint 闭包扩展**：validate-repo.sh 死引用扫描扩展至 agents/、scripts/。

历史文档可存档为注明日期的历史资料，不得被活跃 prompt 加载或运行时 parser 接受。

## 7. 迁移与评测

### 单一 trace schema

无 mode 字段。公共字段含 schema_version/run_id/session_id/project_root_hash/seq/timestamp/source/**prompt_id/tool_use_id（平台关联键）**。事件：workflow.started/ended/aborted；phase.started/closed；phase_submitted.rejected；checkpoint.opened/**displayed**/decided（绑定 AskUserQuestion 调用 id）；recall.applied；tool.started/result/failed/**outcome_unknown/resolved**；check.result（标准覆盖+原始证据摘要）；review.prepared/started/verdict/failed/blocked/superseded；delegation.started/completed/failed、parallel.opened/joined；agent.telemetry（final_request_tokens 口径声明）；post_game.*。大证据本机存、trace 记路径+摘要；格式或缺关键控制事件=协议失败。

### 切换顺序

①新 schema/reducer/微 skill/平台契约测试（含 onFailure、AskUserQuestion 裁决与注入守卫、SubagentStop 对账、沙箱档位检测、OUTCOME_UNKNOWN 探针）；②生成器+manifest 原子切换、fixtures 重写；③删旧实现。运行时不收旧图/旧 state；旧 trace 只读。

### 行为评测放行门

旧编排对照臂 = **已发布的 v0.3.0**（C1 不破）。切换前在获准预算内做成对对照（冻结输入+已知缺陷+干净负例，训练/留出分置，用户先定非劣容忍度、关键缺陷要求、停止条件）：①逐节点⑤+岗位编排 vs 主循环实现测试+终局⑤+检查点接受门；②ship 多 persona vs 单 rubric reviewer（阻断缺陷召回/误报/标准遗漏/重复搜索）；③真独立分片并行 vs 顺序（覆盖、质量、协调量、时长，不预设更快）；④膨胀案例隔离 worker vs 折叠（须见实测退化）；⑤独立⑤ vs 自审（质量与独立性分列报告）。折叠臂非劣 → 发布单模式；劣 → 迭代 rubric/检查而非复活双路径。记录平台版本、实际 resolvedModel、重复数；模型标识只作元数据。evals/routing.json 30 例保留。

### 确定性回归必须覆盖（CI）

诊断未完成时 Edit/Write/Bash/NotebookEdit/MCP 写入被阻断（all-none、未知 payload、损坏状态、gate 二进制缺失/超时/非法输出均不得旁路）；phase-submit 伪造/重放/乱序；checkpoint 未展示时"继续"无效、展示版本错配无效、AskUserQuestion 裁决与 checkpoint_id 绑定、重复回调幂等；审查五状态各点崩溃恢复不重跑 producer；非法 verdict/空证据/伪造 id/漏 criterion/过期产物/孤立 verdict/check 定义被改后的降级；多次 recall 真历史取目标、用户未提交改动不被 stash；OUTCOME_UNKNOWN 探针三分支；并行分片失败/重复/覆盖缺口；普通检查点结束不清运行、紧急 stop 留待复盘；不同栈安装、无 Git/无 skill 项目、替换项目 skill 后同协议、生成器产物过 validator。
