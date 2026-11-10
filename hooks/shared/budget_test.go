package shared

// budget_test.go — T18 unit suite: the budget ride-along channel
// (ParseBudgetRideAlongs), the settlement writers (SetBudgetLimits /
// ExtendBudget / RecordDecisionWithBudget atomicity), the digest-bound OFFER
// vs applied-LIMITS distinction, the manifest suggested_budgets projection,
// and the post-game closure ledger (markers, journal reconciliation, the sole
// CLOSED writer). The real-binary cycles live in hooks/contract.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// budgetAskQuestions builds the two budget ride-along question shapes against
// a checkpoint's persisted asks (order-free variants included).
func budgetAskQuestions(cp CheckpointState) []interface{} {
	return []interface{}{
		map[string]interface{}{
			"question": "Budget confirmation for this run?",
			"options": []interface{}{
				map[string]interface{}{"label": cp.BudgetAsk.ConfirmLabel},
				map[string]interface{}{"label": cp.BudgetAsk.UnlimitedLabel},
			},
		},
	}
}

func extendAskQuestions(cp CheckpointState) []interface{} {
	return []interface{}{
		map[string]interface{}{
			"question": "Budget exhausted: extend?",
			"options": []interface{}{
				map[string]interface{}{"label": cp.ExtendAsk.ExtendLabel},
				map[string]interface{}{"label": cp.ExtendAsk.HoldLabel},
			},
		},
	}
}

func TestParseBudgetRideAlongsShape(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	cp := openAndSubmit(t, r, "context")
	if cp.BudgetAsk == nil {
		t.Fatal("the first checkpoint must carry the persisted budget ask")
	}

	// No budget questions → nil, no problem.
	if rides, problem := ParseBudgetRideAlongs(&cp, nil); problem != "" || rides != nil {
		t.Fatalf("no questions: (%v, %q), want (nil, \"\")", rides, problem)
	}
	ordinary := []interface{}{map[string]interface{}{
		"question": "plain question",
		"options": []interface{}{
			map[string]interface{}{"label": "a"},
			map[string]interface{}{"label": "b"},
		},
	}}
	if rides, problem := ParseBudgetRideAlongs(&cp, ordinary); problem != "" || rides != nil {
		t.Fatalf("ordinary questions: (%v, %q), want (nil, \"\")", rides, problem)
	}

	// Well-formed budget ride (either label order).
	rides, problem := ParseBudgetRideAlongs(&cp, budgetAskQuestions(cp))
	if problem != "" || len(rides) != 1 || rides[0].Kind != "budget" {
		t.Fatalf("well-formed budget ride: (%+v, %q)", rides, problem)
	}
	swapped := []interface{}{map[string]interface{}{
		"question": "Budget confirmation for this run?",
		"options": []interface{}{
			map[string]interface{}{"label": cp.BudgetAsk.UnlimitedLabel},
			map[string]interface{}{"label": cp.BudgetAsk.ConfirmLabel},
		},
	}}
	if _, problem := ParseBudgetRideAlongs(&cp, swapped); problem != "" {
		t.Fatalf("swapped label order must pass (set-equal pair): %q", problem)
	}

	// Foreign labels — a label-parsed number must never launder through.
	foreign := []interface{}{map[string]interface{}{
		"question": "Budget?",
		"options": []interface{}{
			map[string]interface{}{"label": "budget: 50"},
			map[string]interface{}{"label": cp.BudgetAsk.UnlimitedLabel},
		},
	}}
	if _, problem := ParseBudgetRideAlongs(&cp, foreign); problem == "" || !strings.Contains(problem, "persisted pointer labels") {
		t.Fatalf("foreign label pair must reject with the pointer-label rule, got %q", problem)
	}

	// Nonce-carrying ride: the nonce binds the DECISION question.
	withNonce := []interface{}{map[string]interface{}{
		"question": "Budget? nonce " + cp.Nonce,
		"options": []interface{}{
			map[string]interface{}{"label": cp.BudgetAsk.ConfirmLabel},
			map[string]interface{}{"label": cp.BudgetAsk.UnlimitedLabel},
		},
	}}
	if _, problem := ParseBudgetRideAlongs(&cp, withNonce); problem == "" || !strings.Contains(problem, "nonce") {
		t.Fatalf("nonce-carrying budget question must reject, got %q", problem)
	}

	// Two budget questions in one call.
	doubled := append(budgetAskQuestions(cp), budgetAskQuestions(cp)...)
	if _, problem := ParseBudgetRideAlongs(&cp, doubled); problem == "" || !strings.Contains(problem, "two budget questions") {
		t.Fatalf("two budget questions must reject, got %q", problem)
	}

	// Three options on the ride.
	three := []interface{}{map[string]interface{}{
		"question": "Budget?",
		"options": []interface{}{
			map[string]interface{}{"label": cp.BudgetAsk.ConfirmLabel},
			map[string]interface{}{"label": cp.BudgetAsk.UnlimitedLabel},
			map[string]interface{}{"label": "budget: other"},
		},
	}}
	if _, problem := ParseBudgetRideAlongs(&cp, three); problem == "" || !strings.Contains(problem, "exactly two options") {
		t.Fatalf("three options must reject, got %q", problem)
	}

	// A budget ride at a checkpoint with NO persisted ask (already settled).
	noAsk := cp
	noAsk.BudgetAsk = nil
	if _, problem := ParseBudgetRideAlongs(&noAsk, budgetAskQuestions(cp)); problem == "" || !strings.Contains(problem, "no persisted budget ask") {
		t.Fatalf("ride without a persisted ask must reject, got %q", problem)
	}

	// An extend ride with a persisted offer passes; without one it rejects.
	withOffer := cp
	withOffer.ExtendAsk = &ExtendAsk{Delta: BudgetLimits{PhaseToolCalls: 5}, ExtendLabel: BudgetExtendLabel, HoldLabel: BudgetHoldLabel}
	if _, problem := ParseBudgetRideAlongs(&withOffer, extendAskQuestions(withOffer)); problem != "" {
		t.Fatalf("well-formed extend ride must pass, got %q", problem)
	}
	cp2 := cp
	cp2.ExtendAsk = nil
	if _, problem := ParseBudgetRideAlongs(&cp2, extendAskQuestions(withOffer)); problem == "" || !strings.Contains(problem, "no persisted extension offer") {
		t.Fatalf("extend ride without an offer must reject, got %q", problem)
	}
}

// firstDisplayedCheckpoint settles nothing: the run starts, its first
// checkpoint opens (framework unlimited ask — no manifest in the temp dir)
// and the display is bound.
func firstDisplayedCheckpoint(t *testing.T, r *RunState) *CheckpointState {
	t.Helper()
	openAndSubmit(t, r, "context")
	displayCurrent(t, r)
	cp := r.CurrentCheckpoint()
	if cp == nil {
		t.Fatal("no open checkpoint")
	}
	return cp
}

func TestSetBudgetLimitsSoleWriter(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	cp := firstDisplayedCheckpoint(t, r)

	// Negative values are corruption.
	if _, err := r.SetBudgetLimits(BudgetLimits{PhaseToolCalls: -1}, "tu-1"); err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("negative limits must reject, got %v", err)
	}

	// Not displayed → refuse (the budget question rides a display-verified ask).
	rUndisplayed := startFreshUndisplayed(t)
	if _, err := rUndisplayed.SetBudgetLimits(BudgetLimits{}, "tu-x"); err == nil || !strings.Contains(err.Error(), "not displayed") {
		t.Fatalf("undisplayed checkpoint must refuse settlement, got %v", err)
	}

	// The real settlement.
	applied, err := r.SetBudgetLimits(BudgetLimits{PhaseToolCalls: 3}, "tu-1")
	if err != nil || !applied {
		t.Fatalf("SetBudgetLimits: applied=%v err=%v", applied, err)
	}
	if !r.BudgetConfirmed || r.BudgetSettledBy != "tu-1" || r.Limits.PhaseToolCalls != 3 {
		t.Fatalf("settlement state: confirmed=%v by=%q limits=%+v", r.BudgetConfirmed, r.BudgetSettledBy, r.Limits)
	}
	// Idempotent: the FIRST human answer governs — a later call never rewrites.
	applied, err = r.SetBudgetLimits(BudgetLimits{Delegations: 9}, "tu-2")
	if err != nil || applied {
		t.Fatalf("second settlement must be a silent no-op: applied=%v err=%v", applied, err)
	}
	if r.Limits.PhaseToolCalls != 3 || r.Limits.Delegations != 0 {
		t.Fatalf("limits rewritten after confirmation: %+v", r.Limits)
	}
	// Replay of the settling call: silent.
	if applied, err := r.SetBudgetLimits(BudgetLimits{PhaseToolCalls: 3}, "tu-1"); err != nil || applied {
		t.Fatalf("replay must be a no-op: applied=%v err=%v", applied, err)
	}
	_ = cp
}

// startFreshUndisplayed opens the first checkpoint WITHOUT binding display.
func startFreshUndisplayed(t *testing.T) *RunState {
	t.Helper()
	r := startTestRun(t, BudgetLimits{})
	openAndSubmit(t, r, "context")
	return r
}

func TestSetBudgetLimitsRequiresPersistedAsk(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	firstDisplayedCheckpoint(t, r)
	// Leave the budget UNSETTLED (the reducer itself does not enforce the
	// post-hoc rule — that is the hook's job) and reach the second checkpoint:
	// no BudgetAsk persists there, so settlement has no surface.
	if err := r.RecordDecision(r.CurrentCheckpoint().ID, DecisionContinue, "tu-1"); err != nil {
		t.Fatal(err)
	}
	openAndSubmit(t, r, "diagnosis")
	displayCurrent(t, r)
	if _, err := r.SetBudgetLimits(BudgetLimits{}, "tu-2"); err == nil || !strings.Contains(err.Error(), "no persisted budget ask") {
		t.Fatalf("settlement at a non-first checkpoint must reject, got %v", err)
	}
}

func TestExtendBudgetAppliesPersistedDelta(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	firstDisplayedCheckpoint(t, r)
	if _, err := r.SetBudgetLimits(BudgetLimits{PhaseToolCalls: 2, Delegations: 1}, "tu-b"); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordDecision(r.CurrentCheckpoint().ID, DecisionContinue, "tu-b"); err != nil {
		t.Fatal(err)
	}

	// Burn to exhaustion, submit, and open the exhaustion checkpoint.
	if err := r.OpenPhase("diagnosis"); err != nil {
		t.Fatal(err)
	}
	r.CountToolCall()
	r.CountToolCall()
	if ok, _ := r.BudgetExhausted(); !ok {
		t.Fatal("expected exhaustion before the extension")
	}
	cp, err := r.SubmitPhase(PhaseSubmission{PhaseID: "diagnosis"})
	if err != nil {
		t.Fatal(err)
	}
	if cp.ExtendAsk == nil {
		t.Fatal("the exhaustion checkpoint must carry the persisted extension offer")
	}
	if cp.ExtendAsk.Delta != (BudgetLimits{PhaseToolCalls: 2, Delegations: 1}) {
		t.Fatalf("delta = %+v, want the current limit per set axis", cp.ExtendAsk.Delta)
	}
	displayCurrent(t, r)

	extended, err := r.ExtendBudget("tu-ext-1")
	if err != nil || !extended {
		t.Fatalf("ExtendBudget: extended=%v err=%v", extended, err)
	}
	if r.Limits.PhaseToolCalls != 4 || r.Limits.Delegations != 2 {
		t.Fatalf("limits after extension = %+v, want {4,0,0,2}", r.Limits)
	}
	// Replay of the same call: no double extension.
	extended, err = r.ExtendBudget("tu-ext-1")
	if err != nil || extended || r.Limits.PhaseToolCalls != 4 || len(r.BudgetExtensions) != 1 {
		t.Fatalf("replay must not extend twice: extended=%v limits=%+v receipts=%d err=%v", extended, r.Limits, len(r.BudgetExtensions), err)
	}
	// The receipt pins the persisted delta that was actually applied.
	if got := r.BudgetExtensions[0]; got.ToolUseID != "tu-ext-1" || got.Delta != (BudgetLimits{PhaseToolCalls: 2, Delegations: 1}) {
		t.Fatalf("extension receipt = %+v, want the persisted delta bound to the answering call", got)
	}
	// A FRESH human answer may extend again (extensions are repeatable; only
	// re-delivered callbacks are not).
	if extended, err := r.ExtendBudget("tu-ext-2"); err != nil || !extended || r.Limits.PhaseToolCalls != 6 {
		t.Fatalf("a fresh answer must extend again: extended=%v limits=%+v err=%v", extended, r.Limits, err)
	}
}

func TestRecordDecisionWithBudgetAtomicOnSaveFailure(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	cp := firstDisplayedCheckpoint(t, r)

	// Make Save fail: the run's session directory becomes read-only, so the
	// commit's temp-file write fails AFTER the in-snapshot effects ran.
	sessDir := filepath.Join(runStateDirOverride, ProjectKey(), "sess-1")
	if err := os.Chmod(sessDir, 0o500); err != nil {
		t.Skipf("cannot chmod the state dir here: %v", err)
	}
	t.Cleanup(func() { os.Chmod(sessDir, 0o755) })

	lim := BudgetLimits{PhaseToolCalls: 7}
	err := r.RecordDecisionWithBudget(cp.ID, DecisionContinue, "tu-1", &BudgetSettlement{ToolUseID: "tu-1", Confirm: &lim})
	if err == nil {
		t.Fatal("RecordDecisionWithBudget must surface the Save failure")
	}
	// Repair and reload: NEITHER the decision NOR the settlement persisted —
	// state authority never half-applies one human callback.
	if err := os.Chmod(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	disk, derr := LoadRun("run-1")
	if derr != nil {
		t.Fatal(derr)
	}
	if disk.Status != StatusWaitUser || disk.Checkpoints[0].Decided || disk.BudgetConfirmed {
		t.Fatalf("half-applied callback on disk: status %s decided %v confirmed %v", disk.Status, disk.Checkpoints[0].Decided, disk.BudgetConfirmed)
	}
}

func TestRecordDecisionWithBudgetValidatesBeforeRecording(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	cp := firstDisplayedCheckpoint(t, r)

	// A settlement without a tool_use_id is malformed: nothing records.
	if err := r.RecordDecisionWithBudget(cp.ID, DecisionContinue, "tu-1", &BudgetSettlement{Confirm: &BudgetLimits{}}); err == nil {
		t.Fatal("settlement without tool_use_id must reject")
	}
	// A negative settlement value: nothing records, the checkpoint stays open.
	neg := BudgetLimits{RunRework: -3}
	if err := r.RecordDecisionWithBudget(cp.ID, DecisionContinue, "tu-1", &BudgetSettlement{ToolUseID: "tu-1", Confirm: &neg}); err == nil {
		t.Fatal("negative settlement must reject")
	}
	if r.Checkpoints[0].Decided || r.BudgetConfirmed {
		t.Fatal("a rejected combined callback must leave nothing recorded")
	}

	// The happy path: settlement + decision in one commit.
	lim := BudgetLimits{FailedChecks: 2}
	if err := r.RecordDecisionWithBudget(cp.ID, DecisionContinue, "tu-1", &BudgetSettlement{ToolUseID: "tu-1", Confirm: &lim}); err != nil {
		t.Fatalf("combined settlement+decision: %v", err)
	}
	if r.Status != StatusActive || !r.BudgetConfirmed || r.Limits.FailedChecks != 2 {
		t.Fatalf("combined result: status %s confirmed %v limits %+v", r.Status, r.BudgetConfirmed, r.Limits)
	}
}

// The OFFER is digest-bound content; the APPLIED limits/counters are not
// (controller clarification 2): settling the budget must never invalidate the
// display binding, but changing the persisted offer after display must.
func TestBudgetOfferDigestBoundAppliedLimitsNot(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	cp := *firstDisplayedCheckpoint(t, r)
	digest := CheckpointContentDigest(cp, r.RequirementsVersion)

	// Applied limits + counters leave the digest untouched.
	r.Limits.PhaseToolCalls = 99
	r.Counters.PhaseToolCalls = 5
	if got := CheckpointContentDigest(cp, r.RequirementsVersion); got != digest {
		t.Fatal("applied Limits/Counters must NOT be digest-bound checkpoint content")
	}

	// A changed OFFER (proposal or source) invalidates the binding.
	changed := cp
	changed.BudgetAsk.Proposal.Limits.PhaseToolCalls = 4
	if got := CheckpointContentDigest(changed, r.RequirementsVersion); got == digest {
		t.Fatal("a changed budget proposal must change the digest (stale offer → re-render)")
	}
	changed = cp
	changed.BudgetAsk.Proposal.Source = "manifest"
	if got := CheckpointContentDigest(changed, r.RequirementsVersion); got == digest {
		t.Fatal("a changed proposal source must change the digest")
	}

	// The stale-offer twin end-to-end: display binds, the offer changes, the
	// combined callback rolls back WHOLLY (decision AND settlement).
	r2 := startTestRun(t, BudgetLimits{})
	cp2 := firstDisplayedCheckpoint(t, r2)
	// Simulate an offer change after display (a rewrite of persisted content).
	cur := r2.CurrentCheckpoint()
	cur.BudgetAsk.Proposal.Limits.PhaseToolCalls = 12
	if err := r2.Save(); err != nil {
		t.Fatal(err)
	}
	lim := BudgetLimits{}
	if err := r2.RecordDecisionWithBudget(cp2.ID, DecisionContinue, "tu-1", &BudgetSettlement{ToolUseID: "tu-1", Confirm: &lim}); err == nil || !strings.Contains(err.Error(), "content changed since display") {
		t.Fatalf("stale offer must refuse the decision, got %v", err)
	}
	disk, err := LoadRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	if disk.Status != StatusWaitUser || disk.BudgetConfirmed || disk.Checkpoints[0].Decided {
		t.Fatalf("stale-offer callback half-applied: status %s confirmed %v decided %v", disk.Status, disk.BudgetConfirmed, disk.Checkpoints[0].Decided)
	}
}

func TestManifestSuggestedBudgetsProjection(t *testing.T) {
	// Full axes project through.
	writeProjectManifest(t, `version: "2"
project:
  name: budget-probe
phases:
  diagnosis:
    skill: none
suggested_budgets:
  phase_tool_calls: 40
  failed_checks: 3
  run_rework: 2
  delegations: 1
  check_timeout_seconds: 120
`)
	m, err := LoadRuntimeManifest()
	if err != nil {
		t.Fatal(err)
	}
	if m.SuggestedBudgets == nil || *m.SuggestedBudgets != (BudgetLimits{PhaseToolCalls: 40, FailedChecks: 3, RunRework: 2, Delegations: 1}) {
		t.Fatalf("projection = %+v, want the four consumed axes (timeout tolerated, not consumed)", m.SuggestedBudgets)
	}

	// Only check_timeout_seconds: legal per the validator, projects NOTHING
	// (the framework default offer — no budget-axis suggestion).
	writeProjectManifest(t, `version: "2"
project:
  name: budget-probe
phases:
  diagnosis:
    skill: none
suggested_budgets:
  check_timeout_seconds: 120
`)
	m, err = LoadRuntimeManifest()
	if err != nil {
		t.Fatalf("timeout-only budgets must load: %v", err)
	}
	if m.SuggestedBudgets != nil {
		t.Fatalf("timeout-only projection = %+v, want nil", m.SuggestedBudgets)
	}

	// Partial: absent axes stay unlimited in the projection.
	writeProjectManifest(t, `version: "2"
project:
  name: budget-probe
phases:
  diagnosis:
    skill: none
suggested_budgets:
  delegations: 2
`)
	m, err = LoadRuntimeManifest()
	if err != nil {
		t.Fatal(err)
	}
	if m.SuggestedBudgets == nil || *m.SuggestedBudgets != (BudgetLimits{Delegations: 2}) {
		t.Fatalf("partial projection = %+v, want delegations-only", m.SuggestedBudgets)
	}

	// Unknown axis: fail closed (a typo'd axis must not silently degrade).
	writeProjectManifest(t, `version: "2"
project:
  name: budget-probe
phases:
  diagnosis:
    skill: none
suggested_budgets:
  phaze_tool_calls: 5
`)
	if _, err := LoadRuntimeManifest(); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("unknown budget axis must fail closed, got %v", err)
	}

	// Zero / negative values: the schema floor is 1.
	for _, bad := range []string{"phase_tool_calls: 0", "delegations: -2"} {
		writeProjectManifest(t, "version: \"2\"\nproject:\n  name: budget-probe\nphases:\n  diagnosis:\n    skill: none\nsuggested_budgets:\n  "+bad+"\n")
		if _, err := LoadRuntimeManifest(); err == nil || !strings.Contains(err.Error(), "positive integer") {
			t.Fatalf("%s must fail closed, got %v", bad, err)
		}
	}
}

// --- post-game closure ledger ------------------------------------------------------

// postGameTestRun drives a run into POST_GAME_PENDING (DONE decided).
func postGameTestRun(t *testing.T, kind string) *RunState {
	t.Helper()
	prev := journalRootOverride
	journalRootOverride = t.TempDir()
	t.Cleanup(func() { journalRootOverride = prev })
	r := startTestRun(t, BudgetLimits{})
	cp := firstDisplayedCheckpoint(t, r)
	lim := BudgetLimits{}
	if err := r.RecordDecisionWithBudget(cp.ID, DecisionContinue, "tu-b", &BudgetSettlement{ToolUseID: "tu-b", Confirm: &lim}); err != nil {
		t.Fatal(err)
	}
	openAndSubmit(t, r, "diagnosis")
	displayCurrent(t, r)
	if err := r.RecordDecision(r.CurrentCheckpoint().ID, DecisionDone, "tu-done"); err != nil {
		t.Fatal(err)
	}
	if kind == "emergency_minimal" {
		t.Fatal("use the STOP path helper for emergency_minimal")
	}
	if err := r.StartPostGame("normal"); err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusPostGamePending {
		t.Fatalf("status %s, want POST_GAME_PENDING", r.Status)
	}
	return r
}

func TestPostGameMarkerLedger(t *testing.T) {
	r := postGameTestRun(t, "normal")

	// A foreign marker is refused.
	if _, err := r.RecordPostGameAnswer("q9", "tu-1"); err == nil {
		t.Fatal("q9 is not a pinned marker")
	}
	if _, err := r.RecordPostGameAnswer("q1", ""); err == nil {
		t.Fatal("the answer must bind to a tool_use_id")
	}
	if r.PostGameReady() {
		t.Fatal("nothing answered yet")
	}
	// One call may answer ALL FOUR markers (the questionnaire is one call).
	for _, m := range PostGameMarkers() {
		if recorded, err := r.RecordPostGameAnswer(m, "tu-pg-1"); err != nil || !recorded {
			t.Fatalf("RecordPostGameAnswer(%s): recorded=%v err=%v", m, recorded, err)
		}
	}
	// Replay is silent; the first observed answer governs.
	if recorded, err := r.RecordPostGameAnswer("q1", "tu-pg-2"); err != nil || recorded || r.PostGame.Answers["q1"] != "tu-pg-1" {
		t.Fatalf("re-observation must be a no-op: recorded=%v answers=%v err=%v", recorded, r.PostGame.Answers, err)
	}
	if r.PostGameReady() {
		t.Fatal("markers alone must not make the run closure-ready (journal still owed)")
	}
}

func TestCompletePostGameSoleClosedWriter(t *testing.T) {
	r := postGameTestRun(t, "normal")

	// Nothing observed: refuse, stay POST_GAME_PENDING.
	if err := r.CompletePostGame(); err == nil || !strings.Contains(err.Error(), "q1") {
		t.Fatalf("closure without answers must refuse naming the markers, got %v", err)
	}
	if r.Status != StatusPostGamePending {
		t.Fatal("a refused closure must leave the run POST_GAME_PENDING")
	}
	for _, m := range PostGameMarkers() {
		if _, err := r.RecordPostGameAnswer(m, "tu-pg-1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.CompletePostGame(); err == nil || !strings.Contains(err.Error(), "journal") {
		t.Fatalf("closure without a journal must refuse, got %v", err)
	}

	// Journal observed, then the FILE drifts: finalization must preserve the
	// LATEST OBSERVED version — never a detached digest, never the drifted file.
	path := JournalPath(r.RunID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("journal v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordPostGameJournal("digest-v1", "tu-jr-1"); err != nil {
		t.Fatal(err)
	}
	if err := r.CompletePostGame(); err == nil || !strings.Contains(err.Error(), "no longer matches") {
		t.Fatalf("a journal that never matched must refuse, got %v", err)
	}

	// The latest observed version governs: observe v2 after writing it.
	if err := os.WriteFile(path, []byte("journal v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordPostGameJournal("digest-v2", "tu-jr-2"); err != nil {
		t.Fatal(err)
	}
	if r.PostGame.Journal.Digest != "digest-v2" || r.PostGame.Journal.ToolUseID != "tu-jr-2" {
		t.Fatalf("the latest observation must overwrite: %+v", r.PostGame.Journal)
	}
	if err := r.CompletePostGame(); err == nil {
		t.Fatal("a mismatched digest (the test used literal digests) must refuse")
	}

	// With the file ACTUALLY matching the observed digest: CLOSED, once.
	realDigest := contentDigestOf(t, path)
	if err := r.RecordPostGameJournal(realDigest, "tu-jr-3"); err != nil {
		t.Fatal(err)
	}
	if err := r.CompletePostGame(); err != nil {
		t.Fatalf("closure with everything on record: %v", err)
	}
	if r.Status != StatusClosed || r.PostGame.CompletedAt == "" {
		t.Fatalf("closure state: %s %+v", r.Status, r.PostGame)
	}
	// CLOSED is terminal: no writer works, nothing reopens.
	if err := r.CompletePostGame(); err == nil {
		t.Fatal("a second CompletePostGame must refuse")
	}
	if err := r.RecordPostGameJournal("whatever", "tu-jr-4"); err == nil {
		t.Fatal("journal observation after CLOSED must refuse")
	}
	if _, err := r.RecordPostGameAnswer("q1", "tu-pg-9"); err == nil {
		t.Fatal("answer observation after CLOSED must refuse")
	}
	// A later ordinary edit changes nothing: the run stays CLOSED.
	if err := os.WriteFile(path, []byte("journal v3 — after CLOSED"), 0o644); err != nil {
		t.Fatal(err)
	}
	disk, err := LoadRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	if disk.Status != StatusClosed || disk.PostGame.CompletedAt == "" {
		t.Fatalf("a closed run must never reopen: %s", disk.Status)
	}
}

func TestWriteMinimalExitJournalAndStopPath(t *testing.T) {
	prev := journalRootOverride
	journalRootOverride = t.TempDir()
	t.Cleanup(func() { journalRootOverride = prev })
	r := startTestRun(t, BudgetLimits{})
	cp := firstDisplayedCheckpoint(t, r)
	lim := BudgetLimits{}
	if err := r.RecordDecisionWithBudget(cp.ID, DecisionStop, "tu-stop", &BudgetSettlement{ToolUseID: "tu-stop", Confirm: &lim}); err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusPostGamePending {
		t.Fatalf("STOP lands POST_GAME_PENDING, got %s", r.Status)
	}
	// The hook-side minimal journal: written, observed, kind emergency_minimal.
	digest, err := WriteMinimalExitJournal(r)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(JournalPath(r.RunID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "kind: emergency_minimal") || !strings.Contains(string(b), r.RunID) {
		t.Fatalf("minimal journal content: %q", string(b))
	}
	if digest != contentDigestOf(t, JournalPath(r.RunID)) {
		t.Fatal("the returned digest must be the written content's digest")
	}
	if err := r.StartPostGame("emergency_minimal"); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordPostGameJournal(digest, "tu-stop"); err != nil {
		t.Fatal(err)
	}
	// The run completes through the ordinary observations later.
	for _, m := range PostGameMarkers() {
		if _, err := r.RecordPostGameAnswer(m, "tu-pg-1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.CompletePostGame(); err != nil {
		t.Fatalf("emergency-stop closure: %v", err)
	}
	if r.Status != StatusClosed || r.PostGame.Kind != "emergency_minimal" {
		t.Fatalf("emergency closure state: %s %+v", r.Status, r.PostGame)
	}
}

func TestPendingPostGameRunsCrossSession(t *testing.T) {
	r := postGameTestRun(t, "normal")
	pending, err := PendingPostGameRuns()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].RunID != r.RunID {
		t.Fatalf("pending scan = %d runs, want the POST_GAME_PENDING one", len(pending))
	}
	// Close it and rescan: display-only surface goes empty.
	for _, m := range PostGameMarkers() {
		if _, err := r.RecordPostGameAnswer(m, "tu-pg-1"); err != nil {
			t.Fatal(err)
		}
	}
	path := JournalPath(r.RunID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("journal"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordPostGameJournal(contentDigestOf(t, path), "tu-jr-1"); err != nil {
		t.Fatal(err)
	}
	if err := r.CompletePostGame(); err != nil {
		t.Fatal(err)
	}
	pending, err = PendingPostGameRuns()
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending scan after closure = %d (err %v), want 0", len(pending), err)
	}
}

func TestIsJournalPathFixedPath(t *testing.T) {
	if !IsJournalPath(filepath.Join(".summoner", "journal", "run-9.md"), "run-9") {
		t.Fatal("the project-relative fixed spelling must match")
	}
	if !IsJournalPath(JournalPath("run-9"), "run-9") {
		t.Fatal("the absolute fixed spelling must match")
	}
	if IsJournalPath(filepath.Join(".summoner", "journal", "run-8.md"), "run-9") {
		t.Fatal("another run's journal is not this run's fixed path")
	}
	if IsJournalPath(filepath.Join(".summoner", "journal", "../escape.md"), "run-9") {
		t.Fatal("path escapes must not match")
	}
	if IsJournalPath("journal/run-9.md", "run-9") {
		t.Fatal("the retired date-keyed journal path is not the fixed path")
	}
}

// TestPostGameNilLedgerFallbackKindUnknown (F5): a nil ledger at observation
// time means the process died between the terminal decision and its
// conclusion — the fallback must stamp the honest "unknown", never an
// unobserved "normal", and a later StartPostGame carrying the real kind
// overwrites it.
func TestPostGameNilLedgerFallbackKindUnknown(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	firstDisplayedCheckpoint(t, r)
	lim := BudgetLimits{}
	if err := r.RecordDecisionWithBudget(r.CurrentCheckpoint().ID, DecisionDone, "tu-done", &BudgetSettlement{ToolUseID: "tu-done", Confirm: &lim}); err != nil {
		t.Fatal(err)
	}
	// Simulate the death window: POST_GAME_PENDING with NO ledger.
	r.PostGame = nil
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RecordPostGameAnswer("q1", "tu-pg-1"); err != nil {
		t.Fatal(err)
	}
	if r.PostGame.Kind != "unknown" {
		t.Fatalf("nil-ledger fallback kind = %q, want \"unknown\" (the true kind is unknowable)", r.PostGame.Kind)
	}
	// A later real StartPostGame still wins.
	if err := r.StartPostGame("emergency_minimal"); err != nil {
		t.Fatal(err)
	}
	if r.PostGame.Kind != "emergency_minimal" {
		t.Fatalf("StartPostGame over unknown = %q, want emergency_minimal", r.PostGame.Kind)
	}
}

// contentDigestOf hashes a file's bytes (the observation digest form).
func contentDigestOf(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return hashHex(b)
}
