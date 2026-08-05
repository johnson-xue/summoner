package graph

import (
	"testing"
)

// memTrace collects emitted events for assertions.
type memTrace struct{ events []map[string]interface{} }

func (m *memTrace) Append(e map[string]interface{}) error { m.events = append(m.events, e); return nil }

// withTempStateDir redirects walk-state persistence into a per-test temp
// directory so Save/LoadState never touch the real home dir. This keeps the
// suite hermetic: without it, a leftover walk-state file from a prior run would
// make NewWalker load stale state and break the replay assertions.
func withTempStateDir(t *testing.T) {
	t.Helper()
	prev := stateDirOverride
	stateDirOverride = t.TempDir()
	t.Cleanup(func() { stateDirOverride = prev })
}

func TestWalker_C4New_Replay(t *testing.T) {
	withTempStateDir(t)
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
		Artifacts:       []string{"docs/reviews/task-npe.md", "player/task/task.go:142"},
		ExitCriteria:    []ExitCriterion{{Name: "root_cause", VerdictType: SOFT, Pin: "file:line"}},
		FactualClaim:    "root cause = nil deref",
		AttemptHistory:  []AttemptEntry{{Node: "diagnose", Attempts: 1, Verifier: "root_cause_pin"}},
		BudgetRemaining: BudgetRemaining{18, 42000},
		Stripped:        []string{"producer_reasoning_trace", "producer_verdict_self_report"},
	}
}
func h002() HandoffEnvelope {
	return HandoffEnvelope{
		EnvelopeID: "h-002", FromNode: "fix", ToNode: "verify", Label: "补 nil 判空",
		Artifacts: []string{"player/task/task.go"},
		ExitCriteria: []ExitCriterion{
			{Name: "diff_applied", VerdictType: DECIDABLE},
			{Name: "all_deref_sites_covered", VerdictType: SOFT, GrepPattern: "player.SubTask"},
		},
		FactualClaim: "nil-guard added at 142",
		AttemptHistory: []AttemptEntry{
			{Node: "diagnose", Attempts: 1, Verifier: "root_cause_pin"},
			{Node: "fix", Attempts: 1, Verifier: "build_clean"},
		},
		BudgetRemaining: BudgetRemaining{15, 38000},
		Stripped:        []string{"producer_reasoning_trace", "producer_verdict_self_report"},
	}
}
func rv(env, node, verdict string, fs []Finding) ReviewVerdict {
	return ReviewVerdict{
		EnvelopeID: env, Node: node, Reviewer: "review-agent:" + node,
		Verdict: verdict, Findings: fs,
		EvidenceToolCalls: []string{"grep -n player.SubTask player/task/task.go"},
	}
}

func TestRecordReviewVerdict_UnknownNode_NoPanic(t *testing.T) {
	withTempStateDir(t)
	g, err := ParseGraph([]byte(c4GraphYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tr := &memTrace{}
	w := NewWalker(g, "test-unknown-node", tr)

	// Record a NEEDS-FIX verdict whose Node is not declared in the graph.
	// Pre-Fix-1(a) this nil-derefed on node.MaxInnerTurns (NodeByID → nil, false;
	// the false was discarded). Now it must escalate to Checkpoint, not panic.
	d, err := w.RecordReviewVerdict(ReviewVerdict{
		EnvelopeID: "h-999", Node: "nonexistent", Reviewer: "review-agent",
		Verdict: "NEEDS-FIX",
	})
	if err != nil {
		t.Fatalf("RecordReviewVerdict returned error: %v", err)
	}
	if d.Kind != Checkpoint {
		t.Fatalf("expected Checkpoint for unknown node, got %v (node=%q)", d.Kind, d.Node)
	}
}

func TestWalker_HaltsOnBudgetExhaustion(t *testing.T) {
	withTempStateDir(t)
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
		Label: "定位根因", Artifacts: []string{"a"}, ExitCriteria: []ExitCriterion{{Name: "x", VerdictType: SOFT}},
		FactualClaim: "c", Stripped: []string{"producer_reasoning_trace"}})
	w.RecordHandoff(HandoffEnvelope{EnvelopeID: "h-002", FromNode: "diagnose", ToNode: "diagnose",
		Label: "定位根因", Artifacts: []string{"a"}, ExitCriteria: []ExitCriterion{{Name: "x", VerdictType: SOFT}},
		FactualClaim: "c", Stripped: []string{"producer_reasoning_trace"}})
	d := w.Next()
	if d.Kind != Halt {
		t.Fatalf("expected Halt after graph_turns exhausted, got %+v", d)
	}
}

// alternatingGraphYAML is a single self-looping node with the window enabled.
const alternatingGraphYAML = `
budget:
  max_graph_turns: 20
  total_token_budget: 50000
  max_back_edges_total: 8
  alternating_finding_window: 4
nodes:
  - id: fix
    label: "补 nil 判空"
    skill: antia-logic
    exit_criteria:
      - {name: diff_applied, verdict_type: DECIDABLE}
    max_inner_turns: 5
    mutating: true
edges: []
back_edges:
  - {from: fix, to: fix, reason: review_needs_fix}
`

// TestRecordReviewVerdict_AlternatingWindow_Escalation feeds A,B,A,B on the
// same node (max_inner_turns:5 so it doesn't fire first). The 4th finding
// completes the rotate → Checkpoint BEFORE a node_review_retry is emitted for
// that final verdict (mirrors the 3× rule's early-return-before-append).
func TestRecordReviewVerdict_AlternatingWindow_Escalation(t *testing.T) {
	withTempStateDir(t)
	g, err := ParseGraph([]byte(alternatingGraphYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tr := &memTrace{}
	w := NewWalker(g, "test-alt-esc", tr)
	// bootstrap so CurrentNode/Attempt are set + a handoff to schedule ⑤
	w.RecordHandoff(HandoffEnvelope{EnvelopeID: "h-001", FromNode: "fix", ToNode: "fix",
		Label: "补 nil 判空", Artifacts: []string{"a.go"},
		ExitCriteria: []ExitCriterion{{Name: "diff_applied", VerdictType: DECIDABLE}},
		FactualClaim: "c", Stripped: []string{"producer_reasoning_trace"}})
	fA := []Finding{{File: "a.go", Line: 1, Issue: "missed nil check"}}
	fB := []Finding{{File: "b.go", Line: 2, Issue: "race on write"}}
	// A → NodeRetry (window: [A], not full)
	r, _ := w.RecordReviewVerdict(rv("h-001", "fix", "NEEDS-FIX", fA))
	if r.Kind != NodeRetry {
		t.Fatalf("1st finding A: expected NodeRetry, got %v", r.Kind)
	}
	// B → NodeRetry (window: [A,B], not full)
	r, _ = w.RecordReviewVerdict(rv("h-001", "fix", "NEEDS-FIX", fB))
	if r.Kind != NodeRetry {
		t.Fatalf("2nd finding B: expected NodeRetry, got %v", r.Kind)
	}
	// A → NodeRetry (window: [A,B,A], not full)
	r, _ = w.RecordReviewVerdict(rv("h-001", "fix", "NEEDS-FIX", fA))
	if r.Kind != NodeRetry {
		t.Fatalf("3rd finding A: expected NodeRetry, got %v", r.Kind)
	}
	// B → window full [A,B,A,B], A and B both reappear non-contiguously → escalate
	r, _ = w.RecordReviewVerdict(rv("h-001", "fix", "NEEDS-FIX", fB))
	if r.Kind != Checkpoint {
		t.Fatalf("4th finding B: expected Checkpoint (rotate escalation), got %v", r.Kind)
	}
	// the escalation early-returns BEFORE emitting node_review_retry for the 4th:
	// exactly 3 retry events (A,B,A), not 4.
	retryCount := 0
	for _, e := range tr.events {
		if e["type"] == "node_review_retry" && e["envelope_id"] == "h-001" {
			retryCount++
		}
	}
	if retryCount != 3 {
		t.Fatalf("expected 3 node_review_retry events (A,B,A), got %d — the 4th must escalate not retry", retryCount)
	}
	// window recorded the findings
	if win := w.state.Windows["fix"]; len(win) != 4 {
		t.Fatalf("expected Windows[fix] len 4, got %d (%v)", len(win), win)
	}
}

// TestRecordReviewVerdict_AlternatingWindow_SingleFindingNoEscalate feeds
// A,A,A on a node with max_inner_turns:5 (so max_inner_turns doesn't fire)
// and alternating_finding_window:4. A single repeating finding is NOT a
// rotation → the window must NOT escalate (returns NodeRetry). The 3× rule
// (cross-node) and max_inner_turns bound single-finding loops, not the window.
func TestRecordReviewVerdict_AlternatingWindow_SingleFindingNoEscalate(t *testing.T) {
	withTempStateDir(t)
	g, err := ParseGraph([]byte(alternatingGraphYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tr := &memTrace{}
	w := NewWalker(g, "test-alt-single", tr)
	w.RecordHandoff(HandoffEnvelope{EnvelopeID: "h-001", FromNode: "fix", ToNode: "fix",
		Label: "补 nil 判空", Artifacts: []string{"a.go"},
		ExitCriteria: []ExitCriterion{{Name: "diff_applied", VerdictType: DECIDABLE}},
		FactualClaim: "c", Stripped: []string{"producer_reasoning_trace"}})
	fA := []Finding{{File: "a.go", Line: 1, Issue: "missed nil check"}}
	for i := 0; i < 3; i++ {
		r, _ := w.RecordReviewVerdict(rv("h-001", "fix", "NEEDS-FIX", fA))
		if r.Kind != NodeRetry {
			t.Fatalf("finding A iteration %d: expected NodeRetry (no window escalate for single finding), got %v", i, r.Kind)
		}
	}
	// window has [A,A,A] but only 1 distinct finding → no escalate
	if win := w.state.Windows["fix"]; len(win) != 3 {
		t.Fatalf("expected Windows[fix] len 3, got %d", len(win))
	}
}

// TestRecordReviewVerdict_AlternatingWindow_SingleReappearanceNoEscalate
// feeds A,B,A,C on the same node (max_inner_turns:5 so it doesn't fire
// first, window n=4). Per spec §5/M7 the rotate rule escalates only when
// "≥2 distinct findings each reappear" non-contiguously. Here only A
// reappears (B and C appear once each), so this is NOT a rotation — the
// window must NOT escalate; all four verdicts return NodeRetry. This guards
// against the over-fire defect where the predicate required only ≥1
// reappearing finding (A,B,A,C wrongly escalated).
func TestRecordReviewVerdict_AlternatingWindow_SingleReappearanceNoEscalate(t *testing.T) {
	withTempStateDir(t)
	g, err := ParseGraph([]byte(alternatingGraphYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tr := &memTrace{}
	w := NewWalker(g, "test-alt-single-reappear", tr)
	w.RecordHandoff(HandoffEnvelope{EnvelopeID: "h-001", FromNode: "fix", ToNode: "fix",
		Label: "补 nil 判空", Artifacts: []string{"a.go"},
		ExitCriteria: []ExitCriterion{{Name: "diff_applied", VerdictType: DECIDABLE}},
		FactualClaim: "c", Stripped: []string{"producer_reasoning_trace"}})
	fA := []Finding{{File: "a.go", Line: 1, Issue: "missed nil check"}}
	fB := []Finding{{File: "b.go", Line: 2, Issue: "race on write"}}
	fC := []Finding{{File: "c.go", Line: 3, Issue: "unchecked error"}}
	// A,B,A,C: only A reappears non-contiguously; B and C appear once each.
	// Spec: ≥2 distinct findings EACH reappear → only 1 qualifies → no escalate.
	for i, f := range [][]Finding{fA, fB, fA, fC} {
		r, _ := w.RecordReviewVerdict(rv("h-001", "fix", "NEEDS-FIX", f))
		if r.Kind != NodeRetry {
			t.Fatalf("finding %d (%v): expected NodeRetry (window must not escalate when only 1 finding reappears), got %v", i, f, r.Kind)
		}
	}
	// window recorded all 4 findings, [A,B,A,C], but did not escalate
	if win := w.state.Windows["fix"]; len(win) != 4 {
		t.Fatalf("expected Windows[fix] len 4, got %d (%v)", len(win), win)
	}
}

// TestRecordReviewVerdict_AlternatingWindow_Disabled_WhenZero feeds A,B,A,B
// with alternating_finding_window:0 (disabled). Asserts NO window escalation
// (returns NodeRetry up to max_inner_turns) — zero means disabled.
func TestRecordReviewVerdict_AlternatingWindow_Disabled_WhenZero(t *testing.T) {
	withTempStateDir(t)
	g, err := ParseGraph([]byte(`
budget:
  max_graph_turns: 20
  total_token_budget: 50000
  max_back_edges_total: 8
  alternating_finding_window: 0
nodes:
  - id: fix
    label: "补 nil 判空"
    skill: antia-logic
    exit_criteria:
      - {name: diff_applied, verdict_type: DECIDABLE}
    max_inner_turns: 5
    mutating: true
edges: []
back_edges:
  - {from: fix, to: fix, reason: review_needs_fix}
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tr := &memTrace{}
	w := NewWalker(g, "test-alt-disabled", tr)
	w.RecordHandoff(HandoffEnvelope{EnvelopeID: "h-001", FromNode: "fix", ToNode: "fix",
		Label: "补 nil 判空", Artifacts: []string{"a.go"},
		ExitCriteria: []ExitCriterion{{Name: "diff_applied", VerdictType: DECIDABLE}},
		FactualClaim: "c", Stripped: []string{"producer_reasoning_trace"}})
	fA := []Finding{{File: "a.go", Line: 1, Issue: "missed nil check"}}
	fB := []Finding{{File: "b.go", Line: 2, Issue: "race on write"}}
	for i, f := range [][]Finding{fA, fB, fA, fB} {
		r, _ := w.RecordReviewVerdict(rv("h-001", "fix", "NEEDS-FIX", f))
		if r.Kind != NodeRetry {
			t.Fatalf("finding %d: expected NodeRetry (window disabled when 0), got %v", i, r.Kind)
		}
	}
	// window map untouched (disabled)
	if _, ok := w.state.Windows["fix"]; ok {
		t.Fatal("expected Windows[fix] absent (window disabled), got entry")
	}
}

// escalation3xGraphYAML is a 3-node graph diagnose→fix→verify with a CROSS-NODE
// back-edge verify→fix (so a NEEDS-FIX ⑤ on verify is cross-node, hitting the
// 3× same-finding path in RecordReviewVerdict). max_back_edges_total:8 (high so
// the budget check doesn't fire before escalation needs ≥2 back-edges),
// alternating_finding_window:0 (disabled — the I2 window needs ≥2 distinct
// reappearing findings to fire; a single repeating finding never triggers it,
// but 0 is the clean disable used here so nothing interferes with the 3× rule),
// max_inner_turns:5 on verify (high so same-node exhaustion never fires first).
const escalation3xGraphYAML = `
budget:
  max_graph_turns: 20
  total_token_budget: 50000
  max_back_edges_total: 8
  alternating_finding_window: 0
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
    max_inner_turns: 5
    mutating: false
edges:
  - {from: diagnose, to: fix}
  - {from: fix, to: verify}
back_edges:
  - {from: verify, to: fix, reason: verifier_failed}
`

// TestRecordReviewVerdict_3xEscalation_Checkpoint drives the 3× same-finding
// cross-node escalation (§2.7 #2). Three NEEDS-FIX verdicts on `verify` carry
// the SAME finding (same findingKey = file:line). The 1st and 2nd increment
// FindingsSeen[key] to 1 then 2 (both <3) and each emits a cross-node
// handoff_reject (BackEdgeKind). The 3rd makes FindingsSeen[key]=3 ≥3 → the
// walker returns Checkpoint BEFORE the handoff_reject append (walk.go ~179,
// early-return precedes the append at ~187) → NO 3rd handoff_reject is emitted.
// So the trace holds exactly 2 handoff_reject events + the final Checkpoint.
// This is the unit-test counterpart to the C9 trace fixture; it guards against
// the regression where a test asserts a 3rd handoff_reject (the C9 defect).
func TestRecordReviewVerdict_3xEscalation_Checkpoint(t *testing.T) {
	withTempStateDir(t)
	g, err := ParseGraph([]byte(escalation3xGraphYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tr := &memTrace{}
	w := NewWalker(g, "test-3x-escalation", tr)

	// bootstrap → RUN_NODE diagnose
	d := w.Next()
	if d.Kind != RunNode || d.Node != "diagnose" {
		t.Fatalf("expected RUN_NODE diagnose, got %+v", d)
	}
	// h-001 diagnose→fix → RUN_REVIEW
	if _, err := w.RecordHandoff(h001()); err != nil {
		t.Fatal(err)
	}
	// ⑤ PASS on diagnose → Checkpoint (advances to fix)
	r, _ := w.RecordReviewVerdict(rv("h-001", "diagnose", "PASS", nil))
	if r.Kind != Checkpoint {
		t.Fatalf("diagnose PASS: expected Checkpoint, got %v", r.Kind)
	}
	// h-002 fix→verify → RUN_REVIEW (⑤ runs on verify)
	if _, err := w.RecordHandoff(h002()); err != nil {
		t.Fatal(err)
	}

	// Same finding across all 3 NEEDS-FIX verdicts on verify (cross-node —
	// verify's back-edge target is fix, ≠ verify). The findingKey dedup key
	// is fs[0].File + ":" + fs[0].Line, so keep File+Line identical.
	same := []Finding{{File: "player/task/task.go", Line: 187, Issue: "unchecked deref"}}
	// 1st NEEDS-FIX → FindingsSeen[key]=1 <3 → BackEdgeKind + handoff_reject #1
	r, _ = w.RecordReviewVerdict(rv("h-002", "verify", "NEEDS-FIX", same))
	if r.Kind != BackEdgeKind {
		t.Fatalf("1st NEEDS-FIX: expected BackEdgeKind, got %v", r.Kind)
	}
	// 2nd NEEDS-FIX → FindingsSeen[key]=2 <3 → BackEdgeKind + handoff_reject #2
	r, _ = w.RecordReviewVerdict(rv("h-002", "verify", "NEEDS-FIX", same))
	if r.Kind != BackEdgeKind {
		t.Fatalf("2nd NEEDS-FIX: expected BackEdgeKind, got %v", r.Kind)
	}
	// 3rd NEEDS-FIX → FindingsSeen[key]=3 ≥3 → Checkpoint (early-return-before-append)
	r, _ = w.RecordReviewVerdict(rv("h-002", "verify", "NEEDS-FIX", same))
	if r.Kind != Checkpoint {
		t.Fatalf("3rd NEEDS-FIX: expected Checkpoint (3× escalation), got %v", r.Kind)
	}

	// Exactly 2 handoff_reject events — the 3rd verdict escalates WITHOUT
	// emitting a 3rd handoff_reject (the >=3 early-return precedes the append).
	rejects := 0
	for _, e := range tr.events {
		if e["type"] == "handoff_reject" && e["envelope_id"] == "h-002" {
			rejects++
		}
	}
	if rejects != 2 {
		t.Fatalf("expected exactly 2 handoff_reject events, got %d — the 3rd verdict must escalate (Checkpoint) without emitting a 3rd reject", rejects)
	}
	// the finding counter did reach 3 (escalation key in state)
	if seen := w.state.FindingsSeen["player/task/task.go:187"]; seen != 3 {
		t.Fatalf("expected FindingsSeen key count 3, got %d", seen)
	}
	// BackEdges incremented on ALL 3 cross-node verdicts (walk.go ~175 runs
	// BEFORE the >=3 early-return at ~178), so the counter reaches 3 even though
	// the 3rd verdict escalates without emitting a handoff_reject (the append at
	// ~187 is skipped by the early-return). The 2-rejects invariant is on the
	// emitted handoff_reject events (asserted above), NOT the counter.
	if w.state.BackEdges != 3 {
		t.Fatalf("expected BackEdges=3 (increments before the >=3 early-return), got %d", w.state.BackEdges)
	}
}

// TestRecordReviewVerdict_MaxBackEdgesHalt drives the MaxBackEdgesTotal
// exhaustion path via RecordReviewVerdict (distinct from
// TestWalker_HaltsOnBudgetExhaustion, which tests max_graph_turns via Next()).
// max_back_edges_total:2 (low), max_graph_turns:20 (high so it doesn't fire
// first), alternating_finding_window:0 (disabled). Three cross-node NEEDS-FIX
// on verify with DISTINCT findings each time (so the 3× same-finding rule does
// NOT fire — a different findingKey each verdict means FindingsSeen[key] stays
// at 1 per key, never reaching 3). The 1st and 2nd → BackEdgeKind
// (BackEdges=1, then 2); the 3rd → BackEdges would be 3 ≥ max_back_edges_total:2
// → Checkpoint (budget-exhaustion early-return, walk.go ~182-184).
func TestRecordReviewVerdict_MaxBackEdgesHalt(t *testing.T) {
	withTempStateDir(t)
	g, err := ParseGraph([]byte(`
budget:
  max_graph_turns: 20
  total_token_budget: 50000
  max_back_edges_total: 2
  alternating_finding_window: 0
nodes:
  - id: diagnose
    label: "定位根因"
    skill: phase.debug
    exit_criteria:
      - {name: root_cause, verdict_type: SOFT}
    max_inner_turns: 3
    mutating: false
  - id: fix
    label: "补 nil 判空"
    skill: antia-logic
    exit_criteria:
      - {name: diff_applied, verdict_type: DECIDABLE}
    max_inner_turns: 4
    mutating: true
  - id: verify
    label: "跑测试套件"
    skill: phase.verify
    exit_criteria:
      - {name: tests_pass, verdict_type: DECIDABLE}
    max_inner_turns: 5
    mutating: false
edges:
  - {from: diagnose, to: fix}
  - {from: fix, to: verify}
back_edges:
  - {from: verify, to: fix, reason: verifier_failed}
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tr := &memTrace{}
	w := NewWalker(g, "test-max-backedges", tr)

	w.Next() // bootstrap → RUN_NODE diagnose
	w.RecordHandoff(h001())
	w.RecordReviewVerdict(rv("h-001", "diagnose", "PASS", nil)) // → Checkpoint, advance to fix
	w.RecordHandoff(h002())                                     // fix→verify → RUN_REVIEW on verify

	// DISTINCT findings each verdict → different findingKey each time →
	// FindingsSeen[key] stays 1, the 3× rule never fires (it needs the SAME
	// key 3×). Only the global BackEdges counter accumulates.
	f1 := []Finding{{File: "player/task/task.go", Line: 187, Issue: "deref unchecked"}}
	f2 := []Finding{{File: "player/task/task.go", Line: 53, Issue: "missing nil check"}}
	// 1st → BackEdges=1 < max(2) → BackEdgeKind + handoff_reject
	r, _ := w.RecordReviewVerdict(rv("h-002", "verify", "NEEDS-FIX", f1))
	if r.Kind != BackEdgeKind {
		t.Fatalf("1st NEEDS-FIX: expected BackEdgeKind (BackEdges=1 <2), got %v", r.Kind)
	}
	// 2nd → BackEdges=2 ≥ max(2) → Checkpoint (budget exhausted)
	r, _ = w.RecordReviewVerdict(rv("h-002", "verify", "NEEDS-FIX", f2))
	if r.Kind != Checkpoint {
		t.Fatalf("2nd NEEDS-FIX: expected Checkpoint (BackEdges=2 ≥ max_back_edges_total:2), got %v", r.Kind)
	}
	// BackEdges reached the cap (2) — the walker halted on the 2nd verdict via
	// the MaxBackEdgesTotal exhaustion early-return (walk.go ~182-184), NOT the
	// 3× rule: each findingKey was distinct, so no FindingsSeen[key] reached 3.
	if w.state.BackEdges != 2 {
		t.Fatalf("expected BackEdges=2 (the cap reached), got %d", w.state.BackEdges)
	}
	// per-key finding counts stayed at 1 — confirms the 3× rule was inert
	// (distinct findings) and the halt was purely the budget exhaustion path
	if w.state.FindingsSeen[findingKey(f1)] != 1 || w.state.FindingsSeen[findingKey(f2)] != 1 {
		t.Fatalf("expected per-key FindingsSeen=1 for the two fed findings (3× rule inert for distinct findings), got %+v", w.state.FindingsSeen)
	}
}
