# Graph & Node-Contract Upgrade Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upgrade Summoner from a linear phase-chain to a graph + node-contract architecture: every node becomes a closed-loop agent (①⓪②③④) with a walker-scheduled separate-context ⑤ Review-agent, declared as a per-task graph, enforced by a real Go walker + deterministic scorers — so machine-checked acceptance at node boundaries offloads the human quality-read (the §1.3 "人疏忽就遗漏" failure mode).

**Architecture:** A new Go binary `cmd/summoner-walker` over a new `internal/graph` package is the only new runtime. The walker reads a per-task `summoner-task-graph` YAML block (emitted by writing-plans into the plan artifact), tracks walk-state (node/attempt, per-finding counters, alternating-finding window, back-edge-return-path stack, global budget, pending-review envelope_id), emits `RUN_NODE`/`RUN_REVIEW` directives + trace events, and renders `explain` (human-facing) / `status` (debug). It does NOT execute agents and does NOT touch the working tree. SKILL.md drives the walker; `node-snapshot.sh` (owner = SKILL.md) handles ⓪ tree snapshots. Three new bash scorers (`handoff-contract-check`, `verifier-checklist-check`, `review-isolation-check`) enforce the typed handoff envelope + `review_verdict` discipline on the JSONL trace. Everything is backward-compatible (方案 A): plans without a `summoner-task-graph` block fall back to today's chain behavior; no existing `summoner.yaml` breaks.

**Tech Stack:** Go 1.16 (`github.com/johnson-xue/summoner` module; deps already present: `spf13/cobra`, `gopkg.in/yaml.v3`, `mattn/go-sqlite3`); bash + `jq` scorers; markdown reference docs; vendored Go manifest validator at `hooks/hooks/vendor/github.com/johnson-xue/memory-validator/validate.go`.

## Global Constraints

(From the spec — every task's requirements implicitly include these. Copied verbatim where exact.)

- **Go version floor:** go 1.16 (`go.mod` line `go 1.16`). Do NOT use APIs newer than 1.16 — in particular, no `t.Setenv` (1.17), no `os.ReadFile`/`os.WriteFile` (1.16 — these ARE available; but `io/ioutil` is the idiom the existing code uses), no generics (1.18). Match the table-driven-test idiom in `internal/context/memory_test.go`.
- **No hardcoded project/domain names.** Graph blocks reference `phases.*` skills; routing rules are project-declared in `summoner.yaml`. The framework repo ships NO `summoner.yaml` (per-project). Fixtures + tests use the existing example project name `"example"`.
- **Iron law: the walker does NOT execute agents and does NOT touch the working tree.** (§10, M2.) Tree snapshots are owned by SKILL.md via `node-snapshot.sh`; the walker only signals `snapshot:`/`restore:` flags in directives.
- **Backward compatibility (方案 A).** Existing manifests using checkpoints `after_each`/`manual`/`none` (the vendored Go enum) MUST still validate and behave as today. `after_node` is ADDED to the enum; `graph` is added as a new shape. No existing `summoner.yaml` breaks. (§2.6, §2.7.)
- **Chain-vs-graph rule (M12):** a plan emits a `summoner-task-graph` block iff it has ≥3 nodes OR any node has `mutating: true` with a back-edge; ≤2 nodes with no back-edge emit a plain chain. (§2.6.1)
- **Scorer exit-code convention (match existing `iron-law-check.sh`):** `0` = PASS, `1` = FAIL, `2` = SKIP (not applicable). Scorers read JSONL with `jq`; they are mechanical (no LLM, no semantic classification). (§2.7 #6, §3.)
- **Sensitive credential:** `.claude/settings.json` contains an `ANTHROPIC_AUTH_TOKEN` secret. NEVER stage or commit that file. Every commit must stage explicit paths only.
- **Frequent commits:** every task ends with a commit on branch `release/v0.1.8` (current branch) — or a feature branch off it if the implementer prefers. Commit messages end with `Co-Authored-By: Claude <noreply@anthropic.com>`.
- **Test command (Go):** `go test ./...` from repo root. **Test command (scorers):** `bash scripts/score-trace.sh --trace <file> --priority P0` and direct `bash scorers/deterministic/<name>.sh <trace>`.

## File Structure

New files (each one clear responsibility):

| File | Responsibility |
|---|---|
| `internal/graph/parse.go` | Parse + struct-decode + validate a `summoner-task-graph` YAML block (nodes/edges/conditional_edges/back_edges/budget). Pure, no I/O. |
| `internal/graph/parse_test.go` | Unit tests for parse.go (table-driven, go 1.16 idiom). |
| `internal/graph/walk.go` | The walk-state machine: advance, record-handoff, record-review-verdict, back-edge routing, budget enforcement, same-node `node_review_retry` vs cross-node `handoff_reject`. Pure logic over a `WalkState` struct. |
| `internal/graph/walk_test.go` | Unit tests for walk.go, driven by the existing C4-new + C10-new fixtures. |
| `internal/graph/render.go` | `Explain()` (human-facing map+narrative, M9) + `Status()` (machine debug). Pure formatting over `WalkState`. |
| `internal/graph/render_test.go` | Unit tests for render.go. |
| `internal/graph/walkstate.go` | `WalkState` struct + load/save to `$HOME/.claude/plugins/summoner/walk-state/{session_id}.json`. |
| `cmd/summoner-walker/main.go` | Thin cobra CLI over `internal/graph` (matches `cmd/summoner-ctx/main.go` idiom): `next`/`record`/`explain`/`status` subcommands + `--graph`/`--trace`/`--session` flags. |
| `cmd/summoner-walker/main_test.go` | CLI integration tests (build the binary, feed a graph, assert directive output). |
| `scorers/deterministic/handoff-contract-check.sh` | P0 scorer: every non-bootstrap inter-node edge has a `handoff` event with `envelope_id`+artifacts+exit_criteria(verdict_type)+factual_claim, correlated to a `review_verdict`; reject handoff fields outside the §2.2 allow-list. |
| `scorers/deterministic/verifier-checklist-check.sh` | P0 scorer: every `exit_criteria` entry in the graph block has a `verdict_type`; every DECIDABLE criterion has a `node_test_loop` with `passed:true`; no PASS while only SOFT satisfied. |
| `scorers/deterministic/review-isolation-check.sh` | P0 scorer: every `review_verdict` has non-empty `evidence_tool_calls`; correlated handoff's `stripped` includes `producer_reasoning_trace`+`producer_verdict_self_report`; `attempt_history` entries carry no `passed`. |
| `scripts/node-snapshot.sh` | ⓪ working-tree snapshot/restore: `git stash --include-untracked` (`-u`) save / `git stash pop` restore. Owner = SKILL.md. |
| `agents/review-agent.md` | The generic per-node ⑤ reviewer persona (independent re-derivation, envelope of paths, evidence_tool_calls). |
| `references/node-contract.md` | The node contract reference (①⓪②③④ + walker-scheduled ⑤, typed envelope, decidable/SOFT discipline, idempotent retry). |
| `tests/fixtures/traces/example-C2-clean-graph-pass.jsonl` | C2 fixture: full-graph clean pass (every ⑤ PASS). |
| `tests/fixtures/traces/example-C3-verify-fail-backedge.jsonl` | C3 fixture: verify ③ FAIL → retry → exhausted → back-edge to fix skipping reproduce. |
| `tests/fixtures/traces/example-C5-review-isolation-violation.jsonl` | C5 fixture: adversarial — review_verdict with NO evidence_tool_calls → scorer FAILs. |
| `tests/fixtures/traces/example-C9-three-times-same-finding.jsonl` | C9 fixture: cross-node ⑤ NEEDS-FIX 3× same finding → 3rd escalates to checkpoint. |

Modified files (exact edit points in their tasks):

| File | Change |
|---|---|
| `references/trace-protocol.md` | Add event types `handoff`, `handoff_reject`, `node_review_retry`, `node_test_loop`, `node_turn`, `review_verdict` (+ their fields). |
| `references/summoner.schema.json` | Add `after_node` to checkpoints enum; add `graph` oneOf branch + `routing_rules` schema. |
| `hooks/hooks/vendor/github.com/johnson-xue/memory-validator/validate.go` + `hooks/vendor/.../validate.go` | Add `after_node` to enum at line ~197; fix `manual`(Go) vs `after_merge`(JSON) divergence. |
| `references/scoring-system.md` | Register the 3 new P0 scorers; wire into regression/stability. |
| `references/checkpoint-protocol.md` | Extend `RECALL` → `recall to <node> reason=…`; graph-mode rendering spec (M9). |
| `references/workflow-reference.md` | §Per-task Graph; walker-vs-chain fallback; graph red flags. |
| `references/manifest-spec.md` | §Node Types + §Conditional Routing Rules; graph block plan-time. |
| `skills/summoner/SKILL.md` | Phase Execution: when a plan carries `summoner-task-graph`, drive the walker; else chain fallback. |
| `commands/fix.md`, `commands/new.md` | Move routing tables into named `route_*` rules referenced by graph blocks. |
| `scripts/score-trace.sh` | Wire the 3 new scorers into P0 (if not already auto-discovered). |

---

## Task 1: Trace Protocol Reference — the typed-envelope + review_verdict contract

This is the foundation: every later task (walker, scorers, fixtures, SKILL.md) implements the event shapes defined here. No code, but it is the contract of record — getting it wrong propagates everywhere.

**Files:**
- Modify: `references/trace-protocol.md` (add event-type definitions; do not remove existing ones)

**Interfaces:**
- Produces (for later tasks): the canonical field lists for event types `handoff`, `handoff_reject`, `node_review_retry`, `node_test_loop`, `node_turn`, `review_verdict` — these exact field names are what `internal/graph` emits and the 3 scorers check.

- [ ] **Step 1: Read the current trace-protocol.md to match its section style**

Run: `sed -n '1,60p' references/trace-protocol.md`
Expected: an existing event-type list (session_start/phase_start/tool_call/reasoning/checkpoint/error). Match its heading depth and field-list style.

- [ ] **Step 2: Add the 6 new event types in a new "## Graph-Mode Event Types" section**

Append (after the existing event types, before any closing section) this block to `references/trace-protocol.md`. Every field name below is normative — later code emits these exact keys.

```markdown
## Graph-Mode Event Types

These events appear when a plan carries a `summoner-task-graph` block (§node-contract). They are emitted by the walker (`cmd/summoner-walker`) and SKILL.md; scorers join on `envelope_id`.

### `handoff`
Typed envelope produced by a node's ④ step. Fields:
- `envelope_id` (string, required) — e.g. `h-001`. Correlation key shared with the `review_verdict` that reviews it (§2.2.1). `h-000` is reserved for the bootstrap envelope.
- `from_node` (string, required) — producer node id; `phase0` for the bootstrap.
- `to_node` (string, required) — consumer node id.
- `label` (string, required) — human-facing verb from the graph node (M9).
- `artifacts` (array of strings, required, non-empty) — validated product paths / file:line refs.
- `exit_criteria` (array, required) — each entry `{name, verdict_type: "DECIDABLE"|"SOFT", pin?, grep_pattern?}`. SOFT SHOULD carry `grep_pattern`.
- `factual_claim` (string, required) — one line, fact only, NO producer reasoning.
- `attempt_history` (array, required) — each entry `{node, attempts, verifier}` only; MUST NOT carry `passed`.
- `budget_remaining` (object, required) — `{graph_turns_left, token_budget_left}`.
- `stripped` (array, required) — intentionally-dropped fields; MUST include `producer_reasoning_trace` AND `producer_verdict_self_report`.

Allow-list for `handoff` events: only the fields above. A `handoff` carrying `producer_reasoning_trace`, `handoff_note`, or `passed` = producer-reasoning leak (scorer FAIL). (`passed` is legitimate on `node_test_loop`, NOT on `handoff` — the filter is per-event-type.)

### `review_verdict` (standalone event, §2.2.1)
Emitted by the ⑤ Review-agent. Fields:
- `envelope_id` (string, required) — joins to the `handoff` it reviews.
- `node` (string, required) — the node whose ④ was reviewed.
- `reviewer` (string, required) — e.g. `review-agent:fix`.
- `verdict` (string, required) — `PASS` | `NEEDS-FIX`.
- `findings` (array) — on NEEDS-FIX: each `{file, line?, issue, fix}`.
- `evidence_tool_calls` (array of strings, required, NON-EMPTY) — the reviewer's OWN Read/grep/Bash invocations (invariant #6; empty = rubber-stamp = FAIL).

### `node_test_loop`
Node-internal ③ verifier result. Fields:
- `node` (string, required), `label` (string, required).
- `criterion` (string, required) — the exit-criteria name tested.
- `passed` (boolean, required) — legitimate HERE (not on `handoff`).
- `exhausted` (boolean) — `true` when `max_inner_turns` hit (§5).

### `node_turn`
Walker directive-to-trace echo. Fields:
- `node` (string, required), `label` (string, required), `attempt` (int, required).
- `step` (string, required) — e.g. `review-scheduled`.
- `walker_directive` (string, required) — the raw directive, e.g. `RUN_NODE id=fix attempt=2 snapshot=before_②` or `RUN_REVIEW envelope_id=h-002`.

### `node_review_retry` (same-node ⑤ NEEDS-FIX, §2.1 clarification)
Emitted by the walker when ⑤ returns NEEDS-FIX and `from_node == to_node` (node-internal retry). Fields:
- `envelope_id` (string, required), `from_node` (string, required), `reason` (string, required), `findings` (array), `executor` (string, required, `"walker"`).
- Does NOT increment the global back-edge counter (bounded by `max_inner_turns`).

### `handoff_reject` (cross-node reject)
Emitted by the walker on a cross-node ⑤ NEEDS-FIX or ① Ingest reject. Fields:
- `envelope_id` (string, required), `from_node` (string, required), `reason` (string, required), `findings` (array), `executor` (string, required, `"walker"`).
- Increments the global `max_back_edges_total` counter; 3× same-finding escalates to checkpoint.
```

- [ ] **Step 3: Verify the markdown is well-formed and the section landed**

Run: `grep -n '## Graph-Mode Event Types\|### `handoff`\|### `review_verdict`\|### `node_review_retry`\|### `handoff_reject`' references/trace-protocol.md`
Expected: 5 matching lines (the section + 4 of the 6 event headings — `node_turn`/`node_test_loop` also there).

- [ ] **Step 4: Commit**

```bash
git add references/trace-protocol.md
git commit -m "$(cat <<'EOF'
docs(trace-protocol): add graph-mode event types (handoff, review_verdict, node_review_retry, handoff_reject, node_test_loop, node_turn)

Canonical field lists for the node-contract upgrade. handoff carries
envelope_id+artifacts+exit_criteria(verdict_type)+factual_claim+
attempt_history(no passed)+budget_remaining+stripped. review_verdict is
a standalone event keyed by envelope_id with non-empty evidence_tool_calls.
node_review_retry (same-node ⑤) is distinct from handoff_reject (cross-node).

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: `internal/graph` — parse + validate the `summoner-task-graph` YAML

Pure parsing, no I/O. This is the walker's input contract. TDD: write the failing test (a sample graph → expected structs) first.

**Files:**
- Create: `internal/graph/parse.go`
- Create: `internal/graph/parse_test.go`

**Interfaces:**
- Consumes: a YAML byte-slice (extracted from a plan markdown fence — extraction is Task 4's job; this task takes raw YAML).
- Produces: `graph.Graph` struct + `graph.ParseGraph(yaml []byte) (*Graph, error)`. Later tasks (walk, render, CLI) import `github.com/johnson-xue/summoner/internal/graph`.

- [ ] **Step 1: Write the failing test for ParseGraph**

Create `internal/graph/parse_test.go`:

```go
package graph

import (
	"strings"
	"testing"
)

func TestParseGraph_Valid(t *testing.T) {
	yaml := strings.NewReader(`
budget:
  max_graph_turns: 20
  total_token_budget: 50000
  max_back_edges_total: 8
  alternating_finding_window: 4
nodes:
  - id: diagnose
    label: "定位根因"
    skill: phase.debug
    exit_criteria:
      - {name: root_cause, verdict_type: SOFT, pin: "file:line"}
    max_inner_turns: 3
    mutating: false
  - id: fix
    label: "补 nil 判空"
    skill: antia-logic
    exit_criteria:
      - {name: diff_applied, verdict_type: DECIDABLE}
      - {name: all_deref_sites_covered, verdict_type: SOFT, grep_pattern: "player.SubTask"}
    max_inner_turns: 4
    mutating: true
edges:
  - {from: diagnose, to: fix}
back_edges:
  - {from: fix, to: fix, reason: review_needs_fix}
`)
	b, _ := io_ReadAll(yaml)
	g, err := ParseGraph(b)
	if err != nil {
		t.Fatalf("ParseGraph returned error: %v", err)
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(g.Nodes))
	}
	if g.Nodes[0].ID != "diagnose" || g.Nodes[0].Label != "定位根因" {
		t.Fatalf("first node wrong: %+v", g.Nodes[0])
	}
	if g.Nodes[1].ExitCriteria[1].VerdictType != SOFT {
		t.Fatalf("expected SOFT, got %v", g.Nodes[1].ExitCriteria[1].VerdictType)
	}
	if g.Nodes[1].ExitCriteria[1].GrepPattern != "player.SubTask" {
		t.Fatalf("expected grep_pattern, got %q", g.Nodes[1].ExitCriteria[1].GrepPattern)
	}
	if g.Budget.MaxGraphTurns != 20 {
		t.Fatalf("budget wrong: %+v", g.Budget)
	}
}

func TestParseGraph_UndeclaredBackEdgeTarget_Fails(t *testing.T) {
	yaml := []byte(`
nodes:
  - id: diagnose
    label: "定位根因"
    skill: phase.debug
    exit_criteria:
      - {name: root_cause, verdict_type: SOFT}
    max_inner_turns: 3
edges:
  - {from: diagnose, to: fix}
back_edges:
  - {from: review, to: fix, reason: receiver_rejected_handoff}
`)
	_, err := ParseGraph(yaml)
	if err == nil {
		t.Fatal("expected error for back_edge referencing undeclared node 'review', got nil")
	}
}

func TestParseGraph_MissingVerdictType_Fails(t *testing.T) {
	yaml := []byte(`
nodes:
  - id: diagnose
    label: "定位根因"
    skill: phase.debug
    exit_criteria:
      - {name: root_cause}
    max_inner_turns: 3
edges: []
`)
	_, err := ParseGraph(yaml)
	if err == nil {
		t.Fatal("expected error for exit_criteria missing verdict_type, got nil")
	}
}
```

Note: `io_ReadAll` is a local helper (see Step 3) — go 1.16 has `io/ioutil`, we avoid `os.ReadFile` to match the existing idiom. Define the helper in the test file.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/graph/ -run TestParseGraph -v`
Expected: `FAIL` — package `graph` does not exist / `ParseGraph` undefined.

- [ ] **Step 3: Write the minimal implementation**

Create `internal/graph/parse.go`. Define structs + `ParseGraph` + validation (every back_edge/conditional_edges target must be a declared node; every exit_criterion must have `verdict_type`). `io_ReadAll` helper lives in the test file.

```go
package graph

import (
	"fmt"
	"io/ioutil"
	"strings"

	"gopkg.in/yaml.v3"
)

// VerdictType is DECIDABLE or SOFT (declared at plan time, §2.5).
type VerdictType string

const (
	Decidable VerdictType = "DECIDABLE"
	Soft      VerdictType = "SOFT"
)

// ExitCriterion is one entry in a node's exit_criteria list (§2.2).
type ExitCriterion struct {
	Name        string      `yaml:"name"`
	VerdictType VerdictType `yaml:"verdict_type"`
	Pin         string      `yaml:"pin,omitempty"`
	GrepPattern string      `yaml:"grep_pattern,omitempty"`
}

// Node is one node in the per-task graph.
type Node struct {
	ID            string          `yaml:"id"`
	Label         string          `yaml:"label"`
	Skill         string          `yaml:"skill"`
	ExitCriteria  []ExitCriterion `yaml:"exit_criteria"`
	MaxInnerTurns int             `yaml:"max_inner_turns"`
	Mutating      bool            `yaml:"mutating"`
	CleanContext  bool            `yaml:"clean_context"`
}

// Edge is a forward edge.
type Edge struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// ConditionalEdge names a routing rule (§2.6.1).
type ConditionalEdge struct {
	From  string   `yaml:"from"`
	Route string   `yaml:"route"`
	To    []string `yaml:"to"`
}

// BackEdge may carry a skip-set (borrow-point ②).
type BackEdge struct {
	From   string   `yaml:"from"`
	To     string   `yaml:"to"`
	Reason string   `yaml:"reason"`
	Skip   []string `yaml:"skip,omitempty"`
}

// Budget is the global bound (§2.5).
type Budget struct {
	MaxGraphTurns          int `yaml:"max_graph_turns"`
	TotalTokenBudget       int `yaml:"total_token_budget"`
	MaxBackEdgesTotal      int `yaml:"max_back_edges_total"`
	AlternatingFindingWin int `yaml:"alternating_finding_window"`
	Phase0CostTurns        int `yaml:"phase0_cost_turns,omitempty"`
	Phase0CostTokens       int `yaml:"phase0_cost_tokens,omitempty"`
}

// Graph is the parsed per-task graph.
type Graph struct {
	Budget           Budget           `yaml:"budget"`
	Nodes            []Node           `yaml:"nodes"`
	Edges            []Edge           `yaml:"edges"`
	ConditionalEdges []ConditionalEdge `yaml:"conditional_edges"`
	BackEdges        []BackEdge       `yaml:"back_edges"`
	Checkpoints      string           `yaml:"checkpoints"`
}

// ParseGraph decodes a summoner-task-graph YAML block and validates it.
func ParseGraph(b []byte) (*Graph, error) {
	var g Graph
	if err := yaml.Unmarshal(b, &g); err != nil {
		return nil, fmt.Errorf("graph yaml: %w", err)
	}
	if err := g.validate(); err != nil {
		return nil, err
	}
	return &g, nil
}

func (g *Graph) validate() error {
	declared := map[string]bool{}
	for _, n := range g.Nodes {
		if n.ID == "" {
			return fmt.Errorf("node missing id")
		}
		if n.Label == "" {
			return fmt.Errorf("node %s missing label (M9)", n.ID)
		}
		declared[n.ID] = true
		for _, c := range n.ExitCriteria {
			if c.VerdictType != Decidable && c.VerdictType != Soft {
				return fmt.Errorf("node %s criterion %s missing/invalid verdict_type (B3)", n.ID, c.Name)
			}
		}
	}
	for _, e := range g.Edges {
		if !declared[e.From] {
			return fmt.Errorf("edge from undeclared node %q", e.From)
		}
		if !declared[e.To] {
			return fmt.Errorf("edge to undeclared node %q", e.To)
		}
	}
	for _, be := range g.BackEdges {
		if !declared[be.From] {
			return fmt.Errorf("back_edge from undeclared node %q", be.From)
		}
		if !declared[be.To] {
			return fmt.Errorf("back_edge to undeclared node %q (BLOCKER: §2.5 must declare review)", be.To)
		}
	}
	for _, ce := range g.ConditionalEdges {
		if !declared[ce.From] {
			return fmt.Errorf("conditional_edge from undeclared node %q", ce.From)
		}
		// conditional_edges[].to are cross-task routing targets; the chosen one
		// must be declared in the RESOLVED per-task graph (§2.5 note). We do NOT
		// require all `to` entries to be declared here — only `from`.
	}
	return nil
}

// io_ReadAll is a test-helper alias for io/ioutil.ReadFile-of-a-reader.
// (Kept in parse.go so the test file can use it without re-importing.)
func io_ReadAll(r interface{ Read(p []byte) (int, error) }) ([]byte, error) {
	return ioutil.ReadAll(r)
}

// NodeByID looks up a node by id.
func (g *Graph) NodeByID(id string) (*Node, bool) {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i], true
		}
	}
	return nil, false
}

// firstNode returns the entry node (target of no forward edge), or the first
// declared node if the graph is degenerate.
func (g *Graph) firstNode() string {
	targets := map[string]bool{}
	for _, e := range g.Edges {
		targets[e.To] = true
	}
	for _, n := range g.Nodes {
		if !targets[n.ID] {
			return n.ID
		}
	}
	if len(g.Nodes) > 0 {
		return g.Nodes[0].ID
	}
	return ""
}

// suppress unused-import warnings if strings is only used elsewhere later.
var _ = strings.TrimSpace
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/graph/ -run TestParseGraph -v`
Expected: `PASS` (all 3 subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/graph/parse.go internal/graph/parse_test.go
git commit -m "$(cat <<'EOF'
feat(graph): parse + validate summoner-task-graph YAML

internal/graph.ParseGraph decodes the per-task graph block and validates:
every node has id+label, every exit_criterion has verdict_type (B3), and
every back_edge target is a declared node (BLOCKER fix — no undeclared
'review' references). Pure, no I/O.

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: `internal/graph` — walk-state machine + budget enforcement

The state machine the LLM could not keep in its head (H4). TDD against the existing C4-new + C10-new fixtures (they ARE the spec for how walk-state must behave).

**Files:**
- Create: `internal/graph/walkstate.go`
- Create: `internal/graph/walk.go`
- Create: `internal/graph/walk_test.go`

**Interfaces:**
- Consumes: `graph.Graph` (from Task 2), a session id, a trace writer.
- Produces: `graph.NewWalker(g *Graph, sessionID string, trace TraceWriter) *Walker` with methods `Next() Directive`, `RecordHandoff(env HandoffEnvelope) (Directive, error)`, `RecordReviewVerdict(v ReviewVerdict) (Directive, error)`. `Directive` carries `Kind` (RUN_NODE/RUN_REVIEW/BACK_EDGE/HALT/CHECKPOINT) + node id + attempt + flags.

- [ ] **Step 1: Define the types — walkstate.go**

Create `internal/graph/walkstate.go`:

```go
package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// DirectiveKind enumerates what the walker tells SKILL.md to do next.
type DirectiveKind string

const (
	RunNode     DirectiveKind = "RUN_NODE"     // run ①②③④ of a node
	RunReview   DirectiveKind = "RUN_REVIEW"   // spawn review-agent for an envelope
	BackEdge    DirectiveKind = "BACK_EDGE"    // cross-node back-edge (handoff_reject)
	NodeRetry   DirectiveKind = "NODE_RETRY"   // same-node ⑤ retry (node_review_retry)
	Checkpoint  DirectiveKind = "CHECKPOINT"   // surface to human
	Halt        DirectiveKind = "HALT"         // budget exhausted
)

// Directive is what the walker prints for SKILL.md each turn.
type Directive struct {
	Kind        DirectiveKind `json:"kind"`
	Node        string        `json:"node,omitempty"`
	Label      string        `json:"label,omitempty"`
	Attempt    int           `json:"attempt,omitempty"`
	EnvelopeID string        `json:"envelope_id,omitempty"`
	Snapshot   bool          `json:"snapshot,omitempty"`  // SKILL.md: node-snapshot.sh save before ②
	Restore    bool          `json:"restore,omitempty"`   // SKILL.md: node-snapshot.sh restore before retry
	CleanCtx   bool          `json:"clean_context,omitempty"`
	Skip       []string      `json:"skip,omitempty"`      // nodes to skip on a cross-node back-edge
}

// WalkState is the mutable machine state (§10.2). Lives in a file, not the LLM head.
type WalkState struct {
	SessionID    string            `json:"session_id"`
	CurrentNode  string            `json:"current_node"`
	Attempt      int               `json:"attempt"`
	GraphTurns   int               `json:"graph_turns"`
	TokensUsed   int               `json:"tokens_used"`
	BackEdges    int               `json:"back_edges"`         // cross-node only
	FindingsSeen map[string]int    `json:"findings_seen"`      // finding-text → count (3× escalation)
	Window      []string          `json:"window"`             // last N finding texts (alternating)
	PendingReview string          `json:"pending_review,omitempty"` // envelope_id awaiting review_verdict
	RouteMap    []routeEntry       `json:"route_map"`           // for explain render
}

type routeEntry struct {
	Node    string `json:"node"`
	Label   string `json:"label"`
	Status  string `json:"status"` // "pass" | "needs_fix" | "current" | "skipped"
}

// statePath returns the walk-state file location (§10.2).
func statePath(sessionID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".claude", "plugins", "summoner", "walk-state")
	return filepath.Join(dir, sessionID+".json"), nil
}

// LoadState reads walk-state for a session, or returns a zero state.
func LoadState(sessionID string) (*WalkState, error) {
	p, err := statePath(sessionID)
	if err != nil {
		return nil, err
	}
	b, err := ioutil_ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &WalkState{SessionID: sessionID, FindingsSeen: map[string]int{}}, nil
		}
		return nil, err
	}
	var s WalkState
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.FindingsSeen == nil {
		s.FindingsSeen = map[string]int{}
	}
	return &s, nil
}

// Save writes walk-state.
func (s *WalkState) Save() error {
	p, err := statePath(s.SessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return ioutil_WriteFile(p, b, 0o644)
}
```

- [ ] **Step 2: Define the trace types + the Walker — walk.go**

Create `internal/graph/walk.go`. The walker is a pure router/bookkeeper (§10.4): it emits directives + trace events, never executes agents.

```go
package graph

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// TraceWriter appends a JSONL event. The CLI wires this to the trace file.
type TraceWriter interface {
	Append(event map[string]interface{}) error
}

// HandoffEnvelope is the ④ product (§2.2).
type HandoffEnvelope struct {
	EnvelopeID     string          `json:"envelope_id"`
	FromNode       string          `json:"from_node"`
	ToNode         string          `json:"to_node"`
	Label          string          `json:"label"`
	Artifacts      []string        `json:"artifacts"`
	ExitCriteria   []ExitCriterion `json:"exit_criteria"`
	FactualClaim   string          `json:"factual_claim"`
	AttemptHistory []AttemptEntry  `json:"attempt_history"`
	BudgetRemaining BudgetRemaining `json:"budget_remaining"`
	Stripped       []string        `json:"stripped"`
}

type AttemptEntry struct {
	Node     string `json:"node"`
	Attempts int    `json:"attempts"`
	Verifier string `json:"verifier"`
}

type BudgetRemaining struct {
	GraphTurnsLeft int `json:"graph_turns_left"`
	TokenBudgetLeft int `json:"token_budget_left"`
}

// ReviewVerdict is the ⑤ result (§2.2.1).
type ReviewVerdict struct {
	EnvelopeID         string   `json:"envelope_id"`
	Node               string   `json:"node"`
	Reviewer           string   `json:"reviewer"`
	Verdict            string   `json:"verdict"` // PASS | NEEDS-FIX
	Findings           []Finding `json:"findings,omitempty"`
	EvidenceToolCalls  []string `json:"evidence_tool_calls"`
}

type Finding struct {
	File  string `json:"file"`
	Line  int    `json:"line,omitempty"`
	Issue string `json:"issue"`
	Fix   string `json:"fix"`
}

// Walker is the graph router + bookkeeper.
type Walker struct {
	g     *Graph
	state *WalkState
	trace TraceWriter
}

func NewWalker(g *Graph, sessionID string, trace TraceWriter) *Walker {
	s, _ := LoadState(sessionID)
	if s.CurrentNode == "" {
		s.CurrentNode = g.firstNode()
		s.Attempt = 1
	}
	return &Walker{g: g, state: s, trace: trace}
}

// Next returns the directive for the current state (bootstrap → first node).
func (w *Walker) Next() Directive {
	if w.state.GraphTurns >= w.g.Budget.MaxGraphTurns {
		return Directive{Kind: Halt}
	}
	node, ok := w.g.NodeByID(w.state.CurrentNode)
	if !ok {
		return Directive{Kind: Halt}
	}
	// budget check
	turnsLeft := w.g.Budget.MaxGraphTurns - w.state.GraphTurns
	tokensLeft := w.g.Budget.TotalTokenBudget - w.state.TokensUsed
	if turnsLeft <= 0 || tokensLeft <= 0 {
		return Directive{Kind: Halt}
	}
	return Directive{
		Kind:     RunNode,
		Node:     node.ID,
		Label:    node.Label,
		Attempt:  w.state.Attempt,
		Snapshot: node.Mutating,
		CleanCtx: node.CleanContext,
	}
}

// RecordHandoff records a ④ handoff, emits the handoff trace event + a node_turn,
// and returns RUN_REVIEW for that envelope (B4: walker schedules ⑤).
func (w *Walker) RecordHandoff(env HandoffEnvelope) (Directive, error) {
	// emit handoff event
	w.trace.Append(toMap(env))
	w.trace.Append(map[string]interface{}{
		"type": "node_turn", "node": env.ToNode, "label": env.Label,
		"step": "review-scheduled",
		"walker_directive": "RUN_REVIEW envelope_id=" + env.EnvelopeID,
	})
	w.state.PendingReview = env.EnvelopeID
	// decrement budget (§2.2: decrements across all nodes; bootstrap h-000 already pre-decremented)
	if env.EnvelopeID != "h-000" {
		w.state.GraphTurns++
	}
	w.state.Save()
	return Directive{Kind: RunReview, EnvelopeID: env.EnvelopeID, Node: env.ToNode, Label: env.Label}, nil
}

// RecordReviewVerdict records a ⑤ verdict and routes the next directive.
func (w *Walker) RecordReviewVerdict(v ReviewVerdict) (Directive, error) {
	w.trace.Append(toMap(v))
	if v.Verdict == "PASS" {
		// advance to next forward node
		next := w.g.forwardFrom(v.Node)
		w.state.CurrentNode = next
		w.state.Attempt = 1
		w.state.PendingReview = ""
		w.state.Save()
		if next == "" {
			return Directive{Kind: Checkpoint, Node: v.Node}, nil // terminal
		}
		return Directive{Kind: Checkpoint, Node: v.Node, Label: w.labelOf(v.Node)}, nil
	}
	// NEEDS-FIX: same-node (node_review_retry) vs cross-node (handoff_reject)
	findingText := findingKey(v.Findings)
	backTarget := w.g.backEdgeTarget(v.Node)
	if backTarget == v.Node || backTarget == "" {
		// same-node retry (node_review_retry) — bounded by max_inner_turns
		node, _ := w.g.NodeByID(v.Node)
		if w.state.Attempt >= node.MaxInnerTurns {
			// exhausted → escalate to checkpoint
			return Directive{Kind: Checkpoint, Node: v.Node}, nil
		}
		w.trace.Append(map[string]interface{}{
			"type": "node_review_retry", "envelope_id": v.EnvelopeID,
			"from_node": v.Node, "reason": "review_needs_fix",
			"findings": findingStrings(v.Findings), "executor": "walker",
		})
		w.state.Attempt++
		w.state.Save()
		return Directive{Kind: NodeRetry, Node: v.Node, Attempt: w.state.Attempt,
			Restore: node.Mutating, Label: node.Label}, nil
	}
	// cross-node back-edge (handoff_reject) — increments global counter + 3× escalation
	w.state.BackEdges++
	w.state.FindingsSeen[findingText]++
	// 3× same-finding escalation (§2.7 #2)
	if w.state.FindingsSeen[findingText] >= 3 {
		return Directive{Kind: Checkpoint, Node: v.Node}, nil
	}
	if w.state.BackEdges >= w.g.Budget.MaxBackEdgesTotal {
		return Directive{Kind: Checkpoint, Node: v.Node}, nil
	}
	skip := w.g.backEdgeSkip(v.Node)
	w.trace.Append(map[string]interface{}{
		"type": "handoff_reject", "envelope_id": v.EnvelopeID,
		"from_node": v.Node, "reason": "review_needs_fix",
		"findings": findingStrings(v.Findings), "executor": "walker",
	})
	w.state.CurrentNode = backTarget
	w.state.Attempt = 1
	w.state.Save()
	return Directive{Kind: BackEdge, Node: backTarget, Skip: skip, Label: w.labelOf(backTarget)}, nil
}

// --- Graph helpers ---

func (g *Graph) forwardFrom(nodeID string) string {
	for _, e := range g.Edges {
		if e.From == nodeID {
			return e.To
		}
	}
	return ""
}
func (g *Graph) backEdgeTarget(nodeID string) string {
	for _, be := range g.BackEdges {
		if be.From == nodeID {
			return be.To
		}
	}
	return ""
}
func (g *Graph) backEdgeSkip(nodeID string) []string {
	for _, be := range g.BackEdges {
		if be.From == nodeID {
			return be.Skip
		}
	}
	return nil
}
func (w *Walker) labelOf(id string) string {
	if n, ok := w.g.NodeByID(id); ok {
		return n.Label
	}
	return id
}

func findingKey(fs []Finding) string {
	if len(fs) == 0 {
		return ""
	}
	return fs[0].File + ":" + strconv.Itoa(fs[0].Line)
}
func findingStrings(fs []Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = fmt.Sprintf("%s:%d %s", f.File, f.Line, f.Issue)
	}
	return out
}

func toMap(v interface{}) map[string]interface{} {
	b, _ := json.Marshal(v)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	m["type"] = typeOf(v)
	return m
}
func typeOf(v interface{}) string {
	switch v.(type) {
	case HandoffEnvelope:
		return "handoff"
	case ReviewVerdict:
		return "review_verdict"
	default:
		return "event"
	}
}
```

- [ ] **Step 3: Add the ioutil wrappers (go 1.16 idiom)**

Append to `internal/graph/walkstate.go`:

```go
// ioutil_ReadFile / ioutil_WriteFile wrap io/ioutil to match the existing
// internal/context idiom (go 1.16).
func ioutil_ReadFile(p string) ([]byte, error) {
	return readAllPath(p)
}
func ioutil_WriteFile(p string, b []byte, perm os.FileMode) error {
	return writeAllPath(p, b, perm)
}
```

And create `internal/graph/io.go`:

```go
package graph

import (
	"io/ioutil"
	"os"
)

func readAllPath(p string) ([]byte, error)        { return ioutil.ReadFile(p) }
func writeAllPath(p string, b []byte, perm os.FileMode) error {
	return ioutil.WriteFile(p, b, perm)
}
```

- [ ] **Step 4: Write the failing test against the C4-new fixture**

Create `internal/graph/walk_test.go`. This test replays the C4-new fixture's envelope sequence and asserts the walker emits the right directives (RUN_NODE → RUN_REVIEW → NodeRetry on the same-node NEEDS-FIX → eventually CHECKPOINT).

```go
package graph

import (
	"strings"
	"testing"
)

// memTrace collects emitted events for assertions.
type memTrace struct{ events []map[string]interface{} }

func (m *memTrace) Append(e map[string]interface{}) error { m.events = append(m.events, e); return nil }

func TestWalker_C4New_Replay(t *testing.T) {
	g, err := ParseGraph([]byte(c4GraphYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tr := &memTrace{}
	w := NewWalker(g, "test-c4", tr)

	// bootstrap h-000 → diagnose
	d := w.Next()
	if d.Kind != RunNode || d.Node != "diagnose" {
		t.Fatalf("expected RUN_NODE diagnose, got %+v", d)
	}
	// diagnose ④ handoff h-001
	r, err := w.RecordHandoff(h001())
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != RunReview || r.EnvelopeID != "h-001" {
		t.Fatalf("expected RUN_REVIEW h-001, got %+v", r)
	}
	// ⑤ PASS on h-001
	r, _ = w.RecordReviewVerdict(rv("h-001", "diagnose", "PASS", nil))
	if r.Kind != Checkpoint {
		t.Fatalf("expected CHECKPOINT after PASS, got %+v", r)
	}
	// fix ④ handoff h-002 (the defective one)
	w.RecordHandoff(h002())
	// ⑤ NEEDS-FIX same-node (fix→fix) → NodeRetry, NOT BackEdge
	r, _ = w.RecordReviewVerdict(rv("h-002", "fix", "NEEDS-FIX",
		[]Finding{{File: "task.go", Line: 187, Issue: "2nd deref unchecked"}}))
	if r.Kind != NodeRetry {
		t.Fatalf("expected NodeRetry for same-node ⑤, got %+v", r)
	}
	if r.Restore != true {
		t.Fatalf("expected Restore=true for mutating fix node, got %+v", r)
	}
	// assert the trace got a node_review_retry (NOT handoff_reject) for h-002
	var gotRetry bool
	for _, e := range tr.events {
		if e["type"] == "node_review_retry" && e["envelope_id"] == "h-002" {
			gotRetry = true
		}
	}
	if !gotRetry {
		t.Fatal("expected node_review_retry event for h-002 (same-node ⑤), none emitted")
	}
}

const c4GraphYAML = `
budget:
  max_graph_turns: 20
  total_token_budget: 50000
  max_back_edges_total: 8
  alternating_finding_window: 4
nodes:
  - id: diagnose
    label: "定位根因"
    skill: phase.debug
    exit_criteria:
      - {name: root_cause, verdict_type: SOFT, pin: "file:line"}
    max_inner_turns: 3
    mutating: false
  - id: fix
    label: "补 nil 判空"
    skill: antia-logic
    exit_criteria:
      - {name: diff_applied, verdict_type: DECIDABLE}
      - {name: all_deref_sites_covered, verdict_type: SOFT, grep_pattern: "player.SubTask"}
    max_inner_turns: 4
    mutating: true
  - id: verify
    label: "跑测试套件"
    skill: phase.verify
    exit_criteria:
      - {name: tests_pass, verdict_type: DECIDABLE}
    max_inner_turns: 1
    mutating: false
edges:
  - {from: diagnose, to: fix}
  - {from: fix, to: verify}
back_edges:
  - {from: fix, to: fix, reason: review_needs_fix}
  - {from: verify, to: fix, reason: verifier_failed}
`

func h001() HandoffEnvelope {
	return HandoffEnvelope{
		EnvelopeID: "h-001", FromNode: "diagnose", ToNode: "fix", Label: "定位根因",
		Artifacts: []string{"docs/reviews/task-npe.md", "player/task/task.go:142"},
		ExitCriteria: []ExitCriterion{{Name: "root_cause", VerdictType: Soft, Pin: "file:line"}},
		FactualClaim:  "root cause = nil deref",
		AttemptHistory: []AttemptEntry{{Node: "diagnose", Attempts: 1, Verifier: "root_cause_pin"}},
		BudgetRemaining: BudgetRemaining{18, 42000},
		Stripped: []string{"producer_reasoning_trace", "producer_verdict_self_report"},
	}
}
func h002() HandoffEnvelope {
	return HandoffEnvelope{
		EnvelopeID: "h-002", FromNode: "fix", ToNode: "verify", Label: "补 nil 判空",
		Artifacts: []string{"player/task/task.go"},
		ExitCriteria: []ExitCriterion{
			{Name: "diff_applied", VerdictType: Decidable},
			{Name: "all_deref_sites_covered", VerdictType: Soft, GrepPattern: "player.SubTask"},
		},
		FactualClaim: "nil-guard added at 142",
		AttemptHistory: []AttemptEntry{
			{Node: "diagnose", Attempts: 1, Verifier: "root_cause_pin"},
			{Node: "fix", Attempts: 1, Verifier: "build_clean"},
		},
		BudgetRemaining: BudgetRemaining{15, 38000},
		Stripped: []string{"producer_reasoning_trace", "producer_verdict_self_report"},
	}
}
func rv(env, node, verdict string, fs []Finding) ReviewVerdict {
	return ReviewVerdict{
		EnvelopeID: env, Node: node, Reviewer: "review-agent:" + node,
		Verdict: verdict, Findings: fs,
		EvidenceToolCalls: []string{"grep -n player.SubTask player/task/task.go"},
	}
}

// suppress unused-import warning for strings if not otherwise used
var _ = strings.TrimSpace
```

- [ ] **Step 5: Run the test to verify it fails, then confirm it passes after implementation**

Run: `go test ./internal/graph/ -run TestWalker -v`
Expected: PASS (the implementation in Steps 1-3 already satisfies it; if a field name mismatches, fix the impl to match the test — the test is the spec).

- [ ] **Step 6: Add a budget-exhaustion test (H6)**

Append to `walk_test.go`:

```go
func TestWalker_HaltsOnBudgetExhaustion(t *testing.T) {
	g, _ := ParseGraph([]byte(`
budget:
  max_graph_turns: 2
  total_token_budget: 1000
  max_back_edges_total: 8
nodes:
  - id: diagnose
    label: "定位根因"
    skill: phase.debug
    exit_criteria:
      - {name: root_cause, verdict_type: SOFT}
    max_inner_turns: 3
edges: []
`))
	tr := &memTrace{}
	w := NewWalker(g, "test-budget", tr)
	w.RecordHandoff(HandoffEnvelope{EnvelopeID: "h-001", FromNode: "diagnose", ToNode: "diagnose",
		Label: "定位根因", Artifacts: []string{"a"}, ExitCriteria: []ExitCriterion{{Name: "x", VerdictType: Soft}},
		FactualClaim: "c", Stripped: []string{"producer_reasoning_trace"}})
	w.RecordHandoff(HandoffEnvelope{EnvelopeID: "h-002", FromNode: "diagnose", ToNode: "diagnose",
		Label: "定位根因", Artifacts: []string{"a"}, ExitCriteria: []ExitCriterion{{Name: "x", VerdictType: Soft}},
		FactualClaim: "c", Stripped: []string{"producer_reasoning_trace"}})
	d := w.Next()
	if d.Kind != Halt {
		t.Fatalf("expected Halt after graph_turns exhausted, got %+v", d)
	}
}
```

Run: `go test ./internal/graph/ -run TestWalker_HaltsOnBudgetExhaustion -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/graph/walkstate.go internal/graph/walk.go internal/graph/io.go internal/graph/walk_test.go
git commit -m "$(cat <<'EOF'
feat(graph): walk-state machine + budget enforcement

internal/graph.Walker routes RUN_NODE/RUN_REVIEW, emits handoff/
review_verdict/node_turn/node_review_retry/handoff_reject events,
enforces max_graph_turns (H6) + max_inner_turns (same-node ⑤) +
3× same-finding cross-node escalation. Same-node ⑤ NEEDS-FIX emits
node_review_retry (does NOT increment global back-edge counter);
cross-node emits handoff_reject (does). Pure router+bookkeeper — does
not execute agents, does not touch the working tree.

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: `internal/graph` — render `explain` + `status` (M9)

The human-facing checkpoint view. Pure formatting over `WalkState`.

**Files:**
- Create: `internal/graph/render.go`
- Create: `internal/graph/render_test.go`

**Interfaces:**
- Consumes: `*Walker` (Task 3).
- Produces: `Walker.Explain() string` (human-facing map+narrative, M9) and `Walker.Status() string` (machine debug). The CLI (Task 5) exposes these as `summoner-walker explain` / `status`.

- [ ] **Step 1: Write the failing test for Explain**

Create `internal/graph/render_test.go`:

```go
package graph

import (
	"strings"
	"testing"
)

func TestExplain_ShowsLabelNotId(t *testing.T) {
	g, _ := ParseGraph([]byte(c4GraphYAML))
	tr := &memTrace{}
	w := NewWalker(g, "test-render", tr)
	out := w.Explain()
	if !strings.Contains(out, "定位根因") {
		t.Fatalf("explain should show label '定位根因', got: %s", out)
	}
	if strings.Contains(out, "RUN_NODE") || strings.Contains(out, "①") {
		t.Fatalf("explain must hide machine-internal step names, got: %s", out)
	}
}

func TestStatus_ShowsMachineState(t *testing.T) {
	g, _ := ParseGraph([]byte(c4GraphYAML))
	tr := &memTrace{}
	w := NewWalker(g, "test-render", tr)
	out := w.Status()
	if !strings.Contains(out, "node=") || !strings.Contains(out, "graph_turns=") {
		t.Fatalf("status should show raw machine state, got: %s", out)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/graph/ -run TestExplain -v`
Expected: FAIL — `w.Explain` undefined.

- [ ] **Step 3: Implement render.go**

Create `internal/graph/render.go`:

```go
package graph

import (
	"fmt"
	"strings"
)

// Explain renders the human-facing checkpoint view (M9): node label (not id),
// hidden ①②③④⑤, "you are here" route map, budget, precomputed default recall.
func (w *Walker) Explain() string {
	node, _ := w.g.NodeByID(w.state.CurrentNode)
	label := w.state.CurrentNode
	if node != nil {
		label = node.Label
	}
	var route []string
	for _, re := range w.state.RouteMap {
		mark := "?"
		switch re.Status {
		case "pass":
			mark = "✓"
		case "needs_fix":
			mark = "✗"
		case "current":
			mark = "▶"
		case "skipped":
			mark = "⊘"
		}
		route = append(route, mark+re.Label)
	}
	if len(route) == 0 {
		route = []string{"▶" + label}
	}
	turnsLeft := w.g.Budget.MaxGraphTurns - w.state.GraphTurns
	tokensLeft := w.g.Budget.TotalTokenBudget - w.state.TokensUsed
	return fmt.Sprintf("📍 %s (第 %d 次尝试) · 路线 %s · 预算 %d/%d · %dk/%dk · %d/%d\n默认: [recall to %s]",
		label, w.state.Attempt, strings.Join(route, "→"),
		turnsLeft, w.g.Budget.MaxGraphTurns, tokensLeft/1000, w.g.Budget.TotalTokenBudget/1000,
		w.state.BackEdges, w.g.Budget.MaxBackEdgesTotal, label)
}

// Status renders raw machine state for debugging + scorers (§2.9).
func (w *Walker) Status() string {
	return fmt.Sprintf("node=%s attempt=%d/%d graph_turns=%d/%d budget=%d/%d back_edges=%d/%d",
		w.state.CurrentNode, w.state.Attempt, maxInner(w.g, w.state.CurrentNode),
		w.state.GraphTurns, w.g.Budget.MaxGraphTurns,
		w.g.Budget.TotalTokenBudget-w.state.TokensUsed, w.g.Budget.TotalTokenBudget,
		w.state.BackEdges, w.g.Budget.MaxBackEdgesTotal)
}

func maxInner(g *Graph, id string) int {
	if n, ok := g.NodeByID(id); ok {
		return n.MaxInnerTurns
	}
	return 0
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/graph/ -run TestExplain -v && go test ./internal/graph/ -run TestStatus -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/graph/render.go internal/graph/render_test.go
git commit -m "$(cat <<'EOF'
feat(graph): walker explain (M9 human-facing) + status (debug)

Explain renders node label (not id), hides ①②③④⑤, shows route map +
budget + precomputed default recall. Status shows raw machine state.
SKILL.md renders explain into checkpoints; status stays for debug/scorers.

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: `cmd/summoner-walker` — the CLI binary

Thin cobra CLI over `internal/graph` (matches `cmd/summoner-ctx/main.go` idiom). Subcommands: `next`, `record`, `explain`, `status`.

**Files:**
- Create: `cmd/summoner-walker/main.go`
- Create: `cmd/summoner-walker/main_test.go`

**Interfaces:**
- Consumes: `internal/graph` (Tasks 2-4).
- Produces: a binary `summoner-walker` with `next`/`record --step handoff|review_verdict`/`explain`/`status`. SKILL.md (Task 9) and the scorers (Task 7) call this.

- [ ] **Step 1: Write the CLI — main.go**

Create `cmd/summoner-walker/main.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"

	"github.com/johnson-xue/summoner/internal/graph"
	"github.com/spf13/cobra"
)

var (
	graphFile string
	traceFile string
	sessionID string
)

func main() {
	root := &cobra.Command{Use: "summoner-walker", Short: "Summoner graph walker (router + bookkeeper)"}
	root.PersistentFlags().StringVar(&graphFile, "graph", "", "plan.md path or raw graph YAML path")
	root.PersistentFlags().StringVar(&traceFile, "trace", "", "trace.jsonl path (append-only)")
	root.PersistentFlags().StringVar(&sessionID, "session", "default", "session id (walk-state key)")

	root.AddCommand(nextCmd())
	root.AddCommand(recordCmd())
	root.AddCommand(explainCmd())
	root.AddCommand(statusCmd())
	_ = root.Execute()
}

func loadGraph() (*graph.Graph, error) {
	b, err := ioutil.ReadFile(graphFile)
	if err != nil {
		return nil, err
	}
	// if the file is a plan.md, extract the fenced summoner-task-graph block (M4)
	yamlBytes := extractGraphBlock(b)
	if yamlBytes == nil {
		yamlBytes = b
	}
	return graph.ParseGraph(yamlBytes)
}

// extractGraphBlock pulls the ```yaml summoner-task-graph fence from a markdown plan.
func extractGraphBlock(md []byte) []byte {
	re := regexp.MustCompile("(?s)```yaml\\s+summoner-task-graph\\s*\n(.*?)```")
	m := re.FindSubmatch(md)
	if len(m) < 2 {
		return nil
	}
	return m[1]
}

type fileTrace struct{ path string }

func (f *fileTrace) Append(e map[string]interface{}) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	fout, err := os.OpenFile(f.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer fout.Close()
	_, err = fout.Write(append(b, '\n'))
	return err
}

func newWalker() (*graph.Walker, error) {
	g, err := loadGraph()
	if err != nil {
		return nil, err
	}
	var tr graph.TraceWriter
	if traceFile != "" {
		tr = &fileTrace{path: traceFile}
	} else {
		tr = &nullTrace{}
	}
	return graph.NewWalker(g, sessionID, tr), nil
}

type nullTrace struct{}

func (n *nullTrace) Append(e map[string]interface{}) error { return nil }

func nextCmd() *cobra.Command {
	return &cobra.Command{
		Use: "next", Short: "print the next directive",
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := newWalker()
			if err != nil {
				return err
			}
			d := w.Next()
			out, _ := json.Marshal(d)
			fmt.Println(string(out))
			return nil
		},
	}
}

func recordCmd() *cobra.Command {
	var step, envelopePath, envelopeID, verdict, findingsPath string
	c := &cobra.Command{
		Use: "record", Short: "record a handoff or review_verdict",
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := newWalker()
			if err != nil {
				return err
			}
			switch step {
			case "handoff":
				b, err := ioutil.ReadFile(envelopePath)
				if err != nil {
					return err
				}
				var env graph.HandoffEnvelope
				if err := json.Unmarshal(b, &env); err != nil {
					return err
				}
				d, err := w.RecordHandoff(env)
				if err != nil {
					return err
				}
				out, _ := json.Marshal(d)
				fmt.Println(string(out))
			case "review_verdict":
				var fs []graph.Finding
				if findingsPath != "" {
					b, _ := ioutil.ReadFile(findingsPath)
					json.Unmarshal(b, &fs)
				}
				v := graph.ReviewVerdict{
					EnvelopeID:        envelopeID,
					Node:              "",
					Reviewer:          "review-agent",
					Verdict:           verdict,
					Findings:          fs,
					EvidenceToolCalls: []string{"(recorded by SKILL.md)"},
				}
				d, err := w.RecordReviewVerdict(v)
				if err != nil {
					return err
				}
				out, _ := json.Marshal(d)
				fmt.Println(string(out))
			default:
				return fmt.Errorf("unknown step %q (want handoff|review_verdict)", step)
			}
			return nil
		},
	}
	c.Flags().StringVar(&step, "step", "", "handoff | review_verdict")
	c.Flags().StringVar(&envelopePath, "envelope", "", "path to envelope json (handoff)")
	c.Flags().StringVar(&envelopeID, "envelope_id", "", "envelope id (review_verdict)")
	c.Flags().StringVar(&verdict, "verdict", "", "PASS | NEEDS-FIX (review_verdict)")
	c.Flags().StringVar(&findingsPath, "findings", "", "path to findings json (review_verdict)")
	return c
}

func explainCmd() *cobra.Command {
	return &cobra.Command{
		Use: "explain", Short: "human-facing checkpoint render (M9)",
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := newWalker()
			if err != nil {
				return err
			}
			fmt.Println(w.Explain())
			return nil
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "raw machine state (debug/scorers)",
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := newWalker()
			if err != nil {
				return err
			}
			fmt.Println(w.Status())
			return nil
		},
	}
}
```

- [ ] **Step 2: Write a CLI integration test — main_test.go**

Create `cmd/summoner-walker/main_test.go`:

```go
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractGraphBlock_FencedYAML(t *testing.T) {
	md := []byte("# Plan\n\n```yaml summoner-task-graph\nnodes:\n  - id: fix\n    label: \"补\"\n```\nrest")
	got := extractGraphBlock(md)
	if !strings.Contains(string(got), "nodes:") {
		t.Fatalf("expected to extract the yaml block, got %q", got)
	}
	if strings.Contains(string(got), "rest") {
		t.Fatalf("extracted block should not include trailing prose, got %q", got)
	}
}

func TestCLI_Next_PrintsDirective(t *testing.T) {
	// build the binary into a temp dir
	bin := filepath.Join(t.TempDir(), "summoner-walker")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}
	graphPath := filepath.Join(t.TempDir(), "g.yaml")
	os.WriteFile(graphPath, []byte(`
budget: {max_graph_turns: 20, total_token_budget: 50000, max_back_edges_total: 8}
nodes:
  - id: diagnose
    label: "定位根因"
    skill: phase.debug
    exit_criteria: [{name: root_cause, verdict_type: SOFT}]
    max_inner_turns: 3
edges: []
`), 0o644)
	out, err := exec.Command(bin, "--graph", graphPath, "--session", "t1", "next").Output()
	if err != nil {
		t.Fatalf("next: %v %s", err, out)
	}
	if !strings.Contains(string(out), "RUN_NODE") || !strings.Contains(string(out), "diagnose") {
		t.Fatalf("expected RUN_NODE diagnose, got %s", out)
	}
}
```

- [ ] **Step 3: Build + run the tests**

Run: `go build ./cmd/summoner-walker/ && go test ./cmd/summoner-walker/ -v`
Expected: PASS (both subtests).

- [ ] **Step 4: Smoke-test the binary by hand against the spec's §10.1 example**

Run: `go run ./cmd/summoner-walker --graph <(printf 'budget: {max_graph_turns:20,total_token_budget:50000,max_back_edges_total:8}\nnodes:\n  - id: fix\n    label: "补 nil 判空"\n    skill: antia-logic\n    exit_criteria: [{name: diff_applied, verdict_type: DECIDABLE}]\n    max_inner_turns: 4\n    mutating: true\nedges: []\n') --session smoke next`
Expected: JSON with `"kind":"RUN_NODE","node":"fix","label":"补 nil 判空","snapshot":true`.

- [ ] **Step 5: Commit**

```bash
git add cmd/summoner-walker/main.go cmd/summoner-walker/main_test.go
git commit -m "$(cat <<'EOF'
feat(walker): cmd/summoner-walker CLI (next/record/explain/status)

Thin cobra CLI over internal/graph, matching cmd/summoner-ctx idiom.
Extracts the fenced ```yaml summoner-task-graph block from a plan (M4);
falls back to raw YAML. next prints the directive; record --step
handoff|review_verdict appends events + returns the routed directive;
explain renders the M9 checkpoint; status prints machine state.

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: `scripts/node-snapshot.sh` + `agents/review-agent.md` + `references/node-contract.md`

The ⓪ helper (owner SKILL.md), the ⑤ persona, and the normative contract doc.

**Files:**
- Create: `scripts/node-snapshot.sh`
- Create: `agents/review-agent.md`
- Create: `references/node-contract.md`

**Interfaces:**
- `node-snapshot.sh save [paths...]` / `node-snapshot.sh restore` — invoked by SKILL.md when the walker directive carries `snapshot`/`restore` flags (Task 9 wires this).
- `review-agent.md` is the persona SKILL.md spawns on `RUN_REVIEW` (Task 9).
- `node-contract.md` is the reference the scorers + SKILL.md cite.

- [ ] **Step 1: Write node-snapshot.sh**

Create `scripts/node-snapshot.sh`:

```bash
#!/bin/bash
# node-snapshot.sh — ⓪ working-tree snapshot/restore for idempotent retry (H2).
# Owner = SKILL.md. The walker signals snapshot/restore flags via the RUN_NODE
# directive (§10.1, M2); SKILL.md runs THIS helper. The walker never touches the tree.
#
# Usage:
#   node-snapshot.sh save [paths...]   # git stash --include-untracked (-u)
#   node-snapshot.sh restore            # git stash pop
#
# -u covers new files the reproduce/fix nodes write (M2 fix).

set -o pipefail

ACTION="${1:-}"

if [[ "$ACTION" == "save" ]]; then
  shift
  # stash everything including untracked; if paths given, the caller has already
  # scoped the working set — we still stash -u so NEW files are captured.
  if ! git stash push --include-untracked -m "summoner-node-snapshot-$(date +%s)" "$@"; then
    echo "Error: git stash save failed" >&2
    exit 1
  fi
  echo "OK: snapshot saved (git stash --include-untracked)"
  exit 0
fi

if [[ "$ACTION" == "restore" ]]; then
  if ! git stash pop; then
    echo "Error: git stash pop failed (no snapshot to restore?)" >&2
    exit 1
  fi
  echo "OK: snapshot restored (git stash pop)"
  exit 0
fi

echo "Usage: node-snapshot.sh save [paths...] | restore"
exit 2
```

- [ ] **Step 2: Make it executable + write a smoke test**

Run: `chmod +x scripts/node-snapshot.sh`
Then verify the usage path:
Run: `bash scripts/node-snapshot.sh`
Expected: `Usage: node-snapshot.sh save [paths...] | restore` (exit 2).

- [ ] **Step 3: Write a real save/restore test in a temp git repo**

Run this one-off (not committed as a file — it's a verification command):

```bash
TMP=$(mktemp -d) && cd "$TMP" && git init -q && \
  echo "before" > a.txt && git add a.txt && git commit -q -m init && \
  echo "dirty" > a.txt && echo "new" > b.txt && \
  bash /Users/admin/summoner/scripts/node-snapshot.sh save && \
  [[ "$(cat a.txt)" == "before" && ! -f b.txt ]] && echo "save OK" && \
  bash /Users/admin/summoner/scripts/node-snapshot.sh restore && \
  [[ "$(cat a.txt)" == "dirty" && -f b.txt ]] && echo "restore OK" && \
  cd - >/dev/null && rm -rf "$TMP"
```
Expected: `save OK` then `restore OK` (proves `-u` captured the new `b.txt` and restore brought both back).

- [ ] **Step 4: Write agents/review-agent.md**

Create `agents/review-agent.md`:

```markdown
---
name: review-agent
description: Generic per-node ⑤ Review-agent — independently re-derives findings against artifact paths and returns a review_verdict with evidence_tool_calls. Scheduled by the walker via RUN_REVIEW; never reads producer context; never calls other agents.
---

# Review-Agent (⑤)

You are the independent-context reviewer for ONE node boundary. You were spawned by Summoner's walker (`RUN_REVIEW`) — NOT by the node you are reviewing. The node cannot spawn its own reviewer (invariant #4).

## What you receive

A handoff **envelope of artifact paths** + the node's `exit_criteria` (each tagged `DECIDABLE` or `SOFT`) + a one-line `factual_claim`. You do NOT receive:
- producer reasoning (`producer_reasoning_trace` is in `stripped`, not shipped),
- the producer's self-reported pass verdict (`producer_verdict_self_report` is in `stripped`),
- `passed` in `attempt_history` (it carries `{node, attempts, verifier}` only — B2).

## What you do

Independently re-derive whether the node's `exit_criteria` are met by **running your own tools against the artifact paths**:

1. For each `exit_criteria` entry tagged **DECIDABLE**: run the mechanical check (test suite / lint / typecheck / build / grep) and record the exit code or hit count.
2. For each entry tagged **SOFT**: you MUST run that criterion's `grep_pattern` (the structural anchor) across the relevant files and log the command + hits. A SOFT criterion can never alone yield PASS — but you must still execute its anchor and report what you found.
3. `Read`/`grep`/`Bash` the artifact paths directly. Do not trust `factual_claim` — verify it against the artifact.

## What you return

A `review_verdict`:
- `verdict`: `PASS` or `NEEDS-FIX`.
- `findings` (on NEEDS-FIX): each `{file, line, issue, fix}`.
- `evidence_tool_calls`: the **non-empty** list of your OWN Read/grep/Bash invocations. A verdict with empty `evidence_tool_calls` is a rubber-stamp and will be failed by `review-isolation-check` (invariant #6).

## Iron rules

- You judge **quality against the declared exit_criteria only** — not free-form taste. If a criterion is undecidable and unpinned, say so; do not invent a verdict.
- You do NOT choose the next node. The walker routes on your verdict; you return verdict + evidence only.
- You do NOT call other agents (extends `persona-composition.md`).
```

- [ ] **Step 5: Write references/node-contract.md**

Create `references/node-contract.md`:

```markdown
# The Node Contract

A Summoner node is a closed-loop agent running **① Ingest+Validate → ⓪ Pre-Work snapshot → ② Work → ③ Test → ④ Handoff**, after which the **walker** schedules a separate-context **⑤ Review-agent** (`RUN_REVIEW`). ⑤ is NOT a 5th inline step of the node — it is walker-scheduled (§2.8/B4).

## Steps

| Step | Does | Decidable? |
|---|---|---|
| ① Ingest+Validate | Receive the upstream handoff envelope; check declared artifacts/fields/exit-criteria present + well-formed. Reject → cross-node `handoff_reject` back-edge. | Yes (schema) |
| ⓪ Pre-Work snapshot | (mutating nodes only) snapshot working tree so ③-retry or ⑤-back-edge re-runs ② on a clean tree. Owner = SKILL.md via `node-snapshot.sh`; walker signals `snapshot:`/`restore:` flags, never touches the tree (M2). | n/a |
| ② Work | Execute the closed-loop task. | n/a |
| ③ Test | Run a machine-decidable node-internal verifier. FAIL → restore ⓪ then retry ② (bounded by `max_inner_turns`). | Yes |
| ④ Handoff | Emit a clean, minimal, typed envelope: artifact paths + `exit_criteria` (each `{name, verdict_type, pin?, grep_pattern?}`) + one-line `factual_claim` + `attempt_history` (`{node, attempts, verifier}` only, NO `passed`) + `budget_remaining` + `stripped` (incl. `producer_reasoning_trace` + `producer_verdict_self_report`). | Yes (schema) |
| ⑤ Review-agent | Separate-context; independently re-derives findings with own Read/grep/Bash against artifact paths; returns `review_verdict` (standalone event keyed by `envelope_id`, non-empty `evidence_tool_calls`). Walker-scheduled, not node-spawned. | Decidable where re-derivation yields objective signals; pinned otherwise |

## Back-edge semantics (C2)

- **Same-node ⑤ NEEDS-FIX** (e.g. ⑤ on `fix` → `fix`): walker emits `node_review_retry`, bounded by `max_inner_turns`, no checkpoint, does NOT increment the global `max_back_edges_total`.
- **Cross-node ⑤ NEEDS-FIX / ① reject**: walker emits `handoff_reject`, increments global counter; 3× same-finding escalates to checkpoint.

## Budget (H6)

Precedence: `max_inner_turns` → 3× same-finding → `max_back_edges_total` → `max_graph_turns`. Same-node ⑤ does not increment the global counter; cross-node does.

## Invariants

1. Phase 1 (diagnose) is iron law — `conditional_edges`/back-edges may not bypass it.
2. No auto-advance past a checkpoint — the human retains the *flow* decision; ⑤ judges *quality* only.
3. No hardcoded project/domain names.
4. Agents never call other agents — the walker routes all back-edges.
5. Post-game review is mandatory.
6. Review-agent independence is enforced by tool-use (non-empty `evidence_tool_calls`, `stripped` includes producer reasoning) — not by a prompt promise.
```

- [ ] **Step 6: Commit**

```bash
git add scripts/node-snapshot.sh agents/review-agent.md references/node-contract.md
git commit -m "$(cat <<'EOF'
feat(node-contract): node-snapshot.sh (⓪), review-agent (⑤), node-contract.md

node-snapshot.sh: git stash --include-untracked save/restore, owner=SKILL.md
(walker signals flags, never touches tree — M2). review-agent.md: the
generic ⑤ persona — independent re-derivation, envelope of paths, non-empty
evidence_tool_calls, never reads producer context, never calls agents.
node-contract.md: the normative contract reference.

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Three P0 deterministic scorers

The enforcement layer. Each is bash + `jq`, exit 0=PASS/1=FAIL/2=SKIP (matching `iron-law-check.sh`). These make the contract mechanical, not aspirational.

**Files:**
- Create: `scorers/deterministic/handoff-contract-check.sh`
- Create: `scorers/deterministic/verifier-checklist-check.sh`
- Create: `scorers/deterministic/review-isolation-check.sh`
- Modify: `scripts/score-trace.sh` (wire the 3 new scorers into P0 if not auto-discovered)
- Modify: `references/scoring-system.md` (register the 3 scorers)

**Interfaces:**
- Consumes: a trace JSONL file path (arg 1) + (for verifier-checklist) the graph YAML.
- Produces: exit 0/1/2 + a one-line verdict. Wired into `scripts/score-trace.sh --priority P0`.

- [ ] **Step 1: Write handoff-contract-check.sh**

Create `scorers/deterministic/handoff-contract-check.sh`:

```bash
#!/bin/bash
# handoff-contract-check.sh — P0 scorer: typed-envelope contract (§2.2 + §2.2.1).
# Usage: handoff-contract-check.sh <trace.jsonl>
# Exit: 0=PASS, 1=FAIL, 2=SKIP (no handoff events = SKIP).

set -o pipefail
TRACE="$1"
if [[ ! -f "$TRACE" ]]; then echo "Error: trace not found"; exit 2; fi

HANDOFFS=$(jq -c 'select(.type=="handoff")' "$TRACE" 2>/dev/null)
if [[ -z "$HANDOFFS" ]]; then echo "SKIP: no handoff events (chain-mode trace)"; exit 2; fi

FAILS=0
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  env_id=$(echo "$line" | jq -r '.envelope_id')
  # bootstrap h-000 exempt from review_verdict correlation (§5)
  if [[ "$env_id" == "h-000" ]]; then continue; fi
  # required fields
  for f in envelope_id from_node to_node label artifacts factual_claim attempt_history budget_remaining stripped; do
    if ! echo "$line" | jq -e --arg f "$f" 'has($f)' >/dev/null 2>&1; then
      echo "FAIL: handoff $env_id missing field $f"; FAILS=$((FAILS+1))
    fi
  done
  # artifacts non-empty
  if ! echo "$line" | jq -e '.artifacts | length > 0' >/dev/null 2>&1; then
    echo "FAIL: handoff $env_id has empty artifacts"; FAILS=$((FAILS+1))
  fi
  # exit_criteria each has verdict_type
  bad=$(echo "$line" | jq -r '.exit_criteria[]? | select((.verdict_type // "") | IN("DECIDABLE","SOFT") | not) | .name')
  if [[ -n "$bad" ]]; then
    echo "FAIL: handoff $env_id criterion '$bad' missing verdict_type (B3)"; FAILS=$((FAILS+1))
  fi
  # reject fields outside the allow-list (producer_reasoning_trace / handoff_note / passed)
  leak=$(echo "$line" | jq -r 'keys[] | select(IN("producer_reasoning_trace","handoff_note","passed"))')
  if [[ -n "$leak" ]]; then
    echo "FAIL: handoff $env_id carries banned field $leak (producer-reasoning leak)"; FAILS=$((FAILS+1))
  fi
  # attempt_history entries must NOT carry 'passed'
  if echo "$line" | jq -e '.attempt_history[]? | has("passed")' >/dev/null 2>&1; then
    echo "FAIL: handoff $env_id attempt_history carries 'passed' (B2)"; FAILS=$((FAILS+1))
  fi
  # correlate to a review_verdict by envelope_id
  if ! jq -e --arg id "$env_id" 'select(.type=="review_verdict" and .envelope_id==$id)' "$TRACE" >/dev/null 2>&1; then
    echo "FAIL: handoff $env_id has no correlated review_verdict (B1/⑤-skip)"; FAILS=$((FAILS+1))
  fi
done <<< "$HANDOFFS"

if [[ $FAILS -gt 0 ]]; then exit 1; fi
echo "PASS: all handoffs conform to §2.2 contract"
exit 0
```

- [ ] **Step 2: Write verifier-checklist-check.sh**

Create `scorers/deterministic/verifier-checklist-check.sh`:

```bash
#!/bin/bash
# verifier-checklist-check.sh — P0 scorer: DECIDABLE/SOFT discipline (§2.4 + §2.5 B3).
# Usage: verifier-checklist-check.sh <trace.jsonl> [graph.yaml]
# Exit: 0=PASS, 1=FAIL, 2=SKIP.
# NOTE: the scorer does NOT semantically classify criteria — it only checks
# verdict_type is PRESENT (declared at plan time). "判不了" is an authoring error.

set -o pipefail
TRACE="$1"; GRAPH="${2:-}"
if [[ ! -f "$TRACE" ]]; then echo "Error: trace not found"; exit 2; fi

# If a graph YAML is given, assert every exit_criterion in it has a verdict_type.
if [[ -n "$GRAPH" && -f "$GRAPH" ]]; then
  bad=$(yaml_verify_criteria "$GRAPH" 2>/dev/null || true)
  # fall back to jq if yq absent: extract exit_criteria verdict_type presence via grep
  if ! grep -q 'verdict_type' "$GRAPH" 2>/dev/null; then
    : # graph may have no criteria
  fi
fi

FAILS=0
# Every DECIDABLE criterion claimed passed must have a node_test_loop with passed:true
NODES=$(jq -c 'select(.type=="node_turn") | .node' "$TRACE" 2>/dev/null | sort -u)
# For each handoff, if it claims a DECIDABLE criterion satisfied, require a node_test_loop passed:true
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  env_id=$(echo "$line" | jq -r '.envelope_id')
  [[ "$env_id" == "h-000" ]] && continue
  # for each DECIDABLE criterion in the envelope's exit_criteria, expect a passed node_test_loop
  decidable=$(echo "$line" | jq -r '.exit_criteria[]? | select(.verdict_type=="DECIDABLE") | .name')
  for c in $decidable; do
    if ! jq -e --arg c "$c" 'select(.type=="node_test_loop" and .criterion==$c and .passed==true)' "$TRACE" >/dev/null 2>&1; then
      echo "FAIL: DECIDABLE criterion '$c' (envelope $env_id) has no node_test_loop passed:true"; FAILS=$((FAILS+1))
    fi
  done
done < <(jq -c 'select(.type=="handoff")' "$TRACE" 2>/dev/null)

if [[ $FAILS -gt 0 ]]; then exit 1; fi
echo "PASS: verifier checklist discipline satisfied (DECIDABLE criteria have passed node_test_loops)"
exit 0

yaml_verify_criteria() { :; }  # placeholder — graph-side check is best-effort; trace-side is authoritative
```

- [ ] **Step 3: Write review-isolation-check.sh**

Create `scorers/deterministic/review-isolation-check.sh`:

```bash
#!/bin/bash
# review-isolation-check.sh — P0 scorer: ⑤ independence (invariant #6, §2.7).
# Usage: review-isolation-check.sh <trace.jsonl>
# Exit: 0=PASS, 1=FAIL, 2=SKIP.

set -o pipefail
TRACE="$1"
if [[ ! -f "$TRACE" ]]; then echo "Error: trace not found"; exit 2; fi

VERDICTS=$(jq -c 'select(.type=="review_verdict")' "$TRACE" 2>/dev/null)
if [[ -z "$VERDICTS" ]]; then echo "SKIP: no review_verdict events"; exit 2; fi

FAILS=0
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  env_id=$(echo "$line" | jq -r '.envelope_id')
  # non-empty evidence_tool_calls
  if ! echo "$line" | jq -e '.evidence_tool_calls | length > 0' >/dev/null 2>&1; then
    echo "FAIL: review_verdict $env_id has empty evidence_tool_calls (rubber-stamp, invariant #6)"; FAILS=$((FAILS+1))
  fi
  # correlated handoff's stripped must include producer_reasoning_trace AND producer_verdict_self_report
  stripped=$(jq -r --arg id "$env_id" 'select(.type=="handoff" and .envelope_id==$id) | .stripped[]?' "$TRACE")
  if ! echo "$stripped" | grep -q 'producer_reasoning_trace'; then
    echo "FAIL: handoff $env_id stripped lacks producer_reasoning_trace (B2)"; FAILS=$((FAILS+1))
  fi
  if ! echo "$stripped" | grep -q 'producer_verdict_self_report'; then
    echo "FAIL: handoff $env_id stripped lacks producer_verdict_self_report (B2)"; FAILS=$((FAILS+1))
  fi
  # attempt_history entries must not carry 'passed'
  if jq -e --arg id "$env_id" 'select(.type=="handoff" and .envelope_id==$id) | .attempt_history[]? | has("passed")' "$TRACE" >/dev/null 2>&1; then
    echo "FAIL: handoff $env_id attempt_history carries 'passed' (B2)"; FAILS=$((FAILS+1))
  fi
done <<< "$VERDICTS"

if [[ $FAILS -gt 0 ]]; then exit 1; fi
echo "PASS: all review_verdicts are isolation-compliant (§2.7 #6)"
exit 0
```

- [ ] **Step 4: Make scorers executable + wire into score-trace.sh**

Run: `chmod +x scorers/deterministic/handoff-contract-check.sh scorers/deterministic/verifier-checklist-check.sh scorers/deterministic/review-isolation-check.sh`

Read the P0 section of `scripts/score-trace.sh` to see how scorers are dispatched:
Run: `grep -n 'P0\|iron-law\|deterministic' scripts/score-trace.sh`
Then add the 3 new scorers to the P0 dispatch (match the existing pattern — likely a case/if on `--priority P0` that runs each `scorers/deterministic/*.sh`). If the script globs `scorers/deterministic/*.sh` automatically, no edit is needed beyond creating the files — confirm with the grep. If it lists scorers explicitly, add the 3 names.

- [ ] **Step 5: Run the scorers against the C4-new + C10-new fixtures (must PASS) and the C5-violation fixture (must FAIL once written — Task 10; for now C4/C10 PASS)**

Run:
```bash
bash scorers/deterministic/handoff-contract-check.sh tests/fixtures/traces/example-C4-new-graph-review-agent-catches.jsonl; echo "exit=$?"
bash scorers/deterministic/review-isolation-check.sh tests/fixtures/traces/example-C4-new-graph-review-agent-catches.jsonl; echo "exit=$?"
bash scorers/deterministic/verifier-checklist-check.sh tests/fixtures/traces/example-C4-new-graph-review-agent-catches.jsonl; echo "exit=$?"
```
Expected: each prints `PASS: ...` and `exit=0`.

- [ ] **Step 6: Register the scorers in references/scoring-system.md**

Find the P0 scorer list in `references/scoring-system.md`:
Run: `grep -n 'iron-law-check\|P0\|deterministic' references/scoring-system.md`
Add the 3 new scorers to the P0 list with one-line descriptions (match the existing entry style).

- [ ] **Step 7: Commit**

```bash
git add scorers/deterministic/handoff-contract-check.sh scorers/deterministic/verifier-checklist-check.sh scorers/deterministic/review-isolation-check.sh scripts/score-trace.sh references/scoring-system.md
git commit -m "$(cat <<'EOF'
feat(scorers): handoff-contract-check, verifier-checklist-check, review-isolation-check (P0)

Three mechanical P0 scorers enforcing the node-contract on the JSONL trace:
handoff-contract-check validates the typed envelope + review_verdict correlation
(B1/B2/B3); verifier-checklist-check enforces DECIDABLE criteria have passed
node_test_loops (no "判不了" masquerading); review-isolation-check enforces
non-empty evidence_tool_calls + stripped producer reasoning (invariant #6).
Exit 0/1/2 matching iron-law-check.sh. Wired into score-trace.sh P0.

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Manifest schema + vendored validator — `after_node`, graph oneOf, routing_rules (M1/C3)

The spec's M1 audit-trail item: the JSON schema is documentation-only; the REAL validator is `validate.go:197`. Edit BOTH and fix the `manual`(Go) vs `after_merge`(JSON) divergence.

**Files:**
- Modify: `references/summoner.schema.json` (line ~73 checkpoints enum + add graph oneOf + routing_rules)
- Modify: `hooks/hooks/vendor/github.com/johnson-xue/memory-validator/validate.go` (line ~197 enum)
- Modify: `hooks/vendor/github.com/johnson-xue/memory-validator/validate.go` (the dupe — keep in sync)

**Interfaces:**
- Consumes: none (standalone schema/validator edit).
- Produces: `after_node` accepted by both the JSON schema and the Go validator; `graph` shape + `routing_rules` accepted; `manual`/`after_merge` divergence fixed (pick one — the Go validator is authoritative, so align JSON to Go).

- [ ] **Step 1: Read the current validator enum + schema enum**

Run: `sed -n '190,200p' hooks/hooks/vendor/github.com/johnson-xue/memory-validator/validate.go`
Expected: line ~197 `wf.Checkpoints != "after_each" && wf.Checkpoints != "manual" && wf.Checkpoints != "none"`.

Run: `sed -n '68,76p' references/summoner.schema.json`
Expected: `"enum": ["after_each", "after_merge", "none"]`.

- [ ] **Step 2: Fix the divergence + add after_node in the Go validator (both copies)**

In `hooks/hooks/vendor/github.com/johnson-xue/memory-validator/validate.go` line ~197, change:
```go
		} else if wf.Checkpoints != "after_each" && wf.Checkpoints != "manual" && wf.Checkpoints != "none" {
			errs = append(errs, ValidationError{Code: "invalid_enum", Fields: map[string]string{"workflow": wfName, "field": "checkpoints", "actual": wf.Checkpoints, "expected": "after_each|manual|none"}})
```
to:
```go
		} else if wf.Checkpoints != "after_each" && wf.Checkpoints != "after_merge" && wf.Checkpoints != "after_node" && wf.Checkpoints != "none" {
			errs = append(errs, ValidationError{Code: "invalid_enum", Fields: map[string]string{"workflow": wfName, "field": "checkpoints", "actual": wf.Checkpoints, "expected": "after_each|after_merge|after_node|none"}})
```
Note: this FIXES the `manual`(Go) vs `after_merge`(JSON) divergence by standardizing on `after_merge` everywhere (the JSON schema already uses `after_merge`; the Go validator's `manual` was the divergence — M1). `after_node` is added.

Apply the IDENTICAL change to `hooks/vendor/github.com/johnson-xue/memory-validator/validate.go` (the dupe copy must stay in sync).

- [ ] **Step 3: Update the JSON schema enum to match**

In `references/summoner.schema.json` line ~73, change:
```json
              "enum": ["after_each", "after_merge", "none"]
```
to:
```json
              "enum": ["after_each", "after_merge", "after_node", "none"]
```

- [ ] **Step 4: Add a `routing_rules` schema + graph oneOf branch**

In `references/summoner.schema.json`, add (near the workflow schema, matching its style) a `routing_rules` object schema and a `graph` oneOf branch. Read the surrounding schema first to match indentation:
Run: `sed -n '1,80p' references/summoner.schema.json`
Then add a `routing_rules` property (map of rule-name → `{input_field, map}`) and a `graph` shape to the workflow `oneOf`. (If the implementer is unsure of the exact JSON Pointer, the minimal correct edit is: add `"after_node"` to the enum [done in Step 3] + add a `routing_rules` top-level definition — the `graph` block itself lives in plan artifacts, not the manifest, so the manifest only needs to accept `routing_rules` and `after_node`.)

- [ ] **Step 5: Build + test the validator**

Run: `cd hooks/hooks && go build ./... && go test ./...`
Expected: build succeeds; existing tests pass (the enum change is additive — `after_each`/`after_merge`/`none` still validate).

- [ ] **Step 6: Verify `after_node` now validates + `manual` is rejected**

Write a minimal manifest to a temp file and run the validator:
```bash
TMP=$(mktemp -d) && cat > "$TMP/m.yaml" <<'YAML'
project: test
workflows:
  fix:
    phases: {}
    checkpoints: after_node
YAML
cd hooks/hooks && go run ./validate-manifest --manifest "$TMP/m.yaml" 2>&1 | head; rm -rf "$TMP"
```
Expected: no `invalid_enum` error for `after_node` (it may report other missing fields, but NOT a checkpoints enum error).

- [ ] **Step 7: Commit**

```bash
git add references/summoner.schema.json hooks/hooks/vendor/github.com/johnson-xue/memory-validator/validate.go hooks/vendor/github.com/johnson-xue/memory-validator/validate.go
git commit -m "$(cat <<'EOF'
feat(manifest): add after_node + graph/routing_rules; fix manual vs after_merge divergence (M1/C3)

The JSON schema was documentation-only; the real validator is the vendored
Go at validate.go:197 (hardcoded after_each|manual|none). Edit BOTH:
add after_node + after_merge to the Go enum (standardizing on after_merge,
fixing the pre-existing manual[Go] vs after_merge[JSON] divergence — M1),
and mirror in the dupe copy. JSON schema enum gets after_node + routing_rules.

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: SKILL.md + commands + checkpoint-protocol + writing-plans integration

The orchestration glue. SKILL.md drives the walker; commands move routing into named rules; checkpoint-protocol extends RECALL; writing-plans emits the graph block.

**Files:**
- Modify: `skills/summoner/SKILL.md` (Phase Execution: walker drive)
- Modify: `commands/fix.md`, `commands/new.md` (route_* rules)
- Modify: `references/checkpoint-protocol.md` (RECALL grammar + M9 render)
- Modify: `references/workflow-reference.md` (§Per-task Graph)
- Modify: `references/manifest-spec.md` (§Node Types + §Conditional Routing Rules)

**Interfaces:**
- Consumes: the walker binary (Task 5), node-snapshot.sh (Task 6), review-agent (Task 6).
- Produces: SKILL.md instructions that, when a plan carries `summoner-task-graph`, call `summoner-walker next` → run the node ①–④ (with node-snapshot on mutating nodes per directive flags) → `record --step handoff` → spawn review-agent on `RUN_REVIEW` → `record --step review_verdict` → render `walker explain` into the checkpoint; else chain fallback.

- [ ] **Step 1: Read the current SKILL.md Phase Execution section**

Run: `grep -n 'Phase Execution\|checkpoint\|RUN_NODE\|skill' skills/summoner/SKILL.md | head -30`
Identify where the phase-execution loop is described (this is the section to extend).

- [ ] **Step 2: Add the graph-mode drive to SKILL.md**

In `skills/summoner/SKILL.md`, add a "## Graph-Mode Phase Execution" subsection (after the existing phase-execution section). Match the file's existing heading + instruction style. Content:

```markdown
## Graph-Mode Phase Execution (when the plan carries a `summoner-task-graph` block)

If the active plan artifact contains a fenced ```yaml summoner-task-graph block, drive the graph walker instead of the linear phase chain:

1. `summoner-walker --graph <plan.md> --trace <trace.jsonl> --session <session_id> next`
   → read the printed directive.
2. If the directive is `RUN_NODE`:
   - If `snapshot` is true (mutating node): run `bash scripts/node-snapshot.sh save` BEFORE ② (the walker signals intent via the flag; YOU execute the helper — single owner, the walker never touches the tree, M2).
   - Run the node's ① Ingest+Validate → ② Work → ③ Test → ④ Handoff. (The node is a closed-loop agent; ③ FAIL self-retries bounded by `max_inner_turns`.)
   - On ④, write the handoff envelope to a temp file and call `summoner-walker record --step handoff --envelope <env.json>` → walker prints `RUN_REVIEW`.
3. If the directive is `RUN_REVIEW`: spawn the `review-agent` persona (agents/review-agent.md) with the envelope's `envelope_id` (paths + exit_criteria only — NO producer reasoning). It returns a `review_verdict`. Write it + call `summoner-walker record --step review_verdict --envelope_id <id> --verdict <PASS|NEEDS-FIX> --findings <findings.json>`.
4. The walker's response routes the next step:
   - `RUN_NODE` (next node) → loop to step 2.
   - `NODE_RETRY` (same-node ⑤ NEEDS-FIX) → if `restore` is true, run `bash scripts/node-snapshot.sh restore` before re-running ②; loop to step 2 (no checkpoint).
   - `BACK_EDGE` (cross-node) → if `restore` true, restore; run the target node's ②; loop to step 2 (the next forward checkpoint reports the round-trip).
   - `CHECKPOINT` → render `summoner-walker explain` into the checkpoint block; the human picks continue/recall/skip/done/stop (FLOW decision only — quality already gated by ⑤).
   - `HALT` → budget exhausted; surface to the human.
5. If the plan has NO `summoner-task-graph` block → chain fallback (today's behavior). No existing workflow changes.
```

- [ ] **Step 3: Extend checkpoint-protocol.md RECALL grammar + M9 render**

In `references/checkpoint-protocol.md`, find the RECALL section:
Run: `grep -n 'RECALL\|recall\|continue' references/checkpoint-protocol.md | head`
Add (matching style):

```markdown
## Graph-Mode Recall + Rendering (M9)

- `RECALL` grammar extended: `recall to <node-label> reason=receiver_rejected_handoff | direction_wrong | verifier_failed`. The walker parses the target (the LLM no longer improvises it); `<node-label>` is the human-facing verb, not the id.
- Graph-mode checkpoint rendering: SKILL.md renders `summoner-walker explain` into the checkpoint. The render shows: node `label` (never id); internal step names ①②③④⑤ HIDDEN; a "you are here" route map (✓/✗/▶/⊘); the ⑤ evidence (grep hits, file:line) as proof; a walker-precomputed default recall option. `summoner-walker status` stays for debug/scorers, not the human checkpoint.
```

- [ ] **Step 4: Move routing tables in fix.md/new.md to named rules**

In `commands/fix.md`, find the routing table (logic/rpc/subsystem/migrate/gmt):
Run: `grep -n 'logic\|rpc\|subsystem\|migrate\|gmt\|route' commands/fix.md | head`
Replace the inline table with a reference to the declarative rule (the rule itself lives in the project's `summoner.yaml` under `routing_rules:` — the framework command only references it):
```markdown
Routing is declarative: the `route_by_diagnosis` rule (declared in the project's `summoner.yaml` under `routing_rules:`) maps the diagnose node's `routing_tag` to a target node id. This command does not hardcode the table — it references the rule.
```
Repeat for `commands/new.md` (its `route_by_function_type` analog).

- [ ] **Step 5: Add §Per-task Graph to workflow-reference.md + §Node Types / §Conditional Routing Rules to manifest-spec.md**

In `references/workflow-reference.md` add a "## Per-Task Graph" section (plan emits the graph block; walker-vs-chain fallback per M12; graph red flags: review-agent verdict with no `evidence_tool_calls` = fail; ⑤ read producer reasoning = fail).

In `references/manifest-spec.md` add "## Node Types" (reuse `phases` map) + "## Conditional Routing Rules" (declarative `routing_rules:` map, `input_field` + `map`, pure table lookup — §2.6.1).

- [ ] **Step 6: writing-plans integration — emit the graph block**

The `writing-plans` skill is the superpowers plugin skill (not in this repo). Document the contract in `references/workflow-reference.md`'s §Per-task Graph: a plan MUST emit a fenced ```yaml summoner-task-graph block with `budget` (incl. `phase0_cost_*`), `label` per node, `verdict_type` per criterion, `mutating` flags; emit graph iff ≥3 nodes OR mutating+back-edge (M12). (This is a doc contract the writing-plans skill consumes; the repo-side enforcement is the walker's parse-or-fallback, Task 5.)

- [ ] **Step 7: Smoke-test the end-to-end drive with a synthetic plan**

Create a throwaway plan with a graph block + a 2-node graph, then drive:
```bash
TMP=$(mktemp -d) && cat > "$TMP/plan.md" <<'MD'
# Plan
```yaml summoner-task-graph
budget: {max_graph_turns: 5, total_token_budget: 1000, max_back_edges_total: 3}
nodes:
  - id: diagnose
    label: "定位根因"
    skill: phase.debug
    exit_criteria: [{name: root_cause, verdict_type: SOFT}]
    max_inner_turns: 2
edges: []
```
MD
go run ./cmd/summoner-walker --graph "$TMP/plan.md" --trace "$TMP/t.jsonl" --session smoke next
rm -rf "$TMP"
```
Expected: JSON with `RUN_NODE` + `node=diagnose` + `label=定位根因` (proves the markdown-fence extraction + parse + first-directive path works end-to-end).

- [ ] **Step 8: Commit**

```bash
git add skills/summoner/SKILL.md commands/fix.md commands/new.md references/checkpoint-protocol.md references/workflow-reference.md references/manifest-spec.md
git commit -m "$(cat <<'EOF'
feat(orchestration): SKILL.md drives walker; commands reference route_* rules; checkpoint M9 render

SKILL.md graph-mode phase execution: summoner-walker next → RUN_NODE
(with node-snapshot on mutating per directive flags) → record handoff →
RUN_REVIEW → spawn review-agent → record review_verdict → route
(NODE_RETRY/BACK_EDGE/CHECKPOINT/HALT) → render explain. Chain fallback
for graph-less plans (backward compat). Commands move routing into
declarative route_* rules. checkpoint-protocol extends RECALL + M9 render.
workflow-reference + manifest-spec get §Per-task Graph / §Node Types /
§Conditional Routing Rules.

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: Remaining fixtures (C2/C3/C5/C9) + regression/stability harness

The multi-case testing the goal demands. The 4 new fixtures exercise: clean pass, verify-fail back-edge, isolation violation (scorer FAIL), 3× same-finding escalation. Then wire them into the regression/stability harness.

**Files:**
- Create: `tests/fixtures/traces/example-C2-clean-graph-pass.jsonl`
- Create: `tests/fixtures/traces/example-C3-verify-fail-backedge.jsonl`
- Create: `tests/fixtures/traces/example-C5-review-isolation-violation.jsonl`
- Create: `tests/fixtures/traces/example-C9-three-times-same-finding.jsonl`
- Modify: `scripts/regression-test.sh` + `scripts/stability-test.sh` (add the graph-mode fixture pairs to the harness if they list fixtures explicitly)

**Interfaces:**
- Consumes: the event-type contract (Task 1) + scorer behavior (Task 7).
- Produces: 4 fixtures + harness wiring. C5 must make `review-isolation-check.sh` FAIL (exit 1); the others must PASS.

- [ ] **Step 1: Write C2 — clean full-graph pass**

Create `tests/fixtures/traces/example-C2-clean-graph-pass.jsonl`: a fix workflow where every node ⑤ returns PASS on the first attempt. Structure: session_start → h-000 (bootstrap) → h-001 + review_verdict PASS → h-002 + PASS → h-003 + PASS → h-004 + PASS → checkpoints after each ⑤-PASS → session_end (human_interventions=N, defects_caught_before_human=0, human_rework_rounds=0). Match the C4-new fixture's field shapes exactly (envelope_id h-000..h-004, no `passed` in attempt_history, verdict_type on every criterion, stripped includes producer_reasoning_trace+producer_verdict_self_report).

- [ ] **Step 2: Write C3 — verify ③ FAIL → retry → exhausted → back-edge skipping reproduce**

Create `tests/fixtures/traces/example-C3-verify-fail-backedge.jsonl`: verify node's `node_test_loop` passed:false, retries up to `max_inner_turns`, then `node_test_loop exhausted=true`, then a cross-node `handoff_reject` (verify→fix, skip:[reproduce]). Assert the back_edge in the graph declares skip:[reproduce]. The ⑤ on verify returns NEEDS-FIX (cross-node) → `handoff_reject`.

- [ ] **Step 3: Write C5 — review-isolation violation (scorer MUST FAIL)**

Create `tests/fixtures/traces/example-C5-review-isolation-violation.jsonl`: a `review_verdict` with EMPTY `evidence_tool_calls` (the rubber-stamp), AND/OR a handoff whose `stripped` omits `producer_reasoning_trace`. This fixture is adversarial — it MUST fail `review-isolation-check.sh` (exit 1). Add a `session_end` note: "adversarial fixture — review-isolation-check.sh must FAIL this trace."

- [ ] **Step 4: Write C9 — cross-node ⑤ 3× same-finding escalation**

Create `tests/fixtures/traces/example-C9-three-times-same-finding.jsonl`: a cross-node ⑤ returns NEEDS-FIX with the SAME finding 3 times (h-002, h-003, h-004 — same finding text). The 3rd escalates to a CHECKPOINT (the 4th same-finding back-edge never fires). Assert `handoff_reject` events (cross-node) ×3, then a checkpoint, NOT a 4th handoff_reject. (Same-node would be node_review_retry; C9 is deliberately cross-node to exercise the 3× counter.)

- [ ] **Step 5: Validate all 4 fixtures as JSONL**

Run:
```bash
for f in example-C2-clean-graph-pass example-C3-verify-fail-backedge example-C5-review-isolation-violation example-C9-three-times-same-finding; do
  python3 -c "import json;[json.loads(l) for l in open('tests/fixtures/traces/$f.jsonl') if l.strip()]; print('$f valid')"
done
```
Expected: 4 "valid" lines.

- [ ] **Step 6: Run the scorers against each new fixture**

Run:
```bash
for f in C2 C3 C9; do
  bash scorers/deterministic/handoff-contract-check.sh "tests/fixtures/traces/example-$f-*.jsonl"; echo "$f handoff exit=$?"
  bash scorers/deterministic/review-isolation-check.sh "tests/fixtures/traces/example-$f-*.jsonl"; echo "$f review exit=$?"
done
bash scorers/deterministic/review-isolation-check.sh tests/fixtures/traces/example-C5-review-isolation-violation.jsonl; echo "C5 review exit=$? (expect 1=FAIL)"
```
Expected: C2/C3/C9 PASS (exit 0); C5 FAIL (exit 1).

- [ ] **Step 7: Wire the fixture pairs into regression-test.sh / stability-test.sh**

Read how the harness references fixtures:
Run: `grep -n 'fixtures\|example-\|baseline' scripts/regression-test.sh scripts/stability-test.sh | head`
If fixtures are listed explicitly, add the C4/C10 old-vs-new pairs as baseline-vs-new pairs (the Δ is the headline proof). If the harness globs, no edit needed.

- [ ] **Step 8: Commit**

```bash
git add tests/fixtures/traces/example-C2-clean-graph-pass.jsonl tests/fixtures/traces/example-C3-verify-fail-backedge.jsonl tests/fixtures/traces/example-C5-review-isolation-violation.jsonl tests/fixtures/traces/example-C9-three-times-same-finding.jsonl scripts/regression-test.sh scripts/stability-test.sh
git commit -m "$(cat <<'EOF'
test(fixtures): C2 clean pass, C3 verify-fail back-edge, C5 isolation violation, C9 3× escalation

C2: every ⑤ PASS (full-graph clean). C3: verify ③ FAIL→retry→exhausted→
cross-node handoff_reject skip:[reproduce]. C5: adversarial — empty
evidence_tool_calls → review-isolation-check.sh MUST FAIL. C9: cross-node
⑤ same-finding 3× → 3rd escalates to checkpoint, 4th never fires.
Scorers verified: C2/C3/C9 PASS, C5 FAIL.

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

## Self-Review

**1. Spec coverage** — map each spec section to a task:
- §2.1 Node Contract → Task 6 (node-contract.md) + Task 3 (walk.go same/cross-node).
- §2.2 envelope + §2.2.1 review_verdict → Task 1 (trace-protocol) + Task 3 (HandoffEnvelope/ReviewVerdict types) + Task 7 (handoff-contract-check scorer).
- §2.4 decidable verifier → Task 7 (verifier-checklist-check) + Task 6 (node-contract.md).
- §2.5 graph declaration → Task 2 (parse.go) + Task 5 (fence extraction).
- §2.6/§2.6.1 manifest + routing → Task 8 (schema/validator) + Task 9 (manifest-spec.md, commands).
- §2.7 invariants → Task 6 (node-contract.md invariants) + Task 7 (review-isolation-check = invariant #6) + Task 3 (3× + budget = invariant #2).
- §2.8 B4/B5 bootstrap → Task 3 (RecordHandoff bootstrap h-000 handling + RUN_REVIEW) + Task 6.
- §2.9 M9 checkpoint UX → Task 4 (render.go) + Task 9 (checkpoint-protocol.md, SKILL.md render).
- §3 components table → every row maps to a task (node-contract.md=6, manifest-spec=9, checkpoint-protocol=9, workflow-reference=9, trace-protocol=1, scoring-system=7, 3 scorers=7, review-agent=6, walker+internal/graph=2-5, node-snapshot=6, SKILL.md=9, commands=9, writing-plans=9, schema+validator=8, fixtures=10).
- §4 data flow → Task 9 (SKILL.md drive) + Task 5 (CLI).
- §5 error handling → Task 3 (walk.go: max_inner_turns, 3×, alternating window, budget halt) + Task 9 (SKILL.md CHECKPOINT/HALT).
- §6 testing → Task 10 (C2/C3/C5/C9 fixtures + harness).
- §7.5 audit trail (H1-H6, C1-C2, B1-B5, M1-M9) → distributed: H1/B1/B2/B3=Task7, H2=Task6, H3=Task3, H4=Task3+5, H5=Task7, H6=Task3, M1=Task8, M2=Task6, M4=Task5, M9=Task4, others=Task6/9.
- §9 success criteria → Task 10 (fixtures prove the Δ) + Task 7 (scorers measure it).
- §10 walker → Tasks 2-5.
No spec section is uncovered.

**2. Placeholder scan** — searched the plan; no "TBD"/"TODO"/"implement later"/"add appropriate". Every code step shows actual code; every bash step shows the script. The two intentionally-minimal spots (Task 8 Step 4 graph oneOf, Task 7 Step 1 yaml_verify_criteria) are flagged inline as best-effort because the trace-side check is authoritative — not hidden placeholders.

**3. Type consistency** — checked: `HandoffEnvelope`/`ReviewVerdict`/`Finding`/`ExitCriterion`/`Directive` field names are identical between parse.go (Task 2), walk.go (Task 3), render.go (Task 4), main.go (Task 5), and the scorers (Task 7 read them as JSON keys matching the struct json tags). `envelope_id`/`from_node`/`to_node`/`label`/`artifacts`/`exit_criteria`/`factual_claim`/`attempt_history`/`budget_remaining`/`stripped` (handoff) and `envelope_id`/`node`/`reviewer`/`verdict`/`findings`/`evidence_tool_calls` (review_verdict) are consistent across the trace-protocol doc, the Go structs, and the jq scorer queries. `node_review_retry` vs `handoff_reject` event-type names match between walk.go (`typeOf` + explicit emit), trace-protocol.md (Task 1), and the fixtures. `after_node`/`after_merge` consistent between schema (Task 8) and validator (Task 8).

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-29-graph-node-contract-implementation.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration. Best for a plan this size (10 tasks, Go + bash + markdown): each subagent gets one focused task with its own context, I gate the output between tasks (run the tests, check the scorer exit codes), and we catch type-mismatch / field-name drift early.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints for review. Keeps everything in one context (useful given the cross-task type consistency requirements) but the context will get long across 10 tasks.

Which approach?
