package shared

import (
	"strings"
	"testing"
)

// Recovery-round C3 (adjudication §3 lines 37–39, recovery-wave brief): the
// wedged RUNNING review — re-send no-ops, RUNNING has no servable dispatch,
// new review ids are refused — becomes recoverable through the existing
// REVIEW_RESULT checkpoint + ReviewRetry obligation, a user-funded
// same-envelope rotation, a late attempt-A conclusion window at its OWN
// retry checkpoint, and a visible UNREVIEWED skip.

// wedgedRunningReview drives a run into the ORIGINAL C3 wedge: a review
// dispatched to RUNNING whose child died without any result callback.
func wedgedRunningReview(t *testing.T, limits BudgetLimits) (*RunState, string, string) {
	t.Helper()
	r := startTestRun(t, limits)
	revID := preparedReview(t, r)
	if err := r.StartDelegation(revID, true, "parent-tu-1"); err != nil {
		t.Fatalf("StartDelegation: %v", err)
	}
	rv := r.FindReview(revID)
	if rv == nil || rv.Status != ReviewRunning {
		t.Fatalf("review not RUNNING after dispatch: %+v", rv)
	}
	return r, revID, rv.ActiveAttemptID
}

// TestRecoverReviewOpensRetryCheckpoint — R1: the recovery operation parks the
// wedged review at an EXISTING-KIND review-result checkpoint carrying the
// ReviewRetry obligation; strict expected_attempt_id; a PREPARED review
// (never dispatched) refuses; state persists atomically.
func TestRecoverReviewOpensRetryCheckpoint(t *testing.T) {
	r, revID, attID := wedgedRunningReview(t, BudgetLimits{})

	if err := r.RecoverReview(revID, attID); err != nil {
		t.Fatalf("RecoverReview: %v", err)
	}
	if r.Status != StatusWaitUser {
		t.Fatalf("status = %s, want WAIT_USER at the retry checkpoint", r.Status)
	}
	cp := r.CurrentCheckpoint()
	if cp == nil || cp.Kind != CheckpointReviewResult {
		t.Fatalf("current checkpoint = %+v, want a REVIEW_RESULT checkpoint", cp)
	}
	rr := cp.ReviewRetry
	if rr == nil {
		t.Fatal("checkpoint carries no ReviewRetry obligation")
	}
	rv := r.FindReview(revID)
	if rr.ReviewID != revID || rr.AttemptID != attID {
		t.Fatalf("ReviewRetry = %+v, want review %s attempt %s", rr, revID, attID)
	}
	if rr.EnvelopeDigest != rv.EnvelopeDigest || rr.ResumeTarget == "" {
		t.Fatalf("ReviewRetry = %+v, want the pinned envelope digest and a resume target", rr)
	}
	// A stays RUNNING (its late result may still conclude at THIS checkpoint).
	if rv.Status != ReviewRunning {
		t.Fatalf("review status = %s, want still RUNNING while parked", rv.Status)
	}
	if att := rv.ActiveAttempt(attID); att == nil || att.State != "RUNNING" {
		t.Fatalf("attempt A must stay RUNNING while parked, got %+v", att)
	}
	// Persisted atomically.
	disk, err := LoadRun("run-1")
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if dcp := disk.CurrentCheckpoint(); dcp == nil || dcp.ReviewRetry == nil {
		t.Fatal("disk state carries no retry checkpoint — RecoverReview must persist")
	}
}

// TestRecoverReviewStrictness — R1 twins: wrong attempt id, unknown review,
// PREPARED (never dispatched) review, and non-REVIEW_PENDING status all
// refuse with state untouched.
func TestRecoverReviewStrictness(t *testing.T) {
	r, revID, attID := wedgedRunningReview(t, BudgetLimits{})

	if err := r.RecoverReview(revID, attID+"-ghost"); err == nil {
		t.Fatal("mismatched expected_attempt_id: want error, got nil")
	}
	if err := r.RecoverReview("rev-ghost", attID); err == nil {
		t.Fatal("unknown review id: want error, got nil")
	}
	if r.Status != StatusReviewPending {
		t.Fatalf("refused recoveries must leave the run REVIEW_PENDING, got %s", r.Status)
	}

	// A PREPARED review was never dispatched — no wedge, no recovery.
	r2 := startTestRun(t, BudgetLimits{})
	rev2 := preparedReview(t, r2)
	if err := r2.RecoverReview(rev2, r2.FindReview(rev2).ActiveAttemptID); err == nil {
		t.Fatal("PREPARED review recovery: want error (nothing is wedged), got nil")
	}
}

// TestRecoverReviewBudgetExempt — R1: the recovery stays reachable with EVERY
// budget exhausted (only the retry dispatch itself needs funding).
func TestRecoverReviewBudgetExempt(t *testing.T) {
	r, revID, attID := wedgedRunningReview(t, BudgetLimits{Delegations: 1, PhaseToolCalls: 1})
	// StartDelegation consumed the only delegation and the only tool call.
	if exhausted, _ := r.BudgetExhausted(); !exhausted {
		t.Fatal("precondition: budget must be exhausted")
	}
	if err := r.RecoverReview(revID, attID); err != nil {
		t.Fatalf("RecoverReview with every budget exhausted: %v", err)
	}
	if r.Status != StatusWaitUser {
		t.Fatalf("status = %s, want WAIT_USER", r.Status)
	}
}

// TestRetryRotationOnContinue — R2: CONTINUE on the retry checkpoint is the
// funded same-envelope rotation — A retired, PREPARED B minted, capsule
// servable, dispatch charges exactly once.
func TestRetryRotationOnContinue(t *testing.T) {
	r, revID, attID := wedgedRunningReview(t, BudgetLimits{})
	if err := r.RecoverReview(revID, attID); err != nil {
		t.Fatalf("RecoverReview: %v", err)
	}
	cp := r.CurrentCheckpoint()
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue)

	rv := r.FindReview(revID)
	if rv.Status != ReviewPrepared {
		t.Fatalf("review status = %s, want PREPARED after rotation", rv.Status)
	}
	if len(rv.Attempts) != 2 {
		t.Fatalf("attempts = %d, want 2 (append-only rotation)", len(rv.Attempts))
	}
	a := rv.ActiveAttempt(attID)
	if a == nil || a.State != "RETIRED" || a.RetiredAt == "" {
		t.Fatalf("attempt A = %+v, want RETIRED with a timestamp", a)
	}
	b := rv.ActiveAttempt(rv.ActiveAttemptID)
	if b == nil || b.State != "PREPARED" || b.AttemptID == attID || b.Token == "" || b.Token == a.Token {
		t.Fatalf("attempt B = %+v, want a fresh PREPARED attempt with its own token", b)
	}
	if r.Status != StatusReviewPending {
		t.Fatalf("run status = %s, want REVIEW_PENDING (dispatchable again)", r.Status)
	}
	// The retry checkpoint closed through the normal decision path and its
	// obligation is consumed (no second rotation off it).
	var decided *CheckpointState
	for i := range r.Checkpoints {
		if r.Checkpoints[i].ID == cp.ID {
			decided = &r.Checkpoints[i]
		}
	}
	if decided == nil || !decided.Decided || decided.Decision != DecisionContinue || decided.ReviewRetry != nil {
		t.Fatalf("retry checkpoint after rotation = %+v, want decided CONTINUE with the obligation consumed", decided)
	}
	// The fresh capsule serves for B and binds a new dispatch.
	if err := r.StartDelegation(revID, true, "parent-tu-2"); err != nil {
		t.Fatalf("re-dispatch of rotated attempt B: %v", err)
	}
	if r.Counters.Delegations != 2 {
		t.Fatalf("delegations = %d, want 2 (the retry dispatch charged once)", r.Counters.Delegations)
	}
	if b2 := r.FindReview(revID).ActiveAttempt(r.FindReview(revID).ActiveAttemptID); b2.State != "RUNNING" {
		t.Fatalf("attempt B = %s, want RUNNING after re-dispatch", b2.State)
	}
}

// TestRetryRotationInsufficientFunding — R3: with the delegation limit
// exhausted and no riding extension, the retry answer is rejected and the
// checkpoint stays open/decidable; a riding extension funds the rotation.
func TestRetryRotationInsufficientFunding(t *testing.T) {
	r, revID, attID := wedgedRunningReview(t, BudgetLimits{Delegations: 1})
	if err := r.RecoverReview(revID, attID); err != nil {
		t.Fatalf("RecoverReview: %v", err)
	}
	cp := r.CurrentCheckpoint()
	displayCurrent(t, r)

	if err := r.RecordDecision(cp.ID, DecisionContinue, "tu-retry-1"); err == nil {
		t.Fatal("unfunded rotation: want error (no free Agent), got nil")
	}
	// The checkpoint stays open and decidable; nothing rotated.
	if c := r.CurrentCheckpoint(); c == nil || c.ID != cp.ID || c.Decided {
		t.Fatalf("refused rotation must leave the retry checkpoint open, got %+v", c)
	}
	rv := r.FindReview(revID)
	if rv.Status != ReviewRunning || len(rv.Attempts) != 1 {
		t.Fatalf("refused rotation must not mutate the review: %s / %d attempts", rv.Status, len(rv.Attempts))
	}
	if r.Counters.Delegations != 1 {
		t.Fatalf("delegations = %d, want 1 (no invented budget)", r.Counters.Delegations)
	}

	// The extension rides the SAME answer: one commit funds and rotates.
	settle := &BudgetSettlement{ToolUseID: "tu-retry-2", Extend: true}
	if err := r.RecordDecisionWithBudget(cp.ID, DecisionContinue, "tu-retry-2", settle); err != nil {
		t.Fatalf("funded rotation via riding extension: %v", err)
	}
	rv = r.FindReview(revID)
	if rv.Status != ReviewPrepared || len(rv.Attempts) != 2 {
		t.Fatalf("funded rotation = %s / %d attempts, want PREPARED with attempt B", rv.Status, len(rv.Attempts))
	}
}

// TestLateAttemptAConcludesAtOwnRetryCheckpoint — R4: while parked at ITS OWN
// retry checkpoint, attempt A's late result concludes the review, supersedes
// the retry checkpoint and opens a fresh result display; any other WAIT_USER
// checkpoint refuses; after rotation A is archival only.
func TestLateAttemptAConcludesAtOwnRetryCheckpoint(t *testing.T) {
	r, revID, attID := wedgedRunningReview(t, BudgetLimits{})
	if err := r.RecoverReview(revID, attID); err != nil {
		t.Fatalf("RecoverReview: %v", err)
	}
	cp := r.CurrentCheckpoint()

	if err := r.RecordReviewResult(coveringResult(r, revID, "pass")); err != nil {
		t.Fatalf("late A result at its own retry checkpoint: %v", err)
	}
	// commit swaps the in-memory state: re-read the retry checkpoint by id.
	superseded := false
	for i := range r.Checkpoints {
		if r.Checkpoints[i].ID == cp.ID {
			superseded = r.Checkpoints[i].Superseded
		}
	}
	if !superseded {
		t.Fatal("the retry checkpoint must be superseded by the fresh result display")
	}
	fresh := r.CurrentCheckpoint()
	if fresh == nil || fresh.ID == cp.ID || fresh.Kind != CheckpointReviewResult {
		t.Fatalf("fresh result display = %+v", fresh)
	}
	rv := r.FindReview(revID)
	if rv.Status != ReviewReviewed || rv.Verdict != VerdictPass {
		t.Fatalf("review = %s/%s, want REVIEWED PASS", rv.Status, rv.Verdict)
	}
	if att := rv.ActiveAttempt(attID); att == nil || att.State != "RETIRED" {
		t.Fatalf("attempt A = %+v, want RETIRED after concluding", att)
	}
	if r.Status != StatusWaitUser {
		t.Fatalf("run = %s, want WAIT_USER at the fresh display", r.Status)
	}

	// After rotation, A is archival only: its result never concludes B's review.
	r2, rev2, att2 := wedgedRunningReview(t, BudgetLimits{})
	if err := r2.RecoverReview(rev2, att2); err != nil {
		t.Fatalf("RecoverReview: %v", err)
	}
	displayCurrent(t, r2)
	decideCurrent(t, r2, DecisionContinue)
	before := r2.FindReview(rev2)
	beforeActive, beforeCp := before.ActiveAttemptID, len(r2.Checkpoints)
	late := coveringResult(r2, rev2, "pass")
	late.AttemptID = att2 // A's child names ITS attempt in the handback
	if err := r2.RecordReviewResult(late); err == nil {
		t.Fatal("retired A's late result after rotation: want refusal, got nil")
	} else if !strings.Contains(err.Error(), "retired") {
		t.Fatalf("refusal should name the retired attempt, got: %v", err)
	}
	// F2: the refusal is ARCHIVAL — no mutation at all. B stays in flight
	// (PREPARED, active attempt unchanged), the run stays REVIEW_PENDING, no
	// checkpoint is opened: "late retired A never concludes/fails B".
	after := r2.FindReview(rev2)
	if after.Status != ReviewPrepared {
		t.Fatalf("review = %s after A's archival refusal, want PREPARED (B in flight)", after.Status)
	}
	if after.ActiveAttemptID != beforeActive || after.ActiveAttemptID == att2 {
		t.Fatalf("active attempt = %q, want unchanged %q (A %q must not be re-activated)", after.ActiveAttemptID, beforeActive, att2)
	}
	if r2.Status != StatusReviewPending {
		t.Fatalf("run = %s after A's archival refusal, want REVIEW_PENDING", r2.Status)
	}
	if len(r2.Checkpoints) != beforeCp {
		t.Fatalf("checkpoints = %d after A's archival refusal, want %d (no FAILED routing)", len(r2.Checkpoints), beforeCp)
	}
}

// TestRetryStopSettlesSkipped — re-review F4: STOP on a retry-pending
// checkpoint settles the review visibly UNREVIEWED (SKIPPED, no verdict)
// instead of leaving an in-flight review dangling over a stopped run; the
// stop itself stands (POST_GAME_PENDING, exit journal is the caller's duty)
// and the terminal-⑤ debt is NOT discharged.
func TestRetryStopSettlesSkipped(t *testing.T) {
	r, revID, attID := wedgedRunningReview(t, BudgetLimits{})
	if err := r.RecoverReview(revID, attID); err != nil {
		t.Fatalf("RecoverReview: %v", err)
	}
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionStop)

	rv := r.FindReview(revID)
	if rv.Status != ReviewSkipped {
		t.Fatalf("review = %s after stop at the retry checkpoint, want SKIPPED (never dangling in flight)", rv.Status)
	}
	if rv.Verdict != "" {
		t.Fatalf("stopped review must carry no verdict, got %q", rv.Verdict)
	}
	if att := rv.ActiveAttempt(attID); att == nil || att.State != "RETIRED" {
		t.Fatalf("attempt A = %+v, want RETIRED", att)
	}
	if r.Status != StatusPostGamePending {
		t.Fatalf("run = %s, want POST_GAME_PENDING (the stop stands)", r.Status)
	}
	if cur := r.CurrentCheckpoint(); cur != nil && cur.ReviewRetry != nil {
		t.Fatal("ReviewRetry must be consumed by the stop")
	}
	if r.terminalReviewSettled() {
		t.Fatal("a stopped-skipped retry must not settle the terminal review debt")
	}
}

// TestRetrySkipConcludesUnreviewed — R5: declining the retry ends the review
// visibly UNREVIEWED (never a fabricated PASS) and the run resumes ACTIVE;
// the terminal-⑤ debt is NOT discharged (SKIPPED ≠ settled).
func TestRetrySkipConcludesUnreviewed(t *testing.T) {
	r, revID, attID := wedgedRunningReview(t, BudgetLimits{})
	if err := r.RecoverReview(revID, attID); err != nil {
		t.Fatalf("RecoverReview: %v", err)
	}
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionSkip)

	rv := r.FindReview(revID)
	if rv.Status != ReviewSkipped {
		t.Fatalf("review status = %s, want SKIPPED (visible UNREVIEWED)", rv.Status)
	}
	if rv.Verdict != "" {
		t.Fatalf("skipped review must carry no verdict, got %q", rv.Verdict)
	}
	if r.Status != StatusActive {
		t.Fatalf("run = %s, want ACTIVE (work resumes)", r.Status)
	}
	if r.CurrentCheckpoint() != nil && r.CurrentCheckpoint().ReviewRetry != nil {
		t.Fatal("ReviewRetry must be consumed by the skip")
	}
	// SKIPPED never discharges the mandatory terminal review debt.
	if r.terminalReviewSettled() {
		t.Fatal("a skipped retry must not settle the terminal review debt")
	}
}
