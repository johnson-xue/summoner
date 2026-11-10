package contract

// probe_e2e_test.go — T19 contract suite: the crash-recovery loop and the
// check trust ladder driven END-TO-END over the real hook binaries, plus the
// in-band sandbox probe adapter binary itself.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// writeContractCheckManifest drops a summoner.yaml with a check_registry.
func writeContractCheckManifest(t *testing.T, defs ...shared.CheckDef) {
	t.Helper()
	dir := os.Getenv("CLAUDE_PROJECT_DIR")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("version: \"2\"\nproject:\n  name: contract-probe\nphases:\n  build:\n    skill: none\ncheck_registry:\n")
	for _, d := range defs {
		quoted := make([]string, 0, len(d.Args))
		for _, a := range d.Args {
			quoted = append(quoted, "'"+a+"'")
		}
		fmt.Fprintf(&b, "  - check_id: %s\n    command: %s\n    args: [%s]\n    cwd: .\n    digest: %s\n    trust: %s\n",
			d.CheckID, d.Command, strings.Join(quoted, ", "), shared.CheckDefDigest(d), d.Trust)
	}
	if err := os.WriteFile(filepath.Join(dir, "summoner.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// probeSubmitViaGate drives Skill(summoner:probe-submit) through the gate.
func probeSubmitViaGate(t *testing.T, toolUseID string, unknowns []string, outcome string, probes []string) (int, string, string) {
	t.Helper()
	return runBinary(t, "pretooluse-gate", gatePayload(t, "Skill", toolUseID, map[string]interface{}{
		"skill": "summoner:probe-submit",
		"args": mustJSON(t, map[string]interface{}{
			"unknown_tool_use_ids": unknowns,
			"outcome":              outcome,
			"probe_tool_use_ids":   probes,
		}),
	}), nil)
}

// bashResultViaPostToolUse records a Bash completion through the real
// posttooluse binary (tool_input carries the command; tool_response the
// stdout/exit the platform would report).
func bashResultViaPostToolUse(t *testing.T, toolUseID, command, stdout string, exit *int) {
	t.Helper()
	resp := map[string]interface{}{"stdout": stdout}
	if exit != nil {
		resp["exit_code"] = *exit
	}
	code, _, stderr := runBinary(t, "posttooluse", mustJSON(t, map[string]interface{}{
		"hook_event_name": "PostToolUse",
		"session_id":      contractSession,
		"tool_name":       "Bash",
		"tool_use_id":     toolUseID,
		"tool_input":      map[string]interface{}{"command": command},
		"tool_response":   resp,
	}), nil)
	if code != 0 {
		t.Fatalf("posttooluse(%s) exit = %d, want 0 (stderr: %q)", toolUseID, code, stderr)
	}
}

// TestEndToEndCrashRecoveryLoop: a gate-allowed write whose result never
// lands (the crash window) is caught by the SessionStart scan → the run goes
// OUTCOME_UNKNOWN → writes are denied → a read-class probe Bash observes →
// probe-submit resolves: all-accepted restores ACTIVE; a failed outcome
// opens the RECOVERY checkpoint; a one-probe-for-two-unknowns batch never
// partially applies.
func TestEndToEndCrashRecoveryLoop(t *testing.T) {
	useContractEnv(t)
	writeContractManifest(t, "")
	runID := "run-crash"
	seedLiveRun(t, runID, "new")

	// The crash window: the REAL gate allows two Writes (tool.started on the
	// trace) and their PostToolUse never fires.
	for _, tu := range []string{"tu-w1", "tu-w3"} {
		if code, _, stderr := runBinary(t, "pretooluse-gate",
			gatePayload(t, "Write", tu, map[string]interface{}{"file_path": "/tmp/x.go", "content": "package x"}), nil); code != 0 {
			t.Fatalf("the pre-crash write %s exit = %d, want 0 (stderr: %q)", tu, code, stderr)
		}
	}

	// Session restart: the scan converts BOTH dangling intents (one episode).
	if code, _, stderr := runBinary(t, "session-start", sessionStartPayload(t), nil); code != 0 {
		t.Fatalf("session-start exit = %d, want 0 — it never fails the session (stderr: %q)", code, stderr)
	}
	run, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != shared.StatusOutcomeUnknown || len(run.OutcomeUnknowns) != 2 {
		t.Fatalf("after the scan: status %s entries %+v, want OUTCOME_UNKNOWN / two entries", run.Status, run.OutcomeUnknowns)
	}
	marks := eventsOfType(allTraceEvents(t), shared.EventToolOutcomeUnknown)
	if len(marks) != 2 {
		t.Fatalf("tool.outcome_unknown events = %d, want 2 (one per entry)", len(marks))
	}
	for _, m := range marks {
		if m.Payload["detected_by"] != "session-start" {
			t.Fatalf("marking %+v must carry detected_by session-start", m.Payload)
		}
	}

	// Mutations are denied while the episode is open (never auto-replayed).
	if code, _, stderr := runBinary(t, "pretooluse-gate",
		gatePayload(t, "Edit", "tu-w2", map[string]interface{}{"file_path": "/tmp/y.go", "old_string": "a", "new_string": "b"}), nil); code != 2 || !strings.Contains(stderr, "OUTCOME_UNKNOWN") {
		t.Fatalf("write during OUTCOME_UNKNOWN: exit %d stderr %q, want exit 2 naming OUTCOME_UNKNOWN", code, stderr)
	}

	// One recovery probe observed.
	if code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Bash", "tu-p1", map[string]interface{}{"command": "ls /tmp"}), nil); code != 0 {
		t.Fatalf("probe Bash exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	bashResultViaPostToolUse(t, "tu-p1", "ls /tmp", "x.go", nil)

	// A twin batch (one probe resolving two unknowns) never partially applies.
	code, _, stderr := probeSubmitViaGate(t, "tu-ps-twin", []string{"tu-w1", "tu-w3"}, shared.OutcomeAccepted, []string{"tu-p1", "tu-p1"})
	if code != 2 || !strings.Contains(stderr, "reuses probe") {
		t.Fatalf("twin batch: exit %d stderr %q, want exit 2 rejecting the reused probe", code, stderr)
	}
	if run, _ = shared.LoadRun(runID); run.OutcomeUnknowns[0].Resolved || run.OutcomeUnknowns[1].Resolved {
		t.Fatalf("a rejected twin batch must resolve nothing: %+v", run.OutcomeUnknowns)
	}

	// The clean resolution of BOTH unknowns (each its own probe).
	if code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Bash", "tu-p2", map[string]interface{}{"command": "cat /tmp/x.go"}), nil); code != 0 {
		t.Fatalf("second probe Bash exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	bashResultViaPostToolUse(t, "tu-p2", "cat /tmp/x.go", "package x", nil)
	if code, _, stderr := probeSubmitViaGate(t, "tu-ps-1", []string{"tu-w1", "tu-w3"}, shared.OutcomeAccepted, []string{"tu-p1", "tu-p2"}); code != 0 {
		t.Fatalf("probe-submit exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if run, _ = shared.LoadRun(runID); run.Status != shared.StatusActive {
		t.Fatalf("all-accepted episode must restore ACTIVE, got %s", run.Status)
	}
	if n := len(eventsOfType(allTraceEvents(t), shared.EventToolResolved)); n != 2 {
		t.Fatalf("tool.resolved events = %d, want 2", n)
	}

	// A FAILED outcome on a fresh episode (this one caught by the Stop scan)
	// opens the recovery checkpoint.
	if code, _, stderr := runBinary(t, "pretooluse-gate",
		gatePayload(t, "Write", "tu-w4", map[string]interface{}{"file_path": "/tmp/w.go", "content": "package w"}), nil); code != 0 {
		t.Fatalf("post-restore write exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if code, _, stderr := runBinary(t, "stop", stopRenderPayload(t, "turn end"), nil); code != 0 {
		t.Fatalf("stop exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if run, _ = shared.LoadRun(runID); len(run.OutcomeUnknowns) != 3 || run.OutcomeUnknowns[2].ToolUseID != "tu-w4" {
		t.Fatalf("after the Stop scan: entries %+v, want tu-w4 marked third", run.OutcomeUnknowns)
	}
	if code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Bash", "tu-p3", map[string]interface{}{"command": "stat /tmp/w.go"}), nil); code != 0 {
		t.Fatalf("third probe Bash exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	bashResultViaPostToolUse(t, "tu-p3", "stat /tmp/w.go", "no such file", nil)
	if code, _, stderr := probeSubmitViaGate(t, "tu-ps-2", []string{"tu-w4"}, shared.OutcomeFailed, []string{"tu-p3"}); code != 0 {
		t.Fatalf("failed probe-submit exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	run, _ = shared.LoadRun(runID)
	if run.Status != shared.StatusWaitUser || run.CurrentCheckpoint() == nil || run.CurrentCheckpoint().Kind != shared.CheckpointRecovery {
		t.Fatalf("failed episode must open a RECOVERY checkpoint, got status %s cp %+v", run.Status, run.CurrentCheckpoint())
	}
}

// TestEndToEndCheckLadder: L3 refused honestly; L2 needs the paired in-band
// probe (absent → BLOCKED, present+refused → admitted execution whose result
// carries the point-in-time tier); L1 runs in-gate at phase-submit and only
// fail ticks the budget; the observed L2 pass is then citable.
func TestEndToEndCheckLadder(t *testing.T) {
	useContractEnv(t)
	writeContractCheckManifest(t,
		shared.CheckDef{CheckID: "unit", Command: "go", Args: []string{"test", "./..."}, Cwd: ".", Trust: "project"},
		shared.CheckDef{CheckID: "netcheck", Command: "curl", Args: []string{"-s", "https://example.com"}, Cwd: ".", Trust: "project"},
		shared.CheckDef{CheckID: "has-readme", Command: shared.CheckOpFileContains, Args: []string{"README.md", "summoner"}, Cwd: ".", Trust: "framework"},
	)
	if err := os.WriteFile(filepath.Join(os.Getenv("CLAUDE_PROJECT_DIR"), "README.md"), []byte("# summoner contract\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runID := "run-ladder"
	seedLiveRun(t, runID, "new")

	// L3: refused, never executed, honestly traced.
	code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Bash", "tu-c-net", map[string]interface{}{"command": "curl -s https://example.com"}), nil)
	if code != 2 || !strings.Contains(stderr, "l3_unsupported") {
		t.Fatalf("L3 execution: exit %d stderr %q, want exit 2 / l3_unsupported", code, stderr)
	}
	net := eventsOfType(allTraceEvents(t), shared.EventCheckResult)
	if len(net) != 1 || net[0].Payload["check_id"] != "netcheck" || net[0].Payload["reason"] != "l3_unsupported" || net[0].Payload["status"] != "blocked" {
		t.Fatalf("L3 check.results = %+v", net)
	}

	// L2 without a probe: honest BLOCKED sandbox_unproven.
	code, _, stderr = runBinary(t, "pretooluse-gate", gatePayload(t, "Bash", "tu-c-1", map[string]interface{}{"command": "go test ./..."}), nil)
	if code != 2 || !strings.Contains(stderr, "sandbox_unproven") {
		t.Fatalf("unprobed L2: exit %d stderr %q, want exit 2 / sandbox_unproven", code, stderr)
	}

	// The in-band probe: gated (marker recorded), observed through
	// posttooluse with the adapter's wrapper on stdout.
	probeCmd := `"${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-sandbox-probe"`
	if code, _, stderr = runBinary(t, "pretooluse-gate", gatePayload(t, "Bash", "tu-q1", map[string]interface{}{"command": probeCmd}), nil); code != 0 {
		t.Fatalf("probe invocation exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	bashResultViaPostToolUse(t, "tu-q1", probeCmd,
		`{"probe":"summoner-sandbox-probe/v1","effect":"refused","detail":"operation not permitted"}`, nil)
	observed := false
	for _, ev := range eventsOfType(allTraceEvents(t), shared.EventToolResult) {
		if ev.ToolUseID == "tu-q1" {
			if m, ok := ev.Payload["sandbox_probe"].(map[string]interface{}); ok && m["effect"] == shared.SandboxProbeRefused {
				observed = true
			}
		}
	}
	if !observed {
		t.Fatal("the probe's refused effect must be observed on tool.result")
	}

	// L2 admitted and passing: the tier declares the point-in-time basis.
	if code, _, stderr = runBinary(t, "pretooluse-gate", gatePayload(t, "Bash", "tu-c-2", map[string]interface{}{"command": "go test ./..."}), nil); code != 0 {
		t.Fatalf("admitted L2 exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	exit := 0
	bashResultViaPostToolUse(t, "tu-c-2", "go test ./...", "ok", &exit)
	var l2pass map[string]interface{}
	for _, ev := range eventsOfType(allTraceEvents(t), shared.EventCheckResult) {
		if ev.ToolUseID == "tu-c-2" {
			l2pass = ev.Payload
		}
	}
	if l2pass == nil || l2pass["status"] != shared.CheckStatusPass {
		t.Fatalf("L2 pass check.result = %+v", l2pass)
	}
	if want := "L2(basis:paired_probe@tu-q1, point-in-time)"; l2pass["tier"] != want {
		t.Fatalf("L2 tier = %v, want %q", l2pass["tier"], want)
	}

	// L1 at submit: runs in-gate over the producer's refs ("new"'s first
	// expected phase is context).
	args := mustJSON(t, shared.PhaseSubmission{
		PhaseID:   "context",
		Summary:   "context done",
		CheckRefs: []string{"check://has-readme", "check://unit"},
	})
	if code, _, stderr = runBinary(t, "pretooluse-gate", gatePayload(t, "Skill", "tu-sub-1", map[string]interface{}{
		"skill": "summoner:phase-submit", "args": args,
	}), nil); code != 0 {
		t.Fatalf("submit with refs exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	l1 := 0
	for _, ev := range eventsOfType(allTraceEvents(t), shared.EventCheckResult) {
		if ev.ToolUseID == "tu-sub-1" && ev.Payload["check_id"] == "has-readme" && ev.Payload["status"] == shared.CheckStatusPass && ev.Payload["tier"] == "L1" {
			l1++
		}
	}
	if l1 != 1 {
		t.Fatalf("in-gate L1 results for has-readme = %d, want 1 (the unit ref needed no new event: its observed L2 pass is current)", l1)
	}
}

// TestSandboxProbeAdapterBinary: the adapter prints exactly one parseable
// wrapper, always exits 0, never leaves its fresh file behind, and classifies
// a read-only HOME as refused (the write the sandbox must refuse).
func TestSandboxProbeAdapterBinary(t *testing.T) {
	useContractEnv(t)

	// Writable HOME: writable effect, no leftover probe file.
	writable := t.TempDir()
	code, stdout, stderr := runBinary(t, "summoner-sandbox-probe", "", map[string]string{"HOME": writable})
	if code != 0 {
		t.Fatalf("adapter exit = %d, want 0 always (stderr: %q)", code, stderr)
	}
	effect, ok := shared.ParseSandboxProbeWrapper(stdout)
	if !ok || effect != shared.SandboxProbeWritable {
		t.Fatalf("writable-HOME wrapper = %q → (%q, %v), want writable", stdout, effect, ok)
	}
	entries, err := os.ReadDir(writable)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("adapter left %d files in HOME (%v), want none — it removes only its own fresh file", len(entries), entries)
	}
	// An EXISTING file at any plausible probe path is never touched: pre-seed
	// one and run again; it must survive byte-identical.
	seed := filepath.Join(writable, ".summoner-sandbox-probe")
	if err := os.WriteFile(seed, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	runBinary(t, "summoner-sandbox-probe", "", map[string]string{"HOME": writable})
	b, err := os.ReadFile(seed)
	if err != nil || string(b) != "precious" {
		t.Fatalf("the pre-existing probe-path file must never be touched, got %q (%v)", string(b), err)
	}

	// Read-only HOME (skip under root, where mode bits do not refuse): the
	// fresh write is refused → effect refused.
	if os.Geteuid() != 0 {
		ro := t.TempDir()
		if err := os.Chmod(ro, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(ro, 0o700) })
		code, stdout, stderr = runBinary(t, "summoner-sandbox-probe", "", map[string]string{"HOME": ro})
		if code != 0 {
			t.Fatalf("refused adapter exit = %d, want 0 (stderr: %q)", code, stderr)
		}
		effect, ok = shared.ParseSandboxProbeWrapper(stdout)
		if !ok || effect != shared.SandboxProbeRefused {
			t.Fatalf("read-only-HOME wrapper = %q → (%q, %v), want refused", stdout, effect, ok)
		}
	}
}
