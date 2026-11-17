package main

// crashscan_test.go — T19 suite, Stop half: the crash-window scan (mutating
// tool intents without results become OUTCOME_UNKNOWN entries with their
// tool.outcome_unknown markings; read-class calls, resolved calls and live
// delegations stay unmarked; the stop itself is never blocked).

import (
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

func appendStopEvent(t *testing.T, ev shared.TraceEvent) {
	t.Helper()
	if err := shared.AppendEvent("run-1", hookSession, ev); err != nil {
		t.Fatalf("append %s: %v", ev.Type, err)
	}
}

// TestStopScanMarksMutatingDanglingIntents: a write-class Bash intent and an
// Edit intent without results are marked OUTCOME_UNKNOWN with detected_by
// stop; the read-class Bash, the resolved Write and the Skill bookkeeping are
// not. The stop is allowed throughout (the asymmetry holds).
func TestStopScanMarksMutatingDanglingIntents(t *testing.T) {
	useStopEnv(t)
	startRun(t)
	// A resolved read-class Bash.
	appendStopEvent(t, shared.TraceEvent{Type: shared.EventToolStarted, ToolUseID: "tu-r1", Payload: map[string]interface{}{"tool_name": "Bash", "read_class": true}})
	appendStopEvent(t, shared.TraceEvent{Type: shared.EventToolResult, ToolUseID: "tu-r1"})
	// A resolved Write.
	appendStopEvent(t, shared.TraceEvent{Type: shared.EventToolStarted, ToolUseID: "tu-w2", Payload: map[string]interface{}{"tool_name": "Write"}})
	appendStopEvent(t, shared.TraceEvent{Type: shared.EventToolResult, ToolUseID: "tu-w2"})
	// Skill bookkeeping.
	appendStopEvent(t, shared.TraceEvent{Type: shared.EventToolStarted, ToolUseID: "tu-s1", Payload: map[string]interface{}{"tool_name": "Skill"}})
	// A DANGLING write-class Bash and a dangling Edit.
	appendStopEvent(t, shared.TraceEvent{Type: shared.EventToolStarted, ToolUseID: "tu-w1", Payload: map[string]interface{}{"tool_name": "Bash"}})
	appendStopEvent(t, shared.TraceEvent{Type: shared.EventToolStarted, ToolUseID: "tu-e1", Payload: map[string]interface{}{"tool_name": "Edit"}})

	code, stdout, stderr := callStop(t, stopPayload(t, "Wrapping up."))
	if code != 0 {
		t.Fatalf("stop exit = %d, want 0 (the scan never blocks; stdout %q stderr %q)", code, stdout, stderr)
	}
	after := reloadRun(t, "run-1")
	if after.Status != shared.StatusOutcomeUnknown {
		t.Fatalf("status = %s, want OUTCOME_UNKNOWN", after.Status)
	}
	marked := map[string]bool{}
	for _, e := range after.OutcomeUnknowns {
		marked[e.ToolUseID] = true
	}
	if !marked["tu-w1"] || !marked["tu-e1"] || len(marked) != 2 {
		t.Fatalf("marked ids = %v, want exactly {tu-w1, tu-e1}", marked)
	}
	evs := readStopTrace(t, "run-1")
	for _, id := range []string{"tu-w1", "tu-e1"} {
		found := false
		for _, ev := range eventsOfType(evs, shared.EventToolOutcomeUnknown) {
			if ev.ToolUseID == id && ev.Payload["detected_by"] == "stop" {
				found = true
			}
		}
		if !found {
			t.Fatalf("no tool.outcome_unknown (detected_by stop) for %s", id)
		}
	}

	// The re-fired stop (8-block reality) records nothing new.
	callStop(t, stopPayload(t, "Wrapping up."))
	after2 := reloadRun(t, "run-1")
	if len(after2.OutcomeUnknowns) != 2 {
		t.Fatalf("idempotent rescan marked %d entries, want 2", len(after2.OutcomeUnknowns))
	}
	if n := len(eventsOfType(readStopTrace(t, "run-1"), shared.EventToolOutcomeUnknown)); n != 2 {
		t.Fatalf("tool.outcome_unknown events = %d, want 2", n)
	}
}

// TestStopScanLeavesLiveDelegations: a live worker delegation at a normal
// per-turn Stop is NOT a crash — the run stays untouched (the SessionStart
// scan owns dead workers after a restart).
func TestStopScanLeavesLiveDelegations(t *testing.T) {
	useStopEnv(t)
	startRun(t)
	appendStopEvent(t, shared.TraceEvent{Type: shared.EventDelegationStarted, ToolUseID: "tu-a1", Payload: map[string]interface{}{"subagent_type": "worker"}})

	code, _, stderr := callStop(t, stopPayload(t, "Delegated a worker."))
	if code != 0 {
		t.Fatalf("stop exit = %d, want 0 (stderr %q)", code, stderr)
	}
	after := reloadRun(t, "run-1")
	if after.Status != shared.StatusActive || len(after.OutcomeUnknowns) != 0 {
		t.Fatalf("a live delegation must not become OUTCOME_UNKNOWN: status %s entries %+v", after.Status, after.OutcomeUnknowns)
	}
}
