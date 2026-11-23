package shared

// startlimits_test.go — T18 (0.6.1) run-start budget resolution: budgets are
// manifest-authored. No manifest or no suggested_budgets → unlimited; a
// declared suggestion applies verbatim; an unreadable manifest fails CLOSED
// (a run never starts on a guessed budget).

import (
	"strings"
	"testing"
)

const startLimitsManifestHead = `version: "2"
project:
  name: budget-probe
phases:
  diagnosis:
    skill: none
`

func TestResolveStartLimits(t *testing.T) {
	// Absent manifest → unlimited, no error.
	setProjectDirForManifest(t, t.TempDir())
	if lim, err := ResolveStartLimits(); err != nil || lim != (BudgetLimits{}) {
		t.Fatalf("absent manifest: (%+v, %v), want unlimited", lim, err)
	}

	// Manifest without suggested_budgets → unlimited.
	writeProjectManifest(t, startLimitsManifestHead)
	if lim, err := ResolveStartLimits(); err != nil || lim != (BudgetLimits{}) {
		t.Fatalf("no suggestion: (%+v, %v), want unlimited", lim, err)
	}

	// Declared suggested_budgets → applied verbatim; undeclared axes stay
	// unlimited.
	writeProjectManifest(t, startLimitsManifestHead+"suggested_budgets:\n  phase_tool_calls: 40\n  delegations: 1\n")
	lim, err := ResolveStartLimits()
	if err != nil {
		t.Fatalf("declared budgets: %v", err)
	}
	if lim.PhaseToolCalls != 40 || lim.Delegations != 1 || lim.FailedChecks != 0 || lim.RunRework != 0 {
		t.Fatalf("declared budgets = %+v, want phase_tool_calls=40 delegations=1, rest unlimited", lim)
	}

	// An unreadable manifest fails CLOSED with the resolution context.
	writeProjectManifest(t, startLimitsManifestHead+"suggested_budgets:\n  tokens: 5\n")
	if _, err := ResolveStartLimits(); err == nil || !strings.Contains(err.Error(), "budget resolution") {
		t.Fatalf("corrupt manifest must fail closed with resolution context, got %v", err)
	}
}

// TestStartRunSettlesBudget pins the run-start settlement: every new run is
// budget-confirmed from birth with the "run-start" provenance and exactly
// the limits it was started with — no checkpoint ever asks.
func TestStartRunSettlesBudget(t *testing.T) {
	r := startTestRun(t, BudgetLimits{PhaseToolCalls: 7})
	if !r.BudgetConfirmed || r.BudgetSettledBy != "run-start" || r.Limits.PhaseToolCalls != 7 {
		t.Fatalf("run-start settlement = confirmed %v by %q limits %+v, want confirmed/run-start/7",
			r.BudgetConfirmed, r.BudgetSettledBy, r.Limits)
	}
	disk, err := LoadRun(r.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !disk.BudgetConfirmed || disk.BudgetSettledBy != "run-start" || disk.Limits != r.Limits {
		t.Fatalf("run-start settlement must persist: confirmed %v by %q limits %+v",
			disk.BudgetConfirmed, disk.BudgetSettledBy, disk.Limits)
	}
}
