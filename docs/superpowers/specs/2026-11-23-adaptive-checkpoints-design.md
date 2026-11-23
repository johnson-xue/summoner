# 自适应检查点 — 设计（0.7.0 第一部分）

- 状态: 草案 rev2（待独立评审 → 用户批准）
- 日期: 2026-11-23
- 裁决: 用户 2026-11-23 — ①结论必须详细；②一轮问完，不再让用户打
  continue；③明确的环节不要公式化地问，效率要提高。
- 事故输入: 11201 会话复盘（`.superpowers/sdd/2026-11-23-incident-11201.md`，
  缺陷 A–J，均有行号/时间证据）。
- 基线: dfa4075（0.6.1 分支顶端）。本文取代 inband-narrative 设计中的
  「渲染瘦身」部分；其 journal 部分保留为后续切片。

## 1. 目标 / 非目标

**目标**
1. 检查点先交付结论：结论、证据、怎么查的、没查清、影响；后续检查点携带
   前面各阶段的结论（经过）。
2. 用户看到"解释 → 裁决框"连续出现，不需要打 continue。
3. 明确的环节自动通过并留痕；只在需要用户判断处停。
4. 修复事故中证实的机制缺陷（D5）。

**非目标**
- 不动 Phase 1 铁律（诊断被接受前禁止写代码）；只修其对只读命令的误判。
- 不动预算机制、变更闭环、recall、post-game 问卷。
- 不放行 TaskStop（见 D5.4）；不把展示绑定迁到未证实的 hook 能力上。

## 2. 事故对照

| 缺陷（台账编号） | 决策 |
|---|---|
| A 结论一行 + 60 行噪音 | D1 |
| B Stop 钩子逐字节相等：解释被拒、横线画长被拒 | D2 |
| C 每检查点要用户打 continue（cp-3 后空等 62 分钟） | D2 |
| D 用户文字被无视、原题重问 | D4 |
| E 诊断自由文本，无"未查清"要求 | D1 |
| F 只读命令误判写类（`2>/dev/null`、引号内 `>`） | D5.3 |
| G 审查员 status 枚举大小写不一致，审查被吞 | D5.1 |
| H status `{}` 跨会话歧义 | D5.2 |
| I TaskStop / SendMessage / MCP 未建模被拒 | 本版只修 MCP 之外的 H；TaskStop 记待办（D5.4） |
| J "尝试修复"后复现阶段被挪用、零改动 | D1 + D3 |

## 3. 设计决策

### D1 结论优先的检查点渲染

- **结构化结论**（PhaseSubmission 新增，全部可选）:
  `conclusion { summary, evidence[], approach, open_questions[], impact }`
  与 `outcome: confirmed | failed | unclear`。
- **不强制、但缺失可见**: 缺省 `outcome` 视为 `unclear`（不得自动过）；
  渲染把缺失部分明写成"未提供"，薄提交在用户眼前暴露而不是被藏起来。
  唯一例外：`diagnosis` 阶段的提交必须带 `summary` 与 `approach`
  （gate 拒绝缺省提交）——这是铁律阶段，结论质量在这里最要紧。
  `open_questions` 非空 ⇒ `outcome` 强制为 `unclear`。
- **渲染顺序**: `经过`（此前各已接受阶段的 summary，一行一个，最近在上）
  → `结论` → `证据` → `怎么查的 / 怎么做的` → `没查清 · 需要你判断` →
  `影响` → `请决定`（只渲染**一页**菜单的逐字标签 + nonce）。
- **移入 `summoner-status` 详情视图、不再进检查点**: 完整验收标准、产物
  digest、需求快照、计划范围、环境行、预算机器键、页码投影、recall 目标
  全列表、review retry、给模型的指令行。
- 兼容: `note` 保留；旧 run（无结构化结论）按"未提供"渲染。

### D2 解释后直接发问（Stop 钩子：绑定后拦停并让模型发问）

现状（`hooks/stop/main.go`）: WAIT_USER 且检查点未展示时，最终消息整条
等于规范渲染 → 绑定并**放行结束回合**（于是要用户打字才进入下一轮）；
否则拦停要求渲染。

改动:
1. 合资格检查由整条消息逐字节相等改为 **containment**：最终消息包含
   「规范渲染去掉末行 ╚ 装饰」的全文。模型可在块前后自由解释。
2. containment 命中 → 绑定展示（`Displayed`，摘要绑定不变）→ **拦停**，
   反馈"检查点已展示，现在用 AskUserQuestion 发起裁决"。
3. 已展示且未裁决 → 同样拦停并要求发问（覆盖"贴完又停"）。
4. 已裁决 → 放行。
5. containment 未命中 → 现状不变（拦停要求渲染规范块）。

不变的部分: `Displayed` 仍只由 Stop 钩子写；"同轮作答一律
decision_rejected"、nonce 入题、选项 = 一页菜单的 gate 校验全部照旧——
ask 仍发生在渲染回合之后，只是由钩子衔接而不是由用户打字衔接。
循环上限沿用宿主对 Stop 拦停的次数上限；到顶后回合结束，用户仍可打字。

### D3 自适应停问（白名单 + outcome 机械判定）

phase-submit 时 reducer 判定。**自动过 = 不进 WAIT_USER，记
`checkpoint.auto` trace（含阶段与结论摘要），直接进入下一阶段**:

| 阶段收口 | 自动过的条件 | 否则 |
|---|---|---|
| context | 一律自动 | — |
| reproduce | outcome=confirmed 且 evidence 非空 | 检查点（复现失败 = 与诊断矛盾） |
| verify / regression | outcome=confirmed 且 evidence 非空 | 检查点 |
| review | run_status=COMPLETED 且 verdict=PASS | 检查点（NEEDS-FIX / BLOCKED） |
| **diagnosis** | 永不自动 | 检查点 |
| **fix** | 永不自动（改动验收） | 检查点 |
| 变更闭环 / 预算耗尽 / 恢复 | 永不自动（现状） | 检查点 |

典型修 bug 的提问次数：诊断、改动验收两处（现状 6 处以上）。
`outcome` 缺省或为 `unclear`、`open_questions` 非空 ⇒ 一律停。
自动过的结论进入下一个真实检查点的 `经过`；用户随时可停。

### D4 用户文字是内容反馈，必须被行动

- 机制上能改的只有一处: `decision_rejected` 的理由。现状文案是"free text
  never advances the run"，模型的自然反应是原题重问。改为明确指令：
  先按文字行动（调查 / 修订 / 澄清），用 phase-submit 重新提交本阶段
  （新检查点带新问题）；文字若明确对应某个选项，简短确认后重新发问。
- 不做机械拦截: 无法区分"纠正诊断"与"就按 A 来"，拦住重问会把后者
  死锁。其余靠协议文字与 evals 用例。
- WAIT_USER 下写 scratch 被拒（事故 L1464）本版不改: 放开写需要先界定
  scratch 范围，超出本次范围，记待办。

### D5 机制缺陷修复

1. **审查员枚举**: `ValidateCriterionVerdicts` 对 status 做
   TrimSpace + ToLower 归一化；`agents/reviewer.md` 的 wire 契约写明
   `"status": "pass|fail|blocked"`。框架侧校验失败在检查点里渲染为
   "框架未能完成审查：<原因>；对你的影响：改动尚未被审查"，不再冒充
   "本阶段结果"。
2. **status 会话定位**: SessionStart 钩子把本会话 id 写入
   `CLAUDE_ENV_FILE`（官方 hooks 文档: SessionStart 可向该文件写入环境
   变量，供后续 bash 命令使用）为 `SUMMONER_SESSION_ID`；`summoner-status`
   的 payload 不带 session_id 时读它。读不到时退回现状（项目内唯一活 run
   则取之，否则列名拒绝）。
3. **写类判定**: 重定向目标为 `/dev/null` 不算写；引号内的 `>` 不算
   重定向；单独的 `2>&1` 不算写。事故中 3 条实测命令进 characterization
   测试。拒绝消息注明命中的规则。
4. **TaskStop（待办，不在本版）**: 需先把被停的后台任务映射回其原始
   Bash 调用，才能对写类命令记 OUTCOME_UNKNOWN；该映射未证实。本版维持
   拒绝。

### D6 协议与文档同步

`references/checkpoint-protocol.md`（两轮段、canonical 模板、白名单表）、
`references/single-loop.md`、`skills/phase-submit/SKILL.md`（结构化结论
payload）、8 个 command 的 Loop 行、`AGENTS.md` 规则 5（改协议同时改
spec）。CHANGELOG 0.7.0。

## 4. 已弃选项

- **展示绑定迁到 gate 的 AskUserQuestion 校验**: gate 看不到模型消息正文；
  需要读 transcript 的流式时序（未证实的前提）。
- **裁决框承载完整结论**: 框内文字长度上限未实测。
- **"最近活跃会话"指针定位 status**: 并发会话下后写者覆盖，会读到对方
  的 run（竞态）。
- **强制所有阶段提交结构化结论**: 会打断全部既有提交方与测试夹具；
  改为缺省可见 + 不得自动过，仅对 diagnosis 强制。

## 5. 风险与缓解

- **R1 自动过被滥用**: confirmed 需 evidence 非空；open_questions 非空强制
  unclear；diagnosis / fix 永不自动；用户在下一检查点看到全部经过并可
  recall；审查仍是实质验收门。
- **R2 containment 放宽削弱防伪造**: 比较的是规范块去装饰后的全文，内容
  差异仍拒；摘要与 nonce 绑定不变。cp-3 实测（3583 字符一致、仅横线
  不同）是放行与拒绝的分界依据。
- **R3 自动过更难察觉**: `checkpoint.auto` trace、`经过` 节、status 详情
  视图三处可查。
- **R4 兼容**: run state 无新增必填字段；旧 run 可读；v1 manifest 不变。

## 6. 验收标准

1. 真实二进制走查 fix run: context 自动过（`checkpoint.auto` 1 条）；诊断
   检查点含 经过 / 结论 / 证据 / 怎么查的 / 没查清 / 影响；用户不输入任何
   内容即出现裁决框。
2. reproduce `outcome=confirmed` → 无检查点、有 auto trace；`failed` →
   检查点；缺省 → 检查点。
3. Stop 钩子: "解释 + 规范块" → 绑定并要求发问；缺规范块 → 拦；仅 ╚ 长短
   不同 → 通过；已裁决 → 放行。
4. 诊断检查点渲染 ≤ 32 行，不含审查标准 / 环境行 / 预算机器键 / 页码投影。
5. verdict status `BLOCKED` 不再被拒；契约写明小写枚举。
6. `ls x 2>/dev/null`、`awk 'NR>=1213' f` 诊断期放行；`echo x > f` 仍拒。
7. 同项目两个会话各有活 run 时，status `{}` 解析为调用者自己会话的 run。
8. 全电池 `-count=1` + lane CI 绿；逐条核对本设计没有依赖未证实前提的
   测试或机制。

## 7. 接缝与切片

测试接缝沿用仓库既有两处，不新增: (a) **hook 二进制的 stdin/stdout**
（contract 测试与真实二进制走查——宿主实际调用的面）；(b) **shared 包
公开 API**（`RenderCheckpointMessage`、reducer 的提交/裁决）。

- S1 结构化结论 + 渲染重写（D1）。
- S2 Stop 钩子 containment + 发问拦停（D2）。
- S3 自适应停问（D3）。
- S4 自由文字反馈理由（D4）。
- S5 缺陷修复 D5.1–D5.3。
- S6 文档同步（D6）+ 全电池 + ship 审查。

每片 RED 先行；`make build release` 后跑 contract；改脚本或 fixture 必
`-count=1`。

## 修订记录

- rev1: 初稿。
- rev2: D2 机制按 `hooks/stop/main.go` 实际代码改写（原稿自相矛盾）；
  D1 改为缺省可见而非强制；D5.2 改用 SessionStart + `CLAUDE_ENV_FILE`
  （原"最近活跃会话"指针在并发下竞态）；TaskStop 移出本版；补接缝。
