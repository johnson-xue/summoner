# 自适应检查点 — 设计（0.7.0 第一部分）

- 状态: rev4（已按两轮独立评审修订；待用户批准）
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
5. **写类判定的范围（待用户裁决）**。评审核实的现状: 诊断期的写类判定只
   认一张短动词表（rm/mv/cp/tee/chmod/chown/touch/mkdir/dd/truncate/ln/
   install/sudo/xargs/eval/exec/sh/bash/zsh）、表内的 git 写子命令、以及
   任意 `>`（bashwrite.go:44-60）。其余在诊断未被接受时全部放行:
   - 带内联代码的解释器（`python3 -c`、`node -e`、`perl -i`、awk 的
     `system()`）；
   - 构建与运行（`make`、`go run`、`npm i`、`npx`、`pip install`）；
   - 远程与复制（`ssh`、`rsync`、`scp`）、下载落盘（`curl -o`、`wget -O`）、
     解包（`tar -x`、`unzip`）；
   - 表外的 git 写子命令（`restore`、`am`、`bundle create`、
     `filter-branch`——未知 git 子命令判只读，gate.go:161-177）；
   - `find -exec`。
   选项: (a) 诊断期把以上判为写类（建议；`find -exec` 一律判写）；
   (b) 维持现状，只把过时的 residual 注释改对。

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
9. D5.3 列出的每个边界: 只读形态诊断期放行，写形态仍拒。
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
- rev4（复核: ②③⑤⑥ 未闭合 + B1–B7）: D3 新开 `AutoPassCheckpoint`
  入口、事件入三份白名单、自动过排除 ExtendAsk 与检查 FAIL（B1）、兜底
  条件按生命周期事件并定义"一次"；D1 行数口径与字符上限、上限改 ≤42、
  保留导航提示（B4）；D5.3 明确 /dev/null 豁免适用于所有文件目标操作符、
  补边界清单、更正调用点数；D4 文案披露已生效部分并禁止重问已结算搭车题
  （B2）、下一步指令按检查点是否有 recall 分情况（B3）；D5.2 记 cd 残余
  并删去对应验收断言（B5）；D6 更正行号（B6）；D2 记 stop-ack 误报代价
  （B7）；D5.5 事实更正为"只认短动词表 + `>`"。
