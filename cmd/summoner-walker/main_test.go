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
