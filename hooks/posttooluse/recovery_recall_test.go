package main

// recovery_recall_test.go — recovery-round I4, PostToolUse half: the "Recall:
// <target>" answer. The label remainder resolves against the checkpoint's
// offered recall targets inside the reducer; the hook then records
// recall.applied (full provenance + the rework tick) followed by phase.started
// for the re-entered visit — the event pair recovery-check.sh audits.

import (
	"fmt"
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

// TestDebtContinueWithACompleteRouteReentersTheLatestPhase — ship review
// round 3 (interaction F3, second half): DONE with an unsettled terminal
// review parks at REVIEW_DEBT. With every route phase settled its
// `continue:` used to leave the run ACTIVE with nothing to open and no
// checkpoint to close from. It is now the default recall — the ordinary
// recall path, traced like one.
func TestDebtContinueWithACompleteRouteReentersTheLatestPhase(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	for _, ph := range []string{"context", "diagnosis", "reproduce", "fix", "regression"} {
		cp := openDisplayedCheckpoint(t, r, ph)
		if err := r.RecordDecision(cp.ID, shared.DecisionContinue, ""); err != nil {
			t.Fatalf("continue %s: %v", ph, err)
		}
	}
	reviewCp := openDisplayedCheckpoint(t, r, "review")
	if err := r.RecordDecision(reviewCp.ID, shared.DecisionDone, ""); err != nil {
		t.Fatalf("done at the review checkpoint: %v", err)
	}
	debt := reloadRun(t, "run-1").CurrentCheckpoint()
	if debt == nil || debt.Kind != shared.CheckpointReviewDebt {
		t.Fatalf("DONE with an unsettled terminal review must park at REVIEW_DEBT, got %+v", debt)
	}
	cur := reloadRun(t, "run-1")
	if shared.NextExpectedPhase(cur) != "" {
		t.Fatal("fixture: the route must be complete")
	}
	if err := cur.MarkDisplayed(debt.ID, shared.CheckpointContentDigest(*debt, cur.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	if err := cur.Save(); err != nil {
		t.Fatal(err)
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

	page := shared.DecisionPage(*debt)
	continueLabel := ""
	for _, l := range page {
		if strings.HasPrefix(strings.ToLower(l), "continue") {
			continueLabel = l
		}
	}
	q, text := cpQuestion(debt.Nonce, false, page...)
	code, stderr := callHook(t, postUsePayload(t, "AskUserQuestion", "toolu-ask-cont",
		map[string]interface{}{"questions": []interface{}{q}},
		map[string]interface{}{
			"questions": []interface{}{map[string]interface{}{"question": text}},
			"answers":   map[string]interface{}{text: continueLabel},
		}))
	if code != 0 {
		t.Fatalf("continue at the debt checkpoint: exit = %d (stderr %q)", code, stderr)
	}
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
	if len(open) != 1 || open[0] != "review@v2" {
		t.Fatalf("open visits = %v, want [review@v2] (the latest accepted phase, re-entered)", open)
	}
	applied := -1
	for i, ev := range readHookTrace(t, "run-1") {
		if ev.Type == shared.EventRecallApplied {
			applied = i
		}
	}
	if applied < 0 {
		t.Fatal("the re-entry must be traced as a recall (recall.applied)")
	}
}
