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

	// All three probe branches are legal (spec §5).
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
		if r2.Status != StatusActive {
			t.Errorf("branch %s: status after resolve = %s, want ACTIVE", want, r2.Status)
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

	// Two unknowns: the run stays OUTCOME_UNKNOWN until the last one resolves.
	r2.ResolveOutcome("tu-bad", "indeterminate")
	r2.MarkOutcomeUnknown("tu-a")
	r2.MarkOutcomeUnknown("tu-b")
	if err := r2.ResolveOutcome("tu-a", "accepted"); err != nil {
		t.Fatalf("ResolveOutcome(tu-a): %v", err)
	}
	if r2.Status != StatusOutcomeUnknown {
		t.Errorf("status = %s while one unknown remains, want OUTCOME_UNKNOWN", r2.Status)
	}
	if err := r2.ResolveOutcome("tu-b", "indeterminate"); err != nil {
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
	if err := r.OpenPhase("p1"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
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

	// Direct resume path (no confirm checkpoint in between).
	r.DevPosition.CurrentTask = "mid-change cleanup"
	if err := r.OpenRequirementChange("follow-up tweak"); err != nil {
		t.Fatalf("OpenRequirementChange: %v", err)
	}
	if err := r.ResumeAfterChange(); err != nil {
		t.Fatalf("ResumeAfterChange: %v", err)
	}
	if r.Status != StatusActive || r.Change != nil || r.DevPosition.CurrentTask != "mid-change cleanup" {
		t.Errorf("direct resume: status=%s change=%v pos=%+v", r.Status, r.Change, r.DevPosition)
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
	if got := phaseByID(t, r, "p2"); !got.Open {
		t.Error("RECALL must reopen the most recent accepted prior phase (p2)")
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

// TestRequirementChangeFromCheckpoint pins the controller ruling: a change may
// open at a pending checkpoint (WAIT_USER entry); ResumeAfterChange refuses
// whenever any checkpoint awaits decision — acceptance only via
// RecordDecision(ACCEPT_REQUIREMENT_CHANGE).
func TestRequirementChangeFromCheckpoint(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	openAndSubmit(t, r, "p1", "c1")
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
	if r.Status != StatusWaitUser || r.CurrentCheckpoint() == nil {
		t.Error("the pending checkpoint must remain owed while the change is open")
	}

	// Direct resume is refused while that checkpoint awaits decision.
	if err := r.ResumeAfterChange(); err == nil {
		t.Error("ResumeAfterChange with pending checkpoint: want error, got nil")
	}

	// The user decides the pending checkpoint; the change stays open.
	decideCurrent(t, r, DecisionContinue)
	if r.Status != StatusActive || r.Change == nil {
		t.Fatalf("after deciding p1: status=%s change=%v, want ACTIVE with change open", r.Status, r.Change)
	}
	// Now direct resume works and restores the frozen position.
	if err := r.ResumeAfterChange(); err != nil {
		t.Fatalf("ResumeAfterChange after checkpoint decided: %v", err)
	}
	if r.Change != nil || r.Status != StatusActive || r.DevPosition.CurrentTask != "mid-phase" {
		t.Errorf("resume: change=%v status=%s pos=%+v", r.Change, r.Status, r.DevPosition)
	}

	// A pending CHANGE_CONFIRM checkpoint also refuses direct resume:
	// acceptance only via RecordDecision(ACCEPT_REQUIREMENT_CHANGE).
	if err := r.OpenRequirementChange("second change"); err != nil {
		t.Fatalf("OpenRequirementChange: %v", err)
	}
	if _, err := r.ApplyRequirementDelta(RequirementDelta{Note: "n", Affected: []string{"c1"}}); err != nil {
		t.Fatalf("ApplyRequirementDelta: %v", err)
	}
	if err := r.ResumeAfterChange(); err == nil {
		t.Error("ResumeAfterChange with pending confirm checkpoint: want error, got nil")
	}
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionAcceptRequirementChange)
	if r.Change != nil || r.Status != StatusActive {
		t.Errorf("after ACCEPT: change=%v status=%s", r.Change, r.Status)
	}
}
