package shared

import (
	"strings"
	"testing"
)

// autopass_test.go — adaptive checkpoints (0.7.0 D3) at the reducer seam:
// a phase-submit checkpoint passes WITHOUT a user decision exactly when the
// submission is confirmed with evidence, on a phase that needs no human
// judgment, with no budget question riding and no FAIL among its checks.

// confirmedSubmission satisfies every auto-pass condition of an
// auto-passable phase.
func confirmedSubmission(phaseID string) PhaseSubmission {
	return PhaseSubmission{
		PhaseID: phaseID, Summary: phaseID + " established",
		Outcome: ConclusionConfirmed,
		Conclusion: &Conclusion{
			Summary:  phaseID + " conclusion",
			Evidence: []string{"evidence-1", "evidence-2"},
			Approach: "read the code and ran the existing checks",
		},
	}
}

// openConfirmed parks a fresh debug run (context → diagnosis) at the
// checkpoint of a CONFIRMED context submission.
func openConfirmed(t *testing.T) (*RunState, CheckpointState) {
	t.Helper()
	r := startTestRun(t, BudgetLimits{})
	if err := r.OpenPhase("context"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	cp, err := r.SubmitPhaseUnstaged(confirmedSubmission("context"))
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	return r, cp
}

// TestAutoPassCheckpointAcceptsAndAdvances — acceptance criterion 1 at the
// reducer seam: the checkpoint record is kept and accepted with the
// framework as the actor, the phase is frozen into the acceptance view, the
// run returns to ACTIVE with nothing pending, and the next visit's budget
// bucket is authorized exactly as a CONTINUE authorizes it.
func TestAutoPassCheckpointAcceptsAndAdvances(t *testing.T) {
	useTraceDir(t)
	r, cp := openConfirmed(t)
	if r.Status != StatusWaitUser {
		t.Fatalf("fixture: status %s, want WAIT_USER", r.Status)
	}

	if err := r.AutoPassCheckpoint(cp.ID, false); err != nil {
		t.Fatalf("AutoPassCheckpoint: %v", err)
	}
	if r.Status != StatusActive {
		t.Fatalf("status = %s, want ACTIVE (nothing waits for the user)", r.Status)
	}
	if got := r.CurrentCheckpoint(); got != nil {
		t.Fatalf("no checkpoint may stay pending, got %+v", got)
	}
	stored := checkpointByID(t, r, cp.ID)
	if !stored.Decided || stored.Decision != DecisionAutoPass || stored.DecisionToolUseID != "" || stored.Displayed {
		t.Fatalf("auto-passed checkpoint = decided %v decision %q tool_use_id %q displayed %v — want decided/AUTO_PASS/\"\"/false",
			stored.Decided, stored.Decision, stored.DecisionToolUseID, stored.Displayed)
	}
	if got := lastVisit(t, r, "context").Outcome; got != "accepted" {
		t.Fatalf("context visit outcome = %q, want accepted", got)
	}
	if len(r.Acceptance.Requirements) != 1 || r.Acceptance.Requirements[0].AcceptedBy != AutoPassActor || r.Acceptance.AcceptedBy != AutoPassActor {
		t.Fatalf("the acceptance record must name the framework as the actor, got %+v / %q", r.Acceptance.Requirements, r.Acceptance.AcceptedBy)
	}
	if !r.UpcomingVisit {
		t.Fatal("an auto-pass must authorize the next visit's budget bucket, exactly like CONTINUE")
	}
	if r.ContinueNudgeFor != cp.ID {
		t.Fatalf("ContinueNudgeFor = %q, want %q (the Stop hook's one-shot continuation cue)", r.ContinueNudgeFor, cp.ID)
	}

	disk := reloadRunState(t, "run-1")
	if got := checkpointByID(t, disk, cp.ID); got.Decision != DecisionAutoPass || disk.Status != StatusActive {
		t.Fatalf("the auto-pass must survive a reload: decision %q status %s", got.Decision, disk.Status)
	}
}

// TestAutoPassCheckpointRefusesWhatOwesTheHumanGate — acceptance criteria 2
// and 3 at the reducer seam: everything that needs a human judgment leaves
// the checkpoint open, untouched.
func TestAutoPassCheckpointRefusesWhatOwesTheHumanGate(t *testing.T) {
	atDiagnosis := func(t *testing.T) (*RunState, CheckpointState) {
		r := startTestRun(t, BudgetLimits{})
		acceptContext(t, r)
		if err := r.OpenPhase("diagnosis"); err != nil {
			t.Fatalf("OpenPhase(diagnosis): %v", err)
		}
		cp, err := r.SubmitPhaseUnstaged(confirmedSubmission("diagnosis"))
		if err != nil {
			t.Fatalf("SubmitPhase: %v", err)
		}
		return r, cp
	}
	withOutcome := func(phaseID, outcome string, evidence []string) func(*testing.T) (*RunState, CheckpointState) {
		return func(t *testing.T) (*RunState, CheckpointState) {
			r := startTestRun(t, BudgetLimits{})
			if err := r.OpenPhase(phaseID); err != nil {
				t.Fatalf("OpenPhase: %v", err)
			}
			cp, err := r.SubmitPhaseUnstaged(PhaseSubmission{
				PhaseID: phaseID, Outcome: outcome,
				Conclusion: &Conclusion{Summary: "s", Evidence: evidence, Approach: "a"},
			})
			if err != nil {
				t.Fatalf("SubmitPhase: %v", err)
			}
			return r, cp
		}
	}
	atExhaustion := func(t *testing.T) (*RunState, CheckpointState) {
		// The budget reads exhausted when the checkpoint OPENS, so the
		// checkpoint carries the extension question.
		r := startTestRun(t, BudgetLimits{PhaseToolCalls: 1})
		if err := r.OpenPhase("context"); err != nil {
			t.Fatalf("OpenPhase: %v", err)
		}
		r.CountToolCall("tu-pre")
		if err := r.Save(); err != nil {
			t.Fatal(err)
		}
		cp, err := r.SubmitPhaseUnstaged(confirmedSubmission("context"))
		if err != nil {
			t.Fatalf("SubmitPhase: %v", err)
		}
		if cp.ExtendAsk == nil {
			t.Fatal("fixture: the checkpoint opened at exhaustion must carry the extend offer")
		}
		return r, cp
	}
	exhaustedAfterOpen := func(t *testing.T) (*RunState, CheckpointState) {
		// Not exhausted at open; a later counter (a FAIL ticking the
		// failed-check budget) exhausts it before the auto-pass is attempted.
		r, cp := openConfirmed(t)
		r.Limits.FailedChecks = 1
		r.CountFailedCheck()
		if err := r.Save(); err != nil {
			t.Fatal(err)
		}
		return r, cp
	}

	otherID := "cp-other"
	cases := []struct {
		name        string
		build       func(*testing.T) (*RunState, CheckpointState)
		checkFailed bool
		id          string // "" = the open checkpoint
		want        string
	}{
		{"diagnosis never auto-passes", atDiagnosis, false, "", "human gate"},
		{"a failed submission", withOutcome("context", ConclusionFailed, []string{"e"}), false, "", "not confirmed"},
		{"an unclaimed outcome", withOutcome("context", "", []string{"e"}), false, "", "not confirmed"},
		{"confirmed without evidence", withOutcome("context", ConclusionConfirmed, nil), false, "", "evidence"},
		{"a checkpoint opened at budget exhaustion", atExhaustion, false, "", "extension"},
		{"a budget exhausted after the open", exhaustedAfterOpen, false, "", "exhausted"},
		{"a FAIL among this submission's checks", openConfirmed, true, "", "FAIL"},
		{"a checkpoint that is not the open one", openConfirmed, false, otherID, "not the open checkpoint"},
	}
	for _, c := range cases {
		r, cp := c.build(t)
		id := c.id
		if id == "" {
			id = cp.ID
		}
		err := r.AutoPassCheckpoint(id, c.checkFailed)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: AutoPassCheckpoint error = %v, want one naming %q", c.name, err, c.want)
		}
		if r.Status != StatusWaitUser {
			t.Errorf("%s: a refused auto-pass must leave the status WAIT_USER, got %s", c.name, r.Status)
		}
		if got := checkpointByID(t, r, cp.ID); got.Decided || got.Decision != "" {
			t.Errorf("%s: the checkpoint must stay undecided, got %+v", c.name, got)
		}
		for _, fr := range r.Acceptance.Requirements {
			if fr.SourceCheckpointID == cp.ID {
				t.Errorf("%s: the refused checkpoint must freeze nothing into the acceptance view, got %+v", c.name, fr)
			}
		}
	}
}

// acceptContext settles context through a human CONTINUE (reducer level).
func acceptContext(t *testing.T, r *RunState) {
	t.Helper()
	if err := r.OpenPhase("context"); err != nil {
		t.Fatalf("OpenPhase(context): %v", err)
	}
	_, err := r.SubmitPhaseUnstaged(PhaseSubmission{PhaseID: "context", Summary: "context done"})
	if err != nil {
		t.Fatalf("SubmitPhase(context): %v", err)
	}
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue)
}

// TestRecordDecisionNeverRecordsAnAutoPass: AUTO_PASS is not a decision a
// platform answer can carry — RecordDecision refuses it, so no answer can
// ever pose as the framework.
func TestRecordDecisionNeverRecordsAnAutoPass(t *testing.T) {
	r, cp := openConfirmed(t)
	displayCurrent(t, r)
	if err := r.RecordDecision(cp.ID, DecisionAutoPass, "tu-ask-1"); err == nil {
		t.Fatal("RecordDecision(AUTO_PASS) must be refused")
	}
	if got := checkpointByID(t, r, cp.ID); got.Decided {
		t.Fatal("a refused AUTO_PASS must decide nothing")
	}
}

// TestOpenPhaseSpendsTheContinuationCue: the one-shot continuation cue lives
// exactly until the next phase opens.
func TestOpenPhaseSpendsTheContinuationCue(t *testing.T) {
	r, cp := openConfirmed(t)
	if err := r.AutoPassCheckpoint(cp.ID, false); err != nil {
		t.Fatalf("AutoPassCheckpoint: %v", err)
	}
	if err := r.OpenPhase("diagnosis"); err != nil {
		t.Fatalf("OpenPhase(diagnosis): %v", err)
	}
	if r.ContinueNudgeFor != "" {
		t.Fatalf("ContinueNudgeFor = %q after the next phase opened, want empty", r.ContinueNudgeFor)
	}
}

// TestClearContinueNudge is the Stop hook's one-shot consumption: the cue
// clears durably, and clearing twice is harmless.
func TestClearContinueNudge(t *testing.T) {
	r, cp := openConfirmed(t)
	if err := r.AutoPassCheckpoint(cp.ID, false); err != nil {
		t.Fatalf("AutoPassCheckpoint: %v", err)
	}
	if err := r.ClearContinueNudge(); err != nil {
		t.Fatalf("ClearContinueNudge: %v", err)
	}
	if reloadRunState(t, "run-1").ContinueNudgeFor != "" {
		t.Fatal("the cleared cue must stay cleared on disk")
	}
	if err := r.ClearContinueNudge(); err != nil {
		t.Fatalf("second ClearContinueNudge: %v", err)
	}
}

// TestContinueNudgePendingReadsTheLifecycleTail — the continuation backstop's
// condition (design D3, review-corrected): the run sits "right after" an
// auto-pass while the LAST lifecycle event (phase.* / checkpoint.*) is that
// checkpoint.auto. Tool and check events after it do not count — after a
// submit there are always tool.result / check.result lines — and a later
// phase.started ends it.
func TestContinueNudgePendingReadsTheLifecycleTail(t *testing.T) {
	useTraceDir(t)
	r, cp := openConfirmed(t)
	if pending, err := ContinueNudgePending(r); err != nil || pending {
		t.Fatalf("before the auto-pass: pending = %v (err %v), want false", pending, err)
	}
	if err := r.AutoPassCheckpoint(cp.ID, false); err != nil {
		t.Fatalf("AutoPassCheckpoint: %v", err)
	}
	mustAppend(t, "run-1", EventCheckpointOpened)
	if pending, _ := ContinueNudgePending(r); pending {
		t.Fatal("an opened checkpoint with no checkpoint.auto yet is not an auto-pass tail")
	}
	auto := TraceEvent{Type: EventCheckpointAuto, Source: "trace_test", Payload: map[string]interface{}{"checkpoint_id": cp.ID, "phase_id": "context", "outcome": ConclusionConfirmed}}
	if err := AppendEvent("run-1", "sess-1", auto); err != nil {
		t.Fatalf("append checkpoint.auto: %v", err)
	}
	for _, typ := range []string{EventToolResult, EventCheckResult, EventToolStarted} {
		mustAppend(t, "run-1", typ)
		if pending, err := ContinueNudgePending(r); err != nil || !pending {
			t.Fatalf("after %s: pending = %v (err %v), want true — non-lifecycle events do not end the tail", typ, pending, err)
		}
	}
	mustAppend(t, "run-1", EventPhaseStarted)
	if pending, _ := ContinueNudgePending(r); pending {
		t.Fatal("a phase.started after the auto-pass ends the tail")
	}

	// No cue, no nudge — whatever the trace says.
	if err := r.ClearContinueNudge(); err != nil {
		t.Fatal(err)
	}
	if err := AppendEvent("run-1", "sess-1", auto); err != nil {
		t.Fatal(err)
	}
	if pending, _ := ContinueNudgePending(r); pending {
		t.Fatal("with the cue cleared nothing is pending")
	}
}

// TestAutoPassablePhase pins the policy table: the phases whose checkpoint
// needs no human judgment, and only those.
func TestAutoPassablePhase(t *testing.T) {
	for _, phase := range []string{"context", "reproduce", "verify", "regression"} {
		if !AutoPassablePhase(phase) {
			t.Errorf("AutoPassablePhase(%q) = false, want true", phase)
		}
	}
	for _, phase := range []string{"diagnosis", "fix", "requirements", "plan", "implement", "review", "prepare", "merge", "decide", "operate", "planning", "changelog", "publish", "", "Context"} {
		if AutoPassablePhase(phase) {
			t.Errorf("AutoPassablePhase(%q) = true — this phase owes the human gate", phase)
		}
	}
}

// TestNextExpectedPhase pins the route walk the status view and the Stop
// hook share to name "what comes next": the first phase whose latest visit
// is unsettled, and nothing once the route is done.
func TestNextExpectedPhase(t *testing.T) {
	r, cp := openConfirmed(t) // debug route: context → diagnosis
	if got := NextExpectedPhase(r); got != "context" {
		t.Fatalf("with context unsettled: next = %q, want context", got)
	}
	if err := r.AutoPassCheckpoint(cp.ID, false); err != nil {
		t.Fatalf("AutoPassCheckpoint: %v", err)
	}
	if got := NextExpectedPhase(r); got != "diagnosis" {
		t.Fatalf("after the context auto-pass: next = %q, want diagnosis", got)
	}
	acceptDiagnosis := func() {
		if err := r.OpenPhase("diagnosis"); err != nil {
			t.Fatalf("OpenPhase(diagnosis): %v", err)
		}
		_, err := r.SubmitPhaseUnstaged(PhaseSubmission{PhaseID: "diagnosis", Summary: "diagnosis done"})
		if err != nil {
			t.Fatalf("SubmitPhase(diagnosis): %v", err)
		}
		displayCurrent(t, r)
		decideCurrent(t, r, DecisionContinue)
	}
	acceptDiagnosis()
	if got := NextExpectedPhase(r); got != "" {
		t.Fatalf("with the route settled: next = %q, want empty", got)
	}
}

// --- ship review round 1 (protocol lens) --------------------------------------------

// TestAutoPassRefusesDegenerateAndDisplayedCheckpoints: a confirmed claim
// carried by blank items is no evidence, a blank conclusion is no
// conclusion, and a checkpoint the user has already SEEN is theirs to
// decide.
func TestAutoPassRefusesDegenerateAndDisplayedCheckpoints(t *testing.T) {
	build := func(summary string, evidence []string) (*RunState, CheckpointState) {
		r := startTestRun(t, BudgetLimits{})
		if err := r.OpenPhase("context"); err != nil {
			t.Fatalf("OpenPhase: %v", err)
		}
		cp, err := r.SubmitPhaseUnstaged(PhaseSubmission{
			PhaseID: "context", Outcome: ConclusionConfirmed,
			Conclusion: &Conclusion{Summary: summary, Evidence: evidence, Approach: "how"},
		})
		if err != nil {
			t.Fatalf("SubmitPhase: %v", err)
		}
		return r, cp
	}
	cases := []struct {
		name    string
		build   func() (*RunState, CheckpointState)
		display bool
		want    string
	}{
		{"blank evidence items", func() (*RunState, CheckpointState) { return build("a real conclusion", []string{"", " ", "\t"}) }, false, "evidence"},
		{"one real item among blanks", func() (*RunState, CheckpointState) { return build("a real conclusion", []string{"", " "}) }, false, "evidence"},
		{"blank conclusion", func() (*RunState, CheckpointState) { return build("   ", []string{"evidence-1"}) }, false, "conclusion"},
		{"already displayed", func() (*RunState, CheckpointState) { return build("a real conclusion", []string{"evidence-1"}) }, true, "displayed"},
	}
	for _, c := range cases {
		r, cp := c.build()
		if c.display {
			displayCurrent(t, r)
		}
		err := r.AutoPassCheckpoint(cp.ID, false)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: AutoPassCheckpoint error = %v, want one naming %q", c.name, err, c.want)
		}
		if r.Status != StatusWaitUser || checkpointByID(t, r, cp.ID).Decided {
			t.Errorf("%s: the checkpoint must stay with the user", c.name)
		}
	}

	// Control: the same shapes with real content pass, and one real item is
	// enough even beside blank ones.
	r, cp := build("a real conclusion", []string{"", "evidence-1", " "})
	if err := r.AutoPassCheckpoint(cp.ID, false); err != nil {
		t.Fatalf("one real evidence item must be enough: %v", err)
	}
}

// TestAutoPassRefusedWhenNothingFollowsOnTheRoute: an auto-pass opens no
// checkpoint; if nothing followed on the route the run would be left ACTIVE
// with no checkpoint to offer done/stop from.
func TestAutoPassRefusedWhenNothingFollowsOnTheRoute(t *testing.T) {
	r := startTestRun(t, BudgetLimits{}) // debug route: context → diagnosis
	// The later phase is already settled, so context is the last thing owed.
	r.Phases = append(r.Phases, PhaseState{ID: "diagnosis", VisitID: "diagnosis@v1", Outcome: "accepted"})
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	if err := r.OpenPhase("context"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	cp, err := r.SubmitPhaseUnstaged(confirmedSubmission("context"))
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	if err := r.AutoPassCheckpoint(cp.ID, false); err == nil || !strings.Contains(err.Error(), "nothing follows") {
		t.Fatalf("AutoPassCheckpoint error = %v, want a refusal naming that nothing follows", err)
	}
	if r.Status != StatusWaitUser {
		t.Fatalf("status = %s, want WAIT_USER", r.Status)
	}
}

// TestRequirementChangeSpendsTheContinuationCue: a change opened right
// after a pass makes "立即进入下一阶段" false (OpenPhase refuses while a
// change is open) — the cue is spent.
func TestRequirementChangeSpendsTheContinuationCue(t *testing.T) {
	r, cp := openConfirmed(t)
	if err := r.AutoPassCheckpoint(cp.ID, false); err != nil {
		t.Fatalf("AutoPassCheckpoint: %v", err)
	}
	if r.ContinueNudgeFor != cp.ID {
		t.Fatal("fixture: the pass must leave the cue")
	}
	if err := r.OpenRequirementChangeAt("cover the logout flow too", DevPosition{CurrentTask: "ctx"}); err != nil {
		t.Fatalf("OpenRequirementChangeAt: %v", err)
	}
	if r.ContinueNudgeFor != "" {
		t.Fatalf("ContinueNudgeFor = %q after a requirement change opened, want empty", r.ContinueNudgeFor)
	}
}

// --- ship review round 1 (UX lens): the continuation cue after a HUMAN decision ---

// decidedByHuman parks a debug run right after a human CONTINUE at the
// context checkpoint, with the trace the PostToolUse hook leaves
// (opened → displayed → decided), and returns the checkpoint.
func decidedByHuman(t *testing.T, d Decision) (*RunState, CheckpointState) {
	t.Helper()
	r := startTestRun(t, BudgetLimits{})
	cp := openAndSubmit(t, r, "context")
	for _, ev := range []TraceEvent{
		{Type: EventCheckpointOpened, Payload: map[string]interface{}{"checkpoint_id": cp.ID, "phase_id": "context", "nonce": cp.Nonce}},
		{Type: EventCheckpointDisplayed, Payload: map[string]interface{}{"checkpoint_id": cp.ID, "nonce": cp.Nonce}},
	} {
		if err := AppendEvent("run-1", "sess-1", ev); err != nil {
			t.Fatalf("append %s: %v", ev.Type, err)
		}
	}
	displayCurrent(t, r)
	if err := r.RecordDecision(cp.ID, d, "tu-ask-1"); err != nil {
		t.Fatalf("decide %s: %v", d, err)
	}
	if err := AppendEvent("run-1", "sess-1", TraceEvent{
		Type: EventCheckpointDecided, ToolUseID: "tu-ask-1",
		Payload: map[string]interface{}{"checkpoint_id": cp.ID, "tool_use_id": "tu-ask-1"},
	}); err != nil {
		t.Fatal(err)
	}
	return r, checkpointByID(t, r, cp.ID)
}

// TestHumanDecisionLeavesTheContinuationCue: a user's continue or skip
// returns the run to ACTIVE with nothing rendered and nothing pushing the
// model on — the cue (and the Stop hook's one-shot nudge) covers it exactly
// as it covers an auto-pass. DONE, which leaves ACTIVE, sets no cue.
func TestHumanDecisionLeavesTheContinuationCue(t *testing.T) {
	useTraceDir(t)
	for _, d := range []Decision{DecisionContinue, DecisionSkip} {
		r, cp := decidedByHuman(t, d)
		if r.Status != StatusActive || r.ContinueNudgeFor != cp.ID {
			t.Errorf("%s: status %s cue %q, want ACTIVE with the cue set to %s", d, r.Status, r.ContinueNudgeFor, cp.ID)
		}
		if pending, err := ContinueNudgePending(r); err != nil || !pending {
			t.Errorf("%s: the run sits right after the decision — pending = %v (err %v), want true", d, pending, err)
		}
		mustAppend(t, "run-1", EventToolResult) // the answering call's own result
		if pending, _ := ContinueNudgePending(r); !pending {
			t.Errorf("%s: a tool.result after the decision must not end the tail", d)
		}
		mustAppend(t, "run-1", EventPhaseStarted)
		if pending, _ := ContinueNudgePending(r); pending {
			t.Errorf("%s: a phase.started after the decision ends the tail", d)
		}
	}

}

// TestDoneLeavesNoContinuationCue: DONE leaves ACTIVE for post-game — the
// model owes a questionnaire, not the next phase.
func TestDoneLeavesNoContinuationCue(t *testing.T) {
	useTraceDir(t)
	r := startTestRun(t, BudgetLimits{})
	cp := openAndSubmit(t, r, "context")
	displayCurrent(t, r)
	if err := r.RecordDecision(cp.ID, DecisionDone, "tu-ask-1"); err != nil {
		t.Fatalf("decide DONE: %v", err)
	}
	if r.Status != StatusPostGamePending || r.ContinueNudgeFor != "" {
		t.Fatalf("DONE: status %s cue %q, want POST_GAME_PENDING with no cue", r.Status, r.ContinueNudgeFor)
	}
}
