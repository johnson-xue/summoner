package main

// recovery_m3_test.go — recovery-round M3, SubagentStop half: review.verdict
// carries attempt_id — the attempt the verdict concluded — so the trace-side
// join binds a verdict to the exact reviewer delegation window that produced
// it (delegation.started.attempt_id ↔ review.verdict.attempt_id).

import (
	"encoding/json"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

func TestReviewVerdictCarriesAttemptID(t *testing.T) {
	useSubagentEnv(t)
	r := startRun(t)
	startRunningReview(t, r, "rev-m3")
	wantAttempt := r.FindReview("rev-m3").ActiveAttemptID
	resJSON, err := json.Marshal(passingResult(t, r, "rev-m3"))
	if err != nil {
		t.Fatal(err)
	}
	path := writeTranscript(t, capsuleOf(t, r, "rev-m3"), string(resJSON))

	code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", path))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	verdicts := eventsOfType(readTrace(t, "run-1"), shared.EventReviewVerdict)
	if len(verdicts) != 1 {
		t.Fatalf("review.verdict events = %d, want 1", len(verdicts))
	}
	if verdicts[0].Payload["attempt_id"] != wantAttempt {
		t.Fatalf("review.verdict attempt_id = %v, want %s (payload %v)",
			verdicts[0].Payload["attempt_id"], wantAttempt, verdicts[0].Payload)
	}
}
