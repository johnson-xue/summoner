package shared

// recovery_i4_test.go — recovery-round I4 (adjudication §5): named recall.
// Visits get durable ids; checkpoints offer the run's REAL accepted-visit
// history as recall targets; a "Recall: <target>" answer resolves against
// those offers (unknown target rejected, latest/default keeps today's
// behavior); the applied recall records the full provenance and ticks the
// run-rework budget.

import (
	"strings"
	"testing"
)

// recallRun drives: context accepted (v1), diagnosis accepted (v1), then a
// SECOND diagnosis visit accepted (v2) — history rich enough to name targets.
func recallRun(t *testing.T) *RunState {
	t.Helper()
	prev := runStateDirOverride
	runStateDirOverride = t.TempDir()
	t.Cleanup(func() { runStateDirOverride = prev })
	r, err := StartRun("run-i4", "sess-i4", "fix", BudgetLimits{})
	if err != nil {
		t.Fatal(err)
	}
	for _, ph := range []string{"context", "diagnosis", "reproduce"} {
		acceptVisit(t, r, ph)
	}
	// A second diagnosis round: a NAMED recall of diagnosis@v1, then accept.
	if err := r.OpenPhase("fix"); err != nil {
		t.Fatal(err)
	}
	cp, err := r.SubmitPhaseUnstaged(PhaseSubmission{PhaseID: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.MarkDisplayed(cp.ID, CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordDecisionWithTarget(cp.ID, DecisionRecall, "tu-ask-seed", "diagnosis@v1"); err != nil {
		t.Fatal(err)
	}
	acceptVisit(t, r, "diagnosis")
	return r
}

// acceptVisit opens+submits+accepts one visit through the real reducer (a
// visit a recall already re-opened is submitted as-is).
func acceptVisit(t *testing.T, r *RunState, phaseID string) {
	t.Helper()
	alreadyOpen := false
	for _, p := range r.Phases {
		if p.Open && p.ID == phaseID {
			alreadyOpen = true
		}
	}
	if !alreadyOpen {
		if err := r.OpenPhase(phaseID); err != nil {
			t.Fatalf("OpenPhase(%s): %v", phaseID, err)
		}
	}
	cp, err := r.SubmitPhaseUnstaged(PhaseSubmission{PhaseID: phaseID})
	if err != nil {
		t.Fatalf("SubmitPhase(%s): %v", phaseID, err)
	}
	if err := r.MarkDisplayed(cp.ID, CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordDecision(cp.ID, DecisionContinue, ""); err != nil {
		t.Fatalf("RecordDecision(%s): %v", phaseID, err)
	}
}

// TestCheckpointOffersRecallTargetMenuPage (re-review F1): named recall is
// reachable ONLY through a persisted menu page — the S2 gate pins every
// nonce-carrying question's options to one page exactly, so a checkpoint
// offering real targets must persist a page carrying them ("Recall: latest"
// plus one "Recall: <visit>" per offered visit).
func TestCheckpointOffersRecallTargetMenuPage(t *testing.T) {
	r := recallRun(t)
	if err := r.OpenPhase("fix"); err != nil {
		t.Fatal(err)
	}
	cp, err := r.SubmitPhaseUnstaged(PhaseSubmission{PhaseID: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Recall: latest", "Recall: context@v1", "Recall: reproduce@v1", "Recall: diagnosis@v2"}
	if cp.MenuPageFor(want) == nil {
		t.Fatalf("no persisted menu page offers the recall targets %v (pages: %+v)", want, cp.MenuPages)
	}
	// The kind pages (with the shipped description label) are still there —
	// the default-recall channel survives alongside the target page.
	desc := false
	for _, pg := range cp.MenuPages {
		for _, l := range pg.Labels {
			if strings.HasPrefix(l, "continue:") {
				desc = true
			}
		}
	}
	if !desc {
		t.Fatalf("kind menu pages lost the anchor actions: %+v", cp.MenuPages)
	}
}

// TestStartRunOpensVisitWindow — I1 (adjudication §5): the run START is the
// first lifecycle boundary — tool calls before the first OpenPhase charge the
// upcoming visit's bucket, and the first OpenPhase adopts that accrual
// instead of resetting it.
func TestStartRunOpensVisitWindow(t *testing.T) {
	t.Helper()
	prev := runStateDirOverride
	runStateDirOverride = t.TempDir()
	t.Cleanup(func() { runStateDirOverride = prev })
	r, err := StartRun("run-i1", "sess-i1", "fix", BudgetLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.UpcomingVisit {
		t.Fatal("StartRun must open the upcoming-visit window (UpcomingVisit=true)")
	}
	// Pre-provider work charges; the first OpenPhase adopts it.
	r.CountToolCall("tu-pre-1")
	if err := r.OpenPhase("context"); err != nil {
		t.Fatal(err)
	}
	if r.UpcomingVisit {
		t.Fatal("first OpenPhase must close the window (adopt the accrual)")
	}
	if r.Counters.PhaseToolCalls != 1 {
		t.Fatalf("PhaseToolCalls = %d, want 1 (pre-provider call adopted)", r.Counters.PhaseToolCalls)
	}
	// A SECOND visit opens only after the first closes. The decision boundary
	// already reset the bucket for the upcoming visit (newVisitBucket), and
	// OpenPhase with no intervening charge keeps it at zero.
	acceptVisit(t, r, "context")
	if err := r.OpenPhase("diagnosis"); err != nil {
		t.Fatal(err)
	}
	if r.Counters.PhaseToolCalls != 0 {
		t.Fatalf("second visit PhaseToolCalls = %d, want 0 (fresh bucket)", r.Counters.PhaseToolCalls)
	}
}

func labelsOf(targets []RecallTarget) []string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, t.Label)
	}
	return out
}

// TestVisitIDsAreDurable: every appended visit — first-round, recalled, and
// fresh — carries a stable unique id `<phase>@v<n>`.
func TestVisitIDsAreDurable(t *testing.T) {
	r := recallRun(t)
	var ids []string
	for _, p := range r.Phases {
		if p.VisitID == "" {
			t.Fatalf("visit %s has no VisitID", p.ID)
		}
		ids = append(ids, p.VisitID)
	}
	want := []string{"context@v1", "diagnosis@v1", "reproduce@v1", "fix@v1", "diagnosis@v2"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("visit ids = %v, want %v", ids, want)
	}
}

// TestRecallTargetsPopulatedFromRealHistory — R9: a checkpoint opened after
// real history offers exactly the ACCEPTED visits as named targets plus the
// "latest" default — every label resolves to a real visit id.
func TestRecallTargetsPopulatedFromRealHistory(t *testing.T) {
	r := recallRun(t)
	if err := r.OpenPhase("fix"); err != nil {
		t.Fatal(err)
	}
	cp, err := r.SubmitPhaseUnstaged(PhaseSubmission{PhaseID: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	got := labelsOf(cp.RecallTargets)
	// Accepted visits at this point: context@v1, reproduce@v1, diagnosis@v2
	// (diagnosis@v1 was recalled). Plus the "latest" default = diagnosis@v2.
	want := []string{"latest", "context@v1", "reproduce@v1", "diagnosis@v2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("recall targets = %v, want %v", got, want)
	}
	for _, tgt := range cp.RecallTargets {
		found := false
		for _, p := range r.Phases {
			if p.VisitID == tgt.VisitID && p.ID == tgt.PhaseID {
				found = true
			}
		}
		if !found {
			t.Fatalf("target %s names no real visit (%s/%s)", tgt.Label, tgt.PhaseID, tgt.VisitID)
		}
	}
}

// TestNamedRecallResolvesOfferedTarget — R9: "Recall: context@v1" re-enters
// exactly that visit — not the default (latest) one; the record carries the
// full provenance and the rework budget ticks.
func TestNamedRecallResolvesOfferedTarget(t *testing.T) {
	r := recallRun(t)
	if err := r.OpenPhase("fix"); err != nil {
		t.Fatal(err)
	}
	cp, err := r.SubmitPhaseUnstaged(PhaseSubmission{PhaseID: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.MarkDisplayed(cp.ID, CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	before := r.Counters.RunRework
	if err := r.RecordDecisionWithTarget(cp.ID, DecisionRecall, "tu-ask-7", "context@v1"); err != nil {
		t.Fatalf("named recall: %v", err)
	}
	// The re-entered phase is context (the NAMED target), not diagnosis.
	var open []string
	for _, p := range r.Phases {
		if p.Open {
			open = append(open, p.ID)
		}
	}
	if len(open) != 1 || open[0] != "context" {
		t.Fatalf("open phases after named recall = %v, want [context]", open)
	}
	// The old context@v1 visit is recalled; the new visit is context@v2.
	var ctxVisits []string
	for _, p := range r.Phases {
		if p.ID == "context" {
			ctxVisits = append(ctxVisits, p.VisitID+":"+p.Outcome)
		}
	}
	if strings.Join(ctxVisits, ",") != "context@v1:recalled,context@v2:" {
		t.Fatalf("context visits = %v", ctxVisits)
	}
	// Full provenance on the record + rework tick.
	rec := r.Recalls[len(r.Recalls)-1]
	if rec.TargetVisitID != "context@v1" || rec.Provenance != "ask:"+cp.ID+"/tu-ask-7" ||
		rec.Reason != "context@v1" || rec.Label == "" || rec.TargetPhaseID != "context" {
		t.Fatalf("recall record = %+v", rec)
	}
	if r.Counters.RunRework != before+1 {
		t.Fatalf("RunRework = %d, want %d", r.Counters.RunRework, before+1)
	}
}

// TestNamedRecallUnknownTargetRejected: a target that is not an offered
// recall target of THIS checkpoint is a decision rejection — never a guess.
func TestNamedRecallUnknownTargetRejected(t *testing.T) {
	r := recallRun(t)
	if err := r.OpenPhase("fix"); err != nil {
		t.Fatal(err)
	}
	cp, err := r.SubmitPhaseUnstaged(PhaseSubmission{PhaseID: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.MarkDisplayed(cp.ID, CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	err = r.RecordDecisionWithTarget(cp.ID, DecisionRecall, "tu-ask-8", "review@v9")
	if err == nil || !strings.Contains(err.Error(), "not an offered recall target") {
		t.Fatalf("unknown target error = %v, want not-an-offered-target", err)
	}
	// Nothing applied: the checkpoint is still pending.
	if cp2 := r.CurrentCheckpoint(); cp2 == nil || cp2.ID != cp.ID {
		t.Fatalf("checkpoint state changed after a rejected target")
	}
}

// TestDefaultRecallStillWorks: "" and "latest" keep the default behavior
// (most recent accepted prior phase).
func TestDefaultRecallStillWorks(t *testing.T) {
	for _, target := range []string{"", "latest"} {
		r := recallRun(t)
		if err := r.OpenPhase("fix"); err != nil {
			t.Fatal(err)
		}
		cp, err := r.SubmitPhaseUnstaged(PhaseSubmission{PhaseID: "fix"})
		if err != nil {
			t.Fatal(err)
		}
		if err := r.MarkDisplayed(cp.ID, CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
			t.Fatal(err)
		}
		if err := r.RecordDecisionWithTarget(cp.ID, DecisionRecall, "tu-ask-9", target); err != nil {
			t.Fatalf("default recall (%q): %v", target, err)
		}
		var open []string
		for _, p := range r.Phases {
			if p.Open {
				open = append(open, p.ID)
			}
		}
		if len(open) != 1 || open[0] != "diagnosis" {
			t.Fatalf("default recall (%q) open = %v, want [diagnosis]", target, open)
		}
	}
}
