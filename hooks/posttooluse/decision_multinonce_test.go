package main

// decision_multinonce_test.go — T16 TDD suite, PostToolUse half: the
// multi-nonce fail-closed parser fix. T4's findDecisionQuestion bound the
// FIRST nonce-carrying question; T16 requires exactly ONE per call — two or
// more nonce-carrying questions make the answers map ambiguous, so the whole
// callback is rejected (checkpoint.decision_rejected, exit 0, nothing
// recorded). No new trace event type: the rejection reuses the existing
// decision_rejected event.

import (
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

func TestDecisionRejectedMultipleNonceQuestions(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := openDisplayedCheckpoint(t, r, "diagnosis")

	// TWO questions carrying the CURRENT checkpoint's nonce in one call; the
	// answer picks a valid offered label of the first.
	q1, t1 := cpQuestion(cp.Nonce, false, decisionLabels...)
	q2, t2 := cpQuestion(cp.Nonce, false, decisionLabels[:3]...)
	payload := postUsePayload(t, "AskUserQuestion", "toolu-multi-1",
		map[string]interface{}{"questions": []interface{}{q1, q2}},
		map[string]interface{}{
			"questions": []interface{}{
				map[string]interface{}{"question": t1},
				map[string]interface{}{"question": t2},
			},
			"answers": map[string]interface{}{t1: "Continue: accept and proceed"},
		})

	code, stderr := callHook(t, payload)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 — a rejection is not a hook error (stderr: %q)", code, stderr)
	}

	// Exactly one fail-closed rejection naming the ambiguity; no decision.
	evs := readHookTrace(t, "run-1")
	rej := wantRejected(t, evs, cp.ID, "exactly ONE")
	if got := rej.Payload["reason"]; got == nil {
		t.Fatalf("rejection payload carries no reason: %v", rej.Payload)
	}
	if n := len(eventsOfType(evs, shared.EventCheckpointDecided)); n != 0 {
		t.Fatalf("checkpoint.decided events = %d, want 0 — nothing may be recorded on a multi-nonce call", n)
	}

	// State untouched: the checkpoint stays open and undecided.
	after := reloadRun(t, "run-1")
	if after.Status != shared.StatusWaitUser {
		t.Fatalf("status = %s, want WAIT_USER", after.Status)
	}
	if got := checkpointByID(t, after, cp.ID); got.Decided || got.Decision != "" {
		t.Fatalf("multi-nonce call must record no decision, got %+v", got)
	}

	// Belt: a single-nonce call with the same answer still decides (the fix
	// must not tighten one-question calls).
	useHookEnv(t)
	r2 := startRun(t)
	cp2 := openDisplayedCheckpoint(t, r2, "diagnosis")
	q, qtext := cpQuestion(cp2.Nonce, false, decisionLabels...)
	code, stderr = callHook(t, askDecision(t, "toolu-single-1", q, qtext, "Continue: accept and proceed", nil))
	if code != 0 {
		t.Fatalf("single-nonce decision: exit %d, want 0 (stderr %q)", code, stderr)
	}
	after2 := reloadRun(t, "run-1")
	if got := checkpointByID(t, after2, cp2.ID); !got.Decided || got.Decision != shared.DecisionContinue {
		t.Fatalf("single-nonce call must still record CONTINUE, got %+v", got)
	}
}
