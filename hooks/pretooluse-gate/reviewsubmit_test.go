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
	"strings"
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

// TestReviewSubmitEnvelopeBeforeFreezeRejected (fix-round m4): a [v] at a
// checkpoint whose phase was never accepted is REFUSED by the reducer — with
// nothing frozen, the run would otherwise park in an unrecoverable
// REVIEW_PENDING (obligation consumed-adjacent, no open checkpoint, every
// channel refused; the vacuous envelope can never cite anything). The refusal
// keeps the checkpoint open; review-submit then denies on status (the run
// never left WAIT_USER). After an acceptance freezes the view, the same [v]
// is legal.
func TestReviewSubmitEnvelopeBeforeFreezeRejected(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	// [v] at the FIRST checkpoint — before any acceptance froze anything.
	cp := submitDisplayedPhase(t, r, "context", []string{"crit-early"})
	err := r.RecordDecision(cp.ID, shared.DecisionReviewOnDemand, "")
	if err == nil || !strings.Contains(err.Error(), "frozen acceptance view is empty") {
		t.Fatalf("[v] before any freeze must be refused naming the empty view, got: %v", err)
	}
	// The refused [v] leaves the checkpoint open and the run decidable.
	if got := reloadRun(t, "run-1"); got.Status != shared.StatusWaitUser || got.ReviewObligation != nil {
		t.Fatalf("after the refused [v]: status %s obligation %+v, want WAIT_USER / nil", got.Status, got.ReviewObligation)
	}
	// review-submit denies on the WAIT_USER discipline — the run never left
	// WAIT_USER, so the checkpoint still comes first.
	code, stderr := callGate(t, reviewSubmitPayload(t, envelopeOver("run-1", "rev-1", "crit-early")))
	wantExit2(t, code, stderr, "WAIT_USER")
	if reloadRun(t, "run-1").ReviewObligation != nil {
		t.Fatal("nothing may record an obligation before the freeze")
	}

	// CONTINUE freezes the view; the same [v] at the next checkpoint is legal
	// and the channel opens for the envelope.
	if err := r.RecordDecision(cp.ID, shared.DecisionContinue, ""); err != nil {
		t.Fatalf("CONTINUE after the refused [v]: %v", err)
	}
	cp2 := submitDisplayedPhase(t, r, "diagnosis", []string{"crit-late"})
	if err := r.RecordDecision(cp2.ID, shared.DecisionReviewOnDemand, ""); err != nil {
		t.Fatalf("[v] after a freeze must be legal: %v", err)
	}
	code, stderr = callGate(t, reviewSubmitPayload(t, envelopeOver("run-1", "rev-1", "crit-early")))
	if code != 0 {
		t.Fatalf("review-submit after the legal [v] exit = %d, want 0 (stderr: %q)", code, stderr)
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

// --- fix round 1 ---------------------------------------------------------------------

// TestAgentNamespacedAdmission (fix-round C1): production plugin agents invoke
// as <plugin>:<name> — Summoner's framework types are `summoner:reviewer` and
// `summoner:worker`. The gate admits BOTH spellings, normalizing by stripping
// exactly the framework's own `summoner:` prefix (never a wildcard), denies
// every other namespace, and traces delegation.started with the CANONICAL
// short form.
func TestAgentNamespacedAdmission(t *testing.T) {
	t.Run("summoner:reviewer admitted; canonical short form in effects", func(t *testing.T) {
		useGateEnv(t)
		r := startFixRun(t, shared.BudgetLimits{})
		parkUnderReviewObligation(t, r, []string{"crit-a"}, nil)
		if _, err := r.RequestReview(envelopeOver("run-1", "rev-1", "crit-a")); err != nil {
			t.Fatal(err)
		}

		code, stderr := callGate(t, toolPayload(t, "Agent", agentInput("summoner:reviewer", "run review rev-1 against the frozen envelope")))
		if code != 0 {
			t.Fatalf("summoner:reviewer delegation exit = %d, want 0 (stderr: %q)", code, stderr)
		}
		if rv := reloadRun(t, "run-1").Reviews[0]; rv.Status != shared.ReviewRunning {
			t.Fatalf("review = %s, want RUNNING — the namespaced reviewer must dispatch", rv.Status)
		}
		started := gateEventsOfType(t, "run-1", shared.EventDelegationStarted)
		if len(started) != 1 || started[0].Payload["subagent_type"] != "reviewer" {
			t.Fatalf("delegation.started = %+v, want one carrying the canonical short form", started)
		}
	})

	t.Run("summoner:worker admitted in ACTIVE", func(t *testing.T) {
		useGateEnv(t)
		startFixRun(t, shared.BudgetLimits{})
		code, stderr := callGate(t, toolPayload(t, "Agent", agentInput("summoner:worker", "run the test suite")))
		if code != 0 {
			t.Fatalf("summoner:worker delegation exit = %d, want 0 (stderr: %q)", code, stderr)
		}
		started := gateEventsOfType(t, "run-1", shared.EventDelegationStarted)
		if len(started) != 1 || started[0].Payload["subagent_type"] != "worker" {
			t.Fatalf("delegation.started = %+v, want canonical worker", started)
		}
	})

	t.Run("every other namespace denied", func(t *testing.T) {
		useGateEnv(t)
		r := startFixRun(t, shared.BudgetLimits{})
		parkUnderReviewObligation(t, r, []string{"crit-a"}, nil)
		if _, err := r.RequestReview(envelopeOver("run-1", "rev-1", "crit-a")); err != nil {
			t.Fatal(err)
		}
		for _, typ := range []string{"summoner:fork", "other:reviewer", "summoner:code-reviewer", "reviewer:summoner", ":reviewer", "summoner:"} {
			code, stderr := callGate(t, toolPayload(t, "Agent", agentInput(typ, "run review rev-1")))
			wantExit2(t, code, stderr, "not allowed")
			if rv := reloadRun(t, "run-1").Reviews[0]; rv.Status != shared.ReviewPrepared {
				t.Fatalf("denied %q left review = %s, want PREPARED (nothing dispatched)", typ, rv.Status)
			}
		}
	})
}

// TestStandaloneRouteScopeCheckAllPhases (fix-round m3): the machine scope
// check binds EVERY phase submission on the standalone review/ship routes —
// not just the entry phase. Otherwise a ship route could launder scope-foreign
// criteria through the review phase's acceptance and force them into every
// later envelope.
func TestStandaloneRouteScopeCheckAllPhases(t *testing.T) {
	useGateEnv(t)
	startRunWF(t, "ship", shared.BudgetLimits{})

	// prepare passes the check (default axes) and its checkpoint decides
	// CONTINUE, settling the phase.
	code, _ := submitPhaseWithCriteria(t, "prepare", []string{"crit-correctness"})
	if code != 0 {
		t.Fatal("prepare submission must pass the scope check")
	}
	r := reloadRun(t, "run-1")
	cp := r.CurrentCheckpoint()
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(*cp, r.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordDecision(cp.ID, shared.DecisionContinue, ""); err != nil {
		t.Fatalf("CONTINUE at prepare: %v", err)
	}

	// The REVIEW phase's submission is scope-checked too: a scope-foreign
	// criterion is a traced rejection even though this is not the entry phase.
	code, stderr := submitPhaseWithCriteria(t, "review", []string{"crit-nope"})
	wantExit2(t, code, stderr, "verification_spec")
	rej := gateEventsOfType(t, "run-1", shared.EventPhaseSubmittedRejected)
	if len(rej) == 0 || rej[len(rej)-1].Payload["phase_id"] != "review" {
		t.Fatalf("phase_submitted.rejected = %+v, want the review-phase rejection traced", rej)
	}
	// A rubric-verifiable criterion passes the same checkpoint.
	code, stderr = submitPhaseWithCriteria(t, "review", []string{"crit-prove-it"})
	if code != 0 {
		t.Fatalf("review-phase submission with rubric criteria exit = %d, want 0 (stderr: %q)", code, stderr)
	}
}

// TestTerminalDefaultReviewWithoutOptIn (spec §1 终局⑤默认强制 — the terminal
// independent review is the DEFAULT, explicitly skippable): a plain CONTINUE
// at the fixed review phase's checkpoint — no [v] anywhere — opens the
// envelope channel from ACTIVE through the reducer's existing default path.
// An explicit SKIP closes the default (UNREVIEWED); every other ACTIVE
// position stays refused.
func TestTerminalDefaultReviewWithoutOptIn(t *testing.T) {
	t.Run("review-phase CONTINUE opens the channel from ACTIVE", func(t *testing.T) {
		useGateEnv(t)
		r := startRunWF(t, "review", shared.BudgetLimits{})
		acceptPhaseWithCriteria(t, r, "context", []string{"crit-correctness"})
		acceptPhaseWithCriteria(t, r, "review", nil)
		if r.Status != shared.StatusActive || r.ReviewObligation != nil {
			t.Fatalf("after the default accepts: %s / %+v — the default path records no obligation", r.Status, r.ReviewObligation)
		}

		code, stderr := callGate(t, reviewSubmitPayload(t, envelopeOver("run-1", "rev-1", "crit-correctness")))
		if code != 0 {
			t.Fatalf("terminal default review-submit exit = %d, want 0 — no [v] opt-in required (stderr: %q)", code, stderr)
		}
		disk := reloadRun(t, "run-1")
		if disk.Status != shared.StatusReviewPending || disk.ReviewObligation != nil ||
			len(disk.Reviews) != 1 || disk.Reviews[0].Status != shared.ReviewPrepared {
			t.Fatalf("after the terminal registration: %s / obligation %+v / reviews %+v", disk.Status, disk.ReviewObligation, disk.Reviews)
		}

		// The reviewer dispatch follows as usual.
		code, stderr = callGate(t, toolPayload(t, "Agent", agentInput("summoner:reviewer", "run review rev-1 against the frozen envelope")))
		if code != 0 {
			t.Fatalf("reviewer delegation exit = %d, want 0 (stderr: %q)", code, stderr)
		}
		if rv := reloadRun(t, "run-1").Reviews[0]; rv.Status != shared.ReviewRunning {
			t.Fatalf("review = %s, want RUNNING after the default-path dispatch", rv.Status)
		}
	})

	t.Run("explicit SKIP closes the default (UNREVIEWED)", func(t *testing.T) {
		useGateEnv(t)
		r := startRunWF(t, "review", shared.BudgetLimits{})
		acceptPhaseWithCriteria(t, r, "context", []string{"crit-correctness"})
		cp := submitDisplayedPhase(t, r, "review", nil)
		if err := r.RecordDecision(cp.ID, shared.DecisionSkip, ""); err != nil {
			t.Fatalf("SKIP at the review-phase checkpoint: %v", err)
		}
		code, stderr := callGate(t, reviewSubmitPayload(t, envelopeOver("run-1", "rev-1", "crit-correctness")))
		wantExit2(t, code, stderr, "explicitly skipped")
		if reloadRun(t, "run-1").Status != shared.StatusActive || len(reloadRun(t, "run-1").Reviews) != 0 {
			t.Fatal("the skipped default must register nothing")
		}
	})

	t.Run("arbitrary ACTIVE positions stay refused", func(t *testing.T) {
		useGateEnv(t)
		r := startRunWF(t, "review", shared.BudgetLimits{})
		acceptPhaseWithCriteria(t, r, "context", []string{"crit-correctness"})
		// The review phase has not run yet: ACTIVE before the review phase.
		code, stderr := callGate(t, reviewSubmitPayload(t, envelopeOver("run-1", "rev-1", "crit-correctness")))
		wantExit2(t, code, stderr, "terminal default position")

		// A dev route mid-workflow (diagnosis accepted, terminal review phase
		// far away) is equally refused.
		useGateEnv(t)
		r2 := startFixRun(t, shared.BudgetLimits{})
		acceptPhaseWithCriteria(t, r2, "diagnosis", []string{"crit-root-cause"})
		code, stderr = callGate(t, reviewSubmitPayload(t, envelopeOver("run-1", "rev-1", "crit-root-cause")))
		wantExit2(t, code, stderr, "terminal default position")
	})
}
