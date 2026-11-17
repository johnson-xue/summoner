package main

// main_test.go — Task 5 TDD suite, SubagentStop half (子 transcript 路径登记 +
// ⑤ 结果补记, spec docs/superpowers/specs/2026-11-09-single-loop-checkpoint-design.md
// §3 崩溃恢复 + §5 SubagentStop row). Fixtures are built through the real
// reducer API (StartRun/RequestReview/StartDelegation); transcript fixtures are
// small hand-written JSONL files — they are platform INPUT data (what the
// platform hands us at agent_transcript_path), not run state. Hermeticity comes
// from the env overrides the shared package honors (go1.16: no t.Setenv).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// --- hermetic environment -------------------------------------------------------

// setenv16 — t.Setenv is go1.17+; this repo pins go1.16.
func setenv16(t *testing.T, key, val string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	if err := os.Setenv(key, val); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	})
}

// unsetenv16 — same, for vars a test must guarantee are absent.
func unsetenv16(t *testing.T, key string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	os.Unsetenv(key)
	t.Cleanup(func() {
		if had {
			os.Setenv(key, old)
		}
	})
}

// useSubagentEnv redirects v2 state, traces and the project key into fresh
// temp dirs so no test ever touches real Summoner state.
func useSubagentEnv(t *testing.T) {
	t.Helper()
	setenv16(t, "SUMMONER_STATE_DIR", t.TempDir())
	setenv16(t, "SUMMONER_TRACE_DIR", t.TempDir())
	setenv16(t, "CLAUDE_PROJECT_DIR", t.TempDir())
	unsetenv16(t, "SUMMONER_HOOKS_DISABLE")
}

// callSubagentStop runs the whole hook; returns (exit code, stderr).
func callSubagentStop(t *testing.T, payload string) (int, string) {
	t.Helper()
	var stderr bytes.Buffer
	code := runSubagentStop(strings.NewReader(payload), &stderr)
	return code, stderr.String()
}

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(b)
}

// --- fixtures -------------------------------------------------------------------

const hookSession = "sess-1"

// startRun starts live run "run-1" (fix workflow) in ACTIVE.
func startRun(t *testing.T) *shared.RunState {
	t.Helper()
	r, err := shared.StartRun("run-1", hookSession, "fix", shared.BudgetLimits{})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	return r
}

// reviewEnvelope builds the identity-only registration for one review id —
// the reducer mints the complete envelope from the pinned acceptance snapshot.
func reviewEnvelope(revID string) shared.Envelope {
	return shared.Envelope{SchemaVersion: "2", RunID: "run-1", ReviewID: revID}
}

// acceptFixPhase drives one accepted phase with REAL artifact bytes (the C1
// staging contract: hook-hashed current digests, never producer claims), so a
// review may be minted over it.
func acceptFixPhase(t *testing.T, r *shared.RunState) {
	t.Helper()
	if err := r.OpenPhase("fix"); err != nil {
		t.Fatalf("OpenPhase(fix): %v", err)
	}
	if err := os.WriteFile(filepath.Join(shared.ProjectDir(), "fix.md"), []byte("fixed\n"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	digest, err := shared.HashArtifactFile("fix.md")
	if err != nil {
		t.Fatalf("HashArtifactFile: %v", err)
	}
	cp, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{
		PhaseID: "fix", CriteriaIDs: []string{"c1"},
		Artifacts: []shared.ArtifactRef{{Path: "fix.md", Digest: digest}},
	})
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	if err := r.RecordDecision(cp.ID, shared.DecisionContinue, ""); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
}

// startRunningReview registers revID over the accepted phase and dispatches
// it: REVIEW_PENDING with the review RUNNING — the state a reviewer subagent
// leaves behind while it works.
func startRunningReview(t *testing.T, r *shared.RunState, revID string) {
	t.Helper()
	acceptFixPhase(t, r)
	if _, err := r.RequestReview(reviewEnvelope(revID), nil); err != nil {
		t.Fatalf("RequestReview(%s): %v", revID, err)
	}
	if err := r.StartDelegation(revID, true, "tu-test"); err != nil {
		t.Fatalf("StartDelegation(%s): %v", revID, err)
	}
}

// passingResult is a well-formed COMPLETE reviewer return for one review id:
// exact all-active-criterion coverage of the MINTED envelope, the minted
// artifact digest, and evidence ids anchored per criterion (C1b/C1c).
func passingResult(t *testing.T, r *shared.RunState, revID string) shared.ReviewResult {
	t.Helper()
	rv := r.FindReview(revID)
	if rv == nil {
		t.Fatalf("passingResult: review %s not found", revID)
	}
	res := shared.ReviewResult{
		ReviewID:            revID,
		ArtifactDigest:      rv.ArtifactDigest,
		RunStatus:           shared.ReviewRunCompleted,
		Verdict:             shared.VerdictPass,
		EvidenceToolCallIDs: []string{"toolu-rev-1"},
	}
	for _, c := range rv.Envelope.ExitCriteria {
		res.CriterionVerdicts = append(res.CriterionVerdicts, shared.CriterionVerdict{
			CriterionID: c.ID,
			Status:      "pass",
			Evidence:    []shared.EvidenceAnchor{{ToolUseID: "toolu-rev-1"}},
		})
	}
	return res
}

// writeTranscript writes a small hand-written agent transcript (JSONL, the
// platform's agent_transcript_path shape): one REAL evidence tool_use (the
// reviewer reading an artifact — the id a well-formed result cites in
// evidence_tool_call_ids) followed by SubagentHandback tool_use blocks that
// carry the given messages, in order.
// writeTranscript writes a small agent transcript (JSONL). The FIRST record
// is the platform's initial task record — for a reviewer that must be the
// EXACT admitted capsule (QA-1 ownership bind); pass the capsuleOf(...) bytes
// whenever the fixture drives a review conclusion, any other string for
// registration-only/worker fixtures.
func writeTranscript(t *testing.T, initialTask string, handbackMsgs ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(mustJSON(t, map[string]interface{}{
		"type":    "user",
		"message": map[string]interface{}{"role": "user", "content": initialTask},
	}) + "\n")
	readLine := map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"role": "assistant",
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "verifying artifacts myself"},
				map[string]interface{}{
					"type":  "tool_use",
					"id":    "toolu-rev-1",
					"name":  "Read",
					"input": map[string]interface{}{"file_path": "fix.md"},
				},
			},
		},
	}
	raw, err := json.Marshal(readLine)
	if err != nil {
		t.Fatal(err)
	}
	b.Write(raw)
	b.WriteByte('\n')
	// The OBSERVED RESULT of that Read — a successful tool_result pair is what
	// C1c evidence reconciliation credits (an unpaired or failed call proves
	// nothing).
	resultLine := map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{
			"role": "user",
			"content": []interface{}{
				map[string]interface{}{
					"type":        "tool_result",
					"tool_use_id": "toolu-rev-1",
					"content":     "fixed\n",
					"is_error":    false,
				},
			},
		},
	}
	raw, err = json.Marshal(resultLine)
	if err != nil {
		t.Fatal(err)
	}
	b.Write(raw)
	b.WriteByte('\n')
	for i, msg := range handbackMsgs {
		line := map[string]interface{}{
			"type": "assistant",
			"message": map[string]interface{}{
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "review complete"},
					map[string]interface{}{
						"type":  "tool_use",
						"id":    fmt.Sprintf("toolu_hb_%d", i+1),
						"name":  "SubagentHandback",
						"input": map[string]interface{}{"message": msg},
					},
				},
			},
		}
		raw, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	p := filepath.Join(t.TempDir(), "agent-transcript.jsonl")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// fenced wraps a payload as a fenced markdown code block, the way a reviewer
// report typically embeds its structured result.
func fenced(jsonStr string) string {
	return "Review complete. Structured result:\n\n```json\n" + jsonStr + "\n```\nThanks."
}

// subStopPayload builds a SubagentStop payload for one agent.
func subStopPayload(t *testing.T, agentType, transcriptPath string) string {
	t.Helper()
	return mustJSON(t, map[string]interface{}{
		"hook_event_name":        "SubagentStop",
		"session_id":             hookSession,
		"stop_hook_active":       false,
		"agent_id":               "agent-1",
		"agent_type":             agentType,
		"agent_transcript_path":  transcriptPath,
		"last_assistant_message": "review complete",
	})
}

func reloadRun(t *testing.T, runID string) *shared.RunState {
	t.Helper()
	r, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatalf("LoadRun(%s): %v", runID, err)
	}
	return r
}

// reviewByID finds one review record by id.
func reviewByID(t *testing.T, r *shared.RunState, revID string) *shared.ReviewRecord {
	t.Helper()
	for i := range r.Reviews {
		if r.Reviews[i].ReviewID == revID {
			return &r.Reviews[i]
		}
	}
	t.Fatalf("review %s not found in %+v", revID, r.Reviews)
	return nil
}

// readTrace returns the run's trace events (nil when no file exists) and
// verifies the hash chain on every read.
func readTrace(t *testing.T, runID string) []shared.TraceEvent {
	t.Helper()
	p := filepath.Join(os.Getenv("SUMMONER_TRACE_DIR"), shared.ProjectKey(), runID+".jsonl")
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read trace: %v", err)
	}
	var evs []shared.TraceEvent
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var ev shared.TraceEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("unmarshal trace line %q: %v", line, err)
		}
		evs = append(evs, ev)
	}
	if err := shared.VerifyChain(runID); err != nil {
		t.Fatalf("VerifyChain(%s): %v", runID, err)
	}
	return evs
}

func eventsOfType(evs []shared.TraceEvent, typ string) []shared.TraceEvent {
	var out []shared.TraceEvent
	for _, ev := range evs {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

func traceTypesOf(evs []shared.TraceEvent) []string {
	types := make([]string, 0, len(evs))
	for _, ev := range evs {
		types = append(types, ev.Type)
	}
	return types
}

// --- review backfill (§3 崩溃恢复: 能从子 transcript 找到有效结果就补记) ------------

// TestSubagentStopBackfillsReview: a reviewer SubagentStop whose transcript's
// SubagentHandback carries the structured ReviewResult concludes the RUNNING
// review through RecordReviewResult — REVIEWED, review-result checkpoint open,
// WAIT_USER — and traces delegation.completed + review.verdict. The result
// object is found fenced, bare, or embedded in prose.
func TestSubagentStopBackfillsReview(t *testing.T) {
	for name, wrap := range map[string]func(string) string{
		"fenced json":   fenced,
		"bare json":     func(s string) string { return s },
		"embedded json": func(s string) string { return "Final report follows.\n" + s + "\nEnd of report." },
	} {
		t.Run(name, func(t *testing.T) {
			useSubagentEnv(t)
			r := startRun(t)
			startRunningReview(t, r, "rev-1")
			// The MINTED envelope's artifact digest is the binding identity —
			// the record and the traced verdict must both carry it exactly.
			wantDigest := r.FindReview("rev-1").ArtifactDigest

			resJSON, err := json.Marshal(passingResult(t, r, "rev-1"))
			if err != nil {
				t.Fatal(err)
			}
			path := writeTranscript(t, capsuleOf(t, r, "rev-1"), wrap(string(resJSON)))

			code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", path))
			if code != 0 {
				t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
			}

			disk := reloadRun(t, "run-1")
			rv := reviewByID(t, disk, "rev-1")
			if rv.Status != shared.ReviewReviewed {
				t.Fatalf("review status = %s, want REVIEWED (backfilled from the transcript)", rv.Status)
			}
			if rv.Verdict != shared.VerdictPass || rv.ArtifactDigest != wantDigest {
				t.Fatalf("review record verdict/digest = %s/%s, want PASS bound to the minted artifact digest %s", rv.Verdict, rv.ArtifactDigest, wantDigest)
			}
			if disk.Status != shared.StatusWaitUser {
				t.Fatalf("run status = %s, want WAIT_USER (PASS still leaves the run WAIT_USER)", disk.Status)
			}
			if cp := disk.CurrentCheckpoint(); cp == nil || cp.Kind != shared.CheckpointReviewResult {
				t.Fatalf("a review-result checkpoint must be open, got %+v", cp)
			}

			evs := readTrace(t, "run-1")
			done := eventsOfType(evs, shared.EventDelegationCompleted)
			if len(done) != 1 {
				t.Fatalf("delegation.completed events = %d, want 1 (trace: %v)", len(done), traceTypesOf(evs))
			}
			if done[0].Payload["agent_type"] != "reviewer" || done[0].Payload["transcript_path"] != path ||
				done[0].Payload["agent_id"] != "agent-1" {
				t.Fatalf("delegation.completed payload = %v, want the transcript registration triple", done[0].Payload)
			}
			verdicts := eventsOfType(evs, shared.EventReviewVerdict)
			if len(verdicts) != 1 {
				t.Fatalf("review.verdict events = %d, want 1 (trace: %v)", len(verdicts), traceTypesOf(evs))
			}
			if verdicts[0].Payload["review_id"] != "rev-1" || verdicts[0].Payload["run_status"] != shared.ReviewRunCompleted {
				t.Fatalf("review.verdict payload = %v, want rev-1 / COMPLETED", verdicts[0].Payload)
			}
			// T12 fix-back: the reconciled evidence list is MIRRORED into the
			// verdict payload — the trace, not just the transcript join, must
			// carry what the PASS is backed by (auditability contract: a
			// COMPLETED+PASS verdict always traces a non-empty list).
			if ids, ok := verdicts[0].Payload["evidence_tool_call_ids"].([]interface{}); !ok || len(ids) != 1 || ids[0] != "toolu-rev-1" {
				t.Fatalf("review.verdict evidence_tool_call_ids = %#v, want [toolu-rev-1]", verdicts[0].Payload["evidence_tool_call_ids"])
			}
			// T12 fix-back 2 (split ruling A): the record's digests mirror
			// too — artifact identity exactly, envelope identity non-empty
			// (opaque content digest, presence is the contract).
			if verdicts[0].Payload["artifact_digest"] != wantDigest {
				t.Fatalf("review.verdict artifact_digest = %#v, want the minted digest %s", verdicts[0].Payload["artifact_digest"], wantDigest)
			}
			if env, ok := verdicts[0].Payload["envelope_digest"].(string); !ok || env == "" {
				t.Fatalf("review.verdict envelope_digest = %#v, want a non-empty digest", verdicts[0].Payload["envelope_digest"])
			}
		})
	}

	// Crash window (spec §3), SUPERSEDED by the final-fix wave's QA-1
	// ownership bind (adjudication §3): a PREPARED review has NO admitted
	// dispatch receipt — no parent Agent tool_use ever bound the attempt — so
	// a child transcript arriving for it has no owner, and ownership is
	// never inferred. The backfill quarantines (FAILED + review-result
	// checkpoint, run decidable); recovery runs through the C3 retry
	// machinery, never a free un-owned conclusion.
	t.Run("prepared review quarantines an unowned child", func(t *testing.T) {
		useSubagentEnv(t)
		r := startRun(t)
		acceptFixPhase(t, r)
		if _, err := r.RequestReview(reviewEnvelope("rev-1"), nil); err != nil {
			t.Fatalf("RequestReview: %v", err)
		}
		resJSON, err := json.Marshal(passingResult(t, r, "rev-1"))
		if err != nil {
			t.Fatal(err)
		}
		code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", writeTranscript(t, "run the review envelope rev-1", string(resJSON))))
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
		}
		disk := reloadRun(t, "run-1")
		if rv := reviewByID(t, disk, "rev-1"); rv.Status != shared.ReviewFailed {
			t.Fatalf("review status = %s, want FAILED (an unowned PREPARED child is quarantined, never concluded — QA-1)", rv.Status)
		}
		if disk.Status != shared.StatusWaitUser {
			t.Fatalf("run status = %s, want WAIT_USER at the quarantined checkpoint", disk.Status)
		}
	})
}

// TestSubagentStopChildOwnershipBind (final-fix wave QA-1, adjudication §3):
// the child transcript's INITIAL record must be the platform-origin launch
// record — type "user", content a plain string byte-identical to the EXACT
// capsule of the admitted attempt — checked BEFORE both verdict acceptance
// and evidence reconciliation. A foreign first record, a near-miss, a
// non-string content or a missing admitted receipt quarantines the review
// (FAILED + review-result checkpoint); the owned transcript concludes
// normally.
func TestSubagentStopChildOwnershipBind(t *testing.T) {
	// Hostile: a perfectly VALID result, but ownership is unproven — the
	// PASS never lands.
	for name, mutate := range map[string]func(r *shared.RunState, capsule string) string{
		"foreign prose": func(r *shared.RunState, capsule string) string {
			return "run the review envelope rev-1"
		},
		"near-miss": func(r *shared.RunState, capsule string) string {
			return capsule[:len(capsule)-2]
		},
	} {
		t.Run(name, func(t *testing.T) {
			useSubagentEnv(t)
			r := startRun(t)
			startRunningReview(t, r, "rev-1")
			resJSON, err := json.Marshal(passingResult(t, r, "rev-1"))
			if err != nil {
				t.Fatal(err)
			}
			path := writeTranscript(t, mutate(r, capsuleOf(t, r, "rev-1")), string(resJSON))
			code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", path))
			if code != 0 {
				t.Fatalf("exit = %d, want 0 (stderr %q)", code, stderr)
			}
			disk := reloadRun(t, "run-1")
			if rv := reviewByID(t, disk, "rev-1"); rv.Status != shared.ReviewFailed {
				t.Fatalf("review = %s, want FAILED (unowned child quarantined)", rv.Status)
			}
			if disk.Status != shared.StatusWaitUser {
				t.Fatalf("status = %s, want WAIT_USER (quarantine routes a decidable checkpoint)", disk.Status)
			}
		})
	}

	// Hostile: the initial record's content is NOT a plain string (a content
	// block array) — malformed platform record, quarantined the same way.
	t.Run("non-string initial content", func(t *testing.T) {
		useSubagentEnv(t)
		r := startRun(t)
		startRunningReview(t, r, "rev-1")
		resJSON, err := json.Marshal(passingResult(t, r, "rev-1"))
		if err != nil {
			t.Fatal(err)
		}
		path := writeTranscript(t, capsuleOf(t, r, "rev-1"), string(resJSON))
		lines := strings.Split(mustRead(t, path), "\n")
		lines[0] = `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"not a plain string"}]}}`
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			t.Fatal(err)
		}
		if code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", path)); code != 0 {
			t.Fatalf("exit = %d, want 0 (stderr %q)", code, stderr)
		}
		if rv := reviewByID(t, reloadRun(t, "run-1"), "rev-1"); rv.Status != shared.ReviewFailed {
			t.Fatalf("review = %s, want FAILED (malformed initial record quarantined)", rv.Status)
		}
	})

	// Positive control: the exact capsule as the initial record — the same
	// result concludes REVIEWED PASS.
	useSubagentEnv(t)
	r := startRun(t)
	startRunningReview(t, r, "rev-1")
	resJSON, err := json.Marshal(passingResult(t, r, "rev-1"))
	if err != nil {
		t.Fatal(err)
	}
	path := writeTranscript(t, capsuleOf(t, r, "rev-1"), string(resJSON))
	if code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", path)); code != 0 {
		t.Fatalf("owned backfill exit = %d, want 0 (stderr %q)", code, stderr)
	}
	disk := reloadRun(t, "run-1")
	rv := reviewByID(t, disk, "rev-1")
	if rv.Status != shared.ReviewReviewed || rv.Verdict != shared.VerdictPass {
		t.Fatalf("owned review = %s/%s, want REVIEWED PASS", rv.Status, rv.Verdict)
	}
	// Recovery round C3: the platform-registered transcript lands on the
	// attempt receipt (observed child transcript — append-only, first wins).
	if att := rv.ActiveAttempt(rv.ActiveAttemptID); att == nil || att.ChildTranscriptPath != path {
		t.Fatalf("attempt receipt transcript = %+v, want the observed path %s", att, path)
	}
}

// TestSubagentStopRetiredAttemptArchival (re-review F2, adjudication §3
// "After user retry/rotation, A is archival only"): after the user rotates a
// wedged review (A RETIRED, fresh B PREPARED, run REVIEW_PENDING), attempt A's
// late stop arrives carrying A's OWN capsule as the initial record and a
// well-formed result naming A. That stop must be ARCHIVAL: A's receipt gets
// the observed transcript, B stays untouched and in flight, no verdict, no
// review.failed, the run stays REVIEW_PENDING — a late A never concludes OR
// fails the successor.
func TestSubagentStopRetiredAttemptArchival(t *testing.T) {
	useSubagentEnv(t)
	r := startRun(t)
	startRunningReview(t, r, "rev-1")
	rv := r.FindReview("rev-1")
	attA := rv.ActiveAttemptID

	// Capture A's capsule BEFORE the rotation (after it the review's active
	// attempt is B — the capsule the classifier must still match A against).
	capsuleA := capsuleOf(t, r, "rev-1")
	if capsuleA == "" {
		t.Fatal("capsuleOf: empty capsule for the dispatched attempt A")
	}

	// The user wedges and rotates: retry checkpoint → CONTINUE (funded).
	if err := r.RecoverReview("rev-1", attA); err != nil {
		t.Fatalf("RecoverReview: %v", err)
	}
	cp := r.CurrentCheckpoint()
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(*cp, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	if err := r.RecordDecision(cp.ID, shared.DecisionContinue, "tu-rotate"); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	rv = r.FindReview("rev-1")
	attB := rv.ActiveAttemptID
	if rv.Status != shared.ReviewPrepared || attB == attA {
		t.Fatalf("after rotation: review = %s, active = %q, want PREPARED with fresh B ≠ A %q", rv.Status, attB, attA)
	}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}

	// A's late transcript: A's own capsule as the initial record, a passing
	// handback naming A — everything a hostile-but-real late child carries.
	res := passingResult(t, r, "rev-1")
	res.AttemptID = attA
	resJSON, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	path := writeTranscript(t, capsuleA, string(resJSON))
	code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", path))
	if code != 0 {
		t.Fatalf("late A stop exit = %d, want 0 (stderr %q)", code, stderr)
	}

	// ARCHIVAL: B in flight, nothing concluded, nothing failed.
	disk := reloadRun(t, "run-1")
	drv := reviewByID(t, disk, "rev-1")
	if drv.Status != shared.ReviewPrepared || drv.ActiveAttemptID != attB {
		t.Fatalf("review = %s active %q, want PREPARED with B %q in flight (a late A never concludes or fails the successor)", drv.Status, drv.ActiveAttemptID, attB)
	}
	if disk.Status != shared.StatusReviewPending {
		t.Fatalf("run = %s, want REVIEW_PENDING (no quarantine, no result display)", disk.Status)
	}
	// A's receipt carries the observed transcript (append-only, first wins);
	// B's receipt must NOT be stamped with A's transcript (false evidence join).
	if att := drv.ActiveAttempt(attA); att == nil || att.ChildTranscriptPath != path {
		t.Fatalf("A's receipt = %+v, want ChildTranscriptPath %q (archival registration)", att, path)
	}
	if att := drv.ActiveAttempt(attB); att == nil || att.ChildTranscriptPath != "" {
		t.Fatalf("B's receipt = %+v, want NO transcript (A's stop must not stamp the active attempt)", att)
	}
	// No verdict, no failure — the trace carries the delegation.completed
	// registration and nothing review-concluding.
	evs := readTrace(t, "run-1")
	if len(eventsOfType(evs, shared.EventReviewVerdict)) != 0 {
		t.Fatal("A's late stop must not trace review.verdict")
	}
	if failed := eventsOfType(evs, shared.EventReviewFailed); len(failed) != 0 {
		t.Fatalf("A's late stop must not trace review.failed, got %+v", failed)
	}
	if !strings.Contains(stderr, "archival") {
		t.Fatalf("stderr should name the archival handling, got %q", stderr)
	}
}

// capsuleOf returns the EXACT launch-capsule bytes of the run's in-flight
// review's active attempt (what the gate matched at dispatch — the ownership
// key of the child's initial record).
func capsuleOf(t *testing.T, r *shared.RunState, revID string) string {
	t.Helper()
	rv := r.FindReview(revID)
	if rv == nil {
		t.Fatalf("capsuleOf: review %s not found", revID)
	}
	capsule, ok := shared.BuildReviewCapsule(r, revID, rv.ActiveAttemptID)
	if !ok {
		t.Fatalf("BuildReviewCapsule(%s): not dispatchable", revID)
	}
	b, err := capsule.Bytes()
	if err != nil {
		t.Fatalf("capsule.Bytes(): %v", err)
	}
	return string(b)
}

// mustRead reads a file whole (fixture surgery helper).
func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSubagentStopDedupsByCallID: a LATE duplicate result (the review already
// concluded; a NEW generation is in flight) must not affect the new generation
// — RecordReviewResult refuses concluded reviews; the hook maps the refusal to
// a stderr note and exit 0, state untouched.
func TestSubagentStopDedupsByCallID(t *testing.T) {
	useSubagentEnv(t)
	r := startRun(t)
	startRunningReview(t, r, "rev-1")

	resJSON, err := json.Marshal(passingResult(t, r, "rev-1"))
	if err != nil {
		t.Fatal(err)
	}
	path := writeTranscript(t, capsuleOf(t, r, "rev-1"), string(resJSON))
	if code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", path)); code != 0 {
		t.Fatalf("first backfill: exit = %d, want 0 (stderr: %q)", code, stderr)
	}

	// Advance past the review-result checkpoint and open a NEW review. The
	// backfill mutated the on-disk state (the hook loads its own copy), so
	// drive the fixture from a fresh reload.
	r = reloadRun(t, "run-1")
	cp := r.CurrentCheckpoint()
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(*cp, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	if err := r.RecordDecision(cp.ID, shared.DecisionContinue, ""); err != nil {
		t.Fatalf("decide review checkpoint: %v", err)
	}
	startRunningReview(t, r, "rev-2")
	before := reloadRun(t, "run-1")

	code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", path)) // same transcript: rev-1's result
	if code != 0 {
		t.Fatalf("late duplicate: exit = %d, want 0 — SubagentStop never bricks the subagent's exit (stderr: %q)", code, stderr)
	}
	if !strings.Contains(stderr, "rev-1") {
		t.Fatalf("stderr %q must note the refused late result for rev-1", stderr)
	}

	after := reloadRun(t, "run-1")
	if rv := reviewByID(t, after, "rev-2"); rv.Status != shared.ReviewRunning {
		t.Fatalf("new generation rev-2 = %s, want RUNNING untouched by the late rev-1 result", rv.Status)
	}
	if rv := reviewByID(t, after, "rev-1"); rv.Status != shared.ReviewReviewed || rv.Verdict != shared.VerdictPass {
		t.Fatalf("rev-1 record = %+v, want its original conclusion preserved", rv)
	}
	if len(after.Checkpoints) != len(before.Checkpoints) {
		t.Fatalf("checkpoints = %d, want %d — a refused late result opens nothing", len(after.Checkpoints), len(before.Checkpoints))
	}
	if after.Status != before.Status {
		t.Fatalf("status = %s, want %s unchanged", after.Status, before.Status)
	}
	if n := len(eventsOfType(readTrace(t, "run-1"), shared.EventReviewVerdict)); n != 1 {
		t.Fatalf("review.verdict events = %d, want exactly 1 (the original conclusion)", n)
	}
}

// TestSubagentStopRegistersWorkerTranscript: a worker delegation registers its
// transcript path (delegation.completed) — and NEVER backfills a review, even
// when a review is RUNNING and the worker's handback happens to embed a
// ReviewResult-shaped object (only the reviewer may conclude its own review).
func TestSubagentStopRegistersWorkerTranscript(t *testing.T) {
	useSubagentEnv(t)
	r := startRun(t)
	startRunningReview(t, r, "rev-1")

	resJSON, err := json.Marshal(passingResult(t, r, "rev-1"))
	if err != nil {
		t.Fatal(err)
	}
	path := writeTranscript(t, "worker shard done: 3 files migrated") // plain worker report
	code, stderr := callSubagentStop(t, subStopPayload(t, "worker", path))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	evs := readTrace(t, "run-1")
	done := eventsOfType(evs, shared.EventDelegationCompleted)
	if len(done) != 1 || done[0].Payload["agent_type"] != "worker" || done[0].Payload["transcript_path"] != path {
		t.Fatalf("worker transcript must be registered, got %v", done)
	}
	if n := len(eventsOfType(evs, shared.EventReviewVerdict)); n != 0 {
		t.Fatalf("worker stop must not conclude reviews, got %d review.verdict events", n)
	}

	// Adversarial: a worker handback carrying a ReviewResult JSON while a
	// review is RUNNING — registration only, review untouched.
	path2 := writeTranscript(t, "worker shard report", string(resJSON))
	code, stderr = callSubagentStop(t, subStopPayload(t, "worker", path2))
	if code != 0 {
		t.Fatalf("adversarial worker: exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if rv := reviewByID(t, reloadRun(t, "run-1"), "rev-1"); rv.Status != shared.ReviewRunning {
		t.Fatalf("a worker transcript must never conclude a review, got %s", rv.Status)
	}
}

// TestStopIgnoresNonReviewerAgents: internal platform agents (prompt
// suggestions, /btw, Explore…) also fire SubagentStop with their own agent_type
// — anything outside {reviewer, worker} is ignored entirely (exit 0, no events,
// no state change).
func TestStopIgnoresNonReviewerAgents(t *testing.T) {
	for _, agentType := range []string{"prompt-suggestions", "btw", "Explore", "claude", ""} {
		useSubagentEnv(t)
		r := startRun(t)
		startRunningReview(t, r, "rev-1")

		code, stderr := callSubagentStop(t, subStopPayload(t, agentType, writeTranscript(t, "internal agent chatter")))
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
}

// TestSubagentStopUnparseableTranscriptRegistersOnly: a transcript that is
// garbage, absent, or carries no structured result registers the path and
// leaves the review RUNNING — the backfill degrades to registration, never an
// error for the subagent's exit.
func TestSubagentStopUnparseableTranscriptRegistersOnly(t *testing.T) {
	cases := map[string]func(t *testing.T) string{
		"garbage file": func(t *testing.T) string {
			p := filepath.Join(t.TempDir(), "t.jsonl")
			if err := os.WriteFile(p, []byte("not jsonl at all\n{broken"), 0o644); err != nil {
				t.Fatal(err)
			}
			return p
		},
		"missing file": func(t *testing.T) string {
			return filepath.Join(t.TempDir(), "never-written.jsonl")
		},
		"plain handback": func(t *testing.T) string {
			return writeTranscript(t, "I reviewed everything and it looked fine to me.")
		},
	}
	for name, fixture := range cases {
		t.Run(name, func(t *testing.T) {
			useSubagentEnv(t)
			r := startRun(t)
			startRunningReview(t, r, "rev-1")

			code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", fixture(t)))
			if code != 0 {
				t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
			}
			disk := reloadRun(t, "run-1")
			if rv := reviewByID(t, disk, "rev-1"); rv.Status != shared.ReviewRunning {
				t.Fatalf("review = %s, want RUNNING (absent/unparseable result concludes nothing)", rv.Status)
			}
			if disk.Status != shared.StatusReviewPending {
				t.Fatalf("status = %s, want REVIEW_PENDING", disk.Status)
			}
			evs := readTrace(t, "run-1")
			if n := len(eventsOfType(evs, shared.EventDelegationCompleted)); n != 1 {
				t.Fatalf("registration must still happen, delegation.completed = %d (trace: %v)", n, traceTypesOf(evs))
			}
			if n := len(eventsOfType(evs, shared.EventReviewVerdict)); n != 0 {
				t.Fatalf("no verdict without a parseable result, got %d review.verdict", n)
			}
		})
	}
}

// TestSubagentStopNoInFlightReviewRegistersOnly: a reviewer stop with no
// PREPARED/RUNNING review (nothing to backfill) registers the transcript and
// changes nothing.
func TestSubagentStopNoInFlightReviewRegistersOnly(t *testing.T) {
	useSubagentEnv(t)
	startRun(t) // ACTIVE, no review ever requested
	// A well-formed-looking result for a review id the run NEVER registered —
	// built inline on purpose (passingResult derives from a minted record,
	// which rev-ghost by construction has none of).
	res := shared.ReviewResult{
		ReviewID:            "rev-ghost",
		ArtifactDigest:      "dd-ghost",
		RunStatus:           shared.ReviewRunCompleted,
		Verdict:             shared.VerdictPass,
		EvidenceToolCallIDs: []string{"toolu-rev-1"},
		CriterionVerdicts:   []shared.CriterionVerdict{{CriterionID: "crit-correctness", Status: "pass", Evidence: []shared.EvidenceAnchor{{ToolUseID: "toolu-rev-1"}}}},
	}
	resJSON, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}

	code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", writeTranscript(t, "review the ghost envelope", string(resJSON))))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	disk := reloadRun(t, "run-1")
	if len(disk.Reviews) != 0 {
		t.Fatalf("reviews = %+v, want none — a transcript alone registers no review", disk.Reviews)
	}
	if disk.Status != shared.StatusActive {
		t.Fatalf("status = %s, want ACTIVE unchanged", disk.Status)
	}
	if n := len(eventsOfType(readTrace(t, "run-1"), shared.EventDelegationCompleted)); n != 1 {
		t.Fatalf("transcript registration must still happen, got %d", n)
	}
}

// TestSubagentStopEmptyTranscriptPath: a reviewer/worker stop without a
// transcript path has nothing to register — stderr note, exit 0, no events.
func TestSubagentStopEmptyTranscriptPath(t *testing.T) {
	useSubagentEnv(t)
	startRun(t)
	code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", ""))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	if evs := readTrace(t, "run-1"); len(evs) != 0 {
		t.Fatalf("no transcript path → no events, got %v", traceTypesOf(evs))
	}
}

// TestSubagentStopNoRunNoEvents: no active run for the session → exit 0, no
// events (a subagent stopping outside any Summoner run is not our business).
func TestSubagentStopNoRunNoEvents(t *testing.T) {
	useSubagentEnv(t)
	code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", writeTranscript(t, "done")))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	if evs := readTrace(t, "run-1"); len(evs) != 0 {
		t.Fatalf("no-run session must produce no events, got %v", traceTypesOf(evs))
	}
}

// TestSubagentStopAmbiguousRunsWarnsNotFails: more than one live run is
// ambiguous — warn on stderr, exit 0 (a registrar must not brick the
// subagent's exit), no events.
func TestSubagentStopAmbiguousRunsWarnsNotFails(t *testing.T) {
	useSubagentEnv(t)
	if _, err := shared.StartRun("run-1", hookSession, "fix", shared.BudgetLimits{}); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.StartRun("run-2", hookSession, "fix", shared.BudgetLimits{}); err != nil {
		t.Fatal(err)
	}
	code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", writeTranscript(t, "done")))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	if !strings.Contains(stderr, "live") {
		t.Fatalf("stderr %q must warn about the ambiguous live runs", stderr)
	}
	for _, id := range []string{"run-1", "run-2"} {
		if evs := readTrace(t, id); len(evs) != 0 {
			t.Fatalf("ambiguous resolution must append no events to %s, got %v", id, traceTypesOf(evs))
		}
	}
}

// TestSubagentStopParseFailureFailsClosed: unparseable stdin and trailing
// garbage are the ONE fail-closed path (exit 2) — everything else exits 0.
func TestSubagentStopParseFailureFailsClosed(t *testing.T) {
	useSubagentEnv(t)
	startRun(t)
	for name, payload := range map[string]string{
		"not json":       "this is not json",
		"trailing bytes": `{"hook_event_name":"SubagentStop"} trailing`,
	} {
		code, stderr := callSubagentStop(t, payload)
		if code != 2 {
			t.Errorf("%s: exit code = %d, want 2 (stderr: %q)", name, code, stderr)
		}
	}
}

// TestEscapeHatchBothBinaries — SubagentStop half (the Stop half lives in the
// sibling stop package): SUMMONER_HOOKS_DISABLE=1 enforces nothing — no
// registration, no backfill — but on exactly one live run it first records the
// contract violation (hooks.disabled); with no live run there are no events.
func TestEscapeHatchBothBinaries(t *testing.T) {
	t.Run("live run traces the violation, no enforcement", func(t *testing.T) {
		useSubagentEnv(t)
		r := startRun(t)
		startRunningReview(t, r, "rev-1")
		resJSON, err := json.Marshal(passingResult(t, r, "rev-1"))
		if err != nil {
			t.Fatal(err)
		}
		setenv16(t, "SUMMONER_HOOKS_DISABLE", "1")

		code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", writeTranscript(t, string(resJSON))))
		if code != 0 {
			t.Fatalf("exit = %d, want 0 — the hatch enforces nothing (stderr: %q)", code, stderr)
		}
		if rv := reviewByID(t, reloadRun(t, "run-1"), "rev-1"); rv.Status != shared.ReviewRunning {
			t.Fatalf("the hatch must not backfill, review = %s", rv.Status)
		}
		evs := readTrace(t, "run-1")
		if n := len(eventsOfType(evs, shared.EventHooksDisabled)); n != 1 {
			t.Fatalf("hooks.disabled events = %d, want exactly 1 (trace: %v)", n, traceTypesOf(evs))
		}
		if n := len(eventsOfType(evs, shared.EventDelegationCompleted)); n != 0 {
			t.Fatalf("the hatch registers nothing, got %d delegation.completed", n)
		}
	})
	t.Run("no live run stays silent", func(t *testing.T) {
		useSubagentEnv(t)
		setenv16(t, "SUMMONER_HOOKS_DISABLE", "1")
		code, stderr := callSubagentStop(t, subStopPayload(t, "worker", writeTranscript(t, "done")))
		if code != 0 {
			t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
		}
		if evs := readTrace(t, "run-1"); len(evs) != 0 {
			t.Fatalf("no live run → no events, got %v", traceTypesOf(evs))
		}
	})
}

// --- T17: evidence reconciliation before acceptance --------------------------------

// TestSubagentStopForgedEvidenceRoutesProtocolError: a claimed evidence id
// that is NOT a tool_use record of the reviewer's own transcript is a forged
// id (spec §3 证据对账) — ReconcileEvidence runs BEFORE the verdict is
// accepted, the failure routes through RecordReviewProtocolError (FAILED +
// REVIEW_RESULT checkpoint + WAIT_USER, persisted), review.failed is traced,
// and the run stays decidable: the routed checkpoint accepts a decision.
func TestSubagentStopForgedEvidenceRoutesProtocolError(t *testing.T) {
	useSubagentEnv(t)
	r := startRun(t)
	startRunningReview(t, r, "rev-1")

	// The transcript holds real tool_use ids toolu-rev-1 and toolu_hb_1; the
	// result claims one real and one FORGED id.
	res := passingResult(t, r, "rev-1")
	res.EvidenceToolCallIDs = []string{"toolu-rev-1", "toolu-forged-1"}
	resJSON, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	path := writeTranscript(t, capsuleOf(t, r, "rev-1"), string(resJSON))

	code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", path))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 — a routed protocol error never bricks the subagent (stderr: %q)", code, stderr)
	}

	disk := reloadRun(t, "run-1")
	rv := reviewByID(t, disk, "rev-1")
	if rv.Status != shared.ReviewFailed {
		t.Fatalf("review status = %s, want FAILED (forged evidence id)", rv.Status)
	}
	if rv.Verdict != "" {
		t.Fatalf("a FAILED review carries no verdict, got %q", rv.Verdict)
	}
	cp := disk.CurrentCheckpoint()
	if cp == nil || cp.Kind != shared.CheckpointReviewResult || disk.Status != shared.StatusWaitUser {
		t.Fatalf("after routing: status %s checkpoint %+v, want WAIT_USER at a REVIEW_RESULT checkpoint", disk.Status, cp)
	}
	if !strings.HasPrefix(cp.Note, "FAILED ") || !strings.Contains(cp.Note, "toolu-forged-1") {
		t.Fatalf("checkpoint note %q must lead with FAILED and name the forged id", cp.Note)
	}

	evs := readTrace(t, "run-1")
	failed := eventsOfType(evs, shared.EventReviewFailed)
	if len(failed) != 1 {
		t.Fatalf("review.failed events = %d, want 1 (trace: %v)", len(failed), traceTypesOf(evs))
	}
	if failed[0].Payload["review_id"] != "rev-1" || !strings.Contains(failed[0].Payload["reason"].(string), "toolu-forged-1") {
		t.Fatalf("review.failed payload = %v, want rev-1 + the forged id in the reason", failed[0].Payload)
	}
	if n := len(eventsOfType(evs, shared.EventReviewVerdict)); n != 0 {
		t.Fatalf("a forged-evidence result never records a verdict, got %d review.verdict", n)
	}

	// Decidable: decide the routed checkpoint like any other.
	r = disk
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(*cp, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	if err := r.RecordDecision(cp.ID, shared.DecisionStop, ""); err != nil {
		t.Fatalf("the run must stay decidable after a forged-evidence protocol error: %v", err)
	}
	if after := reloadRun(t, "run-1"); after.Status != shared.StatusPostGamePending {
		t.Fatalf("status = %s, want POST_GAME_PENDING after STOP at the FAILED checkpoint", after.Status)
	}
}

// TestSubagentStopForgedEvidenceCrashWindow: the PREPARED crash window
// reconciles identically — a forged id in a re-discovered result routes to
// FAILED without a delegation ever having been recorded.
func TestSubagentStopForgedEvidenceCrashWindow(t *testing.T) {
	useSubagentEnv(t)
	r := startRun(t)
	acceptFixPhase(t, r)
	if _, err := r.RequestReview(reviewEnvelope("rev-1"), nil); err != nil {
		t.Fatal(err)
	}
	res := passingResult(t, r, "rev-1")
	res.EvidenceToolCallIDs = []string{"toolu-forged-1"}
	resJSON, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", writeTranscript(t, capsuleOf(t, r, "rev-1"), string(resJSON))))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	disk := reloadRun(t, "run-1")
	if rv := reviewByID(t, disk, "rev-1"); rv.Status != shared.ReviewFailed {
		t.Fatalf("review = %s, want FAILED from the PREPARED crash window", rv.Status)
	}
	if disk.Status != shared.StatusWaitUser {
		t.Fatalf("status = %s, want WAIT_USER at the routed checkpoint", disk.Status)
	}
}

// TestSubagentStopLateDuplicateSkipsReconciliation: a late duplicate result
// naming an ALREADY-concluded review while a new generation runs must not be
// treated as a forged-evidence protocol error against the NEW review — the
// result's review id gates reconciliation, and the refusal stays a refusal.
func TestSubagentStopLateDuplicateSkipsReconciliation(t *testing.T) {
	useSubagentEnv(t)
	r := startRun(t)
	startRunningReview(t, r, "rev-1")

	// First result claims an id the transcript does not carry but names a
	// review that is NOT the in-flight one — reconciliation is gated on the
	// result's own review id; RecordReviewResult refuses the unknown id.
	res := shared.ReviewResult{
		ReviewID:            "rev-other",
		RunStatus:           shared.ReviewRunCompleted,
		Verdict:             shared.VerdictPass,
		EvidenceToolCallIDs: []string{"toolu-forged-1"},
	}
	resJSON, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", writeTranscript(t, capsuleOf(t, r, "rev-1"), string(resJSON))))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	disk := reloadRun(t, "run-1")
	if rv := reviewByID(t, disk, "rev-1"); rv.Status != shared.ReviewRunning {
		t.Fatalf("in-flight review = %s, want RUNNING untouched", rv.Status)
	}
	if disk.Status != shared.StatusReviewPending || disk.CurrentCheckpoint() != nil {
		t.Fatalf("nothing must route for a foreign-id result: %s / %+v", disk.Status, disk.CurrentCheckpoint())
	}
	if n := len(eventsOfType(readTrace(t, "run-1"), shared.EventReviewFailed)); n != 0 {
		t.Fatalf("review.failed events = %d, want 0", n)
	}
}

// TestSubagentStopBlockedResultWithoutEvidencePasses: a BLOCKED result claims
// no evidence ids (legitimate — BLOCKED reports a reason code, not a quality
// conclusion); reconciliation is skipped and the result records normally.
func TestSubagentStopBlockedResultWithoutEvidencePasses(t *testing.T) {
	useSubagentEnv(t)
	r := startRun(t)
	startRunningReview(t, r, "rev-1")

	// BLOCKED carries no quality conclusion and no evidence ids, but STILL
	// names the artifact set it was operating on (C1b: the digest is compared,
	// never stored blind — it proves the blocked reviewer held the pinned
	// envelope).
	res := shared.ReviewResult{
		ReviewID:       "rev-1",
		ArtifactDigest: r.FindReview("rev-1").ArtifactDigest,
		RunStatus:      shared.ReviewRunBlocked,
		ReasonCode:     "sandbox-inactive",
	}
	resJSON, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", writeTranscript(t, capsuleOf(t, r, "rev-1"), string(resJSON))))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	disk := reloadRun(t, "run-1")
	rv := reviewByID(t, disk, "rev-1")
	if rv.Status != shared.ReviewBlocked || rv.ReasonCode != "sandbox-inactive" {
		t.Fatalf("review = %+v, want BLOCKED with the reason code", rv)
	}
	if disk.Status != shared.StatusWaitUser {
		t.Fatalf("status = %s, want WAIT_USER at the review-result checkpoint", disk.Status)
	}

	// T12 fix-back 3 (F7), C1b-adapted side of the digest mirror: a
	// reason-code BLOCKED verdict carries NO verdict and NO evidence ids
	// (a reason code never poses as a quality conclusion) — while BOTH
	// digests mirror: envelope_digest (registration identity, set at
	// RequestReview) and artifact_digest (the minted artifact-set identity
	// the BLOCKED result claimed and had compared — a block still names the
	// pinned envelope it was operating on).
	verdicts := eventsOfType(readTrace(t, "run-1"), shared.EventReviewVerdict)
	if len(verdicts) != 1 {
		t.Fatalf("review.verdict events = %d, want 1", len(verdicts))
	}
	pl := verdicts[0].Payload
	if pl["artifact_digest"] != r.FindReview("rev-1").ArtifactDigest {
		t.Errorf("BLOCKED verdict artifact_digest = %#v, want the minted digest the result verified", pl["artifact_digest"])
	}
	if _, ok := pl["verdict"]; ok {
		t.Errorf("BLOCKED verdict carries verdict = %#v — a reason code must never pose as a quality conclusion", pl["verdict"])
	}
	if _, ok := pl["evidence_tool_call_ids"]; ok {
		t.Errorf("BLOCKED verdict carries evidence_tool_call_ids = %#v — nothing was reconciled to claim", pl["evidence_tool_call_ids"])
	}
	if env, ok := pl["envelope_digest"].(string); !ok || env == "" {
		t.Errorf("BLOCKED verdict envelope_digest = %#v, want a non-empty registration digest", pl["envelope_digest"])
	}
}
