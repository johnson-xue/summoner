# Summoner Graph & Node-Contract Upgrade — Design Spec

> Date: 2026-07-27
> Source article: 煎鱼《Loop Engineering is dead. Long live Graph Engineering?》(WeChat, 2026-07)
> Spec author: brainstorming session, approved direction = 方案 A (incremental compat)

## 1. Problem & Motivation

### 1.1 What the article argues

The article traces the hierarchy Prompt ⊂ Context ⊂ Harness ⊂ Loop ⊂ Graph, and makes four load-bearing claims that Summoner should internalize:

1. **The bottleneck of any loop is the verifier (stop condition).** `all tests pass` is decidable; `把代码写得好一点` is not. A loop with an undecidable verifier either runs forever or quits early.
2. **Graph engineering = moving control flow out of the model's ad-hoc judgment and into a structure you can read, test, and breakpoint** — via `add_conditional_edges(route_fn)`. The graph defines which paths are legal, where the model may choose, where a deterministic branch must run — decided *before* `compile()`.
3. **The loop is a special case of the graph, not its predecessor.** Production agent graphs are usually *not* DAGs: retries, missing-info follow-ups, post-verify rework, human-confirm-then-continue are all cycles. Every node, when it runs, is still a loop.
4. **What changed in three years is what can live in a node.** Three years ago a node could only be a single LLM call. Now a node can be a full agent run with its own tools, retries, and inner loop. **The unit of orchestration moved from "an LLM call" to "an agent."** Coding agents are the canonical beneficiary.

The article's closing self-check — "is this actually graph engineering, or a loop wearing a graph skin?" — rests on three tests: (a) are contexts *really* isolated between nodes, (b) is there real parallel fan-out/fan-in, (c) does the verdict have *evidence external to the system*. Failing all three means only the nouns changed.

### 1.2 Where Summoner stands today

Summoner already has more graph-nature than a naive loop, but it does not *declare* its control flow as a readable graph, and its nodes are still "one skill call," not "a closed-loop agent":

| Article dimension | Summoner today | Gap |
|---|---|---|
| Stop condition / verifier | Phase 4 `verify` runs the test suite (decidable); Phase 5 `review` is human/agent free-form (largely undecidable) | `review`'s acceptance is not machine-decidable — falls in the "判不了" bucket |
| Conditional routing | `fix` Phase 3 routes by diagnosis (logic/rpc/subsystem/migrate/gmt); `new` Phase 3 by function type; `ship` adaptive fan-out by diff size | Routing lives in *prose* inside `commands/*.md`, not in a structure you can read/test as a graph |
| Back-edge (cycle) | checkpoint `[recall]` returns to the *previous* phase only | Cannot skip a node when returning (article's red edge: reject → writer, *skipping* researcher) |
| Parallel fan-out/fan-in | `ship`: 3 personas in parallel → merge → go/no-go | Already real; can be generalized |
| Context isolation | Each phase invokes an independent skill; personas each have fresh context | `fix`'s reproduce→fix→verify actually share one session context — debug output and half-baked diffs flow through; the verdict node is not protected from upstream pollution |
| Interface written down | checkpoint protocol fields fixed, 5 options fixed order, signal grammar fixed | Solid — extend it rather than replace |
| Memory isolation | per-project SQLite namespace | Already isolated; reused as-is |
| Graph not bound to framework | framework verbs fixed + project skills replaceable (Makefile-like) | Solid — preserved as an invariant |

Summoner also already ships real, implemented infrastructure that this upgrade must build on rather than duplicate:
- `references/trace-protocol.md` — JSONL trace events (session/phase/tool/reasoning/checkpoint/error).
- `references/scoring-system.md` — P0–P2 three-tier scoring, deterministic scorers (`scorers/deterministic/*` are real bash scripts), rubric scorers, baseline + `regression-test.sh` + `stability-test.sh`.
- `references/persona-composition.md` — the iron rule **"personas never call other personas"**; orchestration is the command layer's job.
- `references/post-game-review.md` — Type 1 (direction correction) / Type 2 (skip) / Type 5 (verbosity) already capture the *human* signals of reject-and-redo.

### 1.3 The core problem (in the user's words)

> "skill 在进行操作后上下文没有隔离也没有对结果进行校验，完全靠个人阅读分析来判断，如果人疏忽就会出现问题，非常容易遗漏。"

Today a node (= a phase = a skill call) does its work and hands the entire session context to the next node. There is **no machine-checked acceptance** at the node boundary, and **no clean handoff**. Correctness leans entirely on a human reading the checkpoint block. When the human is tired, things slip through. The fix the article prescribes — and this spec implements — is to upgrade every node from "a skill call" into "a closed-loop agent that (a) validates what it received, (b) does the work, (c) self-tests with a decidable verifier, (d) hands off a clean, minimal, typed context to the next node — and can reject an upstream handoff back along a back-edge."

## 2. Design (Headline: the Node Contract)

The single headline change: **a Summoner node is no longer "one skill call." A node is a closed-loop agent with a five-step contract (① Ingest+Validate → ② Work → ③ Test → ④ Handoff → ⑤ Review-agent).** The graph is just the composition of such nodes. The chosen borrow-points (① explicit graph declaration / ② skip-node back-edge / ③ verdict-node context isolation / ④ decidable verifier) are all absorbed into one node contract rather than kept as four loose features — and the user's added direction (offload the human quality-read onto an independent-context review agent) is absorbed as step ⑤.

### 2.1 The Node Contract (every node = agent run, 4 steps)

```
        ┌──────────────────────────────────────────────┐
        │  NODE (agent) — closed loop                   │
        │                                              │
  in ─▶ │  ① Ingest+Validate   (handoff envelope check)│ ── REJECT ─▶ back-edge to upstream
        │  ② Work              (the task itself)        │
        │  ③ Test              (node-internal verifier │ ── FAIL ─▶ self-retry (bounded)
        │                        decidable; inner loop)│
        │  ④ Handoff           (clean, minimal, typed) │
        │  ⑤ Review-agent      (SEPARATE context, reads │ ── NEEDS-FIX ─▶ back-edge
        │                        only the ④ envelope,      (with findings as reason)
        │                        judges exit_criteria)    │
        └──────────────────────────────────────────────┘
                          │  (⑤ = PASS)
                          ▼
                  Summoner checkpoint — human FLOW gate
                  (continue / recall to <node> / skip / done / stop)
                  review agent judged quality; human judges direction
```

| Step | Does | Decidable? | Absorbs borrow-point |
|---|---|---|---|
| ① Ingest+Validate | Receive the upstream handoff envelope; check that declared artifacts/fields/exit-criteria are present and well-formed. If not → emit `handoff_reject` and route back along the back-edge. | Yes (schema/structural checks) | ② (consumer side) + ③ (consumer side) |
| ② Work | Execute the closed-loop task (diagnose / reproduce / fix / verify / review / define / plan / implement / test / fan-out persona). | n/a | — |
| ③ Test | Run a **machine-decidable** node-internal verifier (test suite / lint / typecheck / contract check / build). If FAIL → retry within the node (bounded; cap via `max_inner_turns`), feeding the verifier feedback into the node's own context. This is the article's "every node is still a loop," kept *inside* the node. | Yes | ④ + article §"node is still a loop" |
| ④ Handoff | Produce a **clean, minimal, typed** context for the downstream node — strip upstream raw data (raw HTML, full debug dumps, half-baked diffs that aren't the artifact), keep only validated artifacts + a structured handoff note. | Yes (handoff schema) | ③ (producer side) + ② (producer side) |
| ⑤ Review-agent | A **separate-context** review agent reads only the ④ handoff envelope (never the node's working context) and judges product quality against the node's `exit_criteria`: PASS, or NEEDS-FIX with concrete findings (file:line + fix). Its verdict is appended to the handoff as `review_verdict`. | Pinned-rubric (decidable where exit_criteria are deterministic; explicitly-labeled soft otherwise) | replaces human "read the checkpoint & judge quality" — the original pain point |

Only after ⑤ does the Summoner checkpoint fire. **What changed vs. today:** the *quality* judgment that used to lean on a human reading the checkpoint block is now done by an independent-context review agent (⑤) — this directly removes the "人疏忽就遗漏" failure mode the user named in §1.3. **What is preserved:** the checkpoint's *flow* decision (continue / recall to \<node\> / skip / done / stop) is still the human's — the review agent judges quality, it does not choose the next node. **Iron law refined:** the node may self-loop internally (③) and may be sent back by ⑤'s NEEDS-FIX (which becomes a `handoff_reject`-equivalent back-edge), but every *inter-node edge advance* still passes a human-gated checkpoint. The graph defines routing; the review agent gates quality; the human gates flow.

### 2.2 The Handoff Envelope (typed, following market best practice)

Concretize "clean context" as a typed envelope (LangGraph handoff / OpenAI Agents SDK handoff / DSPy module boundary all converge on this shape):

```jsonl
{
  "type": "handoff",
  "from_node": "diagnose",
  "to_node": "fix",
  "artifacts": ["docs/reviews/2026-07-27-task-npe.md", "player/task/task.go:142"],
  "exit_criteria": ["root_cause_identified", "fix_approach_stated"],
  "handoff_note": "TaskModule.handle() 未判空 player.SubTask；离线触发 NPE。Phase 3 建议路由 antia-logic。",
  "stripped": ["raw_grep_output", "full_stack_trace"],
  "review_verdict": {"reviewer": "review-agent:diagnose", "verdict": "PASS", "findings": []}
}
```

- `artifacts` — concrete, validated products the downstream can rely on (paths, file:line, decisions). Never empty.
- `exit_criteria` — the machine-checkable list this node claims to have satisfied (see §2.4). The downstream's ① Ingest validates *these*.
- `handoff_note` — one-paragraph structured summary; the only prose that crosses the boundary.
- `review_verdict` — filled by ⑤ Review-agent. `PASS` or `NEEDS-FIX` + findings (each finding: file:line + concrete fix). On `NEEDS-FIX` the orchestrator routes a back-edge to `from_node` with the findings as reason (before the checkpoint, so the human at the checkpoint sees a quality-clean product).
- `stripped` — what was intentionally dropped (auditable: a verifier can confirm these didn't leak into the downstream context).

This is a **new trace event type** (`handoff`), not a runtime object — Summoner's runtime is markdown protocol + trace + scorers, so the envelope is emitted to the trace and enforced by scorers, not by a Python state machine.

### 2.3 Back-edge upgrade (②): `recall to <node>`, executor = orchestrator

The current `[recall]` returns only to the previous phase. Upgrade to:

```
recall to <node>  reason=receiver_rejected_handoff | direction_wrong | verifier_failed
```

- May cross nodes (skip intermediate nodes) — the article's red edge (reject → writer, *skipping* researcher).
- May be triggered two ways: (a) **human** at a checkpoint ("方向不对，回 diagnose"), already captured by checkpoint content-feedback grammar; (b) **downstream node's ① Ingest** rejecting an upstream handoff — emits `handoff_reject`, and the **orchestrator (command/SKILL layer, the graph-walk flow)** routes back. It is *not* the downstream agent spawning the upstream agent — that would violate `persona-composition.md`'s "personas never call other personas" and Claude Code's "subagents cannot spawn subagents" platform limit. The orchestrator (main session / SKILL.md flow) executes the back-edge, exactly like Alexey's example where the orchestrator (`process.md`) runs step 5 and 7.
- The reject → upstream-redo flow is a real cycle, not a DAG. This matches the article: production agent graphs are usually cyclic.

### 2.4 Decidable verifier checklist (④) — the article's named bottleneck

The article says the loop's bottleneck is always the verifier, and `review` today is mostly undecidable. Fix by giving each verdict node a **machine-checkable exit-criteria list** modeled on the article's QA output format:

```markdown
## Node: verify (fix Phase 4)
Exit criteria (all must pass — PASS/FAIL, no prose verdict):
- [ ] `go test ./...` exit 0     (deterministic scorer: test-pass-rate)
- [ ] no new lint errors          (deterministic scorer: lint-check)
- [ ] build succeeds              (deterministic scorer: build-check)
- [ ] edited files match diagnose root_cause scope (rubric, but pinned to a file:line diff)
Verdict: PASS only if all boxes checked; otherwise FAIL with the failing box as handoff-reject reason.
```

```markdown
## Node: review (fix Phase 5)
Exit criteria:
- [ ] code-reviewer persona returned 0 Critical findings (deterministic: parse persona report)
- [ ] every Critical/Important finding has a file:line + concrete fix (deterministic: report-shape check)
- [ ] [project-specific] no auth/data/config boundary touched without explicit ack (rubric, pinned)
Verdict: PASS / NEEDS-FIX (NEEDS-FIX → back-edge to fix with the finding as reason).
```

"把代码写好一点" type criteria are explicitly **banned** from exit-criteria lists — if a criterion can't be checked, it doesn't belong in the verifier; it belongs in a *suggestion* tier, not the verdict. This is the article's `verifier.check()` discipline made literal.

### 2.5 Per-task graph declaration (①) — graph produced at plan time

Borrow-point ① is **not** "predeclare a per-project workflow graph in `summoner.yaml`." It is: **the Phase 2 `plan` step (writing-plans) decomposes the task into small closed loops, each a node, and writes out that per-task graph explicitly** — nodes, edges, back-edges, and which exit-criteria each node uses. This graph is written *before* execution, readable as a graph, and the execution follows it. This satisfies the article's "control flow readable as a graph, routing pre-defined" even though it is per-task, not per-project.

The graph is declared in the plan artifact (`docs/superpowers/plans/<date>-<topic>.md`, produced by writing-plans) in a small, greppable block:

```yaml
# summoner-task-graph (per-task, plan-time)
nodes:
  - id: diagnose
    skill: phase.debug        # from summoner.yaml phases.*
    exit_criteria: [root_cause, fix_approach]
    max_inner_turns: 3
  - id: reproduce
    skill: phase.test
    exit_criteria: [repro_test_written, repro_test_fails_before_fix]
    max_inner_turns: 2
  - id: fix
    skill: antia-logic        # routed by diagnose outcome (conditional edge)
    exit_criteria: [diff_applied, no_compile_error]
    max_inner_turns: 4
  - id: verify
    skill: phase.verify
    clean_context: true       # verdict node — isolated context
    exit_criteria: [tests_pass, no_new_lint, build_ok]
    max_inner_turns: 1
edges:
  - {from: diagnose, to: reproduce}
  - {from: reproduce, to: fix}
  - {from: fix, to: verify}
conditional_edges:
  - {from: diagnose, route: route_by_diagnosis, to: [fix, rpc, subsystem, migrate, gmt]}
back_edges:
  - {from: verify, to: fix, reason: verifier_failed}       # skip nothing
  - {from: review, to: fix, reason: receiver_rejected_handoff, skip: [verify]}  # skip verify
checkpoints: after_node      # human FLOW gate after every node's ⑤ (review agent already gated quality)
```

Key points:
- `nodes[].skill` references `summoner.yaml` `phases.*` — no project/domain name hardcoded in the framework.
- `conditional_edges` mirrors the article's `add_conditional_edges(route_fn)` — a named routing rule, not model ad-hoc judgment. The existing `fix` Phase 3 routing table (logic/rpc/subsystem/migrate/gmt) becomes a named `route_by_diagnosis` rule.
- `back_edges[].skip` realizes borrow-point ② (skip intermediate nodes on return).
- `clean_context: true` on verdict nodes (verify/review) realizes borrow-point ③ — the orchestrator starts that node's agent with a handoff envelope only, not the upstream session context.
- `max_inner_turns` realizes the article's `MAX_TURNS` backstop against infinite token burn inside a node's self-loop.

### 2.6 Manifest role (narrowed, incremental-compat)

`summoner.yaml` does **not** predeclare workflow graphs. It declares:
1. Available **node types** (which skills/personas can act as a node) — reuse existing `phases` map.
2. The **node contract** (reference to `references/node-contract.md`) — framework-level, same for all projects.
3. **Conditional routing rules** available to plans (e.g., `route_by_diagnosis`) — project-level, declared once, reused across plans.

Existing `chain` / `fan_out` / `checkpoints` schema is **preserved unchanged** as the legacy/fallback path. A plan that produces a `summoner-task-graph` block runs the graph-walk flow (SKILL.md walks the per-task graph; no new runtime); a plan that doesn't falls back to today's chain behavior. **No existing `summoner.yaml` breaks.** This is the user-approved 方案 A (incremental compat) and also satisfies the article's "don't use a graph where a loop suffices" — simple per-task graphs can still just be a chain.

### 2.7 What stays unchanged (invariants)

These Summoner iron laws are **not** relaxed by the graph upgrade:
1. **Phase 1 is iron law** — the `diagnose` node's `exit_criteria` includes `root_cause`; `conditional_edges` may not bypass `diagnose`; back-edges may not skip *into* skipping diagnose.
2. **No auto-advance past a checkpoint — the human retains the *flow* decision.** The review agent (⑤) judges *product quality*; the *flow* decision at the checkpoint (continue / recall to \<node\> / skip / done / stop) is still the human's. What is automated: node-internal self-loops (③) and the ⑤ quality gate (NEEDS-FIX auto-routes a back-edge before the human ever sees a sub-par product). Both are bounded — ③ by `max_inner_turns`, ⑤ by a ≤3 same-finding-reject escalation to checkpoint.
3. **No hardcoded project/domain names** — graph blocks reference `phases.*` skills; routing rules are project-declared in `summoner.yaml`, not framework-baked.
4. **Agents never call other agents** — back-edges (including ⑤ NEEDS-FIX) are executed by the orchestrator (command/SKILL layer), never by the review agent spawning an upstream node, and never by a node spawning its own reviewer. The review agent only *reads* the ④ envelope and *returns a verdict*; the orchestrator acts on it. (Extends `persona-composition.md`'s "personas never call other personas" to all agents, consistent with Claude Code's "subagents cannot spawn subagents" platform limit.)
5. **Post-game review is mandatory** — at workflow end, the existing 5-type questionnaire fires. New Type-1 triggers (`handoff_reject` and ⑤ NEEDS-FIX events) feed the reject-redo signal into memory, same as human "方向不对" today.
6. **Review-agent context isolation is load-bearing** — ⑤ runs in a separate context that sees *only* the ④ handoff envelope, never the producing node's working context. This is the whole point: it cannot be lulled by the producer's reasoning, only by the artifacts — which is exactly the article's "verdict needs evidence external to the system." A ⑤ that read the producer's context would degenerate into "agent stamps its own work."

## 3. Components Touched

| Component | Change | New? |
|---|---|---|
| `references/node-contract.md` | The 5-step contract (incl. ⑤ Review-agent), handoff envelope + `review_verdict`, decidable exit-criteria discipline, review-agent context-isolation rule | **New** |
| `references/manifest-spec.md` | Add §Node Types + §Conditional Routing Rules; mark `graph` block as plan-time (not manifest-time) | Edit |
| `references/checkpoint-protocol.md` | Extend `[recall]` → `[recall to <node> reason=...]`; reframe checkpoint as *human FLOW gate* (quality already gated by ⑤); add verdict-node `clean_context` entry behavior | Edit |
| `references/workflow-reference.md` | Add §Per-task Graph section; document graph-walk vs chain fallback; add graph red flags (incl. "review agent read producer's context = fail") | Edit |
| `references/trace-protocol.md` | Add event types: `handoff`, `handoff_reject`, `node_test_loop`, `node_turn`, `review_verdict` | Edit |
| `references/scoring-system.md` | Add P0 scorers: `handoff-contract-check`, `verifier-checklist-check`, `review-isolation-check`; wire into regression/stability | Edit |
| `scorers/deterministic/handoff-contract-check.sh` | Validate every inter-node edge has a `handoff` event with non-empty `artifacts`+`exit_criteria`+`handoff_note`+`review_verdict`; flag missing/stripped-leak | **New** |
| `scorers/deterministic/verifier-checklist-check.sh` | For each node, confirm all exit_criteria boxes checked PASS or an explicit FAIL-with-reason; ban "判不了" criteria | **New** |
| `scorers/deterministic/review-isolation-check.sh` | Confirm every ⑤ Review-agent was dispatched as a separate subagent (separate context) and its input set ⊆ the ④ envelope fields; flag any producer-context leak into the review input | **New** |
| `agents/review-agent.md` | Generic per-node quality reviewer persona: reads only the ④ envelope, judges `exit_criteria`, returns `review_verdict` (PASS / NEEDS-FIX + file:line findings). Never reads producer context; never calls other agents; returns verdict only. | **New** |
| `skills/summoner/SKILL.md` | Phase Execution: when a plan carries a `summoner-task-graph`, walk it per node (①→②→③→④→⑤), dispatch ⑤ as a subagent with envelope-only input, route ⑤ NEEDS-FIX as a back-edge via orchestrator; else chain fallback | Edit |
| `commands/fix.md`, `commands/new.md` | Move the routing tables into named `route_*` rules referenced by graph blocks; commands become thinner | Edit |
| `skills/summoner-writing-plans` (or superpowers:writing-plans integration) | Plan artifact must emit a `summoner-task-graph` block for non-trivial tasks | Edit/Note |
| `tests/fixtures/traces/` | Add graph-mode fixtures: a clean pass (⑤ PASS), a ⑤ NEEDS-FIX→back-edge case, a ③ FAIL→retry case, a review-isolation-violation case | **New** |

## 4. Data Flow (one fix workflow, graph mode)

```
User: /summoner:fix "SC_ErrInnerLogic nil pointer in task"
  │
  ├─ Phase 0: memory retrieval (unchanged, ≤1500 tok)
  │
  ├─ Plan (writing-plans) decomposes into summoner-task-graph:
  │     diagnose → reproduce → fix → verify → review, with back_edges
  │
  ▼ Graph walk (no new runtime — the SKILL.md markdown-protocol flow
    follows the plan's summoner-task-graph block; trace+scorers enforce it):
  ┌─ diagnose ─────────────────────────────────────────────────┐
  │ ① Ingest: user input + memory patterns (envelope from Phase 0)│
  │ ② Work: antia-debug skill → root cause                       │
  │ ③ Test: exit_criteria[root_cause] = "file:line + hypothesis │
  │        stated" — a *pinned rubric* (soft, explicitly labeled │
  │        as non-decidable; scorer surfaces it, never masks it)│
  │        retry if the pin (file:line) is missing (≤3 turns)   │
  │ ④ Handoff: envelope{artifacts:[report.md, task.go:142],    │
  │            exit_criteria:[root_cause,fix_approach], note}    │
  │ ⑤ Review-agent (separate context, envelope only):            │
  │     PASS → review_verdict stamped                            │
  │     NEEDS-FIX → back-edge to diagnose (orchestrator routes)  │
  └─────────────────────────────────────────────────────────────┘
       │ checkpoint — human FLOW gate (continue/recall to <node>/stop)
       │   note: human sees a ⑤-PASS product; quality already gated
       ▼
  ┌─ reproduce ─────────────────────────────────────────────────┐
  │ ① Ingest: validate diagnose envelope + review_verdict=PASS ✓ │
  │ ② Work: write repro test                                      │
  │ ③ Test: repro test FAILS before fix (Prove-It) — decidable   │
  │ ④ Handoff: envelope{artifacts:[repro_test.go], criteria}    │
  │ ⑤ Review-agent: PASS / NEEDS-FIX → back to reproduce        │
  └─────────────────────────────────────────────────────────────┘
       │ checkpoint
       ▼
  ┌─ fix (routed by conditional edge route_by_diagnosis → antia-logic) ┐
  │ ① Ingest: validate reproduce envelope + review_verdict=PASS ✓  │
  │ ② Work: apply fix                                              │
  │ ③ Test: build + compile clean (decidable); retry ≤4 turns     │
  │ ④ Handoff: envelope{artifacts:[task.go diff], exit_criteria}   │
  │ ⑤ Review-agent: PASS / NEEDS-FIX → back to fix (w/ findings)  │
  └────────────────────────────────────────────────────────────────┘
       │ checkpoint
       ▼
  ┌─ verify (clean_context: true — fresh agent, envelope only) ┐
  │ ① Ingest: validate fix envelope + review_verdict=PASS ✓     │
  │ ② Work: run test suite                                        │
  │ ③ Test: tests_pass + no_new_lint + build_ok — all decidable  │
  │   FAIL → self-retry; exhausted → back-edge to fix             │
  │ ④ Handoff: envelope{verdict:PASS, test_results}              │
  │ ⑤ Review-agent: PASS / NEEDS-FIX → back to verify or fix     │
  └─────────────────────────────────────────────────────────────┘
       │ checkpoint
       ▼
  ┌─ review (clean_context: true) — the code-reviewer PERSONA node │
  │ ① Ingest: validate verify envelope + review_verdict=PASS ✓    │
  │ ② Work: code-reviewer persona (5-axis)                         │
  │ ③ Test: 0 Critical + every finding has file:line+fix — decidable│
  │ ④ Handoff: envelope{verdict:PASS, report}                     │
  │ ⑤ Review-agent (meta-review of the persona's report):         │
  │     PASS / NEEDS-FIX → back to review node                    │
  └────────────────────────────────────────────────────────────────┘
       │
       ▼ Post-game review (5-type; handoff_reject + ⑤ NEEDS-FIX → Type 1 memory)
```

## 5. Error Handling

- **Node self-loop exhausted (③ hits `max_inner_turns`):** node emits `node_test_loop` with `exhausted=true`; orchestrator surfaces at checkpoint as `⚠️ 发现: <node> self-loop exhausted (N turns)` and offers `[recall to <upstream>]` / `[stop]`. Never silently burns unbounded tokens — the article's `MAX_TURNS` backstop.
- **Handoff reject (① fails):** `handoff_reject` event; orchestrator routes back-edge; the reject reason becomes the upstream node's next Ingest input. If a back-edge loops ≥3 times on the same reject reason, escalate to checkpoint (likely a real direction problem → Type 1 review).
- **⑤ Review-agent returns NEEDS-FIX:** orchestrator routes a back-edge to `from_node` with the findings as reason — *before* the checkpoint. The human at the checkpoint should only ever see a ⑤-PASS product (the whole point of offloading the quality read). If ⑤ NEEDS-FIX loops ≥3 times on the same finding, escalate to checkpoint (⑤ may be misreading, or the producer can't satisfy the criterion — human decides).
- **⑤ Review-agent context leak (it somehow saw producer context):** `review-isolation-check` scorer flags it; that node's `review_verdict` is voided and the node re-runs ⑤ with a correctly-scoped envelope. This is a correctness defect, not a flow decision — never surfaces to the human as "your call."
- **⑤ Review-agent unreachable / dispatch failure:** treat as ③-exhausted equivalent — surface at checkpoint, offer `[recall to <upstream>]` / `[stop]`. Never block the workflow silently.
- **Verdict FAIL with no upstream fix node (e.g., review rejects on a debug-only workflow):** no back-edge target → surface to user as a checkpoint decision, don't fabricate a target.
- **Graph parse failure (malformed `summoner-task-graph` block):** fall back to chain mode + warn at checkpoint ("plan graph malformed — running chain fallback"). Never block the user.
- **Missing manifest:** graph blocks can't resolve `phases.*` skills → existing No-Manifest menu (Phase 3) handles it; graph mode simply isn't entered.
- **Trace write failure:** existing `SUMMONER_NO_TRACE` semantics; scorers emit SKIP, never block workflow.

## 6. Testing Strategy (multi-case, per the goal)

Cases must demonstrate the upgrade *and* prove "smoother / higher correctness / less human intervention" via the existing scoring + regression infrastructure:

| Case | Exercises | Expected new-behavior | Measured by |
|---|---|---|---|
| C1: config-only fix (1-line) | Graph-vs-chain fallback (article: don't graph a loop) | Plan emits minimal chain (no reproduce/verify graph nodes); fast path; ⑤ still runs on the single node | token usage, phase count |
| C2: nil-pointer logic fix | Full graph + verdict node isolation + per-node ⑤ | every node ⑤ PASS; verify runs in clean context; no debug-dump leak; human only sees PASS products | `handoff-contract-check` + `review-isolation-check`; context-leak assertion |
| C3: fix where verify FAILS | ③ FAIL→retry then back-edge (②) skip-node | verify ③ FAIL → self-retry → exhausted → back to fix, *skipping reproduce*; ⑤ on fix NEEDS-FIX also back-edges | `verifier-checklist-check`; back-edge trace assertion |
| C4: ⑤ Review-agent NEEDS-FIX catches a defect the human would have missed | The headline benefit — agent替人读 | ⑤ on `fix` returns NEEDS-FIX (e.g., missed nil-check caller); orchestrator back-edges *before* checkpoint; human never sees the defective handoff | `review_verdict` NEEDS-FIX trace; back-edge-before-checkpoint assertion; old-Summoner would have let it through (control) |
| C5: ⑤ context-isolation violation (adversarial) | Reviewer must not read producer context | inject a producer-context field; ⑤ must reject/ignore; `review-isolation-check` flags if it leaks | `review-isolation-check` scorer FAIL on leak, PASS when isolated |
| C6: new subsystem feature | Per-task graph from plan + conditional edge | plan emits `summoner-task-graph`; route_by_function_type conditional edge | graph-block presence; routing-rule reference |
| C7: ship fan-out | Existing parallelism under graph framing | 3 personas as 3 nodes, merge node; clean_context on each; merge node has its own ⑤ | fan-out trace; merge envelope |
| C8: node self-loop exhaustion | `max_inner_turns` backstop | fix node ③ retries then escalates at checkpoint (no infinite burn) | `node_test_loop exhausted=true` trace; token ceiling |
| C9: ⑤ 3× same-finding escalation | Bounded auto-redo | ⑤ NEEDS-FIX loops 3× on same finding → escalates to checkpoint instead of looping forever | escalation trace; no >3 same-finding back-edges |

For each case: run under **old (chain) Summoner** and **new (graph+⑤) Summoner**, capture traces, run `regression-test.sh` + `stability-test.sh` (≥5 runs, fix workflow 0% tolerance per scoring-system), and report Δ on: P0 score, **human-intervention count** (checkpoint content-feedback + recall events — the key metric, since ⑤ offloads the quality read from the human), token usage. The goal's "smoother / higher correctness / less human intervention" is quantified as: P0 score ↑, human-intervention count ↓ (most directly via C4 — defects ⑤ catches before the human), token neutral-or-down.

## 7. Out of Scope (YAGNI)

- No new runtime engine (no Python/Go graph executor). The graph is walked by the SKILL.md markdown-protocol flow + trace/scorer enforcement — consistent with how Summoner already works.
- No per-project predeclared workflow graphs in `summoner.yaml` (only node types + routing rules). Per-task graphs live in plan artifacts.
- No replacing the checkpoint human *flow* gate. The review agent (⑤) offloads *quality* judgment from the human; the *flow* decision (continue/recall/skip/done/stop) stays human. The two are deliberately not merged.
- No review agent making flow decisions. ⑤ returns PASS/NEEDS-FIX only; it never picks the next node. (If a future need arises for agent-decided flow, that is a separate spec — explicitly out of scope here.)
- No auto-promotion of memory patterns into skills (existing manual review path stays).
- No new *domain* personas. One generic `review-agent` persona is added (⑤ is the same role per node, parameterized by the node's `exit_criteria`); the existing 4 domain personas become node-capable as-is.

## 8. Risks & Mitigations

| Risk | Mitigation |
|---|---|
| Handoff envelope becomes ceremony / token overhead | Envelope is ≤120 tokens; `stripped` is audited not echoed; scorers enforce brevity |
| Graph mode adds friction to simple tasks (violates "don't graph a loop") | Plan emits chain (not graph) for trivial tasks (C1); graph is opt-in per plan |
| Conditional routing rules duplicated between `summoner.yaml` and `commands/*.md` | Single source of truth: rules declared in manifest, referenced (not redefined) in commands |
| Back-edge loops forever | `max_inner_turns` per node + 3-reject-same-reason escalation to checkpoint |
| Scorers give false confidence (rubric "pinned to file:line" still soft) | Iron law preserved: deterministic scorers dominate; rubric only where no deterministic check exists; human calibration stays |
| "判不了" criteria sneak back into exit_criteria | `verifier-checklist-check.sh` explicitly flags any criterion not matchable to a deterministic scorer or a pinned structural check |
| ⑤ Review-agent rubber-stamps (agent stamps its own work) | ⑤ runs in **separate context** with envelope-only input (invariant #6); `review-isolation-check` scorer enforces it; C5 adversarially tests it |
| ⑤ Review-agent too strict → loops on nitpicks | ⑤ judges only the node's declared `exit_criteria`, not free-form quality; 3× same-finding escalation (C9) surfaces stuck loops to the human |
| ⑤ adds latency/tokens per node | ⑤ uses a fast model on a tiny envelope (≤120 tok in, ≤80 tok verdict); for C1 trivial path, ⑤ is the single node's only quality gate (cheaper than full chain) |

## 9. Success Criteria (matches the goal)

The upgrade succeeds when, across cases C1–C9:
1. **Correctness ↑:** new Summoner P0 score ≥ old, on identical tasks (regression-test non-regressing, stability-test passing at 0% tolerance for fix).
2. **Human intervention ↓:** fewer checkpoint content-feedback + recall events per task in new vs old (measured from trace `checkpoint` + `handoff_reject` + ⑤ NEEDS-FIX events). This is the headline win — ⑤ catches defective handoffs *before* the human would have had to read them.
3. **Smoother:** post-game Type-4 rating ≥ old; Type-1 (direction correction) frequency ↓ (reject-redo handled inside graph before reaching the human).
4. **Real-world:** at least one case run against a real bug in this repo (not only synthetic fixtures), demonstrating ⑤ catching a defect the old chain would have let through to the human reviewer (C4 control comparison).
