package main

// main_test.go — Task 4 TDD suite (PostToolUse 登记与 AskUserQuestion 裁决
// 记账 + Agent 遥测). Fixtures are built through the real reducer API
// (StartRun/OpenPhase/SubmitPhase/MarkDisplayed); hermeticity comes from the
// env overrides the shared package honors (SUMMONER_STATE_DIR /
// SUMMONER_TRACE_DIR / CLAUDE_PROJECT_DIR) — the same pattern as the gate's
// suite (go1.16: no t.Setenv).

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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

// useHookEnv redirects v2 state, traces and the project key into fresh temp
// dirs so no test ever touches real Summoner state.
func useHookEnv(t *testing.T) {
	t.Helper()
	setenv16(t, "SUMMONER_STATE_DIR", t.TempDir())
	setenv16(t, "SUMMONER_TRACE_DIR", t.TempDir())
	setenv16(t, "CLAUDE_PROJECT_DIR", t.TempDir())
	unsetenv16(t, "SUMMONER_HOOKS_DISABLE")
}

// callHook runs the whole hook on a payload; returns (exit code, stderr).
func callHook(t *testing.T, payload string) (int, string) {
	t.Helper()
	var stderr bytes.Buffer
	code := runHook(strings.NewReader(payload), &stderr)
	return code, stderr.String()
}

// --- fixtures -------------------------------------------------------------------

const hookSession = "sess-1"

// startRun starts live run "run-1" (fix workflow) in the test session.
func startRun(t *testing.T) *shared.RunState {
	t.Helper()
	r, err := shared.StartRun("run-1", hookSession, "fix", shared.BudgetLimits{})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	return r
}

// openDisplayedCheckpoint drives one full phase cycle up to a DISPLAYED
// checkpoint: the run sits in WAIT_USER with cp open, nonce drawn, display
// digest bound — the decision fixture (ruling: decisions need Displayed
// checkpoints via the real CheckpointContentDigest).
func openDisplayedCheckpoint(t *testing.T, r *shared.RunState, phaseID string) shared.CheckpointState {
	t.Helper()
	if err := r.OpenPhase(phaseID); err != nil {
		t.Fatalf("OpenPhase(%s): %v", phaseID, err)
	}
	cp, err := r.SubmitPhase(shared.PhaseSubmission{
		PhaseID:   phaseID,
		Summary:   phaseID + " done",
		Artifacts: []shared.ArtifactRef{{Path: phaseID + ".md", Digest: "d-" + phaseID, Kind: "doc"}},
	})
	if err != nil {
		t.Fatalf("SubmitPhase(%s): %v", phaseID, err)
	}
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed(%s): %v", cp.ID, err)
	}
	return cp
}

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(b)
}

// postUsePayload builds a PostToolUse payload.
func postUsePayload(t *testing.T, toolName, toolUseID string, toolInput map[string]interface{}, toolResponse interface{}) string {
	t.Helper()
	return mustJSON(t, map[string]interface{}{
		"hook_event_name": "PostToolUse",
		"session_id":      hookSession,
		"tool_name":       toolName,
		"tool_use_id":     toolUseID,
		"tool_input":      toolInput,
		"tool_response":   toolResponse,
	})
}

// postFailPayload builds a PostToolUseFailure payload; isInterrupt nil = omit
// the optional field.
func postFailPayload(t *testing.T, toolName, toolUseID, errMsg string, isInterrupt interface{}) string {
	t.Helper()
	m := map[string]interface{}{
		"hook_event_name": "PostToolUseFailure",
		"session_id":      hookSession,
		"tool_name":       toolName,
		"tool_use_id":     toolUseID,
		"tool_input":      map[string]interface{}{"command": "make"},
		"error":           errMsg,
	}
	if isInterrupt != nil {
		m["is_interrupt"] = isInterrupt
	}
	return mustJSON(t, m)
}

// cpQuestion builds a decision question object for a checkpoint nonce;
// returns the object and its full text (the answers map key).
func cpQuestion(nonce string, multi bool, labels ...string) (map[string]interface{}, string) {
	opts := make([]interface{}, 0, len(labels))
	for _, l := range labels {
		opts = append(opts, map[string]interface{}{"label": l, "description": "what this does"})
	}
	text := fmt.Sprintf("Checkpoint %s: how should the run proceed?", nonce)
	return map[string]interface{}{
		"question":    text,
		"header":      "Checkpoint",
		"options":     opts,
		"multiSelect": multi,
	}, text
}

// decisionLabels is the full decision menu (leading keywords per ruling 3).
var decisionLabels = []string{
	"Continue: accept and proceed",
	"Skip: mark UNVERIFIED",
	"Done: accept and finish",
	"Recall: redo an earlier phase",
	"Stop: end the run",
	"Review: independent review on demand",
	"Accept: accept the requirement change",
}

// answeredResponse builds the platform-injected tool_response for one chosen
// answer; extra keys (response, afkTimeoutMs…) are merged in.
func answeredResponse(qtext, answer string, extra map[string]interface{}) map[string]interface{} {
	resp := map[string]interface{}{
		"questions": []interface{}{map[string]interface{}{"question": qtext}},
		"answers":   map[string]interface{}{qtext: answer},
	}
	for k, v := range extra {
		resp[k] = v
	}
	return resp
}

// askDecision builds a full PostToolUse(AskUserQuestion) decision payload.
func askDecision(t *testing.T, toolUseID string, q map[string]interface{}, qtext, answer string, extra map[string]interface{}) string {
	t.Helper()
	return postUsePayload(t, "AskUserQuestion", toolUseID,
		map[string]interface{}{"questions": []interface{}{q}},
		answeredResponse(qtext, answer, extra))
}

// readHookTrace returns the run's trace events (nil when no file exists) and
// verifies the hash chain on every read.
func readHookTrace(t *testing.T, runID string) []shared.TraceEvent {
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

func reloadRun(t *testing.T, runID string) *shared.RunState {
	t.Helper()
	r, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatalf("LoadRun(%s): %v", runID, err)
	}
	return r
}

// checkpointByID finds one checkpoint by id in the loaded run.
func checkpointByID(t *testing.T, r *shared.RunState, id string) *shared.CheckpointState {
	t.Helper()
	for i := range r.Checkpoints {
		if r.Checkpoints[i].ID == id {
			return &r.Checkpoints[i]
		}
	}
	t.Fatalf("checkpoint %s not found in %+v", id, r.Checkpoints)
	return nil
}

// wantRejected asserts the trace carries exactly one decision_rejected event
// bound to cpID with reason containing reasonSub.
func wantRejected(t *testing.T, evs []shared.TraceEvent, cpID, reasonSub string) shared.TraceEvent {
	t.Helper()
	rej := eventsOfType(evs, shared.EventCheckpointDecisionRejected)
	if len(rej) != 1 {
		t.Fatalf("decision_rejected events = %d, want exactly 1 (trace: %v)", len(rej), traceTypesOf(evs))
	}
	ev := rej[0]
	if ev.Payload["checkpoint_id"] != cpID {
		t.Errorf("decision_rejected checkpoint_id = %v, want %s", ev.Payload["checkpoint_id"], cpID)
	}
	reason, _ := ev.Payload["reason"].(string)
	if !strings.Contains(reason, reasonSub) {
		t.Errorf("decision_rejected reason %q must contain %q", reason, reasonSub)
	}
	return ev
}

// --- decision channel ------------------------------------------------------------

// TestDecisionRecordedOnValidResponse: a platform-shape response whose answer
// exactly matches an offered label is recorded via RecordDecision and traced
// as checkpoint.decided carrying the checkpoint_id + AskUserQuestion
// tool_use_id binding (spec §7).
func TestDecisionRecordedOnValidResponse(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := openDisplayedCheckpoint(t, r, "diagnosis")

	q, qtext := cpQuestion(cp.Nonce, false, decisionLabels...)
	code, stderr := callHook(t, askDecision(t, "toolu-dec-1", q, qtext, "Continue: accept and proceed", nil))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}

	disk := reloadRun(t, "run-1")
	got := checkpointByID(t, disk, cp.ID)
	if !got.Decided || got.Decision != shared.DecisionContinue || got.DecisionToolUseID != "toolu-dec-1" {
		t.Fatalf("checkpoint after decision = %+v, want decided CONTINUE bound to toolu-dec-1", got)
	}
	if disk.Status != shared.StatusActive {
		t.Fatalf("status = %s, want ACTIVE (CONTINUE resumes work)", disk.Status)
	}
	evs := readHookTrace(t, "run-1")
	decided := eventsOfType(evs, shared.EventCheckpointDecided)
	if len(decided) != 1 {
		t.Fatalf("checkpoint.decided events = %d, want exactly 1 (trace: %v)", len(decided), traceTypesOf(evs))
	}
	ev := decided[0]
	if ev.ToolUseID != "toolu-dec-1" {
		t.Errorf("checkpoint.decided tool_use_id = %q, want toolu-dec-1", ev.ToolUseID)
	}
	if ev.Source != "posttooluse" {
		t.Errorf("checkpoint.decided source = %q, want posttooluse", ev.Source)
	}
	if ev.Payload["checkpoint_id"] != cp.ID || ev.Payload["tool_use_id"] != "toolu-dec-1" {
		t.Errorf("checkpoint.decided payload = %v, want checkpoint_id %s + the AskUserQuestion tool_use_id", ev.Payload, cp.ID)
	}
	if n := len(eventsOfType(evs, shared.EventCheckpointDecisionRejected)); n != 0 {
		t.Errorf("valid decision must leave no decision_rejected events, got %d", n)
	}
}

// TestDecisionRejectedWrongCheckpoint: the question carries an earlier (already
// decided) checkpoint's nonce instead of the current one — rejected against the
// CURRENT checkpoint, nothing decided.
func TestDecisionRejectedWrongCheckpoint(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp1 := openDisplayedCheckpoint(t, r, "context")
	if err := r.RecordDecision(cp1.ID, shared.DecisionContinue, ""); err != nil {
		t.Fatalf("decide cp1: %v", err)
	}
	cp2 := openDisplayedCheckpoint(t, r, "diagnosis")

	q, qtext := cpQuestion(cp1.Nonce, false, decisionLabels...)
	code, stderr := callHook(t, askDecision(t, "toolu-wrong", q, qtext, "Continue: accept and proceed", nil))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 — a rejection is not a hook error (stderr: %q)", code, stderr)
	}

	disk := reloadRun(t, "run-1")
	if got := checkpointByID(t, disk, cp2.ID); got.Decided || got.Decision != "" {
		t.Fatalf("current checkpoint must stay undecided, got %+v", got)
	}
	if disk.Status != shared.StatusWaitUser {
		t.Fatalf("status = %s, want WAIT_USER (unchanged)", disk.Status)
	}
	evs := readHookTrace(t, "run-1")
	wantRejected(t, evs, cp2.ID, "not the current checkpoint")
	if n := len(eventsOfType(evs, shared.EventCheckpointDecided)); n != 0 {
		t.Fatalf("checkpoint.decided events = %d, want 0", n)
	}
}

// TestDecisionRejectedNonceMissing: WAIT_USER question carrying no nonce at
// all cannot bind to the checkpoint — rejected, nothing decided.
func TestDecisionRejectedNonceMissing(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := openDisplayedCheckpoint(t, r, "diagnosis")

	q, qtext := cpQuestion("", false, decisionLabels...) // no nonce anywhere
	code, stderr := callHook(t, askDecision(t, "toolu-nononce", q, qtext, "Continue: accept and proceed", nil))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	disk := reloadRun(t, "run-1")
	if got := checkpointByID(t, disk, cp.ID); got.Decided {
		t.Fatalf("nonce-less question must decide nothing, got %+v", got)
	}
	wantRejected(t, readHookTrace(t, "run-1"), cp.ID, "nonce")
}

// TestDecisionRejectedNonPlatformShape: tool_response that is not the injected
// answer object (a bare string) is not a decision.
func TestDecisionRejectedNonPlatformShape(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := openDisplayedCheckpoint(t, r, "diagnosis")

	q, _ := cpQuestion(cp.Nonce, false, decisionLabels...)
	payload := postUsePayload(t, "AskUserQuestion", "toolu-shape",
		map[string]interface{}{"questions": []interface{}{q}},
		"the user seemed to say yes") // not the platform answer object
	code, stderr := callHook(t, payload)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	disk := reloadRun(t, "run-1")
	if got := checkpointByID(t, disk, cp.ID); got.Decided {
		t.Fatalf("non-platform shape must decide nothing, got %+v", got)
	}
	wantRejected(t, readHookTrace(t, "run-1"), cp.ID, "not the platform")
}

// TestDecisionRejectedFreeformResponse: a typed free-form response is content
// feedback, never advancement (spec §4).
func TestDecisionRejectedFreeformResponse(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := openDisplayedCheckpoint(t, r, "diagnosis")

	q, qtext := cpQuestion(cp.Nonce, false, decisionLabels...)
	code, stderr := callHook(t, askDecision(t, "toolu-free", q, qtext, "Continue: accept and proceed",
		map[string]interface{}{"response": "just keep going, but check tests again"}))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	disk := reloadRun(t, "run-1")
	if got := checkpointByID(t, disk, cp.ID); got.Decided {
		t.Fatalf("free-form response must decide nothing, got %+v", got)
	}
	wantRejected(t, readHookTrace(t, "run-1"), cp.ID, "free-form")
}

// TestDecisionRejectedAfkTimeout: afkTimeoutMs present means the dialog
// auto-resolved — not a human decision (spec §1.5 injection/timeout guard,
// runtime half).
func TestDecisionRejectedAfkTimeout(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := openDisplayedCheckpoint(t, r, "diagnosis")

	q, qtext := cpQuestion(cp.Nonce, false, decisionLabels...)
	code, stderr := callHook(t, askDecision(t, "toolu-afk", q, qtext, "Continue: accept and proceed",
		map[string]interface{}{"afkTimeoutMs": 300000}))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	disk := reloadRun(t, "run-1")
	if got := checkpointByID(t, disk, cp.ID); got.Decided {
		t.Fatalf("auto-resolved dialog must decide nothing, got %+v", got)
	}
	wantRejected(t, readHookTrace(t, "run-1"), cp.ID, "afk")
}

// TestDecisionRejectedMultiSelect: a comma-joined multi-select answer on the
// decision question is not one decision.
func TestDecisionRejectedMultiSelect(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := openDisplayedCheckpoint(t, r, "diagnosis")

	q, qtext := cpQuestion(cp.Nonce, true, "Continue: accept and proceed", "Skip: mark UNVERIFIED")
	answer := "Continue: accept and proceed, Skip: mark UNVERIFIED"
	code, stderr := callHook(t, askDecision(t, "toolu-multi", q, qtext, answer, nil))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	disk := reloadRun(t, "run-1")
	if got := checkpointByID(t, disk, cp.ID); got.Decided {
		t.Fatalf("multi-select answer must decide nothing, got %+v", got)
	}
	wantRejected(t, readHookTrace(t, "run-1"), cp.ID, "multi-select")
}

// TestDecisionRejectedUnknownLabel: an answer that matches no offered label,
// and a label whose leading keyword maps to no decision, are both rejected.
func TestDecisionRejectedUnknownLabel(t *testing.T) {
	t.Run("answer not offered", func(t *testing.T) {
		useHookEnv(t)
		r := startRun(t)
		cp := openDisplayedCheckpoint(t, r, "diagnosis")
		q, qtext := cpQuestion(cp.Nonce, false, decisionLabels...)
		code, stderr := callHook(t, askDecision(t, "toolu-unk", q, qtext, "Maybe", nil))
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
		}
		if got := checkpointByID(t, reloadRun(t, "run-1"), cp.ID); got.Decided {
			t.Fatal("unmatched answer must decide nothing")
		}
		wantRejected(t, readHookTrace(t, "run-1"), cp.ID, "not an offered option label")
	})
	t.Run("keyword unmapped", func(t *testing.T) {
		useHookEnv(t)
		r := startRun(t)
		cp := openDisplayedCheckpoint(t, r, "diagnosis")
		q, qtext := cpQuestion(cp.Nonce, false, "Proceed with confidence")
		code, stderr := callHook(t, askDecision(t, "toolu-kw", q, qtext, "Proceed with confidence", nil))
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
		}
		if got := checkpointByID(t, reloadRun(t, "run-1"), cp.ID); got.Decided {
			t.Fatal("unmapped keyword must decide nothing")
		}
		wantRejected(t, readHookTrace(t, "run-1"), cp.ID, "maps to no decision")
	})
}

// TestDecisionRejectedIllegalForKind: a valid platform answer that is illegal
// for the checkpoint kind (CONTINUE at a change-confirm checkpoint) surfaces
// the reducer's error as a rejection — kind guards live in the reducer, the
// hook only maps the error.
func TestDecisionRejectedIllegalForKind(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	openDisplayedCheckpoint(t, r, "diagnosis")

	// Enter the change loop from WAIT_USER: cp-1 is superseded, a
	// change-confirm checkpoint opens after the delta.
	if err := r.OpenRequirementChange("switch export format"); err != nil {
		t.Fatalf("OpenRequirementChange: %v", err)
	}
	if _, err := r.ApplyRequirementDelta(shared.RequirementDelta{Note: "csv", Changed: []string{"c1"}}); err != nil {
		t.Fatalf("ApplyRequirementDelta: %v", err)
	}
	confirm := r.CurrentCheckpoint()
	if confirm == nil || confirm.Kind != shared.CheckpointChangeConfirm {
		t.Fatalf("want an open change-confirm checkpoint, got %+v", confirm)
	}
	if err := r.MarkDisplayed(confirm.ID, shared.CheckpointContentDigest(*confirm, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed(%s): %v", confirm.ID, err)
	}

	q, qtext := cpQuestion(confirm.Nonce, false, decisionLabels...)
	code, stderr := callHook(t, askDecision(t, "toolu-kind", q, qtext, "Continue: accept and proceed", nil))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	disk := reloadRun(t, "run-1")
	if got := checkpointByID(t, disk, confirm.ID); got.Decided {
		t.Fatalf("illegal-for-kind decision must not be recorded, got %+v", got)
	}
	if disk.Status != shared.StatusWaitUser {
		t.Fatalf("status = %s, want WAIT_USER (change still open)", disk.Status)
	}
	wantRejected(t, readHookTrace(t, "run-1"), confirm.ID, "change-confirm")
}

// TestDecisionIdempotent: the same AskUserQuestion callback delivered twice
// records the decision exactly once — the replay neither re-decides nor
// re-traces checkpoint.decided.
func TestDecisionIdempotent(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := openDisplayedCheckpoint(t, r, "diagnosis")

	q, qtext := cpQuestion(cp.Nonce, false, decisionLabels...)
	payload := askDecision(t, "toolu-replay", q, qtext, "Continue: accept and proceed", nil)

	if code, stderr := callHook(t, payload); code != 0 {
		t.Fatalf("first callback: exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if code, stderr := callHook(t, payload); code != 0 {
		t.Fatalf("replayed callback: exit = %d, want 0 (stderr: %q)", code, stderr)
	}

	disk := reloadRun(t, "run-1")
	got := checkpointByID(t, disk, cp.ID)
	if !got.Decided || got.Decision != shared.DecisionContinue || got.DecisionToolUseID != "toolu-replay" {
		t.Fatalf("checkpoint after replay = %+v, want exactly one CONTINUE bound to toolu-replay", got)
	}
	if disk.Status != shared.StatusActive {
		t.Fatalf("status = %s, want ACTIVE (unchanged by the replay)", disk.Status)
	}
	evs := readHookTrace(t, "run-1")
	if n := len(eventsOfType(evs, shared.EventCheckpointDecided)); n != 1 {
		t.Fatalf("checkpoint.decided events = %d, want exactly 1 (trace: %v)", n, traceTypesOf(evs))
	}
	if n := len(eventsOfType(evs, shared.EventCheckpointDecisionRejected)); n != 0 {
		t.Fatalf("replay must not be a rejection, got %d decision_rejected events", n)
	}
	// Outside WAIT_USER the replay is an ordinary completion observation.
	if n := len(eventsOfType(evs, shared.EventToolResult)); n != 1 {
		t.Fatalf("tool.result events = %d, want 1 (the replay as ordinary Q&A)", n)
	}
}

// --- ordinary tool accounting ------------------------------------------------------

// TestToolResultDigestEmitted: a non-decision tool completion traces tool.result
// with the sha256 of the canonical JSON of tool_response (big evidence stays
// local per §7).
func TestToolResultDigestEmitted(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	openDisplayedCheckpoint(t, r, "diagnosis")
	if err := r.RecordDecision(r.CurrentCheckpoint().ID, shared.DecisionContinue, ""); err != nil {
		t.Fatalf("decide: %v", err)
	}

	resp := map[string]interface{}{"file_path": "/tmp/x.go", "content": "hello", "lines": 7}
	code, stderr := callHook(t, postUsePayload(t, "Read", "toolu-read-1",
		map[string]interface{}{"file_path": "/tmp/x.go"}, resp))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	want, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(want)
	evs := readHookTrace(t, "run-1")
	results := eventsOfType(evs, shared.EventToolResult)
	if len(results) != 1 {
		t.Fatalf("tool.result events = %d, want 1 (trace: %v)", len(results), traceTypesOf(evs))
	}
	ev := results[0]
	if ev.ToolUseID != "toolu-read-1" {
		t.Errorf("tool.result tool_use_id = %q, want toolu-read-1", ev.ToolUseID)
	}
	if ev.Payload["tool_name"] != "Read" {
		t.Errorf("tool.result tool_name = %v, want Read", ev.Payload["tool_name"])
	}
	if ev.Payload["response_sha256"] != hex.EncodeToString(sum[:]) {
		t.Errorf("tool.result response_sha256 = %v, want %s", ev.Payload["response_sha256"], hex.EncodeToString(sum[:]))
	}
}

// TestToolFailedEmitted: PostToolUseFailure traces tool.failed with the first
// error line and the interrupt flag; is_interrupt absent reads as false.
func TestToolFailedEmitted(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	openDisplayedCheckpoint(t, r, "diagnosis")
	if err := r.RecordDecision(r.CurrentCheckpoint().ID, shared.DecisionContinue, ""); err != nil {
		t.Fatalf("decide: %v", err)
	}

	code, stderr := callHook(t, postFailPayload(t, "Bash", "toolu-fail-1",
		"exit status 1\nmake: *** [build] Error 1", true))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	evs := readHookTrace(t, "run-1")
	failed := eventsOfType(evs, shared.EventToolFailed)
	if len(failed) != 1 {
		t.Fatalf("tool.failed events = %d, want 1 (trace: %v)", len(failed), traceTypesOf(evs))
	}
	ev := failed[0]
	if ev.ToolUseID != "toolu-fail-1" {
		t.Errorf("tool.failed tool_use_id = %q, want toolu-fail-1", ev.ToolUseID)
	}
	if ev.Payload["tool_name"] != "Bash" || ev.Payload["error_first_line"] != "exit status 1" {
		t.Errorf("tool.failed payload = %v, want Bash / first error line", ev.Payload)
	}
	if isInt, _ := ev.Payload["is_interrupt"].(bool); !isInt {
		t.Errorf("tool.failed is_interrupt = %v, want true", ev.Payload["is_interrupt"])
	}

	// Without the optional flag the failure is recorded as non-interrupt.
	code, stderr = callHook(t, postFailPayload(t, "Bash", "toolu-fail-2", "boom", nil))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	evs = readHookTrace(t, "run-1")
	failed = eventsOfType(evs, shared.EventToolFailed)
	if len(failed) != 2 {
		t.Fatalf("tool.failed events = %d, want 2", len(failed))
	}
	if isInt, _ := failed[1].Payload["is_interrupt"].(bool); isInt {
		t.Errorf("absent is_interrupt must read as false, got %v", failed[1].Payload["is_interrupt"])
	}
}

// TestAgentTelemetryEmitted: an Agent completion traces tool.result plus
// agent.telemetry with the declared fields (final_request_tokens mirrors
// totalTokens — LAST-API-REQUEST-ONLY 口径 — and total_tool_use_count mirrors
// totalToolUseCount); absent usage fields are tolerated (omitted, never
// fabricated).
func TestAgentTelemetryEmitted(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	openDisplayedCheckpoint(t, r, "diagnosis")
	if err := r.RecordDecision(r.CurrentCheckpoint().ID, shared.DecisionContinue, ""); err != nil {
		t.Fatalf("decide: %v", err)
	}

	code, stderr := callHook(t, postUsePayload(t, "Agent", "toolu-ag-1",
		map[string]interface{}{"subagent_type": "worker", "prompt": "do work"},
		map[string]interface{}{"totalTokens": 5432, "totalToolUseCount": 12, "result": "done"}))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	evs := readHookTrace(t, "run-1")
	tele := eventsOfType(evs, shared.EventAgentTelemetry)
	if len(tele) != 1 {
		t.Fatalf("agent.telemetry events = %d, want 1 (trace: %v)", len(tele), traceTypesOf(evs))
	}
	ev := tele[0]
	if ev.ToolUseID != "toolu-ag-1" {
		t.Errorf("agent.telemetry tool_use_id = %q, want toolu-ag-1", ev.ToolUseID)
	}
	if got := ev.Payload["final_request_tokens"]; got != float64(5432) {
		t.Errorf("final_request_tokens = %v (%T), want 5432", got, got)
	}
	if got := ev.Payload["total_tool_use_count"]; got != float64(12) {
		t.Errorf("total_tool_use_count = %v (%T), want 12", got, got)
	}
	if n := len(eventsOfType(evs, shared.EventToolResult)); n != 1 {
		t.Errorf("Agent completion must also trace tool.result, got %d", n)
	}

	// Absent usage fields: the telemetry event still fires, without fabricated
	// values.
	code, stderr = callHook(t, postUsePayload(t, "Agent", "toolu-ag-2",
		map[string]interface{}{"subagent_type": "worker", "prompt": "more work"},
		map[string]interface{}{}))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	evs = readHookTrace(t, "run-1")
	tele = eventsOfType(evs, shared.EventAgentTelemetry)
	if len(tele) != 2 {
		t.Fatalf("agent.telemetry events = %d, want 2", len(tele))
	}
	if len(tele[1].Payload) != 0 {
		t.Errorf("absent usage fields must be omitted, got payload %v", tele[1].Payload)
	}
}

// --- run resolution & escape hatch --------------------------------------------------

// TestEscapeHatchPassThrough: SUMMONER_HOOKS_DISABLE=1 enforces nothing — no
// decision is recorded — but on exactly one live run it first records the
// contract violation (hooks.disabled); with no live run there are no events.
func TestEscapeHatchPassThrough(t *testing.T) {
	t.Run("live run traces the violation", func(t *testing.T) {
		useHookEnv(t)
		r := startRun(t)
		cp := openDisplayedCheckpoint(t, r, "diagnosis")
		setenv16(t, "SUMMONER_HOOKS_DISABLE", "1")

		q, qtext := cpQuestion(cp.Nonce, false, decisionLabels...)
		code, stderr := callHook(t, askDecision(t, "toolu-hatch", q, qtext, "Continue: accept and proceed", nil))
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 — the hatch enforces nothing (stderr: %q)", code, stderr)
		}
		disk := reloadRun(t, "run-1")
		if got := checkpointByID(t, disk, cp.ID); got.Decided {
			t.Fatalf("the hatch must not record decisions, got %+v", got)
		}
		evs := readHookTrace(t, "run-1")
		if n := len(eventsOfType(evs, shared.EventHooksDisabled)); n != 1 {
			t.Fatalf("hooks.disabled events = %d, want exactly 1 (trace: %v)", n, traceTypesOf(evs))
		}
		if n := len(eventsOfType(evs, shared.EventCheckpointDecided)); n != 0 {
			t.Fatalf("checkpoint.decided under the hatch = %d, want 0", n)
		}
	})
	t.Run("no live run stays silent", func(t *testing.T) {
		useHookEnv(t)
		setenv16(t, "SUMMONER_HOOKS_DISABLE", "1")
		code, stderr := callHook(t, postUsePayload(t, "Read", "toolu-hatch-2",
			map[string]interface{}{"file_path": "/tmp/x.go"}, map[string]interface{}{"content": "x"}))
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
		}
		if evs := readHookTrace(t, "run-1"); len(evs) != 0 {
			t.Fatalf("no live run → no events, got %v", traceTypesOf(evs))
		}
	})
}

// TestNoRunPassThrough: zero non-CLOSED runs is not our business — exit 0, no
// events, no state change.
func TestNoRunPassThrough(t *testing.T) {
	useHookEnv(t)
	code, stderr := callHook(t, postUsePayload(t, "Read", "toolu-none",
		map[string]interface{}{"file_path": "/tmp/x.go"}, map[string]interface{}{"content": "x"}))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	if evs := readHookTrace(t, "run-1"); len(evs) != 0 {
		t.Fatalf("no-run session must produce no events, got %v", traceTypesOf(evs))
	}
}

// TestAmbiguousRunsFailClosed: more than one live run is ambiguous — the hook
// refuses to guess which run to record against (exit 2).
func TestAmbiguousRunsFailClosed(t *testing.T) {
	useHookEnv(t)
	if _, err := shared.StartRun("run-1", hookSession, "fix", shared.BudgetLimits{}); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.StartRun("run-2", hookSession, "fix", shared.BudgetLimits{}); err != nil {
		t.Fatal(err)
	}
	code, stderr := callHook(t, postUsePayload(t, "Read", "toolu-amb",
		map[string]interface{}{"file_path": "/tmp/x.go"}, map[string]interface{}{"content": "x"}))
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %q)", code, stderr)
	}
	if !strings.Contains(stderr, "live") {
		t.Fatalf("stderr %q must name the ambiguous live runs", stderr)
	}
}

// TestParseFailureFailsClosed: unparseable input, trailing garbage and missing
// identity fields are fail-closed exit 2.
func TestParseFailureFailsClosed(t *testing.T) {
	useHookEnv(t)
	startRun(t)
	for name, payload := range map[string]string{
		"not json":       "this is not json",
		"trailing bytes": `{"hook_event_name":"PostToolUse"} trailing`,
		"no tool_name":   mustJSON(t, map[string]interface{}{"hook_event_name": "PostToolUse", "session_id": hookSession, "tool_use_id": "tu-1"}),
		"no tool_use_id": mustJSON(t, map[string]interface{}{"hook_event_name": "PostToolUse", "session_id": hookSession, "tool_name": "Read"}),
		"unknown event":  mustJSON(t, map[string]interface{}{"hook_event_name": "Nonsense", "session_id": hookSession, "tool_name": "Read", "tool_use_id": "tu-1"}),
		// Task 6 (C1): empty hook_event_name is a miswire now that
		// hooks.json wires BOTH events explicitly — fail closed.
		"no event name": mustJSON(t, map[string]interface{}{"session_id": hookSession, "tool_name": "Read", "tool_use_id": "tu-1"}),
	} {
		code, stderr := callHook(t, payload)
		if code != 2 {
			t.Errorf("%s: exit code = %d, want 2 (stderr: %q)", name, code, stderr)
		}
	}
}

// TestPostToolUseFailureEmptyErrorFailsClosed (task 6 / C2): a
// PostToolUseFailure payload with an EMPTY error string is malformed for a
// KNOWN event — same shape-validation principle the gate applies. Fail-closed
// exit 2, nothing recorded.
func TestPostToolUseFailureEmptyErrorFailsClosed(t *testing.T) {
	useHookEnv(t)
	startRun(t)
	payload := mustJSON(t, map[string]interface{}{
		"hook_event_name": "PostToolUseFailure",
		"session_id":      hookSession,
		"tool_name":       "Bash",
		"tool_use_id":     "tu-noerr",
		"tool_input":      map[string]interface{}{"command": "make"},
		"error":           "",
	})
	code, stderr := callHook(t, payload)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %q)", code, stderr)
	}
	if evs := readHookTrace(t, "run-1"); len(evs) != 0 {
		t.Fatalf("refused payload must record nothing, got %v", traceTypesOf(evs))
	}
}
