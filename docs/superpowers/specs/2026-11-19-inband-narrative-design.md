# In-band artifacts + session narrative — design rev2 (2026-11-19)

Rev2 incorporates the adversarial review round (4 BLOCKER / 8 MAJOR findings,
all adjudicated below) and adds §10 — deep adaptation to mattpocock/skills
(user directive 2026-11-19, after disabling superpowers and binding
antia-server define→grilling). Rev1's user rulings stand unchanged:
**A+B+C 全量；叙事存项目内 markdown；易失路径产物拒绝+内联。**

Baseline corrections found by review (rev1 was wrong about these):
- `JournalPath` is ALREADY project-local (`{project}/.summoner/journal/
  {run}.md`, reducer.go:2954) — there is no root switch to do. What is
  missing is per-decision sections and write safety.
- Contract tests already have the hermetic journal seam
  (`journalRootOverride`) — no test-layout change needed.

## 0. Problem (unchanged from rev1)

验收标准 wall（六轴每阶段刷屏）；tmp 路径产物被钉进检查点
（ship-r5.0.200 run 实证）；持久记录应是整理好的前因后果叙事而非快照。

## 1. Design principle (unchanged)

**The chat return IS the artifact surface.** 检查点消息承载阶段全部组织好的
信息；持久文件是可选项（仅项目内路径）；run 的因果叙事是一等人读文件。

## 2. (A) Render — information architecture

**受众裁决（rev3，用户反馈 2026-11-19：检查点里太多对获取当前任务状态
无用的数据）**：检查点是**用户做裁决的界面**——每行必须通过「这句话帮
用户决定继续与否吗」的测试。机器要的数据出渲染、进专用 JSON 视图。

瘦身清单（出渲染）：
- verification 环境行（tier/os/arch/runtime/defaults）→ 留状态与信封；
- 预算机器键值（phase_tool_calls=40,… · counters …）→ 人话一行
  `预算: 工具调用 4/40 · 回工 0/2 · 委派 1/5`，且**仅近限（≥80%）或有
  ask 搭车时显示**；
- 分页投影行（page i/n: …）→ 新 JSON 视图 `{"view":"pages"}` 供模型
  取精确页集（gate 照常校验）；渲染只留全量菜单；
- 「decide on the NEXT turn …」模型指令行 → 移入技能契约文档；
- 标准子行（scope/check/rubric 指针）→ 折进行内标签，不单占行；
- digest 截短 8 字符。

保留（有裁决价值或绑定契约）：nonce 行（显示绑定契约，压缩为一行）、
run 身份行、note + 背景/动作/结论、本阶段标准（一行一条：
`{id} {goal} → {expected} [method·必]`）、框架基线一行、产物、至今脉络
（cap 5）、菜单全量。

`RenderCheckpointMessage` regroups（左栏框布局不变）：

- **▶ 本阶段结果** — note + narrative 三段（§4），每段独立转义。
- **▶ 本阶段标准** — 非「精确默认定义」的标准全文。精确默认 = 与
  `DefaultReviewCriteria()` 中同 id 条目**全字段相等**（含 Required 与
  CheckID — 评审 #16）。
- **▶ 框架审查基线** — 一行六轴 id 串 + 提示语；仅当基线标准实际在册
  （review 工作流）才渲染（评审 #16：ops/release 不显示虚假承诺）。
- **▶ 产物（项目内持久路径）** — 持久路径 + 摘要；项目外路径渲染
  `⚠ 项目外/易失路径`（legacy 状态的诚实标记；§3 之后新提交不可能）。
- **▶ 至今脉络** — 取**最近 8 个已决 checkpoint**，按 checkpoint 数组序
  （= 真实决策顺序，tie 由数组位置打破 — 评审 #9），旧→新排列；
  `{phase}@{visit} → {decision} · {note 摘要}`。note **先按 rune 截断到
  40 再转义**（不得切开转义序列）；重复 phase 用 visit id 消歧。检查点
  副标注一句：提交文本会进入 journal 并可被 reviewer 引用（隐私预期）。
- 需求快照/计划范围/验证环境/预算/menu 不变。

## 3. (B) Artifact admission — 路径身份与准入

- **唯一解析器**（评审 #5/#6）：`ResolveProjectPath(raw) → (canonicalRel,
  err)`。规则：`filepath.Abs` → 对**已存在的最近祖先** `EvalSymlinks` →
  组件边界包含判断（`EvaledTarget` 必须在 `EvaledProjectRoot` 之内）→
  归一化为 project-relative 拼写。**包含优先**：目标在项目内即合法
  （含测试临时项目根 —— rev1 的 /tmp、/var/folders 禁用清单只适用于
  **项目外**绝对路径，修复评审 #15 的测试/评测冲突）。项目外路径才走
  禁用类清单（~/.claude、~/.summoner、系统 tmp、越界 ..）。同一文件
  的不同拼写解析后去重。`.summoner/` 自身不作为可提交产物（自指，
  评审 #12）。
- **一次解析、全程复用**（评审 #6）：gate 的 `stagePhaseSemantics` 在
  **哈希之前**对每个提交路径调用解析器，canonical 相对拼写贯穿
  staging/存储/新鲜度校验（修复现状 Join(ProjectDir(), abs) 双拼错位
  导致产物静默落入 planned scope 的 bug）。区分「尚不存在」与
  「读失败」：不存在 → planned scope；权限/IO 错误 → 拒绝并指明。
- **两处强制**：gate staging（exit-2 指令：内联或移入项目）+ reducer
  `SubmitPhase` belt（不信线缆）。
- **内联报告**取代 tmp 文件：`narrative {background, actions, outcome}`
  （§4）。无第二个无上限 blob。
- **框架自身写盘审计**：运行时只写 state-v2 / traces-v2 / journal ——
  无需删除的框架 tmp 保留；tmp 产物源自模型提交，结构性拒绝即可。

## 3.5 中间产物最小化（rev4，用户裁决 2026-11-19）

**原则：文件产物只属于最终交付物；中间过程住在叙事里。** 两条红线
（最终交付物质量不降、状态反馈与会话间同步不打折）由 §9 的专项测试
钉住。

- **交付类准入（deliverable-class）**：manifest v2 新增
  `artifact_dirs: [internal/, scripts/, configs/…]`（v1 翻译缺省 = 现行
  规则，零门槛升级）。声明后，文件产物只准落在声明目录——一次性
  分析报告/repro 脚本/草稿一律拒绝，其**要义内联进叙事**（复现命令 +
  观察到的输出摘要随 narrative 入 journal）。未声明 = 仅保留 rev3 的
  项目内路径规则（渐进采纳，不破坏现有项目）。
- **phase-submit 契约改写**：文件产物**可选**而非鼓励——「阶段记录 =
  note + 叙事四段（journal 持久化）；文件产物仅当属于交付物（代码/
  测试/配置/将复用的脚本）」。取消每阶段一份报告文件的隐含预期。
- **审查证据面转移**：标准对着**交付物本体**（代码/测试的钉死版本 +
  standards_docs）重推导；叙事=待验主张（prove-it 轴本义）；中间报告
  文件本就不再可提交，信封引用面自动收敛到真实交付物。质量论据：
  中间报告从来不是强证据，可执行测试与代码才是——证据面从「转述」
  升到「本体」。
- **状态反馈不折扣**：零文件产物的 run 一样渲染完整检查点（全部信息
  在带内）——专项测试：无文件 run 的 checkpoint 含结论/标准/脉络，
  review 照常对交付物验证。
- **会话间同步不折扣**：机器同步靠 state-v2 + traces（不动）；人/agent
  同步靠 journal（每裁决小节，交付物摘要随节落盘）；平台转录可 fork。
  三层各有其主，中间产物最小化不动这三层。
- **机器态非中间产物**：state/trace/journal 是同步与证据的骨架，
  明确排除在「最小化」之外；评测侧 scorer 基线同理。

## 4. Narrative schema (C)

`PhaseSubmission.narrative`（可选；空则渲染 `—`；phase-submit 契约推动
填写，中英自由）：

```json
{"background": "为什么做/前因", "actions": "关键动作与转折", "outcome": "结论与当前判断"}
```

- **统一散文预算**（评审 #8）：narrative 每字段 ≤400 rune；`summary`
  ≤400 rune；`ProposedRequirement.content` ≤600 rune。超限拒绝并报上限。
  另设**转义后字节预算**：narrative 三段合计 ≤4KB、渲染整消息
  ≤24KB（超限报「拆分或落为项目内文件」）。保持整串相等校验不变 ——
  永不放宽格式来救排版。
- **提交时归一化**：CRLF→LF、去尾空白后进入哈希/持久化/渲染（同拼写
  单一形式）；渲染仍走 escapeScalar。

## 5. Journal — 项目内 markdown（前因后果的持久层）

- 路径不变：`{project}/.summoner/journal/{run_id}.md`。框架按需建目录；
  不管理项目 .gitignore。
- **追加式阶段小节**，写在 `applyDecision` 决策核心（所有
  RecordDecision* 变体共用 —— 评审 #2）：

  ```markdown
  ## <phase-id>@<visit> — <timestamp>
  - 裁决: <CONTINUE|SKIP|…>（checkpoint <id>）
  - 结论: <note>
  - 背景: <narrative.background>
  - 动作: <narrative.actions>
  - 结果: <narrative.outcome>   ← rev1 漏项，补齐（评审 #3）
  - 产物: <durable paths @ 短摘要 | —>
  ```

- **崩溃安全投影协议**（评审 #2）：小节身份键 =
  `(run_id, checkpoint_id, decision, tool_use_id)` + 期望内容摘要。
  流程：①状态先记 `journal_pending`（含期望摘要）→ ②追加写小节（O_APPEND，
  单次 write，末行哨兵 `<!-- summoner:section <digest> -->` 收口）→
  ③状态记 `journal_committed`（回执：路径+摘要）。**重放幂等**：回执在则
  跳过；哨兵在但回执缺（进程死于写后保存前）→ 校验摘要一致补回执，
  不一致则拒绝并指明撕裂；写失败 → 决策拒绝并给出修复指引。
- **停止不对称（closed-for-work, open-for-exit — 评审 #1）**：journal
  追加失败时，CONTINUE/SKIP/DONE/RECALL/REVIEW 一律拒绝（修复后重试）；
  **STOP 放行** —— 落 POST_GAME_PENDING + `journal_debt` 回执（欠账
  陈述进状态与 status 渲染），恢复会话首先补写。绝不为停止设置存储
  人质。启动时（首个检查点前）在渲染中披露 journal 路径。
- **终局写手改为追加**（评审 #3）：`WriteMinimalExitJournal` 与 post-game
  journal 观察一律**追加小节**（各自带 digest 哨兵），永不整文件覆写；
  post-game-review.md 的「关闭前可自由重写」条款改为「追加；已提交
  小节受哨兵保护」。
- **渲染不读 journal** —— 至今脉络源自状态（§2）；journal 是人读层。

## 6. Review path

- `SemanticEnvelope` 增 **versioned narrative 投影**：新字段全部
  `omitempty`，且 envelope 内容摘要函数按「无叙事字段时与现行逐字节相等」
  构造（评审 #11：在途 0.5.1 review 的重铸/胶囊等值不受影响；serialized
  legacy PREPARED/RUNNING/result fixture 进测试钉住）。
- **冻结的是小节引用而非整文件**（评审 #12）：[v]/终局冻结
  `(checkpoint_id, section_digest)` 引用列表（只含已接受 visit 的段落）；
  dispatch/conclusion 只校验被引小节哨兵摘要 —— 后续追加不失效、
  不相关小节（recall/skip 史）不进证据。journal 路径不可作为普通文件
  产物（§3）。reviewer 侧：叙事散文是**未信生产者主张**，重推导仍须
  对着持久产物/标准文档。
- CheckScope.readable 增加 journal 单文件路径。
- **不新增 trace 事件类型**（评审 #13）：journal 回执搭
  `checkpoint.decided` payload 顺车（`journal: {path, digest, status}`），
  词表/镜像 scorer/38-type 断言零改动。

## 7. Backward compatibility

- `CheckpointContentDigest` 维持 v1 投影不变；叙事通过**独立 v2 摘要**
  （`narrative_digest`，叙事为空时与 v1 同字节）参与显示绑定 —— 在途
  已显示的 0.5.1 检查点绑定不失效（评审 #10）。`rerenderCheckpoint`
  等替换路径显式复制 narrative 字段。
- 旧状态（路径产物、无叙事）照常加载；§2 标记诚实渲染；其上决策照常
  （journal 小节叙事为 `—`）。POST_GAME_PENDING 旧 run 的欠账 journal
  路径不变。
- phase-submit 载荷新字段被旧 gate 忽略（permissive Unmarshal 已核实）；
  新 gate 接受旧载荷（narrative 可选）。
- trace fixtures/scorers 不涉渲染字节（0.5.1 已验证）不变。

## 8. Non-goals

不变：无转录导出/分叉特性；不改决策语义/菜单/预算/两轮显示/nonce/分页；
无 SQLite 叙事表；不管理项目 .gitignore。新增：不重写历史小节（追加
协议只增不改）；不做跨 run 叙事合并。

## 9. Test plan (TDD, RED before GREEN)

1. 解析器：包含优先/组件边界/符号链接祖先/双拼写去重/项目外禁用类/
   不存在-vs-IO错误（`narrative_test.go`）。
2. gate：解析先于哈希（绝对项目内路径不再静默落 planned）；reducer
   belt 表驱动。
3. 散文预算：各字段上限、转义字节预算、CRLF 归一化。
4. journal：小节字节格式（含 outcome）；pending→committed 幂等重放；
   哨兵撕裂恢复；追加失败拒绝非 STOP 决策；**STOP 放行 + journal_debt**；
   终局写手追加不覆写（旧覆写测试反转）。
5. Stop hook 优先级（评审 #4）：**整串规范匹配先于 stopAck** —— 含
   "Stopped/Halt/停止" 字样的合法渲染必须绑定显示而非被误读为停机
   应答；真实自由文本停机应答仍走 ack。全帧回归用例覆盖每个叙事字段
   与历史行。
6. 摘要兼容：v1 相等性（无叙事）；v2 叙事摘要；替换检查点保 narrative；
   legacy envelope fixture 重铸等值。
7. 渲染：分组/脉络排序（数组序+cap 8+rune 截断）/legacy 标记。
8. gate method_skills（§10.1）：方法技能不 auto-open、不拦截（phase
   开放中）、WAIT_USER 仍拦。
9. contract：post-game「journal 不存在」断言反转为「阶段小节存在且
   未被终局覆写」；fresh binaries 先行（make -C hooks build）。
10. 中间产物最小化红线（§3.5）：(a) 零文件产物 run 的检查点仍完整
   （结论/标准/脉络全在带内）；(b) review 对交付物本体验证、叙事仅作
   主张（prove-it）；(c) 跨会话恢复只依赖 state+journal——进程重启后
   status 渲染、至今脉络、journal 三者一致；(d) artifact_dirs 声明前后
   的准入差异表。
11. Full battery；0.6.0 发布另经用户确认。

## 10. Deep adaptation — mattpocock/skills × summoner

用户已停用 superpowers；mattpocock 为手法层（antia define→grilling 已绑）。

### 10.1 冲突判定（机制级）

| 冲突 | 等级 | 处置 |
|---|---|---|
| 手法技能在 ACTIVE-无开放-phase 时被 gate auto-open 下一 phase（ruling 3） | **真冲突** | §10.2 method_skills 修复 |
| to-spec→to-tickets→implement 链与 /summoner:new 平行轨道 | 轨道竞争 | §10.3 票据接驳（不绑 slash-only 技能） |
| git-guardrails 装 PreToolUse 钩子与 gate 并存 | 无协议冲突 | 独立钩子，deny 优先，defense-in-depth；文档化 |
| wizard 交互式命令 | 无新冲突 | WAIT_USER 本就拦 Bash；ACTIVE 内交互挂起是平台通用问题 |
| Matt code-review vs 信封审查 | 分工不冲突 | run 内走 [v]/终局信封；分支审查 run 外用 Matt 版 |
| tdd/diagnosing-bugs/prototype/research 等 | 无冲突 | phase 开放中 gate 放行，作为阶段内手法 |

### 10.2 method_skills（gate 语义 + manifest 声明）

v2 manifest 新增 `method_skills: [tdd, diagnosing-bugs, prototype,
research, codebase-design, domain-modeling, grilling, …]`（项目声明其
手法层）。gate 对命中技能：ACTIVE 期间**永不 auto-open**（摘除 ruling 3
副作用）；phase 开放中放行为阶段内手法（照常计入预算、追
`method.use`）；WAIT_USER 照拦（检查点纪律不变）。框架默认集即上表
（v1 manifest 经兼容翻译同样生效）。

### 10.3 票据原生计划（to-tickets 接驳）

`to-tickets` 产出的 tracer-bullet ticket 文件（每票一文件 + 阻塞边）
是天然 plan 产物：plan phase 的 provider（如 antia-docwriter）可产出
票据目录；implement 阶段 phase-submit 以票据文件为产物引用（项目内
路径，§3 准入 ✓），checkpoint 冻结其版本，验收按票。不绑
`disable-model-invocation` 技能为 provider —— 用户手动 `/to-spec`、
`/to-tickets` 的产物由后续 phase 消化。

### 10.4 Standards/Spec 双轴与 verification_spec 对齐

v2 manifest `verification_spec.standards_docs: [path…]`：声明项目标准
文档（Matt code-review 的 Standards 轴来源）→ 进 envelope readable
scope；六轴默认映射：spec 轴 = 项目 staged 标准，standards 轴 = 由
standards_docs 派生的机械+判断标准。信封审查因此拿到与 Matt 版同源
的标准依据，双轨 rubric 收敛为一。

### 10.5 出厂建议

summoner 文档（SKILL.md 路由节）写明分层：`/summoner:*` 唯一流程入口；
方法技能是阶段内手法；分支审查/PR/原型在 run 外自由使用。antia-server
作为参考实现把 method_skills 写进 manifest。
