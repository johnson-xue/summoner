package shared

// round2_decision_test.go — ship review round 2 (protocol F1/F4, interaction
// F3/F4): the review record carries the result it concluded from, a PASS
// review lets DONE reach post-game instead of parking at a false
// "unsettled" checkpoint, every decision that returns the run to ACTIVE
// leaves the continuation cue, and blank/zero-width conclusion items are not
// content.

import (
	"os"
	"testing"
)

func TestPassReviewStoresItsResult(t *testing.T) {
	useTraceDir(t)
	r := pendingReviewRun(t)
	if err := r.RecordReviewResult(coveringResult(r, "rev-1", "pass")); err != nil {
		t.Fatalf("RecordReviewResult: %v", err)
	}
	rv := r.Reviews[0]
	if rv.Status != ReviewReviewed || rv.Verdict != VerdictPass {
		t.Fatalf("review = %s/%q, want REVIEWED/PASS", rv.Status, rv.Verdict)
	}
	if rv.Result == nil || rv.Result.Verdict != VerdictPass || len(rv.Result.CriterionVerdicts) == 0 {
		t.Fatalf("the record must store the result it concluded from, got %+v", rv.Result)
	}
}

// TestDoneAfterAPassReviewReachesPostGame — the terminal-review debt exists
// so a DONE cannot skip an owed review; a concluded PASS must discharge it.
// Without rv.Result stored, every passing fix/new/review/ship run parked at
// a REVIEW_DEBT checkpoint that said the review was unsettled.
// isolatedRun is startTestRun's state-dir isolation with a chosen workflow.
func isolatedRun(t *testing.T, workflow string) *RunState {
	t.Helper()
	prev := runStateDirOverride
	runStateDirOverride = t.TempDir()
	t.Cleanup(func() { runStateDirOverride = prev })
	prevDir := os.Getenv("CLAUDE_PROJECT_DIR")
	if err := os.Setenv("CLAUDE_PROJECT_DIR", t.TempDir()); err != nil {
		t.Fatalf("set project dir: %v", err)
	}
	t.Cleanup(func() {
		if prevDir == "" {
			os.Unsetenv("CLAUDE_PROJECT_DIR")
			return
		}
		os.Setenv("CLAUDE_PROJECT_DIR", prevDir)
	})
	r, err := StartRun("run-fix", "sess-1", workflow, BudgetLimits{})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	return r
}

func TestDoneAfterAPassReviewReachesPostGame(t *testing.T) {
	useTraceDir(t)
	r := isolatedRun(t, "fix")
	for _, phase := range []string{"context", "diagnosis", "reproduce", "fix", "regression", "review"} {
		openAndSubmit(t, r, phase)
		displayCurrent(t, r)
		decideCurrent(t, r, DecisionContinue)
	}
	env := testEnvelope()
	env.RunID = r.RunID
	if _, err := r.RequestReview(env, nil); err != nil {
		t.Fatalf("RequestReview: %v", err)
	}
	if err := r.MarkReviewRunning("rev-1"); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordReviewResult(coveringResult(r, "rev-1", "pass")); err != nil {
		t.Fatalf("RecordReviewResult: %v", err)
	}
	cp := r.CurrentCheckpoint()
	if cp == nil || cp.Kind != CheckpointReviewResult {
		t.Fatalf("after the verdict the run must sit at REVIEW_RESULT, got %+v", cp)
	}
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionDone)
	if r.Status != StatusPostGamePending {
		t.Fatalf("DONE after a concluded PASS: status = %s, want POST_GAME_PENDING (a false REVIEW_DEBT dialog here was ship round 2, protocol F1)", r.Status)
	}
}

func TestChangeAcceptLeavesTheContinuationCue(t *testing.T) {
	useTraceDir(t)
	r := startTestRun(t, BudgetLimits{})
	// A change opened while ACTIVE with no phase open (between visits) — the
	// accept resumes into exactly that position (the stall F4 reports).
	openAndSubmit(t, r, "context")
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue)
	if err := r.OpenRequirementChange("between-visits scope change"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ApplyRequirementDelta(RequirementDelta{Note: "delta"}); err != nil {
		t.Fatal(err)
	}
	displayCurrent(t, r)
	cc := r.CurrentCheckpoint()
	if err := r.RecordDecision(cc.ID, DecisionAcceptRequirementChange, "tu-ask-1"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := AppendEvent(r.RunID, "sess-1", TraceEvent{Type: EventCheckpointDecided, Payload: map[string]interface{}{"checkpoint_id": cc.ID}}); err != nil {
		t.Fatalf("append decided: %v", err)
	}
	if r.Status != StatusActive || r.ContinueNudgeFor != cc.ID {
		t.Fatalf("after ACCEPT: status %s cue %q, want ACTIVE with the cue set to %s", r.Status, r.ContinueNudgeFor, cc.ID)
	}
	if pending, err := ContinueNudgePending(r); err != nil || !pending {
		t.Fatalf("the run sits right after the accept — pending = %v (err %v), want true", pending, err)
	}
}

func TestRecoveryContinueLeavesTheContinuationCue(t *testing.T) {
	useTraceDir(t)
	r := startTestRun(t, BudgetLimits{})
	r.MarkOutcomeUnknown("tu-crash")
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	if err := r.ResolveOutcome("tu-crash", OutcomeFailed); err != nil {
		t.Fatal(err)
	}
	cp := r.CurrentCheckpoint()
	if cp == nil || cp.Kind != CheckpointRecovery {
		t.Fatalf("recovery checkpoint = %+v", cp)
	}
	displayCurrent(t, r)
	if err := r.RecordDecision(cp.ID, DecisionContinue, "tu-ask-1"); err != nil {
		t.Fatalf("continue: %v", err)
	}
	if r.Status != StatusActive || r.ContinueNudgeFor != cp.ID {
		t.Fatalf("after recovery CONTINUE: status %s cue %q, want ACTIVE with the cue set to %s", r.Status, r.ContinueNudgeFor, cp.ID)
	}
	if err := AppendEvent(r.RunID, "sess-1", TraceEvent{Type: EventCheckpointDecided, Payload: map[string]interface{}{"checkpoint_id": cp.ID}}); err != nil {
		t.Fatal(err)
	}
	if pending, err := ContinueNudgePending(r); err != nil || !pending {
		t.Fatalf("recovery continue: pending = %v (err %v), want true", pending, err)
	}
}

func TestBlankConclusionItemsAreNotContent(t *testing.T) {
	if !isEmptyConclusion(&Conclusion{Evidence: []string{""}, Summary: "​", Impact: "  "}) {
		t.Fatal("blank and zero-width items are not conclusion content")
	}
	if hasRealEvidence([]string{"​", "  "}) {
		t.Fatal("a zero-width space is not evidence")
	}
	if isEmptyConclusion(&Conclusion{Evidence: []string{"real.go:1"}}) {
		t.Fatal("a real item is content")
	}
}

// TestAutoPassAndNudgeDefenseInDepth — guards only a direct reducer caller
// can reach today, each unpinned without this test (ship review round 2,
// protocol F5): the kind guard, the open-questions guard, and the nudge
// tail's event-TYPE test.
func TestAutoPassAndNudgeDefenseInDepth(t *testing.T) {
	useTraceDir(t)
	r := startTestRun(t, BudgetLimits{})
	r.MarkOutcomeUnknown("tu-crash")
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	if err := r.ResolveOutcome("tu-crash", OutcomeFailed); err != nil {
		t.Fatal(err)
	}
	cp := r.CurrentCheckpoint()
	if cp == nil || cp.Kind != CheckpointRecovery {
		t.Fatalf("fixture: recovery checkpoint = %+v", cp)
	}
	if err := r.AutoPassCheckpoint(cp.ID, false); err == nil {
		t.Fatal("a non-PHASE checkpoint must never auto-pass")
	}

	cfg := &RunState{Workflow: "fix"}
	withQuestions := CheckpointState{ID: "cp-x", Kind: CheckpointPhase, PhaseID: "context",
		Outcome: ConclusionConfirmed, Note: "s",
		Conclusion: &Conclusion{Summary: "s", Evidence: []string{"e"}, OpenQuestions: []string{"q"}}}
	if AutoPassBlocker(cfg, withQuestions, false) == "" {
		t.Fatal("a conclusion with open questions owes the human gate")
	}

	r2 := startTestRun(t, BudgetLimits{})
	cp2 := openAndSubmit(t, r2, "context")
	displayCurrent(t, r2)
	decideCurrent(t, r2, DecisionContinue)
	// The reducer does not trace; the hook would have appended the decided
	// event. A DIFFERENT lifecycle event naming the cue must not arm the
	// nudge (the tail's type test).
	if err := AppendEvent(r2.RunID, "sess-1", TraceEvent{Type: EventCheckpointOpened, Payload: map[string]interface{}{"checkpoint_id": cp2.ID}}); err != nil {
		t.Fatal(err)
	}
	if pending, err := ContinueNudgePending(r2); err != nil || pending {
		t.Fatalf("a non-decision lifecycle event armed the nudge (pending %v, err %v)", pending, err)
	}
}
