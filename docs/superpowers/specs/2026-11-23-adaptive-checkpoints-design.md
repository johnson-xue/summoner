# 自适应检查点 — 设计（0.7.0 第一部分）

- 状态: rev3（已按独立评审 rev2 的六项修订；待复核 → 用户批准）
- 日期: 2026-11-23
- 裁决: 用户 2026-11-23 — ①结论必须详细；②一轮问完，不再让用户打
  continue；③明确的环节不要公式化地问，效率要提高。
- 事故输入: 11201 会话复盘（`.superpowers/sdd/2026-11-23-incident-11201.md`，
  缺陷 A–J）。
- 基线: dfa4075（0.6.1 分支顶端）。本文取代 inband-narrative 设计中的
  「渲染瘦身」部分；其 journal 部分保留为后续切片。
- 标注约定: 「评审指出」= 独立评审给出的代码事实，实现时先核实再用。

## 1. 目标 / 非目标

**目标**
1. 检查点先交付结论：结论、证据、怎么查的、没查清、影响；后续检查点
   携带前面各阶段的结论（经过）。
2. 用户看到"解释 → 裁决框"连续出现，不需要打 continue。
3. 明确的环节自动通过并留痕；只在需要用户判断处停。
4. 裁决被拒时模型能看见原因并知道下一步怎么做。
5. 修复事故中证实的机制缺陷（D5）。

**非目标**
- 不动 Phase 1 铁律本身；只修其对只读命令的误判（解释器策略待用户裁决，
  见 D5.5）。
- 不动预算机制、变更闭环、recall、post-game 问卷。
- 不放行 TaskStop；不把展示绑定迁到 gate 或 transcript 时序上。

## 2. 事故对照

| 缺陷 | 决策 |
|---|---|
| A 结论一行 + 60 行噪音 | D1 |
| B Stop 钩子逐字节相等：解释被拒、横线画长被拒 | D2 |
| C 每检查点要用户打 continue | D2 |
| D 用户文字被无视、原题重问 | D4（根因: 拒绝对模型不可见） |
| E 诊断自由文本，无"未查清"要求 | D1 |
| F 只读命令误判写类 | D5.3 |
| G 审查员 status 枚举大小写 | D5.1 |
| H status `{}` 跨会话歧义 | D5.2 |
| I TaskStop 等未建模被拒 | 本版不放行 TaskStop（D5.4） |
| J 复现阶段被挪用、零改动 | D1 + D3 |

## 3. 设计决策

### D1 结论优先的检查点渲染

- **结构化结论**（PhaseSubmission 新增，全部可选）:
  `conclusion { summary, evidence[], approach, open_questions[], impact }`、
  `outcome: confirmed | failed | unclear`。
- **不强制、但缺失可见**: 缺省 `outcome` 视为 `unclear`；渲染把缺失部分
  明写成"未提供"。例外: `diagnosis` 提交必须带 `summary` 与 `approach`
  （gate 拒绝缺省提交）。`open_questions` 非空 ⇒ `outcome` 强制 `unclear`。
- **入摘要**: conclusion 非空时并入 `CheckpointContentDigest`，使"展示后
  内容变更即拒"同样覆盖结论与证据；字段为空的旧 run 摘要不变。
- **渲染顺序**: `经过` → `结论` → `证据` → `怎么查的 / 怎么做的` →
  `没查清 · 需要你判断` → `影响` → `请决定`。
- **上限**（使行数预算可断言）: 经过 ≤ 6 行（每阶段最近一次已接受访问的
  summary，最近在上）；evidence ≤ 4；open_questions ≤ 3；每项一行；超出写
  "…另 N 条，见 summoner-status"。菜单只渲染**第 1 页**（恒含 continue 与
  stop）的逐字标签 + nonce。
- **移入 `summoner-status` 默认视图**: 完整验收标准、产物 digest、需求
  快照、计划范围、环境行、预算机器键、页码投影、recall 目标全列表、
  review retry、给模型的指令行。
- 诊断检查点在"最大合法提交"下 ≤ 32 行，由契约测试断言。
- 评审指出: 现有测试与夹具里约 14 处 diagnosis 提交（10 个文件）会因强制
  字段被拒——列入 S1 一并更新。

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
- `decision: block` 是官方文档规定、且 11201 现场六次证实能让模型继续
  的机制；不改用 additionalContext。
- 宿主对连续拦停有 8 次上限，**每次工具调用归零**（官方原文）；发问是
  工具调用，所以每个检查点只消耗 1 次。模型只贴块不发问最多 8 次后回合
  结束，用户可打字、可选 stop、可用 SUMMONER_HOOKS_DISABLE=1。
- 不变: `Displayed` 只由 Stop 钩子写；同轮作答一律拒绝；nonce 入题；
  选项 = 一页菜单。
- 测试: 旧 nonce 的块与新块并存时只绑新块。
- 评审指出需同步的代码内旧文案（模型实际读到的面）: `stop/main.go` 的
  block 理由"整条消息必须等于该渲染"；`render.go` "╠═ 下一轮裁决"与
  "decide on the NEXT turn"；`gate.go` "copy … from the render's paging
  note"；`skills/phase-submit/SKILL.md` 未说贴完即结束回合。

### D3 自适应停问

**自动过 = 照常创建检查点记录，随即由 reducer 以 continue 自动裁决**
（`AutoPassed` 标记，永不展示），不进入用户等待。评审指出: 接受的全部
副作用挂在检查点记录上（结果置 accepted、验收冻结、访问桶），跳过记录
会让后续审查信封为空。

| 阶段收口 | 自动过的条件 | 否则 |
|---|---|---|
| context | 一律自动 | — |
| reproduce | outcome=confirmed 且 evidence 非空 | 检查点 |
| verify / regression | outcome=confirmed 且 evidence 非空 | 检查点 |
| diagnosis、fix | 永不自动 | 检查点 |
| 其余阶段（requirements/plan/implement/planning/changelog/publish/prepare/merge/decide） | 永不自动（本版） | 检查点 |
| 变更闭环 / 预算耗尽 / 恢复 | 永不自动（现状） | 检查点 |

- **审查结果**（单独切片）: 评审指出审查结果检查点由 `RecordReviewResult`
  开，不在提交站点；且审查常是末阶段。实现为第二站点: COMPLETED+PASS
  且证据齐 → 自动裁决，路线有下一阶段则 continue，否则走 done 进入
  POST_GAME_PENDING。若实现中发现需要新的转移路径，**本版放弃该行**
  （审查结果照常停），不硬做。
- `outcome` 缺省或 `unclear`、`open_questions` 非空 ⇒ 一律停。
- **续跑兜底**（评审指出: 否则事故 C 换个形态复发）:
  ① `summoner-status` 默认视图明写"阶段 X 已自动通过 → 立即进入 Y"；
  ② Stop 钩子: 活跃 run 最近一条事件是 `checkpoint.auto` 且其后没有
  `phase.started` → 拦停一次要求继续（先过 stop-ack）。
- 留痕: `checkpoint.auto` trace；自动过的结论进入下一个真实检查点的
  `经过`。
- 提问次数: 修 bug 的强制提问为诊断、改动验收两处；审查结果是否为第三处
  取决于上述切片。

### D4 裁决被拒对模型可见

- **根因**（评审 Critical）: PostToolUse 拒绝只写 trace 与 stderr 并以 0
  退出；退出码 0 的 stderr 模型收不到。所以今天每一次裁决被拒，模型都只
  看到用户的原始回答。
- **改法**: 裁决被拒时向 stdout 输出
  `{"decision":"block","reason":<原因与下一步>}`，对自由文字与其余裁决拒绝
  一并生效；契约测试断言 stdout 携带该指令。
- **自由文字的指令必须是合法路径**（评审指出: WAIT_USER 下阶段已关闭、
  写被拒，无法重新提交）: 先做文字要求的只读调查并汇报；结论需要修订时，
  请用户选 recall 重开本阶段后重新提交；`more:` / `back:` 是翻页，原样
  重问目标页。
- 文字本身仍不直接推进 run（现行裁决不变）。
- WAIT_USER 下写 scratch 被拒本版不改，记待办。

### D5 机制缺陷修复

1. **审查员枚举**: `ValidateCriterionVerdicts` 对 status 做 TrimSpace +
   ToLower；`agents/reviewer.md` 写明 `"status": "pass|fail|blocked"`。
   框架侧校验失败渲染为"框架未能完成审查：<原因>；对你的影响：改动尚未
   被审查"。
2. **status 会话定位**: SessionStart 钩子向 `CLAUDE_ENV_FILE` **追加**
   `export SUMMONER_SESSION_ID='<id>'`（官方: SessionStart 可向该文件写入
   变量供会话内后续 bash 命令使用）；`summoner-status` 无 session_id 时读
   之，此时按名列出项目内其他活 run。读不到则退回现状。评审指出待走查:
   `cd` 之后该变量是否仍在。
3. **写类判定**（评审要求写死规则——同一谓词同时决定铁律拒绝、L2 检查
   记分、崩溃扫描的 read_class，放宽不当即静默漏放）:
   - 逐个重定向判定；目标去引号后**恰为** `/dev/null` 才不算写
     （`> /dev/null > f`、`>$NULL`、`>~/dev/null` 仍是写）。
   - 文件描述符复制仅限 `>&` + 单个数字（`2>&1`、`1>&2`）；`>&file`、
     `&>`、`&>>`、`>|`、`>>`、`<>` 仍是写。
   - 词法: 单引号、双引号、`$'…'`、未引用反斜杠；heredoc 正文保守判写。
   - 动词通道与重定向通道独立。
   - `isWriteClassCommand` 改为同时返回命中的规则，拒绝消息注明。
   - 事故 3 条实测命令 + 上述每个边界进 characterization 测试。
4. **TaskStop**: 本版不放行（需先把后台任务映射回原始调用，映射未证实）。
5. **解释器与构建工具（待用户裁决）**: 0.6.1 复核实测，诊断未被接受时
   `python3 -c` 写文件、`node -e`、`make build`、`go run`、`ssh host "…"`、
   `find -exec` 全部放行——铁律对解释器是纸面的。选项: (a) 诊断期把
   带内联代码的解释器与构建/运行/远程工具判为写类（建议）；(b) 维持现状，
   只更正过时的 residual 注释。

### D6 协议与文档同步

`references/checkpoint-protocol.md`、`references/single-loop.md`、
`references/workflow-reference.md`（评审指出 :71、:73 与本设计相反）、
`skills/phase-submit/SKILL.md`、8 个 command 的 Loop 行、D2 列出的代码内
字符串、`AGENTS.md` 规则 5 与 Testing 节（`go test -count=1`）。
CHANGELOG 0.7.0。

## 4. 已弃选项

- 展示绑定迁到 gate: gate 看不到模型消息正文。
- 裁决框承载完整结论: 框内文字上限未实测。
- "最近活跃会话"指针: 并发下后写者覆盖。
- 强制所有阶段结构化结论: 打断全部既有提交方。
- 用 additionalContext 替代 Stop 的 block: block 是文档规定且现场证实的
  让模型继续的机制。
- 自动过不建检查点记录: 丢失验收冻结等副作用（评审指出）。

## 5. 风险与缓解

- **R1 自动过被滥用**: confirmed 需 evidence；open_questions 强制 unclear；
  diagnosis / fix 永不自动；经过 可见、可 recall；审查仍是验收门。
- **R2 containment 放宽**: 比较规范块去尾行全文；结论入摘要；nonce 绑定。
- **R3 拦停把用户锁住**: 已展示分支先过 stop-ack；8 次上限且工具调用归零；
  SUMMONER_HOOKS_DISABLE 可解。
- **R4 写类规则放宽引入漏放**: 规则写死 + 逐边界测试；三条通道共用一个
  谓词，测试同时断言铁律、L2、崩溃扫描三处行为。
- **R5 拒绝可见化改变模型行为**: 只增不改——原本不可见的信息变为可见；
  文案给出合法下一步。
- **R6 兼容**: run state 无新增必填字段；旧 run 摘要不变。

## 6. 验收标准（可断言）

1. 真实二进制走查 fix run: context 提交后无待裁决检查点、状态 ACTIVE、
   trace 有 1 条 `checkpoint.auto`，且该检查点记录已裁决并带 AutoPassed。
2. reproduce `confirmed`+evidence → 自动裁决；`failed`、缺省 → 待裁决。
3. Stop 钩子: "解释 + 规范块" → 绑定且输出 block 要求发问；仅 ╚ 长短不同
   → 同上；缺规范块 → block 要求渲染；已展示 + 停止确认 → 放行；
   已裁决 → 放行；旧 nonce 块与新块并存 → 只绑新块。
4. gate: Displayed 已置位时，带 nonce 且选项等于第 1 页的 AskUserQuestion
   放行（裁决框本身是宿主行为，不在此断言）。
5. 诊断检查点在最大合法提交下 ≤ 32 行；不含审查标准 / 环境行 / 预算
   机器键 / 页码投影。
6. 裁决被拒（自由文字、非法选项）时 PostToolUse 的 stdout 含
   `decision: block` 与下一步指令。
7. verdict status `BLOCKED` 不再被拒。
8. D5.3 每条边界: 只读形态诊断期放行，写形态仍拒。
9. 同项目两个会话各有活 run，注入 SUMMONER_SESSION_ID 后 `{}` 解析为
   对应会话；`cd` 之后结果不变。
10. 全电池 `-count=1` + lane CI 绿。
11. 评审动作（非测试）: 实现完成后逐条核对本设计无依赖未证实前提的
    机制或测试。

## 7. 接缝与切片

测试接缝沿用既有两处: (a) hook 二进制的 stdin/stdout；(b) shared 包
公开 API。

- S1 结构化结论 + 摘要 + 渲染重写 + 上限 + 既有 diagnosis 夹具更新。
- S2 Stop 钩子 containment、发问拦停、停止豁免 + 代码内文案。
- S3 自适应停问（提交站点 + 续跑兜底）。
- S3b 审查结果自动裁决（第二站点；不成立则放弃该行）。
- S4 裁决拒绝可见化 + 合法路径指令。
- S5 缺陷修复 D5.1–D5.3。
- S6 文档同步 + 全电池 + ship 审查。

每片 RED 先行；`make build release` 后跑 contract；改脚本或 fixture 必
`-count=1`；**修好冒烟测试的配置污染（task #42）之前不跑
`install-smoke-test.sh`**。

## 修订记录

- rev1: 初稿。
- rev2: D2 按 stop/main.go 实际代码改写；D1 缺省可见；D5.2 改用
  SessionStart + CLAUDE_ENV_FILE；TaskStop 移出；补接缝。
- rev3（按独立评审六项）: D4 改为 stdout 可见通道并给出合法路径指令
  （原方案的前提不成立）；D3 自动过保留检查点记录、审查结果列为独立
  切片、补续跑兜底；D1 定上限并入摘要；D2 已展示分支加停止豁免、补代码
  内文案；D5.3 写死判定规则并改签名；D6 补 workflow-reference；验收
  标准改写为可断言项；新增 D5.5 解释器策略待裁决。
