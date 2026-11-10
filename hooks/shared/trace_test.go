package shared

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- helpers ----------------------------------------------------------------

// useTraceDir redirects the v2 trace root into a fresh temp dir (hermetic
// suite; same pattern as startTestRun for run-state).
func useTraceDir(t *testing.T) string {
	t.Helper()
	prev := traceDirOverride
	traceDirOverride = t.TempDir()
	t.Cleanup(func() { traceDirOverride = prev })
	return traceDirOverride
}

func traceFileFor(t *testing.T, runID string) string {
	t.Helper()
	p, err := traceFilePath(runID)
	if err != nil {
		t.Fatalf("traceFilePath(%s): %v", runID, err)
	}
	return p
}

func readTraceLines(t *testing.T, runID string) []TraceEvent {
	t.Helper()
	b, err := os.ReadFile(traceFileFor(t, runID))
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	var evs []TraceEvent
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var ev TraceEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("unmarshal trace line %q: %v", line, err)
		}
		evs = append(evs, ev)
	}
	return evs
}

func mustAppend(t *testing.T, runID, typ string) {
	t.Helper()
	if err := AppendEvent(runID, TraceEvent{Type: typ, Source: "trace_test"}); err != nil {
		t.Fatalf("AppendEvent(%s): %v", typ, err)
	}
}

// --- Step 1 contract tests (brief task-2) -----------------------------------

// TestEventMarshalAllowList: unknown event types are rejected; every allowed
// type is accepted and marshals to one JSON object carrying the common fields.
func TestEventMarshalAllowList(t *testing.T) {
	useTraceDir(t)
	const run = "run-tr-1"

	err := AppendEvent(run, TraceEvent{Type: "bogus.event", Source: "trace_test"})
	if err == nil {
		t.Fatal("AppendEvent accepted unknown event type bogus.event")
	}
	if _, statErr := os.Stat(traceFileFor(t, run)); !os.IsNotExist(statErr) {
		t.Fatalf("rejected append must not create the trace file (stat: %v)", statErr)
	}

	for _, typ := range allEventTypes {
		if err := AppendEvent(run, TraceEvent{Type: typ, Source: "trace_test"}); err != nil {
			t.Fatalf("AppendEvent(%s): %v", typ, err)
		}
	}

	evs := readTraceLines(t, run)
	if len(evs) != len(allEventTypes) {
		t.Fatalf("got %d events, want %d", len(evs), len(allEventTypes))
	}
	first := evs[0]
	if first.Type != allEventTypes[0] || first.Seq != 1 {
		t.Fatalf("first event = %+v, want type %s seq 1", first, allEventTypes[0])
	}
	if first.Timestamp == "" || first.PrevHash == "" || first.Hash == "" {
		t.Fatalf("event must carry timestamp/prev_hash/hash, got %+v", first)
	}
	if err := VerifyChain(run); err != nil {
		t.Fatalf("VerifyChain after full allow-list run: %v", err)
	}
}

// TestSeqMonotonic: seq is per-file, starts at 1, increments on every append,
// and a caller-supplied seq never breaks the file's monotonic order.
func TestSeqMonotonic(t *testing.T) {
	useTraceDir(t)
	const run = "run-tr-2"

	mustAppend(t, run, EventWorkflowStarted)
	mustAppend(t, run, EventPhaseStarted)
	mustAppend(t, run, EventPhaseClosed)

	// Caller-supplied seq is garbage; the writer owns seq assignment.
	if err := AppendEvent(run, TraceEvent{Type: EventCheckpointOpened, Seq: 99, Source: "trace_test"}); err != nil {
		t.Fatalf("AppendEvent(checkpoint.opened): %v", err)
	}
	mustAppend(t, run, EventCheckpointDecided)

	evs := readTraceLines(t, run)
	wantSeqs := []int{1, 2, 3, 4, 5}
	if len(evs) != len(wantSeqs) {
		t.Fatalf("got %d events, want %d", len(evs), len(wantSeqs))
	}
	for i, ev := range evs {
		if ev.Seq != wantSeqs[i] {
			t.Fatalf("event %d has seq %d, want %d", i+1, ev.Seq, wantSeqs[i])
		}
	}
	if err := VerifyChain(run); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
}

// TestHashChainTamper: a clean chain verifies; any tampered line is detected
// with an error naming the first bad line — both when the stored hash goes
// stale and when the tamperer re-hashes the edited line (breaking prev_hash of
// the next line).
func TestHashChainTamper(t *testing.T) {
	useTraceDir(t)
	const run = "run-tr-3"

	mustAppend(t, run, EventWorkflowStarted)
	mustAppend(t, run, EventPhaseStarted)
	mustAppend(t, run, EventPhaseClosed)
	if err := VerifyChain(run); err != nil {
		t.Fatalf("VerifyChain on clean chain: %v", err)
	}

	path := traceFileFor(t, run)

	// Variant A: edit line 2's payload, keep its (now stale) hash.
	rewriteLine := func(f func(*TraceEvent) TraceEvent) string {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read trace: %v", err)
		}
		lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		var ev TraceEvent
		if err := json.Unmarshal([]byte(lines[1]), &ev); err != nil {
			t.Fatalf("unmarshal line 2: %v", err)
		}
		lines[1] = string(mustJSON(t, f(&ev)))
		out := strings.Join(lines, "\n") + "\n"
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			t.Fatalf("rewrite trace: %v", err)
		}
		return lines[1]
	}

	rewriteLine(func(ev *TraceEvent) TraceEvent {
		ev.Payload = map[string]interface{}{"injected": true}
		return *ev
	})
	err := VerifyChain(run)
	if err == nil {
		t.Fatal("VerifyChain accepted a tampered payload")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("tamper error must name line 2, got: %v", err)
	}

	// Variant B: tamperer re-hashes line 2 so it is internally consistent —
	// the chain still breaks, at line 2's own hash vs the recomputed value is
	// now fine, so line 3's prev_hash is what must catch it.
	cleanB, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	var ev2 TraceEvent
	lines := strings.Split(strings.TrimRight(string(cleanB), "\n"), "\n")
	if err := json.Unmarshal([]byte(lines[1]), &ev2); err != nil {
		t.Fatalf("unmarshal line 2: %v", err)
	}
	ev2.Hash = mustHashEvent(t, ev2)
	lines[1] = string(mustJSON(t, ev2))
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("rewrite trace: %v", err)
	}
	err = VerifyChain(run)
	if err == nil {
		t.Fatal("VerifyChain accepted a re-hashed tampered line")
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("re-hash tamper must surface at line 3, got: %v", err)
	}
}

// TestCriticalEventsMissing: the completeness rule — empty or absent trace is
// no error yet; with events present, workflow.started plus a terminal marker
// (workflow.ended | workflow.aborted | post_game.completed) is required.
func TestCriticalEventsMissing(t *testing.T) {
	dir := useTraceDir(t)

	// Absent file: no events, no error yet.
	if err := CriticalControlEventsPresent("run-tr-4"); err != nil {
		t.Fatalf("missing trace file must be no error yet, got: %v", err)
	}

	// Empty file: no error yet.
	if err := os.MkdirAll(filepath.Join(dir, ProjectKey()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(traceFileFor(t, "run-tr-4"), nil, 0o644); err != nil {
		t.Fatalf("write empty trace: %v", err)
	}
	if err := CriticalControlEventsPresent("run-tr-4"); err != nil {
		t.Fatalf("empty trace must be no error yet, got: %v", err)
	}

	// Events but no workflow.started.
	const run = "run-tr-5"
	mustAppend(t, run, EventPhaseStarted)
	if err := CriticalControlEventsPresent(run); err == nil {
		t.Fatal("events without workflow.started must be a protocol error")
	}

	// workflow.started but no terminal marker.
	const run2 = "run-tr-6"
	mustAppend(t, run2, EventWorkflowStarted)
	mustAppend(t, run2, EventPostGameStarted)
	if err := CriticalControlEventsPresent(run2); err == nil {
		t.Fatal("workflow.started without a terminal marker must be a protocol error")
	}

	// Each terminal marker completes the trace.
	terminals := []struct{ name, typ string }{
		{"workflow.ended", EventWorkflowEnded},
		{"workflow.aborted", EventWorkflowAborted},
		{"post_game.completed", EventPostGameCompleted},
	}
	for i, term := range terminals {
		runID := "run-tr-7-" + string(rune('a'+i))
		mustAppend(t, runID, EventWorkflowStarted)
		mustAppend(t, runID, term.typ)
		if err := CriticalControlEventsPresent(runID); err != nil {
			t.Fatalf("%s must complete the trace, got: %v", term.name, err)
		}
	}
}

// --- supporting checks --------------------------------------------------------

// TestTraceFirstPrevHash: the first event's prev_hash is sha256 of the empty
// string; subsequent prev_hash values commit to the previous line's canonical
// bytes.
func TestTraceFirstPrevHash(t *testing.T) {
	useTraceDir(t)
	const run = "run-tr-8"
	mustAppend(t, run, EventWorkflowStarted)
	mustAppend(t, run, EventToolStarted)

	evs := readTraceLines(t, run)
	if got, want := evs[0].PrevHash, emptySHA256(); got != want {
		t.Fatalf("first prev_hash = %s, want sha256(\"\") = %s", got, want)
	}
	prevLine := string(mustJSON(t, evs[0]))
	if got, want := evs[1].PrevHash, mustSHA256(t, prevLine); got != want {
		t.Fatalf("second prev_hash = %s, want sha256(%s) = %s", got, prevLine, want)
	}
}

// TestAppendEventIndependentOfRunState: trace append must not fail because
// run-state is unreadable — events are the durable record (task-2 context).
func TestAppendEventIndependentOfRunState(t *testing.T) {
	useTraceDir(t)
	// No StartRun, no run-state dir: AppendEvent works for an unknown run.
	const run = "run-tr-9"
	mustAppend(t, run, EventWorkflowStarted)
	if err := VerifyChain(run); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
}

// TestTraceRejectsUnsafeRunID: run ids become path segments — traversal and
// empty ids are refused by every entry point.
func TestTraceRejectsUnsafeRunID(t *testing.T) {
	useTraceDir(t)
	for _, runID := range []string{"", "..", "a/b", "run evil"} {
		if err := AppendEvent(runID, TraceEvent{Type: EventWorkflowStarted}); err == nil {
			t.Fatalf("AppendEvent accepted unsafe run id %q", runID)
		}
		if err := VerifyChain(runID); err == nil {
			t.Fatalf("VerifyChain accepted unsafe run id %q", runID)
		}
		if err := CriticalControlEventsPresent(runID); err == nil {
			t.Fatalf("CriticalControlEventsPresent accepted unsafe run id %q", runID)
		}
	}
}

// TestVerifyChainMissingFile: verifying a run with no trace file is an error,
// not silence.
func TestVerifyChainMissingFile(t *testing.T) {
	useTraceDir(t)
	if err := VerifyChain("run-tr-10"); err == nil {
		t.Fatal("VerifyChain on a missing trace file must error")
	}
}

// --- test utilities ------------------------------------------------------------

func mustJSON(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func mustHashEvent(t *testing.T, ev TraceEvent) string {
	t.Helper()
	h, err := eventHash(ev)
	if err != nil {
		t.Fatalf("eventHash: %v", err)
	}
	return h
}

func mustSHA256(t *testing.T, s string) string {
	t.Helper()
	return hashHex([]byte(s))
}

func emptySHA256() string { return hashHex(nil) }
