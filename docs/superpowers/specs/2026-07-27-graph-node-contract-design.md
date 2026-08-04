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

The single headline change: **a Summoner node is no longer "one skill call." A node is a closed-loop agent running ① Ingest+Validate → ⓪ Pre-Work snapshot → ② Work → ③ Test → ④ Handoff, after which the walker schedules a separate-context ⑤ Review-agent (⑤ is not a 5th inline step of the node — it is walker-scheduled via `RUN_REVIEW`, §2.8).** The graph is just the composition of such nodes. The chosen borrow-points (① explicit graph declaration / ② skip-node back-edge / ③ verdict-node context isolation / ④ decidable verifier) are all absorbed into one node contract rather than kept as four loose features — and the user's added direction (offload the human quality-read onto an independent-context review agent) is absorbed as ⑤.

### 2.1 The Node Contract (every node = agent run: ①⓪②③④, then walker-scheduled ⑤)

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
        └──────────────────────────────────────────────┘
              │ (④ handoff → walker record)
              ▼
        ┌──────────────────────────────────────────────┐
        │  ⑤ Review-agent  (NOT a sub-step of the node;│ ── NEEDS-FIX ─▶ back-edge
        │   scheduled by the walker via RUN_REVIEW     │   (walker signals restore:true
        │   — §2.8/B4; SEPARATE context; re-derives    │    for mutating upstream node;
        │   findings with OWN Read/grep against the    │    SKILL.md runs node-snapshot)
        │   ④ artifact paths; evidence_tool_calls      │
        │   = the verdict's external evidence — §2.2.1)│
        └──────────────────────────────────────────────┘
                          │  (⑤ = PASS)
                          ▼
        ┌──────────────────────────────────────────────┐
        │ Summoner checkpoint — human FLOW gate         │
        │ (rendered by `walker explain` — §2.9/M9:       │
        │  node label not id; ①②③④⑤ hidden; route map;  │
        │  annotated [recall to <label>] + precomputed   │
        │  default; review agent judged quality with its │
        │  own evidence; human judges direction)         │
        └──────────────────────────────────────────────┘
```

**Idempotent retry (H2 fix):** step ⓪ snapshots the working tree before any mutating ② (fix / implement / subsystem / migrate). On ③ FAIL self-retry *or* ⑤ NEEDS-FIX back-edge to a mutating node, **SKILL.md** restores the ⓪ snapshot (the walker signals `restore:true` via the `RUN_NODE` directive and never touches the tree itself — §10.1, M2) before re-running ② — otherwise half-applied Edits compound (`old_string` no longer matches, neighbor-line corruption). Read-only nodes (diagnose, verify, review) skip ⓪.

| Step | Does | Decidable? | Absorbs borrow-point |
|---|---|---|---|
| ① Ingest+Validate | Receive the upstream handoff envelope; check that declared artifacts/fields/exit-criteria are present and well-formed. If not → emit `handoff_reject` and route back along the back-edge. | Yes (schema/structural checks) | ② (consumer side) + ③ (consumer side) |
| ② Work | Execute the closed-loop task (diagnose / reproduce / fix / verify / review / define / plan / implement / test / fan-out persona). | n/a | — |
| ③ Test | Run a **machine-decidable** node-internal verifier (test suite / lint / typecheck / contract check / build). If FAIL → **restore the ⓪ snapshot** (on mutating nodes) then retry ② within the node (bounded; cap via `max_inner_turns`), feeding the verifier feedback into the node's own context. This is the article's "every node is still a loop," kept *inside* the node. | Yes | ④ + article §"node is still a loop" |
| ④ Handoff | Produce a **clean, minimal, typed** context for the downstream node — strip upstream raw data (raw HTML, full debug dumps, producer *reasoning*), keep only validated artifact **paths** + a one-line factual claim + `attempt_history` (carrying `{node, attempts, verifier}` only, no `passed` — B2) + `budget_remaining`. No producer prose crosses the boundary (see H1). | Yes (handoff schema) | ③ (producer side) + ② (producer side) |
| ⑤ Review-agent | A **separate-context** review agent that **independently re-derives** its findings by running its own `Read`/`grep`/`Bash` against the `artifacts` paths in the ④ envelope — it does **not** trust producer prose, and sees no producer `passed` self-report (B2). The envelope carries only paths + `exit_criteria` (each with `verdict_type`) + a one-line factual claim (no producer reasoning). The reviewer's tool calls are logged to the trace as evidence (the article's "verdict needs evidence external to the system"). Verdict `PASS` or `NEEDS-FIX` + findings (each: file:line + fix), emitted as a **separate `review_verdict` trace event** (§2.2.1) keyed by the ④ envelope's `envelope_id`, with `evidence_tool_calls` (the reviewer's own tool calls that produced it). ⑤ is **scheduled by the walker via `RUN_REVIEW`** (§2.8/B4), not a sub-step of `RUN_NODE` — the node cannot spawn its own reviewer (invariant #4). | Decidable where the re-derivation yields objective signals (test exit code, grep hit count); pinned-rubric otherwise — but the evidence is the reviewer's own tool calls, never the producer's self-report | replaces human "read the checkpoint & judge quality" — the original pain point; **isolation is enforced by independent tool-use, not by a prompt promise** |

Only after ⑤ does the Summoner checkpoint fire. **What changed vs. today:** the *quality* judgment that used to lean on a human reading the checkpoint block is now done by a separate-context review agent that **re-derives findings with its own tools** (⑤) — this directly removes the "人疏忽就遗漏" failure mode the user named in §1.3. **What is preserved:** the checkpoint's *flow* decision (continue / recall to \<node\> / skip / done / stop) is still the human's — the review agent judges quality, it does not choose the next node.

**Two clarifications forced by the adversarial review (H1, C1, C2):**
- **Context isolation is enforced by tool-use, not by prompt promise (H1/C1).** The ④ envelope carries artifact **paths** + `exit_criteria` + a one-line factual claim — **no producer reasoning**. A reviewer cannot be "lulled by the producer's self-justifying reasoning" because no producer reasoning crosses the boundary. The reviewer must independently `Read`/`grep` the artifact paths and decide from the artifact itself. Its tool calls are logged to the trace as the verdict's evidence. (The earlier `handoff_note` field carrying producer prose was a spec defect — the C4 fixture has been fixed to carry only a factual claim.)
- **⑤ back-edge to the *same* node is node-internal (not inter-node); ⑤ back-edge to a *different* node is inter-node (C2).** A same-node ⑤ NEEDS-FIX (e.g. ⑤ on `fix` → back to `fix`) is treated like ③ self-retry: the walker emits a **`node_review_retry`** event (NOT a `handoff_reject` — `handoff_reject` is reserved for *cross-node* rejects where a downstream node refuses an upstream handoff), bounded by `max_inner_turns`, no checkpoint — the human should not see a sub-par product. A cross-node ⑤ NEEDS-FIX or ① reject (e.g. `verify` reject → back to `fix`, skipping `reproduce`) *is* an inter-node edge; the walker emits `handoff_reject` and auto-routes the back-edge before the checkpoint, and the **next forward checkpoint the human sees will report the round-trip** (the human is not blind to the graph having cycled — they just don't gate every cycle, else the benefit evaporates). Both are globally bounded (H6, §2.5).

**Iron law refined:** the node may self-loop internally (③), may be sent back by ⑤ same-node NEEDS-FIX, and the orchestrator may auto-route bounded cross-node back-edges — but every **forward** inter-node edge advance still passes a human-gated checkpoint, and the whole graph is bounded by a global budget (§2.5). The walker tracks graph routing; the review agent gates quality (with its own evidence); the human gates forward flow.

### 2.2 The Handoff Envelope (typed, following market best practice)

Concretize "clean context" as a typed envelope (LangGraph handoff / OpenAI Agents SDK handoff / DSPy module boundary all converge on this shape):

```jsonl
{"type":"handoff","envelope_id":"h-001","from_node":"diagnose","to_node":"fix",
 "label":"定位根因",
 "artifacts":["docs/reviews/2026-07-27-task-npe.md","player/task/task.go:142"],
 "exit_criteria":[{"name":"root_cause","verdict_type":"SOFT","pin":"file:line"},
                  {"name":"fix_approach","verdict_type":"SOFT","pin":"file:line"}],
 "factual_claim":"root cause = nil deref of player.SubTask at task.go:142 on player offline",
 "attempt_history":[{"node":"diagnose","attempts":1,"verifier":"root_cause_pin:file:line"}],
 "budget_remaining":{"graph_turns_left":18,"token_budget_left":42000},
 "stripped":["raw_grep_output","full_stack_trace","producer_reasoning_trace","producer_verdict_self_report"]}
```

- `envelope_id` — **correlation key (B1/M8 fix).** Stable id (e.g. `h-001`) shared by this `handoff` event and the `review_verdict` event that reviews it. `review_verdict` is emitted as a **separate trace event** (not an inline field) carrying the same `envelope_id`; scorers join on it (no fragile timestamp-adjacency). See §2.2.1.
- `label` — **human-facing verb (M9 fix).** Plan-supplied plain-language node name shown at checkpoints ("定位根因", "补 nil 判空"), distinct from the machine `id` ("fix"). See §2.9.
- `artifacts` — concrete, validated product **paths** (paths, file:line refs). Never empty. The downstream and the reviewer `Read` these directly — the envelope carries paths, not contents. A reviewer may derive the enclosing file/directory from a `file:line` artifact to scope its own re-derivation grep.
- `exit_criteria` — each entry is `{name, verdict_type: DECIDABLE|SOFT, pin?, grep_pattern?}` (B3 fix). `verdict_type` is declared in the per-task graph block (§2.5), NOT inferred from the name by a scorer — so an LLM-judgment criterion (`all_deref_sites_covered`, `root_cause`) is explicitly SOFT and can never alone yield PASS. A SOFT criterion SHOULD carry a `grep_pattern` (the structural anchor the ⑤ reviewer MUST run and log in `evidence_tool_calls` — §2.5 line 242, §3 review-agent.md); `pin` is a free-form location hint. The downstream's ① Ingest validates these are present and well-formed.
- `factual_claim` — **one line, fact only, no producer reasoning.** Producer reasoning is banned from crossing the boundary (H1/C1: it would let the reviewer collude with the producer's self-justification). What the downstream needs to *know* (not *reason*) goes here. This field replaces the spec-draft `handoff_note` field that carried producer prose (a defect caught in the H1 review — §7.5 H1, §2.1 clarification); `handoff_note` never shipped in a released Summoner, only in this spec's earlier drafts.
- `attempt_history` — **accumulating cross-node state (H3 fix), but the producer's self-reported pass verdict does NOT cross the boundary into ⑤ (B2 fix).** Each entry carries `{node, attempts, verifier}` — it does NOT carry `passed`/the producer's own verdict. This lets a re-running node see what approaches it already tried (so it does not repeat a failed approach) without smuggling the producer's "I passed my own verifier" self-report, which would pre-prime the ⑤ reviewer. The `passed` boolean lives in the trace's `node_test_loop` event (machine-readable, scorer-checked), not in the handoff envelope.
- `budget_remaining` — **global budget (H6 fix).** Decrements across all nodes + back-edges; when it hits 0 the walker halts at a checkpoint. Bounded by `max_graph_turns` + `total_token_budget` in the graph block (§2.5).
- `stripped` — what was intentionally dropped (auditable). Producer reasoning (`producer_reasoning_trace`) AND the producer's self-reported verdict (`producer_verdict_self_report`) are always listed here (B2).
- `review_verdict` — emitted by ⑤ as a **separate trace event** (see §2.2.1), keyed by `envelope_id`. `PASS` or `NEEDS-FIX` + findings + `evidence_tool_calls` (the reviewer's own Read/grep — the verdict's external evidence). On `NEEDS-FIX` the walker routes a back-edge to `from_node` with the findings as reason.

This is a **new trace event type** (`handoff`), not a runtime object — Summoner's runtime is markdown protocol + trace + scorers, so the envelope is emitted to the trace and enforced by scorers, not by a Python state machine.

### 2.2.1 The `review_verdict` event (B1/M8 fix — separate event, correlation key)

⑤'s verdict is emitted as a **standalone trace event**, not an inline field of `handoff`:

```jsonl
{"type":"review_verdict","envelope_id":"h-001","node":"fix","reviewer":"review-agent:fix",
 "verdict":"NEEDS-FIX","findings":[{"file":"task.go:187","fix":"add nil guard before access"}],
 "evidence_tool_calls":["grep -n player.SubTask player/task/","Read task.go:180-200"]}
```

- `envelope_id` joins this event to the `handoff` event it reviews (§2.2). Scorers correlate by this stable id — never by timestamp adjacency (fragile under clock skew, retries, or a long-running ⑤).
- Two events for one node boundary (a `handoff` then its `review_verdict`), not one — because the walker *acts on* the verdict (routes a back-edge) as a distinct signal, and ⑤ is scheduled as a separate agent run (§2.8 / §10). Inlining the verdict into the handoff would conflate "producer emits product" with "reviewer emits verdict", two different lifecycles.
- `evidence_tool_calls` is **non-empty by invariant #6** — the reviewer must have run its own tools; a verdict with no tool-call evidence is a rubber-stamp and is failed by `review-isolation-check` (§3).

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
    label: "定位根因"          # human-facing verb (M9); shown at checkpoint, not id
    skill: phase.debug        # from summoner.yaml phases.*
    exit_criteria:
      - {name: root_cause,    verdict_type: SOFT,      pin: "file:line + hypothesis stated"}
      - {name: fix_approach,   verdict_type: SOFT,      pin: "file:line"}
    max_inner_turns: 3
    mutating: false           # read-only → no ⓪ snapshot needed
  - id: reproduce
    label: "写复现测试"
    skill: phase.test
    exit_criteria:
      - {name: repro_test_written,        verdict_type: DECIDABLE}
      - {name: repro_fails_before_fix,    verdict_type: DECIDABLE}
    max_inner_turns: 2
    mutating: true            # writes a repro test file → ⓪ snapshot before ② (H2)
  - id: fix
    label: "补 nil 判空"
    skill: antia-logic        # routed by diagnose outcome (conditional edge)
    exit_criteria:
      - {name: diff_applied,           verdict_type: DECIDABLE}
      - {name: no_compile_error,       verdict_type: DECIDABLE}
      - {name: all_deref_sites_covered, verdict_type: SOFT, grep_pattern: "player.SubTask"}
    max_inner_turns: 4
    mutating: true            # ⓪ snapshot before ② (H2)
  - id: verify
    label: "跑测试套件"
    skill: phase.verify
    clean_context: true       # verdict node — isolated context
    exit_criteria:
      - {name: tests_pass,   verdict_type: DECIDABLE}
      - {name: no_new_lint,  verdict_type: DECIDABLE}
      - {name: build_ok,     verdict_type: DECIDABLE}
    max_inner_turns: 1
    mutating: false
  - id: review
    label: "独立复查"          # the code-reviewer PERSONA node (§2.4) — read-only, no ⓪
    skill: phase.review
    clean_context: true
    exit_criteria:
      - {name: report_well_formed, verdict_type: DECIDABLE, pin: "every finding has file:line+fix"}
      - {name: no_unaddressed_sev, verdict_type: SOFT, grep_pattern: "TODO|FIXME|XXX"}
    max_inner_turns: 1
    mutating: false           # read-only → no ⓪ snapshot (§2.1)
edges:
  - {from: diagnose, to: reproduce}
  - {from: reproduce, to: fix}
  - {from: fix, to: verify}
  - {from: verify, to: review}
conditional_edges:
  - {from: diagnose, route: route_by_diagnosis, to: [fix, rpc, subsystem, migrate, gmt]}  # `to:` lists cross-task routing targets; the chosen target MUST be a node declared in the *resolved* per-task graph (this example block is the fix workflow, so only `fix` is in `nodes:` above; rpc/subsystem/migrate/gmt belong to other task-type graphs)
back_edges:
  - {from: verify, to: fix, reason: verifier_failed}       # skip nothing
  - {from: review, to: fix, reason: receiver_rejected_handoff, skip: [verify]}  # review is downstream of verify; skip re-running verify on the return
checkpoints: after_node      # human FLOW gate after every node's ⑤ (review agent already gated quality)
```

Key points:
- `nodes[].label` is the **human-facing** name (M9); `id` stays machine-only. Every node MUST have a `label`.
- `nodes[].skill` references `summoner.yaml` `phases.*` — no project/domain name hardcoded in the framework.
- `exit_criteria[].verdict_type` is **declared in the graph block** (B3 fix), NOT inferred by a scorer. `all_deref_sites_covered` is explicitly SOFT (whether each site is "covered" is an LLM judgment, not a grep exit code) — yet §2.4's rule still holds: a SOFT criterion never alone yields PASS; ⑤ must also run its own `grep_pattern` (a SOFT criterion's `grep_pattern` is the structural anchor the reviewer must execute and log in `evidence_tool_calls`). This is what makes §2.4's H5 discipline land at the declaration level, not just the template level. **B3 scoping clause:** B3 applies to nodes with DECIDABLE criteria; a node whose exit_criteria are all SOFT (e.g. diagnose — root cause is a judgment, not a binary test) is **exempt** and may legitimately PASS on SOFT alone, provided ⑤ ran its declared `grep_pattern` anchor and logged it in `evidence_tool_calls`. The trace-side scorer (`verifier-checklist-check.sh`) enforces only the enforceable slice it can see: a `review_verdict verdict=="PASS"` on a NON-diagnose node must be backed by ≥1 `passed==true && verdict_type=="DECIDABLE"` `node_test_loop` on that node; diagnose is exempt by name (§2.5 graph block: `id: diagnose`).
- `conditional_edges` mirrors the article's `add_conditional_edges(route_fn)` — a named routing rule, not model ad-hoc judgment. The existing `fix` Phase 3 routing table (logic/rpc/subsystem/migrate/gmt) becomes a named `route_by_diagnosis` rule. Its interface is defined in §2.6.1 (input = the diagnose node's `factual_claim` + a routing tag in `exit_criteria`; output = a target node id from `to`; declarative table, not an LLM/script call).
- `back_edges[].skip` realizes borrow-point ② (skip intermediate nodes on return).
- `clean_context: true` on verdict nodes (verify/review) realizes borrow-point ③ — the orchestrator starts that node's agent with a handoff envelope only, not the upstream session context.
- `max_inner_turns` realizes the article's `MAX_TURNS` backstop against infinite token burn inside a node's self-loop.

### 2.6 Manifest role (narrowed, incremental-compat)

`summoner.yaml` does **not** predeclare workflow graphs. It declares:
1. Available **node types** (which skills/personas can act as a node) — reuse existing `phases` map.
2. The **node contract** (reference to `references/node-contract.md`) — framework-level, same for all projects.
3. **Conditional routing rules** available to plans (e.g., `route_by_diagnosis`) — project-level, declared once, reused across plans.

Existing `chain` / `fan_out` / `checkpoints` schema is **backward-compatible**: `after_node` is added as a new enum value and a `graph` shape is added as a new `oneOf` branch, but existing manifests using `after_each` / `after_merge` / `none` still validate and behave as today. A plan that produces a `summoner-task-graph` block runs the **walker** (§10) which routes nodes and tracks walk-state; a plan that doesn't falls back to today's chain behavior. **No existing `summoner.yaml` breaks.** This is the user-approved 方案 A (incremental compat) and also satisfies the article's "don't use a graph where a loop suffices" — simple per-task graphs can still just be a chain.

### 2.6.1 Conditional routing rule interface (M3 fix)

A `conditional_edges` entry names a routing rule (e.g. `route_by_diagnosis`). Its contract is **declarative**, not an LLM call or a script (the walker "does not call models", §10.4 — so routing cannot depend on model judgment). Each rule is declared once in `summoner.yaml` under a new `routing_rules:` key:

```yaml
routing_rules:
  route_by_diagnosis:
    input_field: "routing_tag"          # a field the diagnose node sets in its handoff envelope
    map:                                 # declarative: value → target node id (must be in conditional_edges[].to)
      logic:     fix
      rpc:       rpc
      subsystem: subsystem
      migrate:   migrate
      gmt:       gmt
```

- **Input:** the `routing_tag` field the diagnose node's ④ Handoff sets (the node classifies its own root cause into one of the map keys — a node-internal act, not the walker's job).
- **Output:** a node id from `conditional_edges[].to`.
- **Execution:** the walker reads `routing_tag` from the handoff envelope and looks up the map. Pure table lookup — no model, no script. This is what makes routing "pre-defined" (article §`add_conditional_edges`): the legal paths are decided at plan/manifest time, the model only fills the tag.

The decision to emit chain vs graph at plan time (C1/YAGNI) is governed by a concrete rule (M12 fix): a plan emits a `summoner-task-graph` block iff it has ≥3 nodes OR any node has `mutating: true` with a back-edge; ≤2 nodes with no back-edge emit a plain chain.

### 2.7 What stays unchanged (invariants)

These Summoner iron laws are **not** relaxed by the graph upgrade:
1. **Phase 1 is iron law** — the `diagnose` node's `exit_criteria` includes `root_cause`; `conditional_edges` may not bypass `diagnose`; back-edges may not skip *into* skipping diagnose.
2. **No auto-advance past a checkpoint — the human retains the *flow* decision.** The review agent (⑤) judges *product quality*; the *flow* decision at the checkpoint (continue / recall to \<node\> / skip / done / stop) is still the human's. What is automated: node-internal self-loops (③) and the ⑤ quality gate (NEEDS-FIX auto-routes a back-edge before the human ever sees a sub-par product). Both are bounded — ③ by `max_inner_turns`, ⑤ by a **3× same-finding-reject** escalation to checkpoint (M6 fix: standardized to `3×` everywhere — §2.5 `max_back_edges_total`, §5 error handling, and C9 now agree; the earlier `≤3` was an operator inconsistency). **Bound precedence (M6):** within one node, `max_inner_turns` (per-node ceiling) fires before the 3× same-finding escalation; same-node ⑤ back-edges do NOT increment the global `max_back_edges_total` (they are bounded by `max_inner_turns`); cross-node back-edges do. Global `max_graph_turns` is the absolute backstop that overrides all per-node/cross-node counters.
3. **No hardcoded project/domain names** — graph blocks reference `phases.*` skills; routing rules are project-declared in `summoner.yaml`, not framework-baked.
4. **Agents never call other agents** — back-edges (including ⑤ NEEDS-FIX) are executed by the **walker** (§10), never by the review agent spawning an upstream node, and never by a node spawning its own reviewer. The review agent only *reads artifact paths* and *returns a verdict with its own tool-call evidence*; the walker acts on the verdict. (Extends `persona-composition.md`'s "personas never call other personas" to all agents, consistent with Claude Code's "subagents cannot spawn subagents" platform limit.)
5. **Post-game review is mandatory** — at workflow end, the existing 5-type questionnaire fires. New Type-1 triggers (`handoff_reject` and ⑤ NEEDS-FIX events) feed the reject-redo signal into memory, same as human "方向不对" today.
6. **Review-agent independence is enforced by tool-use, not by a prompt promise (H1 fix).** ⑤ runs in a separate context, receives an envelope of artifact **paths** + `exit_criteria` + a one-line factual claim (no producer reasoning), and **independently re-derives** its findings by running its own `Read`/`grep`/`Bash` against those paths. Its tool calls are logged as the verdict's evidence. This is the whole point: it cannot be lulled by the producer's self-justifying reasoning (none crosses the boundary), only by the artifacts themselves — which is exactly the article's "verdict needs evidence external to the system." The producer's **self-reported pass verdict does not cross the boundary either** (B2): `attempt_history` carries `{node, attempts, verifier}` only — never `passed` — so ⑤ is not pre-primed by the producer's own "I passed." A ⑤ that read producer reasoning, or that returned a verdict with no tool-call evidence, would degenerate into "agent stamps its own work." The `review-isolation-check` scorer verifies the verdict has `evidence_tool_calls` and that the envelope's `stripped` includes `producer_reasoning_trace` AND `producer_verdict_self_report`.

### 2.8 ⑤ scheduling + the entry (bootstrap) envelope (B4 + B5 fix)

**B4 — who triggers ⑤ (it is not a sub-step of RUN_NODE):** The walker, not the node agent, schedules ⑤. The node agent runs ①→②→③→④ only; after ④ it hands the envelope to the walker via `summoner-walker record --step handoff`. The walker then emits a `RUN_REVIEW` directive (§10.1) — a **distinct directive** from `RUN_NODE` — and SKILL.md spawns the `review-agent` persona with the just-recorded envelope's `envelope_id` (paths + exit_criteria only, no producer reasoning). ⑤ returns a `review_verdict` event; the walker records it and routes forward or back-edge. This closes the gap where ⑤ "might never fire" if left to SKILL.md's ad-hoc judgment — the walker explicitly sequences `RUN_NODE` (①–④) → `RUN_REVIEW` (⑤) per node. A node cannot spawn its own reviewer (invariant #4); the walker is the single scheduler.

**B5 — the first node has no upstream producer, so who builds its ① Ingest envelope?** The walker synthesizes a **bootstrap envelope** for the first node from `{user_input, memory_patterns}` (Phase 0 output):
- `envelope_id`: `h-000`; `from_node`: `phase0`; `to_node`: `<first node>` (e.g. `diagnose`).
- `artifacts`: paths to the user-supplied input artifact (pasted error log / complaint text saved to a temp file) + memory pattern refs.
- `exit_criteria`: `[]` (Phase 0 makes no node-level claims).
- `attempt_history`: `[]`.
- `budget_remaining`: initialized from the graph `budget` block, minus a **documented Phase-0 cost** (a fixed `phase0_cost` declared in the `budget` block, e.g. `{graph_turns: 2, tokens: 8000}` — matching the C4 fixture's `18/42000` when `max_graph_turns:20`/`total_token_budget:50000`).
- The first node's ① Ingest validates the bootstrap envelope is well-formed, but does NOT emit a `handoff_reject` back-edge if it is sparse (there is no upstream node to return to — §5's "no back-edge target" rule applies; surface to user instead). Phase 0 is not a graph node and is not executed by the walker; only its budget cost is pre-decremented.

### 2.9 Checkpoint UX — the human can see where they are and intervene (M9 fix, user-raised)

The user's concern: node names like `fix` / `antia-logic` and step labels like ①②③④⑤ are machine-internal and make it hard for a human to understand the run and intervene when something is off. The fix is a **rendering layer** at the checkpoint, not a change to the machine contract:

- **Node `label` (required).** Every graph node declares a `label:` plain-language verb (§2.5). The checkpoint shows `label`, never the bare `id` or `skill` name. (`fix` → "补 nil 判空"; `triage` → "分流症状"; `investigate` → "追道具消失路径".)
- **Internal step names ①⓪②③④⑤ are hidden from the human.** The checkpoint renders two human-meaningful verdicts: "机器自检"(③) and "独立复查"(⑤), with the ⑤ evidence (grep hits, file:line) shown as proof. The ① Ingest / ⓪ snapshot / ④ Handoff steps are implementation detail and stay out of the checkpoint block.
- **"You are here" map.** The checkpoint renders the walked route with ✓/✗/▶ markers (current node, nodes back-edged from, skipped nodes, remaining budget) so the human knows their position in a cyclic walk — not just a one-line `[continue]/[recall]/[stop]`.
- **Annotated recall options.** Each `[recall to <label>]` option carries a one-line "回 <label> 干什么" annotation, and the walker **precomputes the default** (on ⑤ NEEDS-FIX, the default is `recall to <producer label>`; the human can accept it with one keystroke). The human picks by consequence, not by remembering node ids.
- **Two walker subcommands.** `summoner-walker explain` renders the human-facing map+narrative for the checkpoint block; `summoner-walker status` prints the raw machine state (node/attempt/counters/budget) for debugging and scorers. SKILL.md calls `explain` into the checkpoint, not `status`.

## 3. Components Touched

| Component | Change | New? |
|---|---|---|
| `references/node-contract.md` | The node contract: ①⓪②③④ agent run + walker-scheduled ⑤ (incl. ⓪ snapshot, ⑤ Review-agent independent re-derivation), typed handoff envelope + `review_verdict` + `evidence_tool_calls`, decidable/SOFT exit-criteria discipline, idempotent-retry rule, `attempt_history`/`budget_remaining` fields | **New** |
| `references/manifest-spec.md` | Add §Node Types + §Conditional Routing Rules; mark `graph` block as plan-time (not manifest-time); add `after_node` to checkpoints enum; add graph `oneOf` branch (C3) | Edit |
| `references/checkpoint-protocol.md` | Extend `[recall]` → `[recall to <node> reason=...]`; reframe checkpoint as *human FLOW gate* (quality already gated by ⑤); add verdict-node `clean_context` entry behavior | Edit |
| `references/workflow-reference.md` | Add §Per-task Graph section; document walker-vs-chain fallback; add graph red flags (incl. "review agent returned verdict with no `evidence_tool_calls` = fail"; "⑤ read producer reasoning = fail") | Edit |
| `references/trace-protocol.md` | Add event types: `handoff`, `handoff_reject` (cross-node reject), `node_review_retry` (same-node ⑤ NEEDS-FIX, node-internal), `node_test_loop`, `node_turn`, `review_verdict` (with `evidence_tool_calls`) | Edit |
| `references/scoring-system.md` | Add P0 scorers: `handoff-contract-check`, `verifier-checklist-check`, `review-isolation-check`; wire into regression/stability | Edit |
| `scorers/deterministic/handoff-contract-check.sh` | Validate every inter-node edge has a `handoff` event (exclude bootstrap `from_node:phase0` h-000) with `envelope_id` + non-empty `artifacts`(paths) + `exit_criteria` (each with `verdict_type`) + `factual_claim`; correlate to its `review_verdict` event via `envelope_id` (B1/M8). Reject any `handoff`-type event carrying a field outside the §2.2 allow-list (`producer_reasoning_trace` / `handoff_note` / `passed` = FAIL — note: `passed` is legitimate on `node_test_loop` events, NOT on `handoff`; the filter is per-event-type). Presence of an unlisted field = producer-reasoning leak. Mechanical (jq on JSONL). | **New** |
| `scorers/deterministic/verifier-checklist-check.sh` | Confirm every `exit_criteria` entry in the graph block has a `verdict_type: DECIDABLE\|SOFT` declared at authoring time (B3). For each node, assert every DECIDABLE criterion has a `node_test_loop` event with `passed:true`; assert no node yields PASS while a SOFT criterion is the *only* satisfied one. **"判不了" is NOT classified by the scorer** — the scorer only checks `verdict_type` is *present*; whether a criterion is DECIDABLE or SOFT is decided at plan-write time against the criterion-name registry (a bash script cannot semantically classify; it only enforces the author tagged it). Mechanical. | **New** |
| `scorers/deterministic/review-isolation-check.sh` | For every `review_verdict` event: non-empty `evidence_tool_calls` (reviewer ran own tools) AND its correlated `handoff` event's `stripped` includes `producer_reasoning_trace` AND `producer_verdict_self_report` (B2); `attempt_history` entries must NOT carry a `passed` field. Correlate via `envelope_id`. Mechanical. | **New** |
| `agents/review-agent.md` | Generic per-node reviewer: receives envelope of artifact **paths** + `exit_criteria` (each with `verdict_type`) + one-line factual claim (NO producer reasoning, NO producer `passed` verdict); **independently runs Read/grep/Bash to re-derive findings**; for SOFT criteria must run the criterion's `grep_pattern` and log it in `evidence_tool_calls`; returns `review_verdict` (PASS / NEEDS-FIX + file:line findings) with `evidence_tool_calls`. Never reads producer context; never calls other agents; returns verdict + evidence only. | **New** |
| `cmd/summoner-walker/` (Go binary, §10) | Reads `summoner-task-graph` YAML, tracks walk-state (node/attempt, per-finding counter, alternating-finding window, back-edge-return-path stack, global budget, pending-review envelope_id), emits `node_turn`/`handoff`/`handoff_reject` (cross-node) /`node_review_retry` (same-node ⑤)/`review_verdict` events. Emits `RUN_NODE` (①–④) and **`RUN_REVIEW`** (⑤, B4) directives + **`explain`** subcommand for human-facing checkpoint rendering (M9). Does NOT execute agents; does NOT touch the working tree (M2). | **New** |
| `internal/graph/` (Go pkg) | Graph parse + walk-state machine + budget enforcement (reuses `internal/` layout). Walker is a thin CLI over this. | **New** |
| `scripts/node-snapshot.sh` | ⓪ working-tree snapshot/restore helper: `git stash --include-untracked` (M2: `-u` covers new files the `reproduce`/`fix` nodes write) / patch-snapshot, for mutating-node idempotent retry (H2). **Owner = SKILL.md**, driven by walker `RUN_NODE` directive's `snapshot: before_②` / `restore: before_retry` flags (M2: walker signals intent, SKILL.md executes the helper — single owner, walker never touches the tree). | **New** |
| `skills/summoner/SKILL.md` | Phase Execution: when a plan carries a `summoner-task-graph`, call `summoner-walker next` → if `RUN_NODE` with `snapshot:` flag, run `node-snapshot.sh save` → run the node ①→②→③→④ only → call `summoner-walker record --step handoff`; walker returns `RUN_REVIEW` → spawn `review-agent` with envelope → call `summoner-walker record --step review_verdict`; on back-edge with `restore:` flag run `node-snapshot.sh restore` before re-running ② → repeat; render `summoner-walker explain` into the checkpoint. Else chain fallback. | Edit |
| `commands/fix.md`, `commands/new.md` | Move the routing tables into named `route_*` rules (declarative `map:` in `summoner.yaml`, §2.6.1) referenced by graph blocks; commands become thinner | Edit |
| `skills/summoner-writing-plans` (or superpowers:writing-plans integration) | Plan artifact must emit a fenced ` ```yaml summoner-task-graph ` block (§10.1 extraction contract, M4) with `budget` (incl. `phase0_cost`), `label` per node, `verdict_type` per criterion, `mutating` flags. Emit graph iff ≥3 nodes OR mutating+back-edge (§2.6.1, M12). | Edit/Note |
| `references/summoner.schema.json` + `hooks/hooks/vendor/.../memory-validator/validate.go` (M1) | Add `after_node` to checkpoints enum + graph `oneOf` branch + `routing_rules` schema. **The JSON schema is documentation-only** — the real validator is the vendored Go at `validate.go:197` (hardcodes enum `after_each\|manual\|none`); edit BOTH and fix the latent `manual` vs `after_merge` divergence. | Edit |
| `tests/fixtures/traces/` | Add graph-mode fixtures: clean pass (⑤ PASS), ⑤ NEEDS-FIX→back-edge, ③ FAIL→retry (with ⓪ restore), review-isolation-violation (no `evidence_tool_calls`), alternating-finding-escalation. **Already written:** C4 old-vs-new (single-file decidable) + C10 old-vs-new (cross-file SOFT, customer-complaint) — both with same-defect controls for a fair before/after Δ. | **New** |

## 4. Data Flow (one fix workflow, graph mode)

```
User: /summoner:fix "SC_ErrInnerLogic nil pointer in task"
  │
  ├─ Phase 0: memory retrieval (unchanged, ≤1500 tok)
  │
  ├─ Plan (writing-plans) decomposes into summoner-task-graph:
  │     diagnose → reproduce → fix → verify → review, with back_edges
  │
  ▼ Graph walk — SKILL.md markdown-protocol drives the §10 walker:
    SKILL.md calls `summoner-walker next` (→ RUN_NODE / RUN_REVIEW),
    runs the node's ①–④ (or the review-agent for ⑤), calls `record`,
    and renders `summoner-walker explain` into each checkpoint. The
    walker routes + bookkeeps; it does NOT execute agents (§10).
  ┌─ diagnose ─────────────────────────────────────────────────┐
  │ (walker: RUN_NODE id=diagnose label="定位根因" mutating=false)│
  │ ① Ingest: bootstrap envelope (envelope_id h-000, from Phase 0;  │
  │           {user_input, memory_patterns}; exit_criteria=[]) —    │
  │           well-formed check only, no back-edge (no upstream)     │
  │ ⓪ (skip — read-only node)                                    │
  │ ② Work: antia-debug skill → root cause                       │
  │ ③ Test: exit_criteria[root_cause] verdict_type=SOFT,         │
  │        pin "file:line + hypothesis"; retry if pin missing     │
  │        (max_inner_turns=3, M6)                                │
  │ ④ Handoff: envelope{envelope_id h-001, artifacts:[report.md,  │
  │            task.go:142], exit_criteria[{root_cause,SOFT},...], │
  │            factual_claim, attempt_history[{diagnose,1,...}],  │
  │            budget_remaining, stripped:[producer_reasoning,    │
  │            producer_verdict_self_report]} → walker record      │
  │ (walker → RUN_REVIEW envelope_id=h-001)                       │
  │ ⑤ Review-agent (separate context): INDEPENDENTLY re-derives │
  │     by Read/grep on task.go for all SubTask deref sites;     │
  │     evidence_tool_calls logged; PASS → review_verdict event   │
  │     (envelope_id h-001); NEEDS-FIX → walker back-edge         │
  └─────────────────────────────────────────────────────────────┘
       │ checkpoint — human FLOW gate; `walker explain` renders:
       │  📍 定位根因 ✓ · 机器自检通过 · 独立复查通过(证据:grep …)
       │  [continue]/[recall to <label>…]/[recall to plan]/[stop]
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
  ┌─ fix (conditional edge route_by_diagnosis → antia-logic) ─────┐
  │ (walker: RUN_NODE id=fix label="补 nil 判空" mutating=true       │
  │           attempt=1 snapshot=before_②)                          │
  │ ① Ingest: validate reproduce envelope + review_verdict=PASS ✓     │
  │ ⓪ Snapshot: SKILL.md runs `node-snapshot.sh save`                │
  │           (git stash -u — covers new files) BEFORE ②              │
  │ ② Work: apply fix                                                   │
  │ ③ Test: no_compile_error (DECIDABLE); on FAIL → walker signals     │
  │        restore:true; SKILL.md runs `node-snapshot.sh restore`      │
  │        then re-runs ② ≤4 turns                                     │
  │ ④ Handoff: envelope{envelope_id h-003, artifacts:[task.go + diff], │
  │            exit_criteria[{diff_applied,DECIDABLE},                  │
  │            {no_compile_error,DECIDABLE},                           │
  │            {all_deref_sites_covered,SOFT,grep_pattern:player.SubTask}],│
  │            factual_claim, attempt_history[{fix,1,verifier:build_clean}]}│
  │            → walker record → RUN_REVIEW h-003                       │
  │ ⑤ Review-agent (separate context): INDEPENDENTLY grep (player.SubTask│
  │     grep_pattern); finds task.go:187 unchecked → NEEDS-FIX + finding;│
  │     walker computes back-edge (same-node, attempt=2), signals      │
  │     restore:true; SKILL.md restores ⓪ — BEFORE checkpoint         │
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

- **Node self-loop exhausted (③ hits `max_inner_turns`):** node emits `node_test_loop` with `exhausted=true`; walker surfaces at checkpoint (via `explain`) as `⚠️ 定位根因 节点自检重试耗尽(N 轮)` and offers `[recall to <upstream label>]` / `[stop]`. Never silently burns unbounded tokens — the article's `MAX_TURNS` backstop. Bound precedence (§2.7 #2): `max_inner_turns` fires before 3× same-finding escalation.
- **Handoff reject (① fails):** `handoff_reject` event; walker routes back-edge; the reject reason becomes the upstream node's next Ingest input. If a back-edge loops **3×** on the same reject reason, escalate to checkpoint (likely a real direction problem → Type 1 review). (M6: standardized to 3×.)
- **⑤ Review-agent returns NEEDS-FIX — same node (node-internal retry):** the walker emits a **`node_review_retry`** event (NOT `handoff_reject`), re-runs the node's ② with the findings fed back, bounded by that node's `max_inner_turns`. No checkpoint. This is treated like ③ self-retry and does **NOT** increment the global `max_back_edges_total` (§2.7 #2).
- **⑤ Review-agent returns NEEDS-FIX — cross node (inter-node reject) / ① Ingest rejects an upstream handoff:** the walker emits a **`handoff_reject`** event and routes a back-edge to `from_node` (skipping the declared `skip:` set) *before* the checkpoint. The reject reason becomes the upstream node's next Ingest input. The human at the checkpoint only ever **gates** a ⑤-PASS product — the product presented for their flow decision is never sub-par (the whole point of offloading the quality read). The `walker explain` render *reports* any round-trip that occurred upstream (so the human is not blind to the graph having cycled — §2.1 clarification), but they do not *gate* the intermediate NEEDS-FIX state. If a **cross-node** ⑤ NEEDS-FIX or ① reject loops **3×** on the same finding/reason, escalate to checkpoint (⑤ may be misreading, or the producer can't satisfy the criterion — human decides). (M6.) Same-node ⑤ NEEDS-FIX is bounded by `max_inner_turns` *first* (§2.7 #2) and does not reach the 3× counter.
- **⑤ Review-agent not scheduled (B4):** the walker MUST emit `RUN_REVIEW` after every `RUN_NODE` ④ `record handoff` (§2.8). If a non-bootstrap `handoff` event (`envelope_id != "h-000"` — equivalently `from_node != phase0`, per §2.8's bootstrap definition pinning `h-000` to `from_node:phase0`) in the trace has no correlated `review_verdict` event (join by `envelope_id`), `review-isolation-check` flags it as ⑤-skip — the node is re-queued for review. The bootstrap `h-000` (from Phase 0, not a graph node) legitimately has no `review_verdict` and is exempt (§2.8). ⑤ never silently fails to fire on a real node.
- **Alternating findings (H6/M7 — the unbounded case):** `alternating_finding_window: N` terminates a cycle where ⑤ keeps raising *different* findings. **"Rotate" is defined concretely (M7):** within the last N ⑤-NEEDS-FIX events on the same node, if ≥2 distinct findings each reappear (a finding that appeared at position i reappears at position j>i with ≥1 different finding between them), the window rule escalates to checkpoint. Precedence (§2.7 #2): the 3× same-finding rule fires first for a single repeating finding; the alternating-window rule fires only for the cross-finding rotation case. Together they bound every ⑤-cycle.
- **⑤ Review-agent context leak (it somehow saw producer context):** `review-isolation-check` scorer flags it; that node's `review_verdict` is voided and the node re-runs ⑤ with a correctly-scoped envelope. This is a correctness defect, not a flow decision — never surfaces to the human as "your call."
- **⑤ Review-agent unreachable / dispatch failure:** treat as ③-exhausted equivalent — surface at checkpoint, offer `[recall to <upstream label>]` / `[stop]`. Never block the workflow silently.
- **Verdict FAIL with no upstream fix node (e.g., review rejects on a debug-only workflow):** no back-edge target → surface to user as a checkpoint decision, don't fabricate a target.
- **Graph parse failure (malformed `summoner-task-graph` block; includes missing ```yaml summoner-task-graph fence, M4):** fall back to chain mode + warn at checkpoint ("plan graph malformed — running chain fallback"). Never block the user.
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
| C9: ⑤ 3× same-finding escalation | Bounded auto-redo | cross-node ⑤ NEEDS-FIX fires 3× on the same finding → the 3rd escalates to checkpoint instead of looping forever (same-node ⑤ is bounded by `max_inner_turns` first — §2.7 #2) | escalation trace; the 4th same-finding cross-node back-edge never fires |
| C10: cross-file SOFT (customer-complaint symptom, no stack) | ⑤ value when the verifier is NOT cleanly decidable — the dirty case | ⑤ on triage independently greps drop/clear call-sites across files → finds a 2nd drop path (inventory.go:410) the producer missed; ⑤ on fix runs the SOFT criterion's `grep_pattern` across `inventory/` → finds :410 still unguarded after attempt-1 only patched sync.go:230; back-edge before checkpoint; `all_drop_sites_guarded` is SOFT and REQUIRED its `grep_pattern` as `evidence_tool_calls` (B3). Zero human rework, 2 defects caught before human | `verifier-checklist-check` (SOFT+grep_pattern); `review-isolation-check` (⑤ ran grep_pattern); NEEDS-FIX×2 trace; control: old chain would let both through to the Phase-5 human |

For each case: run under **old (chain) Summoner** and **new (graph+⑤) Summoner**, capture traces, run `regression-test.sh` + `stability-test.sh` (≥5 runs, fix workflow 0% tolerance per scoring-system), and report Δ on: P0 score, **human-intervention count**, token usage. **Two sub-counts of human-intervention (both reported):**
- **total checkpoint interactions** — every checkpoint gate where a human pressed a key (`continue` / `done` / `recall` / content-feedback). The fixtures' `human_interventions` field counts this (C4-old: 5 → C4-new: 3).
- **expensive interventions** — the subset that are `recall` or content-feedback (i.e. the human had to *judge quality or correct direction*, not just OK a ⑤-PASS product). ⑤ offloads the quality read, so this sub-count is what the upgrade drives toward 0 (C4-old: 1 recall → C4-new: 0).

The goal's "smoother / higher correctness / less human intervention" is quantified as: P0 score ↑, total checkpoint interactions ↓, expensive interventions → 0 (most directly via C4 + C10 — defects ⑤ catches before the human, including on the non-decidable-verifier cross-file case), token neutral-or-down.

**Fixtures already written (feasibility proof):** `example-C4-old-chain-lets-defect-through.jsonl` (control: 5 interventions, 0 defects-caught-before-human, 1 rework round) vs `example-C4-new-graph-review-agent-catches.jsonl` (3 interventions, 1 caught, 0 rework) — single-file decidable case. `example-C10-old-chain-lets-defect-through.jsonl` (control: 5 interventions, 0 caught, 1 rework) vs `example-C10-cross-file-soft-⑤-catches.jsonl` — the dirty cross-file SOFT case (4 interventions, 2 caught, 0 rework). Both old/new pairs use the SAME defect per case for a fair Δ (C4: task.go:187; C10: inventory.go:410 Clear() + sync.go:230). The two NEW fixtures conform to the revised §2.2 contract (`envelope_id` correlation, no `passed` in `attempt_history`, `verdict_type` on every criterion, `RUN_REVIEW`, `node-snapshot` owner=SKILL.md + `git stash -u`); the two OLD fixtures are pre-graph baselines and are not subject to the §2.2 contract (they have no envelopes). C2 (full-graph clean pass) and C3/C5/C6/C7/C8/C9 fixtures are still outstanding deliverables (§3).

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
| B1 | C4 "proof" fixture had `review_verdict` as separate events but §2.2 showed it inline; no join key | §2.2.1: `review_verdict` is a standalone event; `envelope_id` is the correlation key on both `handoff` and `review_verdict`; scorers join by id. |
| B2 | `attempt_history.passed` smuggled the producer's self-reported verdict across the boundary (invariant #6 leak) | §2.2 `attempt_history` carries `{node, attempts, verifier}` only — no `passed`; `stripped` lists `producer_verdict_self_report`; invariant #6 updated. |
| B3 | `all_deref_sites_covered`/`root_cause` are LLM judgments sitting unlabelled in DECIDABLE-looking lists; bash cannot classify "判不了" | §2.5 graph block: each `exit_criteria` entry declares `{name, verdict_type: DECIDABLE\|SOFT, pin?/grep_pattern?}`; §3 scorer only checks `verdict_type` is *present* (author tags at plan time, scorer doesn't semantically classify). |
| B4 | ⑤ had no trigger — walker emitted only `RUN_NODE`; ⑤ might never fire | §2.8 + §10.1: walker emits `RUN_REVIEW` (distinct from `RUN_NODE`) after every `record --step handoff`; SKILL.md spawns `review-agent`; §5 flags ⑤-skip if a `handoff` lacks its `review_verdict`. |
| B5 | First node had no upstream producer for its ① Ingest envelope; budget accounting for Phase 0 undefined | §2.8: walker synthesizes a bootstrap envelope (`h-000`, `exit_criteria:[]`, `budget` minus declared `phase0_cost`); ① validates well-formedness only, no back-edge. |
| M1 | Schema edits were inert — real validator is vendored Go `validate.go:197` (hardcodes enum; already diverged `manual` vs `after_merge`) | §3 + §10.3: name the real file; edit both + fix the divergence; JSON schema marked documentation-only. |
| M2 | `node-snapshot.sh` had 3 owners; `git stash` without `-u` misses new files (`reproduce`/`fix` create them) | §3 + §10.1: owner = SKILL.md (walker signals `snapshot:`/`restore:` flags via `RUN_NODE`, never touches tree); `git stash --include-untracked`. |
| M3 | `route_by_diagnosis` interface (I/O shape, declarative-vs-executable) unspecified; no schema home | §2.6.1: declarative `routing_rules:` map in `summoner.yaml`; input = `routing_tag` field, output = node id; pure table lookup, no model/script. |
| M4 | Graph-YAML-in-markdown extraction (fence? heading? fragment?) unspecified → parser guesswork | §3 + §5: plan emits fenced ` ```yaml summoner-task-graph ` block; walker matches info-string; parse failure → chain fallback. |
| M6 | `max_inner_turns` / `max_back_edges_total` / 3× escalation precedence unspecified; ≤3 vs ≥3 vs 3× inconsistent | §2.7 #2: precedence = `max_inner_turns` → 3× same-finding → `max_back_edges_total` → `max_graph_turns`; same-node ⑤ doesn't increment global counter; standardized to 3×. |
| M7 | `alternating_finding_window` "rotate" undefined (a name, not a mechanism) | §5 + §10.2: "rotate" = ≥2 distinct findings each reappear within the window; precedence vs 3× same-finding rule specified. |
| M9 | No "you are here" checkpoint rendering; machine-internal names (id/⑤) leaked to humans; hard to intervene | §2.9 + §2.5 `label` + §10.1 `explain`: human-facing map + narrative + annotated recall options + walker-precomputed default; `status` stays for debug. |

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
2. **Human intervention ↓:** fewer total checkpoint interactions per task in new vs old (measured by counting `checkpoint` trace events with human `user_input`), and **expensive interventions (recall + content-feedback) → 0** (measured as the subset of those checkpoints where `user_input` is not a bare `continue`/`done`). This is the headline win — ⑤ catches defective handoffs *before* the human would have had to read them, so the human only ever gates a ⑤-PASS product with a bare continue.
3. **Smoother:** post-game Type-4 rating ≥ old; Type-1 (direction correction) frequency ↓ (reject-redo handled inside graph before reaching the human).
4. **Real-world:** at least one case run against a real bug in this repo (not only synthetic fixtures), demonstrating ⑤ catching a defect the old chain would have let through to the human reviewer — with fixture-proven before/after Δ on **both** the single-file decidable case (C4: old 5/0/1 → new 3/1/0) and the cross-file SOFT case (C10: old 5/0/1 → new 4/2/0, same-defect control).

## 10. The Walker (real graph runtime — H4 fix)

The adversarial review established that a markdown protocol (prose instructions to an LLM) cannot deterministically walk a cyclic graph with skip-sets, conditional edges, per-finding counters, and global budgets — the "no new runtime" claim was false. This section adds the real, minimal runtime. **Design principle: the walker does NOT execute agents.** It only (a) reads the per-task graph, (b) tracks walk-state, (c) decides which node runs next, (d) emits trace events, (e) tells the SKILL.md flow what to do. All actual work (② Work, ③ Test, ⑤ Review) stays in agents/skills the SKILL.md invokes. The walker is a router + bookkeeper, not an executor.

### 10.1 Deliverable

`cmd/summoner-walker/` — a small Go binary (consistent with the existing `cmd/summoner-ctx/` and `hooks/hooks/` Go codebase; reuses `internal/`). Single responsibility: given a `summoner-task-graph` YAML + a stream of node-completion signals, emit the next-node directive + trace events.

```
summoner-walker --graph <plan.md#summoner-task-graph> --trace <trace.jsonl> next
  → prints: RUN_NODE id=fix label="补 nil 判空" skill=antia-logic
            clean_context=false mutating=true attempt=2 snapshot=before_② restore=before_retry
summoner-walker --graph ... record --node fix --step handoff --envelope <envelope.json>
  → appends handoff event (with envelope_id) to trace, updates walk-state, prints next directive
            → RUN_REVIEW envelope_id=h-003      (B4: walker schedules ⑤, not the node)
summoner-walker --graph ... record --node fix --step review_verdict --envelope_id h-003 --verdict NEEDS-FIX --findings <findings.json>
  → appends review_verdict event, computes back-edge target from graph.back_edges + skip-set,
    prints: RUN_NODE id=fix ... (same-node, attempt 3, restore=true) OR BACK_EDGE to=fix skip=[verify]
summoner-walker --graph ... explain
  → human-facing map: 📍 补 nil 判空 (第 2 次尝试) · 路线 ✓定位→✓复现→▶补判空 ·
    独立复查: NEEDS-FIX(grep player.SubTask → task.go:187 未判空) · 预算 9/20 · 38k/50k · 3/8
    默认: [recall to 补 nil 判空]   (walker precomputed)
summoner-walker --graph ... status
  → prints: node=fix attempt=3/4 graph_turns=11/20 budget=38000/50000 back_edges=3/8
```

### 10.2 Walk-state (what the walker tracks — the thing the LLM could not)

| State | Purpose | Fixes |
|---|---|---|
| current node + attempt # | which node, which retry | ③ bound |
| per-finding reject counter | 3× same-finding escalation | H6 (partial) |
| findings-seen-this-window + rotate predicate (M7) | alternating-finding detection: escalate when ≥2 distinct findings each reappear within the window | H6 (the unbounded case) |
| back-edge-return-path stack | "I am returning from verify→fix; on forward, skip reproduce" | H4 (skip-sets) |
| graph_turns / token / back_edges totals | global budget; halt at 0 (cross-node back-edges only; same-node ⑤ bounded by max_inner_turns, §2.7 #2) | H6 |
| attempt_history (read from trace, without `passed`) | hand to re-running node so it doesn't repeat a failed approach — but without the producer's self-reported verdict (B2) | H3 |
| pending review envelope_id | the ④ handoff awaiting its ⑤ review_verdict; if absent, `review-isolation-check` flags ⑤-skip (B4) | B1/M8/B4 |
| node label + route map | render `explain` (the human-facing checkpoint view, M9) from walk-state, not the trace | M9 |
| next forward checkpoint pending | report round-trips to the human at the next gate | C2 |

The walker writes walk-state to `$HOME/.claude/plugins/summoner/walk-state/{session_id}.json` (not the trace — the trace is the append-only log; walk-state is the mutable machine state the LLM was wrongly keeping in its head). This is **router bookkeeping, not ambient LLM state** (P3): SKILL.md reads only the current `RUN_NODE`/`RUN_REVIEW` directive + the current envelope each turn — walk-state lives in the file, never re-read wholesale into the orchestrator's context, so the orchestrator context stays bounded across graph turns (M5).

### 10.3 Schema + protocol changes (C3 fix)

- `references/summoner.schema.json` + the **real validator** `hooks/hooks/vendor/github.com/johnson-xue/memory-validator/validate.go` (M1): add `after_node` to the checkpoints enum and a `graph` `oneOf` branch + `routing_rules` schema. **The JSON schema is documentation-only** — no Go code reads it; the live validator hardcodes the enum at `validate.go:197` (`after_each|manual|none`). Edit BOTH the schema and the vendored Go, and fix the pre-existing `manual` (Go) vs `after_merge` (JSON) divergence in the same pass. (C3's fix only lands if the Go changes.)
- `references/checkpoint-protocol.md`: extend `RECALL` grammar to `recall to <node> reason=...` (the walker parses this; the LLM no longer improvises the target); add the **graph-mode checkpoint rendering spec** (M9): node `label` shown not `id`; internal step names ①⓪②③④⑤ hidden; "you are here" route map; annotated `[recall to <label>]` options with walker-precomputed default; SKILL.md renders `walker explain` into the checkpoint.
- `references/trace-protocol.md`: add event types `handoff` (with `envelope_id`, `label`), `handoff_reject` (cross-node reject), `node_review_retry` (same-node ⑤ NEEDS-FIX, node-internal — §2.1 clarification), `node_test_loop`, `node_turn`, `review_verdict` (standalone event, keyed by `envelope_id`, with `evidence_tool_calls` — §2.2.1).
- `skills/summoner/SKILL.md`: Phase Execution calls `summoner-walker next` → `RUN_NODE` (run ①–④, with `node-snapshot.sh` on mutating nodes per the directive's snapshot/restore flags, M2) → `record --step handoff` → walker returns `RUN_REVIEW` → spawn `review-agent` → `record --step review_verdict` → repeat; render `walker explain` into each checkpoint. Chain fallback stays for graph-less plans.

### 10.4 Why this is "minimal" and not over-engineered

- The walker is ~one Go package, no external deps beyond what `internal/` + `hooks/` already use. It does not call models, does not run tools, does not touch the working tree (that's ⓪ snapshot, a separate shell helper). It is a state machine for graph routing — exactly what LangGraph's `compile()` produces, but as a local binary instead of a Python library.
- It preserves the article's core: control flow is now in a structure (the walker's state machine), not in the model's ad-hoc judgment. The LLM's job shrinks to "run the node the walker tells me to run" — which is what makes the graph *real* instead of a "loop wearing a graph skin."
