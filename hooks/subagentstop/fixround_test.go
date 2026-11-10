package main

// fixround_test.go — T17 fix-round suite, SubagentStop half:
//   - C1: plugin agents report namespaced agent_type — `summoner:reviewer` /
//     `summoner:worker` are Summoner delegations (same normalization as the
//     gate), every other namespace is ignored;
//   - m1: a late duplicate of an already-FAILED review must not trace a second
//     review.failed (the failure already happened and was traced).

import (
	"encoding/json"
	"strings"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// TestSubagentStopNamespacedAgentTypes (fix-round C1): the platform reports
// plugin agents as <plugin>:<name>. A namespaced Summoner reviewer/worker stop
// is processed like the bare form (registration + backfill), while every other
// namespace — including look-alikes — is ignored entirely.
func TestSubagentStopNamespacedAgentTypes(t *testing.T) {
	t.Run("summoner:reviewer backfills the verdict", func(t *testing.T) {
		useSubagentEnv(t)
		r := startRun(t)
		startRunningReview(t, r, "rev-1")

		resJSON, err := json.Marshal(passingResult("rev-1"))
		if err != nil {
			t.Fatal(err)
		}
		path := writeTranscript(t, string(resJSON))

		code, stderr := callSubagentStop(t, subStopPayload(t, "summoner:reviewer", path))
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
		}
		disk := reloadRun(t, "run-1")
		if rv := reviewByID(t, disk, "rev-1"); rv.Status != shared.ReviewReviewed || rv.Verdict != shared.VerdictPass {
			t.Fatalf("namespaced reviewer stop must backfill, review = %+v", rv)
		}
		if disk.Status != shared.StatusWaitUser {
			t.Fatalf("status = %s, want WAIT_USER at the review-result checkpoint", disk.Status)
		}
		evs := readTrace(t, "run-1")
		done := eventsOfType(evs, shared.EventDelegationCompleted)
		if len(done) != 1 || done[0].Payload["agent_type"] != "summoner:reviewer" {
			t.Fatalf("delegation.completed = %v, want the registration carrying the raw agent_type", done)
		}
		if n := len(eventsOfType(evs, shared.EventReviewVerdict)); n != 1 {
			t.Fatalf("review.verdict events = %d, want 1 (trace: %v)", n, traceTypesOf(evs))
		}
	})

	t.Run("summoner:worker registers its transcript", func(t *testing.T) {
		useSubagentEnv(t)
		r := startRun(t)
		startRunningReview(t, r, "rev-1")

		path := writeTranscript(t, "worker shard done: 3 files migrated")
		code, stderr := callSubagentStop(t, subStopPayload(t, "summoner:worker", path))
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
		}
		evs := readTrace(t, "run-1")
		done := eventsOfType(evs, shared.EventDelegationCompleted)
		if len(done) != 1 || done[0].Payload["agent_type"] != "summoner:worker" || done[0].Payload["transcript_path"] != path {
			t.Fatalf("namespaced worker transcript must register, got %v", done)
		}
		if n := len(eventsOfType(evs, shared.EventReviewVerdict)); n != 0 {
			t.Fatalf("worker stop must not conclude reviews, got %d review.verdict", n)
		}
	})

	t.Run("every other namespace ignored", func(t *testing.T) {
		for _, agentType := range []string{"other:reviewer", "summoner:fork", "summoner:code-reviewer", "reviewer:summoner", ":reviewer", "summoner:"} {
			useSubagentEnv(t)
			r := startRun(t)
			startRunningReview(t, r, "rev-1")
			resJSON, err := json.Marshal(passingResult("rev-1"))
			if err != nil {
				t.Fatal(err)
			}

			code, stderr := callSubagentStop(t, subStopPayload(t, agentType, writeTranscript(t, string(resJSON))))
			if code != 0 {
				t.Fatalf("agent_type %q: exit = %d, want 0 (stderr: %q)", agentType, code, stderr)
			}
			if evs := readTrace(t, "run-1"); len(evs) != 0 {
				t.Fatalf("agent_type %q must produce no events, got %v", agentType, traceTypesOf(evs))
			}
			if rv := reviewByID(t, reloadRun(t, "run-1"), "rev-1"); rv.Status != shared.ReviewRunning {
				t.Fatalf("agent_type %q must not touch state, review = %s", agentType, rv.Status)
			}
		}
	})
}

// TestSubagentStopLateDuplicateOfFailedReviewNoSecondEvent (fix-round m1):
// a late duplicate of an ALREADY-FAILED review is refused with nothing traced
// — the failure already happened and its review.failed is in the trace. A
// second event would show an audit reader a failure that never happened.
func TestSubagentStopLateDuplicateOfFailedReviewNoSecondEvent(t *testing.T) {
	useSubagentEnv(t)
	r := startRun(t)
	startRunningReview(t, r, "rev-1")

	// rev-1 concludes FAILED through the forged-evidence routing: one
	// review.failed, routed checkpoint, WAIT_USER.
	res := passingResult("rev-1")
	res.EvidenceToolCallIDs = []string{"toolu-forged-1"}
	resJSON, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	path := writeTranscript(t, string(resJSON))
	if code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", path)); code != 0 {
		t.Fatalf("forged-evidence backfill: exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if n := len(eventsOfType(readTrace(t, "run-1"), shared.EventReviewFailed)); n != 1 {
		t.Fatalf("review.failed events after the original failure = %d, want 1", n)
	}

	// Decide the routed checkpoint, then open a NEW review generation.
	r = reloadRun(t, "run-1")
	cp := r.CurrentCheckpoint()
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(*cp, r.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordDecision(cp.ID, shared.DecisionContinue, ""); err != nil {
		t.Fatal(err)
	}
	startRunningReview(t, r, "rev-2")
	before := reloadRun(t, "run-1")

	// The late duplicate of the old rev-1 transcript: refused (already
	// concluded), and NO second review.failed for rev-1.
	code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", path))
	if code != 0 {
		t.Fatalf("late duplicate: exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if !strings.Contains(stderr, "rev-1") {
		t.Fatalf("stderr %q must note the refused late result", stderr)
	}

	after := reloadRun(t, "run-1")
	if rv := reviewByID(t, after, "rev-2"); rv.Status != shared.ReviewRunning {
		t.Fatalf("rev-2 = %s, want RUNNING untouched", rv.Status)
	}
	if rv := reviewByID(t, after, "rev-1"); rv.Status != shared.ReviewFailed {
		t.Fatalf("rev-1 = %s, want its original FAILED conclusion preserved", rv.Status)
	}
	if len(after.Checkpoints) != len(before.Checkpoints) || after.Status != before.Status {
		t.Fatalf("the refused late duplicate must change nothing: %s / %d vs %s / %d checkpoints",
			after.Status, len(after.Checkpoints), before.Status, len(before.Checkpoints))
	}
	failed := eventsOfType(readTrace(t, "run-1"), shared.EventReviewFailed)
	if len(failed) != 1 {
		t.Fatalf("review.failed events = %d, want still exactly 1 — the failure was already traced (trace: %v)",
			len(failed), traceTypesOf(readTrace(t, "run-1")))
	}
}
