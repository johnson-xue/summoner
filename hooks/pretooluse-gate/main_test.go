package main

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

// main_test.go — fixture-driven gate tests (Task 3 brief step 1 + controller
// rulings). All fixtures are built through the real reducer API; hermeticity
// comes from the env overrides the shared package already honors
// (SUMMONER_STATE_DIR / SUMMONER_TRACE_DIR / CLAUDE_PROJECT_DIR).

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

// useGateEnv redirects v2 state, traces and the project key into fresh temp
// dirs so no test ever touches real Summoner state.
func useGateEnv(t *testing.T) {
	t.Helper()
	setenv16(t, "SUMMONER_STATE_DIR", t.TempDir())
	setenv16(t, "SUMMONER_TRACE_DIR", t.TempDir())
	setenv16(t, "CLAUDE_PROJECT_DIR", t.TempDir())
	unsetenv16(t, "SUMMONER_HOOKS_DISABLE")
}

// callGate runs the whole hook on a payload; returns (exit code, stderr).
func callGate(t *testing.T, payload string) (int, string) {
	t.Helper()
	var stderr bytes.Buffer
	code := runGate(strings.NewReader(payload), &stderr)
	return code, stderr.String()
}

// --- fixtures -------------------------------------------------------------------

const gateSession = "sess-1"

// startRunWF starts run "run-1" for an arbitrary workflow in this test's session.
func startRunWF(t *testing.T, workflow string, limits shared.BudgetLimits) *shared.RunState {
	t.Helper()
	r, err := shared.StartRun("run-1", gateSession, workflow, limits)
	if err != nil {
		t.Fatalf("StartRun(%s): %v", workflow, err)
	}
	return r
}

// startFixRun starts run "run-1" for the fix workflow in this test's session.
func startFixRun(t *testing.T, limits shared.BudgetLimits) *shared.RunState {
	t.Helper()
	return startRunWF(t, "fix", limits)
}

// acceptPhase drives one full phase cycle: open → submit → display → CONTINUE.
func acceptPhase(t *testing.T, r *shared.RunState, phaseID string) {
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
	if err := r.RecordDecision(cp.ID, shared.DecisionContinue); err != nil {
		t.Fatalf("RecordDecision(%s): %v", cp.ID, err)
	}
}

// mustJSONStr marshals v (payloads are built with it, so no string-escaping bugs).
func mustJSONStr(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(b)
}

func toolPayload(t *testing.T, toolName string, toolInput map[string]interface{}) string {
	t.Helper()
	return mustJSONStr(t, map[string]interface{}{
		"session_id":  gateSession,
		"tool_name":   toolName,
		"tool_use_id": "tu-" + toolName,
		"tool_input":  toolInput,
	})
}

func editInput() map[string]interface{} {
	return map[string]interface{}{
		"file_path": "/tmp/x.go", "old_string": "a", "new_string": "b",
	}
}

func writeInput() map[string]interface{} {
	return map[string]interface{}{"file_path": "/tmp/x.go", "content": "package x\n"}
}

func notebookInput() map[string]interface{} {
	return map[string]interface{}{"notebook_path": "/tmp/nb.ipynb", "new_source": "print(1)"}
}

func readInput() map[string]interface{} {
	return map[string]interface{}{"file_path": "/tmp/x.go"}
}

func bashInput(cmd string) map[string]interface{} {
	return map[string]interface{}{"command": cmd}
}

func skillInput(skill, args string) map[string]interface{} {
	m := map[string]interface{}{"skill": skill}
	if args != "" {
		m["args"] = args
	}
	return m
}

func agentInput(subagentType, prompt string) map[string]interface{} {
	return map[string]interface{}{"subagent_type": subagentType, "prompt": prompt, "description": "d"}
}

func askInput(questionTexts ...string) map[string]interface{} {
	qs := make([]interface{}, 0, len(questionTexts))
	for _, q := range questionTexts {
		qs = append(qs, map[string]interface{}{
			"question":    q,
			"header":      "Checkpoint",
			"options":     []interface{}{map[string]interface{}{"label": "CONTINUE"}},
			"multiSelect": false,
		})
	}
	return map[string]interface{}{"questions": qs}
}

func submitArgs(t *testing.T, phaseID string) string {
	t.Helper()
	return mustJSONStr(t, shared.PhaseSubmission{PhaseID: phaseID, Summary: phaseID + " done"})
}

// readGateTrace returns the run's trace events (nil when no file exists) and
// verifies the hash chain on every read.
func readGateTrace(t *testing.T, runID string) []shared.TraceEvent {
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

func traceTypes(evs []shared.TraceEvent) []string {
	types := make([]string, 0, len(evs))
	for _, ev := range evs {
		types = append(types, ev.Type)
	}
	return types
}

func wantExit2(t *testing.T, code int, stderr, wantSub string) {
	t.Helper()
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %q)", code, stderr)
	}
	if wantSub != "" && !strings.Contains(stderr, wantSub) {
		t.Fatalf("stderr %q must mention %q", stderr, wantSub)
	}
}

func reloadRun(t *testing.T, runID string) *shared.RunState {
	t.Helper()
	r, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatalf("LoadRun(%s): %v", runID, err)
	}
	return r
}

// --- write-eligibility matrix (ruling 4) -----------------------------------------

func TestWriteGateBeforeDiagnosisAccepted(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r, "context") // context accepted; diagnosis NOT yet

	code, stderr := callGate(t, toolPayload(t, "Edit", editInput()))
	wantExit2(t, code, stderr, "diagnosis")

	code, stderr = callGate(t, toolPayload(t, "Bash", bashInput("rm -rf /tmp/x")))
	wantExit2(t, code, stderr, "diagnosis")

	// Reads stay open — the iron law blocks writes only.
	code, _ = callGate(t, toolPayload(t, "Read", readInput()))
	if code != 0 {
		t.Fatalf("Read while diagnosis unaccepted: exit %d, want 0", code)
	}
}

func TestWriteGateBlockedWhileWaitUser(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r, "context")
	acceptPhase(t, r, "diagnosis")
	if err := r.OpenPhase("reproduce"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	if _, err := r.SubmitPhase(shared.PhaseSubmission{PhaseID: "reproduce"}); err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	} // WAIT_USER

	code, stderr := callGate(t, toolPayload(t, "Edit", editInput()))
	wantExit2(t, code, stderr, "WAIT_USER")

	code, stderr = callGate(t, toolPayload(t, "Write", writeInput()))
	wantExit2(t, code, stderr, "WAIT_USER")

	code, stderr = callGate(t, toolPayload(t, "Bash", bashInput("echo hi > f.txt")))
	wantExit2(t, code, stderr, "WAIT_USER")
}

func TestWriteGateBlockedWhileReviewPendingOrOutcomeUnknown(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r, "context")
	acceptPhase(t, r, "diagnosis")
	env := shared.Envelope{
		SchemaVersion: "2",
		RunID:         "run-1",
		ReviewID:      "rev-1",
		ArtifactSet:   []shared.ArtifactRef{{Path: "reproduce.md", Digest: "ddd"}},
	}
	if _, err := r.RequestReview(env); err != nil {
		t.Fatalf("RequestReview: %v", err)
	} // REVIEW_PENDING
	code, stderr := callGate(t, toolPayload(t, "Edit", editInput()))
	wantExit2(t, code, stderr, "REVIEW_PENDING")

	// OUTCOME_UNKNOWN: an unpersisted tool side effect freezes writes.
	useGateEnv(t)
	r2 := startFixRun(t, shared.BudgetLimits{})
	r2.MarkOutcomeUnknown("tu-crash")
	if err := r2.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	code, stderr = callGate(t, toolPayload(t, "Edit", editInput()))
	wantExit2(t, code, stderr, "OUTCOME_UNKNOWN")
}

func TestWriteGateAllowedAfterDiagnosisAccepted(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r, "context")
	acceptPhase(t, r, "diagnosis")
	if err := r.OpenPhase("reproduce"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}

	code, stderr := callGate(t, toolPayload(t, "Edit", editInput()))
	if code != 0 {
		t.Fatalf("Edit after diagnosis accepted: exit %d, want 0 (stderr %q)", code, stderr)
	}
	evs := readGateTrace(t, "run-1")
	if got := traceTypes(evs); len(got) != 1 || got[0] != shared.EventToolStarted {
		t.Fatalf("trace types = %v, want [tool.started]", got)
	}
	if evs[0].Payload["tool_name"] != "Edit" {
		t.Fatalf("tool.started payload = %v, want tool_name Edit", evs[0].Payload)
	}
	if reloadRun(t, "run-1").Counters.PhaseToolCalls != 1 {
		t.Fatal("allowed call must CountToolCall")
	}

	// POST_GAME_PENDING allows writes: journal persistence precedes CLOSED.
	useGateEnv(t)
	r2 := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r2, "context")
	acceptPhase(t, r2, "diagnosis")
	if err := r2.OpenPhase("reproduce"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	cp, err := r2.SubmitPhase(shared.PhaseSubmission{PhaseID: "reproduce"})
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	if err := r2.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(cp, r2.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	if err := r2.RecordDecision(cp.ID, shared.DecisionDone); err != nil {
		t.Fatalf("RecordDecision(DONE): %v", err)
	}
	if code, _ = callGate(t, toolPayload(t, "Edit", editInput())); code != 0 {
		t.Fatalf("Edit in POST_GAME_PENDING: exit %d, want 0", code)
	}

	// Recall re-blocks writes when it reopens diagnosis (ruling 4): recall
	// targets the most recent accepted phase (or the checkpoint's own phase
	// when none was accepted), so skip context and recall from the diagnosis
	// checkpoint itself — diagnosis's accepted outcome is cleared.
	useGateEnv(t)
	r3 := startFixRun(t, shared.BudgetLimits{})
	if err := r3.OpenPhase("context"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	cpC, err := r3.SubmitPhase(shared.PhaseSubmission{PhaseID: "context"})
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	if err := r3.MarkDisplayed(cpC.ID, shared.CheckpointContentDigest(cpC, r3.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	if err := r3.RecordDecision(cpC.ID, shared.DecisionSkip); err != nil {
		t.Fatalf("RecordDecision(SKIP): %v", err)
	}
	if err := r3.OpenPhase("diagnosis"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	cpD, err := r3.SubmitPhase(shared.PhaseSubmission{PhaseID: "diagnosis"})
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	if err := r3.MarkDisplayed(cpD.ID, shared.CheckpointContentDigest(cpD, r3.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	if err := r3.RecordDecision(cpD.ID, shared.DecisionRecall); err != nil {
		t.Fatalf("RecordDecision(RECALL): %v", err)
	}
	code, stderr = callGate(t, toolPayload(t, "Edit", editInput()))
	wantExit2(t, code, stderr, "diagnosis")
}

// --- phase-submit (ruling 6) ------------------------------------------------------

func TestPhaseSubmitRejectedBadPayload(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	if err := r.OpenPhase("context"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}

	cases := []struct {
		name string
		args interface{}
		want string
	}{
		{"args not JSON", "not-json{", "not valid JSON"},
		{"args absent", nil, "args"},
		{"args not a string", map[string]interface{}{"phase_id": "context"}, "args"},
		{"missing phase_id", `{"summary":"x"}`, "phase_id"},
	}
	for _, tc := range cases {
		input := map[string]interface{}{"skill": "summoner:phase-submit"}
		if tc.args != nil {
			input["args"] = tc.args
		}
		code, stderr := callGate(t, toolPayload(t, "Skill", input))
		wantExit2(t, code, stderr, tc.want)
	}

	// I8: EVERY submission-path failure during an active run traces
	// phase_submitted.rejected — parse failures included — with no
	// tool.started and no CountToolCall.
	evs := readGateTrace(t, "run-1")
	if got := traceTypes(evs); len(got) != len(cases) {
		t.Fatalf("trace types = %v, want %d phase_submitted.rejected events", got, len(cases))
	}
	for i, ev := range evs {
		if ev.Type != shared.EventPhaseSubmittedRejected {
			t.Fatalf("event %d = %s, want phase_submitted.rejected", i, ev.Type)
		}
		if ev.Payload["reason"] == nil || ev.Payload["reason"] == "" {
			t.Fatalf("rejection %d must carry a reason payload, got %v", i, ev.Payload)
		}
	}
	after := reloadRun(t, "run-1")
	if after.Status != shared.StatusActive || len(after.Checkpoints) != 0 || after.Counters.PhaseToolCalls != 0 {
		t.Fatalf("state must be untouched, got %s / %d checkpoints / %d calls",
			after.Status, len(after.Checkpoints), after.Counters.PhaseToolCalls)
	}
}

func TestPhaseSubmitRejectedReplay(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	if err := r.OpenPhase("context"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}

	code, stderr := callGate(t, toolPayload(t, "Skill", skillInput("summoner:phase-submit", submitArgs(t, "context"))))
	if code != 0 {
		t.Fatalf("first phase-submit: exit %d, want 0 (stderr %q)", code, stderr)
	}
	if got := reloadRun(t, "run-1").Status; got != shared.StatusWaitUser {
		t.Fatalf("status = %s, want WAIT_USER", got)
	}

	// Duplicate same payload (replay) while the checkpoint is pending.
	code, stderr = callGate(t, toolPayload(t, "Skill", skillInput("summoner:phase-submit", submitArgs(t, "context"))))
	wantExit2(t, code, stderr, "")

	evs := readGateTrace(t, "run-1")
	got := traceTypes(evs)
	want := []string{shared.EventToolStarted, shared.EventPhaseClosed, shared.EventCheckpointOpened, shared.EventPhaseSubmittedRejected}
	if len(got) != len(want) {
		t.Fatalf("trace types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trace types = %v, want %v", got, want)
		}
	}
	after := reloadRun(t, "run-1")
	if len(after.Checkpoints) != 1 {
		t.Fatalf("replay must not open a second checkpoint, got %d", len(after.Checkpoints))
	}
	if evs[2].Payload["checkpoint_id"] != "cp-1" || evs[2].Payload["nonce"] == "" {
		t.Fatalf("checkpoint.opened payload = %v, want checkpoint_id cp-1 + nonce", evs[2].Payload)
	}

	// Submitting the NEXT expected phase while WAIT_USER must not auto-open
	// it: the rejection still comes from SubmitPhase (traced), never from a
	// half-attempted OpenPhase.
	code, stderr = callGate(t, toolPayload(t, "Skill", skillInput("summoner:phase-submit", submitArgs(t, "diagnosis"))))
	wantExit2(t, code, stderr, "")
	evs = readGateTrace(t, "run-1")
	if got := traceTypes(evs); got[len(got)-1] != shared.EventPhaseSubmittedRejected {
		t.Fatalf("WAIT_USER out-of-order submit must end with phase_submitted.rejected, got %v", got)
	}
	if after = reloadRun(t, "run-1"); len(after.Phases) != 1 || after.Phases[0].ID != "context" {
		t.Fatalf("no phase may be auto-opened while WAIT_USER, phases = %+v", after.Phases)
	}
}

func TestPhaseSubmitAutoOpensSkilllessPhase(t *testing.T) {
	useGateEnv(t)
	startFixRun(t, shared.BudgetLimits{}) // ACTIVE, no phase open (skill-less P0)

	code, stderr := callGate(t, toolPayload(t, "Skill", skillInput("summoner:phase-submit", submitArgs(t, "context"))))
	if code != 0 {
		t.Fatalf("skillless phase-submit: exit %d, want 0 (stderr %q)", code, stderr)
	}
	after := reloadRun(t, "run-1")
	if after.Status != shared.StatusWaitUser || len(after.Phases) != 1 || after.Phases[0].ID != "context" || after.Phases[0].Open {
		t.Fatalf("auto-open+submit state = %s %+v", after.Status, after.Phases)
	}
	got := traceTypes(readGateTrace(t, "run-1"))
	want := []string{shared.EventToolStarted, shared.EventPhaseStarted, shared.EventPhaseClosed, shared.EventCheckpointOpened}
	if len(got) != len(want) {
		t.Fatalf("trace types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trace types = %v, want %v", got, want)
		}
	}

	// The fallback only fires for the NEXT expected phase: submitting
	// "diagnosis" out of order neither opens nor submits anything.
	useGateEnv(t)
	startFixRun(t, shared.BudgetLimits{})
	code, stderr = callGate(t, toolPayload(t, "Skill", skillInput("summoner:phase-submit", submitArgs(t, "diagnosis"))))
	wantExit2(t, code, stderr, "")
	after = reloadRun(t, "run-1")
	if len(after.Phases) != 0 {
		t.Fatalf("out-of-order submit must not open a phase, got %+v", after.Phases)
	}
	if evs := readGateTrace(t, "run-1"); len(evs) != 1 || evs[0].Type != shared.EventPhaseSubmittedRejected {
		t.Fatalf("trace types = %v, want [phase_submitted.rejected]", traceTypes(evs))
	}
}

// --- skill phase-opening (ruling 3) -----------------------------------------------

func TestSkillOpensNextPhase(t *testing.T) {
	useGateEnv(t)
	startFixRun(t, shared.BudgetLimits{})

	code, stderr := callGate(t, toolPayload(t, "Skill", skillInput("project-context-skill", "")))
	if code != 0 {
		t.Fatalf("phase skill: exit %d, want 0 (stderr %q)", code, stderr)
	}
	after := reloadRun(t, "run-1")
	if len(after.Phases) != 1 || after.Phases[0].ID != "context" || !after.Phases[0].Open {
		t.Fatalf("phases = %+v, want context open", after.Phases)
	}
	got := traceTypes(readGateTrace(t, "run-1"))
	if len(got) != 2 || got[0] != shared.EventToolStarted || got[1] != shared.EventPhaseStarted {
		t.Fatalf("trace types = %v, want [tool.started phase.started]", got)
	}

	// A second skill while a phase is open records only — no second open.
	code, _ = callGate(t, toolPayload(t, "Skill", skillInput("project-context-skill", "")))
	if code != 0 {
		t.Fatalf("second skill call: exit %d, want 0", code)
	}
	if reloadRun(t, "run-1").Phases[0].OpenedAt == "" || len(reloadRun(t, "run-1").Phases) != 1 {
		t.Fatal("a phase is already open — no second OpenPhase may happen")
	}
	if evs := readGateTrace(t, "run-1"); len(evs) != 3 || evs[2].Type != shared.EventToolStarted {
		t.Fatalf("second call must append only tool.started, got %v", traceTypes(evs))
	}

	// Framework verbs never open phases.
	code, _ = callGate(t, toolPayload(t, "Skill", skillInput("summoner:status", "")))
	if code != 0 {
		t.Fatalf("summoner:-prefixed skill: exit %d, want 0", code)
	}
	if after = reloadRun(t, "run-1"); len(after.Phases) != 1 {
		t.Fatalf("summoner: skills must not open phases, phases = %+v", after.Phases)
	}

	// A Skill payload with no skill name cannot be verified — fail closed.
	code, stderr = callGate(t, toolPayload(t, "Skill", map[string]interface{}{}))
	wantExit2(t, code, stderr, "skill")
}

// --- agent delegation (ruling 7) --------------------------------------------------

func TestAgentGateWrongType(t *testing.T) {
	useGateEnv(t)
	startFixRun(t, shared.BudgetLimits{})

	for _, typ := range []string{"", "fork", "general-purpose", "Explore", "summoner:code-reviewer"} {
		input := agentInput(typ, "do work")
		if typ == "" {
			input = map[string]interface{}{"prompt": "do work"}
		}
		code, stderr := callGate(t, toolPayload(t, "Agent", input))
		wantExit2(t, code, stderr, "subagent_type")
	}

	code, stderr := callGate(t, toolPayload(t, "Agent", agentInput("worker", "implement shard 1")))
	if code != 0 {
		t.Fatalf("worker delegation: exit %d, want 0 (stderr %q)", code, stderr)
	}
	if reloadRun(t, "run-1").Counters.Delegations != 1 {
		t.Fatal("allowed delegation must increment Counters.Delegations")
	}
	evs := readGateTrace(t, "run-1")
	if got := traceTypes(evs); len(got) != 2 || got[0] != shared.EventToolStarted || got[1] != shared.EventDelegationStarted {
		t.Fatalf("trace types = %v, want [tool.started delegation.started]", got)
	}
	if evs[1].Payload["subagent_type"] != "worker" {
		t.Fatalf("delegation.started payload = %v, want subagent_type worker", evs[1].Payload)
	}
}

func TestAgentReviewerRequiresPreparedReviewID(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	env := shared.Envelope{
		SchemaVersion: "2",
		RunID:         "run-1",
		ReviewID:      "rev-42",
		ArtifactSet:   []shared.ArtifactRef{{Path: "fix.md", Digest: "ddd"}},
	}
	if _, err := r.RequestReview(env); err != nil {
		t.Fatalf("RequestReview: %v", err)
	} // REVIEW_PENDING, review PREPARED

	code, stderr := callGate(t, toolPayload(t, "Agent", agentInput("reviewer", "please review the artifacts")))
	wantExit2(t, code, stderr, "review_id")

	code, _ = callGate(t, toolPayload(t, "Agent", agentInput("reviewer", "review envelope review_id=rev-42 per contract")))
	if code != 0 {
		t.Fatalf("reviewer with bound review_id: exit %d, want 0", code)
	}
	after := reloadRun(t, "run-1")
	if after.Reviews[0].Status != shared.ReviewRunning {
		t.Fatalf("review status = %s, want RUNNING", after.Reviews[0].Status)
	}
	if after.Counters.Delegations != 1 {
		t.Fatalf("delegations = %d, want 1", after.Counters.Delegations)
	}
	evs := readGateTrace(t, "run-1")
	if len(evs) != 2 || evs[1].Type != shared.EventDelegationStarted {
		t.Fatalf("trace types = %v, want [tool.started delegation.started]", traceTypes(evs))
	}
	if evs[1].Payload["review_id"] != "rev-42" || evs[1].Payload["subagent_type"] != "reviewer" {
		t.Fatalf("delegation.started payload = %v, want review_id rev-42 + reviewer", evs[1].Payload)
	}
}

func TestDelegationBudgetExhausted(t *testing.T) {
	useGateEnv(t)
	startFixRun(t, shared.BudgetLimits{Delegations: 1})

	code, _ := callGate(t, toolPayload(t, "Agent", agentInput("worker", "shard 1")))
	if code != 0 {
		t.Fatalf("first delegation: exit %d, want 0", code)
	}
	code, stderr := callGate(t, toolPayload(t, "Agent", agentInput("worker", "shard 2")))
	wantExit2(t, code, stderr, "delegation")

	// BudgetExhausted is global-first (ruling 9): zero delegation budget
	// freezes every tool, not just Agent calls.
	code, stderr = callGate(t, toolPayload(t, "Read", readInput()))
	wantExit2(t, code, stderr, "delegation")

	// Limit 0 = unlimited (same semantics as BudgetExhausted).
	useGateEnv(t)
	startFixRun(t, shared.BudgetLimits{})
	for i := 0; i < 2; i++ {
		if code, _ := callGate(t, toolPayload(t, "Agent", agentInput("worker", "shard"))); code != 0 {
			t.Fatalf("unlimited delegations: call %d exit %d, want 0", i+1, code)
		}
	}
	if n := reloadRun(t, "run-1").Counters.Delegations; n != 2 {
		t.Fatalf("delegations = %d, want 2", n)
	}
}

// --- AskUserQuestion nonce binding (ruling 8) --------------------------------------

func TestAskUserMissingNonce(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	if err := r.OpenPhase("context"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	cp, err := r.SubmitPhase(shared.PhaseSubmission{PhaseID: "context"})
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}

	code, stderr := callGate(t, toolPayload(t, "AskUserQuestion", askInput("Continue to the next phase?")))
	wantExit2(t, code, stderr, "nonce")

	code, _ = callGate(t, toolPayload(t, "AskUserQuestion", askInput(
		fmt.Sprintf("Checkpoint %s (nonce %s): continue?", cp.ID, cp.Nonce))))
	if code != 0 {
		t.Fatalf("nonce-bearing question: exit %d, want 0", code)
	}

	// In ACTIVE the same tool is ordinary Q&A — never gated. (The decision
	// itself is recorded by PostToolUse, not here; decide via the reducer.)
	cur := reloadRun(t, "run-1")
	cpNow := cur.CurrentCheckpoint()
	if err := cur.MarkDisplayed(cpNow.ID, shared.CheckpointContentDigest(*cpNow, cur.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	if err := cur.RecordDecision(cpNow.ID, shared.DecisionContinue); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
	code, _ = callGate(t, toolPayload(t, "AskUserQuestion", askInput("any question at all")))
	if code != 0 {
		t.Fatalf("AskUserQuestion in ACTIVE: exit %d, want 0", code)
	}
}

// --- budget (ruling 9) --------------------------------------------------------------

func TestBudgetExhaustionBlocks(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{PhaseToolCalls: 2})
	acceptPhase(t, r, "context")
	acceptPhase(t, r, "diagnosis")
	if err := r.OpenPhase("reproduce"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}

	for i := 0; i < 2; i++ {
		if code, _ := callGate(t, toolPayload(t, "Read", readInput())); code != 0 {
			t.Fatalf("read %d within budget: exit != 0", i+1)
		}
	}
	code, stderr := callGate(t, toolPayload(t, "Read", readInput()))
	wantExit2(t, code, stderr, "budget")

	// I5: the AskUserQuestion budget exemption applies only to the
	// WAIT_USER decision channel — an ordinary ACTIVE question is still
	// budget-gated (耗尽 → 检查点).
	code, stderr = callGate(t, toolPayload(t, "AskUserQuestion", askInput("ordinary question")))
	wantExit2(t, code, stderr, "budget")

	// Control-plane exceptions: phase-submit and the checkpoint's
	// AskUserQuestion must stay usable, or a zero budget could never reach
	// the checkpoint where the user resolves it.
	code, stderr = callGate(t, toolPayload(t, "Skill", skillInput("summoner:phase-submit", submitArgs(t, "reproduce"))))
	if code != 0 {
		t.Fatalf("phase-submit under exhausted budget: exit %d, want 0 (stderr %q)", code, stderr)
	}
	cp := reloadRun(t, "run-1").CurrentCheckpoint()
	if cp == nil {
		t.Fatal("phase-submit must have opened the checkpoint")
	}
	code, _ = callGate(t, toolPayload(t, "AskUserQuestion", askInput(fmt.Sprintf("nonce %s: continue?", cp.Nonce))))
	if code != 0 {
		t.Fatalf("AskUserQuestion under exhausted budget: exit %d, want 0", code)
	}
}

// --- fail-closed parsing (ruling 2 / brief) -----------------------------------------

func TestGateParseFailureIsDeny(t *testing.T) {
	useGateEnv(t)
	startFixRun(t, shared.BudgetLimits{})

	for name, stdin := range map[string]string{
		"invalid JSON":                           `{"session_id": "sess-1", "tool_name":`,
		"empty stdin":                            ``,
		"null stdin":                             `null`,
		"array stdin":                            `[]`,
		"tool_input is a string (not an object)": `{"session_id":"sess-1","tool_name":"Edit","tool_input":"/tmp/x.go"}`,
		"trailing garbage":                       `{"session_id":"sess-1","tool_name":"Edit"} oops`,
	} {
		code, stderr := callGate(t, stdin)
		if code != 2 {
			t.Fatalf("%s: exit code = %d, want 2 (fail-closed); stderr %q", name, code, stderr)
		}
	}

	// Parse failure denies even with no run at all — it precedes everything.
	useGateEnv(t)
	if code, _ := callGate(t, `{`); code != 2 {
		t.Fatalf("parse failure with no run: exit %d, want 2", code)
	}
}

// --- run resolution (ruling 5) -------------------------------------------------------

func countFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return n
}

func TestNoRunPassThrough(t *testing.T) {
	useGateEnv(t)

	code, _ := callGate(t, toolPayload(t, "Edit", editInput()))
	if code != 0 {
		t.Fatalf("Edit with no run: exit %d, want 0 (non-Summoner sessions untouched)", code)
	}
	code, _ = callGate(t, toolPayload(t, "Skill", skillInput("anything", "")))
	if code != 0 {
		t.Fatalf("Skill with no run: exit %d, want 0", code)
	}
	if n := countFiles(t, os.Getenv("SUMMONER_STATE_DIR")); n != 0 {
		t.Fatalf("pass-through created %d state files, want 0", n)
	}
	if n := countFiles(t, os.Getenv("SUMMONER_TRACE_DIR")); n != 0 {
		t.Fatalf("pass-through created %d trace files, want 0", n)
	}

	// Exception: phase-submit requires an active run.
	code, stderr := callGate(t, toolPayload(t, "Skill", skillInput("summoner:phase-submit", submitArgs(t, "context"))))
	wantExit2(t, code, stderr, "requires an active run")

	// A CLOSED run is not active: same pass-through.
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	r.Status = shared.StatusClosed // not yet reachable via reducer API (post-game hook is a later task)
	if err := r.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if code, _ := callGate(t, toolPayload(t, "Edit", editInput())); code != 0 {
		t.Fatalf("Edit with only a CLOSED run: exit %d, want 0", code)
	}
	if evs := readGateTrace(t, "run-1"); len(evs) != 0 {
		t.Fatalf("CLOSED run must not be traced, got %v", traceTypes(evs))
	}
}

func TestAmbiguousRunsFailClosed(t *testing.T) {
	useGateEnv(t)
	if _, err := shared.StartRun("run-a", gateSession, "fix", shared.BudgetLimits{}); err != nil {
		t.Fatalf("StartRun(run-a): %v", err)
	}
	if _, err := shared.StartRun("run-b", gateSession, "fix", shared.BudgetLimits{}); err != nil {
		t.Fatalf("StartRun(run-b): %v", err)
	}

	for _, tool := range []string{"Edit", "Read"} {
		var input map[string]interface{}
		if tool == "Edit" {
			input = editInput()
		} else {
			input = readInput()
		}
		code, stderr := callGate(t, toolPayload(t, tool, input))
		wantExit2(t, code, stderr, "ambiguous")
	}
	code, stderr := callGate(t, toolPayload(t, "Skill", skillInput("summoner:phase-submit", submitArgs(t, "context"))))
	wantExit2(t, code, stderr, "ambiguous")
}

// --- Bash write-class heuristic (ruling 4) -------------------------------------------

func TestWriteClassBashHeuristic(t *testing.T) {
	writes := []string{
		"echo hi > f.txt", "echo hi >> f.txt", "go test ./... 2> err.log",
		"tee out.txt", "cat a.txt | tee b.txt",
		"rm -rf /tmp/x", "mv a b", "cp src dst",
		"sed -i 's/a/b/' f.txt", "sed -i.bak 's/a/b/' f.txt",
		"chmod +x run.sh", "chown user file", "touch marker", "mkdir build",
		"git commit -m 'x'", "git push origin main", "git -C /repo reset --hard HEAD~1",
		"git checkout -b feature", "git rebase main", "git merge develop", "git stash",
		"echo $(rm -rf x)", "FOO=1 rm -rf x", "true && touch done",
		// I4: ordinary spellings that must not slip through — absolute
		// paths, wrappers, quoted flag values.
		"/bin/rm -f x", "/usr/bin/rm -rf /tmp/x", "/usr/bin/git commit -m x",
		"command rm x", "command git push", "env MODE=1 rm x", "env rm x", "nohup rm x",
		`git -C "/tmp/a repo" reset --hard`, "nohup env MODE=1 tee out.txt",
	}
	for _, cmd := range writes {
		if !isWriteClassCommand(cmd) {
			t.Errorf("isWriteClassCommand(%q) = false, want true", cmd)
		}
	}

	nonWrites := []string{
		"", "ls -la", "cat file.txt", "grep pattern file.txt",
		"git log --oneline", "git status", "git diff HEAD",
		"sed -n '1p' file.txt", "echo hello", "cat a | grep b",
		"find . -name '*.go'", "grep -c rm notes.txt",
		"go test ./...", "curl -s https://example.com",
		// I4 negatives: quoting must not hide reads, wrappers must not
		// invent writes.
		`grep 'foo bar' file.txt`, `cat "my file.txt"`,
		`git log --grep "fix stuff"`, "git diff HEAD~1..HEAD",
		"env | grep PATH", "command -v rm", "nohup cat file.txt",
	}
	for _, cmd := range nonWrites {
		if isWriteClassCommand(cmd) {
			t.Errorf("isWriteClassCommand(%q) = true, want false", cmd)
		}
	}

	// The heuristic must actually gate: write-class Bash obeys the matrix.
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r, "context")
	code, stderr := callGate(t, toolPayload(t, "Bash", bashInput("git push origin main")))
	wantExit2(t, code, stderr, "diagnosis")
	code, _ = callGate(t, toolPayload(t, "Bash", bashInput("git status")))
	if code != 0 {
		t.Fatalf("read-only Bash: exit %d, want 0", code)
	}
}

// --- escape hatch (controller amendment) ----------------------------------------------

func TestEscapeHatchPassThrough(t *testing.T) {
	useGateEnv(t)
	setenv16(t, "SUMMONER_HOOKS_DISABLE", "1")

	// No live run: nothing to violate, nothing to enforce — pure exit 0.
	code, stderr := callGate(t, toolPayload(t, "Edit", editInput()))
	if code != 0 {
		t.Fatalf("hatch + no run: exit %d, want 0 (stderr %q)", code, stderr)
	}
	if n := countFiles(t, os.Getenv("SUMMONER_STATE_DIR")); n != 0 {
		t.Fatalf("hatch pass-through created %d state files, want 0", n)
	}
	if n := countFiles(t, os.Getenv("SUMMONER_TRACE_DIR")); n != 0 {
		t.Fatalf("hatch pass-through created %d trace files, want 0", n)
	}

	// Ambiguous (>1 live runs): still exit 0, and NO events — nothing
	// unambiguously live to record a violation against.
	if _, err := shared.StartRun("run-a", gateSession, "fix", shared.BudgetLimits{}); err != nil {
		t.Fatalf("StartRun(run-a): %v", err)
	}
	if _, err := shared.StartRun("run-b", gateSession, "fix", shared.BudgetLimits{}); err != nil {
		t.Fatalf("StartRun(run-b): %v", err)
	}
	code, _ = callGate(t, toolPayload(t, "Edit", editInput()))
	if code != 0 {
		t.Fatalf("hatch + ambiguous runs: exit %d, want 0", code)
	}
	for _, runID := range []string{"run-a", "run-b"} {
		if evs := readGateTrace(t, runID); len(evs) != 0 {
			t.Fatalf("hatch with ambiguous runs must not trace, run %s got %v", runID, traceTypes(evs))
		}
	}
}

func TestEscapeHatchTracesViolationWhenRunActive(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	if err := r.OpenPhase("context"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	} // Edit below would be denied (diagnosis not accepted)
	setenv16(t, "SUMMONER_HOOKS_DISABLE", "1")

	code, stderr := callGate(t, toolPayload(t, "Edit", editInput()))
	if code != 0 {
		t.Fatalf("hatch: exit %d, want 0 (stderr %q)", code, stderr)
	}
	evs := readGateTrace(t, "run-1")
	if got := traceTypes(evs); len(got) != 1 || got[0] != shared.EventHooksDisabled {
		t.Fatalf("trace types = %v, want [hooks.disabled]", got)
	}
	if evs[0].Payload["tool_name"] != "Edit" {
		t.Fatalf("hooks.disabled payload = %v, want tool_name Edit", evs[0].Payload)
	}
	// Nothing else moved: no tool.started, no counters, phase still open.
	after := reloadRun(t, "run-1")
	if after.Counters.PhaseToolCalls != 0 || !after.Phases[0].Open {
		t.Fatalf("hatch must not mutate state, counters=%d phases=%+v", after.Counters.PhaseToolCalls, after.Phases)
	}
}

// M1: the hatch is armed by the exact value "1" only — "0", "false", "yes"
// leave enforcement active.
func TestEscapeHatchStrictValue(t *testing.T) {
	for _, val := range []string{"0", "false", "yes", "true"} {
		useGateEnv(t)
		startFixRun(t, shared.BudgetLimits{})
		setenv16(t, "SUMMONER_HOOKS_DISABLE", val)

		// Pre-diagnosis Edit would be denied with enforcement active.
		code, stderr := callGate(t, toolPayload(t, "Edit", editInput()))
		if code != 2 {
			t.Fatalf("SUMMONER_HOOKS_DISABLE=%q must NOT arm the hatch: exit %d, want 2", val, code)
		}
		if !strings.Contains(stderr, "diagnosis") {
			t.Fatalf("SUMMONER_HOOKS_DISABLE=%q: stderr %q must show enforcement", val, stderr)
		}
		if evs := readGateTrace(t, "run-1"); len(evs) != 0 {
			t.Fatalf("SUMMONER_HOOKS_DISABLE=%q must not trace, got %v", val, traceTypes(evs))
		}
	}
}

// --- review round 1: I1 — run discovery fails closed on unreadable state ------

func TestSessionDirUnreadableFailsClosed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not block reads")
	}
	useGateEnv(t)
	startFixRun(t, shared.BudgetLimits{})

	dir := filepath.Join(os.Getenv("SUMMONER_STATE_DIR"), shared.ProjectKey(), gateSession)
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Skipf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	// An unreadable session dir must not look like "no runs" (pass-through);
	// it is a fail-closed deny.
	code, stderr := callGate(t, toolPayload(t, "Edit", editInput()))
	wantExit2(t, code, stderr, "unreadable")
}

// --- review round 1: I2 — recall-aware next-phase sequencing -------------------

// TestNextPhaseAfterRecall — the reviewer's repro: recall to context, re-accept
// it, and the next skill must open DIAGNOSIS (its visit is unsettled: recalled),
// not reproduce.
func TestNextPhaseAfterRecall(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r, "context")

	// Diagnosis submitted, then recalled: the recall targets the most recent
	// accepted phase (context), and diagnosis's visit is marked recalled.
	if err := r.OpenPhase("diagnosis"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	cpD, err := r.SubmitPhase(shared.PhaseSubmission{PhaseID: "diagnosis"})
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	if err := r.MarkDisplayed(cpD.ID, shared.CheckpointContentDigest(cpD, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	if err := r.RecordDecision(cpD.ID, shared.DecisionRecall); err != nil {
		t.Fatalf("RecordDecision(RECALL): %v", err)
	}

	// Re-accept context (reopened by the recall).
	cpC, err := r.SubmitPhase(shared.PhaseSubmission{PhaseID: "context"})
	if err != nil {
		t.Fatalf("SubmitPhase(context): %v", err)
	}
	if err := r.MarkDisplayed(cpC.ID, shared.CheckpointContentDigest(cpC, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	if err := r.RecordDecision(cpC.ID, shared.DecisionContinue); err != nil {
		t.Fatalf("RecordDecision(CONTINUE): %v", err)
	}

	code, stderr := callGate(t, toolPayload(t, "Skill", skillInput("project-diagnosis-skill", "")))
	if code != 0 {
		t.Fatalf("post-recall skill: exit %d, want 0 (stderr %q)", code, stderr)
	}
	after := reloadRun(t, "run-1")
	last := after.Phases[len(after.Phases)-1]
	if !last.Open || last.ID != "diagnosis" {
		t.Fatalf("next phase after recall-to-context must be diagnosis, got %+v (all: %+v)", last, after.Phases)
	}
}

// TestSkippedPhaseSequencing — a skipped phase is settled; sequencing moves on.
func TestSkippedPhaseSequencing(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	if err := r.OpenPhase("context"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	cp, err := r.SubmitPhase(shared.PhaseSubmission{PhaseID: "context"})
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	if err := r.RecordDecision(cp.ID, shared.DecisionSkip); err != nil {
		t.Fatalf("RecordDecision(SKIP): %v", err)
	}

	code, _ := callGate(t, toolPayload(t, "Skill", skillInput("project-diagnosis-skill", "")))
	if code != 0 {
		t.Fatalf("skill after skip: exit %d, want 0", code)
	}
	after := reloadRun(t, "run-1")
	if len(after.Phases) != 2 || !after.Phases[1].Open || after.Phases[1].ID != "diagnosis" {
		t.Fatalf("after SKIP, next phase must be diagnosis, got %+v", after.Phases)
	}
}

// --- review round 1: I3 — post-game writes without diagnosis -------------------

func TestPostGameWriteWithoutDiagnosis(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	if err := r.OpenPhase("context"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	cp, err := r.SubmitPhase(shared.PhaseSubmission{PhaseID: "context"})
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	// Emergency stop: minimal exit journal must still be writable.
	if err := r.RecordDecision(cp.ID, shared.DecisionStop); err != nil {
		t.Fatalf("RecordDecision(STOP): %v", err)
	}
	if r.Status != shared.StatusPostGamePending {
		t.Fatalf("status = %s, want POST_GAME_PENDING", r.Status)
	}

	code, stderr := callGate(t, toolPayload(t, "Write", writeInput()))
	if code != 0 {
		t.Fatalf("exit-journal Write in POST_GAME_PENDING (no accepted diagnosis): exit %d, want 0 (stderr %q)", code, stderr)
	}
}

// --- review round 1: I6 — intent events precede any mutation -------------------

func TestAgentTraceFailureDeniesWithoutMutation(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not block reads")
	}
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	env := shared.Envelope{
		SchemaVersion: "2",
		RunID:         "run-1",
		ReviewID:      "rev-1",
		ArtifactSet:   []shared.ArtifactRef{{Path: "fix.md", Digest: "ddd"}},
	}
	if _, err := r.RequestReview(env); err != nil {
		t.Fatalf("RequestReview: %v", err)
	}

	// Break the trace dir: the intent append must fail BEFORE any state
	// mutation — the review stays PREPARED, counters untouched.
	keyDir := filepath.Join(os.Getenv("SUMMONER_TRACE_DIR"), shared.ProjectKey())
	if err := os.MkdirAll(keyDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(keyDir, 0o000); err != nil {
		t.Skipf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(keyDir, 0o755) })

	code, stderr := callGate(t, toolPayload(t, "Agent", agentInput("reviewer", "review envelope review_id=rev-1")))
	wantExit2(t, code, stderr, "")
	after := reloadRun(t, "run-1")
	if after.Reviews[0].Status != shared.ReviewPrepared {
		t.Fatalf("failed intent append must not start the review, status = %s", after.Reviews[0].Status)
	}
	if after.Counters.Delegations != 0 || after.Counters.PhaseToolCalls != 0 {
		t.Fatalf("failed intent append must not consume counters, got %+v", after.Counters)
	}

	// Repair: the same call now succeeds end-to-end.
	if err := os.Chmod(keyDir, 0o755); err != nil {
		t.Fatalf("chmod repair: %v", err)
	}
	code, _ = callGate(t, toolPayload(t, "Agent", agentInput("reviewer", "review envelope review_id=rev-1")))
	if code != 0 {
		t.Fatalf("retry after repair: exit %d, want 0", code)
	}
	after = reloadRun(t, "run-1")
	if after.Reviews[0].Status != shared.ReviewRunning || after.Counters.Delegations != 1 {
		t.Fatalf("retry after repair: review=%s delegations=%d", after.Reviews[0].Status, after.Counters.Delegations)
	}
	if got := traceTypes(readGateTrace(t, "run-1")); len(got) != 2 || got[0] != shared.EventToolStarted || got[1] != shared.EventDelegationStarted {
		t.Fatalf("trace types = %v, want [tool.started delegation.started] (failed attempt must have appended nothing)", got)
	}
}

// --- review round 1: I7 — malformed known-tool payloads deny -------------------

func TestGateMalformedKnownToolPayloads(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r, "context")
	acceptPhase(t, r, "diagnosis") // writes legal now — only shape can deny

	cases := []struct {
		name      string
		toolName  string
		toolInput map[string]interface{}
	}{
		{"Bash command missing", "Bash", map[string]interface{}{}},
		{"Bash command numeric", "Bash", map[string]interface{}{"command": 42}},
		{"Bash command null", "Bash", map[string]interface{}{"command": nil}},
		{"Edit without file_path", "Edit", map[string]interface{}{"old_string": "a", "new_string": "b"}},
		{"Write file_path numeric", "Write", map[string]interface{}{"file_path": 42, "content": "x"}},
		{"NotebookEdit without notebook_path", "NotebookEdit", map[string]interface{}{"new_source": "x"}},
		{"Agent without prompt", "Agent", map[string]interface{}{"subagent_type": "worker"}},
		{"Skill numeric", "Skill", map[string]interface{}{"skill": 42}},
		{"AskUserQuestion questions not array", "AskUserQuestion", map[string]interface{}{"questions": "yes?"}},
		{"AskUserQuestion question not string", "AskUserQuestion", map[string]interface{}{"questions": []interface{}{map[string]interface{}{"header": "h"}}}},
	}
	for _, tc := range cases {
		code, stderr := callGate(t, toolPayload(t, tc.toolName, tc.toolInput))
		if code != 2 {
			t.Errorf("%s: exit %d, want 2 (fail-closed); stderr %q", tc.name, code, stderr)
		}
	}

	// Empty tool_name denies even though nothing else is known about the call.
	raw := mustJSONStr(t, map[string]interface{}{
		"session_id": gateSession, "tool_use_id": "tu-x", "tool_input": map[string]interface{}{},
	})
	if code, _ := callGate(t, raw); code != 2 {
		t.Fatalf("empty tool_name: exit %d, want 2", code)
	}

	// Unknown tool NAMES keep the observe-and-allow policy.
	code, _ := callGate(t, toolPayload(t, "FancyMcpTool", map[string]interface{}{"whatever": 1}))
	if code != 0 {
		t.Fatalf("unknown tool name with arbitrary shape: exit %d, want 0", code)
	}
}

// --- review round 1: M2 — shape and workflow coverage ---------------------------

func TestNotebookEditGate(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r, "context")

	code, stderr := callGate(t, toolPayload(t, "NotebookEdit", notebookInput()))
	wantExit2(t, code, stderr, "diagnosis")

	acceptPhase(t, r, "diagnosis")
	code, _ = callGate(t, toolPayload(t, "NotebookEdit", notebookInput()))
	if code != 0 {
		t.Fatalf("NotebookEdit after diagnosis accepted: exit %d, want 0", code)
	}
}

func TestNewWorkflowSequence(t *testing.T) {
	useGateEnv(t)
	r := startRunWF(t, "new", shared.BudgetLimits{})

	// The diagnosis iron law is fix|debug only: new allows ACTIVE writes.
	code, _ := callGate(t, toolPayload(t, "Write", writeInput()))
	if code != 0 {
		t.Fatalf("new workflow ACTIVE Write: exit %d, want 0", code)
	}

	code, _ = callGate(t, toolPayload(t, "Skill", skillInput("project-context-skill", "")))
	if code != 0 {
		t.Fatalf("first skill: exit %d, want 0", code)
	}
	after := reloadRun(t, "run-1")
	if len(after.Phases) != 1 || after.Phases[0].ID != "context" {
		t.Fatalf("new workflow must open context first, got %+v", after.Phases)
	}
	acceptPhase(t, r, "context")

	code, _ = callGate(t, toolPayload(t, "Skill", skillInput("project-requirements-skill", "")))
	if code != 0 {
		t.Fatalf("second skill: exit %d, want 0", code)
	}
	after = reloadRun(t, "run-1")
	if last := after.Phases[len(after.Phases)-1]; !last.Open || last.ID != "requirements" {
		t.Fatalf("second phase must be requirements, got %+v", after.Phases)
	}
}

func TestDebugWorkflowSequence(t *testing.T) {
	useGateEnv(t)
	r := startRunWF(t, "debug", shared.BudgetLimits{})

	code, _ := callGate(t, toolPayload(t, "Skill", skillInput("project-context-skill", "")))
	if code != 0 {
		t.Fatalf("first skill: exit %d, want 0", code)
	}
	acceptPhase(t, r, "context")

	code, _ = callGate(t, toolPayload(t, "Skill", skillInput("project-diagnosis-skill", "")))
	if code != 0 {
		t.Fatalf("second skill: exit %d, want 0", code)
	}
	after := reloadRun(t, "run-1")
	if last := after.Phases[len(after.Phases)-1]; !last.Open || last.ID != "diagnosis" {
		t.Fatalf("debug workflow second phase must be diagnosis, got %+v", after.Phases)
	}

	// debug carries the diagnosis iron law: writes blocked until accepted.
	code, stderr := callGate(t, toolPayload(t, "Edit", editInput()))
	wantExit2(t, code, stderr, "diagnosis")
	acceptPhase(t, r, "diagnosis")
	code, _ = callGate(t, toolPayload(t, "Edit", editInput()))
	if code != 0 {
		t.Fatalf("Edit after diagnosis accepted: exit %d, want 0", code)
	}
}
