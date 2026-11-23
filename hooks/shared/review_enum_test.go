package shared

import (
	"fmt"
	"strings"
	"testing"
)

// review_enum_test.go — reviewer wire enums (0.7.0 D5.1). The incident run
// lost its review to casing: the reviewer wrote a criterion status in
// uppercase and the framework failed the whole review over it — the user
// was shown a protocol error where a verdict should be.

// passResult builds a COMPLETED PASS result covering every criterion of the
// minted envelope, with the given status spelling.
func passResult(r *RunState, status string) ReviewResult {
	res := ReviewResult{
		ReviewID:            r.Reviews[0].ReviewID,
		RunStatus:           ReviewRunCompleted,
		Verdict:             VerdictPass,
		ArtifactDigest:      r.Reviews[0].ArtifactDigest,
		EvidenceToolCallIDs: []string{"toolu_1"},
	}
	for _, c := range r.Reviews[0].Envelope.ExitCriteria {
		res.CriterionVerdicts = append(res.CriterionVerdicts, CriterionVerdict{
			CriterionID: c.ID,
			Status:      status,
			Evidence:    []EvidenceAnchor{{ToolUseID: "toolu_1", Path: "p1.md"}},
		})
	}
	return res
}

// runningReview drives a run to a RUNNING review (dispatched, result owed).
func runningReview(t *testing.T) *RunState {
	t.Helper()
	r := startTestRun(t, BudgetLimits{})
	revID := preparedReview(t, r)
	if err := r.StartDelegation(revID, true, "parent-tu-1"); err != nil {
		t.Fatalf("StartDelegation: %v", err)
	}
	return r
}

// TestReviewEnumsAreCaseInsensitive: a verdict whose enums differ from the
// canonical spelling only in case or surrounding whitespace concludes the
// review — for the per-criterion status (lowercase by contract) and for the
// aggregate run_status / verdict (uppercase by contract).
func TestReviewEnumsAreCaseInsensitive(t *testing.T) {
	for _, status := range []string{" PASS ", "Pass"} {
		r := runningReview(t)
		res := passResult(r, status)
		res.RunStatus, res.Verdict = " completed ", "Pass"
		if err := r.RecordReviewResult(res); err != nil {
			t.Fatalf("criterion status %q, run_status %q, verdict %q: a verdict that differs only in case/space must conclude the review, got %v", status, res.RunStatus, res.Verdict, err)
		}
		if r.Reviews[0].Status != ReviewReviewed || r.Reviews[0].Verdict != VerdictPass {
			t.Fatalf("review = %s / %q, want REVIEWED / PASS", r.Reviews[0].Status, r.Reviews[0].Verdict)
		}
	}

	// A BLOCKED per-criterion verdict under a BLOCKED run — the incident's
	// exact shape: the optional criterion is spelled in uppercase.
	r := runningReview(t)
	res := ReviewResult{
		ReviewID:       r.Reviews[0].ReviewID,
		RunStatus:      "Blocked",
		ReasonCode:     "l2_inactive",
		ArtifactDigest: r.Reviews[0].ArtifactDigest,
		CriterionVerdicts: []CriterionVerdict{{
			CriterionID: r.Reviews[0].Envelope.ExitCriteria[0].ID,
			Status:      "BLOCKED",
		}},
	}
	if err := r.RecordReviewResult(res); err != nil {
		t.Fatalf("a BLOCKED verdict spelled in uppercase must conclude the review, got %v", err)
	}
	if r.Reviews[0].Status != ReviewBlocked {
		t.Fatalf("review = %s, want BLOCKED", r.Reviews[0].Status)
	}
}

// TestReviewEnumOutsideTheVocabularyIsStillAProtocolError: tolerance is for
// spelling, not meaning — an unknown status fails the review, and the
// checkpoint tells the user what that means for them.
func TestReviewEnumOutsideTheVocabularyIsStillAProtocolError(t *testing.T) {
	r := runningReview(t)
	if err := r.RecordReviewResult(passResult(r, "passed")); err == nil {
		t.Fatal("an unknown criterion status must fail the review")
	}
	if r.Reviews[0].Status != ReviewFailed {
		t.Fatalf("review = %s, want FAILED", r.Reviews[0].Status)
	}
	cp := r.CurrentCheckpoint()
	if cp == nil || cp.Kind != CheckpointReviewResult || cp.ReviewDisposition != "FAILED" {
		t.Fatalf("the failure must route to a review-result checkpoint, got %+v", cp)
	}
	render := RenderCheckpointMessage(r, *cp)
	for _, want := range []string{"框架未能完成审查", "对你的影响：改动尚未被审查", "must be pass, fail or blocked"} {
		if !strings.Contains(render, want) {
			t.Errorf("a framework validation failure must say so and what it means for the user, lost %q:\n%s", want, render)
		}
	}
	if strings.Contains(render, fmt.Sprintf("%s %s", ReviewRunCompleted, VerdictPass)) {
		t.Errorf("a failed framework validation must never read like a quality conclusion:\n%s", render)
	}
}
