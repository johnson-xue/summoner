package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A5 RouteMap write-point tests. The design (winner of the 4-lens
// adversarial panel, 工程债优先) writes RouteMap at: NewWalker bootstrap
// (setRoute current), RecordReviewVerdict PASS (v.Node→pass, next→current),
// NEEDS-FIX preamble (v.Node→needs_fix), and cross-node advance
// (backTarget→current, NO sweep so v.Node stays needs_fix). The headline
// invariant the panel caught: a cross-node back-edge must NOT demote the
// failing node to "pass" — render shows ✗ on verify, ▶ on fix (revisiting).

// routeStatus returns the Status of the route entry for nodeID, or "" if absent.
func routeStatus(w *Walker, nodeID string) string {
	for _, re := range w.state.RouteMap {
		if re.Node == nodeID {
			return re.Status
		}
	}
	return ""
}

// countStatus returns how many route entries carry status.
func countStatus(w *Walker, status string) int {
	n := 0
	for _, re := range w.state.RouteMap {
		if re.Status == status {
			n++
		}
	}
	return n
}

const a5DiagnoseFixVerifyYAML = `
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
    skill: verify.test
    exit_criteria:
      - {name: tests_pass, verdict_type: DECIDABLE}
    max_inner_turns: 2
    mutating: false
edges:
  - {from: diagnose, to: fix}
  - {from: fix, to: verify}
back_edges:
  - {from: verify, to: fix, reason: verifier_failed}
`

// TestA5_BootstrapSeedsCurrent: NewWalker alone (fresh session) seeds the
// first node as "current" so Explain renders a REAL route (▶定位根因), not
// the len==0 fallback. Proves the "always-fallback" lie (A5) is fixed.
func TestA5_BootstrapSeedsCurrent(t *testing.T) {
	withTempStateDir(t)
	g, err := ParseGraph([]byte(a5DiagnoseFixVerifyYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	w := NewWalker(g, "a5-bootstrap", &memTrace{})
	if got := routeStatus(w, "diagnose"); got != "current" {
		t.Fatalf("bootstrap: diagnose status=%q, want %q", got, "current")
	}
	if countStatus(w, "current") != 1 {
		t.Fatalf("bootstrap: want exactly 1 current, got %d", countStatus(w, "current"))
	}
	out := w.Explain()
	if !strings.Contains(out, "定位根因") {
		t.Fatalf("explain should contain label 定位根因, got: %s", out)
	}
	if !strings.Contains(out, "▶") {
		t.Fatalf("explain should mark current node with ▶, got: %s", out)
	}
	if strings.Contains(out, "RUN_NODE") || strings.Contains(out, "①") {
		t.Fatalf("explain must hide machine internals, got: %s", out)
	}
}

// TestA5_PassAdvancesCurrent: handoff + PASS advances the current marker
// from the passed node to the next. diagnose→pass, fix→current.
func TestA5_PassAdvancesCurrent(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(a5DiagnoseFixVerifyYAML))
	w := NewWalker(g, "a5-pass", &memTrace{})
	w.Next() // bootstrap → diagnose
	w.RecordHandoff(h001()) // diagnose→fix, schedules review on diagnose
	w.RecordReviewVerdict(rv("h-001", "diagnose", "PASS", nil)) // → Checkpoint, advance to fix
	if got := routeStatus(w, "diagnose"); got != "pass" {
		t.Fatalf("after PASS: diagnose status=%q, want %q", got, "pass")
	}
	if got := routeStatus(w, "fix"); got != "current" {
		t.Fatalf("after PASS: fix status=%q, want %q", got, "current")
	}
	if countStatus(w, "current") != 1 {
		t.Fatalf("after PASS: want exactly 1 current, got %d", countStatus(w, "current"))
	}
}

// TestA5_CrossNodeBackEdge_FailingNodeStaysNeedsFix is the HEADLINE invariant
// the adversarial panel caught: a cross-node back-edge (verify fails → back to
// fix) must NOT demote the failing node (verify) to "pass". The earlier
// auto-sweep design rendered a ✗ as a ✓ — a lie. The no-sweep setRoute keeps
// verify="needs_fix" and marks fix="current". Explain output must show ✗ on
// the failing node and ▶ on the revisited node.
func TestA5_CrossNodeBackEdge_FailingNodeStaysNeedsFix(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(a5DiagnoseFixVerifyYAML))
	w := NewWalker(g, "a5-backedge", &memTrace{})
	w.Next()                                          // bootstrap → diagnose
	w.RecordHandoff(h001())                           // diagnose→fix
	w.RecordReviewVerdict(rv("h-001", "diagnose", "PASS", nil)) // → fix
	w.RecordHandoff(h002())                           // fix→verify, schedules review on verify
	// verify NEEDS-FIX with a finding → cross-node back-edge to fix.
	finding := []Finding{{File: "player/task/task.go", Line: 187, Issue: "deref"}}
	d, _ := w.RecordReviewVerdict(rv("h-002", "verify", "NEEDS-FIX", finding))
	if d.Kind != BackEdgeKind {
		t.Fatalf("expected BackEdgeKind, got %v", d.Kind)
	}
	// The headline invariant: verify stays "needs_fix", NOT demoted to "pass".
	if got := routeStatus(w, "verify"); got != "needs_fix" {
		t.Fatalf("HEADLINE INVARIANT BROKEN: verify status=%q after cross-node back-edge, want needs_fix (auto-sweep regression would make it pass)", got)
	}
	if got := routeStatus(w, "fix"); got != "current" {
		t.Fatalf("fix (back-edge target) status=%q, want current", got)
	}
	if got := routeStatus(w, "diagnose"); got != "pass" {
		t.Fatalf("diagnose status=%q, want pass", got)
	}
	if countStatus(w, "current") != 1 {
		t.Fatalf("want exactly 1 current, got %d", countStatus(w, "current"))
	}
	// Explain must surface the failure mark AND the revisit mark.
	out := w.Explain()
	if !strings.Contains(out, "✗") {
		t.Fatalf("explain should show ✗ for the failed verify node, got: %s", out)
	}
	if !strings.Contains(out, "跑测试套件") {
		t.Fatalf("explain should contain verify label 跑测试套件, got: %s", out)
	}
}

// TestA5_3xEscalation_MarksFailedNode: the NEEDS-FIX preamble runs before
// the 3× early-return, so a checkpoint reached via 3× escalation shows the
// failing node as needs_fix (✗), not as current (▶) — honest "where we got
// stuck".
func TestA5_3xEscalation_MarksFailedNode(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(escalation3xGraphYAML))
	w := NewWalker(g, "a5-3x", &memTrace{})
	w.Next()
	w.RecordHandoff(h001())
	w.RecordReviewVerdict(rv("h-001", "diagnose", "PASS", nil)) // → fix
	w.RecordHandoff(h002()) // fix→verify, review on verify
	same := []Finding{{File: "player/task/task.go", Line: 187, Issue: "unchecked deref"}}
	for i := 0; i < 3; i++ {
		d, _ := w.RecordReviewVerdict(rv("h-002", "verify", "NEEDS-FIX", same))
		if i < 2 && d.Kind != BackEdgeKind {
			t.Fatalf("verdict %d: expected BackEdgeKind, got %v", i+1, d.Kind)
		}
		if i == 2 && d.Kind != Checkpoint {
			t.Fatalf("3rd verdict: expected Checkpoint (3× escalation), got %v", d.Kind)
		}
	}
	if got := routeStatus(w, "verify"); got != "needs_fix" {
		t.Fatalf("after 3× escalation: verify status=%q, want needs_fix", got)
	}
	if countStatus(w, "current") != 1 {
		t.Fatalf("after 3× escalation: want exactly 1 current (fix), got %d", countStatus(w, "current"))
	}
}

// TestA5_ReconcileRoute_HealsDanglingCurrentOnLoad: simulate a crash that
// left a dangling "current" on a node that is NOT CurrentNode, plus a stale
// entry for a removed node. LoadState → NewWalker.reconcileRoute must heal:
// the CurrentNode's entry forced "current", the dangling current → "pass",
// and the removed-node entry pruned.
func TestA5_ReconcileRoute_HealsDanglingCurrentOnLoad(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(a5DiagnoseFixVerifyYAML))
	tr := &memTrace{}
	// First walker: get to a known state (diagnose=pass, fix=current).
	w := NewWalker(g, "a5-reconcile", tr)
	w.Next()
	w.RecordHandoff(h001())
	w.RecordReviewVerdict(rv("h-001", "diagnose", "PASS", nil)) // fix=current, diagnose=pass
	// Now corrupt the persisted file: add a dangling "current" on verify
	// (which is NOT CurrentNode=fix) and a stale entry for a removed node.
	w.state.RouteMap = append(w.state.RouteMap,
		routeEntry{Node: "verify", Label: "跑测试套件", Status: "current"}, // dangling current
		routeEntry{Node: "gone_node", Label: "ghost", Status: "pass"},    // stale (not in graph)
	)
	if err := w.state.Save(); err != nil {
		t.Fatalf("seed corrupt state: %v", err)
	}
	// Reload via a fresh NewWalker: reconcileRoute should heal.
	w2 := NewWalker(g, "a5-reconcile", tr)
	// fix (== CurrentNode) forced current; dangling verify current → pass; gone_node pruned.
	if got := routeStatus(w2, "fix"); got != "current" {
		t.Fatalf("reconcile: fix (CurrentNode) status=%q, want current", got)
	}
	if got := routeStatus(w2, "verify"); got != "pass" {
		t.Fatalf("reconcile: dangling current on verify should be healed to pass, got %q", got)
	}
	if got := routeStatus(w2, "gone_node"); got != "" {
		t.Fatalf("reconcile: stale node gone_node should be pruned, got status %q", got)
	}
	if countStatus(w2, "current") != 1 {
		t.Fatalf("reconcile: want exactly 1 current after heal, got %d", countStatus(w2, "current"))
	}
}

// TestA5_RouteMapBoundedByNodeCount: a long walk (many retries/back-edges)
// must not grow RouteMap unboundedly — dedup-by-Node bounds it to len(nodes).
func TestA5_RouteMapBoundedByNodeCount(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(escalation3xGraphYAML)) // 3 nodes
	w := NewWalker(g, "a5-bounded", &memTrace{})
	w.Next()
	w.RecordHandoff(h001())
	w.RecordReviewVerdict(rv("h-001", "diagnose", "PASS", nil))
	w.RecordHandoff(h002())
	// Fire several cross-node back-edges (verify fails → fix) with distinct
	// findings so the 3× rule stays inert. RouteMap should stay ≤3 entries.
	f1 := []Finding{{File: "a.go", Line: 1, Issue: "x"}}
	f2 := []Finding{{File: "b.go", Line: 2, Issue: "y"}}
	for i := 0; i < 4; i++ {
		f := f1
		if i%2 == 1 {
			f = f2
		}
		w.RecordReviewVerdict(rv("h-002", "verify", "NEEDS-FIX", f))
	}
	if len(w.state.RouteMap) > len(g.Nodes) {
		t.Fatalf("RouteMap unbounded: len=%d > node count %d (dedup-by-Node regression)", len(w.state.RouteMap), len(g.Nodes))
	}
}

// TestA5_SameNodeRetry_KeepsCurrentMarker (S8 fix): the NEEDS-FIX preamble
// marks v.Node "needs_fix", but a same-node RETRY (self-loop back-edge where
// backTarget==v.Node==CurrentNode) means the node is still ACTIVE — it must be
// re-promoted to "current" so the route renders ▶ (not ✗ with zero current).
// The S8 review caught this render regression: pre-fix, a fix→fix self-loop
// retry showed "✓定位根因→✗补 nil 判空" with NO ▶ anywhere, making the
// actively-retried node indistinguishable from an escalated-to-checkpoint node.
// The failure is still conveyed by the "(第 N 次尝试)" header suffix; the route
// shows WHERE the walk is (still on fix). The escalation early-returns
// (max_inner_turns, alternating) keep "needs_fix" (✗, walk stopped) — covered
// by TestA5_3xEscalation_MarksFailedNode.
func TestA5_SameNodeRetry_KeepsCurrentMarker(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(c4GraphYAML)) // has back_edges: fix→fix (self-loop)
	w := NewWalker(g, "a5-samenode", &memTrace{})
	w.Next()                                                // bootstrap → diagnose=current
	w.RecordHandoff(h001())                                 // diagnose→fix
	w.RecordReviewVerdict(rv("h-001", "diagnose", "PASS", nil)) // diagnose→pass, fix→current
	// fix NEEDS-FIX → same-node retry (backTarget==fix==v.Node, max_inner_turns=4 not exhausted).
	d, _ := w.RecordReviewVerdict(rv("h-002", "fix", "NEEDS-FIX", []Finding{{File: "task.go", Line: 1, Issue: "x"}}))
	if d.Kind != NodeRetry {
		t.Fatalf("expected NodeRetry (same-node), got %v", d.Kind)
	}
	// The fix: the retried node stays "current" (▶), NOT demoted to needs_fix.
	if got := routeStatus(w, "fix"); got != "current" {
		t.Fatalf("S8 REGRESSION: same-node retry demoted fix to %q, want current — the ▶ marker must survive an active retry", got)
	}
	if got := routeStatus(w, "diagnose"); got != "pass" {
		t.Fatalf("diagnose status=%q, want pass", got)
	}
	if countStatus(w, "current") != 1 {
		t.Fatalf("want exactly 1 current (the retried fix node), got %d", countStatus(w, "current"))
	}
	// Explain must still surface a ▶ for the active node.
	out := w.Explain()
	if !strings.Contains(out, "▶") {
		t.Fatalf("explain should show ▶ for the actively-retried fix node, got: %s", out)
	}
	if !strings.Contains(out, "补 nil 判空") {
		t.Fatalf("explain should contain fix label 补 nil 判空, got: %s", out)
	}
}

// TestA5_RenderIsolationNoLeak: the two render tests must use withTempStateDir
// so a stale walk-state file from a prior run cannot make Explain nondeterministic.
// This confirms the no-Save design invariant (S8 fix): NewWalker's bootstrap path
// must NOT persist state — the first transition's saveStateOrLog persists RouteMap.
// Asserting NewWalker alone writes NO state file is falsifiable: if a future
// change adds a Save to the bootstrap, this test fails.
func TestA5_RenderIsolationNoLeak(t *testing.T) {
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(c4GraphYAML))
	w := NewWalker(g, "test-render", &memTrace{})
	_ = w.Explain()
	// Invariant: NewWalker did NOT Save. After NewWalker + Explain (no
	// RecordHandoff/RecordReviewVerdict), NO state file should exist yet —
	// the bootstrap setRoute is in-memory only. If a regression adds a Save
	// to the bootstrap path, this file appears and the test fails.
	if _, err := os.Stat(filepath.Join(stateDirOverride, "test-render.json")); err == nil {
		t.Fatalf("NewWalker leaked a state file — bootstrap must NOT Save (in-memory only); found test-render.json in state dir")
	}
}
