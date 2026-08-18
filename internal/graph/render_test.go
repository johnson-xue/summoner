package graph

import (
	"strings"
	"testing"
)

func TestExplain_ShowsLabelNotId(t *testing.T) {
	// A5: isolate walk-state to a temp dir so a stale test-render.json from a
	// prior run/machine cannot make LoadState return non-empty state (which
	// would skip the bootstrap and render a stale route → flaky).
	withTempStateDir(t)
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
	withTempStateDir(t)
	g, _ := ParseGraph([]byte(c4GraphYAML))
	tr := &memTrace{}
	w := NewWalker(g, "test-render", tr)
	out := w.Status()
	if !strings.Contains(out, "node=") || !strings.Contains(out, "graph_turns=") {
		t.Fatalf("status should show raw machine state, got: %s", out)
	}
}
