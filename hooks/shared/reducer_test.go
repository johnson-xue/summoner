package shared

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// --- helpers ----------------------------------------------------------------

// startTestRun redirects v2 run-state into a fresh temp dir (hermetic suite)
// and starts run "run-1" in session "sess-1".
func startTestRun(t *testing.T, limits BudgetLimits) *RunState {
	t.Helper()
	prev := runStateDirOverride
	runStateDirOverride = t.TempDir()
	t.Cleanup(func() { runStateDirOverride = prev })
	r, err := StartRun("run-1", "sess-1", "fix", limits)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	return r
}

func openAndSubmit(t *testing.T, r *RunState, phaseID string, criteria ...string) CheckpointState {
	t.Helper()
	if err := r.OpenPhase(phaseID); err != nil {
		t.Fatalf("OpenPhase(%s): %v", phaseID, err)
	}
	sub := PhaseSubmission{
		PhaseID:     phaseID,
		CriteriaIDs: criteria,
		Artifacts:   []ArtifactRef{{Path: phaseID + ".md", Digest: "digest-" + phaseID, Kind: "doc"}},
	}
	cp, err := r.SubmitPhase(sub)
	if err != nil {
		t.Fatalf("SubmitPhase(%s): %v", phaseID, err)
	}
	return cp
}

func displayCurrent(t *testing.T, r *RunState) {
	t.Helper()
	cp := r.CurrentCheckpoint()
	if cp == nil {
		t.Fatal("displayCurrent: no open checkpoint")
	}
	if err := r.MarkDisplayed(cp.ID, CheckpointContentDigest(*cp, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed(%s): %v", cp.ID, err)
	}
}

func decideCurrent(t *testing.T, r *RunState, d Decision) {
	t.Helper()
	cp := r.CurrentCheckpoint()
	if cp == nil {
		t.Fatal("decideCurrent: no open checkpoint")
	}
	if err := r.RecordDecision(cp.ID, d); err != nil {
		t.Fatalf("RecordDecision(%s, %s): %v", cp.ID, d, err)
	}
}

func phaseByID(t *testing.T, r *RunState, id string) PhaseState {
	t.Helper()
	for _, p := range r.Phases {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("phase %s not found in %+v", id, r.Phases)
	return PhaseState{}
}

// lastVisit returns the LATEST visit of a phase id — recall (M11) appends a
// fresh visit instead of reopening in place, so "the phase's state" is the
// last entry with that id.
func lastVisit(t *testing.T, r *RunState, id string) PhaseState {
	t.Helper()
	var last PhaseState
	found := false
	for _, p := range r.Phases {
		if p.ID == id {
			last = p
			found = true
		}
	}
	if !found {
		t.Fatalf("phase %s not found in %+v", id, r.Phases)
	}
	return last
}

func visitCount(r *RunState, id string) int {
	n := 0
	for _, p := range r.Phases {
		if p.ID == id {
			n++
		}
	}
	return n
}

// --- legality matrix --------------------------------------------------------

func TestIllegalTransitions(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})

	// SubmitPhase with no open phase.
	if _, err := r.SubmitPhase(PhaseSubmission{PhaseID: "p1"}); err == nil {
		t.Error("SubmitPhase before OpenPhase: want error, got nil")
	}

	cp1 := openAndSubmit(t, r, "p1", "c1")
	if cp1.Nonce == "" {
		t.Fatal("checkpoint nonce is empty")
	}

	// Duplicate submit while the first checkpoint is pending (WAIT_USER).
	if _, err := r.SubmitPhase(PhaseSubmission{PhaseID: "p1"}); err == nil {
		t.Error("SubmitPhase while WAIT_USER: want error, got nil")
	}

	// ACCEPT_REQUIREMENT_CHANGE with no change open (checkpoint not displayed yet either).
	if err := r.RecordDecision(cp1.ID, DecisionAcceptRequirementChange); err == nil {
		t.Error("ACCEPT_REQUIREMENT_CHANGE outside CHANGE_OPEN: want error, got nil")
	}

	displayCurrent(t, r)
	if err := r.RecordDecision(cp1.ID, Decision("BOGUS")); err == nil {
		t.Error("unknown decision value: want error, got nil")
	}
	decideCurrent(t, r, DecisionContinue)

	// Submit for a phase that is not the open one.
	if err := r.OpenPhase("p2"); err != nil {
		t.Fatalf("OpenPhase(p2): %v", err)
	}
	if _, err := r.SubmitPhase(PhaseSubmission{PhaseID: "p1"}); err == nil {
		t.Error("SubmitPhase with wrong phase id: want error, got nil")
	}
	cp2, err := r.SubmitPhase(PhaseSubmission{PhaseID: "p2"})
	if err != nil {
		t.Fatalf("SubmitPhase(p2): %v", err)
	}
	if cp2.Nonce == cp1.Nonce {
		t.Error("two checkpoints share the same nonce")
	}
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue)

	// CHANGE_OPEN blocks phase-submit; only ACCEPT (or STOP) decides the confirm checkpoint.
	if err := r.OpenPhase("p3"); err != nil {
		t.Fatalf("OpenPhase(p3): %v", err)
	}
	if err := r.OpenRequirementChange("switch export format"); err != nil {
		t.Fatalf("OpenRequirementChange: %v", err)
	}
	if r.Change == nil {
		t.Fatal("OpenRequirementChange did not open a change")
	}
	if _, err := r.SubmitPhase(PhaseSubmission{PhaseID: "p3"}); err == nil {
		t.Error("SubmitPhase during CHANGE_OPEN: want error, got nil")
	}
	if _, err := r.ApplyRequirementDelta(RequirementDelta{Note: "csv", Affected: []string{"c1"}}); err != nil {
		t.Fatalf("ApplyRequirementDelta: %v", err)
	}
	displayCurrent(t, r)
	if err := r.RecordDecision(r.CurrentCheckpoint().ID, DecisionContinue); err == nil {
		t.Error("CONTINUE at change-confirm checkpoint: want error, got nil")
	}
	decideCurrent(t, r, DecisionAcceptRequirementChange)
	if r.Status != StatusActive {
		t.Errorf("after ACCEPT_REQUIREMENT_CHANGE: status = %s, want ACTIVE", r.Status)
	}

	// Everything is illegal once CLOSED.
	r.Status = StatusClosed
	checks := map[string]func() error{
		"OpenPhase":             func() error { return r.OpenPhase("p9") },
		"SubmitPhase":           func() error { _, err := r.SubmitPhase(PhaseSubmission{PhaseID: "p9"}); return err },
		"MarkDisplayed":         func() error { return r.MarkDisplayed("cp-999", "digest") },
		"RecordDecision":        func() error { return r.RecordDecision("cp-999", DecisionContinue) },
		"RequestReview":         func() error { _, err := r.RequestReview(testEnvelope()); return err },
		"RecordReviewResult":    func() error { return r.RecordReviewResult(ReviewResult{ReviewID: "rev-1"}) },
		"OpenRequirementChange": func() error { return r.OpenRequirementChange("late change") },
		"ApplyRequirementDelta": func() error { _, err := r.ApplyRequirementDelta(RequirementDelta{}); return err },
		"ResumeAfterChange":     r.ResumeAfterChange,
		"ResolveOutcome":        func() error { return r.ResolveOutcome("tu-1", "accepted") },
	}
	for name, fn := range checks {
		if err := fn(); err == nil {
			t.Errorf("%s on CLOSED run: want error, got nil", name)
		}
	}
}

func testEnvelope() Envelope {
	return Envelope{
		SchemaVersion: "1",
		RunID:         "run-1",
		ReviewID:      "rev-1",
		ArtifactSet:   []ArtifactRef{{Path: "pkg/x.go", Digest: "abc", Kind: "code"}},
		Requirements: FrozenView{Version: 1, Requirements: []FrozenRequirement{
			{ID: "req-1", CriteriaIDs: []string{"c1"}, Artifacts: []string{"pkg/x.go"}},
		}},
		ExitCriteria: []Criterion{
			{ID: "c1", Goal: "export works", Method: MethodMechanical, CheckID: "check-export"},
		},
		VerificationScope: VerificationScope{Readable: []string{"pkg/"}, CheckIDs: []string{"check-export"}},
	}
}

// --- display binding --------------------------------------------------------

func TestDecisionRequiresDisplayed(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	cp := openAndSubmit(t, r, "p1", "c1")

	if cp.Nonce == "" {
		t.Fatal("checkpoint nonce is empty")
	}
	if len(cp.Nonce) > 32 {
		t.Errorf("nonce %q too long for a short checkpoint nonce", cp.Nonce)
	}

	// Deciding before the checkpoint was displayed.
	if err := r.RecordDecision(cp.ID, DecisionContinue); err == nil {
		t.Error("RecordDecision before MarkDisplayed: want error, got nil")
	}

	// Display digest mismatch (stale/altered render) is rejected.
	if err := r.MarkDisplayed(cp.ID, "deadbeef"); err == nil {
		t.Error("MarkDisplayed with mismatched digest: want error, got nil")
	}
	if cur := r.CurrentCheckpoint(); cur == nil || cur.Displayed {
		t.Fatal("checkpoint must remain undisplayed after digest mismatch")
	}

	// Display digest for a checkpoint that is not the open one.
	if err := r.MarkDisplayed("cp-999", CheckpointContentDigest(cp, r.RequirementsVersion)); err == nil {
		t.Error("MarkDisplayed for non-open checkpoint: want error, got nil")
	}

	displayCurrent(t, r)
	if err := r.RecordDecision(cp.ID, DecisionContinue); err != nil {
		t.Fatalf("RecordDecision after display: %v", err)
	}
	if r.Status != StatusActive {
		t.Errorf("status = %s, want ACTIVE", r.Status)
	}
}

// --- crash recovery ---------------------------------------------------------

func TestOutcomeUnknownCrashRecovery(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	openAndSubmit(t, r, "p1", "c1")
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue)

	r.MarkOutcomeUnknown("tu-1")
	if err := r.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The entry survives a reload (crash + recovery).
	r2, err := LoadRun("run-1")
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if len(r2.OutcomeUnknowns) != 1 || r2.OutcomeUnknowns[0].ToolUseID != "tu-1" {
		t.Fatalf("outcome-unknown entry lost across reload: %+v", r2.OutcomeUnknowns)
	}
	if r2.Status != StatusOutcomeUnknown {
		t.Errorf("reloaded status = %s, want OUTCOME_UNKNOWN", r2.Status)
	}
	if r2.OutcomeUnknowns[0].PriorStatus != StatusActive {
		t.Errorf("prior status = %s, want ACTIVE", r2.OutcomeUnknowns[0].PriorStatus)
	}

	if err := r2.ResolveOutcome("tu-1", "accepted"); err != nil {
		t.Fatalf("ResolveOutcome(accepted): %v", err)
	}
	if r2.Status != StatusActive {
		t.Errorf("status after resolve = %s, want ACTIVE", r2.Status)
	}

	// All three probe branches are legal (spec §5). accepted restores the
	// prior status directly; failed/indeterminate open a recovery checkpoint
	// (B3) that the user must decide before the run resumes.
	for i, want := range []string{"accepted", "failed", "indeterminate"} {
		id := fmt.Sprintf("tu-%d", i+2)
		r2.MarkOutcomeUnknown(id)
		if r2.Status != StatusOutcomeUnknown {
			t.Fatalf("branch %s: status = %s, want OUTCOME_UNKNOWN", want, r2.Status)
		}
		if err := r2.ResolveOutcome(id, want); err != nil {
			t.Fatalf("ResolveOutcome(%s): %v", want, err)
		}
		e := r2.OutcomeUnknowns[len(r2.OutcomeUnknowns)-1]
		if !e.Resolved || e.Outcome != want {
			t.Errorf("entry = %+v, want resolved/%s", e, want)
		}
		switch want {
		case OutcomeAccepted:
			if r2.Status != StatusActive {
				t.Fatalf("branch %s: status after resolve = %s, want ACTIVE", want, r2.Status)
			}
		default:
			if r2.Status != StatusWaitUser {
				t.Fatalf("branch %s: status after resolve = %s, want WAIT_USER (recovery checkpoint)", want, r2.Status)
			}
			if cp := r2.CurrentCheckpoint(); cp == nil || cp.Kind != CheckpointRecovery {
				t.Fatalf("branch %s: want an open recovery checkpoint, got %+v", want, cp)
			}
			displayCurrent(t, r2)
			decideCurrent(t, r2, DecisionContinue)
			if r2.Status != StatusActive {
				t.Fatalf("branch %s: status after recovery CONTINUE = %s, want ACTIVE", want, r2.Status)
			}
		}
	}

	// Invalid values are rejected and leave the entry unresolved.
	r2.MarkOutcomeUnknown("tu-bad")
	if err := r2.ResolveOutcome("tu-bad", "probably"); err == nil {
		t.Error("invalid outcome value: want error, got nil")
	}
	if r2.OutcomeUnknowns[len(r2.OutcomeUnknowns)-1].Resolved {
		t.Error("entry must stay unresolved after an invalid resolve attempt")
	}
	if err := r2.ResolveOutcome("tu-404", "accepted"); err == nil {
		t.Error("unknown toolUseID: want error, got nil")
	}

	// Close tu-bad's episode through its recovery checkpoint, then a clean
	// two-unknown episode: the run stays OUTCOME_UNKNOWN until the last one
	// resolves.
	if err := r2.ResolveOutcome("tu-bad", "indeterminate"); err != nil {
		t.Fatalf("ResolveOutcome(tu-bad): %v", err)
	}
	displayCurrent(t, r2)
	decideCurrent(t, r2, DecisionContinue)
	if r2.Status != StatusActive {
		t.Fatalf("status after recovery CONTINUE = %s, want ACTIVE", r2.Status)
	}
	r2.MarkOutcomeUnknown("tu-a")
	r2.MarkOutcomeUnknown("tu-b")
	if err := r2.ResolveOutcome("tu-a", "accepted"); err != nil {
		t.Fatalf("ResolveOutcome(tu-a): %v", err)
	}
	if r2.Status != StatusOutcomeUnknown {
		t.Errorf("status = %s while one unknown remains, want OUTCOME_UNKNOWN", r2.Status)
	}
	if err := r2.ResolveOutcome("tu-b", "accepted"); err != nil {
		t.Fatalf("ResolveOutcome(tu-b): %v", err)
	}
	if r2.Status != StatusActive {
		t.Errorf("status = %s after last resolve, want ACTIVE", r2.Status)
	}
}

func TestIdempotentRecord(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})

	r.MarkOutcomeUnknown("tu-1")
	r.MarkOutcomeUnknown("tu-1")
	if len(r.OutcomeUnknowns) != 1 {
		t.Fatalf("duplicate MarkOutcomeUnknown created %d entries, want 1", len(r.OutcomeUnknowns))
	}
	if err := r.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadRun("run-1")
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if len(got.OutcomeUnknowns) != 1 {
		t.Fatalf("reloaded %d entries, want 1", len(got.OutcomeUnknowns))
	}

	// Duplicate resolve with the same outcome is a no-op; a conflicting one errors.
	if err := got.ResolveOutcome("tu-1", "accepted"); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if err := got.ResolveOutcome("tu-1", "accepted"); err != nil {
		t.Fatalf("idempotent resolve: %v", err)
	}
	if err := got.ResolveOutcome("tu-1", "failed"); err == nil {
		t.Error("conflicting re-resolve: want error, got nil")
	}
	got2, err := LoadRun("run-1")
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if len(got2.OutcomeUnknowns) != 1 || !got2.OutcomeUnknowns[0].Resolved || got2.OutcomeUnknowns[0].Outcome != "accepted" {
		t.Errorf("entry = %+v, want single resolved/accepted", got2.OutcomeUnknowns)
	}
}

// --- budget -----------------------------------------------------------------

func TestBudgetCounting(t *testing.T) {
	limits := BudgetLimits{PhaseToolCalls: 2, FailedChecks: 1, RunRework: 1, Delegations: 1}
	r := startTestRun(t, limits)

	if exhausted, _ := r.BudgetExhausted(); exhausted {
		t.Fatal("fresh run must not be exhausted")
	}
	// The phase tool budget is per open phase (M1b): open one first.
	if err := r.OpenPhase("p1"); err != nil {
		t.Fatalf("OpenPhase(p1): %v", err)
	}
	r.CountToolCall()
	if exhausted, _ := r.BudgetExhausted(); exhausted {
		t.Fatal("1 of 2 tool calls must not exhaust the phase budget")
	}
	r.CountToolCall()
	exhausted, reason := r.BudgetExhausted()
	if !exhausted || !strings.Contains(reason, "phase") {
		t.Fatalf("BudgetExhausted = (%v, %q), want phase exhaustion", exhausted, reason)
	}

	// Per-phase scope: opening a new phase resets the phase tool-call counter.
	cp, err := r.SubmitPhase(PhaseSubmission{PhaseID: "p1"})
	if err != nil {
		t.Fatalf("SubmitPhase(p1): %v", err)
	}
	if err := r.MarkDisplayed(cp.ID, CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	if err := r.RecordDecision(cp.ID, DecisionContinue); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
	if err := r.OpenPhase("p2"); err != nil {
		t.Fatalf("OpenPhase(p2): %v", err)
	}
	if exhausted, _ := r.BudgetExhausted(); exhausted {
		t.Fatal("phase tool-call budget must reset on OpenPhase")
	}

	// Each remaining counter trips its own limit with a distinct reason.
	r.Counters.FailedChecks = 1
	if exhausted, reason := r.BudgetExhausted(); !exhausted || !strings.Contains(reason, "check") {
		t.Fatalf("failed-check budget: (%v, %q)", exhausted, reason)
	}
	r.Counters.FailedChecks = 0
	r.Counters.RunRework = 1
	if exhausted, reason := r.BudgetExhausted(); !exhausted || !strings.Contains(reason, "rework") {
		t.Fatalf("run-rework budget: (%v, %q)", exhausted, reason)
	}
	r.Counters.RunRework = 0
	r.Counters.Delegations = 1
	if exhausted, reason := r.BudgetExhausted(); !exhausted || !strings.Contains(reason, "delegation") {
		t.Fatalf("delegation budget: (%v, %q)", exhausted, reason)
	}

	// Zero-value limits mean unlimited.
	free := startTestRun(t, BudgetLimits{})
	free.Counters = BudgetCounters{PhaseToolCalls: 1 << 20, FailedChecks: 1 << 20, RunRework: 1 << 20, Delegations: 1 << 20}
	if exhausted, reason := free.BudgetExhausted(); exhausted {
		t.Fatalf("zero limits must mean unlimited, got (%v, %q)", exhausted, reason)
	}
}

// --- requirement-change loop ------------------------------------------------

func TestRequirementChangeLoop(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})

	// An accepted result exists before the change: p1 with criteria c1, c2.
	openAndSubmit(t, r, "p1", "c1", "c2")
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue)
	p1 := phaseByID(t, r, "p1")
	if p1.Outcome != "accepted" || !containsID(p1.AcceptedCriteria, "c2") {
		t.Fatalf("p1 = %+v, want accepted with c2", p1)
	}

	if err := r.OpenPhase("p2"); err != nil {
		t.Fatalf("OpenPhase(p2): %v", err)
	}
	r.DevPosition = DevPosition{
		CurrentTask:          "implement csv export",
		TestProgress:         "3/5 local tests green",
		PendingVerifications: []string{"integration suite"},
	}

	// Opening the change freezes the development position.
	if err := r.OpenRequirementChange("user wants csv instead of json"); err != nil {
		t.Fatalf("OpenRequirementChange: %v", err)
	}
	if r.Change == nil || r.Change.SavedPosition.CurrentTask != "implement csv export" {
		t.Fatalf("change = %+v, want frozen DevPosition", r.Change)
	}
	if got := r.Change.SavedPosition.PendingVerifications; !reflect.DeepEqual(got, []string{"integration suite"}) {
		t.Errorf("frozen PendingVerifications = %v", got)
	}

	// The delta bumps the requirements version, stales affected ids, and
	// leaves untouched accepted results valid.
	report, err := r.ApplyRequirementDelta(RequirementDelta{
		Note:     "export format json -> csv",
		Changed:  []string{"req-export"},
		Affected: []string{"c1"},
	})
	if err != nil {
		t.Fatalf("ApplyRequirementDelta: %v", err)
	}
	if r.RequirementsVersion != 2 {
		t.Errorf("RequirementsVersion = %d, want 2", r.RequirementsVersion)
	}
	if !containsID(r.Stale, "c1") {
		t.Errorf("Stale = %v, want c1", r.Stale)
	}
	if containsID(r.Stale, "c2") {
		t.Errorf("c2 must not be stale: %v", r.Stale)
	}
	if !containsID(report.Preserved, "c2") {
		t.Errorf("ImpactReport.Preserved = %v, want c2", report.Preserved)
	}
	if !containsID(report.NewlyStale, "c1") {
		t.Errorf("ImpactReport.NewlyStale = %v, want c1", report.NewlyStale)
	}
	p1 = phaseByID(t, r, "p1")
	if !containsID(p1.AcceptedCriteria, "c2") {
		t.Error("untouched accepted criterion c2 must keep its acceptance")
	}
	cp := r.CurrentCheckpoint()
	if cp == nil || cp.Kind != CheckpointChangeConfirm {
		t.Fatalf("want an open CHANGE_CONFIRM checkpoint, got %+v", cp)
	}

	// Single-point confirmation restores the saved position and returns to ACTIVE.
	displayCurrent(t, r)
	if err := r.RecordDecision(cp.ID, DecisionAcceptRequirementChange); err != nil {
		t.Fatalf("RecordDecision(ACCEPT_REQUIREMENT_CHANGE): %v", err)
	}
	if r.Change != nil {
		t.Error("change must be closed after ACCEPT_REQUIREMENT_CHANGE")
	}
	if r.Status != StatusActive {
		t.Errorf("status = %s, want ACTIVE", r.Status)
	}
	if r.DevPosition.CurrentTask != "implement csv export" || r.DevPosition.TestProgress != "3/5 local tests green" {
		t.Errorf("DevPosition = %+v, want restored position", r.DevPosition)
	}
	if !reflect.DeepEqual(r.DevPosition.PendingVerifications, []string{"integration suite"}) {
		t.Errorf("PendingVerifications = %v, want restored", r.DevPosition.PendingVerifications)
	}

	// Direct resume without a delta is the bypass M2 kills: a change may only
	// close through its single-point confirm checkpoint.
	r.DevPosition.CurrentTask = "mid-change cleanup"
	if err := r.OpenRequirementChange("follow-up tweak"); err != nil {
		t.Fatalf("OpenRequirementChange: %v", err)
	}
	if err := r.ResumeAfterChange(); err == nil {
		t.Fatal("ResumeAfterChange without an applied delta: want error, got nil")
	}
	if r.Change == nil {
		t.Fatal("the refused resume must leave the change open")
	}
	if _, err := r.ApplyRequirementDelta(RequirementDelta{Note: "tweak"}); err != nil {
		t.Fatalf("ApplyRequirementDelta: %v", err)
	}
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionAcceptRequirementChange)
	if r.Status != StatusActive || r.Change != nil || r.DevPosition.CurrentTask != "mid-change cleanup" {
		t.Errorf("after delta+accept: status=%s change=%v pos=%+v", r.Status, r.Change, r.DevPosition)
	}
}

// --- recall -----------------------------------------------------------------

func TestRecallReopensAcceptedPriorPhase(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})

	for _, id := range []string{"p1", "p2"} {
		openAndSubmit(t, r, id, "c-"+id)
		displayCurrent(t, r)
		decideCurrent(t, r, DecisionContinue)
	}
	openAndSubmit(t, r, "p3", "c-p3")
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionRecall)

	if r.Status != StatusActive {
		t.Errorf("status = %s, want ACTIVE", r.Status)
	}
	// M11: the recall target keeps its history — one closed "recalled" visit
	// plus a fresh open visit with the same id.
	if n := visitCount(r, "p2"); n != 2 {
		t.Fatalf("p2 has %d visits, want 2 (recalled original + fresh open)", n)
	}
	if first := phaseByID(t, r, "p2"); first.Open || first.Outcome != "recalled" {
		t.Errorf("p2 first visit = %+v, want closed/recalled", first)
	}
	if got := lastVisit(t, r, "p2"); !got.Open || got.Outcome != "" {
		t.Errorf("p2 latest visit = %+v, want open/undecided", got)
	}
	if got := phaseByID(t, r, "p3"); got.Open || got.Outcome != "recalled" {
		t.Errorf("p3 = %+v, want closed/recalled", got)
	}
	if !containsID(r.Stale, "c-p3") {
		t.Errorf("Stale = %v, want downstream c-p3", r.Stale)
	}
	if len(r.Recalls) != 1 || r.Recalls[0].TargetPhaseID != "p2" {
		t.Errorf("Recalls = %+v, want a single recall targeting p2", r.Recalls)
	}
	if r.CurrentCheckpoint() != nil {
		t.Error("checkpoint must be decided after recall")
	}
}

// --- persistence ------------------------------------------------------------

func TestSaveLoadRoundTrip(t *testing.T) {
	// m2: hermeticity must hold from the FIRST LoadRun — the override is set
	// before any state read, so the missing-run probe never touches the real
	// state directory.
	prev := runStateDirOverride
	runStateDirOverride = t.TempDir()
	t.Cleanup(func() { runStateDirOverride = prev })

	if _, err := LoadRun("missing-run"); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("LoadRun(missing) = %v, want ErrRunNotFound", err)
	}

	r := startTestRun(t, BudgetLimits{PhaseToolCalls: 10, Delegations: 2})
	openAndSubmit(t, r, "p1", "c1", "c2")
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue)
	openAndSubmit(t, r, "p2", "c3")
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionRecall)

	r.DevPosition = DevPosition{CurrentTask: "task", TestProgress: "1/2", PendingVerifications: []string{"v1"}}
	if err := r.OpenRequirementChange("late change"); err != nil {
		t.Fatalf("OpenRequirementChange: %v", err)
	}
	if _, err := r.RequestReview(testEnvelope()); err != nil {
		t.Fatalf("RequestReview: %v", err)
	}
	if err := r.MarkReviewRunning("rev-1"); err != nil {
		t.Fatalf("MarkReviewRunning: %v", err)
	}
	r.MarkOutcomeUnknown("tu-1")
	r.Counters.Delegations = 1
	if err := r.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := LoadRun("run-1")
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	want, _ := json.Marshal(r)
	have, _ := json.Marshal(got)
	if !bytes.Equal(want, have) {
		t.Errorf("round-trip mismatch:\nwant: %s\nhave: %s", want, have)
	}

	// Atomic save leaves no temp files behind and exactly one state file.
	var tmpCount, jsonCount int
	err = filepath.Walk(runStateDirOverride, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return err
		}
		switch {
		case strings.HasPrefix(info.Name(), ".runstate-"):
			tmpCount++
		case strings.HasSuffix(info.Name(), ".json"):
			jsonCount++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk state dir: %v", err)
	}
	if tmpCount != 0 {
		t.Errorf("%d temp files left behind by Save", tmpCount)
	}
	if jsonCount != 1 {
		t.Errorf("state file count = %d, want 1", jsonCount)
	}

	// Wire types round-trip with every field populated (json tags on every field).
	env := testEnvelope()
	env.ExitCriteria = append(env.ExitCriteria, Criterion{
		ID: "c2", Goal: "no injection", Scope: "all", Method: MethodJudgment,
		Expected: "no unsafe paths", Required: true, Rubric: "rubrics/security.md",
	})
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	var env2 Envelope
	if err := json.Unmarshal(b, &env2); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if !reflect.DeepEqual(env, env2) {
		t.Errorf("Envelope round-trip: %+v vs %+v", env, env2)
	}

	res := ReviewResult{
		ReviewID:       "rev-1",
		ArtifactDigest: "abc",
		RunStatus:      "COMPLETED",
		Verdict:        "NEEDS-FIX",
		CriterionVerdicts: []CriterionVerdict{
			{CriterionID: "c1", Status: "FAIL", Note: "missing branch"},
		},
		Findings: []Finding{
			{CriterionID: "c1", Path: "pkg/x.go", Line: 42, Failure: "nil deref on empty input", Suggestion: "guard len"},
		},
		EvidenceToolCallIDs: []string{"toolu_01", "toolu_02"},
	}
	b, err = json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal review result: %v", err)
	}
	var res2 ReviewResult
	if err := json.Unmarshal(b, &res2); err != nil {
		t.Fatalf("unmarshal review result: %v", err)
	}
	if !reflect.DeepEqual(res, res2) {
		t.Errorf("ReviewResult round-trip: %+v vs %+v", res, res2)
	}

	// A BLOCKED result keeps run_status and verdict separate.
	blocked := ReviewResult{ReviewID: "rev-2", RunStatus: "BLOCKED", ReasonCode: "SANDBOX_UNAVAILABLE"}
	if blocked.Verdict != "" {
		t.Error("BLOCKED result must not carry a quality verdict")
	}
}

// --- fix round 1: review crash recovery, failure routing, change gating ------

// TestRequestReviewResendSameEnvelope pins spec §3 崩溃恢复: while a review is
// PREPARED/RUNNING (run REVIEW_PENDING after a crash + reconnect), re-sending
// the SAME envelope must succeed idempotently — no duplicate review record.
func TestRequestReviewResendSameEnvelope(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	openAndSubmit(t, r, "p1", "c1")
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue)

	id, err := r.RequestReview(testEnvelope())
	if err != nil {
		t.Fatalf("RequestReview: %v", err)
	}
	if id != "rev-1" || r.Status != StatusReviewPending || len(r.Reviews) != 1 {
		t.Fatalf("id=%s status=%s reviews=%d", id, r.Status, len(r.Reviews))
	}

	// Crash + reconnect: same envelope re-sent while REVIEW_PENDING.
	again, err := r.RequestReview(testEnvelope())
	if err != nil {
		t.Fatalf("same-envelope re-send while REVIEW_PENDING: %v", err)
	}
	if again != id || len(r.Reviews) != 1 || r.Status != StatusReviewPending {
		t.Errorf("re-send: again=%s reviews=%d status=%s, want idempotent no-op", again, len(r.Reviews), r.Status)
	}

	// Same id with a tampered envelope is refused.
	tampered := testEnvelope()
	tampered.ArtifactSet[0].Digest = "tampered"
	if _, err := r.RequestReview(tampered); err == nil {
		t.Error("same id with different envelope: want error, got nil")
	}

	// A new review id while one is in flight is refused.
	fresh := testEnvelope()
	fresh.ReviewID = "rev-2"
	if _, err := r.RequestReview(fresh); err == nil {
		t.Error("new review id while one is in flight: want error, got nil")
	}

	// After conclusion the id cannot be reused.
	if err := r.MarkReviewRunning("rev-1"); err != nil {
		t.Fatalf("MarkReviewRunning: %v", err)
	}
	if err := r.RecordReviewResult(ReviewResult{ReviewID: "rev-1", RunStatus: "COMPLETED", Verdict: "PASS"}); err != nil {
		t.Fatalf("RecordReviewResult: %v", err)
	}
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue)
	if _, err := r.RequestReview(testEnvelope()); err == nil {
		t.Error("concluded review id re-request: want error, got nil")
	}
}

// TestReviewFailureRoutesToCheckpoint pins spec §3 review.failed semantics: a
// protocol-error result must not brick the run in REVIEW_PENDING — it routes
// to a checkpoint a human can still decide.
func TestReviewFailureRoutesToCheckpoint(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	openAndSubmit(t, r, "p1", "c1")
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue)
	if _, err := r.RequestReview(testEnvelope()); err != nil {
		t.Fatalf("RequestReview: %v", err)
	}
	if err := r.MarkReviewRunning("rev-1"); err != nil {
		t.Fatalf("MarkReviewRunning: %v", err)
	}

	if err := r.RecordReviewResult(ReviewResult{ReviewID: "rev-1", RunStatus: "WEIRD", Verdict: "PASS"}); err == nil {
		t.Fatal("invalid run_status: want protocol error, got nil")
	}
	if r.Status != StatusWaitUser {
		t.Errorf("status after review.failed = %s, want WAIT_USER (routed to checkpoint)", r.Status)
	}
	if len(r.Reviews) != 1 || r.Reviews[0].Status != ReviewFailed {
		t.Errorf("reviews = %+v, want the review marked FAILED", r.Reviews)
	}
	cp := r.CurrentCheckpoint()
	if cp == nil || cp.Kind != CheckpointReviewResult {
		t.Fatalf("want an open REVIEW_RESULT checkpoint after failure, got %+v", cp)
	}

	// The run stays operable: the failure checkpoint is display-bound and
	// decidable like any other.
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue)
	if r.Status != StatusActive {
		t.Errorf("status after deciding the failure checkpoint = %s, want ACTIVE", r.Status)
	}
}

// TestM2RequirementChangeFromCheckpointSupersedesAndRerenders pins the M2
// change-loop closure from a WAIT_USER entry: the pending checkpoint is
// superseded (undecidable), the delta is reachable, and acceptance lands on a
// fresh re-rendered checkpoint for the same phase — never straight back to
// ACTIVE, and never a re-decision of the superseded original.
func TestM2RequirementChangeFromCheckpointSupersedesAndRerenders(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	cp1 := openAndSubmit(t, r, "p1", "c1") // WAIT_USER, cp-1 (PHASE, p1)
	displayCurrent(t, r)

	r.DevPosition = DevPosition{CurrentTask: "mid-phase", PendingVerifications: []string{"v1"}}
	if err := r.OpenRequirementChange("adjust at checkpoint"); err != nil {
		t.Fatalf("OpenRequirementChange from WAIT_USER: %v", err)
	}
	if r.Change == nil || r.Change.PriorStatus != StatusWaitUser {
		t.Fatalf("change = %+v, want PriorStatus WAIT_USER recorded", r.Change)
	}
	if r.Change.SavedPosition.CurrentTask != "mid-phase" {
		t.Errorf("frozen position = %+v", r.Change.SavedPosition)
	}

	// The pending checkpoint is superseded: nothing is current, nothing is
	// decidable — the change must be processed, not decided around.
	if r.CurrentCheckpoint() != nil {
		t.Fatal("a superseded checkpoint must not be CurrentCheckpoint")
	}
	if err := r.RecordDecision(cp1.ID, DecisionContinue); err == nil {
		t.Fatal("decision on a superseded checkpoint: want error, got nil")
	}

	// The delta is reachable from the WAIT_USER entry and opens the confirm.
	if _, err := r.ApplyRequirementDelta(RequirementDelta{Note: "csv", Affected: []string{"c1"}}); err != nil {
		t.Fatalf("ApplyRequirementDelta from WAIT_USER entry: %v", err)
	}
	confirm := r.CurrentCheckpoint()
	if confirm == nil || confirm.Kind != CheckpointChangeConfirm {
		t.Fatalf("want a pending CHANGE_CONFIRM checkpoint, got %+v", confirm)
	}
	if r.RequirementsVersion != 2 {
		t.Fatalf("RequirementsVersion = %d, want 2", r.RequirementsVersion)
	}
	displayCurrent(t, r)
	if err := r.RecordDecision(confirm.ID, DecisionAcceptRequirementChange); err != nil {
		t.Fatalf("RecordDecision(ACCEPT_REQUIREMENT_CHANGE): %v", err)
	}

	// Landing: the change closes, the position restores, and a fresh
	// re-rendered checkpoint for p1 keeps the run at WAIT_USER (single-point
	// closure — one re-render, not a phase-by-phase reopen).
	if r.Change != nil {
		t.Fatal("change must be closed after ACCEPT_REQUIREMENT_CHANGE")
	}
	if r.Status != StatusWaitUser {
		t.Fatalf("status after accept = %s, want WAIT_USER (re-rendered checkpoint owed)", r.Status)
	}
	if r.DevPosition.CurrentTask != "mid-phase" {
		t.Errorf("DevPosition = %+v, want restored position", r.DevPosition)
	}
	if !r.Checkpoints[0].Superseded {
		t.Fatal("the original checkpoint must be marked Superseded")
	}
	rerender := r.CurrentCheckpoint()
	if rerender == nil || rerender.ID == cp1.ID || rerender.Kind != CheckpointPhase || rerender.PhaseID != "p1" {
		t.Fatalf("want a re-rendered PHASE checkpoint for p1, got %+v", rerender)
	}
	if !strings.Contains(rerender.Note, "re-rendered after requirement change v2") {
		t.Fatalf("re-render note = %q, want the v2 marker", rerender.Note)
	}
	if p := phaseByID(t, r, "p1"); p.CheckpointID != rerender.ID {
		t.Fatalf("p1.CheckpointID = %s, want the re-rendered %s", p.CheckpointID, rerender.ID)
	}
	if !containsID(r.Stale, "c1") {
		t.Fatalf("Stale = %v, want c1 stale after the delta", r.Stale)
	}

	// The re-rendered checkpoint decides the phase normally.
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue)
	if r.Status != StatusActive {
		t.Fatalf("status after deciding the re-render = %s, want ACTIVE", r.Status)
	}
	if p := phaseByID(t, r, "p1"); p.Outcome != "accepted" {
		t.Fatalf("p1 = %+v, want accepted via the re-rendered checkpoint", p)
	}
}

// TestM2ResumeWithoutDeltaRefused pins the killed bypass: opening a change and
// resuming without any applied delta is refused — the only closure is the
// single-point confirm checkpoint after a delta.
func TestM2ResumeWithoutDeltaRefused(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	if err := r.OpenRequirementChange("no delta"); err != nil {
		t.Fatalf("OpenRequirementChange: %v", err)
	}
	if err := r.ResumeAfterChange(); err == nil {
		t.Fatal("ResumeAfterChange without an applied delta: want error, got nil")
	}
	if r.Change == nil {
		t.Fatal("the refused resume must leave the change open")
	}

	// The sanctioned closure: delta → confirm → ACCEPT.
	if _, err := r.ApplyRequirementDelta(RequirementDelta{Note: "explicit no-op delta"}); err != nil {
		t.Fatalf("ApplyRequirementDelta: %v", err)
	}
	if !r.Change.Applied {
		t.Fatal("ApplyRequirementDelta must set Change.Applied")
	}
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionAcceptRequirementChange)
	if r.Change != nil || r.Status != StatusActive {
		t.Fatalf("after delta+accept: change=%v status=%s", r.Change, r.Status)
	}
}

// --- session run resolution (Task 3 ruling 5) ----------------------------------

// TestSessionRuns: the gate resolves "the session's live runs" through this
// helper — every run file under the session dir, deterministically ordered,
// with unparseable state failing closed instead of shrinking the set.
func TestSessionRuns(t *testing.T) {
	prev := runStateDirOverride
	runStateDirOverride = t.TempDir()
	t.Cleanup(func() { runStateDirOverride = prev })

	if _, err := StartRun("run-b", "sess-1", "fix", BudgetLimits{}); err != nil {
		t.Fatalf("StartRun(run-b): %v", err)
	}
	if _, err := StartRun("run-a", "sess-1", "new", BudgetLimits{}); err != nil {
		t.Fatalf("StartRun(run-a): %v", err)
	}
	if _, err := StartRun("run-z", "sess-2", "fix", BudgetLimits{}); err != nil {
		t.Fatalf("StartRun(run-z): %v", err)
	}

	runs, err := SessionRuns("sess-1")
	if err != nil {
		t.Fatalf("SessionRuns(sess-1): %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs for sess-1, want 2", len(runs))
	}
	if runs[0].RunID != "run-a" || runs[1].RunID != "run-b" {
		t.Fatalf("SessionRuns must order deterministically by run id, got %s, %s", runs[0].RunID, runs[1].RunID)
	}

	if runs, err := SessionRuns("sess-none"); err != nil || len(runs) != 0 {
		t.Fatalf("SessionRuns(sess-none) = %v, %v; want empty, nil", runs, err)
	}
	if _, err := SessionRuns("../evil"); err == nil {
		t.Fatal("SessionRuns must reject unsafe session ids")
	}

	// A corrupt run file must fail closed, never silently reduce the live set.
	dir := filepath.Join(runStateDirOverride, ProjectKey(), "sess-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run-c.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt run: %v", err)
	}
	if _, err := SessionRuns("sess-1"); err == nil {
		t.Fatal("SessionRuns accepted an unparseable run file — must fail closed")
	}
}

// TestSessionRunsUnreadableDirFailsClosed: an unreadable session dir must be an
// error, never "zero runs" — the gate's pass-through depends on that
// distinction (review round 1, I1).
func TestSessionRunsUnreadableDirFailsClosed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not block reads")
	}
	prev := runStateDirOverride
	runStateDirOverride = t.TempDir()
	t.Cleanup(func() { runStateDirOverride = prev })

	if _, err := StartRun("run-1", "sess-9", "fix", BudgetLimits{}); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	dir := filepath.Join(runStateDirOverride, ProjectKey(), "sess-9")
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Skipf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	if runs, err := SessionRuns("sess-9"); err == nil {
		t.Fatalf("SessionRuns on unreadable dir = %v, nil — must fail closed, not look like zero runs", runs)
	}
}

// TestSessionRunsDirEntryFailsClosed: a run-state path that became a
// directory is detectable corruption — it must error, never silently
// shrink the run set to zero (review round 2, R4).
func TestSessionRunsDirEntryFailsClosed(t *testing.T) {
	prev := runStateDirOverride
	runStateDirOverride = t.TempDir()
	t.Cleanup(func() { runStateDirOverride = prev })

	if _, err := StartRun("run-1", "sess-d", "fix", BudgetLimits{}); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	dir := filepath.Join(runStateDirOverride, ProjectKey(), "sess-d")
	if err := os.MkdirAll(filepath.Join(dir, "evil.json"), 0o755); err != nil {
		t.Fatalf("mkdir evil.json: %v", err)
	}
	if runs, err := SessionRuns("sess-d"); err == nil {
		t.Fatalf("SessionRuns accepted a directory named *.json (runs=%v) — must fail closed", runs)
	}
}

// --- delegation start (review round 2, R1) --------------------------------------

// preparedReview drives a run to REVIEW_PENDING with one PREPARED review.
func preparedReview(t *testing.T, r *RunState) string {
	t.Helper()
	env := Envelope{
		SchemaVersion: "2",
		RunID:         r.RunID,
		ReviewID:      "rev-1",
		ArtifactSet:   []ArtifactRef{{Path: "a.md", Digest: "ddd"}},
	}
	if _, err := r.RequestReview(env); err != nil {
		t.Fatalf("RequestReview: %v", err)
	}
	return env.ReviewID
}

// TestStartDelegation: one method, one Save — reviewer PREPARED→RUNNING in
// REVIEW_PENDING, worker only in ACTIVE, both consuming a delegation slot
// and a tool call atomically.
func TestStartDelegation(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	revID := preparedReview(t, r)

	// Worker delegation needs ACTIVE — REVIEW_PENDING refuses.
	if err := r.StartDelegation("", false); err == nil {
		t.Fatal("StartDelegation(worker) in REVIEW_PENDING: want error, got nil")
	}
	// Reviewer happy path: PREPARED → RUNNING, counters consumed, one Save.
	if err := r.StartDelegation(revID, true); err != nil {
		t.Fatalf("StartDelegation(reviewer): %v", err)
	}
	if r.Reviews[0].Status != ReviewRunning {
		t.Fatalf("review status = %s, want RUNNING", r.Reviews[0].Status)
	}
	if r.Counters.Delegations != 1 {
		t.Fatalf("counters = %+v, want delegations=1", r.Counters)
	}
	// M1b: the phase tool-call budget charges only an OPEN phase — this run
	// never opened one, so PhaseToolCalls stays zero through both delegations.
	// The persisted file agrees (single Save actually happened).
	disk, err := LoadRun("run-1")
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if disk.Reviews[0].Status != ReviewRunning || disk.Counters.Delegations != 1 {
		t.Fatalf("disk state = %s %+v — StartDelegation must persist atomically", disk.Reviews[0].Status, disk.Counters)
	}

	// Idempotence guards: unknown review, non-PREPARED review, wrong status.
	if err := r.StartDelegation("nope", true); err == nil {
		t.Fatal("StartDelegation(unknown review): want error")
	}
	if err := r.StartDelegation(revID, true); err == nil {
		t.Fatal("StartDelegation on a RUNNING review: want error")
	}

	// Worker delegation in ACTIVE works and counts.
	if err := r.RecordReviewResult(ReviewResult{ReviewID: revID, RunStatus: ReviewRunBlocked, ReasonCode: "x"}); err != nil {
		t.Fatalf("RecordReviewResult: %v", err)
	}
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue) // review-result checkpoint → ACTIVE
	if err := r.StartDelegation("", false); err != nil {
		t.Fatalf("StartDelegation(worker) in ACTIVE: %v", err)
	}
	if r.Counters.Delegations != 2 {
		t.Fatalf("counters = %+v, want delegations=2", r.Counters)
	}
	if r.Counters.PhaseToolCalls != 0 {
		t.Fatalf("counters = %+v, want PhaseToolCalls=0 (no open phase, M1b)", r.Counters)
	}
}

// TestStartDelegationSaveFailureLeavesDiskUnchanged: the whole transition is
// one in-memory pass + one Save — an unwritable state dir leaves the disk
// exactly as it was (review still PREPARED, counters untouched), so a retry
// after repair is clean.
func TestStartDelegationSaveFailureLeavesDiskUnchanged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not block writes")
	}
	r := startTestRun(t, BudgetLimits{})
	revID := preparedReview(t, r)

	dir := filepath.Join(runStateDirOverride, ProjectKey(), "sess-1")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Skipf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	if err := r.StartDelegation(revID, true); err == nil {
		t.Fatal("StartDelegation with an unwritable state dir must fail")
	}

	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod repair: %v", err)
	}
	disk, err := LoadRun("run-1")
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if disk.Reviews[0].Status != ReviewPrepared || disk.Counters.Delegations != 0 || disk.Counters.PhaseToolCalls != 0 {
		t.Fatalf("disk after failed Save = %s %+v — must be unchanged (PREPARED, zero counters)",
			disk.Reviews[0].Status, disk.Counters)
	}
}

// --- task 2.5: adversarial-review hardening ------------------------------------

// TestB1DisplayBindingRejectsAlteredCheckpoint pins the B1 display binding:
// the digest covers ALL checkpoint content (note, criteria, phase, version),
// and a decision is accepted only when the content is byte-identical to what
// was displayed — any post-display change demands a re-render.
func TestB1DisplayBindingRejectsAlteredCheckpoint(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	openAndSubmit(t, r, "p1", "c1", "c2")
	displayCurrent(t, r)
	cp := r.CurrentCheckpoint()

	// Note edited after the display.
	cp.Note = "amended after display"
	err := r.RecordDecision(cp.ID, DecisionContinue)
	if err == nil || !strings.Contains(err.Error(), "changed since display") {
		t.Fatalf("note edited after display: err = %v, want content-changed rejection", err)
	}
	cp.Note = ""

	// Criteria added after the display.
	cp.CriteriaIDs = append(cp.CriteriaIDs, "c3")
	if err := r.RecordDecision(cp.ID, DecisionContinue); err == nil {
		t.Fatal("criteria edited after display: want error, got nil")
	}
	cp.CriteriaIDs = cp.CriteriaIDs[:len(cp.CriteriaIDs)-1]

	// Requirements version bumped after the display.
	r.RequirementsVersion++
	if err := r.RecordDecision(cp.ID, DecisionContinue); err == nil {
		t.Fatal("requirements version bump after display: want error, got nil")
	}
	r.RequirementsVersion--

	// Phase binding drifted after the display (review round 1, F3).
	origPhase := cp.PhaseID
	cp.PhaseID = "p-other"
	if err := r.RecordDecision(cp.ID, DecisionContinue); err == nil {
		t.Fatal("phase drift after display: want error, got nil")
	}
	cp.PhaseID = origPhase

	// Artifact version drifted after the display (review round 1, F3).
	origArtifact := cp.ArtifactVersions["p1.md"]
	cp.ArtifactVersions["p1.md"] = "tampered"
	if err := r.RecordDecision(cp.ID, DecisionContinue); err == nil {
		t.Fatal("artifact-version drift after display: want error, got nil")
	}
	cp.ArtifactVersions["p1.md"] = origArtifact

	// Untouched content still decides.
	if err := r.RecordDecision(cp.ID, DecisionContinue); err != nil {
		t.Fatalf("unchanged checkpoint decision: %v", err)
	}
	if r.Status != StatusActive {
		t.Fatalf("status = %s, want ACTIVE", r.Status)
	}
}

// TestB2ReviewOnDemandObligation pins the B2 persisted review obligation:
// [v] REVIEW_ON_DEMAND records an obligation and parks the run at
// REVIEW_PENDING (not ACTIVE); the obligation blocks work until the matching
// RequestReview consumes it, and it survives a crash.
func TestB2ReviewOnDemandObligation(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	cp := openAndSubmit(t, r, "p1", "c1")
	displayCurrent(t, r)
	if err := r.RecordDecision(cp.ID, DecisionReviewOnDemand); err != nil {
		t.Fatalf("RecordDecision(REVIEW_ON_DEMAND): %v", err)
	}
	if r.Status != StatusReviewPending {
		t.Fatalf("status = %s, want REVIEW_PENDING (obligation recorded, not ACTIVE)", r.Status)
	}
	if r.ReviewObligation == nil || r.ReviewObligation.SourceCheckpointID != cp.ID || r.ReviewObligation.PhaseID != "p1" {
		t.Fatalf("ReviewObligation = %+v, want source %s / phase p1", r.ReviewObligation, cp.ID)
	}

	// The obligation blocks work: no phase may open while it is outstanding.
	if err := r.OpenPhase("p2"); err == nil {
		t.Fatal("OpenPhase with an outstanding review obligation: want error, got nil")
	}

	// Requesting the review consumes the obligation and binds its source.
	if _, err := r.RequestReview(testEnvelope()); err != nil {
		t.Fatalf("RequestReview under obligation: %v", err)
	}
	if r.ReviewObligation != nil {
		t.Fatal("RequestReview must consume the obligation")
	}
	if len(r.Reviews) != 1 || r.Reviews[0].Status != ReviewPrepared || r.Reviews[0].SourceCheckpointID != cp.ID {
		t.Fatalf("reviews = %+v, want one PREPARED review carrying the source checkpoint", r.Reviews)
	}

	// Crash while the obligation is outstanding: reload, re-issue.
	r2 := startTestRun(t, BudgetLimits{})
	cp2 := openAndSubmit(t, r2, "p1", "c1")
	displayCurrent(t, r2)
	decideCurrent(t, r2, DecisionReviewOnDemand)
	reloaded, err := LoadRun("run-1")
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if reloaded.ReviewObligation == nil {
		t.Fatal("the obligation must persist across a crash")
	}
	if _, err := reloaded.RequestReview(testEnvelope()); err != nil {
		t.Fatalf("RequestReview after crash-reload: %v", err)
	}
	if reloaded.ReviewObligation != nil || reloaded.Reviews[0].SourceCheckpointID != cp2.ID {
		t.Fatalf("recovered obligation = %+v, reviews = %+v", reloaded.ReviewObligation, reloaded.Reviews)
	}
}

// TestB3RecoveryCheckpointOnFailedOrIndeterminate pins the B3 recovery
// checkpoint: a failed or indeterminate probe never auto-restores — it opens a
// `recovery` checkpoint whose only legal decisions are CONTINUE / RECALL /
// STOP, and CONTINUE accepts the status quo and restores the prior.
func TestB3RecoveryCheckpointOnFailedOrIndeterminate(t *testing.T) {
	for _, branch := range []string{OutcomeFailed, OutcomeIndeterminate} {
		r := startTestRun(t, BudgetLimits{})
		openAndSubmit(t, r, "p1", "c1")
		displayCurrent(t, r)
		decideCurrent(t, r, DecisionContinue) // ACTIVE

		r.MarkOutcomeUnknown("tu-1")
		if err := r.ResolveOutcome("tu-1", branch); err != nil {
			t.Fatalf("ResolveOutcome(%s): %v", branch, err)
		}
		if r.Status != StatusWaitUser {
			t.Fatalf("branch %s: status = %s, want WAIT_USER (recovery checkpoint, no auto-restore)", branch, r.Status)
		}
		cp := r.CurrentCheckpoint()
		if cp == nil || cp.Kind != CheckpointRecovery {
			t.Fatalf("branch %s: want an open recovery checkpoint, got %+v", branch, cp)
		}
		if !strings.Contains(cp.Note, "tu-1="+branch) {
			t.Fatalf("branch %s: recovery note %q must list each entry's outcome", branch, cp.Note)
		}

		// Only CONTINUE / RECALL / STOP are legal at a recovery checkpoint.
		displayCurrent(t, r)
		for _, d := range []Decision{DecisionSkip, DecisionDone, DecisionReviewOnDemand, DecisionAcceptRequirementChange} {
			if err := r.RecordDecision(cp.ID, d); err == nil {
				t.Fatalf("branch %s: %s at a recovery checkpoint: want error, got nil", branch, d)
			}
		}
		if err := r.RecordDecision(cp.ID, DecisionContinue); err != nil {
			t.Fatalf("branch %s: CONTINUE at recovery checkpoint: %v", branch, err)
		}
		if r.Status != StatusActive {
			t.Fatalf("branch %s: status after CONTINUE = %s, want ACTIVE (prior restored)", branch, r.Status)
		}
		if r.RecoveryPrior != "" {
			t.Fatalf("branch %s: RecoveryPrior must clear when the episode ends", branch)
		}
	}

	// All accepted: no recovery checkpoint, direct restore.
	r := startTestRun(t, BudgetLimits{})
	r.MarkOutcomeUnknown("tu-ok")
	if err := r.ResolveOutcome("tu-ok", OutcomeAccepted); err != nil {
		t.Fatalf("ResolveOutcome(accepted): %v", err)
	}
	if r.Status != StatusActive {
		t.Fatalf("all-accepted: status = %s, want direct restore to ACTIVE", r.Status)
	}
	if cp := r.CurrentCheckpoint(); cp != nil {
		t.Fatalf("all-accepted must not open a checkpoint, got %+v", cp)
	}
}

// TestB4RecoveryPriorFrozenPerEpisode pins the B4 episode prior: the restore
// target is frozen when the episode STARTS (RecoveryPrior), so a later episode
// never inherits an earlier one's prior, and a checkpoint pending when the
// episode hit is not bypassed by the recovery.
func TestB4RecoveryPriorFrozenPerEpisode(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	openAndSubmit(t, r, "p1", "c1")
	displayCurrent(t, r) // WAIT_USER with cp-1 pending

	// Episode 1 starts from WAIT_USER and resolves accepted.
	r.MarkOutcomeUnknown("tu-1")
	if err := r.ResolveOutcome("tu-1", OutcomeAccepted); err != nil {
		t.Fatalf("ResolveOutcome(tu-1): %v", err)
	}
	if r.Status != StatusWaitUser {
		t.Fatalf("episode 1 restore: status = %s, want WAIT_USER", r.Status)
	}
	if cp := r.CurrentCheckpoint(); cp == nil || cp.ID != "cp-1" {
		t.Fatalf("pending checkpoint after episode 1: %+v, want cp-1 still owed", cp)
	}

	// Leave WAIT_USER; episode 2 has two entries and starts from ACTIVE.
	decideCurrent(t, r, DecisionContinue)
	if r.Status != StatusActive {
		t.Fatalf("status after deciding cp-1 = %s, want ACTIVE", r.Status)
	}
	r.MarkOutcomeUnknown("tu-a")
	r.MarkOutcomeUnknown("tu-b")
	if err := r.ResolveOutcome("tu-a", OutcomeAccepted); err != nil {
		t.Fatalf("ResolveOutcome(tu-a): %v", err)
	}
	if r.Status != StatusOutcomeUnknown {
		t.Fatalf("mid-episode: status = %s, want OUTCOME_UNKNOWN", r.Status)
	}
	if err := r.ResolveOutcome("tu-b", OutcomeAccepted); err != nil {
		t.Fatalf("ResolveOutcome(tu-b): %v", err)
	}
	if r.Status != StatusActive {
		t.Fatalf("episode 2 restore: status = %s, want ACTIVE (the episode's own prior, not episode 1's WAIT_USER)", r.Status)
	}

	// A FAILED episode that started at WAIT_USER must not bypass the pending
	// checkpoint either: after the recovery checkpoint is decided, the
	// original checkpoint resurfaces.
	r2 := startTestRun(t, BudgetLimits{})
	cp1 := openAndSubmit(t, r2, "p1", "c1")
	displayCurrent(t, r2)
	r2.MarkOutcomeUnknown("tu-f")
	if err := r2.ResolveOutcome("tu-f", OutcomeFailed); err != nil {
		t.Fatalf("ResolveOutcome(tu-f): %v", err)
	}
	displayCurrent(t, r2)
	decideCurrent(t, r2, DecisionContinue)
	if r2.Status != StatusWaitUser {
		t.Fatalf("status after recovery CONTINUE = %s, want WAIT_USER (episode prior)", r2.Status)
	}
	if cp := r2.CurrentCheckpoint(); cp == nil || cp.ID != cp1.ID {
		t.Fatalf("current checkpoint = %+v, want the original %s to resurface (not bypassed)", cp, cp1.ID)
	}
}

// TestM1aRequestReviewStatusGateFirst pins the M1a reorder: the status check
// precedes the same-envelope fast path, so a re-send never succeeds once the
// run left REVIEW_PENDING (CLOSED / WAIT_USER / OUTCOME_UNKNOWN all refuse).
func TestM1aRequestReviewStatusGateFirst(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	if _, err := r.RequestReview(testEnvelope()); err != nil {
		t.Fatalf("RequestReview from ACTIVE: %v", err)
	}
	// Force states a PREPARED review should not authorize a re-send from.
	for _, st := range []RunStatus{StatusClosed, StatusWaitUser, StatusOutcomeUnknown} {
		r.Status = st
		if _, err := r.RequestReview(testEnvelope()); err == nil {
			t.Fatalf("same-envelope re-send with status %s: want error, got nil", st)
		}
	}
	if len(r.Reviews) != 1 || r.Reviews[0].Status != ReviewPrepared {
		t.Fatalf("refused re-sends must leave the review untouched, got %+v", r.Reviews)
	}
}

// TestM1bCountToolCallRequiresOpenPhase pins M1b: the per-phase tool budget
// charges only while a phase is open.
func TestM1bCountToolCallRequiresOpenPhase(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	r.CountToolCall()
	r.CountToolCall()
	if r.Counters.PhaseToolCalls != 0 {
		t.Fatalf("CountToolCall with no open phase: %d, want 0 (no-op)", r.Counters.PhaseToolCalls)
	}
	if err := r.OpenPhase("p1"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	r.CountToolCall()
	if r.Counters.PhaseToolCalls != 1 {
		t.Fatalf("CountToolCall with an open phase: %d, want 1", r.Counters.PhaseToolCalls)
	}
}

// mustJSONStrT marshals v to a string (test-local helper name to avoid
// clashing with the gate package's helper).
func mustJSONStrT(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// TestM8ValidateRunState pins the M8 load-time validation: null, empty, wrong
// schema, id-mismatched, enum-violating and referentially-broken state files
// are corrupt errors — through LoadRun and SessionRuns alike.
func TestM8ValidateRunState(t *testing.T) {
	prev := runStateDirOverride
	runStateDirOverride = t.TempDir()
	t.Cleanup(func() { runStateDirOverride = prev })

	if _, err := StartRun("run-1", "sess-1", "fix", BudgetLimits{}); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	dir := filepath.Join(runStateDirOverride, ProjectKey(), "sess-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := LoadRun("run-1"); err != nil {
		t.Fatalf("healthy state must load: %v", err)
	}

	// A healthy run to mutate into each corruption variant.
	healthy := func() *RunState {
		x, err := LoadRun("run-1")
		if err != nil {
			t.Fatalf("LoadRun base: %v", err)
		}
		return x
	}
	withID := func(id string, mutate func(*RunState)) string {
		x := healthy()
		x.RunID = id
		if mutate != nil {
			mutate(x)
		}
		return mustJSONStrT(t, x)
	}
	old := healthy()
	old.SchemaVersion = "1"
	cases := []struct {
		name   string
		fileID string
		body   string
	}{
		{"null file", "run-null", "null"},
		{"empty object", "run-empty", "{}"},
		{"old schema version", "run-old", mustJSONStrT(t, old)},
		{"run id mismatch", "run-mism", withID("run-mism", func(x *RunState) { x.RunID = "run-somewhere-else" })},
		{"empty session id", "run-sess", withID("run-sess", func(x *RunState) { x.SessionID = "" })},
		{"empty workflow", "run-wf", withID("run-wf", func(x *RunState) { x.Workflow = "" })},
		{"status outside enum", "run-st", withID("run-st", func(x *RunState) { x.Status = RunStatus("PAUSED") })},
		{"dangling checkpoint reference", "run-dangle", withID("run-dangle", func(x *RunState) {
			x.Phases = append(x.Phases, PhaseState{ID: "p9", CheckpointID: "cp-999"})
		})},
	}
	for _, tc := range cases {
		p := filepath.Join(dir, tc.fileID+".json")
		if err := os.WriteFile(p, []byte(tc.body), 0o644); err != nil {
			t.Fatalf("write %s: %v", tc.name, err)
		}
		if _, err := LoadRun(tc.fileID); err == nil || !strings.Contains(err.Error(), "corrupt") {
			t.Errorf("%s: LoadRun = %v, want corrupt-state error", tc.name, err)
		}
		if _, err := SessionRuns("sess-1"); err == nil {
			t.Errorf("%s: SessionRuns must fail closed on a corrupt session file", tc.name)
		}
		if err := os.Remove(p); err != nil {
			t.Fatalf("remove %s: %v", p, err)
		}
	}

	// The session loads again once the corrupt files are gone.
	if runs, err := SessionRuns("sess-1"); err != nil || len(runs) != 1 {
		t.Fatalf("SessionRuns after cleanup = %v, %v; want 1 run, nil", runs, err)
	}
}

// TestM9CommitRollsBackOnSaveFailure pins the M9 commit pattern: a failed
// Save leaves the in-memory state exactly where it was, so a retry after
// repair takes the real path — it cannot masquerade as idempotent success.
func TestM9CommitRollsBackOnSaveFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not block writes")
	}
	r := startTestRun(t, BudgetLimits{})
	dir := filepath.Join(runStateDirOverride, ProjectKey(), "sess-1")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Skipf("chmod: %v", err)
	}

	if _, err := r.RequestReview(testEnvelope()); err == nil {
		t.Fatal("RequestReview with an unwritable state dir must fail")
	}
	if len(r.Reviews) != 0 || r.Status != StatusActive {
		t.Fatalf("memory after failed Save: reviews=%d status=%s — commit must roll back", len(r.Reviews), r.Status)
	}

	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod repair: %v", err)
	}
	if _, err := r.RequestReview(testEnvelope()); err != nil {
		t.Fatalf("retry after repair: %v", err)
	}
	if len(r.Reviews) != 1 || r.Reviews[0].Status != ReviewPrepared {
		t.Fatalf("reviews after retry = %+v, want one PREPARED", r.Reviews)
	}
	disk, err := LoadRun("run-1")
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if len(disk.Reviews) != 1 || disk.Status != StatusReviewPending {
		t.Fatalf("disk after retry = %+v / %s — the retry must persist for real", disk.Reviews, disk.Status)
	}
}

// TestM10StaleClearedOnReaccept pins M10: a criterion that went stale regains
// validity when its phase is redone and re-accepted at a checkpoint.
func TestM10StaleClearedOnReaccept(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	openAndSubmit(t, r, "p1", "c1")
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue) // c1 accepted

	// A requirement delta marks c1 stale.
	if err := r.OpenRequirementChange("format change"); err != nil {
		t.Fatalf("OpenRequirementChange: %v", err)
	}
	if _, err := r.ApplyRequirementDelta(RequirementDelta{Note: "csv", Affected: []string{"c1"}}); err != nil {
		t.Fatalf("ApplyRequirementDelta: %v", err)
	}
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionAcceptRequirementChange)
	if !containsID(r.Stale, "c1") {
		t.Fatalf("Stale = %v, want c1 stale after the delta", r.Stale)
	}

	// Redo p1 (recall from p2's checkpoint) and re-accept it.
	openAndSubmit(t, r, "p2", "c2")
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionRecall)
	sub := PhaseSubmission{
		PhaseID:     "p1",
		Summary:     "p1 redone",
		CriteriaIDs: []string{"c1"},
		Artifacts:   []ArtifactRef{{Path: "p1.md", Digest: "d2", Kind: "doc"}},
	}
	cp, err := r.SubmitPhase(sub)
	if err != nil {
		t.Fatalf("resubmit p1: %v", err)
	}
	if err := r.MarkDisplayed(cp.ID, CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	if err := r.RecordDecision(cp.ID, DecisionContinue); err != nil {
		t.Fatalf("RecordDecision(p1 redo): %v", err)
	}
	if containsID(r.Stale, "c1") {
		t.Fatalf("Stale = %v — re-accepting the phase must clear its own criteria", r.Stale)
	}
	// M10 is selective: p2's criterion was staled by the recall, not re-accepted.
	if !containsID(r.Stale, "c2") {
		t.Fatalf("Stale = %v, want c2 still stale (recall propagation)", r.Stale)
	}
}

// TestM11RecallAppendsNewVisit pins M11: recall keeps the old visit (marked
// recalled) and appends a fresh open visit of the same id; the checkpoint
// history stays complete and "the phase" resolves to the latest visit.
func TestM11RecallAppendsNewVisit(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	for _, id := range []string{"p1", "p2"} {
		openAndSubmit(t, r, id, "c-"+id)
		displayCurrent(t, r)
		decideCurrent(t, r, DecisionContinue)
	}
	cp3 := openAndSubmit(t, r, "p3", "c-p3")
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionRecall)

	if n := visitCount(r, "p2"); n != 2 {
		t.Fatalf("p2 visits = %d, want 2 (old kept + fresh appended)", n)
	}
	if got := lastVisit(t, r, "p2"); !got.Open || got.Outcome != "" || got.ClosedAt != "" {
		t.Fatalf("p2 latest visit = %+v, want open/undecided", got)
	}
	if got := phaseByID(t, r, "p2"); got.Open || got.Outcome != "recalled" {
		t.Fatalf("p2 original visit = %+v, want closed/recalled", got)
	}
	// The checkpoint history is complete: all three checkpoints remain.
	if len(r.Checkpoints) != 3 || r.Checkpoints[2].ID != cp3.ID {
		t.Fatalf("checkpoints = %+v, want all three kept", r.Checkpoints)
	}
	if len(r.Recalls) != 1 || r.Recalls[0].TargetPhaseID != "p2" || r.Recalls[0].FromPhaseID != "p3" {
		t.Fatalf("recalls = %+v", r.Recalls)
	}
	if ph := r.openPhaseState(); ph == nil || ph.ID != "p2" {
		t.Fatalf("open phase = %+v, want the fresh p2 visit", ph)
	}
}

// TestM12BlockedCompletedExclusivity pins M12: BLOCKED must not pose as a
// quality verdict and COMPLETED must not carry a reason code — both are
// protocol errors routed through the ReviewFailed recovery path.
func TestM12BlockedCompletedExclusivity(t *testing.T) {
	pendingReview := func(t *testing.T) *RunState {
		t.Helper()
		r := startTestRun(t, BudgetLimits{})
		openAndSubmit(t, r, "p1", "c1")
		displayCurrent(t, r)
		decideCurrent(t, r, DecisionContinue)
		if _, err := r.RequestReview(testEnvelope()); err != nil {
			t.Fatalf("RequestReview: %v", err)
		}
		return r
	}

	// BLOCKED with a quality verdict: protocol error.
	r := pendingReview(t)
	if err := r.RecordReviewResult(ReviewResult{ReviewID: "rev-1", RunStatus: ReviewRunBlocked, ReasonCode: "L3", Verdict: VerdictPass}); err == nil {
		t.Fatal("BLOCKED with a verdict: want protocol error, got nil")
	}
	if r.Reviews[0].Status != ReviewFailed {
		t.Fatalf("review = %s, want FAILED", r.Reviews[0].Status)
	}
	if r.Status != StatusWaitUser || r.CurrentCheckpoint() == nil || r.CurrentCheckpoint().Kind != CheckpointReviewResult {
		t.Fatalf("protocol error must route to a REVIEW_RESULT checkpoint, status=%s cp=%+v", r.Status, r.CurrentCheckpoint())
	}

	// COMPLETED with a reason code: protocol error.
	r2 := pendingReview(t)
	if err := r2.RecordReviewResult(ReviewResult{ReviewID: "rev-1", RunStatus: ReviewRunCompleted, Verdict: VerdictPass, ReasonCode: "leftover"}); err == nil {
		t.Fatal("COMPLETED with a reason_code: want protocol error, got nil")
	}
	if r2.Reviews[0].Status != ReviewFailed {
		t.Fatalf("review = %s, want FAILED", r2.Reviews[0].Status)
	}

	// A clean BLOCKED result notes BLOCKED (plus reason) without a verdict.
	r3 := pendingReview(t)
	if err := r3.RecordReviewResult(ReviewResult{ReviewID: "rev-1", RunStatus: ReviewRunBlocked, ReasonCode: "L3-UNAVAILABLE"}); err != nil {
		t.Fatalf("clean BLOCKED: %v", err)
	}
	cp := r3.CurrentCheckpoint()
	if cp == nil {
		t.Fatal("clean BLOCKED must open a REVIEW_RESULT checkpoint")
	}
	if !strings.Contains(cp.Note, ReviewRunBlocked) || strings.Contains(cp.Note, VerdictPass) || strings.Contains(cp.Note, VerdictNeedsFix) {
		t.Fatalf("BLOCKED note = %q, want run status only (no verdict)", cp.Note)
	}
}

// TestMinorNonceRedrawsOnCollision pins m1: a nonce colliding with an existing
// checkpoint nonce in the run is re-drawn, at most 8 times.
func TestMinorNonceRedrawsOnCollision(t *testing.T) {
	draws := 0
	n, err := drawNonce(func(string) bool { draws++; return draws <= 3 })
	if err != nil {
		t.Fatalf("drawNonce: %v", err)
	}
	if draws != 4 || n == "" {
		t.Fatalf("draws = %d nonce = %q, want the 4th draw accepted", draws, n)
	}

	if _, err := drawNonce(func(string) bool { return true }); err == nil {
		t.Fatal("drawNonce with everything rejected: want error after 8 attempts")
	}

	// The run-level nonce never duplicates an existing checkpoint nonce.
	r := startTestRun(t, BudgetLimits{})
	if err := r.OpenPhase("p1"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	cp, err := r.SubmitPhase(PhaseSubmission{PhaseID: "p1"})
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	for _, existing := range r.Checkpoints {
		if existing.ID != cp.ID && existing.Nonce == cp.Nonce {
			t.Fatalf("nonce collision between %s and %s", existing.ID, cp.ID)
		}
	}
}

// checkpointByID returns the checkpoint state with that id.
func checkpointByID(t *testing.T, r *RunState, id string) CheckpointState {
	t.Helper()
	for _, cp := range r.Checkpoints {
		if cp.ID == id {
			return cp
		}
	}
	t.Fatalf("checkpoint %s not found in %+v", id, r.Checkpoints)
	return CheckpointState{}
}

// --- task 2.5 review round 1 ------------------------------------------------------

// TestF1RecoveryRecallSupersedesOrphanedCheckpoint pins F1: a RECALL decided
// at a recovery checkpoint sits ABOVE the phase visit's own still-pending
// checkpoint; marking the visit recalled must also supersede that checkpoint
// — otherwise it resurfaces as an orphan in ACTIVE, where it can never be
// decided (RecordDecision requires WAIT_USER), wedging the change loop.
func TestF1RecoveryRecallSupersedesOrphanedCheckpoint(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	openAndSubmit(t, r, "p1", "c-1")
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue) // p1 accepted
	cp2 := openAndSubmit(t, r, "p2", "c-2")
	displayCurrent(t, r) // cp-2 pending, WAIT_USER

	// A failed probe episode parks the run at a recovery checkpoint above the
	// still-pending cp-2.
	r.MarkOutcomeUnknown("tu-1")
	if err := r.ResolveOutcome("tu-1", OutcomeFailed); err != nil {
		t.Fatalf("ResolveOutcome(failed): %v", err)
	}
	if cp := r.CurrentCheckpoint(); cp == nil || cp.Kind != CheckpointRecovery {
		t.Fatalf("want a pending recovery checkpoint, got %+v", cp)
	}
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionRecall) // recall targets the accepted p1

	// cp-2 is superseded, not orphaned.
	if got := checkpointByID(t, r, cp2.ID); !got.Superseded {
		t.Fatalf("cp-2 = %+v — the recalled visit's pending checkpoint must be superseded (F1)", got)
	}
	if cur := r.CurrentCheckpoint(); cur != nil {
		t.Fatalf("CurrentCheckpoint = %s, want nil after the recovery-RECALL", cur.ID)
	}
	if got := lastVisit(t, r, "p2"); got.Outcome != "recalled" {
		t.Fatalf("p2 latest visit = %+v, want recalled", got)
	}
	if got := lastVisit(t, r, "p1"); !got.Open {
		t.Fatalf("p1 latest visit = %+v, want reopened by the recall", got)
	}

	// The run stays usable: the change loop is reachable again instead of
	// wedged behind an undecidable orphan.
	if err := r.OpenRequirementChange("post-recovery adjustment"); err != nil {
		t.Fatalf("OpenRequirementChange after recovery-RECALL: %v", err)
	}
	if _, err := r.ApplyRequirementDelta(RequirementDelta{Note: "n", Affected: []string{"c-2"}}); err != nil {
		t.Fatalf("ApplyRequirementDelta after recovery-RECALL: %v (an orphaned checkpoint wedges the loop)", err)
	}
}

// TestF1DirectPhaseRecallLeavesCheckpointDecided pins the distinction: a
// RECALL decided ON the phase's own checkpoint (PHASE kind) needs no orphan
// handling — that checkpoint is decided normally, never superseded.
func TestF1DirectPhaseRecallLeavesCheckpointDecided(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	openAndSubmit(t, r, "p1", "c-1")
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue)
	cp2 := openAndSubmit(t, r, "p2", "c-2")
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionRecall) // RECALL decided ON cp-2

	if got := checkpointByID(t, r, cp2.ID); !got.Decided || got.Superseded {
		t.Fatalf("cp-2 = decided:%v superseded:%v — a direct phase RECALL decides its checkpoint", got.Decided, got.Superseded)
	}
	if cur := r.CurrentCheckpoint(); cur != nil {
		t.Fatalf("CurrentCheckpoint = %s, want nil after the direct RECALL", cur.ID)
	}
	if got := lastVisit(t, r, "p1"); !got.Open {
		t.Fatalf("p1 latest visit = %+v, want reopened", got)
	}
	if got := lastVisit(t, r, "p2"); got.Outcome != "recalled" {
		t.Fatalf("p2 latest visit = %+v, want recalled", got)
	}
}

// TestF1ReviewResultRecallDoesNotTouchDecidedCheckpoint pins the other side of
// the F1 distinction: when the fallback-recalled visit's checkpoint was
// already decided (e.g. [v] REVIEW_ON_DEMAND), a later RECALL at the
// review-result checkpoint must NOT supersede it.
func TestF1ReviewResultRecallDoesNotTouchDecidedCheckpoint(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	openAndSubmit(t, r, "p1", "c-1")
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue) // p1 accepted — the recall target
	cp2 := openAndSubmit(t, r, "p2", "c-2")
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionReviewOnDemand) // cp-2 decided [v]
	if _, err := r.RequestReview(testEnvelope()); err != nil {
		t.Fatalf("RequestReview: %v", err)
	}
	if err := r.RecordReviewResult(ReviewResult{ReviewID: "rev-1", RunStatus: ReviewRunCompleted, Verdict: VerdictPass}); err != nil {
		t.Fatalf("RecordReviewResult: %v", err)
	}
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionRecall) // at the REVIEW_RESULT checkpoint

	if got := checkpointByID(t, r, cp2.ID); !got.Decided || got.Superseded {
		t.Fatalf("cp-2 = decided:%v superseded:%v — a checkpoint decided via [v] must not be superseded", got.Decided, got.Superseded)
	}
	if got := lastVisit(t, r, "p2"); got.Outcome != "recalled" {
		t.Fatalf("p2 latest visit = %+v, want recalled", got)
	}
	if got := lastVisit(t, r, "p1"); !got.Open {
		t.Fatalf("p1 latest visit = %+v, want reopened", got)
	}
}

// TestF2StartRunRejectsEmptyWorkflow pins F2: StartRun must refuse an empty
// workflow (and empty ids) — a written file validateRunState rejects would
// brick the session's next SessionRuns.
func TestF2StartRunRejectsEmptyWorkflow(t *testing.T) {
	prev := runStateDirOverride
	runStateDirOverride = t.TempDir()
	t.Cleanup(func() { runStateDirOverride = prev })

	if _, err := StartRun("run-1", "sess-1", "", BudgetLimits{}); err == nil {
		t.Fatal("StartRun with empty workflow: want error, got nil")
	}
	if _, err := LoadRun("run-1"); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("LoadRun after the refused StartRun = %v, want ErrRunNotFound (no file written)", err)
	}

	// Empty ids were already guarded by validID — pinned here with the guard.
	for _, bad := range []struct{ runID, sessionID string }{{"", "sess-1"}, {"run-1", ""}} {
		if _, err := StartRun(bad.runID, bad.sessionID, "fix", BudgetLimits{}); err == nil {
			t.Fatalf("StartRun(%q, %q): want error, got nil", bad.runID, bad.sessionID)
		}
	}
}

// TestStartDelegationEnforcesOwnBudget pins the T3 handoff 14(a): the reducer
// enforces the delegation budget itself — the gate's check is not the only
// guard, so direct reducer callers cannot exceed the limit either.
func TestStartDelegationEnforcesOwnBudget(t *testing.T) {
	r := startTestRun(t, BudgetLimits{Delegations: 1})
	if err := r.OpenPhase("p1"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	if err := r.StartDelegation("", false); err != nil {
		t.Fatalf("first delegation within budget: %v", err)
	}
	if err := r.StartDelegation("", false); err == nil {
		t.Fatal("StartDelegation past the delegation limit: want error (reducer-level guard), got nil")
	}
	if r.Counters.Delegations != 1 {
		t.Fatalf("delegations = %d, want 1 (the refused call must not consume)", r.Counters.Delegations)
	}

	// The reviewer path is guarded by the same budget.
	if _, err := r.RequestReview(testEnvelope()); err != nil {
		t.Fatalf("RequestReview: %v", err)
	}
	if err := r.StartDelegation("rev-1", true); err == nil {
		t.Fatal("reviewer StartDelegation past the limit: want error, got nil")
	}
	if r.Reviews[0].Status != ReviewPrepared {
		t.Fatalf("review = %s, want PREPARED (refused dispatch must not start it)", r.Reviews[0].Status)
	}
}
