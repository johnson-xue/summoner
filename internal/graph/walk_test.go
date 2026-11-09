package graph

import (
	"fmt"
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
	// The envelope's producer IS the unknown node (host sent a garbage handoff —
	// RecordHandoff deliberately does not validate graph membership), so the
	// verdict passes the producer-match gate and reaches the routing fallback.
	w.RecordHandoff(HandoffEnvelope{EnvelopeID: "h-001", FromNode: "nonexistent", ToNode: "diagnose",
		Label: "x", Artifacts: []string{"a"}, ExitCriteria: []ExitCriterion{{Name: "x", VerdictType: SOFT}},
		FactualClaim: "c", Stripped: []string{"producer_reasoning_trace"}})
	d, err := w.RecordReviewVerdict(ReviewVerdict{
		EnvelopeID: "h-001", Node: "nonexistent", Reviewer: "review-agent",
		Verdict: "NEEDS-FIX", EvidenceToolCalls: []string{"grep -n x a.go"},
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
	// one verdict per envelope: handoff → PASS verdict → handoff → PASS verdict
	// (each handoff consumes a graph turn; max_graph_turns:2 exhausts on the 2nd)
	mkEnv := func(id string) HandoffEnvelope {
		return HandoffEnvelope{EnvelopeID: id, FromNode: "diagnose", ToNode: "diagnose",
			Label: "定位根因", Artifacts: []string{"a"}, ExitCriteria: []ExitCriterion{{Name: "x", VerdictType: SOFT}},
			FactualClaim: "c", Stripped: []string{"producer_reasoning_trace"}}
	}
	w.RecordHandoff(mkEnv("h-001"))
	w.RecordReviewVerdict(rv("h-001", "diagnose", "PASS", nil))
	w.RecordHandoff(mkEnv("h-002"))
	w.RecordReviewVerdict(rv("h-002", "diagnose", "PASS", nil))
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

// hf builds a same-node (fix→fix) handoff for the alternating-window tests.
// One verdict per envelope: each retry cycle re-runs ②③④ and hands off a NEW
// envelope, so the test interleaves hf() before every verdict.
func hf(envID string) HandoffEnvelope {
	return HandoffEnvelope{EnvelopeID: envID, FromNode: "fix", ToNode: "fix",
		Label: "补 nil 判空", Artifacts: []string{"a.go"},
		ExitCriteria: []ExitCriterion{{Name: "diff_applied", VerdictType: DECIDABLE}},
		FactualClaim: "c", Stripped: []string{"producer_reasoning_trace"}}
}

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
	ids := []string{"h-001", "h-002", "h-003", "h-004"}
	fA := []Finding{{File: "a.go", Line: 1, Issue: "missed nil check"}}
	fB := []Finding{{File: "b.go", Line: 2, Issue: "race on write"}}
	for i, f := range [][]Finding{fA, fB, fA, fB} {
		if _, err := w.RecordHandoff(hf(ids[i])); err != nil {
			t.Fatal(err)
		}
		r, _ := w.RecordReviewVerdict(rv(ids[i], "fix", "NEEDS-FIX", f))
		want := NodeRetry
		if i == 3 {
			want = Checkpoint // window full [A,B,A,B] → rotate escalation
		}
		if r.Kind != want {
			t.Fatalf("verdict %d (%v): expected %v, got %v", i+1, ids[i], want, r.Kind)
		}
	}
	// the escalation early-returns BEFORE emitting node_review_retry for the 4th:
	// exactly 3 retry events (A,B,A), not 4.
	retryCount := 0
	for _, e := range tr.events {
		if e["type"] == "node_review_retry" {
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
	fA := []Finding{{File: "a.go", Line: 1, Issue: "missed nil check"}}
	ids := []string{"h-001", "h-002", "h-003"}
	for i := range ids {
		if _, err := w.RecordHandoff(hf(ids[i])); err != nil {
			t.Fatal(err)
		}
		r, _ := w.RecordReviewVerdict(rv(ids[i], "fix", "NEEDS-FIX", fA))
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
	fA := []Finding{{File: "a.go", Line: 1, Issue: "missed nil check"}}
	fB := []Finding{{File: "b.go", Line: 2, Issue: "race on write"}}
	fC := []Finding{{File: "c.go", Line: 3, Issue: "unchecked error"}}
	ids := []string{"h-001", "h-002", "h-003", "h-004"}
	// A,B,A,C: only A reappears non-contiguously; B and C appear once each.
	// Spec: ≥2 distinct findings EACH reappear → only 1 qualifies → no escalate.
	for i, f := range [][]Finding{fA, fB, fA, fC} {
		if _, err := w.RecordHandoff(hf(ids[i])); err != nil {
			t.Fatal(err)
		}
		r, _ := w.RecordReviewVerdict(rv(ids[i], "fix", "NEEDS-FIX", f))
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
	fA := []Finding{{File: "a.go", Line: 1, Issue: "missed nil check"}}
	fB := []Finding{{File: "b.go", Line: 2, Issue: "race on write"}}
	ids := []string{"h-001", "h-002", "h-003", "h-004"}
	for i, f := range [][]Finding{fA, fB, fA, fB} {
		if _, err := w.RecordHandoff(hf(ids[i])); err != nil {
			t.Fatal(err)
		}
		r, _ := w.RecordReviewVerdict(rv(ids[i], "fix", "NEEDS-FIX", f))
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
	// h-002 fix→verify → RUN_REVIEW (⑤ reviews fix's ④)
	if _, err := w.RecordHandoff(h002()); err != nil {
		t.Fatal(err)
	}
	if _, err := w.RecordReviewVerdict(rv("h-002", "fix", "PASS", nil)); err != nil {
		t.Fatal(err)
	}

	// Same finding across all 3 cross-node NEEDS-FIX verdicts on verify (verify's
	// back-edge target is fix, ≠ verify). One verdict per envelope: verify
	// re-runs and hands off a NEW envelope each round (h-003, h-004, h-005).
	// findingKey hashes the whole sorted finding set, so identical sets share
	// the key across envelopes.
	same := []Finding{{File: "player/task/task.go", Line: 187, Issue: "unchecked deref"}}
	ids := []string{"h-003", "h-004", "h-005"}
	for i, id := range ids {
		if _, err := w.RecordHandoff(hv(id)); err != nil {
			t.Fatal(err)
		}
		r, err := w.RecordReviewVerdict(rv(id, "verify", "NEEDS-FIX", same))
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if i < 2 && r.Kind != BackEdgeKind {
			t.Fatalf("NEEDS-FIX #%d (%s): expected BackEdgeKind, got %v", i+1, id, r.Kind)
		}
		if i == 2 && r.Kind != Checkpoint {
			t.Fatalf("3rd NEEDS-FIX (%s): expected Checkpoint (3× escalation), got %v", id, r.Kind)
		}
	}

	// Exactly 2 handoff_reject events — the 3rd verdict escalates WITHOUT
	// emitting a 3rd handoff_reject (the >=3 early-return precedes the append).
	rejects := 0
	for _, e := range tr.events {
		if e["type"] == "handoff_reject" {
			rejects++
		}
	}
	if rejects != 2 {
		t.Fatalf("expected exactly 2 handoff_reject events, got %d — the 3rd verdict must escalate (Checkpoint) without emitting a 3rd reject", rejects)
	}
	// the finding counter did reach 3 (escalation key in state)
	if seen := w.state.FindingsSeen[findingKey(same)]; seen != 3 {
		t.Fatalf("expected FindingsSeen key count 3, got %d", seen)
	}
	// BackEdges incremented on ALL 3 cross-node verdicts (the counter bump runs
	// BEFORE the >=3 early-return), so the counter reaches 3 even though the
	// 3rd verdict escalates without emitting a handoff_reject. The 2-rejects
	// invariant is on the emitted handoff_reject events, NOT the counter.
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
	w.RecordHandoff(h002())                                     // fix→verify → RUN_REVIEW on fix
	w.RecordReviewVerdict(rv("h-002", "fix", "PASS", nil))      // → Checkpoint, advance to verify

	// DISTINCT findings each verdict → different findingKey each time →
	// FindingsSeen[key] stays 1, the 3× rule never fires (it needs the SAME
	// key 3×). Only the global BackEdges counter accumulates. One verdict per
	// envelope: h-003 then h-004, both produced by verify.
	f1 := []Finding{{File: "player/task/task.go", Line: 187, Issue: "deref unchecked"}}
	f2 := []Finding{{File: "player/task/task.go", Line: 53, Issue: "missing nil check"}}
	// 1st → BackEdges=1 < max(2) → BackEdgeKind + handoff_reject
	w.RecordHandoff(hv("h-003"))
	r, _ := w.RecordReviewVerdict(rv("h-003", "verify", "NEEDS-FIX", f1))
	if r.Kind != BackEdgeKind {
		t.Fatalf("1st NEEDS-FIX: expected BackEdgeKind (BackEdges=1 <2), got %v", r.Kind)
	}
	// 2nd → BackEdges=2 ≥ max(2) → Checkpoint (budget exhausted)
	w.RecordHandoff(hv("h-004"))
	r, _ = w.RecordReviewVerdict(rv("h-004", "verify", "NEEDS-FIX", f2))
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

// hv builds a handoff envelope whose PRODUCER is verify (fixture-faithful
// direction: verify hands off toward a downstream consumer like "review").
// Cross-node NEEDS-FIX verdicts on verify route via the verify→fix back-edge.
func hv(envID string) HandoffEnvelope {
	return HandoffEnvelope{
		EnvelopeID: envID, FromNode: "verify", ToNode: "review", Label: "跑测试套件",
		Artifacts:    []string{"player/task/task.go"},
		ExitCriteria: []ExitCriterion{{Name: "tests_pass", VerdictType: DECIDABLE}},
		FactualClaim: "all tests pass",
		AttemptHistory: []AttemptEntry{
			{Node: "diagnose", Attempts: 1, Verifier: "root_cause_pin"},
			{Node: "fix", Attempts: 2, Verifier: "build_clean"},
			{Node: "verify", Attempts: 1, Verifier: "tests_pass"},
		},
		BudgetRemaining: BudgetRemaining{9, 30000},
		Stripped:        []string{"producer_reasoning_trace", "producer_verdict_self_report"},
	}
}

// --- P0 review-loop hardening (TDD): runtime verdict gating ---

// TestRecordReviewVerdict_RejectsVerdictWithoutPendingReview: a verdict with
// no preceding handoff (state.PendingReview=="") must be rejected with an
// error, not silently routed. Production change that would break this:
// removing the PendingReview gate in RecordReviewVerdict.
func TestRecordReviewVerdict_RejectsVerdictWithoutPendingReview(t *testing.T) {
	withTempStateDir(t)
	g, err := ParseGraph([]byte(escalation3xGraphYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	w := NewWalker(g, "test-gate-nopending", &memTrace{})
	if _, err := w.RecordReviewVerdict(rv("h-001", "diagnose", "PASS", nil)); err == nil {
		t.Fatal("expected error for verdict with no pending review envelope, got nil")
	}
}

// TestRecordReviewVerdict_RejectsMismatchedEnvelope: a verdict whose
// envelope_id does not match the pending envelope must be rejected.
func TestRecordReviewVerdict_RejectsMismatchedEnvelope(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(escalation3xGraphYAML))
	w := NewWalker(g, "test-gate-mismatch", &memTrace{})
	if _, err := w.RecordHandoff(h001()); err != nil {
		t.Fatal(err)
	}
	if _, err := w.RecordReviewVerdict(rv("h-999", "diagnose", "PASS", nil)); err == nil {
		t.Fatal("expected error for verdict envelope_id != pending envelope, got nil")
	}
}

// TestRecordReviewVerdict_RejectsUnknownVerdictString: verdict must be
// exactly PASS or NEEDS-FIX; anything else is a protocol violation.
func TestRecordReviewVerdict_RejectsUnknownVerdictString(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(escalation3xGraphYAML))
	w := NewWalker(g, "test-gate-verdictstr", &memTrace{})
	if _, err := w.RecordHandoff(h001()); err != nil {
		t.Fatal(err)
	}
	if _, err := w.RecordReviewVerdict(rv("h-001", "diagnose", "MAYBE", nil)); err == nil {
		t.Fatal("expected error for verdict string outside {PASS, NEEDS-FIX}, got nil")
	}
}

// TestRecordReviewVerdict_RejectsEmptyEvidence: invariant #6 as a runtime
// gate — a verdict with empty evidence_tool_calls is a rubber stamp and must
// be rejected at record time, not only post-hoc by review-isolation-check.
func TestRecordReviewVerdict_RejectsEmptyEvidence(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(escalation3xGraphYAML))
	w := NewWalker(g, "test-gate-evidence", &memTrace{})
	if _, err := w.RecordHandoff(h001()); err != nil {
		t.Fatal(err)
	}
	v := rv("h-001", "diagnose", "PASS", nil)
	v.EvidenceToolCalls = nil
	if _, err := w.RecordReviewVerdict(v); err == nil {
		t.Fatal("expected error for verdict with empty evidence_tool_calls, got nil")
	}
}

// TestRecordReviewVerdict_RejectsNodeNotMatchingProducer: trace-protocol
// §2.2.1 — review_verdict.node is the node whose ④ was reviewed (the
// envelope producer). A verdict naming any other node is a routing hazard
// and must be rejected.
func TestRecordReviewVerdict_RejectsNodeNotMatchingProducer(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(escalation3xGraphYAML))
	w := NewWalker(g, "test-gate-node", &memTrace{})
	// h-001's producer is diagnose; a verdict claiming node "fix" is wrong.
	if _, err := w.RecordHandoff(h001()); err != nil {
		t.Fatal(err)
	}
	if _, err := w.RecordReviewVerdict(rv("h-001", "fix", "PASS", nil)); err == nil {
		t.Fatal("expected error for verdict node != envelope producer node, got nil")
	}
}

// TestRecordReviewVerdict_SecondVerdictSameEnvelopeRejected: one verdict per
// envelope. After a verdict is recorded (any kind), the pending slot is
// consumed; a second verdict for the same envelope_id must error.
func TestRecordReviewVerdict_SecondVerdictSameEnvelopeRejected(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(escalation3xGraphYAML))
	w := NewWalker(g, "test-gate-once", &memTrace{})
	if _, err := w.RecordHandoff(h001()); err != nil {
		t.Fatal(err)
	}
	if _, err := w.RecordReviewVerdict(rv("h-001", "diagnose", "PASS", nil)); err != nil {
		t.Fatalf("first (valid) verdict errored: %v", err)
	}
	if _, err := w.RecordReviewVerdict(rv("h-001", "diagnose", "NEEDS-FIX",
		[]Finding{{File: "a.go", Line: 1, Issue: "late"}})); err == nil {
		t.Fatal("expected error for second verdict on an already-consumed envelope, got nil")
	}
}

// TestRecordHandoff_RunReviewTargetsProducer: the RUN_REVIEW directive (and
// its review-scheduled node_turn echo) must name the PRODUCER node (the node
// whose ④ is under review), not the consumer — trace-protocol §2.2.1 and the
// C4 fixture (RUN_REVIEW envelope_id=h-001 scheduled on diagnose).
func TestRecordHandoff_RunReviewTargetsProducer(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(c4GraphYAML))
	tr := &memTrace{}
	w := NewWalker(g, "test-runreview-node", tr)
	d, err := w.RecordHandoff(h001())
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != RunReview || d.Node != "diagnose" {
		t.Fatalf("expected RUN_REVIEW node=diagnose (producer), got %+v", d)
	}
	var schedNode interface{}
	var found bool
	for _, e := range tr.events {
		if e["type"] == "node_turn" && e["step"] == "review-scheduled" {
			schedNode = e["node"]
			found = true
		}
	}
	if !found {
		t.Fatal("no review-scheduled node_turn emitted for h-001")
	}
	if schedNode != "diagnose" {
		t.Fatalf("review-scheduled node_turn node=%v, want diagnose (producer)", schedNode)
	}
}

// TestRecordHandoff_RejectsHandoffWhileReviewPending: a ④ handoff arriving
// while the previous envelope is still awaiting its ⑤ verdict would silently
// overwrite the pending slot (unreviewed handoff). It must be rejected.
func TestRecordHandoff_RejectsHandoffWhileReviewPending(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(escalation3xGraphYAML))
	w := NewWalker(g, "test-handoff-gate", &memTrace{})
	if _, err := w.RecordHandoff(h001()); err != nil {
		t.Fatal(err)
	}
	if _, err := w.RecordHandoff(hv("h-003")); err == nil {
		t.Fatal("expected error for handoff while envelope h-001 is still awaiting review, got nil")
	}
}

// --- P0 wave 2: adversarial-review fixes (gate symmetry + escape hatch) ---

// h000 is the walker-synthesized bootstrap envelope (B5): phase0 → first node.
func h000() HandoffEnvelope {
	return HandoffEnvelope{
		EnvelopeID: "h-000", FromNode: "phase0", ToNode: "diagnose", Label: "记忆检索",
		Artifacts: []string{"user-input.txt"}, FactualClaim: "bootstrap",
		BudgetRemaining: BudgetRemaining{18, 42000},
		Stripped:        []string{"producer_reasoning_trace", "producer_verdict_self_report"},
	}
}

// TestRecordHandoff_RejectsEmptyEnvelopeID: an empty envelope_id would set
// PendingReview="" — silently disabling BOTH gates (the verdict for it can
// never match, and the next handoff sails through unreviewed).
func TestRecordHandoff_RejectsEmptyEnvelopeID(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(escalation3xGraphYAML))
	w := NewWalker(g, "test-h-empty-id", &memTrace{})
	env := h001()
	env.EnvelopeID = ""
	if _, err := w.RecordHandoff(env); err == nil {
		t.Fatal("expected error for handoff with empty envelope_id, got nil")
	}
}

// TestRecordHandoff_RejectsEmptyFromNode: an empty producer node would leave
// PendingReviewNode="" and skip the producer-match verdict gate entirely.
func TestRecordHandoff_RejectsEmptyFromNode(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(escalation3xGraphYAML))
	w := NewWalker(g, "test-h-empty-from", &memTrace{})
	env := h001()
	env.FromNode = ""
	if _, err := w.RecordHandoff(env); err == nil {
		t.Fatal("expected error for handoff with empty from_node, got nil")
	}
}

// TestRecordHandoff_BootstrapH000_NoReviewNoDeadlock: h-000 is the bootstrap
// envelope — it legitimately has NO ⑤ verdict (trace-protocol reserves it;
// the C4 fixture has no review_verdict for h-000). Recording it must NOT
// schedule a review or hold the pending slot (which would deadlock every
// later handoff); it routes straight to the first node. Re-submitting h-000
// is rejected (envelope ids are single-use — this also closes the free
// budget-evading h-000 spam loop).
func TestRecordHandoff_BootstrapH000_NoReviewNoDeadlock(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(escalation3xGraphYAML))
	tr := &memTrace{}
	w := NewWalker(g, "test-h-bootstrap", tr)
	d, err := w.RecordHandoff(h000())
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != RunNode || d.Node != "diagnose" {
		t.Fatalf("expected h-000 to route RUN_NODE diagnose (no ⑤ for bootstrap), got %+v", d)
	}
	// no deadlock: the next handoff is accepted
	if _, err := w.RecordHandoff(h001()); err != nil {
		t.Fatalf("h-001 after h-000 must be accepted, got %v", err)
	}
	// no review-scheduled node_turn for h-000 (h-001's own scheduling is legit)
	for _, e := range tr.events {
		if e["type"] == "node_turn" && e["step"] == "review-scheduled" &&
			fmt.Sprint(e["walker_directive"]) == "RUN_REVIEW envelope_id=h-000" {
			t.Fatalf("bootstrap h-000 must not schedule a review, got %v", e)
		}
	}
	// single-use: re-submitting h-000 is rejected
	if _, err := w.RecordHandoff(h000()); err == nil {
		t.Fatal("expected error for re-used envelope id h-000, got nil")
	}
}

// TestRecordHandoff_RejectsReusedEnvelopeID: envelope ids are single-use —
// a consumed id re-handed-off would mint a second handoff+verdict pair in
// the trace for the same envelope_id.
func TestRecordHandoff_RejectsReusedEnvelopeID(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(escalation3xGraphYAML))
	w := NewWalker(g, "test-h-reuse", &memTrace{})
	if _, err := w.RecordHandoff(h001()); err != nil {
		t.Fatal(err)
	}
	if _, err := w.RecordReviewVerdict(rv("h-001", "diagnose", "PASS", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.RecordHandoff(h001()); err == nil {
		t.Fatal("expected error for re-using consumed envelope id h-001, got nil")
	}
}

// TestAbandonPendingReview_CheckpointsAndClears: an interrupted/abandoned ⑤
// must have an escape hatch — abandon emits a review_abandoned trace event,
// clears the pending slot, and routes CHECKPOINT (human decides; continuing
// re-runs the producer node and mints a FRESH envelope).
func TestAbandonPendingReview_CheckpointsAndClears(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(escalation3xGraphYAML))
	tr := &memTrace{}
	w := NewWalker(g, "test-abandon", tr)
	if _, err := w.RecordHandoff(h001()); err != nil {
		t.Fatal(err)
	}
	d, err := w.AbandonPendingReview("session interrupted mid-review")
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != Checkpoint || d.Node != "diagnose" {
		t.Fatalf("expected Checkpoint on producer node, got %+v", d)
	}
	// trace carries the abandon event
	var abandoned bool
	for _, e := range tr.events {
		if e["type"] == "review_abandoned" && e["envelope_id"] == "h-001" {
			abandoned = true
		}
	}
	if !abandoned {
		t.Fatal("expected review_abandoned event for h-001")
	}
	// abandoning with nothing pending is an error (checked BEFORE the next handoff)
	if _, err := w.AbandonPendingReview("nothing"); err == nil {
		t.Fatal("expected error for abandon with no pending review, got nil")
	}
	// pending cleared: a new handoff (fresh id) is accepted
	if _, err := w.RecordHandoff(hv("h-003")); err != nil {
		t.Fatalf("handoff after abandon must be accepted, got %v", err)
	}
	// a verdict for the abandoned envelope no longer matches anything
	if _, err := w.RecordReviewVerdict(rv("h-001", "diagnose", "PASS", nil)); err == nil {
		t.Fatal("expected error for verdict on abandoned envelope, got nil")
	}
}

// TestRecordReviewVerdict_RejectionLeavesStateAndTraceUntouched: the gate's
// zero-side-effect promise — a rejected verdict appends NO trace event,
// leaves the pending envelope in place, and the next VALID verdict records.
func TestRecordReviewVerdict_RejectionLeavesStateAndTraceUntouched(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(escalation3xGraphYAML))
	tr := &memTrace{}
	w := NewWalker(g, "test-gate-pure", tr)
	if _, err := w.RecordHandoff(h001()); err != nil {
		t.Fatal(err)
	}
	eventsBefore := len(tr.events)
	if _, err := w.RecordReviewVerdict(rv("h-999", "diagnose", "PASS", nil)); err == nil {
		t.Fatal("expected rejection for mismatched envelope")
	}
	if len(tr.events) != eventsBefore {
		t.Fatalf("rejection appended %d trace events (must be zero)", len(tr.events)-eventsBefore)
	}
	if _, err := w.RecordReviewVerdict(rv("h-001", "diagnose", "PASS", nil)); err != nil {
		t.Fatalf("valid verdict after a rejection must still record, got %v", err)
	}
}

// TestRecordReviewVerdict_EmptyFindingsNotInAlternatingWindow: same-node
// empty-finding NEEDS-FIX verdicts are degenerate and must not feed the
// rotation window (the cross-node counterpart is covered by
// EmptyFindingsDoNotCountToward3x).
func TestRecordReviewVerdict_EmptyFindingsNotInAlternatingWindow(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(alternatingGraphYAML))
	w := NewWalker(g, "test-empty-window", &memTrace{})
	ids := []string{"h-001", "h-002", "h-003", "h-004"}
	for _, id := range ids {
		if _, err := w.RecordHandoff(hf(id)); err != nil {
			t.Fatal(err)
		}
		r, err := w.RecordReviewVerdict(rv(id, "fix", "NEEDS-FIX", nil))
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if r.Kind != NodeRetry {
			t.Fatalf("%s: expected NodeRetry (empty findings never escalate), got %v", id, r.Kind)
		}
	}
	if win := w.state.Windows["fix"]; len(win) != 0 {
		t.Fatalf("empty-finding verdicts must not feed the window, got %v", win)
	}
}

// --- P0 review-loop hardening (TDD): findingKey ---

// TestFindingKey_CoversAllFindings_OrderInsensitive: the 3×/window dedup key
// must hash the WHOLE finding set (sorted), so (1) reordering findings does
// not mint a new key, and (2) two genuinely different sets get different
// keys. The old fs[0]-only key let a shuffled first element dodge the 3×
// rule and let different issues at one file:line collide.
func TestFindingKey_CoversAllFindings_OrderInsensitive(t *testing.T) {
	a := []Finding{{File: "a.go", Line: 1, Issue: "nil"}, {File: "b.go", Line: 2, Issue: "race"}}
	b := []Finding{{File: "b.go", Line: 2, Issue: "race"}, {File: "a.go", Line: 1, Issue: "nil"}}
	if findingKey(a) != findingKey(b) {
		t.Fatalf("same finding set in different order must share a key: %q vs %q", findingKey(a), findingKey(b))
	}
	c := []Finding{{File: "a.go", Line: 1, Issue: "nil"}}
	if findingKey(a) == findingKey(c) {
		t.Fatal("subset finding set must not collide with the full set's key")
	}
	// same file:line, DIFFERENT issue at fs[0] — old key collided these.
	d1 := []Finding{{File: "a.go", Line: 1, Issue: "nil deref"}, {File: "z.go", Line: 9, Issue: "other"}}
	d2 := []Finding{{File: "a.go", Line: 1, Issue: "unbounded loop"}, {File: "z.go", Line: 9, Issue: "other"}}
	if findingKey(d1) == findingKey(d2) {
		t.Fatal("different issues at the same fs[0] file:line must not share a key")
	}
}

// TestFindingKey_EmptyReturnsEmpty: no findings → "" (callers treat "" as
// "do not count toward 3× / do not append to the window").
func TestFindingKey_EmptyReturnsEmpty(t *testing.T) {
	if findingKey(nil) != "" || findingKey([]Finding{}) != "" {
		t.Fatal("empty finding set must key to \"\"")
	}
}

// TestRecordReviewVerdict_EmptyFindingsDoNotCountToward3x: cross-node
// NEEDS-FIX verdicts with NO findings are degenerate — they must route as
// back-edges (bounded by MaxBackEdgesTotal) but never accumulate a FindingsSeen
// entry, so an empty-finding stream cannot mint a phantom "" 3× escalation.
func TestRecordReviewVerdict_EmptyFindingsDoNotCountToward3x(t *testing.T) {
	withTempStateDir(t)
	g, err := ParseGraph([]byte(escalation3xGraphYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tr := &memTrace{}
	w := NewWalker(g, "test-empty-3x", tr)
	w.Next()
	if _, err := w.RecordHandoff(h001()); err != nil {
		t.Fatal(err)
	}
	if _, err := w.RecordReviewVerdict(rv("h-001", "diagnose", "PASS", nil)); err != nil {
		t.Fatal(err)
	}
	// three cross-node empty-finding verdicts, each on its OWN envelope
	// (one verdict per envelope): h-002, h-003, h-004 produced by verify.
	for _, id := range []string{"h-002", "h-003", "h-004"} {
		if _, err := w.RecordHandoff(hv(id)); err != nil {
			t.Fatal(err)
		}
		r, err := w.RecordReviewVerdict(rv(id, "verify", "NEEDS-FIX", nil))
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if r.Kind != BackEdgeKind {
			t.Fatalf("%s: expected BackEdgeKind (empty findings must not escalate), got %v", id, r.Kind)
		}
	}
	if n := len(w.state.FindingsSeen); n != 0 {
		t.Fatalf("empty-finding verdicts must leave FindingsSeen empty, got %+v", w.state.FindingsSeen)
	}
	if w.state.BackEdges != 3 {
		t.Fatalf("expected BackEdges=3, got %d", w.state.BackEdges)
	}
}
