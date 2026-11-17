package main

// recovery_m3_test.go — recovery-round M3: the reviewer delegation's
// attempt identity is traced on BOTH ends — delegation.started carries the
// attempt_id the gate dispatched (the C3 ownership key), and review.verdict
// carries the attempt the verdict concluded — so the trace-side join
// (review-evidence-check.sh) can bind verdicts to the exact attempt window.

import (
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

func TestDelegationStartedCarriesAttemptID(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r, "context")
	if _, err := r.RequestReview(shared.Envelope{SchemaVersion: "2", RunID: "run-1", ReviewID: "rev-m3"}, nil); err != nil {
		t.Fatalf("RequestReview: %v", err)
	}
	wantAttempt := r.Reviews[0].ActiveAttemptID
	capsule, ok := shared.BuildReviewCapsule(r, "rev-m3", wantAttempt)
	if !ok {
		t.Fatalf("BuildReviewCapsule: not ok")
	}
	capsulePrompt, err := capsule.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	code, stderr := callGate(t, toolPayloadID(t, "Agent", "tu-m3-1", agentInput("reviewer", string(capsulePrompt))))
	if code != 0 {
		t.Fatalf("reviewer dispatch exit = %d (stderr %q)", code, stderr)
	}
	var started map[string]interface{}
	for _, ev := range readGateTrace(t, "run-1") {
		if ev.Type == shared.EventDelegationStarted {
			started = ev.Payload
		}
	}
	if started == nil {
		t.Fatal("no delegation.started event")
	}
	if started["attempt_id"] != wantAttempt {
		t.Fatalf("delegation.started attempt_id = %v, want %s (payload %v)", started["attempt_id"], wantAttempt, started)
	}
}
