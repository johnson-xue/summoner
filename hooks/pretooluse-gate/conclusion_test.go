package main

import (
	"strings"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// conclusion_test.go — the structured conclusion of a phase submission at the
// gate (0.7.0 D1). The payload is model-authored; the gate validates the
// shape a human cannot do without (diagnosis must state its conclusion and
// how it was reached) and rejects a malformed outcome enum BEFORE any effect.

// rawSubmitArgs builds a phase-submit args JSON with no helper defaults —
// exactly the fields the test names.
func rawSubmitArgs(t *testing.T, fields map[string]interface{}) string {
	t.Helper()
	return mustJSONStr(t, fields)
}

// openDiagnosis parks a fix run with context accepted and diagnosis OPEN —
// the state a diagnosis submission arrives in.
func openDiagnosis(t *testing.T) *shared.RunState {
	t.Helper()
	r := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r, "context")
	if err := r.OpenPhase("diagnosis"); err != nil {
		t.Fatalf("OpenPhase(diagnosis): %v", err)
	}
	return r
}

func submitCall(t *testing.T, id string, fields map[string]interface{}) (int, string) {
	t.Helper()
	return callGate(t, toolPayloadID(t, "Skill", id, skillInput("summoner:phase-submit", rawSubmitArgs(t, fields))))
}

// TestDiagnosisSubmissionRequiresSummaryAndApproach: a diagnosis without a
// stated conclusion, or without how it was reached, is rejected before any
// effect and names the missing field; the complete submission opens the
// checkpoint. Each rejection is traced (ruling 6).
func TestDiagnosisSubmissionRequiresSummaryAndApproach(t *testing.T) {
	useGateEnv(t)
	openDiagnosis(t)

	code, stderr := submitCall(t, "tu-s1", map[string]interface{}{
		"phase_id": "diagnosis", "summary": "found it",
	})
	wantExit2(t, code, stderr, "conclusion.summary")

	code, stderr = submitCall(t, "tu-s2", map[string]interface{}{
		"phase_id": "diagnosis", "conclusion": map[string]interface{}{"summary": "root cause X"},
	})
	wantExit2(t, code, stderr, "conclusion.approach")

	// Nothing moved: diagnosis is still open, no checkpoint exists.
	after := reloadRun(t, "run-1")
	if after.Status != shared.StatusActive || len(after.Checkpoints) != 1 {
		t.Fatalf("a rejected submission must consume nothing: status %s, %d checkpoints", after.Status, len(after.Checkpoints))
	}
	if !after.Phases[len(after.Phases)-1].Open {
		t.Fatal("diagnosis must stay open after a rejected submission")
	}

	code, stderr = submitCall(t, "tu-s3", map[string]interface{}{
		"phase_id": "diagnosis",
		"conclusion": map[string]interface{}{
			"summary":  "root cause X",
			"approach": "read the call chain from the error site",
		},
	})
	if code != 0 {
		t.Fatalf("complete diagnosis submission: exit %d, want 0 (stderr: %q)", code, stderr)
	}
	if got := reloadRun(t, "run-1"); got.Status != shared.StatusWaitUser {
		t.Fatalf("status = %s, want WAIT_USER", got.Status)
	}

	rejected := 0
	for _, ev := range readGateTrace(t, "run-1") {
		if ev.Type == shared.EventPhaseSubmittedRejected {
			rejected++
		}
	}
	if rejected != 2 {
		t.Fatalf("phase_submitted.rejected events = %d, want 2", rejected)
	}
}

// TestOtherPhasesSubmitWithoutAConclusion: only diagnosis forces the
// conclusion fields; every other phase still submits conclusion-free and
// lands on a pending checkpoint (the render then says what is missing).
func TestOtherPhasesSubmitWithoutAConclusion(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	if err := r.OpenPhase("context"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	code, stderr := submitCall(t, "tu-c1", map[string]interface{}{"phase_id": "context", "summary": "context done"})
	if code != 0 {
		t.Fatalf("conclusion-free context submission: exit %d, want 0 (stderr: %q)", code, stderr)
	}
	got := reloadRun(t, "run-1")
	if got.Status != shared.StatusWaitUser {
		t.Fatalf("status = %s, want WAIT_USER", got.Status)
	}
	if cp := got.CurrentCheckpoint(); cp == nil || cp.Outcome != shared.ConclusionUnclear || cp.Conclusion != nil {
		t.Fatalf("a conclusion-free submission stores outcome unclear and no conclusion, got %+v", cp)
	}
}

// TestSubmissionOutcomeEnumIsValidated: an outcome outside
// confirmed|failed|unclear is a malformed payload for a known channel —
// denied before any effect, naming the legal values.
func TestSubmissionOutcomeEnumIsValidated(t *testing.T) {
	useGateEnv(t)
	openDiagnosis(t)

	code, stderr := submitCall(t, "tu-o1", map[string]interface{}{
		"phase_id": "diagnosis", "outcome": "definitely-fixed",
		"conclusion": map[string]interface{}{"summary": "s", "approach": "a"},
	})
	wantExit2(t, code, stderr, "confirmed")
	if !strings.Contains(stderr, "unclear") {
		t.Fatalf("stderr %q must name the legal outcome values", stderr)
	}
	if got := reloadRun(t, "run-1"); got.Status != shared.StatusActive {
		t.Fatalf("an invalid outcome must consume nothing, status = %s", got.Status)
	}
}

// TestOpenQuestionsForceAnUnclearOutcome: a submission that lists open
// questions cannot claim `confirmed` — the stored outcome is `unclear`, so
// the checkpoint asks instead of asserting a conclusion that has holes.
func TestOpenQuestionsForceAnUnclearOutcome(t *testing.T) {
	useGateEnv(t)
	openDiagnosis(t)

	code, stderr := submitCall(t, "tu-q1", map[string]interface{}{
		"phase_id": "diagnosis", "outcome": "confirmed",
		"conclusion": map[string]interface{}{
			"summary":        "root cause X",
			"approach":       "read the call chain",
			"open_questions": []string{"why is the hero banned in team mode"},
		},
	})
	if code != 0 {
		t.Fatalf("exit %d, want 0 (stderr: %q)", code, stderr)
	}
	cp := reloadRun(t, "run-1").CurrentCheckpoint()
	if cp == nil || cp.Outcome != shared.ConclusionUnclear {
		t.Fatalf("stored outcome = %q, want unclear (open questions present)", cp.Outcome)
	}
}
