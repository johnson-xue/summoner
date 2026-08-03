package graph

import (
	"io/ioutil"
	"strings"
	"testing"
)

// io_ReadAll is a test-helper alias for io/ioutil.ReadAll of a reader.
func io_ReadAll(r interface{ Read(p []byte) (int, error) }) ([]byte, error) {
	return ioutil.ReadAll(r)
}

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
