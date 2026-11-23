# 自适应检查点 — 设计（0.7.0 第一部分）

- 状态: rev11（S0–S6 已实现；ship 审查第 1–4 轮的发现均已修复，见
  §9–§12；分类器的缺省策略（未知动词判读/判写）正由对抗小组裁决，见 §12
  末。rev5 = 评审闭合 + D5.5 已裁决）
- 日期: 2026-11-23
- 裁决: 用户 2026-11-23 — ①结论必须详细；②一轮问完，不再让用户打
  continue；③明确的环节不要公式化地问，效率要提高。
- 事故输入: 11201 会话复盘（`.superpowers/sdd/2026-11-23-incident-11201.md`，
  缺陷 A–J）。
- 基线: dfa4075（0.6.1 分支顶端）。本文取代 inband-narrative 设计中的
  「渲染瘦身」部分；其 journal 部分保留为后续切片。
- 标注约定: 「评审指出」= 独立评审给出的代码事实（附行号），实现时先
  核实再用。
- 评审状态: rev2 → 六项修订；rev3 → 复核判定两项闭合、四项未闭合，
  另报 B1–B7；本文逐项关闭，闭合方式即复核给出的修法。实现按切片评审，
  每片对照代码核实本文引用的事实。

## 1. 目标 / 非目标

**目标**
1. 检查点先交付结论：结论、证据、怎么查的、没查清、影响；后续检查点
   携带前面各阶段的结论（经过）。
2. 用户看到"解释 → 裁决框"连续出现，不需要打 continue。
3. 明确的环节自动通过并留痕；只在需要用户判断处停。
4. 裁决被拒时模型能看见原因并知道下一步怎么做。
5. 修复事故中证实的机制缺陷（D5）。

**非目标**
- 不动 Phase 1 铁律本身；写类判定的范围待用户裁决（D5.5）。
- 不动预算机制、变更闭环、recall、post-game 问卷。
- 不放行 TaskStop；不把展示绑定迁到 gate 或 transcript 时序上。

## 2. 事故对照

| 缺陷 | 决策 |
|---|---|
| A 结论一行 + 60 行噪音 | D1 |
| B Stop 钩子逐字节相等 | D2 |
| C 每检查点要用户打 continue | D2 |
| D 用户文字被无视、原题重问 | D4（根因: 拒绝对模型不可见） |
| E 诊断自由文本，无"未查清"要求 | D1 |
| F 只读命令误判写类 | D5.3 |
| G 审查员 status 枚举大小写 | D5.1 |
| H status `{}` 跨会话歧义 | D5.2 |
| I TaskStop 等未建模被拒 | 本版不放行 TaskStop |
| J 复现阶段被挪用、零改动 | D1 + D3 |

## 3. 设计决策

### D1 结论优先的检查点渲染

- **结构化结论**（PhaseSubmission 新增，全部可选）:
  `conclusion { summary, evidence[], approach, open_questions[], impact }`、
  `outcome: confirmed | failed | unclear`。
- **不强制、但缺失可见**: 缺省 `outcome` 视为 `unclear`；缺失部分渲染为
  "未提供"。例外: `diagnosis` 提交必须带 `summary` 与 `approach`。
  `open_questions` 非空 ⇒ `outcome` 强制 `unclear`。
- **入摘要**: conclusion 非空时并入 `CheckpointContentDigest`；字段为空的
  旧 run 摘要不变。
- **渲染顺序**: `经过` → `结论` → `证据` → `怎么查的 / 怎么做的` →
  `没查清 · 需要你判断` → `影响` → `请决定`。
- **上限**（评审指出: 无字符上限则行数不可断言）: 经过 ≤ 6 行；evidence
  ≤ 4；open_questions ≤ 3；每项 ≤ 120 字符；summary、approach ≤ 200
  字符；impact ≤ 120 字符；超出写"…另 N 条，见 summoner-status"。
- **菜单**: 只渲染第 1 页（评审核实: 恒含 continue 与 stop）的逐字标签 +
  nonce，**并保留一行导航提示**——"更多选项（recall / review / 回城目标）
  回复 more: 翻页，或用 summoner-status 查看全部"。评审指出: recall 与
  review 在第 2 页、回城目标页在最后，全部移走会让用户无法发现它们。
- **行数口径**: 以规范渲染的换行数计。按上述上限逐节相加，零空行为 33
  行，每节一个空行约 41 行（评审核算）；上限定为 **≤ 42 行**，由契约
  测试在"最大合法提交"上断言。自由文本的显示折行由字符上限约束。
- **移入 `summoner-status` 默认视图**: 完整验收标准、产物 digest、需求
  快照、计划范围、环境行、预算机器键、页码投影、recall 目标全列表、
  review retry、给模型的指令行。
- 评审指出: 现有测试与夹具里约 14 处 diagnosis 提交（10 个文件）会因
  强制字段被拒——列入 S1。

### D2 解释后直接发问（Stop 钩子）

改动（`hooks/stop/main.go`）:
1. 合资格检查由整条消息逐字节相等改为 **containment**: 最终消息包含
   「规范渲染去掉尾行」的全文。
2. containment 命中 → 绑定展示 → **拦停**（`decision: block`），反馈
   "检查点已展示，现在用 AskUserQuestion 发起裁决"。
3. 已展示且未裁决 → 先过停止确认（stop-ack），否则同样拦停要求发问。
4. 已裁决 → 放行。
5. containment 未命中 → 现状不变。

依据与边界:
- `decision: block` 是官方文档规定、且 11201 现场六次证实能让模型继续的
  机制。
- 宿主对连续拦停有 8 次上限，每次工具调用归零（官方原文）；发问是工具
  调用，每个检查点只消耗 1 次。到顶后回合结束，用户可打字、可选 stop、
  可用 SUMMONER_HOOKS_DISABLE=1。
- 评审核实时序自洽: gate 的发问校验要求 `Displayed`（gate.go:1556-1561），
  `Displayed` 只由 Stop 钩子写 → 模型结束回合 → Stop 绑定并拦停 → 模型
  发问被放行。
- 不变: `Displayed` 只由 Stop 钩子写；同轮作答一律拒绝；nonce 入题；
  选项 = 一页菜单。
- 测试: 旧 nonce 的块与新块并存时只绑新块。
- **已知代价**（评审 B7）: stop-ack 按关键词匹配，模型收尾文字里出现
  stop / halt / 停止（如"服务在收到 SIGTERM 时停止"）会被当作停止确认而
  放行，回合停在"已展示未发问"，需用户打字。现有代码在未展示分支已
  承担此代价，本设计扩大到已展示分支。
- 需同步的代码内旧文案（模型实际读到的面）: `stop/main.go` 的 block 理由
  "整条消息必须等于该渲染"；`render.go` "╠═ 下一轮裁决"与"decide on the
  NEXT turn"；`gate.go:1561` "…END the turn … ask … on the NEXT turn"；
  `gate.go:1576` "copy … from the render's paging note"（改为指向
  summoner-status）；`skills/phase-submit/SKILL.md` 补"贴完即结束回合"。

### D3 自适应停问

**自动过 = 照常创建检查点记录，由新的 reducer 入口 `AutoPassCheckpoint`
当即接受**（不进入用户等待）。评审指出的三处代码事实与本设计的对应:
- 现有 `RecordDecision` 强制要求已展示（reducer.go:1428-1430）——所以
  新开入口，复用接受路径的内部逻辑（结果置 accepted、验收冻结
  `freezeAcceptance`、新访问桶；reducer.go:650-661、861-868、883-899），
  不做展示前置。`AcceptedBy = "auto-pass"`；**不发**带伪造 tool_use_id 的
  `checkpoint.decided`（trace 协议规定该 id 必须是真实的 AskUserQuestion
  调用），只发 `checkpoint.auto`。
- `checkpoint.auto` 今天不在 trace 白名单（trace.go:119-135；未知类型
  `AppendEvent` 报错，而 gate 的提交路径是"先提交、再追加、追加失败即
  拒"）——先入三份白名单: `trace.go`、
  `scorers/deterministic/schema-check.sh`、`references/trace-protocol.md`。

| 阶段收口 | 自动过的条件 | 否则 |
|---|---|---|
| context | 下述全部条件 | 检查点 |
| reproduce | 同上，且 outcome=confirmed | 检查点 |
| verify / regression | 同上，且 outcome=confirmed | 检查点 |
| diagnosis、fix | 永不自动 | 检查点 |
| 其余阶段 | 永不自动（本版） | 检查点 |

**"下述全部条件"**（逐项 AND）:
- outcome=confirmed 且 evidence 非空；`open_questions` 为空；
- **`cp.ExtendAsk == nil`**（评审 B1: 预算耗尽时开的检查点带追加追问，
  自动过会吞掉用户的预算决策，并把 run 送回"仍耗尽、无法工作"的状态）；
- **本次提交的检查结果无 FAIL**（机械证据与"已确认"的自述矛盾时停）。

- **审查结果**（单独切片）: 结果检查点由 `RecordReviewResult` 开，不在
  提交站点，且审查常是末阶段。实现为第二站点: COMPLETED+PASS 且证据齐 →
  自动裁决，路线有下一阶段则继续，否则走 done 进入 POST_GAME_PENDING。
  若需要新的转移路径，**本版放弃该行**。
- **续跑兜底**（评审指出原条件不可满足: 提交后必有 `tool.result`、
  `check.result`，自动通过事件永远不是最后一条）:
  ① `summoner-status` 默认视图明写"阶段 X 已自动通过 → 立即进入 Y"；
  ② Stop 钩子: 活跃 run 的**最近一条生命周期事件**（phase.* / checkpoint.*）
  是 `checkpoint.auto` 且其后没有 `phase.started` → 先过 stop-ack，再拦停
  要求继续。**"一次"的落点**: run state 记 `AutoPassNudgeFor = <检查点 id>`，
  每次自动过至多拦停一次（Stop 在活跃 run 上现状从不拦停，条件若不收敛
  会无限拦）。
- 提问次数: 诊断、改动验收两处为确定；审查结果是否为第三处取决于上述
  切片。

### D4 裁决被拒对模型可见

- **根因**（评审 Critical，官方文档印证）: PostToolUse 拒绝只写 trace 与
  stderr 并以 0 退出；"Claude never sees stderr from a hook that exits 0"。
- **改法**: 裁决被拒时向 stdout 输出
  `{"decision":"block","reason":<原因与下一步>}`。官方语义: block 把 reason
  加在工具结果旁，Claude 仍看到原始输出，且不结束回合。契约测试断言**整段
  stdout 可解析为单个 JSON 对象**。
- **文案必须披露同一次回调里已生效的部分**（评审 B2: 拒绝前可能已记
  drop、已追加预算）: 列出"已记录的 drop / 已追加的预算；裁决本身未记录"，
  并写明**重问时不再携带已结算的搭车题**——预算追加只按 tool_use_id
  去重，重问会追加两次。
- **下一步指令必须是该检查点上合法的路径**（评审 B3: WAIT_USER 下阶段已
  关闭、写被拒；并非每个检查点都有 recall）: 先做文字要求的只读调查并
  汇报；本检查点含 recall 选项时，请用户选 recall 重开本阶段后重新提交；
  需求调整走 summoner:change；两者都没有时只能 stop。`more:` / `back:`
  是翻页，原样重问目标页。
- 文字本身仍不直接推进 run。WAIT_USER 下写 scratch 被拒本版不改。

### D5 机制缺陷修复

1. **审查员枚举**: `ValidateCriterionVerdicts` 对 status 做 TrimSpace +
   ToLower；`agents/reviewer.md` 写明 `"status": "pass|fail|blocked"`。
   框架侧校验失败渲染为"框架未能完成审查：<原因>；对你的影响：改动尚未
   被审查"。
2. **status 会话定位**: SessionStart 钩子向 `CLAUDE_ENV_FILE` **追加**
   `export SUMMONER_SESSION_ID='<id>'`；`summoner-status` 无 session_id 时
   读之，并按名列出项目内其他活 run；读不到则退回现状。**已知残余**
   （评审 B5）: 官方对 CwdChanged 的说明是写入该文件的变量"持续到下一个
   CwdChanged 事件被清除"，`cd` 之后该变量很可能不在——依赖退路，不作为
   验收断言。
3. **写类判定**（同一谓词的两个调用点 gate.go:237、:578；崩溃扫描的
   read_class 来自 :237 产生的效果——放宽不当即静默漏放）:
   - 逐个重定向判定；词法处理单引号、双引号、`$'…'`、未引用反斜杠；
     heredoc 正文保守判写。
   - **`/dev/null` 豁免适用于所有以文件为目标的操作符**（`>`、`>>`、`>|`、
     `&>`、`&>>`、`>&`、`<>`）: 目标去引号后恰为 `/dev/null` 即不算写。
     **文件描述符复制不是文件目标**: `>&` 后跟单个数字（`2>&1`、`1>&2`）
     不算写。其余目标一律是写（`> /dev/null > f`、`>$NULL`、`>~/dev/null`
     均为写）。
   - 动词通道与重定向通道独立。
   - 进 characterization 测试的边界: 事故 3 条实测命令；
     `cmd 2>/dev/null 1>&2`、`>>/dev/null`、`&>/dev/null`、`</dev/null`、
     `$(cat f > /dev/null)`（只读）；`$(cat f > /dev/null > g)`、
     `>/dev/null>g`（写）；`echo "a > b"`、`[[ a > b ]]`、`$((1 > 2))`、
     注释与 heredoc 正文里的 `>`（只读，现状为过拦）；
     `echo "$(cat "a>b")"`（词法边角）。
   - `isWriteClassCommand` 改为同时返回命中的规则，拒绝消息注明。
4. **TaskStop**: 本版不放行。
5. **写类判定的范围 — 已裁决 (a)**（用户 2026-11-23: 都按建议）。诊断期
   把以下全部判为写类（`find -exec` 一律判写）:
   - 带内联代码的解释器（`python3 -c`、`node -e`、`perl -i`、awk 的
     `system()`）；
   - 构建与运行（`make`、`go run`、`npm i`、`npx`、`pip install`）；
   - 远程与复制（`ssh`、`rsync`、`scp`）、下载落盘（`curl -o`、`wget -O`）、
     解包（`tar -x`、`unzip`）；
   - 表外的 git 写子命令（`restore`、`am`、`bundle create`、
     `filter-branch`）；未知 git 子命令改判写（不再默认只读）；
   - `bashwrite.go:32-36` 的 residual 注释按新范围重写。
   铁律文案同步: 诊断期的可执行面只剩只读动词 + LSP。

### D6 协议与文档同步

`references/checkpoint-protocol.md`、`references/single-loop.md`、
`references/workflow-reference.md`（评审核实与本设计相反的是 :70 "free
text never advances"、:73 "PASS 仍留 WAIT_USER"、:77 Red Flag "Advancing
past a checkpoint without a recorded AskUserQuestion decision"）、
`skills/phase-submit/SKILL.md`、8 个 command 的 Loop 行、D2 列出的代码内
字符串、`AGENTS.md` 规则 5 与 Testing 节（`go test -count=1`）、
`references/trace-protocol.md`（新增事件）。CHANGELOG 0.7.0。

## 4. 已弃选项

- 展示绑定迁到 gate: gate 看不到模型消息正文。
- 裁决框承载完整结论: 框内文字上限未实测。
- "最近活跃会话"指针: 并发下后写者覆盖。
- 强制所有阶段结构化结论: 打断全部既有提交方。
- 用 additionalContext 替代 Stop 的 block: block 是文档规定且现场证实。
- 自动过不建检查点记录: 丢失验收冻结等副作用。
- 自动过走现有 `RecordDecision`: 该入口强制要求已展示。
- 页码投影全部移出渲染且不留提示: recall 与 review 对用户不可发现。

## 5. 风险与缓解

- **R1 自动过被滥用**: 需 confirmed 且有证据；`ExtendAsk` 存在或本次有
  检查 FAIL 即停；diagnosis / fix 永不自动；经过 可见、可 recall。
- **R2 containment 放宽**: 比较规范块去尾行全文；结论入摘要；nonce 绑定。
- **R3 拦停把用户锁住**: 已展示分支先过 stop-ack；8 次上限且工具调用
  归零；SUMMONER_HOOKS_DISABLE 可解。代价: stop-ack 关键词误报（见 D2）。
- **R4 写类规则放宽引入漏放**: 规则写死 + 逐边界测试；铁律与预算豁免两处
  调用同谓词，测试同时覆盖。
- **R5 拒绝可见化**: 只增不改；文案披露已生效部分，禁止重问已结算的
  搭车题。
- **R6 续跑兜底不收敛**: 每次自动过至多拦停一次（run state 标记）。
- **R7 兼容**: run state 新增字段均可选；旧 run 摘要不变；新事件入白名单。

## 6. 验收标准（可断言；接缝 = hook 二进制 stdin/stdout、shared 公开 API）

1. fix run 提交 context 后: 状态 ACTIVE、无待裁决检查点、检查点记录
   已接受且 AcceptedBy=auto-pass、trace 有 1 条 `checkpoint.auto`、无
   `checkpoint.decided`。
2. reproduce `confirmed`+evidence → 自动接受；`failed`、缺省 → 待裁决。
3. 检查点带 `ExtendAsk`，或本次提交含检查 FAIL → **不**自动过，待裁决。
4. Stop 钩子: "解释 + 规范块" → 绑定且输出 block 要求发问；仅尾行长短
   不同 → 同上；缺规范块 → block 要求渲染；已展示 + 停止确认 → 放行；
   已裁决 → 放行；旧 nonce 块与新块并存 → 只绑新块。
5. gate: `MarkDisplayed` 置位后，带 nonce 且选项等于第 1 页的
   AskUserQuestion 放行（裁决框是宿主行为，不在此断言）。
6. 诊断检查点在最大合法提交下 ≤ 42 行（按换行计）；不含审查标准 /
   环境行 / 预算机器键 / 页码投影；含一行导航提示。
7. 裁决被拒（自由文字、非法选项）时 PostToolUse 的**整段 stdout** 可
   解析为单个 JSON 对象，含 `decision: block`、下一步指令，以及本次已
   生效部分的披露。
8. verdict status `BLOCKED` 不再被拒。
9. D5.3 列出的每个边界: 只读形态诊断期放行，写形态仍拒；D5.5 裁决
   (a) 的每个类别诊断期被拒（`python3 -c "…"`, `make`, `go run`, `ssh`,
   `rsync`, `curl -o`, `npm i`, `tar -x`, `git restore`, `find -exec`）。
10. SessionStart 在 `CLAUDE_ENV_FILE` 存在时向其追加 export 行；
    `summoner-status` 无 session_id 时按该变量解析 `{}`；变量不存在时
    退回现状。
11. `checkpoint.auto` 被 trace 写入、`schema-check.sh` 与
    `trace-protocol.md` 三处白名单同时接受。
12. 续跑兜底: 自动通过后模型结束回合 → Stop 拦停一次；同一自动过再次
    结束 → 放行。
13. 全电池 `-count=1` + lane CI 绿。
14. 评审动作: 每个切片评审时对照代码核实本文引用的事实。

## 7. 接缝与切片

- S0 `checkpoint.auto` 入三份白名单（S3 的前置）。
- S1 结构化结论 + 摘要 + 渲染重写 + 上限 + 导航提示 + 既有 diagnosis
  夹具更新。
- S2 Stop 钩子 containment、发问拦停、停止豁免 + 代码内文案。
- S3 自适应停问（`AutoPassCheckpoint` 入口、条件、续跑兜底）。
- S3b 审查结果自动裁决（第二站点；不成立则放弃该行）。
- S4 裁决拒绝可见化 + 披露 + 合法路径指令。
- S5 缺陷修复 D5.1–D5.3（D5.5 视用户裁决）。
- S6 文档同步 + 全电池 + ship 审查。

每片 RED 先行；`make build release` 后跑 contract；改脚本或 fixture 必
`-count=1`；**修好冒烟测试的配置污染（task #42）之前不跑
`install-smoke-test.sh`**。

## 修订记录

- rev1: 初稿。
- rev2: D2 按 stop/main.go 改写；D1 缺省可见；D5.2 改用 SessionStart +
  CLAUDE_ENV_FILE；TaskStop 移出；补接缝。
- rev3（评审六项）: D4 改为 stdout 可见通道；D3 保留检查点记录、审查
  结果独立切片、补续跑兜底；D1 定上限并入摘要；D2 加停止豁免与代码内
  文案；D5.3 写死规则并改签名；验收改写为可断言；新增 D5.5。
- rev5: D5.5 用户裁决落定——诊断期解释器/构建/远程/解包/表外 git 写子命令/
  find -exec 一律判写；铁律文案与验收同步。
- rev4（复核: ②③⑤⑥ 未闭合 + B1–B7）: D3 新开 `AutoPassCheckpoint`
  入口、事件入三份白名单、自动过排除 ExtendAsk 与检查 FAIL（B1）、兜底
  条件按生命周期事件并定义"一次"；D1 行数口径与字符上限、上限改 ≤42、
  保留导航提示（B4）；D5.3 明确 /dev/null 豁免适用于所有文件目标操作符、
  补边界清单、更正调用点数；D4 文案披露已生效部分并禁止重问已结算搭车题
  （B2）、下一步指令按检查点是否有 recall 分情况（B3）；D5.2 记 cd 残余
  并删去对应验收断言（B5）；D6 更正行号（B6）；D2 记 stop-ack 误报代价
  （B7）；D5.5 事实更正为"只认短动词表 + `>`"。

## 8. 实现记录（rev6）

S0–S6 已按 §7 落地于分支 `adaptive-checkpoints`（基线 `dfa4075`）。验收标准
与测试锚点（全部在 `-count=1` 全电池下运行）：

| 验收 | 锚点 |
|---|---|
| 1 | `hooks/pretooluse-gate/autopass_test.go:TestConfirmedContextSubmissionAutoPasses`；`hooks/contract/adaptive_e2e_test.go:TestAdaptiveAutoPassOverBinaries` |
| 2 | `autopass_test.go:TestReproduceAutoPassesOnlyWhenConfirmedWithEvidence`；`shared/autopass_test.go`（reducer 层） |
| 3 | `TestExhaustionCheckpointNeverAutoPasses`、`TestFailedCheckStopsTheAutoPass` |
| 4 | `hooks/stop/main_test.go`（绑定即发问、尾行任意、解释在前、旧 nonce 并存、停止豁免）；`TestAdaptiveExplainThenAskOverBinaries` |
| 5 | `hooks/pretooluse-gate/menu_test.go:TestAskSameTurnDeniedBeforeDisplay`（display 后放行） |
| 6 | `shared/render_layout_test.go`（≤42 行、移出机制内容）；`TestAdaptiveRenderLeadsWithConclusionOverBinaries` |
| 7 | `hooks/posttooluse/rejection_visibility_test.go`（整段 stdout 单 JSON、披露、路径）；`TestAdaptiveRejectionIsVisibleOverBinaries` |
| 8 | `shared/review_enum_test.go`；`contract/adaptive_seams_test.go:TestAdaptiveReviewerEnumCaseOverBinaries`（二进制级：事故里的大写拼法走到 REVIEWED/PASS） |
| 9 | `hooks/pretooluse-gate/writeclass_test.go`（边界表）、`writeclass_probe_test.go`（看门狗 + 6000 输入 fuzz）、`writeclass_review_test.go`（审查轮补的绕过与误拒）、`contract/finalwave_e2e_test.go:TestFinalWaveWriteBarrier`（二进制级） |
| 10 | `hooks/session-start/env_export_test.go`；`summoner-status` 的 env/回退/other-runs 测试；`TestAdaptiveSessionExportOverBinaries`（二进制级：环境文件、按会话限定、过期 id 回退） |
| 11 | `shared/trace_test.go`（39 型）、`schema-check.sh`、`trace-protocol.md`；10 个真实链 fixture；`TestAdaptiveScorerMirrorsTheAutoPassPolicy`（评分器的策略集 = reducer 的策略表） |
| 12 | `hooks/stop/autopass_test.go`（一次、已开下一阶段则不再、停止豁免）；`TestAdaptiveAutoPassOverBinaries` |
| 13 | 本地全电池（hooks 10 包、root、48 fixture × 5 评分器 = 285 条断言、validate-repo）绿；lane CI 见发布记录 |
| 14 | 每片的 ship 审查对代码核实（见 §修订记录、§9 与审查报告） |

### 设计之外的改动（实现中发现，含理由）
1. **S0**：`user-gate-check.sh` 一并认识 `checkpoint.auto`。设计只列三份白名单，
   但自动通过的检查点是 `opened` 后无 `displayed`——评分器会判 MISSING，真实
   0.7.0 trace 一评分就红。策略镜像与 reducer 一致：仅四阶段、仅 confirmed、
   一次、不得与 displayed/decided 并存。
2. **S3**：自动通过时重读预算。`ExtendAsk` 在检查点打开时定格；一次 FAIL 会在
   其后再把 failed-check 预算打满，只看 `ExtendAsk` 会把 run 放进“耗尽、无法
   工作”——正是评审 B1 要防的状态。条件取并集。
3. **S3b 放弃**（设计的放弃条款）：自动 done 会让模型在 reviewer 返回时面对
   POST_GAME_PENDING 而非检查点，需要新增 post-game 问卷续跑提示；且
   `checkpoint.auto` 需为非阶段检查点扩展 trace/scorer。记为 0.7.x 候选。
4. **S4**：所有拒绝共用一份“下一步”文案。真实对话框把用户打的字作为该题的
   答案值送达（与“打错的标签”同一条代码路径），只给 `response` 字段配指引会
   漏掉事故形态；文案用条件句分清“选项写错→原样重问”与“是文字→合法路径”，
   且只列该检查点确实有的路径（无 recall 的 kind 不提 recall）。
5. **D5.1**：`agents/reviewer.md` 的线格式模板缺 per-criterion `evidence`——照
   模板写的 PASS 必被判“claims pass with no evidence anchor”。模板已对齐强制
   契约；聚合枚举 `run_status`/`verdict` 一并按大小写/空白归一化。
6. **D5.3 实现口径**：
   - “heredoc 正文保守判写”实现为：正文是数据（其中的 `>` 不是重定向），但
     正文里的 `$(…)`/反引号是活的、照常判——验收标准 9 明确把正文里的 `>`
     列为只读。
   - `[[ … ]]`（命令位置）内部 `<`/`>` 是比较；但 `(( … ))` 的**词**照判
     （既有写屏障契约要求拒 `((rm scratch))`；只在算术内忽略重定向）。
   - 保留字（then/do/time/!…）、替换充当命令名（`"$(echo rm)" -rf x`）、
     读 stdin 的脚本（`python3 -`）、perl/ruby/php 内联代码、包装器扫描
     （timeout/nice/watch/doas/strace）一并覆盖。
7. **自身缺陷（探查抓到，非设计）**：`clipRunes` 把字节偏移当 rune 序号；词法
   器换行分支不前进（任何多行命令死循环 → gate 10s 超时）；反斜杠续行隐藏
   动词。已修；新增看门狗 + 6000 输入 fuzz + 词法器前进守卫
   （`!lex-stop` → 判写，绝不静默截断）。
8. **后果（已接受并记录）**：写类的项目检查（如 `make test`）不再能作为 L2
   被计账；`xargs` 历来判写，管线式查找在冻结期被拒——均属裁决 (a) 的代价。

### 遗留（不在本版范围）
- 既有缺陷：WAIT_USER 入口需求变更关闭后，`rerenderCheckpoint` 新建的后继
  检查点丢 `Snapshot`（实测 criteria 1→0）。建议携带 `src.Snapshot`，待用户
  裁决是否并入本版（task #44）。
- `install-smoke-test.sh` 的注册表污染（task #42）：修好前禁跑。
- REVIEW_RESULT 检查点的结论仍是一句状态（审查轮 L3-F6）：独立审查的发现
  没有进入检查点正文，用户仍要去别处读审查报告。需给审查结果定义结构化
  结论并与 S3b 一并设计。
- 写类分类器的残余类（裁决 (a) 的代价，未关）：表外动词缺省判读——
  `docker`/`kubectl`/`sqlite3`/`cargo`/`brew`/`gzip`/`zip`… 以及作为参数
  运行的脚本。关掉它要把诊断冻结期改成**白名单缺省判写**（表外即写，
  git 子命令已是这个口径），代价是未列出的只读工具会被拒。**是否翻转，
  等用户裁决。**
- `time -f %e` 一类的格式串取值被当成时间选项的值处理（审查轮 L2-F11，
  次要）：不影响判类，仅注释与实现对不齐。
- 回滚限制（已知并已写入 `checkpoint-protocol.md`）：0.7.x 下展示的、
  outcome 非 `unclear` 或带结论的检查点，回滚到 0.6.1 后裁决不落地；
  0.6.1 的 Stop 钩子不重新绑定展示。装回 0.7.x 后该检查点仍是「已展示」
  且绑定一致，直接重问即可。

## 9. Ship 审查第 1 轮（rev7）

冻结基线 `d9978b5`，四个独立视角（协议/评分器、写类分类器、渲染与交互、
兼容/升级），**四路均判 NEEDS-FIX**。每条发现先对代码复现再修；下表按
视角列结论与修法。括号里是各路报告的 Finding 编号；分类器一路只对上了
F7、F11 两个编号，其余行按发现内容列出。

| 视角 | 发现 | 修法 |
|---|---|---|
| 协议/评分器 | 空证据、空摘要也能自动通过（L1-F1） | `hasRealEvidence`：至少一条非空证据；摘要非空 |
| | 已展示给用户的检查点仍可被自动通过（L1-F3） | `cp.Displayed` 即拒：用户看过了，裁决就是用户的 |
| | 路线末段自动通过后无处可去（L1-F5） | `routeCompleteAfter` 守卫：后面没有阶段就不自动通过 |
| | 需求变更后仍残留续跑提示（L1-F6） | `OpenPhase`、`openRequirementChange` 清除提示 |
| | 评分器对自动通过的形状只查一半（L1-F4） | `user-gate-check.sh` 按协议逐项查：先 opened、同阶段、不搭 AskUserQuestion 调用、两次自动通过之间有 `phase.started`、其后不得 drop/extend；fixture 42→48，断言 250→285 |
| 写类分类器 | 运行时才确定的命令词被判读（L2，Critical） | 替换产生的空词不再当命令名；命令词按嵌套层级归集 |
| | 分组形式未建模、类别只按样例实现（L2） | `(`/`)` 进界符表；各类别按类别定义而非样例；补 `writeclass_review_test.go` |
| | 正常的只读命令被误拒：`if [[ … ]]`、git 读子命令（L2） | 条件命令在保留字后接受 `[[`；git 读表补全 |
| | 词法器二次复杂度：300 KB 命令 3 秒跑不完（L2） | 线性前缀匹配，300 KB 约 33 ms |
| | `]]` 后一个字符丢失、CRLF heredoc 吞掉后续命令（L2） | 界符消费长度修正；heredoc 正文剥尾部 `\r` |
| | 被拒的是已登记的项目检查时没有后果说明（L2-F7） | L2 后果披露 + `xargs`/`find -exec` 的展开提示 |
| 渲染与交互 | 超限结论被截成半句且无指引（L3-F1/F5） | 截在句末并以 `…（全文见 summoner-status）` 收尾；预算按**转义后**的 rune 计；框架字形（`║ ╔ ╠ ╚ ▶`）转义 |
| | 用户回 `more:`/`back:` 翻页被当成裁决（L3-F2） | 翻页回答返回导航块，不记录任何决策 |
| | PostToolUse 拒绝文案按字节截断（L3-F3） | 按 rune 截断 |
| | Stop 发问块在单页菜单上也写 `more:`（L3-F4） | 仅多页时写 |
| | 展示后内容变更的检查点永远无法再裁决（L3-F8） | 0.7.0 的 Stop 钩子把不匹配的绑定视作未展示并重新绑定 |
| | 用户点了 continue/skip 后模型收尾即停（L3-F9） | 续跑提示对**任何**回到 ACTIVE 的裁决生效（不只是自动通过） |
| | 状态视图不说为什么没自动通过（L3-F10） | `auto-pass not taken:` 行给出原因 |
| | 最大合法结论没有二进制级测试（L3-F11） | 补 e2e + 摘要折叠的钉死字面量 |
| | 文档漂移（L3-F7） | 见下「文档」 |
| 兼容/升级 | 框架文案与选项讨论被当成「用户要求停止」（L4-F1、L1-F2） | `stopack.go`：先剥掉每一份框架文案，停止词须与用户请求同句，出现选项讨论即不算；未展示时从宽、已展示时从严 |
| | `outcome: unclear` 这个缺省值进了摘要（L4-F2） | 仅有内容可保护时才折叠 outcome/结论；无结论检查点的摘要与 0.6.1 逐字节相同 |
| | 过期的会话 id 让状态视图显示「无活动 run」（L4-F3） | 导出的会话下无活动 run 即回退到项目级扫描；措辞改为陈述范围而非断言归属 |
| | 多条守卫只在进程内测试里钉了（L4-F4） | `contract/adaptive_seams_test.go`：旧状态走新二进制、枚举大小写、会话导出、诊断缺结论被拒、评分器 = reducer 策略 |

**文档（L3-F7）**：规范模板与真实渲染不一致（分节线前多一条空轨线、导航
提示文案过期、追加/不追加两个标签多了一对引号）；「导航提示是渲染里唯一
面向模型的行」（分节线同样是）；`commands/status.md` 把展示绑定说成
「id + nonce」；`commands/fix.md`、`new.md` 仍写「每阶段由用户裁决」。
均已改。为防再漂，新增 `shared/render_template_test.go`：模板里的
`{…}` 当通配符，必须与最大形状的渲染**逐行**一致；文档列出的每种变体行
必须是真实渲染会输出的行。

**跨版本对账（一次性，用 v0.6.1 标签的真实二进制）**：
- 升级：0.6.1 把 run 停在「已展示待裁决」→ 0.7.0 的 Stop 钩子发问且**不改写
  状态文件**（0.6.1 绑定的摘要就是 0.7.0 算出的那个）→ gate 放行 →
  PostToolUse 记下 CONTINUE → ACTIVE。绿。
- 回滚：0.7.0 展示一个无结论检查点 → 0.6.1 记下裁决。绿。
- 回滚限制：带 0.7.0 内容的检查点在 0.6.1 下被拒。成立，已记入文档。
- 钉死的摘要字面量 `24e10856…` 由 v0.6.1 代码重算，一致。

**变异检查**：接缝测试钉的是已有行为，「通过」本身不证明有效。在临时
副本里逐个破坏对应实现（摘要折叠所有 outcome、不归一化枚举、不导出会话
id、不做过期回退、不校验结论、评分器放行 diagnosis、Go 策略放行全部），
每条测试只在对应破坏下变红。绿。

## 10. Ship 审查第 2 轮（rev8）

冻结基线 `b6c33ba`；四路各自一份 detached worktree（上一轮共用副本互相干扰的
教训）。结论：协议 GO（1 条 Important 为 0.6.1 起的既有缺陷），分类器/
交互/兼容均 NEEDS-FIX。每条先复现再修；复现出的绕过与误拒全部成了回归
用例（`writeclass_r2_test.go` 74 行、`round2_decision_test.go`、
`stopack_test.go` 新增语表、`TestAdaptiveStopAckStrippingOverBinaries`）。

| 视角 | 结论 | 已处理 |
|---|---|---|
| 协议 | GO + F1（既有） | F1：`RecordReviewResult` 从不写 `rv.Result` → PASS 后 DONE 仍落在假的「审查未结算」检查点；已写入并以 `TestDoneAfterAPassReviewReachesPostGame` 钉住。F2：评分器补「opened 与 auto 之间的 FAIL check.result」审计 + 4 个新 fixture（52 fixture / 309 断言）；`trace-protocol.md` 的自动通过形状改为 `opened → (check.result …) → auto`。F3：空白内容（含零宽/组合符）不算证据也不再折叠进摘要。F4：accept / recovery-continue 补续跑提示（recall 会直接开启目标阶段，本就不需要提示——round 3 核实后删除了那条不可达代码并改正文档）。F5：三类防御性守卫补单元测试。F6：协议文档列表格式修正。 |
| 分类器 | NEEDS-FIX | 8 类「写判读」绕过与 9 类误拒全修：解释器的 stdin 程序、`[[ ]]`/`${…}` 内的进程替换、算术内的替换与重定向、裸替换词、`git branch -t`、`go test -o/-profile`、`perl -E`、`time` 包装器、大小写变形词；git config 作用域、npm audit、pip show、unzip -l、make -n、bash -n、tar -xO、/dev/std* 等误拒；`attachedAfter` 看闭合字符而非其后字符；嵌套深度上限 `maxLexDepth`（8k 深曾 2.6 s）。 |
| 交互 | NEEDS-FIX | F1：请求关联式的自然措辞（带宾语、since you asked me to、at the user's request、遵照/依照、停止后置标记）放行，且 中止/结束/停下 入词表。F2：协议把 `经过` 定义为标题，详情视图新增 `history conclusions` 承载各已验收阶段的结构化结论全文。F3：最后阶段 skip / REVIEW_DEBT continue 的续跑提示与状态 awaiting 行不再指向不存在的下一步，也不再插值空阶段名。F4：同协议 F4。F5/F6：描述性散文维持已记录的残余；宽松档收尾停止词要求前文也短。F7：渲染配方改指 `view:checkpoint`。F8：详情视图省略空小节与无 recall 页时的目标列表。F9：`model:` 行走 stderr。F10：单页发问不带 `more:` 的测试。F11/F12：门禁与续跑文案更新。F13：渲染要求点明常见原因（缩进/引用/围栏/改写）。F14：翻页回答补追加题提示，单页不再提翻页。 |
| 兼容 | NEEDS-FIX | F1：回滚段重写——0.6.1 的任何一次状态保存会丢弃 outcome/conclusion 且摘要不再匹配，答复被静默拒绝，装回 0.7.x 需重贴渲染，结论正文不可恢复；不再声称「绑定仍然一致」。F2：框架文案剥离补二进制级测试。F3：seam 清单（session-start 边缘、auto-pass 各守卫、宽松档选项否决）记为残余覆盖缺口。F4：空白结论项不再折叠（同协议 F3）。F5：邻接损坏状态文件可击穿项目级视图——既有缺陷，记录。 |

**未在 round 2 关闭（记录在案）**：
- 评分器无路由表，「路线已走完」一行无法被审计——由 Go 守卫与单元测试
  覆盖，评分器侧记为缺口。
- 第 3 轮开工前补：最后未结算阶段的 `skip:` 直接进入 POST_GAME_PENDING
  （显式 UNREVIEWED 收尾，不再是 ACTIVE 死路；交互 F3 的 A 类真正闭合，
  `TestSkipOfTheLastPhaseClosesThroughPostGame` 钉住；REVIEW_DEBT 的
  `continue:` 仍只能靠 stop 收尾，记为残余）。
- 回滚的「无损摘要」方案（把 0.6.1 可算的摘要留在 `display_digest`、另存
  0.7.0 摘要）会改状态格式，未采纳；文档按有损回滚写实。
- `git --git-dir="$(pwd)/.git"` 之外的同类「替换值拆词」形状未逐一穷举。

## 11. Ship 审查第 3 轮（rev9）

两路（冻结 `6d66e84`，各一份隔离副本）：**闭合核验**判 NEEDS-FIX（第 2 轮
27 条中 24 条闭合），**分类器对抗复审**判 NEEDS-FIX（第 2 轮 46 行全部闭合，
但 round 2 的修复又带出新洞）。每条先复现（全部复现）再修。

| 来源 | 发现 | 处理 |
|---|---|---|
| 分类器 | round-2 修复的直接回归：`bash -c '…' -n` / `bash s.sh -n`（选项在代码之后就执行）、`npm audit fix`、`go tool cover -o`、`git --namespace= push`（拆值规则吃掉子命令）、`git branch --contains HEAD` 被拒 | 全修。`-n` 只在首位才算语法检查；`audit fix` 判写；拆值规则只吃词法器拆出来的「展开词」 |
| 分类器 | 新机制带出的洞：解释器选项的值被当成脚本（`python3 -W ignore` + stdin）；进程替换喂 stdin 或充当操作数（`python3 <(echo code)`）；带展开的选项词吞掉后面的命令词（`env -u$(echo FOO) rm x`、`git --git-dir=$(echo .git) push`）；包装器里的运行时命令词与「命令字符串」（`watch -n 2 'rm -rf x'`）；表外 shell（dash/ksh/fish）；`ENV`/`NOHUP` 大小写；`/dev/fd/../..`；GNU sed 的 `e` | 全修，并按「类别而非样例」补齐文件写入动词（rmdir chgrp unlink mkfifo shred mktemp split patch）。自查又补：`python3 0<<EOF` 的 fd 数字、包装器不传递 stdin 状态、`ruby -`、`php -f`、`python3 -mjson.tool`、`perl -M`、git 只读子命令的 `--output=`。回归矩阵 150 行 |
| 分类器 | 引号分隔符的 heredoc 正文被当作会展开 | 正文是纯文本，不再词法分析其中的替换 |
| 分类器 | `make -n` 被当成读：GNU make 解析期就会执行 `$(shell …)` | 撤回该豁免：make 一律判写（回到裁决原文） |
| 分类器 | `attachedAfter` 的修复没有测试钉住 | 加了拆值后缀词的行 |
| 闭合核验 | 第 2 轮给「框架文案剥离」补的二进制测试钉不住：夹具行不带停止词，剥不剥离都一样 | 换成以停止词开头的结论行；变异检查确认：关掉剥离（两种变异）该测试变红 |
| 闭合核验 | round 2 把 结束/中止 放进裸停止词表，新增停摆：「上下文收集结束。」「第一阶段结束。」在未展示时直接结束回合 | 裸停止只认 停止/终止/stop/halt/abort；宽词表只用于「请求关联式」。另补 按照/应…的/因为/because/upon 等漏判 |
| 闭合核验 | REVIEW_DEBT 的 `continue:` 在路线走完时仍是死路（skip 一半已在 `80f7afa` 修） | 路线走完时 `continue:` = 默认 recall（回到最近已验收阶段），走既有 recall 路径并有 trace；REVIEW_DEBT 检查点带 recall 页 |
| 闭合核验 | 文档说 recall 也有续跑提示，代码不可达 | 删除不可达代码，改正文档 |
| 闭合核验 | 一个损坏的邻接状态文件击穿项目级状态视图 | 扫描跳过并在 stderr 具名（新 fallback 依赖这条路径） |

**残余（已记录）**：请求关联式对第三人称描述性散文仍会误判（「服务已停止，
按你的要求重启。」）——round 1 起即记录的残余；脚本作为参数运行
（`python3 x.py`）与表外工具（docker/kubectl/sqlite3/cargo/brew/zip/gzip）
仍是「未知动词缺省判读」，等用户对白名单缺省的裁决；GNU tar
`--checkpoint-action=exec`、`sort -o` 等未建模。

## 12. Ship 审查第 4 轮（rev10）

两路 @ `ed6d186`：**闭合终审**判 NEEDS-FIX（第 3 轮 8 条全部按声明闭合；新报
3 条 Important + 若干建议）；分类器终审见本节末尾的补记。

| 发现 | 处理 |
|---|---|
| **N1（回归，Important）**：第 3 轮给请求关联式加的裸「应/依」会匹配「停止响**应**你的要求」——普通诊断 prose 在已展示检查点上直接结束回合（`6d66e84` 的二进制不会） | 去掉裸 应/依，只在句子/从句边界后认；另加否定守卫（「没有按要求停止」「I did not stop because you asked」不算停止）。全部句子进回归表，两档都必须不结束回合 |
| **N2（Important）**：审查欠账 continue 的 recall 写出的 `recall.applied` 里 `from_phase_id` 为空，自带评分器判为畸形 | trace 增加 `from_checkpoint_kind`；评分器只对「有阶段的检查点」要求 `from_phase_id`；用真实二进制跑出该路径的 trace 作为 fixture（`valid-recall-from-debt.jsonl`，53 fixture / 315 断言） |
| **N3（Important，既有）**：PASS 之后在 REVIEW_RESULT 上点 continue 同样是死路 | 已结清审查时 continue = 收尾（post-game），与 done/skip 同处；审查未结清（NEEDS-FIX/BLOCKED）且路线走完时 = 回到开发阶段（同欠账检查点） |
| N4：映射后的 continue 对模型是静默的 | PostToolUse 回一个块，写明回到了哪个阶段（recall 自哪个 visit、当前是哪个 visit） |
| N5：宽松档裸停止词开头时对后半句不设限（「已停止响应。」「The service stopped responding after SIGTERM.」会结束回合） | 开头型停止词：前导只能是感叹/第一人称词，后半句有界（中文只许「了」与句末标点；英文至多三个短词且不是 -ing 分词）；结尾型：尾短且前文 ≤10 字 |
| N6：`tar -O` 只有紧贴写法被当成读；文档把包装器说成一律判写 | `-O`/`--to-stdout` 任意位置都算读（修改归档的 -r/-u/-A/--delete 除外）；文档改为「包装器内被执行的命令按其本身判定」 |
| N7：trace-protocol 的 `recall.applied` 字段、review-debt 不发 `checkpoint.opened`、recall 的提供范围、reducer 报错文案 | 均已对齐 |
| N8：六处承重守卫无测试（宽松档字数上限、选项讨论否决、粘贴守卫、展示先于停止判定、未走完路线不得映射、项目扫描不得浮现已关闭的 run） | 各补一条测试 |
| N9：唯一的状态损坏时 stdout 只说「无活动 run」 | stdout 加一行说明有 N 个会话无法读取 |

**回归的教训**（已写入记忆）：每放宽一个匹配器都要用「普通诊断 prose」做反向
表——这类放宽在三轮里两次造出事故级的停摆。

**本轮未钉住的一角**：钩子里「已结清 PASS 时 continue 不映射为 recall」这半个条
件没有钩子级测试（reducer 分支有 `TestContinueAfterASettledReviewReachesPostGame`
钉住；钩子侧构造「已结清 + 路线走完」的状态成本高）。变异检查其余各行全红：
裸 应/依、否定守卫、开头停止词的有界尾、映射块、tar -O、项目扫描（关闭的 run /
首个损坏会话）。

### 第 4 轮：分类器终审（rev11）

冻结 `ed6d186`。上一轮 49 行全部按声明闭合；**又报 12 类写判读**，并给出
结构性判断：「一轮修完、下一轮在修复里找到新的」已重复四次，「第 5 轮还会
找到更多」；误拒已收敛（961 行读命令，862 行按预期，常见读命令无误拒）。

| 类别 | 处理 |
|---|---|
| 赋值遮住动词：`X="$(pwd)" rm -rf x`、`X="$(pwd)" make`、`env X="$(pwd)" rm -rf x`、`time X="$(pwd)" make` | 闭引号留下的空词是赋值的值，不是命令。`X="$((1))" git status` 也不再被误拒 |
| 未加引号的 heredoc 正文里跨行的替换（`$( ⏎ rm -rf x ⏎ )`、反引号、`${x:-$(…)}`） | 正文作为一个整体处理，不再逐行拼接 |
| 脚本操作数就是 stdin：`python3 /dev/stdin < s.py`、`/dev/fd/0`、`php -f /dev/stdin`、`sed -f /dev/stdin` | 不算「已命名程序」 |
| 包装器：不分大小写（`timeout 5 RM -rf x`）、不懂选项参数个数（`timeout -s KILL 5 $(…)`、`flock -w 5 lock $(…)`）、变量命令词（`nice "$RM" -rf x`） | 选项参数表 + 时长识别 + 命令位置上的「运行时计算的词」判写 |
| 命令字符串包装器跳过以赋值开头的字符串（`watch -n 2 'FOO=1 rm -rf x'`）；`trap`、`parallel` 未建模 | 先词法分析字符串；trap / parallel 纳入 |
| GNU sed：任意分隔符的 `e` 标志、`!e`、转义斜杠地址、紧贴的 `-e'…'`、`--expression=`、`-f` | 改为按代码判定；`-f` 判写；`w` 只认命令与替换标志 |
| 字面 basename 取错词元（`"/bin/$(echo rm)" -rf x`） | 以 `/` 结尾的词元算「运行时计算」 |
| php 的 -B/-R/-E/-H/--run/--process-*；外部 time（`\time -a rm -rf x`）；zsh 的 `=(cmd)` | 均判写 |
| 第 12 类：**未知动词判读**（`luajit -e`、`pypy3 -c`、`pwsh -c`、`osascript -e`、`tsc`、`eslint --fix`、`prettier --write`、`cargo build`、`docker`、`kubectl apply`、`sqlite3 db 'update …'`、`brew install`…） | **不是补丁能关的**——是缺省策略问题，见下 |

回归矩阵 150 → 235 行。另按审查的误拒清单修了十余处罕见拼写（长选项吞掉脚本名、
脚本名之后的选项被当成解释器选项、`go tool vet/pprof`、`git <sub> --help` 等）。

**缺省策略（对抗小组裁决中）**：(A) 维持「未知动词判读」并在协议里写明「冻结
防的是普通写命令，不是有意的绕过」；(B) 翻转为「未知动词判写」+ 只读白名单 +
项目清单可声明只读命令行；(C) 诊断期 OS 级只读强制。冻结完整性视角的初稿
立场已回：建议 B 进 0.7.0、C 进 0.7.x；它做的本地普查显示，这位用户的真实会话
里约 9.4% 的「读」命令会落在表外（`rtk`、`gh`、`claude`、`docker`、`shellcheck`）
——翻转的代价取决于白名单能否覆盖这些。

