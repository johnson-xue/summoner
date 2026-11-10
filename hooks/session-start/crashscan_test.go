package main

// crashscan_test.go — T19 suite, session-start half: the crash-window recovery
// scan of a RESTARTED session (mutating tool intents AND dead worker
// delegations convert; terminal runs, reviewer delegations and read-class
// calls never do; a broken trace skips the run without failing the session).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// useScanEnv redirects v2 state, traces and the project key into fresh temp
// dirs (the other hook suites' pattern; go1.16: no t.Setenv).
func useScanEnv(t *testing.T) {
	t.Helper()
	for _, kv := range [][2]string{
		{"SUMMONER_STATE_DIR", t.TempDir()},
		{"SUMMONER_TRACE_DIR", t.TempDir()},
		{"CLAUDE_PROJECT_DIR", t.TempDir()},
	} {
		key, val := kv[0], kv[1]
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
	os.Unsetenv("SUMMONER_HOOKS_DISABLE")
}

func appendScanEvent(t *testing.T, runID string, ev shared.TraceEvent) {
	t.Helper()
	if err := shared.AppendEvent(runID, "sess-1", ev); err != nil {
		t.Fatalf("append %s: %v", ev.Type, err)
	}
}

func scanTrace(t *testing.T, runID string) []shared.TraceEvent {
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

func scanMarkings(t *testing.T, runID string) []shared.TraceEvent {
	t.Helper()
	var out []shared.TraceEvent
	for _, ev := range scanTrace(t, runID) {
		if ev.Type == shared.EventToolOutcomeUnknown {
			out = append(out, ev)
		}
	}
	return out
}

// TestSessionStartScanConvertsIntentsAndDeadWorkers: after a restart, a
// dangling write-class tool intent AND an uncompleted worker delegation both
// convert (detected_by session-start); read-class calls, resolved calls, a
// live reviewer delegation and Skill bookkeeping never do.
func TestSessionStartScanConvertsIntentsAndDeadWorkers(t *testing.T) {
	useScanEnv(t)
	r, err := shared.StartRun("run-1", "sess-1", "fix", shared.BudgetLimits{})
	if err != nil {
		t.Fatal(err)
	}
	_ = r
	// Resolved read-class Bash + dangling write-class Bash.
	appendScanEvent(t, "run-1", shared.TraceEvent{Type: shared.EventToolStarted, ToolUseID: "tu-r1", Payload: map[string]interface{}{"tool_name": "Bash", "read_class": true}})
	appendScanEvent(t, "run-1", shared.TraceEvent{Type: shared.EventToolResult, ToolUseID: "tu-r1"})
	appendScanEvent(t, "run-1", shared.TraceEvent{Type: shared.EventToolStarted, ToolUseID: "tu-w1", Payload: map[string]interface{}{"tool_name": "Bash"}})
	// Dead worker delegation (started, never completed) + live reviewer.
	appendScanEvent(t, "run-1", shared.TraceEvent{Type: shared.EventDelegationStarted, ToolUseID: "tu-a1", Payload: map[string]interface{}{"subagent_type": "worker"}})
	appendScanEvent(t, "run-1", shared.TraceEvent{Type: shared.EventDelegationStarted, ToolUseID: "tu-a2", Payload: map[string]interface{}{"subagent_type": "reviewer"}})

	scanDanglingIntents("sess-1")

	after, err := shared.LoadRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != shared.StatusOutcomeUnknown {
		t.Fatalf("status = %s, want OUTCOME_UNKNOWN", after.Status)
	}
	marked := map[string]bool{}
	for _, e := range after.OutcomeUnknowns {
		marked[e.ToolUseID] = true
	}
	if !marked["tu-w1"] || !marked["tu-a1"] || len(marked) != 2 {
		t.Fatalf("marked ids = %v, want exactly {tu-w1, tu-a1} (worker converts at SessionStart; reviewer never)", marked)
	}
	for _, m := range scanMarkings(t, "run-1") {
		if m.Payload["detected_by"] != "session-start" {
			t.Fatalf("marking %+v must carry detected_by session-start", m.Payload)
		}
	}

	// Re-running the scan (a second restart) marks nothing new.
	scanDanglingIntents("sess-1")
	after2, _ := shared.LoadRun("run-1")
	if len(after2.OutcomeUnknowns) != 2 {
		t.Fatalf("rescan marked %d entries, want 2", len(after2.OutcomeUnknowns))
	}
}

// TestSessionStartScanSkipsTerminalRunsAndBrokenTraces: CLOSED and
// POST_GAME_PENDING runs are never marked; an unreadable trace skips the run
// without failing the session.
func TestSessionStartScanSkipsTerminalRunsAndBrokenTraces(t *testing.T) {
	useScanEnv(t)
	// A CLOSED run with a dangling write: terminal, never marked.
	rc, err := shared.StartRun("run-c", "sess-1", "fix", shared.BudgetLimits{})
	if err != nil {
		t.Fatal(err)
	}
	appendScanEvent(t, "run-c", shared.TraceEvent{Type: shared.EventToolStarted, ToolUseID: "tu-x1", Payload: map[string]interface{}{"tool_name": "Bash"}})
	rc.Status = shared.StatusClosed
	if err := rc.Save(); err != nil {
		t.Fatal(err)
	}
	// An ACTIVE run with a dangling write but a MISSING trace file: skipped.
	if _, err := shared.StartRun("run-m", "sess-1", "fix", shared.BudgetLimits{}); err != nil {
		t.Fatal(err)
	}
	// A torn trace tail: the readable prefix classifies, the garbage is
	// skipped, the session still starts.
	if _, err := shared.StartRun("run-t", "sess-1", "fix", shared.BudgetLimits{}); err != nil {
		t.Fatal(err)
	}
	appendScanEvent(t, "run-t", shared.TraceEvent{Type: shared.EventToolStarted, ToolUseID: "tu-t1", Payload: map[string]interface{}{"tool_name": "Edit"}})
	tp := filepath.Join(os.Getenv("SUMMONER_TRACE_DIR"), shared.ProjectKey(), "run-t.jsonl")
	f, err := os.OpenFile(tp, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{torn json\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	scanDanglingIntents("sess-1")

	if after, _ := shared.LoadRun("run-c"); len(after.OutcomeUnknowns) != 0 {
		t.Fatalf("CLOSED run marked %+v — terminal runs are never marked", after.OutcomeUnknowns)
	}
	if after, _ := shared.LoadRun("run-t"); len(after.OutcomeUnknowns) != 1 {
		t.Fatalf("torn-tail run marked %+v, want the one readable dangling intent", after.OutcomeUnknowns)
	}
}

// TestParseSessionIDTolerant: a missing/invalid payload yields "" (the scan
// skips); a real payload yields its session id.
func TestParseSessionIDTolerant(t *testing.T) {
	if got := parseSessionID(strings.NewReader("not json")); got != "" {
		t.Fatalf("garbage stdin session id = %q, want \"\"", got)
	}
	if got := parseSessionID(strings.NewReader(`{"session_id":"sess-9"}`)); got != "sess-9" {
		t.Fatalf("session id = %q, want sess-9", got)
	}
}
