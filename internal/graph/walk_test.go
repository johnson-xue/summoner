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
