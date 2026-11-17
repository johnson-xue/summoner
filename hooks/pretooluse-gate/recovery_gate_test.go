package main

// recovery_gate_test.go — recovery-round C3, gate half: the review-submit
// RECOVERY form (identity triple + strict expected_attempt_id) is the
// user-reachable channel out of the wedged RUNNING review, budget-exempt
// (reachable with every budget exhausted), untraced-as-registration (no
// review.prepared for a recovery), while the plain registration form keeps
// the ordinary budget discipline.

import (
	"encoding/json"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// wedgedGateReview drives a fix run into the ORIGINAL C3 wedge through the
// gate fixtures: [v] obligation → review-submit registration → reviewer
// dispatch (RUNNING) → the child dies with no result callback.
func wedgedGateReview(t *testing.T, limits shared.BudgetLimits) *shared.RunState {
	t.Helper()
	r := startFixRun(t, limits)
	parkUnderReviewObligation(t, r, []string{"crit-a"}, nil)
	if _, err := r.RequestReview(envelopeOver("run-1", "rev-1"), nil); err != nil {
		t.Fatalf("RequestReview: %v", err)
	}
	if err := r.StartDelegation("rev-1", true, "tu-parent-1"); err != nil {
		t.Fatalf("StartDelegation: %v", err)
	}
	return r
}

func recoveryPayload(t *testing.T, runID, reviewID, attemptID string) string {
	t.Helper()
	env := envelopeOver(runID, reviewID)
	env.ExpectedAttemptID = attemptID
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return toolPayload(t, "Skill", skillInput("summoner:review-submit", string(b)))
}

// TestGateReviewRecoveryForm — R1: the recovery form parks the wedged review
// at its retry checkpoint; strict attempt metadata refuses; a recovery is not
// a registration (no review.prepared event).
func TestGateReviewRecoveryForm(t *testing.T) {
	useGateEnv(t)
	r := wedgedGateReview(t, shared.BudgetLimits{})
	attID := r.Reviews[0].ActiveAttemptID
	beforePrepared := len(gateEventsOfType(t, "run-1", shared.EventReviewPrepared))

	// Strict twin FIRST (state stays REVIEW_PENDING): a mismatched attempt id
	// never recovers.
	code, stderr := callGate(t, recoveryPayload(t, "run-1", "rev-1", "rev-1-a9"))
	wantExit2(t, code, stderr, "does not match the active attempt")
	if d := reloadRun(t, "run-1"); d.Status != shared.StatusReviewPending {
		t.Fatalf("refused recovery must leave the run REVIEW_PENDING, got %s", d.Status)
	}

	code, stderr = callGate(t, recoveryPayload(t, "run-1", "rev-1", attID))
	if code != 0 {
		t.Fatalf("recovery exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	disk := reloadRun(t, "run-1")
	if disk.Status != shared.StatusWaitUser {
		t.Fatalf("disk status = %s, want WAIT_USER at the retry checkpoint", disk.Status)
	}
	cp := disk.CurrentCheckpoint()
	if cp == nil || cp.ReviewRetry == nil || cp.ReviewRetry.AttemptID != attID {
		t.Fatalf("retry checkpoint = %+v, want the obligation pinning attempt %s", cp, attID)
	}
	if n := len(gateEventsOfType(t, "run-1", shared.EventReviewPrepared)); n != beforePrepared {
		t.Fatalf("review.prepared events = %d, want %d (a recovery registers nothing)", n, beforePrepared)
	}
}

// TestGateReviewRecoveryRunIDBound — re-review F6: the recovery form is
// bound to THIS run. An envelope naming a foreign run id (a replayed payload
// from another session's wedged review) never recovers here — denied before
// effects, state untouched, no registration events.
func TestGateReviewRecoveryRunIDBound(t *testing.T) {
	useGateEnv(t)
	r := wedgedGateReview(t, shared.BudgetLimits{})
	attID := r.Reviews[0].ActiveAttemptID
	beforePrepared := len(gateEventsOfType(t, "run-1", shared.EventReviewPrepared))

	code, stderr := callGate(t, recoveryPayload(t, "run-2", "rev-1", attID))
	wantExit2(t, code, stderr, "run")
	if d := reloadRun(t, "run-1"); d.Status != shared.StatusReviewPending {
		t.Fatalf("foreign-run recovery must leave the run REVIEW_PENDING, got %s", d.Status)
	}
	if n := len(gateEventsOfType(t, "run-1", shared.EventReviewPrepared)); n != beforePrepared {
		t.Fatalf("review.prepared events = %d, want %d (a foreign-run form registers nothing)", n, beforePrepared)
	}

	// The correctly-bound form still recovers (positive control).
	code, stderr = callGate(t, recoveryPayload(t, "run-1", "rev-1", attID))
	if code != 0 {
		t.Fatalf("bound recovery exit = %d, want 0 (stderr %q)", code, stderr)
	}
	if d := reloadRun(t, "run-1"); d.Status != shared.StatusWaitUser {
		t.Fatalf("bound recovery: disk = %s, want WAIT_USER at the retry checkpoint", d.Status)
	}
}

// TestGateReviewRecoveryBudgetExempt — R1: the recovery channel stays
// reachable with EVERY budget exhausted; the plain registration form keeps
// the ordinary budget denial (no blanket review-submit exemption).
func TestGateReviewRecoveryBudgetExempt(t *testing.T) {
	useGateEnv(t)
	r := wedgedGateReview(t, shared.BudgetLimits{Delegations: 1, PhaseToolCalls: 1})
	if exhausted, _ := r.BudgetExhausted(); !exhausted {
		t.Fatal("precondition: budget must be exhausted after the dispatch")
	}

	// The plain form still waits for the extension (no new exemption).
	code, stderr := callGate(t, reviewSubmitPayload(t, envelopeOver("run-1", "rev-1")))
	wantExit2(t, code, stderr, "余量为零")

	// The recovery form is the user's recovery channel: exempt.
	code, stderr = callGate(t, recoveryPayload(t, "run-1", "rev-1", r.Reviews[0].ActiveAttemptID))
	if code != 0 {
		t.Fatalf("budget-exempt recovery exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if d := reloadRun(t, "run-1"); d.Status != shared.StatusWaitUser {
		t.Fatalf("disk status = %s, want WAIT_USER at the retry checkpoint", d.Status)
	}
}
