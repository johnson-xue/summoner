package main

// recovery_recall_test.go — recovery-round I4, PostToolUse half: the "Recall:
// <target>" answer. The label remainder resolves against the checkpoint's
// offered recall targets inside the reducer; the hook then records
// recall.applied (full provenance + the rework tick) followed by phase.started
// for the re-entered visit — the event pair recovery-check.sh audits.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// recallAnsweredCheckpoint drives: context accepted, diagnosis accepted, then
// a displayed fix checkpoint offering the real history as recall targets.
func recallAnsweredCheckpoint(t *testing.T) (*shared.RunState, shared.CheckpointState) {
	t.Helper()
	useHookEnv(t)
	r := startRun(t)
	for _, ph := range []string{"context", "diagnosis"} {
		if err := r.OpenPhase(ph); err != nil {
			t.Fatalf("OpenPhase(%s): %v", ph, err)
		}
		c, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: ph})
		if err != nil {
			t.Fatalf("SubmitPhase(%s): %v", ph, err)
		}
		if err := r.MarkDisplayed(c.ID, shared.CheckpointContentDigest(c, r.RequirementsVersion)); err != nil {
			t.Fatal(err)
		}
		if !r.BudgetConfirmed {
			// T18: the run's FIRST checkpoint carries the budget ask — settle
			// it (unlimited) while it is still the current checkpoint.
			if _, err := r.SetBudgetLimits(shared.BudgetLimits{}, "tu-budget-seed"); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.RecordDecision(c.ID, shared.DecisionContinue, ""); err != nil {
			t.Fatal(err)
		}
	}
	cp := openDisplayedCheckpoint(t, r, "fix")
	for _, want := range []string{"latest", "context@v1", "diagnosis@v1"} {
		found := false
		for _, tgt := range cp.RecallTargets {
			if tgt.Label == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("checkpoint offers no recall target %q (offers %v)", want, cp.RecallTargets)
		}
	}
	return r, cp
}

func answerPayload(t *testing.T, cp shared.CheckpointState, label string) string {
	t.Helper()
	q, text := cpQuestion(cp.Nonce, false,
		"Continue: accept and proceed", "Recall: "+strings.TrimPrefix(label, "Recall: "))
	return postUsePayload(t, "AskUserQuestion", "toolu-recall-1",
		map[string]interface{}{"questions": []interface{}{q}},
		map[string]interface{}{
			"questions": []interface{}{map[string]interface{}{"question": text}},
			"answers":   map[string]interface{}{text: label},
		})
}

// TestNamedRecallAnswerResolvesTargetAndRecords — R9/R10: answering the
// offered "Recall: context@v1" re-enters exactly that visit; the trace gains
// recall.applied THEN phase.started; an unknown target is a decision
// rejection that records nothing.
func TestNamedRecallAnswerResolvesTargetAndRecords(t *testing.T) {
	r, cp := recallAnsweredCheckpoint(t)
	beforeRework := r.Counters.RunRework

	code, stderr := callHook(t, answerPayload(t, cp, "Recall: context@v1"))
	if code != 0 {
		t.Fatalf("named recall exit = %d (stderr %q)", code, stderr)
	}
	after := reloadRun(t, "run-1")
	// Exactly context is re-opened (the named target, not the latest default
	// diagnosis).
	var open []string
	for _, p := range after.Phases {
		if p.Open {
			open = append(open, p.ID)
		}
	}
	if len(open) != 1 || open[0] != "context" {
		t.Fatalf("open phases = %v, want [context]", open)
	}
	if after.Counters.RunRework != beforeRework+1 {
		t.Fatalf("RunRework = %d, want %d", after.Counters.RunRework, beforeRework+1)
	}
	rec := after.Recalls[len(after.Recalls)-1]
	if rec.TargetVisitID != "context@v1" || rec.TargetPhaseID != "context" || rec.Provenance == "" {
		t.Fatalf("recall record = %+v", rec)
	}

	// Trace order: checkpoint.decided → recall.applied → phase.started (the
	// re-entered visit's started — what recovery-check.sh joins on).
	evs := readHookTrace(t, "run-1")
	decided, applied, started := -1, -1, -1
	for i, ev := range evs {
		switch ev.Type {
		case shared.EventCheckpointDecided:
			if decided < 0 {
				decided = i
			}
		case shared.EventRecallApplied:
			applied = i
		case shared.EventPhaseStarted:
			if applied >= 0 && started < 0 {
				started = i
			}
		}
	}
	if decided < 0 || applied < 0 || started < 0 || !(decided < applied && applied < started) {
		t.Fatalf("trace order wrong: decided=%d applied=%d started=%d", decided, applied, started)
	}
	ra := evs[applied].Payload
	if ra["target_phase_id"] != "context" || ra["target_visit_id"] != "context@v1" ||
		ra["from_phase_id"] != "fix" || fmt.Sprint(ra["run_rework"]) != fmt.Sprint(after.Counters.RunRework) {
		t.Fatalf("recall.applied payload = %v", ra)
	}
	if _, ok := ra["provenance"].(string); !ok {
		t.Fatalf("recall.applied carries no provenance: %v", ra)
	}
	if evs[started].Payload["phase_id"] != "context" {
		t.Fatalf("post-recall phase.started payload = %v", evs[started].Payload)
	}

	// Unknown target twin: decision rejected, nothing recorded.
	r2, cp2 := recallAnsweredCheckpoint(t)
	code, stderr = callHook(t, answerPayload(t, cp2, "Recall: review@v9"))
	if code != 0 {
		t.Fatalf("unknown-target recall exit = %d (stderr %q)", code, stderr)
	}
	evs2 := readHookTrace(t, "run-1")
	wantRejected(t, evs2, cp2.ID, "not an offered recall target")
	if n := len(eventsOfType(evs2, shared.EventRecallApplied)); n != 0 {
		t.Fatalf("rejected target recorded %d recall.applied events", n)
	}
	_ = r2
}

// TestDefaultRecallViaShippedMenuLabel (re-review F1): the SHIPPED kind-menu
// recall label — "recall: 回城——回到指定历史阶段重做" — performs the §4
// DEFAULT recall (latest accepted visit). Its remainder is a DESCRIPTION,
// not a target name; pre-F1 every real recall answer from the shipped menu
// was decision_rejected.
func TestDefaultRecallViaShippedMenuLabel(t *testing.T) {
	r, cp := recallAnsweredCheckpoint(t)
	_ = r
	label := "recall: 回城——回到指定历史阶段重做"
	q, text := cpQuestion(cp.Nonce, false, "continue: 继续下一阶段", label)
	code, stderr := callHook(t, postUsePayload(t, "AskUserQuestion", "toolu-recall-def",
		map[string]interface{}{"questions": []interface{}{q}},
		map[string]interface{}{
			"questions": []interface{}{map[string]interface{}{"question": text}},
			"answers":   map[string]interface{}{text: label},
		}))
	if code != 0 {
		t.Fatalf("shipped-label recall exit = %d, want 0 (stderr %q)", code, stderr)
	}
	after := reloadRun(t, "run-1")
	// The default target is the LATEST accepted visit (diagnosis@v1) — the
	// exact behavior the shipped menu promised before the wave regressed it.
	var open []string
	for _, p := range after.Phases {
		if p.Open {
			open = append(open, p.ID)
		}
	}
	if len(open) != 1 || open[0] != "diagnosis" {
		t.Fatalf("open phases = %v, want [diagnosis] (the default latest target)", open)
	}
	if n := len(eventsOfType(readHookTrace(t, "run-1"), shared.EventRecallApplied)); n != 1 {
		t.Fatalf("recall.applied events = %d, want 1 (the default recall applied)", n)
	}
}

// walkFixRoute accepts context → regression through their checkpoints and
// returns the review phase's displayed checkpoint (the run sits on it).
func walkFixRoute(t *testing.T) (*shared.RunState, shared.CheckpointState) {
	t.Helper()
	useHookEnv(t)
	r := startRun(t)
	for _, ph := range []string{"context", "diagnosis", "reproduce", "fix", "regression"} {
		cp := openDisplayedCheckpoint(t, r, ph)
		if err := r.RecordDecision(cp.ID, shared.DecisionContinue, ""); err != nil {
			t.Fatalf("continue %s: %v", ph, err)
		}
	}
	reviewCp := openDisplayedCheckpoint(t, r, "review")
	return r, reviewCp
}

// continueAnswerPayload builds the answer `continue:` for a phase-less
// checkpoint whose page 1 carries a continue label.
func continueAnswerPayload(t *testing.T, debt *shared.CheckpointState, toolUseID string) string {
	t.Helper()
	page := shared.DecisionPage(*debt)
	continueLabel := ""
	for _, l := range page {
		if strings.HasPrefix(strings.ToLower(l), "continue") {
			continueLabel = l
		}
	}
	if continueLabel == "" {
		t.Fatalf("fixture: page 1 offers no continue label: %q", page)
	}
	q, text := cpQuestion(debt.Nonce, false, page...)
	return postUsePayload(t, "AskUserQuestion", toolUseID,
		map[string]interface{}{"questions": []interface{}{q}},
		map[string]interface{}{
			"questions": []interface{}{map[string]interface{}{"question": text}},
			"answers":   map[string]interface{}{text: continueLabel},
		})
}

// display binds the current checkpoint (the two-turn display act).
func display(t *testing.T, r *shared.RunState) *shared.CheckpointState {
	t.Helper()
	cp := r.CurrentCheckpoint()
	if cp == nil {
		t.Fatal("fixture: no current checkpoint")
	}
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(*cp, r.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	return cp
}

// assertReenteredByContinue checks the effect of a "continue" that was
// mapped to a recall: one open visit, a recall record, the trace pair with
// the checkpoint's kind, and the block that tells the model.
func assertReenteredByContinue(t *testing.T, out string, wantVisit, wantKind string) {
	t.Helper()
	after := reloadRun(t, "run-1")
	if after.Status != shared.StatusActive || !after.HasOpenPhase() {
		t.Fatalf("after continue: status %s open %v, want ACTIVE with a phase open (not stranded)", after.Status, after.HasOpenPhase())
	}
	var open []string
	for _, p := range after.Phases {
		if p.Open {
			open = append(open, p.VisitID)
		}
	}
	if len(open) != 1 || open[0] != wantVisit {
		t.Fatalf("open visits = %v, want [%s]", open, wantVisit)
	}
	applied := -1
	for i, ev := range readHookTrace(t, "run-1") {
		if ev.Type == shared.EventRecallApplied {
			applied = i
			if ev.Payload["from_checkpoint_kind"] != wantKind {
				t.Fatalf("recall.applied from_checkpoint_kind = %v, want %s", ev.Payload["from_checkpoint_kind"], wantKind)
			}
		}
	}
	if applied < 0 {
		t.Fatal("the re-entry must be traced as a recall (recall.applied)")
	}
	if !strings.Contains(out, shared.MappedContinueHeader) || !strings.Contains(out, wantVisit) {
		t.Fatalf("the model must be TOLD that continue re-entered %s:\n%s", wantVisit, out)
	}
}

// TestDebtContinueWithACompleteRouteReentersDevelopment — ship review
// rounds 3–4 (interaction F3): DONE with an unsettled terminal review parks
// at REVIEW_DEBT. With every route phase settled its `continue:` used to
// leave the run ACTIVE with nothing to open and no checkpoint to close
// from. It now re-enters the last accepted DEVELOPMENT phase through the
// ordinary recall path — traced as one, and told to the model.
func TestDebtContinueWithACompleteRouteReentersDevelopment(t *testing.T) {
	r, reviewCp := walkFixRoute(t)
	if err := r.RecordDecision(reviewCp.ID, shared.DecisionDone, ""); err != nil {
		t.Fatalf("done at the review checkpoint: %v", err)
	}
	debt := display(t, reloadRun(t, "run-1"))
	if debt.Kind != shared.CheckpointReviewDebt || shared.NextExpectedPhase(reloadRun(t, "run-1")) != "" {
		t.Fatalf("fixture: want REVIEW_DEBT on a complete route, got %+v", debt)
	}
	offersRecall := false
	for _, pg := range debt.MenuPages {
		for _, l := range pg.Labels {
			if strings.HasPrefix(l, "Recall: latest") {
				offersRecall = true
			}
		}
	}
	if !offersRecall {
		t.Fatalf("the debt checkpoint must offer the recall page: %+v", debt.MenuPages)
	}
	code, out, stderr := callHookOut(t, continueAnswerPayload(t, debt, "toolu-ask-cont"))
	if code != 0 {
		t.Fatalf("continue at the debt checkpoint: exit = %d (stderr %q)", code, stderr)
	}
	assertReenteredByContinue(t, out, "regression@v2", "REVIEW_DEBT")
}

// TestResultContinueWithACompleteRouteAndAnUnsettledReviewReentersDevelopment
// — the same dead end one checkpoint later: a review that concluded
// NEEDS-FIX parks at REVIEW_RESULT; with the route complete its `continue:`
// re-enters the last development phase too.
func TestResultContinueWithACompleteRouteAndAnUnsettledReviewReentersDevelopment(t *testing.T) {
	r, reviewCp := walkFixRoute(t)
	if err := r.RecordDecision(reviewCp.ID, shared.DecisionContinue, ""); err != nil {
		t.Fatalf("continue at the review checkpoint: %v", err)
	}
	// A REVIEW_RESULT checkpoint whose review concluded NEEDS-FIX: built the
	// way addCheckpoint builds it (kind menu, offered recall history).
	cp := shared.CheckpointState{
		ID: fmt.Sprintf("cp-%d", len(r.Checkpoints)+1), Kind: shared.CheckpointReviewResult,
		Nonce: "n0nc3r501", Note: "COMPLETED NEEDS-FIX", MenuPages: shared.MenuProjection(shared.CheckpointReviewResult),
	}
	for i := range r.Phases {
		if r.Phases[i].Outcome == "accepted" {
			cp.RecallTargets = append(cp.RecallTargets, shared.RecallTarget{VisitID: r.Phases[i].VisitID, Label: r.Phases[i].VisitID, PhaseID: r.Phases[i].ID})
		}
	}
	latest := cp.RecallTargets[len(cp.RecallTargets)-1]
	cp.RecallTargets = append([]shared.RecallTarget{{VisitID: latest.VisitID, Label: "latest", PhaseID: latest.PhaseID}}, cp.RecallTargets...)
	r.Checkpoints = append(r.Checkpoints, cp)
	r.Status = shared.StatusWaitUser
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	result := display(t, r)
	if shared.NextExpectedPhase(reloadRun(t, "run-1")) != "" || reloadRun(t, "run-1").TerminalReviewSettled() {
		t.Fatal("fixture: want a complete route with an unsettled review")
	}
	code, out, stderr := callHookOut(t, continueAnswerPayload(t, result, "toolu-ask-cont"))
	if code != 0 {
		t.Fatalf("continue at the review-result checkpoint: exit = %d (stderr %q)", code, stderr)
	}
	assertReenteredByContinue(t, out, "regression@v2", "REVIEW_RESULT")
}

// TestDebtContinueOnAnIncompleteRouteResumesTheRoute — the mapping is for a
// COMPLETE route only: with phases still to do, `continue:` resumes the
// route exactly as before (no recall, no block, the continuation cue).
func TestDebtContinueOnAnIncompleteRouteResumesTheRoute(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	for _, ph := range []string{"context", "diagnosis"} {
		cp := openDisplayedCheckpoint(t, r, ph)
		if err := r.RecordDecision(cp.ID, shared.DecisionContinue, ""); err != nil {
			t.Fatalf("continue %s: %v", ph, err)
		}
	}
	repro := openDisplayedCheckpoint(t, r, "reproduce")
	if err := r.RecordDecision(repro.ID, shared.DecisionDone, ""); err != nil {
		t.Fatalf("done at reproduce: %v", err)
	}
	debt := display(t, reloadRun(t, "run-1"))
	if debt.Kind != shared.CheckpointReviewDebt || shared.NextExpectedPhase(reloadRun(t, "run-1")) != "fix" {
		t.Fatalf("fixture: want REVIEW_DEBT with fix still to do, got %+v", debt)
	}
	before := len(reloadRun(t, "run-1").Recalls)
	code, out, stderr := callHookOut(t, continueAnswerPayload(t, debt, "toolu-ask-cont"))
	if code != 0 {
		t.Fatalf("continue: exit = %d (stderr %q)", code, stderr)
	}
	after := reloadRun(t, "run-1")
	if after.Status != shared.StatusActive || after.HasOpenPhase() || after.ContinueNudgeFor != debt.ID || len(after.Recalls) != before {
		t.Fatalf("an incomplete route must resume, not recall: status %s open %v cue %q recalls %d→%d",
			after.Status, after.HasOpenPhase(), after.ContinueNudgeFor, before, len(after.Recalls))
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("an ordinary recorded decision writes nothing, got %q", out)
	}
}

// settleTheReview concludes the run's default review with a covering PASS
// (the reducer's own API — the reviewer transcript join is other tests').
func settleTheReview(t *testing.T, r *shared.RunState) {
	t.Helper()
	if _, err := r.RequestReview(shared.Envelope{SchemaVersion: "2", RunID: r.RunID, ReviewID: "rev-1"}, nil); err != nil {
		t.Fatalf("RequestReview: %v", err)
	}
	if err := r.MarkReviewRunning("rev-1"); err != nil {
		t.Fatal(err)
	}
	res := shared.ReviewResult{ReviewID: "rev-1", ArtifactDigest: r.Reviews[0].ArtifactDigest,
		RunStatus: shared.ReviewRunCompleted, Verdict: shared.VerdictPass, EvidenceToolCallIDs: []string{"toolu-rev-1"}}
	for _, c := range r.Reviews[0].Envelope.ExitCriteria {
		res.CriterionVerdicts = append(res.CriterionVerdicts, shared.CriterionVerdict{
			CriterionID: c.ID, Status: "pass", Evidence: []shared.EvidenceAnchor{{ToolUseID: "toolu-rev-1"}}})
	}
	if err := r.RecordReviewResult(res); err != nil {
		t.Fatalf("RecordReviewResult: %v", err)
	}
}

// TestResultContinueAfterASettledPassIsTheFinish — through the RECORDER: at
// the terminal REVIEW_RESULT of a settled review, `continue:` with a
// complete route is the finish, never a recall (round 5, test gap D2/R2:
// each half of the condition survived mutation alone).
func TestResultContinueAfterASettledPassIsTheFinish(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	// The review mints an envelope over the accepted artifacts: the files
	// must exist with the digests the submissions carried.
	for _, ph := range []string{"context", "diagnosis", "reproduce", "fix", "regression", "review"} {
		path := ph + ".md"
		if err := os.WriteFile(filepath.Join(shared.ProjectDir(), path), []byte(ph+" done\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		digest, err := shared.HashArtifactFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.OpenPhase(ph); err != nil {
			t.Fatalf("OpenPhase(%s): %v", ph, err)
		}
		cp, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: ph, Summary: ph + " done",
			Artifacts: []shared.ArtifactRef{{Path: path, Digest: digest, Kind: "doc"}}})
		if err != nil {
			t.Fatalf("SubmitPhase(%s): %v", ph, err)
		}
		if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
			t.Fatal(err)
		}
		if err := r.RecordDecision(cp.ID, shared.DecisionContinue, ""); err != nil {
			t.Fatalf("continue at %s: %v", ph, err)
		}
	}
	settleTheReview(t, reloadRun(t, "run-1"))
	result := display(t, reloadRun(t, "run-1"))
	if result.Kind != shared.CheckpointReviewResult || !reloadRun(t, "run-1").TerminalReviewSettled() {
		t.Fatalf("fixture: want a settled REVIEW_RESULT on a complete route, got %+v", result)
	}
	before := len(reloadRun(t, "run-1").Recalls)
	code, out, stderr := callHookOut(t, continueAnswerPayload(t, result, "toolu-ask-cont"))
	if code != 0 {
		t.Fatalf("continue: exit = %d (stderr %q)", code, stderr)
	}
	after := reloadRun(t, "run-1")
	if after.Status != shared.StatusPostGamePending || len(after.Recalls) != before {
		t.Fatalf("continue after a settled PASS: status %s recalls %d→%d, want POST_GAME_PENDING and no recall", after.Status, before, len(after.Recalls))
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("an ordinary recorded decision writes nothing, got %q", out)
	}
}

// TestDebtContinueSkipsVisitsThatWereNotAccepted — the re-entry target is
// the last phase that was ACCEPTED: a skipped development visit is not
// re-entered (round 5, test gap D5).
func TestDebtContinueSkipsVisitsThatWereNotAccepted(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	for i, ph := range []string{"context", "diagnosis", "reproduce", "fix", "regression"} {
		cp := openDisplayedCheckpoint(t, r, ph)
		decision := shared.DecisionContinue
		if i == 4 { // regression: skipped
			decision = shared.DecisionSkip
		}
		if err := r.RecordDecision(cp.ID, decision, ""); err != nil {
			t.Fatalf("%s at %s: %v", decision, ph, err)
		}
	}
	reviewCp := openDisplayedCheckpoint(t, r, "review")
	if err := r.RecordDecision(reviewCp.ID, shared.DecisionDone, ""); err != nil {
		t.Fatalf("done at the review checkpoint: %v", err)
	}
	debt := display(t, reloadRun(t, "run-1"))
	if debt.Kind != shared.CheckpointReviewDebt {
		t.Fatalf("fixture: want REVIEW_DEBT, got %+v", debt)
	}
	code, out, stderr := callHookOut(t, continueAnswerPayload(t, debt, "toolu-ask-cont"))
	if code != 0 {
		t.Fatalf("continue: exit = %d (stderr %q)", code, stderr)
	}
	assertReenteredByContinue(t, out, "fix@v2", "REVIEW_DEBT")
}
