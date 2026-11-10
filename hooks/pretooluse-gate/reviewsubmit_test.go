package main

// reviewsubmit_test.go — T17 suite, gate half: the Skill(summoner:review-submit)
// channel (legal ONLY in REVIEW_PENDING; DecodeEnvelope + ValidateEnvelope
// against the frozen view minus drops; RequestReview → review.prepared) and
// the standalone-review freeze contract (review/ship runs' context/prepare
// submission carries criteria ⊆ the effective verification_spec; CONTINUE
// freezes them before any envelope — the vacuous-view guard then rejects an
// envelope submitted before that freeze).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// --- review-submit fixtures --------------------------------------------------------

// submitDisplayedPhase opens a phase, submits it with criteria and binds the
// display — the run sits WAIT_USER at the phase's checkpoint, ready for a
// decision or a drop ride-along.
func submitDisplayedPhase(t *testing.T, r *shared.RunState, phaseID string, criteria []string) shared.CheckpointState {
	t.Helper()
	if err := r.OpenPhase(phaseID); err != nil {
		t.Fatalf("OpenPhase(%s): %v", phaseID, err)
	}
	cp, err := r.SubmitPhase(shared.PhaseSubmission{
		PhaseID:     phaseID,
		Summary:     phaseID + " done",
		CriteriaIDs: criteria,
		Artifacts:   []shared.ArtifactRef{{Path: phaseID + ".md", Digest: "d-" + phaseID, Kind: "doc"}},
	})
	if err != nil {
		t.Fatalf("SubmitPhase(%s): %v", phaseID, err)
	}
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed(%s): %v", cp.ID, err)
	}
	return cp
}

// parkUnderReviewObligation drives one accepted phase (its criteria frozen
// into the acceptance view) plus a second submitted+displayed checkpoint,
// then a [v] decision there — the run ends REVIEW_PENDING with a persisted
// obligation for review-submit to consume. drops are recorded BEFORE the [v]
// decision, exactly as the ride-along channel orders them.
func parkUnderReviewObligation(t *testing.T, r *shared.RunState, frozen, pending []string, drops ...string) shared.CheckpointState {
	t.Helper()
	acceptPhaseWithCriteria(t, r, "context", frozen)
	cp := submitDisplayedPhase(t, r, "diagnosis", pending)
	for _, id := range drops {
		if _, err := r.RecordCriteriaDrop(cp.ID, id, "tu-drop-x"); err != nil {
			t.Fatalf("RecordCriteriaDrop(%s): %v", id, err)
		}
	}
	if err := r.RecordDecision(cp.ID, shared.DecisionReviewOnDemand, ""); err != nil {
		t.Fatalf("RecordDecision(REVIEW_ON_DEMAND): %v", err)
	}
	return cp
}

// acceptPhaseWithCriteria is acceptPhase with explicit criteria (the frozen
// view is what review-submit validates against).
func acceptPhaseWithCriteria(t *testing.T, r *shared.RunState, phaseID string, criteria []string) {
	t.Helper()
	cp := submitDisplayedPhase(t, r, phaseID, criteria)
	if err := r.RecordDecision(cp.ID, shared.DecisionContinue, ""); err != nil {
		t.Fatalf("RecordDecision(%s): %v", cp.ID, err)
	}
}

// envelopeOver builds a minimal legal envelope whose exit criteria are
// exactly the given ids (judgment method — no registry in these fixtures).
func envelopeOver(runID, reviewID string, ids ...string) shared.Envelope {
	env := shared.Envelope{
		SchemaVersion: "2",
		RunID:         runID,
		ReviewID:      reviewID,
		ArtifactSet:   []shared.ArtifactRef{{Path: "diagnosis.md", Digest: "d-diagnosis", Kind: "doc"}},
	}
	for _, id := range ids {
		env.ExitCriteria = append(env.ExitCriteria, shared.Criterion{ID: id, Method: shared.MethodJudgment})
	}
	return env
}

// reviewSubmitPayload builds the Skill(summoner:review-submit) gate payload
// carrying one envelope as args.
func reviewSubmitPayload(t *testing.T, env shared.Envelope) string {
	t.Helper()
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return toolPayload(t, "Skill", skillInput("summoner:review-submit", string(b)))
}

func gateEventsOfType(t *testing.T, runID, typ string) []shared.TraceEvent {
	t.Helper()
	var out []shared.TraceEvent
	for _, ev := range readGateTrace(t, runID) {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

// --- the channel -------------------------------------------------------------------

// TestReviewSubmitChannelLegality: review-submit is the envelope registration
// channel and it is legal ONLY in REVIEW_PENDING — other statuses and
// malformed payloads are exit-2 denials with mapped reasons; a successful
// call consumes the [v] obligation, registers the review PREPARED and traces
// review.prepared exactly once.
func TestReviewSubmitChannelLegality(t *testing.T) {
	t.Run("ACTIVE refused", func(t *testing.T) {
		useGateEnv(t)
		startFixRun(t, shared.BudgetLimits{})
		code, stderr := callGate(t, reviewSubmitPayload(t, envelopeOver("run-1", "rev-1", "crit-a")))
		wantExit2(t, code, stderr, "REVIEW_PENDING")
	})

	t.Run("WAIT_USER refused (checkpoint discipline)", func(t *testing.T) {
		useGateEnv(t)
		r := startFixRun(t, shared.BudgetLimits{})
		submitDisplayedPhase(t, r, "context", nil)
		code, stderr := callGate(t, reviewSubmitPayload(t, envelopeOver("run-1", "rev-1", "crit-a")))
		wantExit2(t, code, stderr, "WAIT_USER")
	})

	t.Run("malformed payload refused", func(t *testing.T) {
		useGateEnv(t)
		r := startFixRun(t, shared.BudgetLimits{})
		parkUnderReviewObligation(t, r, []string{"crit-a"}, nil)

		code, stderr := callGate(t, toolPayload(t, "Skill", skillInput("summoner:review-submit", "")))
		wantExit2(t, code, stderr, "requires args")

		code, stderr = callGate(t, toolPayload(t, "Skill", skillInput("summoner:review-submit", "not json")))
		wantExit2(t, code, stderr, "envelope")

		// Strict allow-list: an unknown envelope field is a parse error.
		code, stderr = callGate(t, toolPayload(t, "Skill", skillInput("summoner:review-submit",
			`{"schema_version":"2","run_id":"run-1","review_id":"rev-1","artifact_set":[{"path":"x","digest":"d"}],"factual_claim":"looks great"}`)))
		wantExit2(t, code, stderr, "envelope")
	})

	t.Run("nothing authorizes a second in-flight review", func(t *testing.T) {
		useGateEnv(t)
		r := startFixRun(t, shared.BudgetLimits{})
		parkUnderReviewObligation(t, r, []string{"crit-a"}, nil)
		// Consume the obligation through the reducer: rev-1 is now PREPARED
		// and in flight — a NEW review id via the channel is refused.
		if _, err := r.RequestReview(envelopeOver("run-1", "rev-1", "crit-a")); err != nil {
			t.Fatal(err)
		}
		code, stderr := callGate(t, reviewSubmitPayload(t, envelopeOver("run-1", "rev-2", "crit-a")))
		wantExit2(t, code, stderr, "one review at a time")
		if n := len(reloadRun(t, "run-1").Reviews); n != 1 {
			t.Fatalf("reviews = %d, want only the in-flight one", n)
		}
	})

	t.Run("obligation-consumed registration", func(t *testing.T) {
		useGateEnv(t)
		r := startFixRun(t, shared.BudgetLimits{})
		vCP := parkUnderReviewObligation(t, r, []string{"crit-a", "crit-b"}, []string{"crit-b"})

		code, stderr := callGate(t, reviewSubmitPayload(t, envelopeOver("run-1", "rev-1", "crit-a", "crit-b")))
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
		}
		disk := reloadRun(t, "run-1")
		if disk.ReviewObligation != nil {
			t.Fatal("a successful review-submit must consume the [v] obligation")
		}
		if len(disk.Reviews) != 1 || disk.Reviews[0].ReviewID != "rev-1" || disk.Reviews[0].Status != shared.ReviewPrepared {
			t.Fatalf("review record = %+v, want PREPARED rev-1", disk.Reviews)
		}
		if disk.Reviews[0].SourceCheckpointID != vCP.ID {
			t.Fatal("the consumed obligation's source checkpoint must land on the review record")
		}
		if disk.Status != shared.StatusReviewPending {
			t.Fatalf("status = %s, want REVIEW_PENDING", disk.Status)
		}
		prepared := gateEventsOfType(t, "run-1", shared.EventReviewPrepared)
		if len(prepared) != 1 || prepared[0].Payload["review_id"] != "rev-1" {
			t.Fatalf("review.prepared events = %+v, want exactly one for rev-1", prepared)
		}

		// The reviewer delegation is now admissible (PREPARED id in prompt) —
		// the [v] → review-submit → Agent(reviewer) chain closes.
		code, stderr = callGate(t, toolPayload(t, "Agent", agentInput("reviewer", "run review rev-1 against the frozen envelope")))
		if code != 0 {
			t.Fatalf("reviewer delegation exit = %d, want 0 (stderr: %q)", code, stderr)
		}
		if rv := reloadRun(t, "run-1").Reviews[0]; rv.Status != shared.ReviewRunning {
			t.Fatalf("review = %s, want RUNNING after the reviewer delegation", rv.Status)
		}
	})
}

// TestReviewSubmitValidatesAgainstFrozenViewMinusDrops: the envelope's exit
// criteria must equal frozen − drops (both directions); a producer trim and
// an invented criterion are exit-2 denials that register nothing.
func TestReviewSubmitValidatesAgainstFrozenViewMinusDrops(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	// frozen {crit-a, crit-b}; the user dropped crit-b at the [v] checkpoint.
	parkUnderReviewObligation(t, r, []string{"crit-a", "crit-b"}, []string{"crit-b"}, "crit-b")

	code, stderr := callGate(t, reviewSubmitPayload(t, envelopeOver("run-1", "rev-1", "crit-b")))
	wantExit2(t, code, stderr, "crit-a")

	code, stderr = callGate(t, reviewSubmitPayload(t, envelopeOver("run-1", "rev-1", "crit-a", "crit-x")))
	wantExit2(t, code, stderr, "crit-x")

	if reloadRun(t, "run-1").ReviewObligation == nil {
		t.Fatal("a denied review-submit must leave the obligation unconsumed")
	}
	if n := len(gateEventsOfType(t, "run-1", shared.EventReviewPrepared)); n != 0 {
		t.Fatalf("review.prepared events = %d, want 0 — nothing registered", n)
	}

	// Exactly frozen − drops registers.
	code, stderr = callGate(t, reviewSubmitPayload(t, envelopeOver("run-1", "rev-1", "crit-a")))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	if len(reloadRun(t, "run-1").Reviews) != 1 {
		t.Fatal("the frozen−drops envelope must register")
	}
}

// TestReviewSubmitEnvelopeBeforeFreezeRejected: a [v] at a checkpoint whose
// phase was never accepted leaves the frozen view empty — review-submit's
// ValidateEnvelope rejects the envelope on the vacuous-view guard (envelope.go
// F5): nothing has been accepted yet, no review is due.
func TestReviewSubmitEnvelopeBeforeFreezeRejected(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	// [v] at the FIRST checkpoint — before any acceptance froze anything.
	cp := submitDisplayedPhase(t, r, "context", []string{"crit-early"})
	if err := r.RecordDecision(cp.ID, shared.DecisionReviewOnDemand, ""); err != nil {
		t.Fatal(err)
	}

	code, stderr := callGate(t, reviewSubmitPayload(t, envelopeOver("run-1", "rev-1", "crit-early")))
	wantExit2(t, code, stderr, "frozen acceptance view is empty")
	if reloadRun(t, "run-1").ReviewObligation == nil {
		t.Fatal("the vacuous rejection must consume nothing")
	}
}

// TestReviewSubmitRequiresRun: with no live run the channel is refused (the
// protocol cannot be driven run-less).
func TestReviewSubmitRequiresRun(t *testing.T) {
	useGateEnv(t)
	code, stderr := callGate(t, reviewSubmitPayload(t, envelopeOver("run-1", "rev-1", "crit-a")))
	wantExit2(t, code, stderr, "requires an active run")
}

// --- standalone-review freeze contract ----------------------------------------------

// submitPhaseWithCriteria drives phase-submit through the REAL gate payload.
func submitPhaseWithCriteria(t *testing.T, phaseID string, criteria []string) (int, string) {
	t.Helper()
	args := mustJSONStr(t, shared.PhaseSubmission{PhaseID: phaseID, Summary: phaseID + " done", CriteriaIDs: criteria})
	return callGate(t, toolPayload(t, "Skill", skillInput("summoner:phase-submit", args)))
}

// writeProjectManifest drops a summoner.yaml into the test's temp project dir.
func writeProjectManifest(t *testing.T, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(os.Getenv("CLAUDE_PROJECT_DIR"), "summoner.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestStandaloneReviewFreezeContract: on review/ship runs the pre-review
// phase (context / prepare) submission carries criteria ⊆ the effective
// verification_spec — the framework default full-rubric set plus the
// manifest's declared additions; anything else is a traced submission
// rejection. Other workflows' phases are not constrained by the review spec.
func TestStandaloneReviewFreezeContract(t *testing.T) {
	t.Run("review run context submission machine-checked", func(t *testing.T) {
		useGateEnv(t) // temp project dir: no manifest → framework default set
		startRunWF(t, "review", shared.BudgetLimits{})

		code, stderr := submitPhaseWithCriteria(t, "context", []string{"crit-made-up"})
		wantExit2(t, code, stderr, "verification_spec")
		rej := gateEventsOfType(t, "run-1", shared.EventPhaseSubmittedRejected)
		if len(rej) != 1 || rej[0].Payload["phase_id"] != "context" {
			t.Fatalf("phase_submitted.rejected events = %+v, want exactly one for context", rej)
		}

		code, stderr = submitPhaseWithCriteria(t, "context", []string{"crit-correctness", "crit-prove-it"})
		if code != 0 {
			t.Fatalf("default-rubric criteria exit = %d, want 0 (stderr: %q)", code, stderr)
		}
		if reloadRun(t, "run-1").Status != shared.StatusWaitUser {
			t.Fatal("the checked submission must have opened the checkpoint")
		}
	})

	t.Run("ship run prepare submission machine-checked", func(t *testing.T) {
		useGateEnv(t)
		startRunWF(t, "ship", shared.BudgetLimits{})
		code, stderr := submitPhaseWithCriteria(t, "prepare", []string{"crit-nope"})
		wantExit2(t, code, stderr, "verification_spec")
	})

	t.Run("manifest additions extend the default set", func(t *testing.T) {
		useGateEnv(t)
		writeProjectManifest(t, "version: \"2\"\nproject:\n  name: p\nphases:\n  review:\n    verification_spec:\n      criteria:\n        - id: crit-extra\n          goal: extra axis\n          method: judgment\n      rubric: plugin:agents/reviewer.md\n")
		startRunWF(t, "review", shared.BudgetLimits{})

		// The declared addition is allowed, and so are the default axes
		// (verification_spec criteria are additions, never replacements —
		// manifest-spec.md).
		code, stderr := submitPhaseWithCriteria(t, "context", []string{"crit-extra", "crit-security"})
		if code != 0 {
			t.Fatalf("manifest addition + default axis exit = %d, want 0 (stderr: %q)", code, stderr)
		}
	})

	t.Run("manifest present: foreign id still rejected", func(t *testing.T) {
		useGateEnv(t)
		writeProjectManifest(t, "version: \"2\"\nproject:\n  name: p\nphases:\n  review:\n    verification_spec:\n      criteria:\n        - id: crit-extra\n          goal: extra axis\n          method: judgment\n      rubric: plugin:agents/reviewer.md\n")
		startRunWF(t, "review", shared.BudgetLimits{})
		code, stderr := submitPhaseWithCriteria(t, "context", []string{"crit-bogus"})
		wantExit2(t, code, stderr, "verification_spec")
	})

	t.Run("non-review routes unconstrained by the review spec", func(t *testing.T) {
		useGateEnv(t)
		startFixRun(t, shared.BudgetLimits{})
		code, stderr := submitPhaseWithCriteria(t, "context", []string{"crit-anything-project-specific"})
		if code != 0 {
			t.Fatalf("fix run context submission exit = %d, want 0 — the review freeze is route-scoped (stderr: %q)", code, stderr)
		}
	})

	t.Run("unparseable manifest fails closed", func(t *testing.T) {
		useGateEnv(t)
		writeProjectManifest(t, "::::[\n")
		startRunWF(t, "review", shared.BudgetLimits{})
		code, stderr := submitPhaseWithCriteria(t, "context", []string{"crit-correctness"})
		wantExit2(t, code, stderr, "manifest")
	})
}

// TestStandaloneFreezePrecedesEnvelope: the standalone chain in order —
// context submission (scope-checked) → CONTINUE freezes the scope → the
// review phase's checkpoint takes the [v] → the envelope citing exactly the
// frozen set registers through the channel. (Gate-level short form; the
// binary-level round trip lives in hooks/contract.)
func TestStandaloneFreezePrecedesEnvelope(t *testing.T) {
	useGateEnv(t)
	r := startRunWF(t, "review", shared.BudgetLimits{})

	code, _ := submitPhaseWithCriteria(t, "context", []string{"crit-correctness"})
	if code != 0 {
		t.Fatal("context submission must pass the freeze check")
	}
	// The gate ran on its own loaded copy — reload before driving the run on.
	r = reloadRun(t, "run-1")
	cp := r.CurrentCheckpoint()
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(*cp, r.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	// CONTINUE accepts context — THIS freezes the scope (the [v] does not).
	if err := r.RecordDecision(cp.ID, shared.DecisionContinue, ""); err != nil {
		t.Fatalf("CONTINUE at context: %v", err)
	}

	// The review phase's submission opens the checkpoint the [v] parks under.
	code, _ = submitPhaseWithCriteria(t, "review", nil)
	if code != 0 {
		t.Fatal("review-phase submission must be admitted")
	}
	r = reloadRun(t, "run-1")
	cp = r.CurrentCheckpoint()
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(*cp, r.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordDecision(cp.ID, shared.DecisionReviewOnDemand, ""); err != nil {
		t.Fatalf("[v] at the review-phase checkpoint: %v", err)
	}

	// The envelope over the frozen criteria registers through the channel.
	code, stderr := callGate(t, reviewSubmitPayload(t, envelopeOver("run-1", "rev-1", "crit-correctness")))
	if code != 0 {
		t.Fatalf("review-submit exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if len(reloadRun(t, "run-1").Reviews) != 1 {
		t.Fatal("the standalone review must be registered")
	}
}
