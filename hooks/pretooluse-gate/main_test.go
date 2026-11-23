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
// dirs so no test ever touches real Summoner state. It also seeds the shared
// write-target fixture x.go (content carries the literal "a" exactly once):
// under the §4 pre-effect declaration, an admitted Edit must APPLY — the
// old /tmp/x.go fixture pointed at a nonexistent file, which is now (correctly)
// a pre-effect denial, not an admit.
func useGateEnv(t *testing.T) {
	t.Helper()
	setenv16(t, "SUMMONER_STATE_DIR", t.TempDir())
	setenv16(t, "SUMMONER_TRACE_DIR", t.TempDir())
	proj := t.TempDir()
	setenv16(t, "CLAUDE_PROJECT_DIR", proj)
	unsetenv16(t, "SUMMONER_HOOKS_DISABLE")
	if err := os.WriteFile(filepath.Join(proj, "x.go"), []byte("package x\n// SEED-TOKEN\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
// Artifacts are REAL files with hook-computed digests (the C1 staging
// contract: producer claims are advisory, current bytes are the truth any
// later mint re-hashes).
func acceptPhase(t *testing.T, r *shared.RunState, phaseID string) {
	t.Helper()
	if err := r.OpenPhase(phaseID); err != nil {
		t.Fatalf("OpenPhase(%s): %v", phaseID, err)
	}
	if err := os.WriteFile(filepath.Join(shared.ProjectDir(), phaseID+".md"), []byte(phaseID+" notes\n"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	digest, err := shared.HashArtifactFile(phaseID + ".md")
	if err != nil {
		t.Fatalf("HashArtifactFile(%s): %v", phaseID+".md", err)
	}
	cp, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{
		PhaseID:   phaseID,
		Summary:   phaseID + " done",
		Artifacts: []shared.ArtifactRef{{Path: phaseID + ".md", Digest: digest, Kind: "doc"}},
	})
	if err != nil {
		t.Fatalf("SubmitPhase(%s): %v", phaseID, err)
	}
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed(%s): %v", cp.ID, err)
	}
	if err := r.RecordDecision(cp.ID, shared.DecisionContinue, ""); err != nil {
		t.Fatalf("RecordDecision(%s): %v", cp.ID, err)
	}
}

// settleReviewDebt discharges the mandatory-terminal-⑤ debt a DONE mints on
// review-phase workflows (M4 / ruling 72): the run parks at a REVIEW_DEBT
// checkpoint in WAIT_USER; this fixture helper displays it and takes the
// explicit UNREVIEWED finish, landing POST_GAME_PENDING — the state every
// older post-game fixture expects. Workflows without a review phase never
// mint the debt and pass through untouched.
func settleReviewDebt(t *testing.T, r *shared.RunState) {
	t.Helper()
	cp := r.CurrentCheckpoint()
	if cp == nil || cp.Kind != shared.CheckpointReviewDebt {
		return
	}
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(*cp, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed(debt %s): %v", cp.ID, err)
	}
	if err := r.RecordDecision(cp.ID, shared.DecisionSkip, ""); err != nil {
		t.Fatalf("RecordDecision(debt %s SKIP): %v", cp.ID, err)
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
	return toolPayloadID(t, toolName, "tu-"+toolName, toolInput)
}

// toolPayloadID builds one gate payload with an EXPLICIT tool_use id — use
// wherever a test repeats the same tool: real transcripts never repeat a
// tool_use id, and the visit bucket's dedup (final-fix I1) collapses a
// replayed id to a single charge.
func toolPayloadID(t *testing.T, toolName, id string, toolInput map[string]interface{}) string {
	t.Helper()
	return mustJSONStr(t, map[string]interface{}{
		"session_id":  gateSession,
		"tool_name":   toolName,
		"tool_use_id": id,
		"tool_input":  toolInput,
	})
}

func editInput() map[string]interface{} {
	return map[string]interface{}{
		"file_path": "x.go", "old_string": "SEED-TOKEN", "new_string": "PATCHED",
	}
}

func writeInput() map[string]interface{} {
	return map[string]interface{}{"file_path": "x.go", "content": "package x\n"}
}

func notebookInput() map[string]interface{} {
	return map[string]interface{}{"notebook_path": "/tmp/nb.ipynb", "new_source": "print(1)"}
}

func readInput() map[string]interface{} {
	return map[string]interface{}{"file_path": "x.go"}
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

// defaultSubmission is the phase-submit payload the fixtures submit by
// default: a title plus a minimal conclusion. Since 0.7.0 a diagnosis
// submission without conclusion.summary/approach is denied before any other
// validation — without this default every "must be rejected" test would pass
// for the wrong reason. The outcome is deliberately omitted (stored as
// unclear): an unclear submission never auto-passes, so these fixtures always
// land on a pending checkpoint.
func defaultSubmission(phaseID string) shared.PhaseSubmission {
	return shared.PhaseSubmission{
		PhaseID:    phaseID,
		Summary:    phaseID + " done",
		Conclusion: &shared.Conclusion{Summary: phaseID + " conclusion", Approach: phaseID + " approach"},
	}
}

func submitArgs(t *testing.T, phaseID string) string {
	t.Helper()
	return mustJSONStr(t, defaultSubmission(phaseID))
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
	if _, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "reproduce"}); err != nil {
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
	// C1: registration is identity-only; the reducer MINTS the envelope from
	// the frozen acceptance (context/diagnosis artifacts above).
	if _, err := r.RequestReview(shared.Envelope{SchemaVersion: "2", RunID: "run-1", ReviewID: "rev-1"}, nil); err != nil {
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
	cp, err := r2.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "reproduce"})
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	if err := r2.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(cp, r2.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	if err := r2.RecordDecision(cp.ID, shared.DecisionDone, ""); err != nil {
		t.Fatalf("RecordDecision(DONE): %v", err)
	}
	settleReviewDebt(t, r2)
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
	cpC, err := r3.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "context"})
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	if err := r3.MarkDisplayed(cpC.ID, shared.CheckpointContentDigest(cpC, r3.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	if err := r3.RecordDecision(cpC.ID, shared.DecisionSkip, ""); err != nil {
		t.Fatalf("RecordDecision(SKIP): %v", err)
	}
	if err := r3.OpenPhase("diagnosis"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	cpD, err := r3.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "diagnosis"})
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	if err := r3.MarkDisplayed(cpD.ID, shared.CheckpointContentDigest(cpD, r3.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	if err := r3.RecordDecision(cpD.ID, shared.DecisionRecall, ""); err != nil {
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
	acceptPhase(t, r, "context")
	// C1: identity-only registration; the reducer mints the envelope from the
	// frozen acceptance. The reviewer prompt must then be the EXACT launch
	// capsule (adjudication §2 — whole-message equality, no containment).
	if _, err := r.RequestReview(shared.Envelope{SchemaVersion: "2", RunID: "run-1", ReviewID: "rev-42"}, nil); err != nil {
		t.Fatalf("RequestReview: %v", err)
	} // REVIEW_PENDING, review PREPARED
	capsule, ok := shared.BuildReviewCapsule(r, "rev-42", r.Reviews[0].ActiveAttemptID)
	if !ok {
		t.Fatalf("BuildReviewCapsule(rev-42): not ok")
	}
	capsulePrompt, err := capsule.Bytes()
	if err != nil {
		t.Fatalf("capsule.Bytes(): %v", err)
	}

	// Review round 2 (R1): worker delegations require ACTIVE — while a
	// review is pending, a worker delegation is denied.
	code, stderr := callGate(t, toolPayload(t, "Agent", agentInput("worker", "shard")))
	wantExit2(t, code, stderr, "ACTIVE")

	// A prose prompt that merely NAMES the review id never binds — only the
	// exact capsule bytes do (whole-message equality).
	code, stderr = callGate(t, toolPayload(t, "Agent", agentInput("reviewer", "review envelope review_id=rev-42 per contract")))
	wantExit2(t, code, stderr, "capsule")

	code, _ = callGate(t, toolPayload(t, "Agent", agentInput("reviewer", string(capsulePrompt))))
	if code != 0 {
		t.Fatalf("reviewer with the exact capsule: exit %d, want 0", code)
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

// --- AskUserQuestion nonce binding (ruling 8 + T16 two-turn/page contract) --------

func TestAskUserMissingNonce(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	if err := r.OpenPhase("context"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	cp, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "context"})
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}

	// No nonce: the answer cannot bind — unchanged deny.
	code, stderr := callGate(t, toolPayload(t, "AskUserQuestion", askInput("Continue to the next phase?")))
	wantExit2(t, code, stderr, "nonce")

	// T16 two-turn: a nonce-carrying ask in the RENDER turn itself (display
	// not yet bound by the Stop hook) is denied with the two-turn
	// instruction — even with the persisted page's exact labels.
	code, stderr = callGate(t, toolPayload(t, "AskUserQuestion",
		askPageInput(cp.Nonce, cp.MenuPages[0].Labels)))
	wantExit2(t, code, stderr, "two-turn")

	// Display bound (the Stop hook's transition), then the page-pinned ask
	// passes.
	cur := reloadRun(t, "run-1")
	cpNow := cur.CurrentCheckpoint()
	if err := cur.MarkDisplayed(cpNow.ID, shared.CheckpointContentDigest(*cpNow, cur.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	code, _ = callGate(t, toolPayload(t, "AskUserQuestion",
		askPageInput(cp.Nonce, cpNow.MenuPages[0].Labels)))
	if code != 0 {
		t.Fatalf("nonce-bearing page-pinned question after display: exit %d, want 0", code)
	}

	// In ACTIVE the same tool is ordinary Q&A — never gated. (The decision
	// itself is recorded by PostToolUse, not here; decide via the reducer.)
	cur = reloadRun(t, "run-1")
	cpNow = cur.CurrentCheckpoint()
	if err := cur.RecordDecision(cpNow.ID, shared.DecisionContinue, ""); err != nil {
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
		if code, _ := callGate(t, toolPayloadID(t, "Read", fmt.Sprintf("tu-read-%d", i+1), readInput())); code != 0 {
			t.Fatalf("read %d within budget: exit != 0", i+1)
		}
	}
	code, stderr := callGate(t, toolPayloadID(t, "Read", "tu-read-3", readInput()))
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
	// T16 two-turn: the decision question is denied until the display is
	// bound (render turn first)…
	code, stderr = callGate(t, toolPayload(t, "AskUserQuestion",
		askPageInput(cp.Nonce, cp.MenuPages[0].Labels)))
	wantExit2(t, code, stderr, "two-turn")
	// …then binds and passes with the persisted page's labels (the display
	// transition the Stop hook performs at turn end).
	cur := reloadRun(t, "run-1")
	if err := cur.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(*cp, cur.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	code, _ = callGate(t, toolPayload(t, "AskUserQuestion",
		askPageInput(cp.Nonce, cp.MenuPages[0].Labels)))
	if code != 0 {
		t.Fatalf("AskUserQuestion under exhausted budget (displayed, page-pinned): exit %d, want 0", code)
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
		// R3: an empty quoted operand is still an operand — the flag must
		// consume it, not the subcommand.
		`git -C "" reset --hard`,
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
		`git -C "real path" log`,
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
	cpD, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "diagnosis"})
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	if err := r.MarkDisplayed(cpD.ID, shared.CheckpointContentDigest(cpD, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	if err := r.RecordDecision(cpD.ID, shared.DecisionRecall, ""); err != nil {
		t.Fatalf("RecordDecision(RECALL): %v", err)
	}

	// Re-accept context (reopened by the recall).
	cpC, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "context"})
	if err != nil {
		t.Fatalf("SubmitPhase(context): %v", err)
	}
	if err := r.MarkDisplayed(cpC.ID, shared.CheckpointContentDigest(cpC, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	if err := r.RecordDecision(cpC.ID, shared.DecisionContinue, ""); err != nil {
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
	cp, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "context"})
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	if err := r.RecordDecision(cp.ID, shared.DecisionSkip, ""); err != nil {
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
	cp, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "context"})
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	// Emergency stop: minimal exit journal must still be writable.
	if err := r.RecordDecision(cp.ID, shared.DecisionStop, ""); err != nil {
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
	acceptPhase(t, r, "context")
	if _, err := r.RequestReview(shared.Envelope{SchemaVersion: "2", RunID: "run-1", ReviewID: "rev-1"}, nil); err != nil {
		t.Fatalf("RequestReview: %v", err)
	}
	capsule, ok := shared.BuildReviewCapsule(r, "rev-1", r.Reviews[0].ActiveAttemptID)
	if !ok {
		t.Fatalf("BuildReviewCapsule(rev-1): not ok")
	}
	capsulePrompt, err := capsule.Bytes()
	if err != nil {
		t.Fatalf("capsule.Bytes(): %v", err)
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

	code, stderr := callGate(t, toolPayload(t, "Agent", agentInput("reviewer", string(capsulePrompt))))
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
	code, _ = callGate(t, toolPayload(t, "Agent", agentInput("reviewer", string(capsulePrompt))))
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

// TestDelegationSaveFailureLeavesDiskUnchanged — review round 2, R1: the
// delegation transition is ONE reducer call with ONE Save. Inject a Save
// failure (session dir made non-writable; ReadDir still works, so the gate
// reaches apply) and the disk must end exactly where it started: review
// PREPARED, counters zero — retry after repair succeeds.
func TestDelegationSaveFailureLeavesDiskUnchanged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not block writes")
	}
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r, "context")
	if _, err := r.RequestReview(shared.Envelope{SchemaVersion: "2", RunID: "run-1", ReviewID: "rev-1"}, nil); err != nil {
		t.Fatalf("RequestReview: %v", err)
	}
	capsule, ok := shared.BuildReviewCapsule(r, "rev-1", r.Reviews[0].ActiveAttemptID)
	if !ok {
		t.Fatalf("BuildReviewCapsule(rev-1): not ok")
	}
	capsulePrompt, err := capsule.Bytes()
	if err != nil {
		t.Fatalf("capsule.Bytes(): %v", err)
	}

	dir := filepath.Join(os.Getenv("SUMMONER_STATE_DIR"), shared.ProjectKey(), gateSession)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Skipf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	code, stderr := callGate(t, toolPayload(t, "Agent", agentInput("reviewer", string(capsulePrompt))))
	wantExit2(t, code, stderr, "")
	_ = os.Chmod(dir, 0o755) // repair for the reload check (Cleanup chmod is idempotent)

	after := reloadRun(t, "run-1")
	if after.Reviews[0].Status != shared.ReviewPrepared {
		t.Fatalf("disk review = %s, want PREPARED — a failed Save must leave the disk unchanged", after.Reviews[0].Status)
	}
	if after.Counters.Delegations != 0 || after.Counters.PhaseToolCalls != 0 {
		t.Fatalf("disk counters = %+v, want zero", after.Counters)
	}

	// Retry after repair: end-to-end success.
	code, _ = callGate(t, toolPayload(t, "Agent", agentInput("reviewer", string(capsulePrompt))))
	if code != 0 {
		t.Fatalf("retry after repair: exit %d, want 0", code)
	}
	after = reloadRun(t, "run-1")
	if after.Reviews[0].Status != shared.ReviewRunning || after.Counters.Delegations != 1 {
		t.Fatalf("retry: review=%s delegations=%d", after.Reviews[0].Status, after.Counters.Delegations)
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
		// R2: required field TYPES, not just presence.
		{"Write content numeric", "Write", map[string]interface{}{"file_path": "/tmp/x", "content": 42}},
		{"Write content missing", "Write", map[string]interface{}{"file_path": "/tmp/x"}},
		{"Edit old_string numeric", "Edit", map[string]interface{}{"file_path": "/tmp/x", "old_string": 1, "new_string": "b"}},
		{"Edit new_string missing", "Edit", map[string]interface{}{"file_path": "/tmp/x", "old_string": "a"}},
		{"NotebookEdit new_source numeric", "NotebookEdit", map[string]interface{}{"notebook_path": "/tmp/n", "new_source": 7}},
	}
	for _, tc := range cases {
		code, stderr := callGate(t, toolPayload(t, tc.toolName, tc.toolInput))
		if code != 2 {
			t.Errorf("%s: exit %d, want 2 (fail-closed); stderr %q", tc.name, code, stderr)
		}
	}

	// Well-typed payloads of the same tools pass shape validation (Write/Edit
	// additionally admit with their §4 declaration; NotebookEdit is denied
	// pre-effect — no supported mutation declaration — shape notwithstanding).
	for _, ok := range []struct {
		name      string
		toolName  string
		toolInput map[string]interface{}
		wantDeny  string // "" = exit 0
	}{
		{"Write", "Write", writeInput(), ""},
		{"Edit", "Edit", editInput(), ""},
		{"NotebookEdit", "NotebookEdit", notebookInput(), "no supported"},
	} {
		code, stderr := callGate(t, toolPayload(t, ok.toolName, ok.toolInput))
		if ok.wantDeny == "" && code != 0 {
			t.Errorf("%s: exit %d, want 0 (stderr %q)", ok.name, code, stderr)
		}
		if ok.wantDeny != "" && code == 0 {
			t.Errorf("%s: exit 0, want denial", ok.name)
		}
	}

	// Empty tool_name denies even though nothing else is known about the call.
	raw := mustJSONStr(t, map[string]interface{}{
		"session_id": gateSession, "tool_use_id": "tu-x", "tool_input": map[string]interface{}{},
	})
	if code, _ := callGate(t, raw); code != 2 {
		t.Fatalf("empty tool_name: exit %d, want 2", code)
	}

	// Unknown tool NAMES are denied: unmodeled tools (MCP writes included) have
	// no supported read-only or mutation/probe declaration — C2's default-allow
	// was an iron-law bypass.
	code, _ := callGate(t, toolPayload(t, "FancyMcpTool", map[string]interface{}{"whatever": 1}))
	if code != 2 {
		t.Fatalf("unknown tool name with arbitrary shape: exit %d, want 2", code)
	}
}

// TestGateReadOnlyToolAdmission pins the zero-touch tool surface (user ruling
// 2026-11-16): LSP navigation is first-party read-only and admitted like
// Read/Grep; a tool the project's manifest declares in
// tool_scope.allowed_tools is admitted as a read; everything else unmodeled
// stays denied — including an undeclared MCP tool, and including a DECLARED
// tool once the manifest breaks (fail-closed: a broken manifest never
// broadens admission).
func TestGateReadOnlyToolAdmission(t *testing.T) {
	useGateEnv(t)
	writeGateManifest(t, `version: "2"
project:
  name: gate-project
phases:
  fix:
    skill: none
tool_scope:
  allowed_tools: ["mcp__graph__search_code"]
`)
	r := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r, "context")

	code, stderr := callGate(t, toolPayload(t, "LSP", map[string]interface{}{"operation": "workspaceSymbol", "query": "Example"}))
	if code != 0 {
		t.Fatalf("LSP navigation is read-only and must be admitted: exit %d stderr %q", code, stderr)
	}
	code, stderr = callGate(t, toolPayload(t, "mcp__graph__search_code", map[string]interface{}{"query": "Example"}))
	if code != 0 {
		t.Fatalf("manifest-declared read-only tool must be admitted: exit %d stderr %q", code, stderr)
	}
	code, _ = callGate(t, toolPayload(t, "mcp__graph__write_file", map[string]interface{}{"path": "/tmp/x", "content": "y"}))
	if code != 2 {
		t.Fatalf("undeclared MCP tool must stay denied: exit %d", code)
	}
	// A broken manifest never broadens admission: the declared tool reverts
	// to default-deny the moment the manifest fails to load.
	writeGateManifest(t, "version: \"2\"\nphases:\n  fix:\n    skill: none\nworkflows: {}\n")
	code, _ = callGate(t, toolPayloadID(t, "mcp__graph__search_code", "tu-retry", map[string]interface{}{"query": "Example"}))
	if code != 2 {
		t.Fatalf("declared tool under a BROKEN manifest must fail closed: exit %d", code)
	}
}

// writeGateManifest plants summoner.yaml in the current gate project dir.
func writeGateManifest(t *testing.T, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(shared.ProjectDir(), "summoner.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- review round 1: M2 — shape and workflow coverage ---------------------------

func TestNotebookEditGate(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r, "context")

	// §4 (recovery round I3): notebook edits have NO supported mutation
	// declaration — denied BEFORE effects at every status, so even the
	// diagnosis iron law is never reached (the denial is capability-shaped,
	// not workflow-shaped).
	code, stderr := callGate(t, toolPayload(t, "NotebookEdit", notebookInput()))
	wantExit2(t, code, stderr, "no supported")

	acceptPhase(t, r, "diagnosis")
	code, stderr = callGate(t, toolPayload(t, "NotebookEdit", notebookInput()))
	wantExit2(t, code, stderr, "no supported")
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

// --- summoner:change (requirement-change channel, T10) ---------------------------

const skillChangeUnderTest = "summoner:change"

// changeOpenInput builds the Skill(summoner:change) tool_input for subcommand
// open: note (what the user wants changed) + position (the model-proposed
// development-position freeze).
func changeOpenInput(t *testing.T, note, currentTask, testProgress string, pending []string) map[string]interface{} {
	t.Helper()
	return map[string]interface{}{
		"skill": skillChangeUnderTest,
		"args": mustJSONStr(t, map[string]interface{}{
			"subcommand": "open",
			"note":       note,
			"position": map[string]interface{}{
				"current_task":          currentTask,
				"test_progress":         testProgress,
				"pending_verifications": pending,
			},
		}),
	}
}

func changeApplyInput(t *testing.T, note string, changed, affected []string) map[string]interface{} {
	t.Helper()
	return map[string]interface{}{
		"skill": skillChangeUnderTest,
		"args": mustJSONStr(t, map[string]interface{}{
			"subcommand": "apply",
			"note":       note,
			"changed":    changed,
			"affected":   affected,
		}),
	}
}

// openMidPhaseChange drives the gate through an ACTIVE mid-phase change open
// and returns the reloaded run.
func openMidPhaseChange(t *testing.T) *shared.RunState {
	t.Helper()
	r := startFixRun(t, shared.BudgetLimits{})
	if err := r.OpenPhase("context"); err != nil {
		t.Fatal(err)
	}
	code, stderr := callGate(t, toolPayload(t, "Skill", changeOpenInput(t,
		"user wants logout flow covered too",
		"diagnosis: nil deref at login.go:88",
		"repro test drafted, not yet run",
		[]string{"reproduce run", "root-cause evidence digest"})))
	if code != 0 {
		t.Fatalf("change open: exit %d, want 0 (stderr %q)", code, stderr)
	}
	return reloadRun(t, "run-1")
}

func containsIDStr(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// TestChangeOpenFreezesPayloadPosition: the open payload's position — not a
// pre-seeded RunState.DevPosition — is what gets frozen (the runtime channel
// has no other position writer); the gate traces tool.started +
// change.opened with the note and the frozen position.
func TestChangeOpenFreezesPayloadPosition(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	if err := r.OpenPhase("context"); err != nil {
		t.Fatal(err)
	}

	code, stderr := callGate(t, toolPayload(t, "Skill", changeOpenInput(t,
		"user wants logout flow covered too",
		"diagnosis: nil deref at login.go:88",
		"repro test drafted, not yet run",
		[]string{"reproduce run", "root-cause evidence digest"})))
	if code != 0 {
		t.Fatalf("change open: exit %d, want 0 (stderr %q)", code, stderr)
	}

	after := reloadRun(t, "run-1")
	if after.Change == nil {
		t.Fatal("change open must set Change (CHANGE_OPEN)")
	}
	pos := after.Change.SavedPosition
	if pos.CurrentTask != "diagnosis: nil deref at login.go:88" ||
		pos.TestProgress != "repro test drafted, not yet run" ||
		len(pos.PendingVerifications) != 2 || pos.PendingVerifications[0] != "reproduce run" {
		t.Fatalf("SavedPosition = %+v, want the position FROM THE PAYLOAD (non-empty, not pre-seeded state)", pos)
	}
	if after.DevPosition.CurrentTask != "" {
		t.Fatalf("run DevPosition = %+v — the At-freeze must not overwrite the live field", after.DevPosition)
	}
	evs := readGateTrace(t, "run-1")
	got := traceTypes(evs)
	want := []string{shared.EventToolStarted, shared.EventChangeOpened}
	if len(got) != len(want) {
		t.Fatalf("trace types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trace types = %v, want %v", got, want)
		}
	}
	opened := evs[1]
	if opened.Payload["note"] != "user wants logout flow covered too" {
		t.Errorf("change.opened payload = %v, want the note", opened.Payload)
	}
	posPayload, _ := opened.Payload["position"].(map[string]interface{})
	if posPayload == nil || posPayload["current_task"] != "diagnosis: nil deref at login.go:88" {
		t.Errorf("change.opened payload position = %v, want the frozen position", opened.Payload["position"])
	}
}

// TestChangeOpenFromWaitUserCarveOut: summoner:change is the ONLY Skill call
// the gate allows while WAIT_USER (the explicit checkpoint entry, spec §1
// 增补) — it supersedes the pending checkpoint; an ordinary skill stays
// rejected there.
func TestChangeOpenFromWaitUserCarveOut(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	if err := r.OpenPhase("context"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "context", Summary: "context done"}); err != nil {
		t.Fatal(err)
	}
	if reloadRun(t, "run-1").Status != shared.StatusWaitUser {
		t.Fatal("fixture must sit at WAIT_USER")
	}

	// Ordinary skill while WAIT_USER: rejected.
	code, stderr := callGate(t, toolPayload(t, "Skill", skillInput("project-diagnosis-skill", "")))
	wantExit2(t, code, stderr, "WAIT_USER")

	// summoner:change open while WAIT_USER: the carve-out — allowed, and the
	// pending checkpoint is superseded (M2).
	code, stderr = callGate(t, toolPayload(t, "Skill", changeOpenInput(t,
		"checkpoint-time scope change", "context review", "n/a", nil)))
	if code != 0 {
		t.Fatalf("change open from WAIT_USER: exit %d, want 0 (stderr %q)", code, stderr)
	}
	after := reloadRun(t, "run-1")
	if after.Change == nil || after.Change.PriorStatus != shared.StatusWaitUser {
		t.Fatalf("change = %+v, want PriorStatus WAIT_USER", after.Change)
	}
	if cp := after.Checkpoints[0]; !cp.Superseded {
		t.Fatalf("the pending checkpoint must be superseded by the change, got %+v", cp)
	}
}

// TestChangeApplyOpensConfirmCheckpoint: apply runs ApplyRequirementDelta
// (CHANGE_OPEN only), bumps the requirements version, marks the delta ids
// stale and opens the single-point confirm checkpoint; the trace carries
// change.delta_applied with the impact (requirements_version + newly stale).
func TestChangeApplyOpensConfirmCheckpoint(t *testing.T) {
	useGateEnv(t)
	openMidPhaseChange(t)

	code, stderr := callGate(t, toolPayload(t, "Skill", changeApplyInput(t,
		"scope: login → login+logout", []string{"crit-root", "crit-repro"}, []string{"crit-evid"})))
	if code != 0 {
		t.Fatalf("change apply: exit %d, want 0 (stderr %q)", code, stderr)
	}
	after := reloadRun(t, "run-1")
	if after.RequirementsVersion != 2 {
		t.Fatalf("requirements version = %d, want 2", after.RequirementsVersion)
	}
	for _, id := range []string{"crit-root", "crit-repro", "crit-evid"} {
		if !containsIDStr(after.Stale, id) {
			t.Fatalf("Stale = %v, want %s staled by the delta", after.Stale, id)
		}
	}
	if after.Status != shared.StatusWaitUser {
		t.Fatalf("status = %s, want WAIT_USER at the change-confirm checkpoint", after.Status)
	}
	cp := after.CurrentCheckpoint()
	if cp == nil || cp.Kind != shared.CheckpointChangeConfirm || !after.Change.Applied {
		t.Fatalf("confirm checkpoint = %+v change = %+v, want CHANGE_CONFIRM + applied", cp, after.Change)
	}

	evs := readGateTrace(t, "run-1")
	got := traceTypes(evs)
	want := []string{
		shared.EventToolStarted, shared.EventChangeOpened, // open
		shared.EventToolStarted, shared.EventChangeDeltaApplied, // apply
	}
	if len(got) != len(want) {
		t.Fatalf("trace types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trace types = %v, want %v", got, want)
		}
	}
	applied := evs[3]
	if v, _ := applied.Payload["requirements_version"].(float64); v != 2 {
		t.Errorf("delta_applied requirements_version = %v, want 2", applied.Payload["requirements_version"])
	}
	stale, _ := applied.Payload["newly_stale"].([]interface{})
	if len(stale) != 3 {
		t.Errorf("delta_applied newly_stale = %v, want the 3 delta ids", applied.Payload["newly_stale"])
	}
}

// TestChangeChannelRejectsUntraced: malformed payloads and illegal states are
// exit-2 denials ONLY — no trace event (standing ruling: non-phase-submit
// denials are untraced), no state change.
func TestChangeChannelRejectsUntraced(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T) *shared.RunState
		input map[string]interface{}
		want  string
	}{
		{
			name:  "args absent",
			setup: func(t *testing.T) *shared.RunState { return startFixRun(t, shared.BudgetLimits{}) },
			input: map[string]interface{}{"skill": skillChangeUnderTest},
			want:  "args",
		},
		{
			name:  "args not JSON",
			setup: func(t *testing.T) *shared.RunState { return startFixRun(t, shared.BudgetLimits{}) },
			input: map[string]interface{}{"skill": skillChangeUnderTest, "args": "not-json{"},
			want:  "not valid JSON",
		},
		{
			name:  "unknown subcommand",
			setup: func(t *testing.T) *shared.RunState { return startFixRun(t, shared.BudgetLimits{}) },
			input: map[string]interface{}{"skill": skillChangeUnderTest, "args": `{"subcommand":"close","note":"x"}`},
			want:  "subcommand",
		},
		{
			name:  "open missing position",
			setup: func(t *testing.T) *shared.RunState { return startFixRun(t, shared.BudgetLimits{}) },
			input: map[string]interface{}{"skill": skillChangeUnderTest, "args": `{"subcommand":"open","note":"x"}`},
			want:  "position",
		},
		{
			name:  "open null position",
			setup: func(t *testing.T) *shared.RunState { return startFixRun(t, shared.BudgetLimits{}) },
			input: map[string]interface{}{"skill": skillChangeUnderTest, "args": `{"subcommand":"open","note":"x","position":null}`},
			want:  "position",
		},
		{
			name:  "open empty note",
			setup: func(t *testing.T) *shared.RunState { return startFixRun(t, shared.BudgetLimits{}) },
			input: map[string]interface{}{"skill": skillChangeUnderTest, "args": `{"subcommand":"open","note":"","position":{"current_task":"t"}}`},
			want:  "note",
		},
		{
			name:  "open unknown field (strict decode)",
			setup: func(t *testing.T) *shared.RunState { return startFixRun(t, shared.BudgetLimits{}) },
			input: map[string]interface{}{"skill": skillChangeUnderTest, "args": `{"subcommand":"open","note":"x","position":{"current_task":"t"},"changed":["c"]}`},
			want:  "unknown field",
		},
		{
			name:  "open position unknown field",
			setup: func(t *testing.T) *shared.RunState { return startFixRun(t, shared.BudgetLimits{}) },
			input: map[string]interface{}{"skill": skillChangeUnderTest, "args": `{"subcommand":"open","note":"x","position":{"current_task":"t","bogus":1}}`},
			want:  "unknown field",
		},
		{
			name:  "open empty current_task (reducer rejects)",
			setup: func(t *testing.T) *shared.RunState { return startFixRun(t, shared.BudgetLimits{}) },
			input: map[string]interface{}{"skill": skillChangeUnderTest, "args": `{"subcommand":"open","note":"x","position":{"current_task":"","test_progress":"","pending_verifications":[]}}`},
			want:  "current_task",
		},
		{
			name: "open while change already open",
			setup: func(t *testing.T) *shared.RunState {
				r := startFixRun(t, shared.BudgetLimits{})
				r.DevPosition = shared.DevPosition{CurrentTask: "t"}
				if err := r.OpenRequirementChange("first"); err != nil {
					t.Fatal(err)
				}
				return r
			},
			input: changeOpenInput(t, "second change", "t2", "", nil),
			want:  "already open",
		},
		{
			name:  "apply with no change open",
			setup: func(t *testing.T) *shared.RunState { return startFixRun(t, shared.BudgetLimits{}) },
			input: changeApplyInput(t, "delta", []string{"crit-1"}, nil),
			want:  "no requirement change open",
		},
		{
			name: "apply unknown field",
			setup: func(t *testing.T) *shared.RunState {
				r := startFixRun(t, shared.BudgetLimits{})
				r.DevPosition = shared.DevPosition{CurrentTask: "t"}
				if err := r.OpenRequirementChange("opened"); err != nil {
					t.Fatal(err)
				}
				return r
			},
			input: map[string]interface{}{"skill": skillChangeUnderTest, "args": `{"subcommand":"apply","note":"d","changed":["c"],"position":{"current_task":"t"}}`},
			want:  "unknown field",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useGateEnv(t)
			tc.setup(t)
			code, stderr := callGate(t, toolPayload(t, "Skill", tc.input))
			wantExit2(t, code, stderr, tc.want)
			if evs := readGateTrace(t, "run-1"); len(evs) != 0 {
				t.Fatalf("change-channel denials must stay untraced, got %v", traceTypes(evs))
			}
		})
	}
}

// TestChangeRequiresRun: summoner:change with no live run is denied like
// summoner:phase-submit — the protocol channel cannot run without a run.
func TestChangeRequiresRun(t *testing.T) {
	useGateEnv(t)
	code, stderr := callGate(t, toolPayload(t, "Skill", changeOpenInput(t, "note", "task", "", nil)))
	wantExit2(t, code, stderr, "requires an active run")
}

// TestWaitUserSkillDenyKeepsSubmitPath: while WAIT_USER, phase-submit keeps
// its own rejection path (SubmitPhase refuses it, traced as
// phase_submitted.rejected) — the WAIT_USER skill deny must not shadow it.
func TestWaitUserSkillDenyKeepsSubmitPath(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	if err := r.OpenPhase("context"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "context", Summary: "context done"}); err != nil {
		t.Fatal(err)
	}
	if reloadRun(t, "run-1").Status != shared.StatusWaitUser {
		t.Fatal("fixture must sit at WAIT_USER")
	}

	code, stderr := callGate(t, toolPayload(t, "Skill", skillInput("summoner:phase-submit", submitArgs(t, "diagnosis"))))
	wantExit2(t, code, stderr, "")
	evs := readGateTrace(t, "run-1")
	if got := traceTypes(evs); got[len(got)-1] != shared.EventPhaseSubmittedRejected {
		t.Fatalf("WAIT_USER phase-submit must end with phase_submitted.rejected, got %v", got)
	}
}

// TestStatusSkillAllowedWhileWaitUser — the read-only status viewer is the
// re-render surface at a pending checkpoint (user report 2026-11-19: the
// model flailed at WAIT_USER because Skill(summoner:status) was blocked and
// a bare binary invocation then failed on empty stdin). summoner:status is
// a framework verb with no state effect — it must pass the WAIT_USER
// discipline, while work skills stay blocked.
func TestStatusSkillAllowedWhileWaitUser(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r, "context")
	acceptPhase(t, r, "diagnosis")
	if err := r.OpenPhase("reproduce"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	if _, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "reproduce"}); err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	} // WAIT_USER

	code, stderr := callGate(t, toolPayload(t, "Skill", map[string]interface{}{"skill": "summoner:status"}))
	if code != 0 {
		t.Fatalf("Skill(summoner:status) at WAIT_USER: exit = %d, want 0 (stderr: %q)", code, stderr)
	}

	// Work skills stay blocked at the checkpoint.
	code, stderr = callGate(t, toolPayload(t, "Skill", map[string]interface{}{"skill": "superpowers:brainstorming"}))
	wantExit2(t, code, stderr, "WAIT_USER")

	// Ship review F2: the status skill must ALSO pass the BUDGET gate at an
	// exhausted WAIT_USER checkpoint — decide() checks budgetControlExempt
	// before routing, and the 0.5.2 exemption list named only the exact Bash
	// status spellings, so Skill(summoner:status) was still denied exactly
	// when the user needs the render that shows the extension offer.
	r2 := reloadRun(t, "run-1")
	r2.Limits = shared.BudgetLimits{PhaseToolCalls: 1}
	r2.Counters = shared.BudgetCounters{PhaseToolCalls: 1}
	if err := r2.Save(); err != nil {
		t.Fatal(err)
	}
	code, stderr = callGate(t, toolPayload(t, "Skill", map[string]interface{}{"skill": "summoner:status"}))
	if code != 0 {
		t.Fatalf("Skill(summoner:status) at exhausted WAIT_USER: exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	// And a work skill at exhausted WAIT_USER still hits the budget wall.
	code, stderr = callGate(t, toolPayload(t, "Skill", map[string]interface{}{"skill": "superpowers:brainstorming"}))
	wantExit2(t, code, stderr, "budget")
}
