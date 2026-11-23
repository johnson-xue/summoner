package main

// main_test.go — Task 5 TDD suite, Stop half (展示核验 + 停止不对称, spec
// docs/superpowers/specs/2026-11-09-single-loop-checkpoint-design.md §1.3/§1.6/
// §4/§5). Fixtures are built through the real reducer API (StartRun/OpenPhase/
// SubmitPhase); hermeticity comes from the env overrides the shared package
// honors (SUMMONER_STATE_DIR / SUMMONER_TRACE_DIR / CLAUDE_PROJECT_DIR) — the
// same pattern as the gate's and posttooluse's suites (go1.16: no t.Setenv).

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

// useStopEnv redirects v2 state, traces and the project key into fresh temp
// dirs so no test ever touches real Summoner state.
func useStopEnv(t *testing.T) {
	t.Helper()
	setenv16(t, "SUMMONER_STATE_DIR", t.TempDir())
	setenv16(t, "SUMMONER_TRACE_DIR", t.TempDir())
	setenv16(t, "CLAUDE_PROJECT_DIR", t.TempDir())
	unsetenv16(t, "SUMMONER_HOOKS_DISABLE")
}

// callStop runs the whole hook on a payload; returns (exit code, stdout, stderr).
// stdout matters here: the block decision is a stdout JSON object.
func callStop(t *testing.T, payload string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runStop(strings.NewReader(payload), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// mustJSON marshals a payload or fails the test.
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

// waitUserCheckpoint drives one phase cycle up to an OPEN, UNDISPLAYED
// checkpoint: the run sits in WAIT_USER with cp pending — the display-binding
// fixture.
func waitUserCheckpoint(t *testing.T, r *shared.RunState, phaseID string) shared.CheckpointState {
	t.Helper()
	if err := r.OpenPhase(phaseID); err != nil {
		t.Fatalf("OpenPhase(%s): %v", phaseID, err)
	}
	cp, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: phaseID, Summary: phaseID + " done"})
	if err != nil {
		t.Fatalf("SubmitPhase(%s): %v", phaseID, err)
	}
	return cp
}

// displayCheckpoint binds the checkpoint as displayed through the real reducer.
func displayCheckpoint(t *testing.T, r *shared.RunState, cp shared.CheckpointState) {
	t.Helper()
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed(%s): %v", cp.ID, err)
	}
}

// stopPayload builds a Stop payload around one last_assistant_message.
func stopPayload(t *testing.T, lastMsg string) string {
	t.Helper()
	return mustJSON(t, map[string]interface{}{
		"hook_event_name":        "Stop",
		"session_id":             hookSession,
		"stop_hook_active":       false,
		"last_assistant_message": lastMsg,
	})
}

// wantAskBlock asserts stdout is the ask block of cp: it names the bound
// checkpoint, carries the nonce and every page-1 label verbatim, and demands
// no re-render.
func wantAskBlock(t *testing.T, stdout string, cp shared.CheckpointState) string {
	t.Helper()
	blocked, reason := decodeBlock(t, stdout)
	if !blocked {
		t.Fatalf("a bound checkpoint must hand back the ask block, got stdout %q", stdout)
	}
	if !strings.Contains(reason, "已展示并绑定") || !strings.Contains(reason, "AskUserQuestion") {
		t.Fatalf("ask block must say the checkpoint is bound and demand the AskUserQuestion, got %q", reason)
	}
	if strings.Contains(reason, "渲染 checkpoint") {
		t.Fatalf("an already-bound checkpoint must not be asked to re-render, got %q", reason)
	}
	for _, want := range append([]string{cp.ID, cp.Nonce}, shared.DecisionPage(cp)...) {
		if !strings.Contains(reason, want) {
			t.Errorf("ask block lost %q:\n%s", want, reason)
		}
	}
	return reason
}

// renderBlock builds the display-act message: the checkpoint-only CANONICAL
// view (shared.RenderCheckpointMessage — the exact production renderer the
// Stop hook binds by containment; 0.7.0 D2).
func renderBlock(run *shared.RunState, cp shared.CheckpointState) string {
	return shared.RenderCheckpointMessage(run, cp)
}

// decodeBlock decodes the hook's stdout as a block decision; "" stdout is not
// a block (ok=false).
func decodeBlock(t *testing.T, stdout string) (bool, string) {
	t.Helper()
	if strings.TrimSpace(stdout) == "" {
		return false, ""
	}
	var out struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout %q is not a hook JSON object: %v", stdout, err)
	}
	return out.Decision == "block", out.Reason
}

func reloadRun(t *testing.T, runID string) *shared.RunState {
	t.Helper()
	r, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatalf("LoadRun(%s): %v", runID, err)
	}
	return r
}

// readStopTrace returns the run's trace events (nil when no file exists) and
// verifies the hash chain on every read.
func readStopTrace(t *testing.T, runID string) []shared.TraceEvent {
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

func currentCheckpoint(t *testing.T, r *shared.RunState) *shared.CheckpointState {
	t.Helper()
	cp := r.CurrentCheckpoint()
	if cp == nil {
		t.Fatalf("run %s has no current checkpoint: %+v", r.RunID, r.Checkpoints)
	}
	return cp
}

// --- display verification (§1.3 展示核验) -------------------------------------------

// TestStopBlocksUndisplayedCheckpoint: WAIT_USER with an undisplayed checkpoint
// and no render block in last_assistant_message blocks with the render
// instruction naming the checkpoint id and nonce. The block is idempotent: a
// re-fire demands nothing new (identical reason — required because the platform
// overrides the 9th consecutive block and ends the turn).
func TestStopBlocksUndisplayedCheckpoint(t *testing.T) {
	useStopEnv(t)
	r := startRun(t)
	cp := waitUserCheckpoint(t, r, "diagnosis")

	code, stdout, stderr := callStop(t, stopPayload(t, "Diagnosis complete, root cause found."))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 — a block is a decision, not an error (stderr: %q)", code, stderr)
	}
	blocked, reason := decodeBlock(t, stdout)
	if !blocked {
		t.Fatalf("stdout %q must be a block decision", stdout)
	}
	want := fmt.Sprintf("渲染 checkpoint %s (nonce %s) 的规范消息后再结束：消息中须完整包含该规范块（summoner-status view:checkpoint 提供逐字节原文；可在块前写解释，尾行 ╚ 长短不限）。刚粘贴过却仍被要求渲染时，常见原因是整块没有逐字包含——行首缩进、> 引用、``` 代码围栏、任何一行被改写都会让绑定失败；用该命令的输出原样重贴。", cp.ID, cp.Nonce)
	if reason != want {
		t.Fatalf("block reason = %q, want exactly %q", reason, want)
	}

	// State untouched by a block: still WAIT_USER, still undisplayed, kept.
	disk := reloadRun(t, "run-1")
	if disk.Status != shared.StatusWaitUser {
		t.Fatalf("status = %s, want WAIT_USER (block never deletes or advances state)", disk.Status)
	}
	if got := currentCheckpoint(t, disk); got.Displayed || got.DisplayDigest != "" {
		t.Fatalf("checkpoint must stay undisplayed after a block, got %+v", got)
	}

	// Idempotent re-fire: same message → same block, byte-identical reason.
	_, stdout2, _ := callStop(t, stopPayload(t, "Diagnosis complete, root cause found."))
	blocked2, reason2 := decodeBlock(t, stdout2)
	if !blocked2 || reason2 != want {
		t.Fatalf("re-fire block = (%v, %q), want identical (%v, %q)", blocked2, reason2, true, want)
	}
	if evs := readStopTrace(t, "run-1"); len(evs) != 0 {
		t.Fatalf("a pure block must append no trace events, got %v", traceTypesOf(evs))
	}
}

// TestStopBindsTheRenderThenDemandsTheAsk (0.7.0 D2): the render block in
// last_assistant_message IS the display act — MarkDisplayed with the
// canonical content digest plus one checkpoint.displayed event — and the
// hook then BLOCKS with the exact decision question to raise. The user no
// longer types "continue" between a pasted checkpoint and its dialog.
func TestStopBindsTheRenderThenDemandsTheAsk(t *testing.T) {
	useStopEnv(t)
	r := startRun(t)
	cp := waitUserCheckpoint(t, r, "diagnosis")

	code, stdout, stderr := callStop(t, stopPayload(t, renderBlock(r, cp)))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	reason := wantAskBlock(t, stdout, cp)

	disk := reloadRun(t, "run-1")
	got := currentCheckpoint(t, disk)
	if !got.Displayed {
		t.Fatal("the render block must mark the checkpoint displayed")
	}
	if want := shared.CheckpointContentDigest(cp, disk.RequirementsVersion); got.DisplayDigest != want {
		t.Fatalf("display digest = %s, want canonical content digest %s", got.DisplayDigest, want)
	}
	if disk.Status != shared.StatusWaitUser || got.Decided {
		t.Fatalf("status = %s decided = %v, want WAIT_USER undecided (binding advances nothing)", disk.Status, got.Decided)
	}
	evs := readStopTrace(t, "run-1")
	shown := eventsOfType(evs, shared.EventCheckpointDisplayed)
	if len(shown) != 1 {
		t.Fatalf("checkpoint.displayed events = %d, want exactly 1 (trace: %v)", len(shown), traceTypesOf(evs))
	}
	if shown[0].Source != "stop" || shown[0].Payload["checkpoint_id"] != cp.ID || shown[0].Payload["nonce"] != cp.Nonce {
		t.Fatalf("checkpoint.displayed = source %q payload %v, want stop / checkpoint_id %s / nonce %s",
			shown[0].Source, shown[0].Payload, cp.ID, cp.Nonce)
	}

	// Idempotent re-fire: the same message yields the byte-identical ask
	// block (required under the platform's consecutive-block cap) and no
	// second display event.
	_, stdout2, _ := callStop(t, stopPayload(t, renderBlock(r, cp)))
	if _, reason2 := decodeBlock(t, stdout2); reason2 != reason {
		t.Fatalf("re-fire ask block differs:\n%q\n%q", reason, reason2)
	}
	if n := len(eventsOfType(readStopTrace(t, "run-1"), shared.EventCheckpointDisplayed)); n != 1 {
		t.Fatalf("checkpoint.displayed events after re-fire = %d, want 1 (idempotent no-op)", n)
	}

	// Displayed by another path and a plain sign-off: still the ask block —
	// a displayed checkpoint that was never asked is exactly the stall the
	// incident spent 62 minutes in.
	useStopEnv(t)
	r2 := startRun(t)
	cp2 := waitUserCheckpoint(t, r2, "diagnosis")
	displayCheckpoint(t, r2, cp2)
	code, stdout, stderr = callStop(t, stopPayload(t, "waiting for your decision"))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	wantAskBlock(t, stdout, cp2)
	if n := len(eventsOfType(readStopTrace(t, "run-1"), shared.EventCheckpointDisplayed)); n != 0 {
		t.Fatalf("the stop must not duplicate the earlier display binding, got %d events", n)
	}
	if disk := reloadRun(t, "run-1"); disk.Status != shared.StatusWaitUser {
		t.Fatalf("status = %s, want WAIT_USER kept", disk.Status)
	}
}

// TestStopExplanationThenBlockBindsAndAsks: an explanation may precede the
// block. The incident's model wrote one line of root cause in front of a
// byte-perfect block and was blocked for it — whole-message equality made
// "explain, then show" illegal.
func TestStopExplanationThenBlockBindsAndAsks(t *testing.T) {
	for name, msg := range map[string]string{
		"explanation before": "根因已定位：TaskModule.handle() 对 player.SubTask 解引用前未判空。\n证据见下方检查点，请裁决。\n\n",
		"stop word in it":    "复现步骤：先停止服务再启动，错误出现。\n\n",
	} {
		useStopEnv(t)
		r := startRun(t)
		cp := waitUserCheckpoint(t, r, "diagnosis")
		before := len(eventsOfType(readStopTrace(t, "run-1"), shared.EventCheckpointDisplayed))

		code, stdout, stderr := callStop(t, stopPayload(t, msg+renderBlock(r, cp)))
		if code != 0 {
			t.Fatalf("%s: exit = %d, want 0 (stderr: %q)", name, code, stderr)
		}
		wantAskBlock(t, stdout, cp)
		if got := currentCheckpoint(t, reloadRun(t, "run-1")); !got.Displayed {
			t.Fatalf("%s: an explanation in front of the block must not stop the binding", name)
		}
		if n := len(eventsOfType(readStopTrace(t, "run-1"), shared.EventCheckpointDisplayed)) - before; n != 1 {
			t.Fatalf("%s: checkpoint.displayed events added = %d, want 1", name, n)
		}
	}
}

// TestStopBindsWhateverTheTailLooksLike: the tail rule is the one line a
// paste may get wrong without changing what the user read — shorter, longer
// or missing, the block still binds.
func TestStopBindsWhateverTheTailLooksLike(t *testing.T) {
	const canonicalTail = "╚════════════════════════════════════\n"
	for name, tail := range map[string]string{
		"shorter": "╚═══\n",
		"longer":  "╚" + strings.Repeat("═", 60) + "\n",
		"none":    "",
	} {
		useStopEnv(t)
		r := startRun(t)
		cp := waitUserCheckpoint(t, r, "diagnosis")
		full := renderBlock(r, cp)
		if !strings.HasSuffix(full, canonicalTail) {
			t.Fatalf("fixture: the canonical render no longer ends with the expected tail")
		}
		msg := strings.TrimSuffix(full, canonicalTail) + tail

		code, stdout, stderr := callStop(t, stopPayload(t, msg))
		if code != 0 {
			t.Fatalf("%s: exit = %d, want 0 (stderr: %q)", name, code, stderr)
		}
		wantAskBlock(t, stdout, cp)
		if got := currentCheckpoint(t, reloadRun(t, "run-1")); !got.Displayed {
			t.Fatalf("%s tail: the block must bind", name)
		}
	}
}

// TestStopStaleAndCurrentBlockBindOnlyTheCurrent: an old-nonce block pasted
// next to the current one binds the CURRENT checkpoint only — the stale one
// stays undisplayed and undecidable.
func TestStopStaleAndCurrentBlockBindOnlyTheCurrent(t *testing.T) {
	useStopEnv(t)
	r := startRun(t)
	old := waitUserCheckpoint(t, r, "diagnosis")
	stale := renderBlock(r, old)
	// A WAIT_USER-entered requirement change supersedes cp-1 and opens the
	// change-confirm checkpoint cp-2.
	if err := r.OpenRequirementChange("cover the logout flow too"); err != nil {
		t.Fatalf("OpenRequirementChange: %v", err)
	}
	if _, err := r.ApplyRequirementDelta(shared.RequirementDelta{Note: "add logout", Changed: []string{"crit-a"}}); err != nil {
		t.Fatalf("ApplyRequirementDelta: %v", err)
	}
	cur := r.CurrentCheckpoint()
	if cur == nil || cur.ID == old.ID {
		t.Fatalf("fixture must have a successor checkpoint, got %+v", cur)
	}

	code, stdout, stderr := callStop(t, stopPayload(t, stale+"\n"+renderBlock(r, *cur)))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	wantAskBlock(t, stdout, *cur)
	disk := reloadRun(t, "run-1")
	for _, c := range disk.Checkpoints {
		switch c.ID {
		case old.ID:
			if c.Displayed || !c.Superseded {
				t.Fatalf("the stale block must bind nothing: cp %s displayed = %v superseded = %v", c.ID, c.Displayed, c.Superseded)
			}
		case cur.ID:
			if !c.Displayed {
				t.Fatalf("the current block must bind cp %s", c.ID)
			}
		}
	}
	if n := len(eventsOfType(readStopTrace(t, "run-1"), shared.EventCheckpointDisplayed)); n != 1 {
		t.Fatalf("checkpoint.displayed events = %d, want exactly 1 (the current checkpoint)", n)
	}
}

// TestStopDisplayedUndecidedHonorsStopAck: stopping stays possible at a
// displayed checkpoint — an explicit stop acknowledgment ends the turn with
// state kept. Anything else is the ask block.
func TestStopDisplayedUndecidedHonorsStopAck(t *testing.T) {
	for _, msg := range []string{"Stopped. The run is aborted per your request.", "用户要求终止，已停止。"} {
		useStopEnv(t)
		r := startRun(t)
		cp := waitUserCheckpoint(t, r, "diagnosis")
		displayCheckpoint(t, r, cp)
		code, stdout, stderr := callStop(t, stopPayload(t, msg))
		if code != 0 {
			t.Fatalf("%q: exit = %d, want 0 (stderr: %q)", msg, code, stderr)
		}
		if blocked, reason := decodeBlock(t, stdout); blocked {
			t.Fatalf("%q: stopping must stay possible at a displayed checkpoint, got block %q", msg, reason)
		}
		if disk := reloadRun(t, "run-1"); disk.Status != shared.StatusWaitUser {
			t.Fatalf("%q: status = %s, want WAIT_USER kept", msg, disk.Status)
		}
	}
}

// TestStopDecidedCheckpointNeedsNoAsk: once the decision landed the run is
// out of WAIT_USER — the Stop hook neither blocks nor asks.
func TestStopDecidedCheckpointNeedsNoAsk(t *testing.T) {
	useStopEnv(t)
	r := startRun(t)
	cp := waitUserCheckpoint(t, r, "diagnosis")
	displayCheckpoint(t, r, cp)
	if err := r.RecordDecision(cp.ID, shared.DecisionContinue, "tu-ask-1"); err != nil {
		t.Fatalf("decide CONTINUE: %v", err)
	}
	code, stdout, _ := callStop(t, stopPayload(t, "继续下一阶段。"))
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if blocked, reason := decodeBlock(t, stdout); blocked {
		t.Fatalf("a decided checkpoint must not be asked again, got block %q", reason)
	}
}

// TestStopAskBlockNamesTheExtendRide: at budget exhaustion the decision call
// must also carry the extend ride-along — the ask block spells out its two
// persisted labels so the model cannot invent them.
func TestStopAskBlockNamesTheExtendRide(t *testing.T) {
	useStopEnv(t)
	r, err := shared.StartRun("run-1", hookSession, "fix", shared.BudgetLimits{PhaseToolCalls: 1})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := r.OpenPhase("context"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	r.CountToolCall("tu-1")
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	cp, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "context", Summary: "context done"})
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	if cp.ExtendAsk == nil {
		t.Fatal("fixture: the checkpoint opened at exhaustion must carry the extend offer")
	}
	_, stdout, _ := callStop(t, stopPayload(t, renderBlock(r, cp)))
	reason := wantAskBlock(t, stdout, cp)
	if !strings.Contains(reason, cp.ExtendAsk.ExtendLabel) || !strings.Contains(reason, cp.ExtendAsk.HoldLabel) {
		t.Fatalf("at exhaustion the ask block must name the extend ride's two labels, got %q", reason)
	}
}

// TestStopSignalEndsTurnKeepsState: a free-text stop signal ends the turn —
// stop asymmetry (§1.6): stopping is always possible, the run state is kept,
// nothing is emitted (no block, no reminder).
func TestStopSignalEndsTurnKeepsState(t *testing.T) {
	useStopEnv(t)
	startRun(t) // ACTIVE

	code, stdout, stderr := callStop(t, stopPayload(t, "Stopping here as requested — run halted."))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("stop signal must not block, stdout = %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stop signal emits nothing (emergency stop path), stderr = %q", stderr)
	}
	disk := reloadRun(t, "run-1")
	if disk.Status != shared.StatusActive {
		t.Fatalf("status = %s, want ACTIVE — the stop keeps state (停止不对称)", disk.Status)
	}
	if evs := readStopTrace(t, "run-1"); len(evs) != 0 {
		t.Fatalf("stop signal must append no events, got %v", traceTypesOf(evs))
	}
}

// TestStopFullRenderBindsDisplayDespiteMenuLabels — the T10 carry, now T16's:
// the stop-ack marker regex matched the literal "stop" inside every rendered
// decision menu, so a last_assistant_message containing the FULL status render
// (whose menu lines include "stop: 停止——保留状态退出") ACKED the stop and
// skipped display binding — the checkpoint would then be undecidable forever.
// The ack match must not be satisfiable by menu labels: pasting the full
// render (menu labels + paging note included) still binds the display.
func TestStopFullRenderBindsDisplayDespiteMenuLabels(t *testing.T) {
	useStopEnv(t)
	r := startRun(t)
	cp := waitUserCheckpoint(t, r, "diagnosis")

	// The display-verified render is the checkpoint-only CANONICAL view —
	// the whole message IS the production renderer's bytes, menu labels and
	// all (the stop:-label ack-defect trigger rides along inside it).
	full := shared.RenderCheckpointMessage(r, cp)
	if !strings.Contains(full, "stop:") {
		t.Fatal("fixture must contain the stop: menu label (the ack-defect trigger)")
	}

	code, stdout, stderr := callStop(t, stopPayload(t, full))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	// Bound, then the ask block — never a demand to re-render.
	wantAskBlock(t, stdout, cp)
	disk := reloadRun(t, "run-1")
	got := currentCheckpoint(t, disk)
	if !got.Displayed {
		t.Fatal("menu labels inside the full render must NOT satisfy the stop-ack — display binding must still happen")
	}
	if want := shared.CheckpointContentDigest(cp, disk.RequirementsVersion); got.DisplayDigest != want {
		t.Fatalf("display digest = %s, want canonical content digest %s", got.DisplayDigest, want)
	}
	if n := len(eventsOfType(readStopTrace(t, "run-1"), shared.EventCheckpointDisplayed)); n != 1 {
		t.Fatalf("checkpoint.displayed events = %d, want exactly 1", n)
	}
}

// TestStopAckMarkerAllowsStopKeepsState: the documented stop-ack marker lets
// the turn end even at an UNDISPLAYED checkpoint — without the render block.
// Stopping must always be possible; state is kept.
func TestStopAckMarkerAllowsStopKeepsState(t *testing.T) {
	for _, msg := range []string{
		"Stopped. The run is aborted per your request.",
		"用户要求终止，已停止。",
		"Halting the workflow now.",
	} {
		useStopEnv(t)
		r := startRun(t)
		waitUserCheckpoint(t, r, "diagnosis")

		code, stdout, stderr := callStop(t, stopPayload(t, msg))
		if code != 0 {
			t.Fatalf("%q: exit = %d, want 0 (stderr: %q)", msg, code, stderr)
		}
		if blocked, reason := decodeBlock(t, stdout); blocked {
			t.Fatalf("%q: stop must always be possible, got block %q", msg, reason)
		}
		disk := reloadRun(t, "run-1")
		if disk.Status != shared.StatusWaitUser {
			t.Fatalf("%q: status = %s, want WAIT_USER kept", msg, disk.Status)
		}
		if got := currentCheckpoint(t, disk); got.Displayed {
			t.Fatalf("%q: ack allows the stop WITHOUT display — checkpoint must stay undisplayed", msg)
		}
		if evs := readStopTrace(t, "run-1"); len(evs) != 0 {
			t.Fatalf("%q: ack path emits nothing, got %v", msg, traceTypesOf(evs))
		}
	}
}

// TestStopAmbiguousRunsWarnsNotFails: more than one live run is ambiguous — the
// Stop hook warns on stderr and allows the stop (exit 0). It is a display
// verifier, not a work gate: this asymmetry vs the gate's fail-closed exit 2 is
// deliberate (Stop must not hard-fail the turn).
func TestStopAmbiguousRunsWarnsNotFails(t *testing.T) {
	useStopEnv(t)
	if _, err := shared.StartRun("run-1", hookSession, "fix", shared.BudgetLimits{}); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.StartRun("run-2", hookSession, "fix", shared.BudgetLimits{}); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := callStop(t, stopPayload(t, "done for now"))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 — Stop never hard-fails on ambiguity (stderr: %q)", code, stderr)
	}
	if blocked, _ := decodeBlock(t, stdout); blocked {
		t.Fatalf("ambiguous runs must not block (cannot name one checkpoint), stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "live") {
		t.Fatalf("stderr %q must warn about the ambiguous live runs", stderr)
	}
	for _, id := range []string{"run-1", "run-2"} {
		if evs := readStopTrace(t, id); len(evs) != 0 {
			t.Fatalf("ambiguous resolution must append no events to %s, got %v", id, traceTypesOf(evs))
		}
	}
}

// TestStopReminderForUnclosedRun: a live run that is not WAIT_USER allows the
// stop with a stderr post-game-pending reminder — state is kept, nothing
// deleted (the questionnaire wiring itself lands in T8/T13).
func TestStopReminderForUnclosedRun(t *testing.T) {
	useStopEnv(t)
	startRun(t) // ACTIVE

	code, stdout, stderr := callStop(t, stopPayload(t, "Implementation done, tests pass."))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("ACTIVE run must not block, stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "post-game") {
		t.Fatalf("stderr %q must carry the post-game-pending reminder", stderr)
	}
	disk := reloadRun(t, "run-1")
	if disk.Status != shared.StatusActive {
		t.Fatalf("status = %s, want ACTIVE — the reminder deletes nothing", disk.Status)
	}
}

// TestOutcomeUnknownRetainedAcrossStop: OUTCOME_UNKNOWN entries and their run
// state are strictly retained across a stop (留档不清理) — the stop may end the
// turn, the reconciliation debt stays on disk.
func TestOutcomeUnknownRetainedAcrossStop(t *testing.T) {
	useStopEnv(t)
	r := startRun(t)
	r.MarkOutcomeUnknown("tu-crash-1")
	r.MarkOutcomeUnknown("tu-crash-2")
	if err := r.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	code, _, stderr := callStop(t, stopPayload(t, "I will stop here."))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	disk := reloadRun(t, "run-1")
	if disk.Status != shared.StatusOutcomeUnknown {
		t.Fatalf("status = %s, want OUTCOME_UNKNOWN unchanged", disk.Status)
	}
	if len(disk.OutcomeUnknowns) != 2 {
		t.Fatalf("outcome_unknowns = %d entries, want 2 retained", len(disk.OutcomeUnknowns))
	}
	for _, e := range disk.OutcomeUnknowns {
		if e.Resolved {
			t.Fatalf("entry %s must stay unresolved (留档不清理)", e.ToolUseID)
		}
	}
}

// TestStopNoRunPassThrough: zero non-CLOSED runs is not our business — exit 0,
// no output, no events. And a CLOSED-only session is equally silent.
func TestStopNoRunPassThrough(t *testing.T) {
	useStopEnv(t)
	code, stdout, stderr := callStop(t, stopPayload(t, "anything"))
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("no-run session: (code, stdout, stderr) = (%d, %q, %q), want (0, \"\", \"\")", code, stdout, stderr)
	}

	// A run stopped through its checkpoint stays POST_GAME_PENDING — CLOSED
	// comes only after post-game persistence (T8/T13), so the run is still
	// live and the stop hook keeps reminding, never deleting (收尾前不 CLOSED).
	r := startRun(t)
	cp := waitUserCheckpoint(t, r, "diagnosis")
	displayCheckpoint(t, r, cp)
	if err := r.RecordDecision(cp.ID, shared.DecisionStop, ""); err != nil {
		t.Fatalf("decide STOP: %v", err)
	}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = callStop(t, stopPayload(t, "run closed"))
	if code != 0 || stdout != "" {
		t.Fatalf("post-game-pending run: (code, stdout) = (%d, %q), want (0, \"\")", code, stdout)
	}
	if !strings.Contains(stderr, "post-game") {
		t.Fatalf("post-game-pending run must keep the reminder, stderr = %q", stderr)
	}
	if disk := reloadRun(t, "run-1"); disk.Status != shared.StatusPostGamePending {
		t.Fatalf("status = %s, want POST_GAME_PENDING retained", disk.Status)
	}
}

// TestStopUnreadableStateWarnsNotFails: an unreadable session state directory
// cannot be display-verified, but the Stop hook still allows the turn to end
// with a stderr warning — the un-brick path is the escape hatch, never a
// Stop-side hard failure.
func TestStopUnreadableStateWarnsNotFails(t *testing.T) {
	useStopEnv(t)
	dir := filepath.Join(os.Getenv("SUMMONER_STATE_DIR"), shared.ProjectKey(), hookSession)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run-corrupt.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := callStop(t, stopPayload(t, "done"))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 — Stop never hard-fails on unreadable state (stderr: %q)", code, stderr)
	}
	if blocked, _ := decodeBlock(t, stdout); blocked {
		t.Fatalf("unreadable state must not fabricate a block, stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "unreadable") {
		t.Fatalf("stderr %q must warn about the unreadable state", stderr)
	}
}

// TestStopParseFailureFailsClosed: unparseable stdin and trailing garbage are
// the one fail-closed path (exit 2) — a hook that cannot read its input must
// not guess.
func TestStopParseFailureFailsClosed(t *testing.T) {
	useStopEnv(t)
	startRun(t)
	for name, payload := range map[string]string{
		"not json":       "this is not json",
		"trailing bytes": `{"hook_event_name":"Stop"} trailing`,
	} {
		var stdout, stderr bytes.Buffer
		code := runStop(strings.NewReader(payload), &stdout, &stderr)
		if code != 2 {
			t.Errorf("%s: exit code = %d, want 2 (stderr: %q)", name, code, stderr)
		}
	}
}

// TestEscapeHatchBothBinaries — Stop half (the SubagentStop half lives in the
// sibling subagentstop package): SUMMONER_HOOKS_DISABLE=1 enforces nothing — no
// block, no display binding — but on exactly one live run it first records the
// contract violation (hooks.disabled); with no live run there are no events.
func TestEscapeHatchBothBinaries(t *testing.T) {
	t.Run("live run traces the violation, no enforcement", func(t *testing.T) {
		useStopEnv(t)
		r := startRun(t)
		waitUserCheckpoint(t, r, "diagnosis") // undisplayed: would block without the hatch
		setenv16(t, "SUMMONER_HOOKS_DISABLE", "1")

		code, stdout, stderr := callStop(t, stopPayload(t, "no render block at all"))
		if code != 0 {
			t.Fatalf("exit = %d, want 0 — the hatch enforces nothing (stderr: %q)", code, stderr)
		}
		if blocked, _ := decodeBlock(t, stdout); blocked {
			t.Fatalf("the hatch must not block, stdout = %q", stdout)
		}
		if got := currentCheckpoint(t, reloadRun(t, "run-1")); got.Displayed {
			t.Fatal("the hatch must not bind display")
		}
		evs := readStopTrace(t, "run-1")
		if n := len(eventsOfType(evs, shared.EventHooksDisabled)); n != 1 {
			t.Fatalf("hooks.disabled events = %d, want exactly 1 (trace: %v)", n, traceTypesOf(evs))
		}
	})
	t.Run("no live run stays silent", func(t *testing.T) {
		useStopEnv(t)
		setenv16(t, "SUMMONER_HOOKS_DISABLE", "1")
		code, stdout, stderr := callStop(t, stopPayload(t, "nothing live"))
		if code != 0 || stdout != "" || stderr != "" {
			t.Fatalf("(code, stdout, stderr) = (%d, %q, %q), want (0, \"\", \"\")", code, stdout, stderr)
		}
	})
}

// --- ship review round 1: the stop acknowledgment ----------------------------------

// stopWordCheckpoint parks a fix run at a diagnosis checkpoint whose
// CONCLUSION is full of stop words — ordinary diagnosis vocabulary.
func stopWordCheckpoint(t *testing.T) (*shared.RunState, shared.CheckpointState) {
	t.Helper()
	r := startRun(t)
	if err := r.OpenPhase("diagnosis"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	cp, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{
		PhaseID: "diagnosis", Summary: "服务停止响应",
		Outcome: shared.ConclusionUnclear,
		Conclusion: &shared.Conclusion{
			Summary:  "服务在收到 SIGTERM 后停止响应，握手被 abort，进程终止",
			Evidence: []string{"svc/handler.go:88 — 收到 SIGTERM 即停止监听"},
			Approach: "先停止服务再复现",
		},
	})
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	return r, cp
}

// statusLikeView stands in for the default summoner-status view: its header
// lines, the detail block, then the canonical message.
func statusLikeView(run *shared.RunState, cp shared.CheckpointState) string {
	return "⚡ SUMMONER STATUS — run run-1 · workflow fix · WAIT_USER\n" +
		"route: context → diagnosis → reproduce → fix → regression → review\n" +
		"awaiting: user decision at checkpoint " + cp.ID + " (nonce " + cp.Nonce + ")\n" +
		shared.CheckpointDetail(run, cp) + "\n" + shared.RenderCheckpointMessage(run, cp)
}

func blockquote(s string) string {
	return "> " + strings.ReplaceAll(strings.TrimSuffix(s, "\n"), "\n", "\n> ") + "\n"
}

// TestStopFrameworkTextIsNeverAStopAcknowledgment (ship review round 1,
// two lenses): the framework's own words — the checkpoint the model pastes,
// the status view it re-pastes to navigate, the hook's own ask block — must
// never read as the model acknowledging a stop. Before the fix a checkpoint
// whose conclusion said 停止 let the turn end with no dialog: the stall this
// release exists to remove.
func TestStopFrameworkTextIsNeverAStopAcknowledgment(t *testing.T) {
	for name, msg := range map[string]func(*shared.RunState, shared.CheckpointState) string{
		"the checkpoint re-pasted":        func(r *shared.RunState, cp shared.CheckpointState) string { return renderBlock(r, cp) },
		"the checkpoint in a blockquote":  func(r *shared.RunState, cp shared.CheckpointState) string { return blockquote(renderBlock(r, cp)) },
		"the status view":                 statusLikeView,
		"the status view in a blockquote": func(r *shared.RunState, cp shared.CheckpointState) string { return blockquote(statusLikeView(r, cp)) },
		"the detail block alone":          func(r *shared.RunState, cp shared.CheckpointState) string { return shared.CheckpointDetail(r, cp) },
		"the model instruction line": func(_ *shared.RunState, cp shared.CheckpointState) string {
			return "等你决定。\n" + shared.DecisionAskInstruction(cp)
		},
		"the rejection block": func(r *shared.RunState, cp shared.CheckpointState) string {
			return "说明一下现在的选择。\n" + shared.RejectionReason(cp, `answer "请停止" is not an offered option label of the decision question`, nil, nil)
		},
	} {
		useStopEnv(t)
		r, cp := stopWordCheckpoint(t)
		displayCheckpoint(t, r, cp)

		code, stdout, stderr := callStop(t, stopPayload(t, msg(r, cp)))
		if code != 0 {
			t.Fatalf("%s: exit = %d, want 0 (stderr: %q)", name, code, stderr)
		}
		wantAskBlock(t, stdout, cp)
		if got := currentCheckpoint(t, reloadRun(t, "run-1")); got.Decided {
			t.Fatalf("%s: nothing may be decided", name)
		}
	}

	// Undisplayed: a sign-off plus the instruction line only is no display
	// act and no stop — the render is demanded.
	for name, msg := range map[string]func(*shared.RunState, shared.CheckpointState) string{
		"sign-off + instruction line": func(_ *shared.RunState, cp shared.CheckpointState) string {
			return "先到这里。\n" + shared.DecisionAskInstruction(cp)
		},
		"sign-off + detail block": func(r *shared.RunState, cp shared.CheckpointState) string {
			return "先到这里。\n" + shared.CheckpointDetail(r, cp)
		},
	} {
		useStopEnv(t)
		r, cp := stopWordCheckpoint(t)
		code, stdout, stderr := callStop(t, stopPayload(t, msg(r, cp)))
		if code != 0 {
			t.Fatalf("%s: exit = %d, want 0 (stderr: %q)", name, code, stderr)
		}
		blocked, _ := decodeBlock(t, stdout)
		if !blocked {
			t.Fatalf("%s: framework text is not a stop — the render must be demanded, got stdout %q", name, stdout)
		}
		if got := currentCheckpoint(t, reloadRun(t, "run-1")); got.Displayed {
			t.Fatalf("%s: nothing may bind", name)
		}
	}
}

// TestStopDisplayedStopMustTieTheStopToTheUsersRequest: at a displayed
// checkpoint whose question is owed, listing the options or explaining a
// failure is not a stop. Only words that tie the stop to the user's request
// end the turn; everything else gets the question (whose `stop:` label is
// the recorded way to stop).
func TestStopDisplayedStopMustTieTheStopToTheUsersRequest(t *testing.T) {
	notAStop := []string{
		"请选择 continue、skip、done 或 stop。",
		"排查结论：网关在升级窗口内停止转发，进程终止后没有自动重启；细节见上面的检查点。",
		"按你的要求排查后确认：服务在升级时停止响应的原因是配置缺失。请决定下一步。",
		"已停止。",
	}
	for _, msg := range notAStop {
		useStopEnv(t)
		r, cp := stopWordCheckpoint(t)
		displayCheckpoint(t, r, cp)
		code, stdout, stderr := callStop(t, stopPayload(t, msg))
		if code != 0 {
			t.Fatalf("%q: exit = %d, want 0 (stderr: %q)", msg, code, stderr)
		}
		wantAskBlock(t, stdout, cp)
	}

	aStop := []string{
		"Stopped as you asked.",
		"Stopped. The run is aborted per your request.",
		"好的，已按要求停止运行。状态已保留，之后可以用 /summoner:status 恢复。",
		"用户要求终止，已停止。",
		"已按您的要求停止了本次工作流。运行状态已保留，之后可以用 /summoner:status 恢复，所以不需要额外操作。",
	}
	for _, msg := range aStop {
		useStopEnv(t)
		r, cp := stopWordCheckpoint(t)
		displayCheckpoint(t, r, cp)
		code, stdout, stderr := callStop(t, stopPayload(t, msg))
		if code != 0 {
			t.Fatalf("%q: exit = %d, want 0 (stderr: %q)", msg, code, stderr)
		}
		if blocked, reason := decodeBlock(t, stdout); blocked {
			t.Fatalf("%q: an explicit stop acknowledgment must end the turn, got block %q", msg, reason)
		}
		if disk := reloadRun(t, "run-1"); disk.Status != shared.StatusWaitUser {
			t.Fatalf("%q: status = %s, want WAIT_USER kept", msg, disk.Status)
		}
	}
}

// TestStopUndisplayedBareStopStaysPossible: before anything is displayed a
// short bare stop still ends the turn (nothing is owed yet); a long
// explanation with an incidental stop word is content — the render is
// demanded.
func TestStopUndisplayedBareStopStaysPossible(t *testing.T) {
	useStopEnv(t)
	r, cp := stopWordCheckpoint(t)
	code, stdout, stderr := callStop(t, stopPayload(t, "好的，已停止。"))
	if code != 0 || stdout != "" {
		t.Fatalf("a short bare stop must end the turn: (code, stdout, stderr) = (%d, %q, %q)", code, stdout, stderr)
	}
	_ = r
	_ = cp

	useStopEnv(t)
	r, cp = stopWordCheckpoint(t)
	long := "排查结论：网关在升级窗口内停止转发，进程终止后没有自动重启；上游因此超时，表现为整场战斗卡在结算，等到升级窗口结束后才自行恢复。" +
		"建议先看网关的升级日志，再核对服务停止时的最后几行输出，最后对照同一时间段的会话心跳记录，确认是网关单方面断开还是会话服务自己退出。"
	code, stdout, _ = callStop(t, stopPayload(t, long))
	_ = r
	_ = cp
	if blocked, _ := decodeBlock(t, stdout); !blocked {
		t.Fatalf("an explanation with an incidental stop word is content — the render must be demanded, got stdout %q", stdout)
	}
}

// TestStopStaleBindingIsRebound: a displayed checkpoint whose content no
// longer matches its binding would reject every answer forever (the reducer
// refuses a drifted binding, and nothing else re-binds). The Stop hook
// treats it as undisplayed: the CURRENT render binds again; the stale one
// does not.
func TestStopStaleBindingIsRebound(t *testing.T) {
	useStopEnv(t)
	r, cp := stopWordCheckpoint(t)
	displayCheckpoint(t, r, cp)
	stale := renderBlock(r, cp)
	// Drift the DISPLAYED content behind the binding (the conclusion is
	// digest-bound and rendered; the phase note is not rendered when a
	// conclusion exists, so drifting only the note changes nothing shown).
	disk := reloadRun(t, "run-1")
	stored := &disk.Checkpoints[len(disk.Checkpoints)-1]
	stored.Conclusion.Summary += "；补充：进程终止后没有自动重启"
	if err := disk.Save(); err != nil {
		t.Fatal(err)
	}
	current := reloadRun(t, "run-1")
	fresh := renderBlock(current, *current.CurrentCheckpoint())

	// The stale render binds nothing.
	code, stdout, stderr := callStop(t, stopPayload(t, "说明。\n\n"+stale))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if blocked, reason := decodeBlock(t, stdout); !blocked || strings.Contains(reason, "已展示并绑定") {
		t.Fatalf("a stale render must be refused with the render demand, got %q", stdout)
	}
	if got := currentCheckpoint(t, reloadRun(t, "run-1")).DisplayDigest; got != shared.CheckpointContentDigest(cp, 1) {
		t.Fatalf("the stale render must not move the binding, digest = %s", got)
	}

	// The current render re-binds and the question follows.
	code, stdout, stderr = callStop(t, stopPayload(t, "说明。\n\n"+fresh))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	wantAskBlock(t, stdout, *current.CurrentCheckpoint())
	bound := currentCheckpoint(t, reloadRun(t, "run-1"))
	if want := shared.CheckpointContentDigest(*bound, 1); bound.DisplayDigest != want {
		t.Fatalf("display digest = %s, want the CURRENT content digest %s", bound.DisplayDigest, want)
	}
}

// TestStopSinglePageAskOmitsThePagingCue — a single-page menu has no `more:`
// (ship review round 2, interaction F4/F10: the guard existed but no test
// pinned it, so always printing the paging line stayed green).
func TestStopSinglePageAskOmitsThePagingCue(t *testing.T) {
	useStopEnv(t)
	r, err := shared.StartRun("run-1", hookSession, "fix", shared.BudgetLimits{})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := r.OpenPhase("context"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	if err := r.OpenRequirementChange("scope change"); err != nil {
		t.Fatalf("OpenRequirementChange: %v", err)
	}
	if _, err := r.ApplyRequirementDelta(shared.RequirementDelta{Note: "delta"}); err != nil {
		t.Fatalf("ApplyRequirementDelta: %v", err)
	}
	cp := r.CurrentCheckpoint()
	if cp == nil || len(cp.MenuPages) != 1 {
		t.Fatalf("fixture: the change-confirm checkpoint must fit one page, got %+v", cp)
	}
	_, stdout, stderr := callStop(t, stopPayload(t, renderBlock(r, *cp)))
	if stderr != "" {
		t.Fatalf("stderr: %q", stderr)
	}
	reason := wantAskBlock(t, stdout, *cp)
	if strings.Contains(reason, "more:") {
		t.Fatalf("a single-page ask must not offer paging, got %q", reason)
	}
	if !strings.Contains(reason, "选项已全部列出") {
		t.Fatalf("the single-page ask must say every option is listed, got %q", reason)
	}
}
