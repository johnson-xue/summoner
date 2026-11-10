package shared

import (
	"reflect"
	"testing"
)

// workflow_test.go pins the fixed workflow phase sequences (Task 3 ruling 3:
// phase opening is sequence-driven — the gate cannot know project skill names
// until manifest v2, so the framework owns the order and the gate derives the
// next expected phase from it). T16 adds the four remaining verbs: the seven
// slash commands the run starter already recognizes all get framework routes.

func TestWorkflowPhasesTable(t *testing.T) {
	want := map[string][]string{
		"fix":     {"context", "diagnosis", "reproduce", "fix", "regression", "review"},
		"new":     {"context", "requirements", "plan", "implement", "verify", "review"},
		"debug":   {"context", "diagnosis"},
		"review":  {"context", "review"},
		"ship":    {"prepare", "review", "merge", "decide"},
		"ops":     {"context", "operate"},
		"release": {"planning", "changelog", "publish"},
	}
	for wf, seq := range want {
		got, err := WorkflowPhases(wf)
		if err != nil {
			t.Fatalf("WorkflowPhases(%q): %v", wf, err)
		}
		if !reflect.DeepEqual(got, seq) {
			t.Errorf("WorkflowPhases(%q) = %v, want %v", wf, got, seq)
		}
	}
}

func TestWorkflowPhasesUnknownWorkflow(t *testing.T) {
	for _, wf := range []string{"", "custom", "refix", "FIX", "Review", "releases"} {
		if got, err := WorkflowPhases(wf); err == nil {
			t.Errorf("WorkflowPhases(%q) = %v, want error", wf, got)
		}
	}
}

// TestWorkflowPhasesReturnsCopy: callers mutate the returned slice at their
// peril — the table itself must never change.
func TestWorkflowPhasesReturnsCopy(t *testing.T) {
	got, err := WorkflowPhases("fix")
	if err != nil {
		t.Fatalf("WorkflowPhases(fix): %v", err)
	}
	got[0] = "mutated"
	again, err := WorkflowPhases("fix")
	if err != nil {
		t.Fatalf("WorkflowPhases(fix): %v", err)
	}
	if again[0] != "context" {
		t.Fatalf("mutating the returned slice changed the table: %v", again)
	}
}
