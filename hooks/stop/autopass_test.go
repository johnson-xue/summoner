package main

import (
	"strings"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// autopass_test.go — the Stop hook's continuation backstop (0.7.0 D3,
// design acceptance criterion 12). An auto-pass opens no checkpoint, so
// nothing tells the model to go on: a model that submitted the phase and
// ended its turn would leave the run ACTIVE and idle until the user typed
// something. The hook nudges it on — once.

func appendStopTrace(t *testing.T, runID string, ev shared.TraceEvent) {
	t.Helper()
	if err := shared.AppendEvent(runID, hookSession, ev); err != nil {
		t.Fatalf("append %s: %v", ev.Type, err)
	}
}

// autoPassedRun leaves run-1 ACTIVE right after an in-gate auto-pass of
// context: the checkpoint accepted by the framework and the trace shaped
// exactly as the gate leaves it (opened → auto, then the submit's result).
func autoPassedRun(t *testing.T) (*shared.RunState, shared.CheckpointState) {
	t.Helper()
	r := startRun(t)
	if err := r.OpenPhase("context"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	cp, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{
		PhaseID: "context", Summary: "context established",
		Outcome:    shared.ConclusionConfirmed,
		Conclusion: &shared.Conclusion{Summary: "context conclusion", Evidence: []string{"evidence-1"}, Approach: "read the code"},
	})
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	appendStopTrace(t, "run-1", shared.TraceEvent{Type: shared.EventCheckpointOpened,
		Payload: map[string]interface{}{"checkpoint_id": cp.ID, "phase_id": "context", "nonce": cp.Nonce}})
	if err := r.AutoPassCheckpoint(cp.ID, false); err != nil {
		t.Fatalf("AutoPassCheckpoint: %v", err)
	}
	appendStopTrace(t, "run-1", shared.TraceEvent{Type: shared.EventCheckpointAuto,
		Payload: map[string]interface{}{"checkpoint_id": cp.ID, "phase_id": "context", "outcome": shared.ConclusionConfirmed}})
	appendStopTrace(t, "run-1", shared.TraceEvent{Type: shared.EventToolResult, ToolUseID: "tu-ps-1",
		Payload: map[string]interface{}{"tool_name": "Skill"}})
	return r, cp
}

// TestStopNudgesOnceAfterAnAutoPass — criterion 12: the turn ends right
// after an auto-pass → the hook blocks once, naming the phase that passed
// and the one to enter; the same sign-off again is allowed.
func TestStopNudgesOnceAfterAnAutoPass(t *testing.T) {
	useStopEnv(t)
	r, _ := autoPassedRun(t)
	if r.Status != shared.StatusActive {
		t.Fatalf("fixture: status %s, want ACTIVE", r.Status)
	}
	before := len(readStopTrace(t, "run-1"))

	code, stdout, stderr := callStop(t, stopPayload(t, "context 阶段已提交。"))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	blocked, reason := decodeBlock(t, stdout)
	if !blocked {
		t.Fatalf("a turn ended right after an auto-pass must be nudged on, got stdout %q", stdout)
	}
	for _, want := range []string{"context", "已自动通过", "diagnosis"} {
		if !strings.Contains(reason, want) {
			t.Errorf("nudge lost %q:\n%s", want, reason)
		}
	}
	if got := reloadRun(t, "run-1"); got.Status != shared.StatusActive || got.AutoPassNudgeFor != "" {
		t.Fatalf("the nudge must keep the run ACTIVE and consume its cue: status %s cue %q", got.Status, got.AutoPassNudgeFor)
	}

	// Idempotent re-fire of the SAME turn end (the platform re-runs the hook
	// when the model signs off again without a tool call): the cue is spent,
	// so the stop is allowed — at most one nudge per auto-pass.
	code, stdout, stderr = callStop(t, stopPayload(t, "context 阶段已提交。"))
	if code != 0 {
		t.Fatalf("second stop: exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if blocked, reason := decodeBlock(t, stdout); blocked {
		t.Fatalf("one auto-pass nudges at most once, got a second block %q", reason)
	}
	if n := len(readStopTrace(t, "run-1")) - before; n != 0 {
		t.Fatalf("a nudge must append no trace events, got %d", n)
	}
}

// TestStopNudgeEndsWhenTheNextPhaseStarted: once the next phase is under
// way there is nothing to nudge — whether the state cue was spent by
// OpenPhase or only the trace shows the phase.started (the belt).
func TestStopNudgeEndsWhenTheNextPhaseStarted(t *testing.T) {
	// The gate opened the next phase: OpenPhase spends the cue.
	useStopEnv(t)
	r, _ := autoPassedRun(t)
	if err := r.OpenPhase("diagnosis"); err != nil {
		t.Fatalf("OpenPhase(diagnosis): %v", err)
	}
	code, stdout, _ := callStop(t, stopPayload(t, "开始 diagnosis。"))
	if code != 0 || stdout != "" {
		t.Fatalf("with the next phase open the stop is silent: (code, stdout) = (%d, %q)", code, stdout)
	}

	// Belt — the cue is still set but the trace shows the phase.started.
	useStopEnv(t)
	r, _ = autoPassedRun(t)
	appendStopTrace(t, "run-1", shared.TraceEvent{Type: shared.EventPhaseStarted,
		Payload: map[string]interface{}{"phase_id": "diagnosis"}})
	code, stdout, _ = callStop(t, stopPayload(t, "开始 diagnosis。"))
	if code != 0 || stdout != "" {
		t.Fatalf("a phase.started after the auto-pass ends the tail — the stop must be silent: (code, stdout) = (%d, %q)", code, stdout)
	}
}

// TestStopNudgeHonorsStopAck: stopping stays possible right after an
// auto-pass — an explicit stop acknowledgment ends the turn unblocked.
func TestStopNudgeHonorsStopAck(t *testing.T) {
	useStopEnv(t)
	autoPassedRun(t)
	code, stdout, stderr := callStop(t, stopPayload(t, "Stopped. The run is halted per your request."))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if blocked, reason := decodeBlock(t, stdout); blocked {
		t.Fatalf("a stop acknowledgment must end the turn, got block %q", reason)
	}
	if got := reloadRun(t, "run-1"); got.Status != shared.StatusActive {
		t.Fatalf("status = %s, want ACTIVE kept", got.Status)
	}
}

// TestStopNoNudgeForAnOrdinaryContinue: a HUMAN continue leaves no cue —
// the backstop is the auto-pass's alone.
func TestStopNoNudgeForAnOrdinaryContinue(t *testing.T) {
	useStopEnv(t)
	r := startRun(t)
	cp := waitUserCheckpoint(t, r, "context")
	displayCheckpoint(t, r, cp)
	if err := r.RecordDecision(cp.ID, shared.DecisionContinue, "tu-ask-1"); err != nil {
		t.Fatalf("decide CONTINUE: %v", err)
	}
	code, stdout, _ := callStop(t, stopPayload(t, "context 已验收，继续。"))
	if code != 0 || stdout != "" {
		t.Fatalf("a human continue must not be nudged: (code, stdout) = (%d, %q)", code, stdout)
	}
}
