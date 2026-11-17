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
	cp1, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{
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
	cp, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{
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

	// The arithmetic follows: the review envelope must omit crit-a (frozen −
	// drops). Assert the exact ids the validator enforces (C1 §1: the mint is
	// the authority — hand-built envelopes are not a fixture lever anymore).
	expected := shared.ExpectedCriteriaIDs(shared.ResolveFrozenView(disk.AcceptanceView()), disk.CriteriaDrops)
	if expected["crit-a"] {
		t.Fatal("frozen−drops must omit the ride-along-dropped crit-a")
	}
	if !expected["crit-b"] {
		t.Fatal("frozen−drops must keep crit-b")
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
		name       string
		q          map[string]interface{}
		wantReason string
	}{
		{
			name:       "no keep option",
			wantReason: "drop",
			q: map[string]interface{}{
				"question": "drop: crit-a — remove it?",
				"options":  []interface{}{dropOption("drop", "crit-a")},
			},
		},
		{
			name:       "keep names a different id",
			wantReason: "drop",
			q: map[string]interface{}{
				"question": "drop: crit-a — remove it?",
				"options":  []interface{}{dropOption("keep", "crit-b"), dropOption("drop", "crit-a")},
			},
		},
		{
			// Fix-round m2: a nonce-carrying drop question ALSO makes two
			// nonce questions — the multi-nonce fail-closed fires first
			// (nothing recorded either way; the earlier, stronger ambiguity
			// rejection wins).
			name:       "drop question carries the nonce",
			wantReason: "exactly ONE",
			q: func() map[string]interface{} {
				q, _ := dropQuestionFixture("crit-a")
				q["question"] = "drop: crit-a (nonce %s) — remove it?" // placeholder; fixed per-case below
				return q
			}(),
		},
		{
			name:       "id outside criteria ∪ frozen view",
			wantReason: "drop",
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
			wantRejected(t, evs, cp.ID, c.wantReason)
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

// --- fix round 1 ---------------------------------------------------------------------

// TestDropReplayAfterReacceptanceRecordsNothing (fix-round I1): a re-delivered
// PostToolUse callback must not re-drop a criterion the user just re-accepted.
// tu-replay-1 answered {CONTINUE + drop: crit-a} at cp-2 → a later context
// CONTINUE re-accepts crit-a (clearing the ledger entry) → cp-4 displayed →
// the SAME callback re-delivered: its tool_use_id was consumed by cp-2's
// decision, so it can never record again — no second criterion.dropped and the
// envelope set is NOT weakened. A FRESH tool_use_id (a fresh human answer)
// may drop again.
func TestDropReplayAfterReacceptanceRecordsNothing(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp2 := droppableCheckpointWithCriteria(t, r, []string{"crit-a"}, []string{"crit-b"})

	dq, dtext := dropQuestionFixture("crit-a")
	q, qtext := cpQuestion(cp2.Nonce, false, decisionLabels...)
	replayPayload := postUsePayload(t, "AskUserQuestion", "tu-replay-1",
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
	code, stderr := callHook(t, replayPayload)
	if code != 0 {
		t.Fatalf("original callback exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	disk := reloadRun(t, "run-1")
	if len(disk.CriteriaDrops) != 1 || disk.CriteriaDrops[0].ToolUseID != "tu-replay-1" {
		t.Fatalf("ledger after the original callback = %+v, want one crit-a entry on tu-replay-1", disk.CriteriaDrops)
	}
	// The hook decided cp-2 on its own loaded copy — reload before driving on.
	r = disk

	// A later context visit re-accepts crit-a → the ledger clears; cp-4
	// (diagnosis, crit-c) is the checkpoint the replay lands at.
	if err := r.OpenPhase("context"); err != nil {
		t.Fatal(err)
	}
	cp3, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "context", Summary: "context redone", CriteriaIDs: []string{"crit-a"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.MarkDisplayed(cp3.ID, shared.CheckpointContentDigest(cp3, r.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordDecision(cp3.ID, shared.DecisionContinue, "tu-re-acc-1"); err != nil {
		t.Fatal(err)
	}
	if err := r.OpenPhase("diagnosis"); err != nil {
		t.Fatal(err)
	}
	cp4, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "diagnosis", Summary: "diagnosis again", CriteriaIDs: []string{"crit-c"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.MarkDisplayed(cp4.ID, shared.CheckpointContentDigest(cp4, r.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	disk = reloadRun(t, "run-1")
	if len(disk.CriteriaDrops) != 0 {
		t.Fatalf("re-acceptance must clear the drop, ledger = %+v", disk.CriteriaDrops)
	}

	// The re-delivered callback: same payload, same tool_use_id, at cp-4.
	code, stderr = callHook(t, replayPayload)
	if code != 0 {
		t.Fatalf("replayed callback exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	disk = reloadRun(t, "run-1")
	if len(disk.CriteriaDrops) != 0 {
		t.Fatalf("the replay must record no drop — an already-consumed tool_use_id can never record again, ledger = %+v", disk.CriteriaDrops)
	}
	evs := readHookTrace(t, "run-1")
	if n := len(eventsOfType(evs, shared.EventCriterionDropped)); n != 1 {
		t.Fatalf("criterion.dropped events = %d, want still 1 (no second drop from the replay; trace: %v)", n, traceTypesOf(evs))
	}
	// The replayed callback already concluded cp-2's decision, so the whole
	// call is a silent idempotent no-op — no decision_rejected either (the
	// coordinator's I1 ruling: reject/no-op an already-concluded decision
	// callback BEFORE any ride-along effect).
	if n := len(eventsOfType(evs, shared.EventCheckpointDecisionRejected)); n != 0 {
		t.Fatalf("decision_rejected events = %d, want 0 — the replay is a silent no-op (trace: %v)", n, traceTypesOf(evs))
	}
	// The envelope arithmetic is NOT weakened: the frozen view still carries
	// crit-a (re-frozen) and crit-b — no drop is in force.
	// The envelope arithmetic is NOT weakened: the frozen view still carries
	// crit-a (re-frozen) and crit-b — no drop is in force. Assert the exact
	// ids the validator enforces (C1 §1: the mint is the authority).
	expected := shared.ExpectedCriteriaIDs(shared.ResolveFrozenView(disk.AcceptanceView()), disk.CriteriaDrops)
	if !expected["crit-a"] || !expected["crit-b"] {
		t.Fatalf("replay must leave both criteria in force: %v", expected)
	}

	// A FRESH tool_use_id with a fresh human answer may drop crit-a again.
	q4, qtext4 := cpQuestion(cp4.Nonce, false, decisionLabels...)
	code, stderr = callHook(t, postUsePayload(t, "AskUserQuestion", "tu-fresh-1",
		map[string]interface{}{"questions": []interface{}{q4, dq}},
		map[string]interface{}{
			"answers": map[string]interface{}{
				qtext4: "Continue: accept and proceed",
				dtext:  "drop: crit-a",
			},
		}))
	if code != 0 {
		t.Fatalf("fresh drop callback exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	disk = reloadRun(t, "run-1")
	if len(disk.CriteriaDrops) != 1 || disk.CriteriaDrops[0].ToolUseID != "tu-fresh-1" {
		t.Fatalf("ledger after the fresh drop = %+v, want one crit-a entry on tu-fresh-1", disk.CriteriaDrops)
	}
}

// TestMultiNonceCallbackRecordsNothingIncludingDrops (fix-round m2): a call
// carrying two nonce questions is ambiguous and rejects the WHOLE callback
// with NOTHING recorded — including drops. The answers map cannot be bound to
// questions, so no part of it may reach the ledger (T16 discipline; the drop
// channel is the decision channel's ride-along, never a side door).
func TestMultiNonceCallbackRecordsNothingIncludingDrops(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := droppableCheckpointWithCriteria(t, r, []string{"crit-a"}, []string{"crit-b"})

	q1, t1 := cpQuestion(cp.Nonce, false, decisionLabels...)
	q2, t2 := cpQuestion(cp.Nonce, false, decisionLabels[:3]...)
	dq, dtext := dropQuestionFixture("crit-a")
	payload := postUsePayload(t, "AskUserQuestion", "tu-mn-1",
		map[string]interface{}{"questions": []interface{}{q1, q2, dq}},
		map[string]interface{}{
			"questions": []interface{}{
				map[string]interface{}{"question": t1},
				map[string]interface{}{"question": t2},
				map[string]interface{}{"question": dtext},
			},
			"answers": map[string]interface{}{
				t1:    "Continue: accept and proceed",
				dtext: "drop: crit-a",
			},
		})
	code, stderr := callHook(t, payload)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 — a rejection is not a hook error (stderr: %q)", code, stderr)
	}

	disk := reloadRun(t, "run-1")
	if len(disk.CriteriaDrops) != 0 {
		t.Fatalf("multi-nonce callback must record NO drop (nothing recorded), ledger = %+v", disk.CriteriaDrops)
	}
	if got := disk.Checkpoints[1]; got.Decided {
		t.Fatal("multi-nonce callback must record no decision")
	}
	evs := readHookTrace(t, "run-1")
	wantRejected(t, evs, cp.ID, "exactly ONE")
	if n := len(eventsOfType(evs, shared.EventCriterionDropped)); n != 0 {
		t.Fatalf("criterion.dropped events = %d, want 0 (trace: %v)", n, traceTypesOf(evs))
	}
	if n := len(eventsOfType(evs, shared.EventCheckpointDecided)); n != 0 {
		t.Fatalf("checkpoint.decided events = %d, want 0", n)
	}
}
