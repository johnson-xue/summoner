# Summoner 单模式重写（Single-Mode Rewrite）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 chain/graph 双执行模式合并为单一「主会话单循环 + Go hook 事件 reducer」模式，删除 walker 与图路由，把全部铁律从 prompt 纪律升级为平台事件锚定的机制。

**Architecture:** 主会话是唯一 LLM 编排者与默认唯一执行者（skills 承载流程）；Go hooks 内嵌事件 reducer 是唯一记账/放行机关（写入资格、检查点生命周期、冻结验收视图、预算、崩溃恢复）；agent 边界只剩终局⑤黑盒审查 + 评测放行的独立分片。检查点闭环钉在平台事件上：phase-submit 微 skill 提交 → Stop hook 展示核验 → AskUserQuestion tool_response 唯一推进来源。

**Tech Stack:** Go（hooks + reducer + status viewer）、Bash（scorers/生成器）、Markdown（skills/commands/references）、JSON Schema（manifest v2）、GitHub Actions（CI/release）。

**Spec:** `docs/superpowers/specs/2026-11-09-single-loop-checkpoint-design.md`（本计划从 spec 展开，执行者须同时读两份）

## Global Constraints

- C1 单一标准模式：不留兼容层、双路径、模式开关。运行时不收旧图/旧 state；旧 trace 只读。
- **Summoner 是控制层（spec §2 四层职责）**：不新增与内置/superpowers/项目 skills 重复的方法论 skill；命令保持薄入口；每个能力恰好一个 default provider。
- C3 铁律不弱化：fix/debug 先诊断后改码（诊断未被用户接受前写入工具被 gate 阻断）；检查点绝不自动推进；post-game 强制；框架动词固定、项目 skill 可替换；不硬编码项目名。
- T1/T2：不引用文章历史数字；折叠/验证取舍一律由评测裁决，不绑定模型版本。
- 平台锚点 = Claude Code 2.1.319 已核验事实（spec §0/§5）：`tools`/`disallowedTools`/`omitClaudeMd` frontmatter、SubagentStop `agent_transcript_path`、Stop `last_assistant_message`、`onFailure:"block"`（v2.1.295+）、AskUserQuestion tool_response 平台注入、UserPromptSubmit 非人类触发源。
- 门型 hook 故障语义 = block（exit 2 / 非法输出 / 超时 / 缺二进制都不得旁路）；唯一逃生舱 `SUMMONER_HOOKS_DISABLE=1`（使用记为契约违例）。
- Go module `github.com/johnson-xue/summoner`；测试全部 hermetic（`stateDirOverride` 先例）；shell 脚本过 `shellcheck`；`scripts/validate-repo.sh` 必须始终绿。
- commit 信息按仓库风格（`feat(...)`/`fix(...)`/`test+docs:`），末尾附 `Co-Authored-By: Claude Code <noreply@anthropic.com>`。
- 每个 Task 结束跑 `go test ./... && bash scripts/validate-repo.sh` 全绿再 commit。

## Review Focus

1. **Gate 旁路**：gate 二进制缺失/超时/崩溃/非法 JSON 时，写入工具必须被阻断而非放行 —— 契约测试进 Task 6。
2. **未展示即推进**：checkpoint 只生成未展示（或展示版本错配）时"继续"必须无效 —— 测试进 Task 5（Stop 展示核验）与 Task 3（决策负载校验）。
3. **裁决伪造/自动续答**：AskUserQuestion 超时自动答案、非当前 checkpoint 的回答、重复回调、注入面 —— 决策不被记账为用户裁决 —— 测试进 Task 4（绑定+幂等）与 Task 6（timeout=never 断言）。
4. **盖章审查/伪造证据**：verdict 声称的 evidence id 不在子 transcript、漏 criterion、产物过期、check 定义被改 —— `review.failed` 或作废 —— 测试进 Task 7。
5. **副作用自动重放**：tool.started 后崩溃 → OUTCOME_UNKNOWN，变更性动作绝不自动重跑，探针三分支 —— 测试进 Task 1（状态机）与 Task 5（Stop 留档）。

---

### Task 1: reducer 核心状态机与持久化

**Files:**
- Create: `hooks/shared/reducer.go`
- Create: `hooks/shared/reducer_test.go`

**Interfaces:**
- Produces（后续所有 hook 任务消费）:
  - 类型：`RunStatus`（ACTIVE/WAIT_USER/REVIEW_PENDING/OUTCOME_UNKNOWN/POST_GAME_PENDING/CLOSED）、`PhaseState`、`CheckpointState`（含 `ID, Nonce, ArtifactVersions, OpenedAt, Displayed bool, DisplayDigest, Decided bool`）、`ReviewState`（PREPARED/RUNNING/REVIEWED/FAILED/BLOCKED）、`BudgetCounters`、`OutcomeUnknown`、`RunState`
  - `func LoadRun(runID string) (*RunState, error)` / `func (r *RunState) Save() error`（原子写，沿用 `stateDirOverride` hermetic 模式）
  - 转换方法：`StartRun`、`OpenPhase(id string) error`、`SubmitPhase(sub PhaseSubmission) (CheckpointState, error)`、`MarkDisplayed(checkpointID, contentDigest string) error`、`RecordDecision(checkpointID string, d Decision) error`、`RequestReview(env Envelope) (string, error)`、`RecordReviewResult(res ReviewResult) error`、`MarkOutcomeUnknown(toolUseID string)`、`ResolveOutcome(toolUseID, outcome string) error`、`CountToolCall()`、`BudgetExhausted() (bool, string)`；**需求变更闭环（spec §1 增补）**：`OpenRequirementChange(note string) error`（冻结 DevPosition，置 CHANGE_OPEN 子态，期间 phase-submit 被拒）、`ApplyRequirementDelta(delta RequirementDelta) (ImpactReport, error)`（`RequirementsVersion++`，按依赖标 stale）、`ResumeAfterChange() error`（恢复保存的开发位置）
  - `Decision` 枚举：CONTINUE/SKIP/DONE/RECALL/STOP/REVIEW_ON_DEMAND/**ACCEPT_REQUIREMENT_CHANGE**（仅 CHANGE_OPEN 确认点合法）
  - `RunState` 增字段：`RequirementsVersion int`、`DevPosition`（当前任务/测试进度/未决验证）、`Stale []string`（被变更判 stale 的 criterion/结果 id）

- [ ] **Step 1: 写失败测试**

`reducer_test.go`：`TestIllegalTransitions`（WAIT_USER 下重复 SubmitPhase 报错；未 OpenPhase 先 SubmitPhase 报错；CLOSED 后一切转换报错；CHANGE_OPEN 期间 SubmitPhase 报错；ACCEPT_REQUIREMENT_CHANGE 在非 CHANGE_OPEN 状态报错）、`TestDecisionRequiresDisplayed`（未 MarkDisplayed 时 RecordDecision 报错；DisplayDigest 不匹配报错）、`TestOutcomeUnknownCrashRecovery`（MarkOutcomeUnknown 后重载状态仍在；ResolveOutcome 三分支 accepted/failed/indeterminate）、`TestIdempotentRecord`（同 toolUseID 重复 MarkOutcomeUnknown 不产生第二条）、`TestBudgetCounting`、`TestRequirementChangeLoop`（OpenRequirementChange 冻结 DevPosition；ApplyRequirementDelta 后 RequirementsVersion++、受影响 criterion 进 Stale、未受影响的已接受结果保持有效；ResumeAfterChange 恢复原开发位置且状态回 ACTIVE）。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./hooks/shared/ -run TestIllegalTransitions -v`
Expected: FAIL（reducer.go 未定义）

- [ ] **Step 3: 实现 `reducer.go`**

状态机为纯函数式转换 + JSON 持久化；每个转换方法先校验合法矩阵再变更；非法转换返回 error（由调用方决定 exit 2 文案）。SPEC 细节见 spec §4/§5。

- [ ] **Step 4: 运行测试通过**

Run: `go test ./hooks/shared/ -v`
Expected: PASS 全部

- [ ] **Step 5: Commit**

```bash
git add hooks/shared/reducer.go hooks/shared/reducer_test.go
git commit -m "feat(reducer): run-state machine v2 — legal transitions, display-bound decisions, OUTCOME_UNKNOWN"
```

### Task 2: trace schema v2 写入器

**Files:**
- Create: `hooks/shared/trace.go`
- Create: `hooks/shared/trace_test.go`

**Interfaces:**
- Consumes: Task 1 的 `RunState`。
- Produces: `type TraceEvent struct { Type string; Seq int; Timestamp string; Source string; PromptID string; ToolUseID string; Payload map[string]interface{} }`；`func AppendEvent(runID string, ev TraceEvent) error`；`func VerifyChain(runID string) error`（哈希链校验）；事件类型常量按 spec §7（workflow.started/ended/aborted、phase.started/closed、phase_submitted.rejected、checkpoint.opened/displayed/decided、recall.applied、**change.opened/delta_applied/resumed**、tool.started/result/failed/outcome_unknown/resolved、check.result、review.prepared/started/verdict/failed/blocked/superseded、delegation.*、parallel.*、agent.telemetry、post_game.*）；`func CriticalControlEventsPresent(runID string) error`（缺关键控制事件=协议失败）。

- [ ] **Step 1: 写失败测试**

`TestEventMarshalAllowList`（未知 type 拒绝）、`TestSeqMonotonic`、`TestHashChainTamper`（篡改一行后 VerifyChain 报错）、`TestCriticalEventsMissing`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./hooks/shared/ -run TestEventMarshal -v` → FAIL

- [ ] **Step 3: 实现 `trace.go`**

append-only JSONL + 每事件 `prev_hash` 链；文件路径 `~/.claude/plugins/summoner/traces-v2/{project_root_hash}/{run_id}.jsonl`（与旧 trace 目录分开，旧只读）。

- [ ] **Step 4: 测试通过**

Run: `go test ./hooks/shared/ -v` → PASS

- [ ] **Step 5: Commit** — `feat(trace): v2 event schema with platform correlation keys + hash chain`

### Task 3: PreToolUse 全工具 gate（新二进制，替换 pretooluse-skill 的门职责）

**Files:**
- Create: `hooks/pretooluse-gate/main.go`
- Create: `hooks/pretooluse-gate/main_test.go`
- Delete（本任务末）: `hooks/pretooluse-skill/main.go`（其 Skill 观测职责并入本 gate）

**Interfaces:**
- Consumes: Task 1 全部；Task 2 `AppendEvent`。
- Produces（stdin JSON → exit code 语义，供 hooks.json 与契约测试）:
  - `Skill` 调用：首个阶段 skill → `OpenPhase`；`summoner:phase-submit` → `SubmitPhase` 校验（payload 含 phase id、产物集、标准 id、检查结果引用；非法 exit 2 带原因）；其余 skill 记录。
  - `Edit/Write/NotebookEdit` + 写类 Bash 判定：诊断未被接受（reducer 无 P1 accepted 检查点）→ exit 2；WAIT_USER 期间 → exit 2。
  - `Agent`：校验 subagent_type ∈ {reviewer, worker}、non-fork、携带已登记 review_id → 放行并记 `delegation.started`；否则 exit 2。
  - `AskUserQuestion`：问题文本须含当前 checkpoint nonce → 否则 exit 2。
  - 一切工具调用前持久化 `tool.started`（OUTCOME_UNKNOWN 前提）并 `CountToolCall`；预算耗尽 → exit 2 文案指向检查点。

- [ ] **Step 1: 写失败测试**

fixture payload 驱动（复用 `shared.ToolInput` 结构）：`TestWriteGateBeforeDiagnosisAccepted`、`TestWriteGateBlockedWhileWaitUser`、`TestPhaseSubmitRejectedBadPayload`、`TestPhaseSubmitRejectedReplay`（重复同 payload）、`TestAgentGateWrongType`、`TestAskUserMissingNonce`、`TestBudgetExhaustionBlocks`、`TestGateParseFailureIsDeny`（非法 stdin → exit 2，替换旧 fail-open）。

- [ ] **Step 2: 确认失败** — `go test ./hooks/pretooluse-gate/ -v` → FAIL

- [ ] **Step 3: 实现 gate**（分支按 Interfaces 表；exit 0/2 语义与 spec §5 事件×责任表一致）

- [ ] **Step 4: 测试通过** — `go test ./hooks/... -v` → PASS

- [ ] **Step 5: Commit** — `feat(gate): all-tool PreToolUse gate — write-eligibility, phase-submit, delegation, nonce, budget`

### Task 4: PostToolUse 登记与 AskUserQuestion 裁决记账

**Files:**
- Create: `hooks/posttooluse/main.go`
- Create: `hooks/posttooluse/main_test.go`

**Interfaces:**
- Consumes: Task 1/2/3。
- Produces: `tool.result`/`tool.failed` 事件（含退出态）；AskUserQuestion 专路——校验 tool_response 为平台注入对象、问题负载携带当前 checkpoint_id、选择 ∈ Decision 枚举 → `RecordDecision` + `checkpoint.decided` 事件；不合法 → `phase_submitted.rejected` 风格的拒绝事件，**不写 decision**；同 toolUseID 重复回调幂等跳过。Agent 工具完成 → `agent.telemetry`（`final_request_tokens` 口径声明字段 + totalToolUseCount）。

- [ ] **Step 1: 失败测试**：`TestDecisionRecordedOnValidResponse`、`TestDecisionRejectedWrongCheckpoint`、`TestDecisionRejectedNonPlatformShape`、`TestDecisionIdempotent`、`TestAgentTelemetryEmitted`。
- [ ] **Step 2: 确认失败** — `go test ./hooks/posttooluse/ -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 通过** — `go test ./hooks/... -v`
- [ ] **Step 5: Commit** — `feat(posttooluse): platform-anchored decision recording + telemetry`

### Task 5: SubagentStop 对账 + Stop 展示核验/停止不对称

**Files:**
- Create: `hooks/subagentstop/main.go` + `main_test.go`
- Modify: `hooks/stop/main.go`（重写）+ `hooks/stop/main_test.go`

**Interfaces:**
- Consumes: Task 1-4；`SubagentStop` 输入的 `agent_transcript_path`；Stop 输入的 `last_assistant_message`。
- Produces:
  - SubagentStop：登记 transcript 路径；若该 run 有 RUNNING review 且 transcript 内可解析出结构化结果 → `RecordReviewResult` 补记（按调用 id 去重，迟到结果不影响新代次）。
  - Stop（重写）：WAIT_USER 且渲染块缺失 → `decision:block, reason:"渲染 checkpoint <id>"`；渲染存在 → `MarkDisplayed` + `checkpoint.displayed`；自由文本停止信号 → 结束当回合、保留状态（停止不对称）；活动 run 存在 OUTCOME_UNKNOWN 项 → 留档不清理；未 CLOSED 且非紧急 stop → 提示 post-game 待办（不删状态）。

- [ ] **Step 1: 失败测试**：`TestStopBlocksUndisplayedCheckpoint`、`TestStopMarksDisplayed`、`TestStopSignalEndsTurnKeepsState`、`TestSubagentStopBackfillsReview`、`TestSubagentStopDedupsByCallID`、`TestOutcomeUnknownRetainedAcrossStop`。
- [ ] **Step 2: 确认失败** — `go test ./hooks/subagentstop/ ./hooks/stop/ -v` → FAIL
- [ ] **Step 3: 实现**（两份）
- [ ] **Step 4: 通过** — `go test ./hooks/... -v`
- [ ] **Step 5: Commit** — `feat(stop+subagentstop): display verification, stop asymmetry, review backfill`

### Task 6: hooks.json 接线 + 故障阻断契约测试

**Files:**
- Modify: `hooks/hooks.json`
- Create: `hooks/contract/contract_test.go`（Go 集成测试：以子进程真实调起各 hook 二进制）
- Create: `settings-recommended.json`（插件随附推荐 settings 片段：`askUserQuestionTimeout: "never"`、沙箱 filesystem 规则、permissions.deny 对 state/transcript 路径）

**Interfaces:**
- Consumes: Task 3/4/5 的二进制。
- Produces: hooks.json 最终接线（UserPromptSubmit=运行启动记录；PreToolUse=gate（onFailure block + timeout）；PostToolUse=登记；SubagentStop；Stop）；契约测试覆盖：缺二进制、超时、非 0/2 退出、非法 JSON → 写入被阻断；`SUMMONER_HOOKS_DISABLE=1` 逃生舱生效且记违例事件；`settings-recommended.json` 含 `askUserQuestionTimeout: "never"`（安装断言）与沙箱档位检测探针（检测到未生效 → L2 检查标记 BLOCKED 而非静默跑）。

- [ ] **Step 1: 失败测试**：`TestMissingBinaryBlocks`、`TestTimeoutBlocks`、`TestInvalidJSONBlocks`、`TestDisableEscapeHatchLogged`、`TestRecommendedSettingsAssertions`、`TestSandboxTierDetection`。
- [ ] **Step 2: 确认失败** — `go test ./hooks/contract/ -v` → FAIL
- [ ] **Step 3: 接线 + 实现**（gate hook 解析失败路径已在 Task 3 改 exit 2；本任务验证端到端语义）
- [ ] **Step 4: 通过** — `go test ./hooks/... -v`
- [ ] **Step 5: Commit** — `feat(contract): onFailure:block wiring + install contract tests + recommended settings`

### Task 7: ⑤ reviewer 新契约（信封 + 冻结验收 + 证据对账）

**Files:**
- Create: `hooks/shared/envelope.go` + `envelope_test.go`
- Modify: `agents/review-agent.md` → 重写为 `agents/reviewer.md`（frontmatter 按 spec §3；删除旧文件）

**Interfaces:**
- Consumes: Task 1 `RequestReview`；Task 5 transcript 登记。
- Produces:
  - `type Envelope struct { SchemaVersion, RunID, ReviewID string; ArtifactSet []ArtifactRef; Requirements FrozenView; ExitCriteria []Criterion; VerificationScope Scope }` —— 严格 allow-list（未知字段解析即错）；`ValidateEnvelope(env Envelope, frozen FrozenView, dropped []string) error`（criteria 集 = 冻结集 − 显式 drop 记录）。
  - `type CheckDef struct { CheckID, Command string; Args []string; Cwd string; EnvAllow []string; Digest string; Trust string }`；`DigestChanged(a, b CheckDef) bool`。
  - `ReconcileEvidence(claimed []string, transcriptPath string) error`（evidence id 必须在子 transcript tool_use 记录中）。
  - `ReviewResult` 处理：run_status COMPLETED|BLOCKED 分离；verdict 枚举校验（非法 = review.failed）；产物 digest 变更 → verdict 作废重审；同 digest+判据+环境 → 复用不重审不递归。
- agents/reviewer.md：spec §3 frontmatter（tools/disallowedTools/omitClaudeMd）+ 判据执行规程 + 结构化返回 schema。

- [ ] **Step 1: 失败测试**：`TestUnknownFieldRejected`、`TestMissingCriterionRejected`、`TestProducerCannotTrim`、`TestCheckDefDigestChangeUpgrades`、`TestEvidenceReconcile`（声称不在 transcript 的 id → err）、`TestBlockedSeparateFromVerdict`、`TestStaleArtifactVoidsVerdict`、`TestSameDigestReuse`。
- [ ] **Step 2: 确认失败** — `go test ./hooks/shared/ -run TestEnvelope -v` → FAIL
- [ ] **Step 3: 实现 envelope.go + 重写 reviewer.md**
- [ ] **Step 4: 通过** — `go test ./hooks/... -v`；`bash scripts/validate-repo.sh`（agents lint 应通过新文件名）
- [ ] **Step 5: Commit** — `feat(review): frozen acceptance view + evidence reconciliation + BLOCKED/verdict separation`

### Task 8: phase-submit 微 skill + SKILL.md 单模式协议 + checkpoint-protocol 重写 + /summoner:status

**Files:**
- Create: `skills/phase-submit/SKILL.md`（零逻辑：只规定 args=phase-result JSON 格式与"必须经此提交"；frontmatter `disable-model-invocation: true`——内部入口不参与自动触发竞争）
- Create: `cmd/summoner-status/main.go`（只读渲染：阶段路线/visit 状态/已验产物版本/未满足标准/等待谁/默认 recall/预算余量/沙箱档位）
- Modify: `skills/summoner/SKILL.md`（删除 §3 双路径与图模式段落、Trace Emission shell echo 段 → 新协议：phase-submit→展示→AskUserQuestion；新增 [v] 按需审查、recall to <label>、stop 不对称）
- Modify: `commands/{fix,new,debug,review,ship,ops}.md`（阶段链对齐 spec §1 流程；ship 改单 reviewer + 敏感加标准；review 走 verification_spec）
- Modify: `references/checkpoint-protocol.md`（重写：新 CHECKPOINT 格式含 nonce 与 [v] 选项；CHANGE_OPEN 确认点呈现影响评估+单点确认「接受变更并恢复开发」；删除 walker recall/explain 承诺段）
- Modify: `references/workflow-reference.md`（删图生成条件/fallback；ship merge 报告保留）

**Interfaces:**
- Consumes: Task 1-7 的运行时语义（文档描述它们）。
- Produces: 单模式人机协议的权威文档集；`/summoner:status` 渲染文本格式（`cmd/summoner-status` 输出，同格式嵌入检查点块）。

- [ ] **Step 1: 写失败校验** — `cmd/summoner-status/main_test.go`：`TestRenderWaitUser`（含 nonce/预算/沙箱档位字段）；`validate-repo.sh` 新增规则：checkpoint 渲染块含 nonce 字段（唯一 canonical 仍在 checkpoint-protocol.md）
- [ ] **Step 2: 确认失败** — `go test ./cmd/summoner-status/ -v` → FAIL
- [ ] **Step 3: 实现 status 二进制 + 重写五份文档/skill**
- [ ] **Step 4: 通过** — `go test ./... && bash scripts/validate-repo.sh` → PASS
- [ ] **Step 5: Commit** — `feat(protocol): single-mode skill/command/checkpoint docs + status viewer`

### Task 9: manifest v2 + validator + 生成器原子切换

**Files:**
- Modify: `summoner.schema.json`（v2：phases 能力映射 + `verification_spec` + check registry + 工具范围 + 预算；删 chain/fan_out/graph/checkpoints 枚举/routing_rules）
- Modify: `hooks/validate-manifest/main.go`（改用内联校验或更新后的库，panel 核实现依赖 memory-validator）
- Modify: `references/manifest-spec.md`、`skills/roster.yaml`（champion 描述改为 verification_spec/skill 示例；删 SDD 默认/岗位串联/固定 ship 双 persona/ce-code-review 12+ 推荐）
- Modify: `scripts/summoner-init.sh`、`scripts/summoner-bp.sh`、`scripts/summoner-setup.sh`（只生成新 manifest）
- Modify: `references/no-manifest-flow.md`（删第二执行链 → 内置默认能力进同一 reducer + 明示未配置）、`skills/auto-setup-summoner/SKILL.md`、`hooks/session-start/main.go`（指针）

**Interfaces:**
- Consumes: spec §1 manifest 语义。
- Produces: 新 manifest 格式（golden 样例 `tests/fixtures/manifests/*.yaml`）；生成器产物必须过新 validator。

- [ ] **Step 1: 失败测试** — `hooks/validate-manifest/main_test.go` 增 v2 用例（合法样例 PASS；含 chain/fan_out/graph 键 → 拒绝并提示迁移；`phases.review` 为 verification_spec 结构）；golden 测试：三个生成器对 tmp 目录产物过 validator
- [ ] **Step 2: 确认失败** — `go test ./hooks/validate-manifest/ -v` → FAIL
- [ ] **Step 3: 实现 schema + validator + 生成器重写**
- [ ] **Step 4: 通过** — `go test ./... && bash scripts/validate-repo.sh && bash scripts/summoner-init.sh --dry-run 2>/dev/null || true`（若脚本无 dry-run 则在 tmp 项目实测一次并清理）
- [ ] **Step 5: Commit** — `feat(manifest): v2 capability-map schema + atomic generator switch`

### Task 10: 需求变更闭环与 /summoner:new 拆分

**Files:**
- Modify: `commands/new.md`（保持单一运行入口；内部拆「需求管理」与「开发交付」两个能力职责，各自可绑定项目 skill）
- Modify: `skills/summoner/SKILL.md`（新增 §需求变更闭环：任意时刻触发→冻结开发位置→增量 delta→影响评估（方法由所选 skill 承担）→单点确认→原地恢复；与 recall 的区分边界）
- Modify: `cmd/summoner-status/main.go`（渲染：CHANGE_OPEN 状态、保存的开发位置、RequirementsVersion、stale 清单）+ `main_test.go`
- Modify: `references/workflow-reference.md`（变更 vs recall 边界表）
- Test: `tests/fixtures/traces/` 增 change-loop fixture（change.opened → delta_applied → checkpoint.decided(ACCEPT_REQUIREMENT_CHANGE) → change.resumed；含 stale 标注）

**Interfaces:**
- Consumes: Task 1 `OpenRequirementChange/ApplyRequirementDelta/ResumeAfterChange` 与 `ACCEPT_REQUIREMENT_CHANGE`；Task 2 `change.*` 事件。
- Produces: 变更闭环的人机协议文档；status 渲染格式（含 dev position/stale 字段）。

- [ ] **Step 1: 失败测试** — `cmd/summoner-status/main_test.go` 增 `TestRenderChangeOpen`（CHANGE_OPEN 下渲染含：保存的开发位置、RequirementsVersion、stale criterion 清单、「等待变更确认」）；change-loop fixture 进 `user-gate-check.sh` 用例（缺失/顺序错=FAIL）
- [ ] **Step 2: 确认失败** — `go test ./cmd/summoner-status/ -v && bash scripts/run-trace-fixtures.sh` → FAIL
- [ ] **Step 3: 实现** — status 渲染 + 四份文档（协议落地细节全部引 spec §1 增补，不自行发明）
- [ ] **Step 4: 通过** — `go test ./... && bash scripts/validate-repo.sh && bash scripts/run-trace-fixtures.sh`
- [ ] **Step 5: Commit** — `feat(change-loop): requirement-change loop with single-point resume, /summoner:new capability split`

### Task 11: 能力去重与四层职责落地

**Files:**
- Create: `references/capability-map.md`（能力 | 主选 provider | 被删除的 Summoner 重复实现 | 触发措辞——去重审计交付物）
- Modify: `skills/roster.yaml`（每能力恰一个 `default: true`，其余标 `alternate`；删除与 Summoner 命令重复的方法论条目与大而全推荐）
- Modify: `plugin.json` + `.claude-plugin/plugin.json`（skills/commands 注册收敛，与 capability-map 一致）
- Modify: `commands/review.md`、`commands/ship.md`（边界成文：review=找问题，ship=发布决策并引用 review 证据；薄入口化）
- Modify: `skills/summoner/SKILL.md`（capability→provider 解析节：内置优先→superpowers→项目→Summoner 控制能力）
- Modify: `scripts/validate-repo.sh`（新规则：roster 每个 phase 恰好一个 `default: true`）

**Interfaces:**
- Consumes: spec §2 四层职责与能力去重。
- Produces: `references/capability-map.md` 作为能力归属的唯一权威表（生成器 Task 9 的输出与之一致）。

- [ ] **Step 1: 失败校验** — `validate-repo.sh` 新规则先写（当前 roster 有多 default 或重复方法论条目 → FAIL）
- [ ] **Step 2: 确认失败** — `bash scripts/validate-repo.sh` → FAIL
- [ ] **Step 3: 实现去重** — 盘点内置/superpowers/项目示例与 Summoner 自有能力的重叠 → 填 capability-map → 收敛 roster/plugin 注册 → 改薄入口
- [ ] **Step 4: 通过** — `bash scripts/validate-repo.sh` → PASS；grep 确认 Summoner 不再注册与 superpowers 重名/同义方法论 skill
- [ ] **Step 5: Commit** — `feat(dedup): four-layer capability map, single default provider per capability, thin entries`

### Task 12: scorers v2 + fixtures 重写 + sidecar

**Files:**
- Modify: `scorers/deterministic/`——重写 `handoff-contract-check.sh`/`review-isolation-check.sh`/`verifier-checklist-check.sh` → `schema-check.sh`（v2 事件集+allow-list+关键控制事件）、`review-evidence-check.sh`（身份/证据连接/逐 criterion 完整性/同 digest 复用）、`user-gate-check.sh`（展示绑定+决策绑定+幂等+nonce）、`recovery-check.sh`（OUTCOME_UNKNOWN 探针/审查五状态恢复不重跑 producer/recall 语义）、`post-game-check.sh`（紧急 stop 留 POST_GAME_PENDING/正常结束才 CLOSED）
- Modify: `scripts/score-trace.sh`（评分写派生 sidecar `.score.json`，不写权威 trace；skip ≠ 满分——N/A 与缺证据严格区分）、`scripts/create-baseline.sh`、`scripts/regression-test.sh`（必需评分缺失=FAIL）
- Rewrite: `tests/fixtures/traces/`（正例 + 对抗例：伪造 verdict、注入 answers、stale 渲染、盖章审查、悬空 review）

**Interfaces:**
- Consumes: Task 2 trace schema；Task 4/5/7 语义。
- Produces: CI 可跑的 fixture 矩阵（`scripts/run-trace-fixtures.sh` 更新）。

- [ ] **Step 1: 先写对抗 fixture**（每只 scorer 至少 1 正 1 负 fixture，负例断言 FAIL 文案）
- [ ] **Step 2: 跑 fixtures 确认新 scorer 未实现/旧 scorer 全爆** — `bash scripts/run-trace-fixtures.sh` → FAIL
- [ ] **Step 3: 实现五只 scorer + sidecar 改造**
- [ ] **Step 4: 通过** — `bash scripts/run-trace-fixtures.sh && shellcheck scorers/deterministic/*.sh scripts/score-trace.sh` → PASS
- [ ] **Step 5: Commit** — `feat(scorers): v2 contract scorers + adversarial fixtures + sidecar scoring`

### Task 13: release/ops 接入同一 reducer

**Files:**
- Modify: `skills/summoner-release/SKILL.md`（manifest `phases.release` 映射 + 默认框架实现；删 `--project summoner` 硬编码取当前项目；变更阶段声明只读对账探针 tag-已存在/版本-已一致；复盘接线替换 "Not implemented yet"）
- Modify: `commands/release.md`、`commands/ops.md`（用户门/trace/复盘走同一 reducer 的说明）
- Test: `tests/fixtures/traces/` 增 release 探针三分支 fixture

- [ ] **Step 1: 失败校验** — release 探针 fixture 进 `recovery-check.sh` 用例（缺失=FAIL）
- [ ] **Step 2: 确认失败** — `bash scripts/run-trace-fixtures.sh` → 该用例 FAIL
- [ ] **Step 3: 实现文档接线**（探针规格在 SKILL.md 中为能力清单必填项——无探针声明不得进变更阶段）
- [ ] **Step 4: 通过** — fixtures 全绿 + `validate-repo.sh`
- [ ] **Step 5: Commit** — `feat(release/ops): manifest mapping, reconciliation probes, post-game wiring`

### Task 14: 删除旧实现 + lint/CI/release 闭包

**Files:**
- Delete: `cmd/summoner-walker/`、`internal/graph/`、`references/graph-mode.md`、`references/node-contract.md`、`scripts/node-snapshot.sh`、`scripts/stability-test.sh`、`agents/{code-reviewer,security-auditor,test-engineer,debug-agent}.md`、旧 trace fixtures（已被 Task 10 重写则跳过）
- Modify: `plugin.json` + `.claude-plugin/plugin.json`（agents 数组只留 reviewer；版本号三处同步 bump）、`references/persona-composition.md`（删"一岗一 persona"与过时平台断言，保留命令唯一编排+不互派纪律）、`scripts/test-summoner-optimizations.sh`、`scripts/validate-repo.sh`（死引用扫描扩展至 agents/、scripts/）、`.github/workflows/ci.yml`（新目标：`go test ./...` 含新包、contract、fixtures）、`.github/workflows/release.yml`（删 walker 构建，增 summoner-status）
- 归档: `docs/superpowers/specs/2026-07-27-graph-node-contract-design.md` 头部加"已被 2026-11-09 设计取代"注记（不删）

- [ ] **Step 1: 验证步骤先行** — `grep -rn "summoner-walker\|internal/graph\|graph-mode\|node-snapshot\|clean_context" --include="*.md" --include="*.go" --include="*.sh" --include="*.json" . | grep -v docs/superpowers/specs/ | grep -v "\.git/"` 应列出的每一处都在删除/修改清单内
- [ ] **Step 2: 执行删除与修改**
- [ ] **Step 3: 全量回归** — `go test ./... && bash scripts/validate-repo.sh && bash scripts/run-trace-fixtures.sh && shellcheck scripts/*.sh` → 全绿；Step 1 的 grep 复跑 → 零残留（specs 归档除外）
- [ ] **Step 4: Commit** — `refactor!: single-mode switch — delete walker/graph/persona roster, close lint/CI/release`

### Task 15: 行为评测 harness（模型运行预算门留给用户检查点）

**Files:**
- Create: `evals/paired/README.md`（五组对照的用例设计：①逐节点⑤+岗位编排 vs 主循环+终局⑤+人类门 ②多 persona vs 单 rubric reviewer ③分片并行 vs 顺序 ④隔离 worker vs 折叠 ⑤独立⑤ vs 自审；指标：阻断缺陷召回/误报/标准遗漏/重复搜索/协调量/时长；训练/留出分置）
- Create: `evals/paired/cases/`（冻结输入 + 已知缺陷标注 + 干净负例）
- Create: `scripts/run-paired-eval.sh`（双臂运行 + 指标汇总到 `evals/paired/results/`；`--arm baseline` 使用 git tag v0.3.0 检出说明，`--arm candidate` 当前分支）
- Create: `evals/paired/tolerances.yaml.example`（非劣容忍度/关键缺陷要求/停止条件——**由用户在运行前裁决**）

**Interfaces:**
- Consumes: spec §7 行为评测放行门。
- Produces: 可运行的对照 harness；实际模型运行与预算在执行到本任务后的检查点向用户申请。

- [ ] **Step 1: 用例集与 harness 脚本实现**（不含模型运行）
- [ ] **Step 2: 冒烟** — `bash scripts/run-paired-eval.sh --list` 列出全部用例与双臂配置 → PASS；`shellcheck scripts/run-paired-eval.sh`
- [ ] **Step 3: Commit** — `test(evals): paired-comparison harness for fold-and-measure gate`
- [ ] **Step 4: 停在检查点** — 向用户提交：预算申请 + tolerances 空表 + 五组对照的运行顺序建议；获准后运行并出报告（非劣 → 发布单模式；劣 → 回炉 rubric/检查，不复活双路径）

---

## 执行顺序与依赖

Task 1→2→3→4→5 严格顺序（reducer/trace 是一切 hook 的地基）；Task 6 依赖 3-5；Task 7 依赖 1/5；Task 8 依赖 1-7（文档描述运行时）；Task 9 依赖 8（生成器与协议一致）；Task 10 依赖 1/2/8（变更闭环用 reducer 转换与 change.* 事件）；Task 11 依赖 9（roster/生成器同源收敛）；Task 12 依赖 7/10（fixtures 覆盖变更闭环）；Task 13 依赖 8/12；Task 14 必须最后（原子删除）；Task 15 在 14 后。

## Self-Review 结论

- Spec 覆盖：spec §1-§7 各节分别由 Task 3/8（§1 协议）、Task 8/9（§2 边界与 manifest）、Task 7（§3 契约）、Task 5/8（§4 检查点）、Task 3-6（§5 强制层）、Task 14（§6 删除）、Task 12/15（§7 评测）承载；§1 增补（需求变更闭环）由 Task 1（转换）+2（事件）+10（协议与拆分）承载；§2 增补（四层职责与去重）由 Task 11 承载；§0 四项用户裁决分别落在 Task 14（删 walker）、Task 8（⑤可跳过标 UNREVIEWED）、Task 6（沙箱 BLOCKED）、Task 5/8（stop 最小 journal）。
- Review Focus 五项均有 owning task 测试。
- 残余风险已知：AskUserQuestion 注入面在平台层不可完全区分——Task 6 契约测试覆盖可观测部分，残余在 spec §1 声明。
