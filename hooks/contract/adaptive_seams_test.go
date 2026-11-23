package contract

// adaptive_seams_test.go — guards the 0.7.0 ship review found pinned at ONE
// seam only (in-process unit tests). The design names the hook binaries'
// stdin/stdout as the seam; each test here drives the real artifacts.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// legacyDigest is CheckpointContentDigest as the v0.6.1 tag states it
// (git show v0.6.1:hooks/shared/reducer.go), restated here so the upgrade
// test never asks the code under test whether its own digest is unchanged.
// The literal below was computed by that tagged code.
func legacyDigest(cp shared.CheckpointState, requirementsVersion int) string {
	limitsKey := func(l shared.BudgetLimits) string {
		return fmt.Sprintf("phase_tool_calls=%d,failed_checks=%d,run_rework=%d,delegations=%d",
			l.PhaseToolCalls, l.FailedChecks, l.RunRework, l.Delegations)
	}
	h := sha256.New()
	fmt.Fprintf(h, "summoner-checkpoint/v1\nid=%s\nnonce=%s\nkind=%s\nphase=%s\nreq=%d\nnote=%s\n",
		cp.ID, cp.Nonce, cp.Kind, cp.PhaseID, requirementsVersion, cp.Note)
	criteria := append([]string(nil), cp.CriteriaIDs...)
	sort.Strings(criteria)
	for _, c := range criteria {
		fmt.Fprintf(h, "criterion=%s\n", c)
	}
	paths := make([]string, 0, len(cp.ArtifactVersions))
	for p := range cp.ArtifactVersions {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		fmt.Fprintf(h, "artifact=%s@%s\n", p, cp.ArtifactVersions[p])
	}
	if cp.BudgetAsk != nil {
		fmt.Fprintf(h, "budget_ask=%s|%s\n", limitsKey(cp.BudgetAsk.Proposal.Limits), cp.BudgetAsk.Proposal.Source)
	}
	if cp.ExtendAsk != nil {
		fmt.Fprintf(h, "extend_ask=%s\n", limitsKey(cp.ExtendAsk.Delta))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestLegacyDigestRestatementIsTheTaggedCode — the restatement above agrees
// with the value the v0.6.1 reducer computed for this fixture (verified
// against the tagged source during the 0.7.0 ship review).
func TestLegacyDigestRestatementIsTheTaggedCode(t *testing.T) {
	legacy := shared.CheckpointState{
		ID: "cp-4", Kind: shared.CheckpointPhase, PhaseID: "diagnosis", Note: "legacy note",
		Nonce: "deadbeef", CriteriaIDs: []string{"crit-b", "crit-a"},
		ArtifactVersions: map[string]string{"b.md": "2", "a.md": "1"},
	}
	const want = "24e1085649f765d85760b66cc7fa5616822867e3abe0741778f6ab16de1eaf6f"
	if got := legacyDigest(legacy, 3); got != want {
		t.Fatalf("the restated v0.6.1 digest = %s, want the tagged code's value %s", got, want)
	}
	if got := shared.CheckpointContentDigest(legacy, 3); got != want {
		t.Fatalf("current digest of a pre-0.7.0 checkpoint = %s, want the v0.6.1 value %s", got, want)
	}
}

func runStateFile(t *testing.T, runID string) string {
	t.Helper()
	// The layout shared.runStatePath builds (unexported): root/project/session/run.
	return filepath.Join(os.Getenv("SUMMONER_STATE_DIR"), shared.ProjectKey(), contractSession, runID+".json")
}

// strip070Fields rewrites a run-state file the way a 0.6.1 hook left it: none
// of the fields 0.7.0 added.
func strip070Fields(t *testing.T, runID string) {
	t.Helper()
	path := runStateFile(t, runID)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc, "continue_nudge_for")
	cps, _ := doc["checkpoints"].([]interface{})
	for _, raw := range cps {
		cp := raw.(map[string]interface{})
		delete(cp, "outcome")
		delete(cp, "conclusion")
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAdaptivePre070StateThroughTheNewBinaries — a run parked at a DISPLAYED
// checkpoint by 0.6.1, resumed after the upgrade: the new Stop hook asks
// (without re-binding: the v0.6.1 digest is the one it computes), the new
// gate admits the question, the new PostToolUse records the answer. This is
// the path every live run takes the moment the plugin updates.
func TestAdaptivePre070StateThroughTheNewBinaries(t *testing.T) {
	useContractEnv(t)
	seedLiveRun(t, "run-old", "fix")
	submitPhaseViaGate(t, "tu-ps-1", "context", nil)
	strip070Fields(t, "run-old")

	run, err := shared.LoadRun("run-old")
	if err != nil {
		t.Fatal(err)
	}
	cp := *run.CurrentCheckpoint()
	if cp.Outcome != "" || cp.Conclusion != nil {
		t.Fatalf("fixture: the checkpoint must carry no 0.7.0 field, got outcome %q conclusion %v", cp.Outcome, cp.Conclusion)
	}
	// 0.6.1's Stop hook bound the display with ITS digest.
	if err := run.MarkDisplayed(cp.ID, legacyDigest(cp, run.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	parked, err := os.ReadFile(runStateFile(t, "run-old"))
	if err != nil {
		t.Fatal(err)
	}

	code, out, stderr := runBinary(t, "stop", stopRenderPayload(t, "等你决定。"), nil)
	if code != 0 || !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("an upgraded displayed checkpoint owes the question: (code, stdout, stderr) = (%d, %q, %q)", code, out, stderr)
	}
	if i := strings.Index(out, `"reason":`); i < 0 || !strings.Contains(out[i:], cp.Nonce) {
		t.Fatalf("the ask block must carry the nonce %s:\n%s", cp.Nonce, out)
	}
	ask := out
	if i := strings.Index(out, `"reason":`); i >= 0 {
		ask = out[i:]
	}
	for _, label := range shared.DecisionPage(cp) {
		if !strings.Contains(ask, label) {
			t.Errorf("the ask block must offer the persisted page-1 label %q:\n%s", label, out)
		}
	}
	if after, _ := os.ReadFile(runStateFile(t, "run-old")); string(after) != string(parked) {
		t.Fatal("the Stop hook rewrote the state: the v0.6.1-bound digest differs from the one 0.7.0 computes (the display was re-bound)")
	}

	page := shared.DecisionPage(cp)
	if code, _, stderr := runBinary(t, "pretooluse-gate", askQuestionPayload(t, "tu-ask-1", cp.Nonce, page), nil); code != 0 {
		t.Fatalf("gate: exit %d, want 0 (stderr: %q)", code, stderr)
	}
	code, out, stderr = runBinary(t, "posttooluse", mustJSON(t, map[string]interface{}{
		"hook_event_name": "PostToolUse",
		"session_id":      contractSession,
		"tool_name":       "AskUserQuestion",
		"tool_use_id":     "tu-ask-1",
		"tool_input":      askQuestionInput(page, cp.Nonce),
		"tool_response":   map[string]interface{}{"answers": map[string]interface{}{askQuestionText(cp.Nonce): page[0]}},
	}), nil)
	if code != 0 || strings.Contains(out, "block") {
		t.Fatalf("the answer must land against the v0.6.1 binding: (code, stdout, stderr) = (%d, %q, %q)", code, out, stderr)
	}
	if run, err = shared.LoadRun("run-old"); err != nil {
		t.Fatal(err)
	}
	if run.Status != shared.StatusActive {
		t.Fatalf("status after CONTINUE = %s, want ACTIVE", run.Status)
	}
	if decided := run.Checkpoints[len(run.Checkpoints)-1]; !decided.Decided || decided.Decision != shared.DecisionContinue || decided.DecisionToolUseID != "tu-ask-1" {
		t.Fatalf("the checkpoint must be decided CONTINUE via tu-ask-1, got %+v", decided)
	}
	if n := len(eventsOfType(allTraceEvents(t), shared.EventCheckpointDecisionRejected)); n != 0 {
		t.Fatalf("decision attempts rejected on the upgraded path = %d, want 0", n)
	}
}

// TestAdaptiveReviewerEnumCaseOverBinaries — the incident's lost review: a
// criterion status spelled in uppercase reached the subagentstop binary and
// failed the whole review. The binary now reads the enums by meaning.
func TestAdaptiveReviewerEnumCaseOverBinaries(t *testing.T) {
	useContractEnv(t)
	runID := contractRunAtReviewPending(t, []string{"crit-ctx"}, []string{"crit-diag"})
	if code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Skill", "tu-rs-1", map[string]interface{}{
		"skill": "summoner:review-submit",
		"args":  contractEnvelopeJSON(t, runID, "rev-1"),
	}), nil); code != 0 {
		t.Fatalf("review-submit exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Agent", "tu-ag-1", map[string]interface{}{
		"subagent_type": "reviewer",
		"prompt":        contractReviewerCapsule(t, runID),
		"description":   "independent review",
	}), nil); code != 0 {
		t.Fatalf("reviewer Agent exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	result := contractPassResult(t, runID)
	for i := range result.CriterionVerdicts {
		result.CriterionVerdicts[i].Status = " PASS " // the incident's spelling
	}
	result.RunStatus, result.Verdict = "Completed", "Pass"
	resJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runBinary(t, "subagentstop", mustJSON(t, map[string]interface{}{
		"hook_event_name":       "SubagentStop",
		"session_id":            contractSession,
		"stop_hook_active":      false,
		"agent_id":              "ag-rev-1",
		"agent_type":            "reviewer",
		"agent_transcript_path": contractReviewerTranscript(t, runID, string(resJSON)),
	}), nil); code != 0 {
		t.Fatalf("subagentstop exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	run, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if rv := run.Reviews[0]; rv.Status != shared.ReviewReviewed || rv.Verdict != shared.VerdictPass {
		t.Fatalf("a verdict whose enums differ only in case must conclude the review, got %s / %q", rv.Status, rv.Verdict)
	}
	if cp := run.CurrentCheckpoint(); cp == nil || cp.Kind != shared.CheckpointReviewResult {
		t.Fatalf("after the verdict the run must sit at the REVIEW_RESULT checkpoint, got %+v", cp)
	}
}

// TestAdaptiveSessionExportOverBinaries — SessionStart writes the session id
// into the environment file; summoner-status scopes `{}` to it over a real
// subprocess. With two live runs in two sessions the scoped view names the
// other run; without the variable the viewer reports the ambiguity.
func TestAdaptiveSessionExportOverBinaries(t *testing.T) {
	useContractEnv(t)
	envFile := filepath.Join(t.TempDir(), "claude.env")
	if code, _, stderr := runBinary(t, "session-start", sessionStartPayload(t), map[string]string{"CLAUDE_ENV_FILE": envFile}); code != 0 {
		t.Fatalf("session-start exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	content, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "export SUMMONER_SESSION_ID='sess-contract'\n" {
		t.Fatalf("environment file = %q, want the session id export", content)
	}

	for _, s := range []struct{ run, session string }{{"run-a", "sess-a"}, {"run-b", "sess-b"}} {
		if _, err := shared.StartRun(s.run, s.session, "fix", shared.BudgetLimits{}); err != nil {
			t.Fatalf("StartRun(%s): %v", s.run, err)
		}
	}
	code, out, stderr := runBinary(t, "summoner-status", `{}`, map[string]string{"SUMMONER_SESSION_ID": "sess-a"})
	if code != 0 || !strings.Contains(out, "⚡ SUMMONER STATUS — run run-a") || !strings.Contains(out, "other live runs in this project: run-b") {
		t.Fatalf("the exported session id must scope the view: (code, stderr) = (%d, %q)\n%s", code, stderr, out)
	}
	code, out, stderr = runBinary(t, "summoner-status", `{}`, nil)
	if code != 0 || !strings.Contains(out, "2 live Summoner runs — ambiguous") {
		t.Fatalf("without the exported id the project-wide fallback stands: (code, stderr) = (%d, %q)\n%s", code, stderr, out)
	}
	// An exported id with nothing live under it is no evidence of "no run":
	// the project-wide fallback stands (it may be an earlier session's id).
	code, out, stderr = runBinary(t, "summoner-status", `{}`, map[string]string{"SUMMONER_SESSION_ID": "sess-gone"})
	if code != 0 || !strings.Contains(out, "2 live Summoner runs — ambiguous") {
		t.Fatalf("a stale exported id must fall back to the project-wide view: (code, stderr) = (%d, %q)\n%s", code, stderr, out)
	}
}

// TestAdaptiveDiagnosisNeedsConclusionOverBinaries — the gate binary refuses
// a diagnosis that states no conclusion and names the missing field; nothing
// is consumed but the rejection trace.
func TestAdaptiveDiagnosisNeedsConclusionOverBinaries(t *testing.T) {
	useContractEnv(t)
	seedLiveRun(t, "run-diag", "fix")
	submitPhaseViaGate(t, "tu-ps-1", "context", nil)
	run, err := shared.LoadRun("run-diag")
	if err != nil {
		t.Fatal(err)
	}
	renderAndDecide(t, "run-diag", "tu-ask-1", run.CurrentCheckpoint(), 0,
		func(labels []string) string { return firstLabelWithKeyword(labels, "continue") })
	if run, err = shared.LoadRun("run-diag"); err != nil {
		t.Fatal(err)
	}
	// diagnosis is not open yet: the next submit opens it (the refusal must
	// come before that, so a refused diagnosis leaves no opened phase).
	if run.CurrentCheckpoint() != nil {
		t.Fatalf("fixture: nothing may be pending after the context decision, got %+v", run.CurrentCheckpoint())
	}

	for i, row := range []struct{ name, args, reason string }{
		{"no conclusion", `{"phase_id":"diagnosis","summary":"found it"}`, "conclusion.summary"},
		{"summary only", `{"phase_id":"diagnosis","conclusion":{"summary":"root cause X"}}`, "conclusion.approach"},
		{"foreign outcome", `{"phase_id":"diagnosis","outcome":"fixed","conclusion":{"summary":"s","approach":"a"}}`, `outcome "fixed" is not one of`},
	} {
		code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Skill", fmt.Sprintf("tu-ps-%d", i+2), map[string]interface{}{
			"skill": "summoner:phase-submit", "args": row.args,
		}), nil)
		if code != 2 || !strings.Contains(stderr, row.reason) {
			t.Errorf("%s: (exit, stderr) = (%d, %q), want exit 2 naming %q", row.name, code, stderr, row.reason)
		}
	}
	if run, err = shared.LoadRun("run-diag"); err != nil {
		t.Fatal(err)
	}
	if run.Status != shared.StatusActive || run.CurrentCheckpoint() != nil {
		t.Fatalf("rejected submissions must consume nothing: status %s pending %+v", run.Status, run.CurrentCheckpoint())
	}
	if n := len(eventsOfType(allTraceEvents(t), shared.EventPhaseSubmittedRejected)); n != 3 {
		t.Fatalf("phase_submitted.rejected events = %d, want one per refused submission (3)", n)
	}
}

// TestAdaptiveScorerMirrorsTheAutoPassPolicy — "change both together" was
// unenforced: the scorer's Python policy set and the reducer's Go table
// could drift with every fixture staying green. One trace per phase of every
// route (plus an unknown one), built through the real writer, must PASS the
// scorer exactly when the Go policy says the phase may auto-pass.
func TestAdaptiveScorerMirrorsTheAutoPassPolicy(t *testing.T) {
	useContractEnv(t)
	scorer, err := filepath.Abs("../../scorers/deterministic/user-gate-check.sh")
	if err != nil {
		t.Fatal(err)
	}
	var phases []string
	for _, workflow := range []string{"fix", "new", "debug", "review", "ship", "ops", "release"} {
		seq, err := shared.WorkflowPhases(workflow)
		if err != nil {
			t.Fatal(err)
		}
		phases = append(phases, seq...)
	}
	phases = append(phases, "no-such-phase")
	for i, phase := range phases {
		runID := fmt.Sprintf("run-pol-%02d", i)
		for _, ev := range []shared.TraceEvent{
			{Type: shared.EventWorkflowStarted, Source: "userpromptsubmit", Payload: map[string]interface{}{"workflow": "fix"}},
			{Type: shared.EventPhaseStarted, Source: "pretooluse-gate", Payload: map[string]interface{}{"phase_id": phase}},
			{Type: shared.EventPhaseClosed, Source: "pretooluse-gate", Payload: map[string]interface{}{"phase_id": phase}},
			{Type: shared.EventCheckpointOpened, Source: "pretooluse-gate", Payload: map[string]interface{}{"checkpoint_id": "cp-1", "phase_id": phase, "nonce": "n0nc3a01"}},
			{Type: shared.EventCheckpointAuto, Source: "pretooluse-gate", Payload: map[string]interface{}{"checkpoint_id": "cp-1", "phase_id": phase, "outcome": shared.ConclusionConfirmed}},
		} {
			if err := shared.AppendEvent(runID, contractSession, ev); err != nil {
				t.Fatalf("%s: AppendEvent(%s): %v", phase, ev.Type, err)
			}
		}
		trace := filepath.Join(os.Getenv("SUMMONER_TRACE_DIR"), shared.ProjectKey(), runID+".jsonl")
		cmd := exec.Command("bash", scorer, trace)
		scored, _ := cmd.CombinedOutput()
		scorerPass := cmd.ProcessState != nil && cmd.ProcessState.Success()
		if scorerPass != shared.AutoPassablePhase(phase) {
			t.Errorf("phase %q: scorer PASS = %v but the Go policy says auto-passable = %v — the two policy tables disagree:\n%s",
				phase, scorerPass, shared.AutoPassablePhase(phase), scored)
		}
	}
}
