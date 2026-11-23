package shared

// startlimits_test.go — T18 (0.6.1) run-start budget resolution: budgets are
// manifest-authored. No manifest or no suggested_budgets → unlimited; a
// declared suggestion applies verbatim; an unreadable manifest fails CLOSED
// (a run never starts on a guessed budget).

import (
	"os"
	"path/filepath"
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

// exhaustedCheckpoint drives the run to a checkpoint that carries the extend
// offer (limits are run-start configuration since 0.6.1).
func exhaustedCheckpoint(t *testing.T, r *RunState) *CheckpointState {
	t.Helper()
	firstDisplayedCheckpoint(t, r)
	if err := r.RecordDecision(r.CurrentCheckpoint().ID, DecisionContinue, "tu-c0"); err != nil {
		t.Fatal(err)
	}
	if err := r.OpenPhase("diagnosis"); err != nil {
		t.Fatal(err)
	}
	r.CountToolCall("")
	cp, err := r.SubmitPhaseUnstaged(PhaseSubmission{PhaseID: "diagnosis"})
	if err != nil {
		t.Fatal(err)
	}
	if cp.ExtendAsk == nil {
		t.Fatal("the exhaustion checkpoint must carry the extension offer")
	}
	displayCurrent(t, r)
	return r.CurrentCheckpoint()
}

// TestExtendSettlementRequiresToolUseID pins the guard that the retired
// budget-settlement cases used to cover: a settlement without the answering
// call's tool_use_id is malformed and records nothing.
func TestExtendSettlementRequiresToolUseID(t *testing.T) {
	r := startTestRun(t, BudgetLimits{PhaseToolCalls: 1})
	cp := *exhaustedCheckpoint(t, r)

	err := r.RecordDecisionWithBudget(cp.ID, DecisionContinue, "tu-e1", &BudgetSettlement{Extend: true})
	if err == nil || !strings.Contains(err.Error(), "tool_use_id") {
		t.Fatalf("a settlement without tool_use_id must reject, got %v", err)
	}
	disk, derr := LoadRun("run-1")
	if derr != nil {
		t.Fatal(derr)
	}
	if disk.Status != StatusWaitUser || disk.Checkpoints[1].Decided || len(disk.BudgetExtensions) != 0 {
		t.Fatalf("a rejected settlement must record nothing: status %s decided %v receipts %d",
			disk.Status, disk.Checkpoints[1].Decided, len(disk.BudgetExtensions))
	}

	// With the id, the same settlement extends and decides in one commit.
	if err := r.RecordDecisionWithBudget(cp.ID, DecisionContinue, "tu-e2", &BudgetSettlement{ToolUseID: "tu-e2", Extend: true}); err != nil {
		t.Fatalf("extend+decision: %v", err)
	}
	if r.Limits.PhaseToolCalls != 2 || len(r.BudgetExtensions) != 1 || r.Status != StatusActive {
		t.Fatalf("extend+decision = limits %+v receipts %d status %s", r.Limits, len(r.BudgetExtensions), r.Status)
	}
}

// TestExtendWithDecisionAtomicOnSaveFailure: the extend settlement and the
// decision share ONE commit — a failed Save applies neither. (This replaces
// the pre-0.6.1 confirm variant of the test; confirm payloads are inert now,
// so the extension is the live settlement.)
func TestExtendWithDecisionAtomicOnSaveFailure(t *testing.T) {
	r := startTestRun(t, BudgetLimits{PhaseToolCalls: 1})
	cp := *exhaustedCheckpoint(t, r)

	sessDir := filepath.Join(runStateDirOverride, ProjectKey(), "sess-1")
	if err := os.Chmod(sessDir, 0o500); err != nil {
		t.Skipf("cannot chmod the state dir here: %v", err)
	}
	t.Cleanup(func() { os.Chmod(sessDir, 0o755) })

	err := r.RecordDecisionWithBudget(cp.ID, DecisionContinue, "tu-x1", &BudgetSettlement{ToolUseID: "tu-x1", Extend: true})
	if err == nil {
		t.Fatal("RecordDecisionWithBudget must surface the Save failure")
	}
	if err := os.Chmod(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	disk, derr := LoadRun("run-1")
	if derr != nil {
		t.Fatal(derr)
	}
	if disk.Status != StatusWaitUser || disk.Checkpoints[1].Decided || len(disk.BudgetExtensions) != 0 || disk.Limits.PhaseToolCalls != 1 {
		t.Fatalf("half-applied callback on disk: status %s decided %v receipts %d limits %+v",
			disk.Status, disk.Checkpoints[1].Decided, len(disk.BudgetExtensions), disk.Limits)
	}
}
