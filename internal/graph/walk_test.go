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
