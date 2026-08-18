package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnson-xue/summoner/internal/graph"
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

// TestCLI_Record_ReviewVerdict_NeedsFix_DoesNotPanic proves Fix 1: a
// `record --step review_verdict --verdict NEEDS-FIX` against a real node must
// not SIGSEGV. Pre-Fix-1(a) the CLI passed Node:"" and RecordReviewVerdict
// nil-derefed NodeByID's nil return. Now --node supplies the id and the walker
// guards the unknown case. We exercise a known node (fix) so the NEEDS-FIX path
// routes through the same-node retry logic rather than the unknown-node guard;
// the goal is to prove the binary does not crash on a NEEDS-FIX verdict.
func TestCLI_Record_ReviewVerdict_NeedsFix_DoesNotPanic(t *testing.T) {
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
  - id: fix
    label: "补 nil 判空"
    skill: antia-logic
    exit_criteria: [{name: diff_applied, verdict_type: DECIDABLE}]
    max_inner_turns: 2
    mutating: true
edges:
  - {from: diagnose, to: fix}
back_edges:
  - {from: fix, to: fix, reason: review_needs_fix}
`), 0o644)

	// First bootstrap a handoff so the walker has a pending review for the fix
	// node. We write a minimal handoff envelope and record it.
	envelopePath := filepath.Join(t.TempDir(), "h-001.json")
	os.WriteFile(envelopePath, []byte(`{
  "envelope_id": "h-001",
  "from_node": "diagnose",
  "to_node": "fix",
  "label": "补 nil 判空",
  "artifacts": ["player/task/task.go"],
  "exit_criteria": [{"name": "diff_applied", "verdict_type": "DECIDABLE"}],
  "factual_claim": "nil-guard added",
  "budget_remaining": {"graph_turns_left": 19, "token_budget_left": 48000},
  "stripped": ["producer_reasoning_trace"]
}`), 0o644)

	session := t.Name()
	// record the handoff (seeds pending review); ignore output, tolerate non-zero
	// exit only if it is not a panic — the handoff record should succeed.
	handoffCmd := exec.Command(bin, "--graph", graphPath, "--session", session,
		"record", "--step", "handoff", "--envelope", envelopePath)
	handoffCmd.Stderr = os.Stderr
	if out, err := handoffCmd.Output(); err != nil {
		// If the handoff record failed for an environmental reason, the NEEDS-FIX
		// record below is the real panic-prevention assertion; but we still want
		// to surface the failure. Fatal only on a signal-style crash.
		if strings.Contains(string(out), "signal") {
			t.Fatalf("handoff record crashed: %v %s", err, out)
		}
		t.Logf("handoff record returned err=%v (continuing to NEEDS-FIX record); out=%s", err, out)
	}

	// The core assertion: record a NEEDS-FIX review verdict. Must not SIGSEGV.
	verdictCmd := exec.Command(bin, "--graph", graphPath, "--session", session,
		"record", "--step", "review_verdict",
		"--envelope_id", "h-001", "--node", "fix", "--verdict", "NEEDS-FIX")
	out, err := verdictCmd.Output()
	if err != nil {
		// A panic surfaces as a non-zero exit; capture stderr to distinguish a
		// panic from a clean cobra usage error.
		t.Fatalf("record review_verdict NEEDS-FIX failed (expected no panic): %v\nstdout=%s\nstderr=%s",
			err, out, verdictCmd.Stderr)
	}
	s := string(out)
	// Should print a JSON directive with kind NODE_RETRY (fix has max_inner_turns
	// 2 and attempt starts at 1) or CHECKPOINT — either is acceptable; the
	// invariant is "did not panic".
	if !strings.Contains(s, "NODE_RETRY") && !strings.Contains(s, "CHECKPOINT") {
		t.Fatalf("expected NODE_RETRY or CHECKPOINT directive, got %s", s)
	}
}

// TestRecordHandoff_ExitCriteriaJsonRoundTrip proves Fix 2: ExitCriterion's
// verdict_type and grep_pattern survive a JSON unmarshal→marshal round-trip.
// Pre-Fix-2 ExitCriterion had only yaml tags, so encoding/json dropped
// verdict_type (underscore breaks case-insensitive matching) and the field
// zeroed — which would have broken the Task-7 scorers that jq over
// .exit_criteria[].verdict_type. This test asserts BOTH the decoded struct
// field AND the re-marshaled JSON key name.
func TestRecordHandoff_ExitCriteriaJsonRoundTrip(t *testing.T) {
	raw := []byte(`{"exit_criteria":[{"name":"rc","verdict_type":"SOFT","pin":"x","grep_pattern":"player.SubTask"}]}`)
	var env graph.HandoffEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.ExitCriteria) != 1 {
		t.Fatalf("expected 1 exit criterion, got %d", len(env.ExitCriteria))
	}
	c := env.ExitCriteria[0]
	if c.VerdictType != graph.SOFT {
		t.Fatalf("expected VerdictType SOFT, got %q", c.VerdictType)
	}
	if c.GrepPattern != "player.SubTask" {
		t.Fatalf("expected GrepPattern player.SubTask, got %q", c.GrepPattern)
	}
	// Re-marshal and assert the JSON key is the underscored form (verdict_type),
	// not the Go field name (VerdictType) — this is the core Fix-2 assertion.
	out, err := json.Marshal(env.ExitCriteria)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, `"verdict_type":"SOFT"`) {
		t.Fatalf("re-marshaled JSON must use key \"verdict_type\" (got: %s)", s)
	}
	if strings.Contains(s, `"VerdictType"`) {
		t.Fatalf("re-marshaled JSON must NOT use Go field name VerdictType (got: %s)", s)
	}
	if !strings.Contains(s, `"grep_pattern":"player.SubTask"`) {
		t.Fatalf("re-marshaled JSON must use key \"grep_pattern\" (got: %s)", s)
	}
}
