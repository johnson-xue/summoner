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

// reviewEnvelope builds a minimal legal review envelope for one review id.
func reviewEnvelope(revID string) shared.Envelope {
	return shared.Envelope{
		SchemaVersion: "2",
		RunID:         "run-1",
		ReviewID:      revID,
		ArtifactSet:   []shared.ArtifactRef{{Path: "fix.md", Digest: "dd-1"}},
	}
}

// startRunningReview registers revID and dispatches it: REVIEW_PENDING with the
// review RUNNING — the state a reviewer subagent leaves behind while it works.
func startRunningReview(t *testing.T, r *shared.RunState, revID string) {
	t.Helper()
	if _, err := r.RequestReview(reviewEnvelope(revID)); err != nil {
		t.Fatalf("RequestReview(%s): %v", revID, err)
	}
	if err := r.StartDelegation(revID, true); err != nil {
		t.Fatalf("StartDelegation(%s): %v", revID, err)
	}
}

// passingResult is a well-formed reviewer return for one review id.
func passingResult(revID string) shared.ReviewResult {
	return shared.ReviewResult{
		ReviewID:            revID,
		ArtifactDigest:      "dd-1",
		RunStatus:           shared.ReviewRunCompleted,
		Verdict:             shared.VerdictPass,
		CriterionVerdicts:   []shared.CriterionVerdict{{CriterionID: "c1", Status: "pass"}},
		EvidenceToolCallIDs: []string{"toolu-rev-1"},
	}
}

// writeTranscript writes a small hand-written agent transcript (JSONL, the
// platform's agent_transcript_path shape) whose SubagentHandback tool_use
// blocks carry the given messages, in order.
func writeTranscript(t *testing.T, handbackMsgs ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(`{"type":"user","message":{"role":"user","content":"run the review envelope rev-x"}}` + "\n")
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

			resJSON, err := json.Marshal(passingResult("rev-1"))
			if err != nil {
				t.Fatal(err)
			}
			path := writeTranscript(t, wrap(string(resJSON)))

			code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", path))
			if code != 0 {
				t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
			}

			disk := reloadRun(t, "run-1")
			rv := reviewByID(t, disk, "rev-1")
			if rv.Status != shared.ReviewReviewed {
				t.Fatalf("review status = %s, want REVIEWED (backfilled from the transcript)", rv.Status)
			}
			if rv.Verdict != shared.VerdictPass || rv.ArtifactDigest != "dd-1" {
				t.Fatalf("review record = %+v, want verdict PASS bound to artifact dd-1", rv)
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
		})
	}

	// Crash window (spec §3): the result is recoverable from the transcript
	// while the review record is still PREPARED (dispatch persistence lost).
	t.Run("prepared review recovers too", func(t *testing.T) {
		useSubagentEnv(t)
		r := startRun(t)
		if _, err := r.RequestReview(reviewEnvelope("rev-1")); err != nil {
			t.Fatalf("RequestReview: %v", err)
		}
		resJSON, err := json.Marshal(passingResult("rev-1"))
		if err != nil {
			t.Fatal(err)
		}
		code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", writeTranscript(t, string(resJSON))))
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
		}
		if rv := reviewByID(t, reloadRun(t, "run-1"), "rev-1"); rv.Status != shared.ReviewReviewed {
			t.Fatalf("review status = %s, want REVIEWED (PREPARED is recoverable per §3 崩溃恢复)", rv.Status)
		}
	})
}

// TestSubagentStopDedupsByCallID: a LATE duplicate result (the review already
// concluded; a NEW generation is in flight) must not affect the new generation
// — RecordReviewResult refuses concluded reviews; the hook maps the refusal to
// a stderr note and exit 0, state untouched.
func TestSubagentStopDedupsByCallID(t *testing.T) {
	useSubagentEnv(t)
	r := startRun(t)
	startRunningReview(t, r, "rev-1")

	resJSON, err := json.Marshal(passingResult("rev-1"))
	if err != nil {
		t.Fatal(err)
	}
	path := writeTranscript(t, string(resJSON))
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

	resJSON, err := json.Marshal(passingResult("rev-1"))
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
	path2 := writeTranscript(t, string(resJSON))
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
	resJSON, err := json.Marshal(passingResult("rev-ghost"))
	if err != nil {
		t.Fatal(err)
	}

	code, stderr := callSubagentStop(t, subStopPayload(t, "reviewer", writeTranscript(t, string(resJSON))))
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
		resJSON, err := json.Marshal(passingResult("rev-1"))
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
