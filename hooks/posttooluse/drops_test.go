package main

// drops_test.go — T17 suite, PostToolUse half: the nonce-free drop:/keep:
// ride-along channel. A decision call may carry drop questions (ONE criterion
// id per question, exactly a drop: and a keep: option each naming that id,
// never carrying the nonce); answered drops record through
// RecordCriteriaDrop + criterion.dropped BEFORE the decision (an accepting
// decision re-accepts its criteria and clears same-id drops); keep is the
// default and records nothing; a decision left unanswered while a drop is
// answered records the drop observation and NOT the decision (partial-answer
// semantics); malformed drop questions fail the whole callback.

import (
	"fmt"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// dropQuestion builds one drop ride-along question object and its text (the
// answers map key): `drop: <id>` / `keep: <id>`, one criterion id, nonce-free.
// dropQuestionFixture builds one drop ride-along question object and its text
// (the answers map key): `drop: <id>` / `keep: <id>`, one criterion id,
// nonce-free.
func dropQuestionFixture(id string) (map[string]interface{}, string) {
	text := fmt.Sprintf("drop: %s — remove this criterion from the acceptance view?", id)
	return map[string]interface{}{
		"question":    text,
		"header":      "Criterion " + id,
		"options":     []interface{}{dropOption("keep", id), dropOption("drop", id)},
		"multiSelect": false,
	}, text
}

func dropOption(verb, id string) map[string]interface{} {
	return map[string]interface{}{"label": verb + ": " + id, "description": "criterion " + id}
}

// droppableCheckpointWithCriteria drives one accepted phase (its criteria
// frozen into the acceptance view) plus a second submitted+displayed
// checkpoint — the state a drop ride-along answers in. frozen ids are
// droppable at the later checkpoint, pending at their own.
func droppableCheckpointWithCriteria(t *testing.T, r *shared.RunState, frozen, pending []string) shared.CheckpointState {
	t.Helper()
	if err := r.OpenPhase("context"); err != nil {
		t.Fatalf("OpenPhase(context): %v", err)
	}
	cp1, err := r.SubmitPhase(shared.PhaseSubmission{
		PhaseID:     "context",
		Summary:     "context done",
		CriteriaIDs: frozen,
	})
	if err != nil {
		t.Fatalf("SubmitPhase(context): %v", err)
	}
	if err := r.MarkDisplayed(cp1.ID, shared.CheckpointContentDigest(cp1, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed(%s): %v", cp1.ID, err)
	}
	if err := r.RecordDecision(cp1.ID, shared.DecisionContinue, ""); err != nil {
		t.Fatalf("RecordDecision(context): %v", err)
	}
	if err := r.OpenPhase("diagnosis"); err != nil {
		t.Fatalf("OpenPhase(diagnosis): %v", err)
	}
	cp, err := r.SubmitPhase(shared.PhaseSubmission{
		PhaseID:     "diagnosis",
		Summary:     "diagnosis done",
		CriteriaIDs: pending,
	})
	if err != nil {
		t.Fatalf("SubmitPhase(diagnosis): %v", err)
	}
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed(%s): %v", cp.ID, err)
	}
	return cp
}

// TestDropRideAlongRecordedWithDecision: the same callback answers the nonce
// decision question AND a drop question — the drop records (ledger entry +
// criterion.dropped) and the decision records; a CONTINUE that does NOT
// re-accept the dropped id leaves the drop in force.
func TestDropRideAlongRecordedWithDecision(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	// frozen {crit-a}; pending {crit-b} — drop the FROZEN id, then CONTINUE
	// accepts only crit-b: the crit-a drop survives its acceptance.
	cp := droppableCheckpointWithCriteria(t, r, []string{"crit-a"}, []string{"crit-b"})

	dq, dtext := dropQuestionFixture("crit-a")
	q, qtext := cpQuestion(cp.Nonce, false, decisionLabels...)
	payload := postUsePayload(t, "AskUserQuestion", "tu-both-1",
		map[string]interface{}{"questions": []interface{}{q, dq}},
		map[string]interface{}{
			"questions": []interface{}{
				map[string]interface{}{"question": qtext},
				map[string]interface{}{"question": dtext},
			},
			"answers": map[string]interface{}{
				qtext: "Continue: accept and proceed",
				dtext: "drop: crit-a",
			},
		})
	code, stderr := callHook(t, payload)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}

	disk := reloadRun(t, "run-1")
	if got := disk.Checkpoints[1]; !got.Decided || got.Decision != shared.DecisionContinue {
		t.Fatalf("decision = %+v, want decided CONTINUE alongside the drop", got)
	}
	if len(disk.CriteriaDrops) != 1 || disk.CriteriaDrops[0].ID != "crit-a" ||
		disk.CriteriaDrops[0].CheckpointID != cp.ID || disk.CriteriaDrops[0].ToolUseID != "tu-both-1" {
		t.Fatalf("drop ledger = %+v, want one crit-a entry bound to %s / tu-both-1", disk.CriteriaDrops, cp.ID)
	}
	evs := readHookTrace(t, "run-1")
	dropped := eventsOfType(evs, shared.EventCriterionDropped)
	if len(dropped) != 1 {
		t.Fatalf("criterion.dropped events = %d, want 1 (trace: %v)", len(dropped), traceTypesOf(evs))
	}
	if dropped[0].Payload["criterion_id"] != "crit-a" || dropped[0].Payload["checkpoint_id"] != cp.ID ||
		dropped[0].ToolUseID != "tu-both-1" {
		t.Fatalf("criterion.dropped payload = %v (tool_use_id %q), want crit-a @ %s bound to tu-both-1",
			dropped[0].Payload, dropped[0].ToolUseID, cp.ID)
	}
	if n := len(eventsOfType(evs, shared.EventCheckpointDecided)); n != 1 {
		t.Fatalf("checkpoint.decided events = %d, want 1", n)
	}

	// The arithmetic follows: an envelope must omit crit-a (frozen − drops).
	env := shared.Envelope{
		SchemaVersion: "2", RunID: "run-1", ReviewID: "rev-drops",
		ArtifactSet:  []shared.ArtifactRef{{Path: "x.md", Digest: "d"}},
		ExitCriteria: []shared.Criterion{{ID: "crit-b", Method: shared.MethodJudgment}},
	}
	if err := shared.ValidateEnvelope(env, disk.AcceptanceView(), disk.CriteriaDrops, nil); err != nil {
		t.Fatalf("envelope over frozen−drops must validate after the ride-along drop: %v", err)
	}
}

// TestDropClearedByReacceptanceInSameCallback: dropping a PENDING criterion
// and then CONTINUE (which re-accepts it) clears the drop — acceptance
// governs (decidePhase → clearDroppedCriteria), while the criterion.dropped
// observation stays in the trace.
func TestDropClearedByReacceptanceInSameCallback(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := droppableCheckpointWithCriteria(t, r, []string{"crit-a"}, []string{"crit-b"})

	dq, dtext := dropQuestionFixture("crit-b")
	q, qtext := cpQuestion(cp.Nonce, false, decisionLabels...)
	code, stderr := callHook(t, postUsePayload(t, "AskUserQuestion", "tu-clear-1",
		map[string]interface{}{"questions": []interface{}{q, dq}},
		map[string]interface{}{
			"answers": map[string]interface{}{
				qtext: "Continue: accept and proceed",
				dtext: "drop: crit-b",
			},
		}))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}

	disk := reloadRun(t, "run-1")
	if len(disk.CriteriaDrops) != 0 {
		t.Fatalf("drop ledger = %+v, want empty — the CONTINUE re-accepted crit-b and cleared its drop", disk.CriteriaDrops)
	}
	if n := len(eventsOfType(readHookTrace(t, "run-1"), shared.EventCriterionDropped)); n != 1 {
		t.Fatalf("criterion.dropped events = %d, want 1 — the observation is recorded even when cleared", n)
	}
}

// TestDropKeepAnswerRecordsNothing: keep is the default — a keep answer
// records no ledger entry and no event; the decision records normally.
func TestDropKeepAnswerRecordsNothing(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := droppableCheckpointWithCriteria(t, r, []string{"crit-a"}, []string{"crit-b"})

	dq, dtext := dropQuestionFixture("crit-a")
	q, qtext := cpQuestion(cp.Nonce, false, decisionLabels...)
	code, stderr := callHook(t, postUsePayload(t, "AskUserQuestion", "tu-keep-1",
		map[string]interface{}{"questions": []interface{}{q, dq}},
		map[string]interface{}{
			"answers": map[string]interface{}{
				qtext: "Continue: accept and proceed",
				dtext: "keep: crit-a",
			},
		}))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	disk := reloadRun(t, "run-1")
	if len(disk.CriteriaDrops) != 0 {
		t.Fatalf("keep must record nothing, ledger = %+v", disk.CriteriaDrops)
	}
	if n := len(eventsOfType(readHookTrace(t, "run-1"), shared.EventCriterionDropped)); n != 0 {
		t.Fatalf("criterion.dropped events = %d, want 0 for keep", n)
	}
	if got := disk.Checkpoints[1]; !got.Decided {
		t.Fatal("the decision must record normally alongside an unanswered-in-effect keep")
	}
}

// TestDropPartialAnswerSemantics: the decision question unanswered while a
// drop question is answered — the drop observation records, the decision does
// NOT (decision_rejected), and the checkpoint stays open.
func TestDropPartialAnswerSemantics(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := droppableCheckpointWithCriteria(t, r, []string{"crit-a"}, []string{"crit-b"})

	dq, dtext := dropQuestionFixture("crit-a")
	q, _ := cpQuestion(cp.Nonce, false, decisionLabels...)
	payload := postUsePayload(t, "AskUserQuestion", "tu-part-1",
		map[string]interface{}{"questions": []interface{}{q, dq}},
		map[string]interface{}{
			"answers": map[string]interface{}{
				dtext: "drop: crit-a", // no answer for the decision question
			},
		})
	code, stderr := callHook(t, payload)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}

	disk := reloadRun(t, "run-1")
	if got := disk.Checkpoints[1]; got.Decided {
		t.Fatalf("an unanswered decision must not record, got %+v", got.Decision)
	}
	if len(disk.CriteriaDrops) != 1 || disk.CriteriaDrops[0].ID != "crit-a" {
		t.Fatalf("drop ledger = %+v, want the answered drop recorded", disk.CriteriaDrops)
	}
	evs := readHookTrace(t, "run-1")
	wantRejected(t, evs, cp.ID, "no answer for the decision question")
	if n := len(eventsOfType(evs, shared.EventCriterionDropped)); n != 1 {
		t.Fatalf("criterion.dropped events = %d, want 1 (trace: %v)", n, traceTypesOf(evs))
	}

	// A replay of the same callback re-records nothing (per-id idempotent)
	// and still refuses the decision.
	code, stderr = callHook(t, payload)
	if code != 0 {
		t.Fatalf("replay exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	evs = readHookTrace(t, "run-1")
	if n := len(eventsOfType(evs, shared.EventCriterionDropped)); n != 1 {
		t.Fatalf("criterion.dropped events after replay = %d, want still 1 (per-id idempotent)", n)
	}
	if n := len(eventsOfType(evs, shared.EventCheckpointDecisionRejected)); n != 2 {
		t.Fatalf("decision_rejected events after replay = %d, want 2 (one per attempt)", n)
	}
}

// TestDropQuestionMalformedRejected: keep-default gate enforcement — a drop
// question without a keep option (or with a nonce, a foreign criterion id per
// label pair, or an id outside checkpoint criteria ∪ frozen view) fails the
// WHOLE callback: decision_rejected, nothing recorded.
func TestDropQuestionMalformedRejected(t *testing.T) {
	cases := []struct {
		name string
		q    map[string]interface{}
	}{
		{
			name: "no keep option",
			q: map[string]interface{}{
				"question": "drop: crit-a — remove it?",
				"options":  []interface{}{dropOption("drop", "crit-a")},
			},
		},
		{
			name: "keep names a different id",
			q: map[string]interface{}{
				"question": "drop: crit-a — remove it?",
				"options":  []interface{}{dropOption("keep", "crit-b"), dropOption("drop", "crit-a")},
			},
		},
		{
			name: "drop question carries the nonce",
			q: func() map[string]interface{} {
				q, _ := dropQuestionFixture("crit-a")
				q["question"] = "drop: crit-a (nonce %s) — remove it?" // placeholder; fixed per-case below
				return q
			}(),
		},
		{
			name: "id outside criteria ∪ frozen view",
			q: func() map[string]interface{} {
				q, _ := dropQuestionFixture("crit-ghost")
				return q
			}(),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			useHookEnv(t)
			r := startRun(t)
			cp := droppableCheckpointWithCriteria(t, r, []string{"crit-a"}, []string{"crit-b"})
			if c.name == "drop question carries the nonce" {
				q, _ := dropQuestionFixture("crit-a")
				q["question"] = fmt.Sprintf("drop: crit-a (nonce %s) — remove it?", cp.Nonce)
				c.q = q
			}
			dq, _ := c.q["question"].(string)
			q, qtext := cpQuestion(cp.Nonce, false, decisionLabels...)
			code, stderr := callHook(t, postUsePayload(t, "AskUserQuestion", "tu-bad-1",
				map[string]interface{}{"questions": []interface{}{q, c.q}},
				map[string]interface{}{
					"answers": map[string]interface{}{
						qtext: "Continue: accept and proceed",
						dq:    "drop: crit-a",
					},
				}))
			if code != 0 {
				t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
			}
			disk := reloadRun(t, "run-1")
			if got := disk.Checkpoints[1]; got.Decided {
				t.Fatal("a malformed drop question must fail the whole callback — no decision")
			}
			if len(disk.CriteriaDrops) != 0 {
				t.Fatalf("a malformed drop question must record no drop, ledger = %+v", disk.CriteriaDrops)
			}
			evs := readHookTrace(t, "run-1")
			wantRejected(t, evs, cp.ID, "drop")
			if n := len(eventsOfType(evs, shared.EventCriterionDropped)); n != 0 {
				t.Fatalf("criterion.dropped events = %d, want 0", n)
			}
		})
	}
}

// TestDropAnswerNotAnOfferedLabel: an answer on a drop question that matches
// neither its drop nor keep label fails the whole callback (the same
// offered-option rule the decision answer obeys).
func TestDropAnswerNotAnOfferedLabel(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := droppableCheckpointWithCriteria(t, r, []string{"crit-a"}, []string{"crit-b"})

	dq, dtext := dropQuestionFixture("crit-a")
	q, qtext := cpQuestion(cp.Nonce, false, decisionLabels...)
	code, stderr := callHook(t, postUsePayload(t, "AskUserQuestion", "tu-badlabel-1",
		map[string]interface{}{"questions": []interface{}{q, dq}},
		map[string]interface{}{
			"answers": map[string]interface{}{
				qtext: "Continue: accept and proceed",
				dtext: "drop it please",
			},
		}))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	disk := reloadRun(t, "run-1")
	if got := disk.Checkpoints[1]; got.Decided {
		t.Fatal("an invalid drop answer must fail the whole callback")
	}
	if len(disk.CriteriaDrops) != 0 {
		t.Fatalf("ledger = %+v, want empty", disk.CriteriaDrops)
	}
	wantRejected(t, readHookTrace(t, "run-1"), cp.ID, "drop question")
}

// TestDropOnlyCallbackRecordsDropsOnly: a call with drop questions but NO
// nonce decision question cannot pass the gate — hand-crafted, it still
// records the answered drop observations and refuses the decision.
func TestDropOnlyCallbackRecordsDropsOnly(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := droppableCheckpointWithCriteria(t, r, []string{"crit-a"}, []string{"crit-b"})

	dq, dtext := dropQuestionFixture("crit-a")
	code, stderr := callHook(t, postUsePayload(t, "AskUserQuestion", "tu-droponly-1",
		map[string]interface{}{"questions": []interface{}{dq}},
		map[string]interface{}{
			"answers": map[string]interface{}{dtext: "drop: crit-a"},
		}))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	disk := reloadRun(t, "run-1")
	if got := disk.Checkpoints[1]; got.Decided {
		t.Fatal("no nonce question — no decision")
	}
	if len(disk.CriteriaDrops) != 1 || disk.CriteriaDrops[0].ID != "crit-a" {
		t.Fatalf("ledger = %+v, want the answered drop", disk.CriteriaDrops)
	}
	wantRejected(t, readHookTrace(t, "run-1"), cp.ID, "nonce")
}
