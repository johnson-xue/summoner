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
        │  ⓪ Pre-Work snapshot (mutating nodes only):  │
        │     git stash / patch-snapshot before ②,    │
        │     so ③-retry or ⑤-back-edge re-runs ② on a│
        │     clean tree (idempotent retry — H2)        │
        │  ② Work              (the task itself)        │
        │  ③ Test              (node-internal verifier │ ── FAIL ─▶ self-retry (bounded)
        │                        decidable; inner loop;│              (restore ⓪ snapshot
        │                        restore ⓪ before each) │               before re-running ②)
        │  ④ Handoff           (clean, minimal, typed; │
        │                        carries attempt_history│
        │                        + budget_remaining — H3)│ ──▶ out to next node
        │  ⑤ Review-agent      (SEPARATE context;      │ ── NEEDS-FIX ─▶ back-edge
        │                        re-derives findings by  │   (with findings; restore ⓪
        │                        running its OWN tools   │    on mutating upstream node)
        │                        against artifact paths;│
        │                        tool_calls = evidence) │
        └──────────────────────────────────────────────┘
                          │  (⑤ = PASS)
                          ▼
                  Summoner checkpoint — human FLOW gate
                  (continue / recall to <node> / skip / done / stop)
                  review agent judged quality (with own evidence); human judges direction
```

**Idempotent retry (H2 fix):** step ⓪ snapshots the working tree before any mutating ② (fix / implement / subsystem / migrate). On ③ FAIL self-retry *or* ⑤ NEEDS-FIX back-edge to a mutating node, the orchestrator restores the ⓪ snapshot before re-running ② — otherwise half-applied Edits compound (`old_string` no longer matches, neighbor-line corruption). Read-only nodes (diagnose, verify, review) skip ⓪.

| Step | Does | Decidable? | Absorbs borrow-point |
|---|---|---|---|
| ① Ingest+Validate | Receive the upstream handoff envelope; check that declared artifacts/fields/exit-criteria are present and well-formed. If not → emit `handoff_reject` and route back along the back-edge. | Yes (schema/structural checks) | ② (consumer side) + ③ (consumer side) |
| ② Work | Execute the closed-loop task (diagnose / reproduce / fix / verify / review / define / plan / implement / test / fan-out persona). | n/a | — |
| ③ Test | Run a **machine-decidable** node-internal verifier (test suite / lint / typecheck / contract check / build). If FAIL → **restore the ⓪ snapshot** (on mutating nodes) then retry ② within the node (bounded; cap via `max_inner_turns`), feeding the verifier feedback into the node's own context. This is the article's "every node is still a loop," kept *inside* the node. | Yes | ④ + article §"node is still a loop" |
| ④ Handoff | Produce a **clean, minimal, typed** context for the downstream node — strip upstream raw data (raw HTML, full debug dumps, producer *reasoning*), keep only validated artifact **paths** + a one-line factual claim + `attempt_history` + `budget_remaining`. No producer prose crosses the boundary (see H1). | Yes (handoff schema) | ③ (producer side) + ② (producer side) |
| ⑤ Review-agent | A **separate-context** review agent that **independently re-derives** its findings by running its own `Read`/`grep`/`Bash` against the `artifacts` paths in the ④ envelope — it does **not** trust producer prose. The envelope carries only paths + `exit_criteria` + a one-line factual claim (no producer reasoning). The reviewer's tool calls are logged to the trace as evidence (the article's "verdict needs evidence external to the system"). Verdict `PASS` or `NEEDS-FIX` + findings (each: file:line + fix), appended to the handoff as `review_verdict` with the `tool_calls` that produced it. | Decidable where the re-derivation yields objective signals (test exit code, grep hit count); pinned-rubric otherwise — but the evidence is the reviewer's own tool calls, never the producer's self-report | replaces human "read the checkpoint & judge quality" — the original pain point; **isolation is enforced by independent tool-use, not by a prompt promise** |

Only after ⑤ does the Summoner checkpoint fire. **What changed vs. today:** the *quality* judgment that used to lean on a human reading the checkpoint block is now done by a separate-context review agent that **re-derives findings with its own tools** (⑤) — this directly removes the "人疏忽就遗漏" failure mode the user named in §1.3. **What is preserved:** the checkpoint's *flow* decision (continue / recall to \<node\> / skip / done / stop) is still the human's — the review agent judges quality, it does not choose the next node.

**Two clarifications forced by the adversarial review (H1, C1, C2):**
- **Context isolation is enforced by tool-use, not by prompt promise (H1/C1).** The ④ envelope carries artifact **paths** + `exit_criteria` + a one-line factual claim — **no producer reasoning**. A reviewer cannot be "lulled by the producer's self-justifying reasoning" because no producer reasoning crosses the boundary. The reviewer must independently `Read`/`grep` the artifact paths and decide from the artifact itself. Its tool calls are logged to the trace as the verdict's evidence. (The earlier `handoff_note` field carrying producer prose was a spec defect — the C4 fixture has been fixed to carry only a factual claim.)
- **⑤ back-edge to the *same* node is node-internal (not inter-node); ⑤ back-edge to a *different* node is inter-node (C2).** A same-node ⑤ NEEDS-FIX (e.g. ⑤ on `fix` → back to `fix`) is treated like ③ self-retry: bounded by `max_inner_turns`, no checkpoint — the human should not see a sub-par product. A cross-node ⑤ NEEDS-FIX or ① reject (e.g. `verify` reject → back to `fix`, skipping `reproduce`) *is* an inter-node edge; it is auto-routed by the orchestrator before the checkpoint, but the **next forward checkpoint the human sees will report the round-trip** (the human is not blind to the graph having cycled — they just don't gate every cycle, else the benefit evaporates). Both are globally bounded (H6, §2.5).

**Iron law refined:** the node may self-loop internally (③), may be sent back by ⑤ same-node NEEDS-FIX, and the orchestrator may auto-route bounded cross-node back-edges — but every **forward** inter-node edge advance still passes a human-gated checkpoint, and the whole graph is bounded by a global budget (§2.5). The walker tracks graph routing; the review agent gates quality (with its own evidence); the human gates forward flow.

### 2.2 The Handoff Envelope (typed, following market best practice)

Concretize "clean context" as a typed envelope (LangGraph handoff / OpenAI Agents SDK handoff / DSPy module boundary all converge on this shape):

```jsonl
{
  "type": "handoff",
  "from_node": "diagnose",
  "to_node": "fix",
  "artifacts": ["docs/reviews/2026-07-27-task-npe.md", "player/task/task.go:142"],
  "exit_criteria": ["root_cause_identified", "fix_approach_stated"],
  "factual_claim": "root cause = nil deref of player.SubTask at task.go:142 on player offline",
  "attempt_history": [{"node":"diagnose","attempts":1,"verifier":"root_cause_pin:file:line","passed":true}],
  "budget_remaining": {"graph_turns_left": 18, "token_budget_left": 42000},
  "stripped": ["raw_grep_output", "full_stack_trace", "producer_reasoning_trace"],
  "review_verdict": {"reviewer":"review-agent:diagnose","verdict":"PASS","findings":[],"evidence_tool_calls":["Read task.go:142","grep -n player.SubTask player/task/"]}
}
```

- `artifacts` — concrete, validated product **paths** (paths, file:line refs). Never empty. The downstream and the reviewer `Read` these directly — the envelope carries paths, not contents.
- `exit_criteria` — the machine-checkable list this node claims to have satisfied (see §2.4). The downstream's ① Ingest validates *these*.
- `factual_claim` — **one line, fact only, no producer reasoning.** Replaces the old `handoff_note`. Producer reasoning is banned from crossing the boundary (H1/C1: it would let the reviewer collude with the producer's self-justification). What the downstream needs to *know* (not *reason*) goes here.
- `attempt_history` — **accumulating cross-node state (H3 fix).** Appends each node's attempts/verifier results as the graph runs. Survives back-edges: when `fix` re-runs after a ⑤ NEEDS-FIX, its ① Ingest sees what it already tried (so it does not reproduce the same failed approach). This is the shared-state channel — it lives *in* the envelope chain, not in a separate store, keeping the "no ambient mutable state" property while not losing history.
- `budget_remaining` — **global budget (H6 fix).** Decrements across all nodes + back-edges; when it hits 0 the walker halts at a checkpoint. Bounded by `max_graph_turns` + `total_token_budget` in the graph block (§2.5).
- `review_verdict` — filled by ⑤. `PASS` or `NEEDS-FIX` + findings + `evidence_tool_calls` (the reviewer's own Read/grep — the verdict's external evidence). On `NEEDS-FIX` the walker routes a back-edge to `from_node` with the findings as reason.
- `stripped` — what was intentionally dropped (auditable). Producer reasoning is always listed here.

This is a **new trace event type** (`handoff`), not a runtime object — Summoner's runtime is markdown protocol + trace + scorers, so the envelope is emitted to the trace and enforced by scorers, not by a Python state machine.

### 2.3 Back-edge upgrade (②): `recall to <node>`, executor = orchestrator

The current `[recall]` returns only to the previous phase. Upgrade to:

```
recall to <node>  reason=receiver_rejected_handoff | direction_wrong | verifier_failed
```

- May cross nodes (skip intermediate nodes) — the article's red edge (reject → writer, *skipping* researcher).
- May be triggered three ways: (a) **human** at a checkpoint ("方向不对，回 diagnose"), already captured by checkpoint content-feedback grammar; (b) **downstream node's ① Ingest** rejecting an upstream handoff — emits `handoff_reject`; (c) **⑤ Review-agent NEEDS-FIX** — emits `review_verdict` with verdict=NEEDS-FIX. In all cases the **walker** (§10, a small Go binary) routes the back-edge, tracking the skip-set and per-finding counters in its walk-state. It is *not* a downstream agent spawning an upstream agent — that would violate `persona-composition.md`'s "personas never call other personas" and Claude Code's "subagents cannot spawn subagents" platform limit. The walker is the orchestrator, exactly like Alexey's example where the orchestrator (`process.md`) runs step 5 and 7.
- The reject → upstream-redo flow is a real cycle, not a DAG. This matches the article: production agent graphs are usually cyclic. The walker (not the LLM) maintains the cycle state — back-edge-return-path, skip-sets, counters — so the LLM no longer has to improvise control flow (H4 fix).

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
- [ ] code-reviewer persona returned 0 Critical findings — **SOFT (LLM judgment, NOT deterministic):** the parse is mechanical but the *finding count* is an LLM's call. This box is a *signal*, never the sole PASS condition. A 0-Critical report does not prove no defects exist.
- [ ] DECIDABLE: report shape — every Critical/Important finding has a file:line + concrete fix (grep report for "file:" lines)
- [ ] DECIDABLE: ⑤ re-derives — reviewer independently re-runs Read/grep on touched files, confirms each finding's file:line is real (reviewer tool_call evidence in trace)
Verdict: PASS requires DECIDABLE boxes all checked AND ⑤'s own re-derivation found no new defects. SOFT boxes never alone yield PASS. NEEDS-FIX on any DECIDABLE miss or ⑤ finding.
```

**The H5 fix (category error):** the earlier spec called "0 Critical findings = deterministic" — but the *finding count* is an LLM judgment, only the *parse* is mechanical. That was a category error letting an LLM's own output masquerade as a decidable verifier (the article's "verdict needs evidence external to the system" was unsatisfied). Now: LLM-judgment criteria are explicitly labeled SOFT and can never alone yield PASS; only criteria whose evidence is the reviewer's *own tool calls* (test exit code, grep hit count, file:line existence) count as DECIDABLE. "把代码写得好一点" type criteria are **banned** from exit-criteria lists entirely; soft criteria must at least be pinned to a structural check. This is the article's `verifier.check()` discipline made literal.


### 2.5 Per-task graph declaration (①) — graph produced at plan time

Borrow-point ① is **not** "predeclare a per-project workflow graph in `summoner.yaml`." It is: **the Phase 2 `plan` step (writing-plans) decomposes the task into small closed loops, each a node, and writes out that per-task graph explicitly** — nodes, edges, back-edges, and which exit-criteria each node uses. This graph is written *before* execution, readable as a graph, and the execution follows it. This satisfies the article's "control flow readable as a graph, routing pre-defined" even though it is per-task, not per-project.

The graph is declared in the plan artifact (`docs/superpowers/plans/<date>-<topic>.md`, produced by writing-plans) in a small, greppable block:

```yaml
# summoner-task-graph (per-task, plan-time)
budget:                     # GLOBAL bounds (H6 fix) — walker enforces, halts at checkpoint on 0
  max_graph_turns: 20      # total node-executions across all nodes + back-edges
  total_token_budget: 50000
  max_back_edges_total: 8   # hard cap on cycles regardless of per-finding counters
  alternating_finding_window: 4   # if N distinct findings rotate within this window → escalate (H6)
nodes:
  - id: diagnose
    skill: phase.debug        # from summoner.yaml phases.*
    exit_criteria: [root_cause, fix_approach]
    max_inner_turns: 3
    mutating: false           # read-only → no ⓪ snapshot needed
  - id: reproduce
    skill: phase.test
    exit_criteria: [repro_test_written, repro_test_fails_before_fix]
    max_inner_turns: 2
    mutating: true            # writes a repro test file → ⓪ snapshot before ② (H2)
  - id: fix
    skill: antia-logic        # routed by diagnose outcome (conditional edge)
    exit_criteria: [diff_applied, no_compile_error, all_deref_sites_covered]
    max_inner_turns: 4
    mutating: true            # ⓪ snapshot before ② (H2)
  - id: verify
    skill: phase.verify
    clean_context: true       # verdict node — isolated context
    exit_criteria: [tests_pass, no_new_lint, build_ok]
    max_inner_turns: 1
    mutating: false
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

Existing `chain` / `fan_out` / `checkpoints` schema is **preserved unchanged** as the legacy/fallback path. A plan that produces a `summoner-task-graph` block runs the **walker** (§10) which routes nodes and tracks walk-state; a plan that doesn't falls back to today's chain behavior. **No existing `summoner.yaml` breaks.** This is the user-approved 方案 A (incremental compat) and also satisfies the article's "don't use a graph where a loop suffices" — simple per-task graphs can still just be a chain.

### 2.7 What stays unchanged (invariants)

These Summoner iron laws are **not** relaxed by the graph upgrade:
1. **Phase 1 is iron law** — the `diagnose` node's `exit_criteria` includes `root_cause`; `conditional_edges` may not bypass `diagnose`; back-edges may not skip *into* skipping diagnose.
2. **No auto-advance past a checkpoint — the human retains the *flow* decision.** The review agent (⑤) judges *product quality*; the *flow* decision at the checkpoint (continue / recall to \<node\> / skip / done / stop) is still the human's. What is automated: node-internal self-loops (③) and the ⑤ quality gate (NEEDS-FIX auto-routes a back-edge before the human ever sees a sub-par product). Both are bounded — ③ by `max_inner_turns`, ⑤ by a ≤3 same-finding-reject escalation to checkpoint.
3. **No hardcoded project/domain names** — graph blocks reference `phases.*` skills; routing rules are project-declared in `summoner.yaml`, not framework-baked.
4. **Agents never call other agents** — back-edges (including ⑤ NEEDS-FIX) are executed by the **walker** (§10), never by the review agent spawning an upstream node, and never by a node spawning its own reviewer. The review agent only *reads artifact paths* and *returns a verdict with its own tool-call evidence*; the walker acts on the verdict. (Extends `persona-composition.md`'s "personas never call other personas" to all agents, consistent with Claude Code's "subagents cannot spawn subagents" platform limit.)
5. **Post-game review is mandatory** — at workflow end, the existing 5-type questionnaire fires. New Type-1 triggers (`handoff_reject` and ⑤ NEEDS-FIX events) feed the reject-redo signal into memory, same as human "方向不对" today.
6. **Review-agent independence is enforced by tool-use, not by a prompt promise (H1 fix).** ⑤ runs in a separate context, receives an envelope of artifact **paths** + `exit_criteria` + a one-line factual claim (no producer reasoning), and **independently re-derives** its findings by running its own `Read`/`grep`/`Bash` against those paths. Its tool calls are logged as the verdict's evidence. This is the whole point: it cannot be lulled by the producer's self-justifying reasoning (none crosses the boundary), only by the artifacts themselves — which is exactly the article's "verdict needs evidence external to the system." A ⑤ that read producer reasoning, or that returned a verdict with no tool-call evidence, would degenerate into "agent stamps its own work." The `review-isolation-check` scorer verifies the verdict has `evidence_tool_calls` and that the envelope's `stripped` includes `producer_reasoning_trace`.

## 3. Components Touched

| Component | Change | New? |
|---|---|---|
| `references/node-contract.md` | The 5-step contract (incl. ⓪ snapshot, ⑤ Review-agent independent re-derivation), typed handoff envelope + `review_verdict` + `evidence_tool_calls`, decidable/SOFT exit-criteria discipline, idempotent-retry rule, `attempt_history`/`budget_remaining` fields | **New** |
| `references/manifest-spec.md` | Add §Node Types + §Conditional Routing Rules; mark `graph` block as plan-time (not manifest-time); add `after_node` to checkpoints enum; add graph `oneOf` branch (C3) | Edit |
| `references/checkpoint-protocol.md` | Extend `[recall]` → `[recall to <node> reason=...]`; reframe checkpoint as *human FLOW gate* (quality already gated by ⑤); add verdict-node `clean_context` entry behavior | Edit |
| `references/workflow-reference.md` | Add §Per-task Graph section; document walker-vs-chain fallback; add graph red flags (incl. "review agent returned verdict with no `evidence_tool_calls` = fail"; "⑤ read producer reasoning = fail") | Edit |
| `references/trace-protocol.md` | Add event types: `handoff`, `handoff_reject`, `node_test_loop`, `node_turn`, `review_verdict` (with `evidence_tool_calls`) | Edit |
| `references/scoring-system.md` | Add P0 scorers: `handoff-contract-check`, `verifier-checklist-check`, `review-isolation-check`; wire into regression/stability | Edit |
| `scorers/deterministic/handoff-contract-check.sh` | Validate every inter-node edge has a `handoff` event with non-empty `artifacts`(paths) + `exit_criteria` + `factual_claim` + `review_verdict` with `evidence_tool_calls`; flag any producer reasoning in the envelope | **New** |
| `scorers/deterministic/verifier-checklist-check.sh` | For each node, confirm all DECIDABLE exit_criteria checked PASS (evidence = tool calls); SOFT criteria never alone yield PASS; ban "判不了" criteria | **New** |
| `scorers/deterministic/review-isolation-check.sh` | Confirm every ⑤ verdict has non-empty `evidence_tool_calls` (the reviewer ran its own tools) AND the envelope's `stripped` includes `producer_reasoning_trace`; flag any verdict lacking independent tool evidence (the rubber-stamp tell) | **New** |
| `agents/review-agent.md` | Generic per-node reviewer: receives envelope of artifact **paths** + `exit_criteria` + one-line factual claim (NO producer reasoning); **independently runs Read/grep/Bash to re-derive findings**; returns `review_verdict` (PASS / NEEDS-FIX + file:line findings) with `evidence_tool_calls`. Never reads producer context; never calls other agents; returns verdict + evidence only. | **New** |
| `cmd/summoner-walker/` (Go binary, §10) | Reads `summoner-task-graph` YAML, tracks walk-state (node/attempt, per-finding counter, alternating-finding window, back-edge-return-path stack, global budget), emits `node_turn`/`handoff`/`handoff_reject` events, tells SKILL.md which node to run next. Does NOT execute agents. | **New** |
| `internal/graph/` (Go pkg) | Graph parse + walk-state machine + budget enforcement (reuses `internal/` layout). Walker is a thin CLI over this. | **New** |
| `scripts/node-snapshot.sh` | ⓪ working-tree snapshot/restore helper (git stash / patch-snapshot) for mutating-node idempotent retry (H2) | **New** |
| `skills/summoner/SKILL.md` | Phase Execution: when a plan carries a `summoner-task-graph`, call `summoner-walker next` → run that node (①→②→③→④→⑤, with ⓪ snapshot on mutating nodes) → call `summoner-walker record` with handoff/review_verdict → repeat; else chain fallback | Edit |
| `commands/fix.md`, `commands/new.md` | Move the routing tables into named `route_*` rules referenced by graph blocks; commands become thinner | Edit |
| `skills/summoner-writing-plans` (or superpowers:writing-plans integration) | Plan artifact must emit a `summoner-task-graph` block (with `budget`, `mutating` flags) for non-trivial tasks | Edit/Note |
| `summoner.schema.json` / `scripts/validate-manifest.sh` | Add `after_node` to checkpoints enum; add graph `oneOf` branch (C3 fix) | Edit |
| `tests/fixtures/traces/` | Add graph-mode fixtures: clean pass (⑤ PASS), ⑤ NEEDS-FIX→back-edge, ③ FAIL→retry (with ⓪ restore), review-isolation-violation (no `evidence_tool_calls`), alternating-finding-escalation | **New** |

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
  │ (walker: RUN_NODE id=diagnose mutating=false)               │
  │ ① Ingest: user input + memory patterns (envelope from Phase 0)│
  │ ⓪ (skip — read-only node)                                    │
  │ ② Work: antia-debug skill → root cause                       │
  │ ③ Test: exit_criteria[root_cause] = "file:line + hypothesis │
  │        stated" — SOFT (pinned to file:line; scorer surfaces, │
  │        never masks); retry if pin missing (≤3 turns)        │
  │ ④ Handoff: envelope{artifacts:[report.md path, task.go:142],│
  │            exit_criteria, factual_claim, attempt_history,    │
  │            budget_remaining, stripped:[producer_reasoning]} │
  │ ⑤ Review-agent (separate context): INDEPENDENTLY re-derives │
  │     by Read/grep on task.go for all SubTask deref sites;     │
  │     evidence_tool_calls logged; PASS → review_verdict        │
  │     NEEDS-FIX → walker routes back-edge to diagnose          │
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
  │ (walker: RUN_NODE id=fix mutating=true attempt=1)                  │
  │ ① Ingest: validate reproduce envelope + review_verdict=PASS ✓     │
  │ ⓪ Snapshot: git stash / patch-snapshot BEFORE ② (mutating node)   │
  │ ② Work: apply fix                                                   │
  │ ③ Test: build + compile clean (DECIDABLE); on FAIL → restore ⓪,   │
  │        retry ② ≤4 turns                                             │
  │ ④ Handoff: envelope{artifacts:[task.go path+diff], exit_criteria,  │
  │            factual_claim, attempt_history (shows prior tries)}      │
  │ ⑤ Review-agent (separate context): INDEPENDENTLY grep all deref   │
  │     sites of player.SubTask; finds task.go:187 unchecked →          │
  │     NEEDS-FIX + finding; walker restores ⓪, routes back-edge to   │
  │     fix (same-node, attempt=2) — BEFORE checkpoint                 │
  └─────────────────────────────────────────────────────────────────────┘
       │ (⑤ PASS after retry) checkpoint — human FLOW gate
       ▼
  ┌─ verify (clean_context: true — fresh agent, envelope of paths) ┐
  │ (walker: RUN_NODE id=verify mutating=false)                    │
  │ ① Ingest: validate fix envelope + review_verdict=PASS ✓        │
  │ ② Work: run test suite                                          │
  │ ③ Test: tests_pass + no_new_lint + build_ok — all DECIDABLE     │
  │   FAIL → self-retry; exhausted → walker back-edge to fix,        │
  │           skipping reproduce (back_edges[].skip)                 │
  │ ④ Handoff: envelope{verdict:PASS, test_results, evidence}      │
  │ ⑤ Review-agent: re-derives by re-running tests + grep — PASS    │
  └─────────────────────────────────────────────────────────────────┘
       │ checkpoint
       ▼
  ┌─ review (clean_context: true) — the code-reviewer PERSONA node │
  │ ① Ingest: validate verify envelope + review_verdict=PASS ✓     │
  │ ② Work: code-reviewer persona (5-axis)                          │
  │ ③ Test: report shape (DECIDABLE: every finding has file:line+fix)│
  │         finding-count=0 is SOFT (signal only, not sole PASS)    │
  │ ④ Handoff: envelope{verdict:PASS, report path}                  │
  │ ⑤ Review-agent (meta-review): INDEPENDENTLY re-greps touched    │
  │     files to confirm persona's findings are real; PASS / NEEDS-FIX│
  └─────────────────────────────────────────────────────────────────┘
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

- ~~No new runtime engine~~ — **revised after adversarial review (H4):** a small Go walker IS in scope (§10). What remains out of scope: no Python runtime, no remote orchestration service. The walker is a local binary that reads a YAML graph + emits trace events + tells the SKILL.md flow which node to run next; it is not a full agent runtime.
- No per-project predeclared workflow graphs in `summoner.yaml` (only node types + routing rules). Per-task graphs live in plan artifacts.
- No replacing the checkpoint human *flow* gate. The review agent (⑤) offloads *quality* judgment from the human; the *flow* decision (continue/recall/skip/done/stop) stays human. The two are deliberately not merged.
- No review agent making flow decisions. ⑤ returns PASS/NEEDS-FIX only; it never picks the next node. (If a future need arises for agent-decided flow, that is a separate spec — explicitly out of scope here.)
- No auto-promotion of memory patterns into skills (existing manual review path stays).
- No new *domain* personas. One generic `review-agent` persona is added (⑤ is the same role per node, parameterized by the node's `exit_criteria`); the existing 4 domain personas become node-capable as-is.

## 7.5 Adversarial Review Corrections (audit trail)

This spec was adversarially reviewed by an independent-context agent against 8 frontier principles. Six holes + four contradictions were found and fixed in this revision. Audit trail (so implementers and reviewers can verify each fix landed):

| ID | Hole (from review) | Fix in this spec |
|---|---|---|
| H1 | ⑤ isolation is a paper promise; scorer only checks self-reported `input_scope` label | §2.1 ⑤ + §2.2: reviewer **independently re-derives** findings with own Read/grep; envelope carries paths only, **no producer reasoning** (`factual_claim` replaces `handoff_note`); `evidence_tool_calls` logged as the verdict's evidence. Isolation enforced by tool-use, not prompt promise. |
| H2 | No rollback before ③ retry on mutating nodes → compounding half-applied Edits | §2.1 step ⓪: snapshot working tree before mutating ②; restore ⓪ before any ③ retry or ⑤ back-edge to a mutating node. §2.5 `mutating: true` flag per node. |
| H3 | No accumulating state; envelope-chaining loses failed-attempt history + budget | §2.2: `attempt_history` (accumulates across back-edges so a re-run node sees what it tried) + `budget_remaining` fields in envelope. |
| H4 | Markdown protocol cannot walk a cyclic graph with skip-sets; "no new runtime" was false | §10: a real Go walker tracks walk-state (back-edge-return-path, skip-sets, per-finding + global counters, budget) and tells SKILL.md which node to run. §7 revised. |
| H5 | `review` "0 Critical = deterministic" was a category error (finding count is LLM judgment) | §2.4: criteria split DECIDABLE (evidence = reviewer's own tool calls) vs SOFT (explicitly labeled, never alone yields PASS). |
| H6 | Alternating-findings loop unbounded; no global budget | §2.5 `budget` block: `max_graph_turns` + `total_token_budget` + `max_back_edges_total` + `alternating_finding_window` escalation. |
| C1 | ⑤ "never reads producer context" vs "judges exit_criteria" (which needs producer output) | §2.1 clarification: boundary = no producer *reasoning*; reviewer reads *artifact paths* (the product), re-derives independently. |
| C2 | "no auto-advance past checkpoint" vs ⑤ back-edge "before checkpoint" | §2.1 clarification: same-node ⑤ back-edge = node-internal (bounded, no checkpoint); cross-node back-edge = inter-node, auto-routed but next forward checkpoint reports the round-trip. |
| C3 | `summoner.schema.json` enum has no `after_node`; `oneOf [chain,fan_out]` rejects graph | §10.3: schema updated — add `after_node` to checkpoints enum, add graph `oneOf` branch. |
| Infra | `node-contract.md`, `review-agent.md`, 3 scorers, trace events all don't exist | §3 marks them **New**; §10.1 lists walker + new files as implementation deliverables (this is a spec, implementation follows). |

## 8. Risks & Mitigations

| Risk | Mitigation |
|---|---|
| Handoff envelope becomes ceremony / token overhead | Envelope is ≤120 tokens; `stripped` is audited not echoed; scorers enforce brevity |
| Graph mode adds friction to simple tasks (violates "don't graph a loop") | Plan emits chain (not graph) for trivial tasks (C1); graph is opt-in per plan |
| Conditional routing rules duplicated between `summoner.yaml` and `commands/*.md` | Single source of truth: rules declared in manifest, referenced (not redefined) in commands |
| Back-edge loops forever | `max_inner_turns` per node + 3-reject-same-reason escalation to checkpoint |
| Scorers give false confidence (rubric "pinned to file:line" still soft) | Iron law preserved: deterministic scorers dominate; rubric only where no deterministic check exists; human calibration stays |
| "判不了" criteria sneak back into exit_criteria | `verifier-checklist-check.sh` explicitly flags any criterion not matchable to a deterministic scorer or a pinned structural check |
| ⑤ Review-agent rubber-stamps (agent stamps its own work) | ⑤ **independently re-derives** findings with own tool calls against artifact paths; envelope carries no producer reasoning; `review-isolation-check` verifies `evidence_tool_calls` present + `stripped` includes producer reasoning (invariant #6); C5 adversarially tests it |
| ⑤ Review-agent too strict → loops on nitpicks | ⑤ judges only the node's declared `exit_criteria`, not free-form quality; 3× same-finding escalation (C9) surfaces stuck loops to the human |
| ⑤ adds latency/tokens per node | ⑤ uses a fast model on a tiny envelope (≤120 tok in, ≤80 tok verdict); for C1 trivial path, ⑤ is the single node's only quality gate (cheaper than full chain) |

## 9. Success Criteria (matches the goal)

The upgrade succeeds when, across cases C1–C9:
1. **Correctness ↑:** new Summoner P0 score ≥ old, on identical tasks (regression-test non-regressing, stability-test passing at 0% tolerance for fix).
2. **Human intervention ↓:** fewer checkpoint content-feedback + recall events per task in new vs old (measured from trace `checkpoint` + `handoff_reject` + ⑤ NEEDS-FIX events). This is the headline win — ⑤ catches defective handoffs *before* the human would have had to read them.
3. **Smoother:** post-game Type-4 rating ≥ old; Type-1 (direction correction) frequency ↓ (reject-redo handled inside graph before reaching the human).
4. **Real-world:** at least one case run against a real bug in this repo (not only synthetic fixtures), demonstrating ⑤ catching a defect the old chain would have let through to the human reviewer (C4 control comparison).

## 10. The Walker (real graph runtime — H4 fix)

The adversarial review established that a markdown protocol (prose instructions to an LLM) cannot deterministically walk a cyclic graph with skip-sets, conditional edges, per-finding counters, and global budgets — the "no new runtime" claim was false. This section adds the real, minimal runtime. **Design principle: the walker does NOT execute agents.** It only (a) reads the per-task graph, (b) tracks walk-state, (c) decides which node runs next, (d) emits trace events, (e) tells the SKILL.md flow what to do. All actual work (② Work, ③ Test, ⑤ Review) stays in agents/skills the SKILL.md invokes. The walker is a router + bookkeeper, not an executor.

### 10.1 Deliverable

`cmd/summoner-walker/` — a small Go binary (consistent with the existing `cmd/summoner-ctx/` and `hooks/hooks/` Go codebase; reuses `internal/`). Single responsibility: given a `summoner-task-graph` YAML + a stream of node-completion signals, emit the next-node directive + trace events.

```
summoner-walker --graph <plan.md#summoner-task-graph> --trace <trace.jsonl> next
  → prints: RUN_NODE id=fix skill=antia-logic clean_context=false mutating=true attempt=2
summoner-walker --graph ... record --node fix --step handoff --envelope <envelope.json>
  → appends handoff event to trace, updates walk-state, prints next directive
summoner-walker --graph ... record --node fix --step review_verdict --verdict NEEDS-FIX --findings <findings.json>
  → computes back-edge target from graph.back_edges + skip-set, prints: RUN_NODE id=fix ... (same-node, attempt 3) OR BACK_EDGE to=fix skip=[verify]
summoner-walker --graph ... status
  → prints: node=fix attempt=3/4 graph_turns=11/20 budget=38000/50000 back_edges=3/8
```

### 10.2 Walk-state (what the walker tracks — the thing the LLM could not)

| State | Purpose | Fixes |
|---|---|---|
| current node + attempt # | which node, which retry | ③ bound |
| per-finding reject counter | 3× same-finding escalation | H6 (partial) |
| findings-seen-this-window | alternating-finding detection (rotate within window → escalate) | H6 (the unbounded case) |
| back-edge-return-path stack | "I am returning from verify→fix; on forward, skip reproduce" | H4 (skip-sets) |
| graph_turns / token / back_edges totals | global budget; halt at 0 | H6 |
| attempt_history (read from trace) | hand to re-running node so it doesn't repeat a failed approach | H3 |
| next forward checkpoint pending | report round-trips to the human at the next gate | C2 |

The walker writes walk-state to `$HOME/.claude/plugins/summoner/walk-state/{session_id}.json` (not the trace — the trace is the append-only log; walk-state is the mutable machine state the LLM was wrongly keeping in its head).

### 10.3 Schema + protocol changes (C3 fix)

- `summoner.schema.json` (or the manifest validator `scripts/validate-manifest.sh`): add `after_node` to the `workflows.checkpoints` enum (currently `[after_each, after_merge, none]`); add a `graph` `oneOf` branch alongside `chain`/`fan_out` so per-task graph blocks validate.
- `references/checkpoint-protocol.md`: extend `RECALL` grammar to `recall to <node> reason=...` (the walker parses this; the LLM no longer improvises the target).
- `references/trace-protocol.md`: add event types `handoff`, `handoff_reject`, `node_test_loop`, `node_turn`, `review_verdict` (with `evidence_tool_calls`).
- `skills/summoner/SKILL.md`: Phase Execution calls `summoner-walker next` to get the next node, runs it, calls `summoner-walker record` with the handoff/review_verdict, repeats. Chain fallback stays for graph-less plans.

### 10.4 Why this is "minimal" and not over-engineered

- The walker is ~one Go package, no external deps beyond what `internal/` + `hooks/` already use. It does not call models, does not run tools, does not touch the working tree (that's ⓪ snapshot, a separate shell helper). It is a state machine for graph routing — exactly what LangGraph's `compile()` produces, but as a local binary instead of a Python library.
- It preserves the article's core: control flow is now in a structure (the walker's state machine), not in the model's ad-hoc judgment. The LLM's job shrinks to "run the node the walker tells me to run" — which is what makes the graph *real* instead of a "loop wearing a graph skin."
