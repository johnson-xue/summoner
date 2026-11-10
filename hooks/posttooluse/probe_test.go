package main

// probe_test.go — T19 suite, posttooluse half: the admission-marker joins on
// a Bash completion — the in-band sandbox probe's effect observation (wrapper
// parsed ONLY for gate-marked probe calls; a forged wrapper line in an
// ordinary command's output is never observed) and the L2 check.result
// writer (pairing re-verified against the current registry, exit observed,
// honest BLOCKED otherwise, fail ticks the failed-check budget).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// writePostManifest drops a summoner.yaml with one project check.
func writePostManifest(t *testing.T, checkID, command string, args []string) shared.CheckDef {
	t.Helper()
	def := shared.CheckDef{CheckID: checkID, Command: command, Args: args, Cwd: ".", Trust: "project"}
	quoted := make([]string, 0, len(args))
	for _, a := range args {
		quoted = append(quoted, "'"+a+"'")
	}
	content := fmt.Sprintf("version: \"2\"\nproject:\n  name: post-proj\nphases:\n  diagnosis:\n    skill: none\ncheck_registry:\n  - check_id: %s\n    command: %s\n    args: [%s]\n    cwd: .\n    digest: %s\n    trust: project\n",
		checkID, command, strings.Join(quoted, ", "), shared.CheckDefDigest(def))
	if err := os.WriteFile(filepath.Join(os.Getenv("CLAUDE_PROJECT_DIR"), "summoner.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return def
}

// appendPostEvent is the test-side trace writer.
func appendPostEvent(t *testing.T, ev shared.TraceEvent) {
	t.Helper()
	if err := shared.AppendEvent("run-1", hookSession, ev); err != nil {
		t.Fatalf("append %s: %v", ev.Type, err)
	}
}

// admittedL2 simulates the gate's admission record of an exact-match check
// execution (read_class + the l2_check binding on tool.started).
func admittedL2(t *testing.T, toolUseID string, def shared.CheckDef) {
	t.Helper()
	appendPostEvent(t, shared.TraceEvent{
		Type:      shared.EventToolStarted,
		ToolUseID: toolUseID,
		Payload: map[string]interface{}{
			"tool_name":  "Bash",
			"read_class": true,
			"l2_check": map[string]interface{}{
				"check_id":  def.CheckID,
				"digest":    shared.CheckDefDigest(def),
				"cwd":       def.Cwd,
				"env_allow": []interface{}{"PATH"},
			},
		},
	})
}

// probeWrapperLine is one adapter wrapper stdout line.
func probeWrapperLine(effect string) string {
	return fmt.Sprintf(`{"probe":"summoner-sandbox-probe/v1","effect":%q}`, effect)
}

// TestProbeEffectObservedOnlyForMarkedCalls: the wrapper in a Bash stdout
// becomes a sandbox_probe observation on the tool.result ONLY when the gate
// marked that call as the probe channel; an unmarked call printing the same
// line (a forged effect) observes nothing, and a marked call without a
// parseable wrapper honestly observes "unproven".
func TestProbeEffectObservedOnlyForMarkedCalls(t *testing.T) {
	useHookEnv(t)
	startRun(t)

	// Marked probe call: the wrapper effect lands on tool.result.
	appendPostEvent(t, shared.TraceEvent{
		Type:      shared.EventToolStarted,
		ToolUseID: "tu-q1",
		Payload:   map[string]interface{}{"tool_name": "Bash", "read_class": true, "sandbox_probe": "v1"},
	})
	if code, stderr := callHook(t, postUsePayload(t, "Bash", "tu-q1",
		map[string]interface{}{"command": "/root/hooks/bin/summoner-sandbox-probe"},
		map[string]interface{}{"stdout": probeWrapperLine(shared.SandboxProbeRefused)})); code != 0 {
		t.Fatalf("probe result exit = %d (stderr: %q)", code, stderr)
	}
	observed := probeObservationOf(t, "tu-q1")
	if observed != shared.SandboxProbeRefused {
		t.Fatalf("observed effect = %q, want refused", observed)
	}

	// Marked call, garbage stdout: honest unproven, never a guess.
	appendPostEvent(t, shared.TraceEvent{
		Type:      shared.EventToolStarted,
		ToolUseID: "tu-q2",
		Payload:   map[string]interface{}{"tool_name": "Bash", "read_class": true, "sandbox_probe": "v1"},
	})
	callHook(t, postUsePayload(t, "Bash", "tu-q2",
		map[string]interface{}{"command": "/root/hooks/bin/summoner-sandbox-probe"},
		map[string]interface{}{"stdout": "operation not permitted"}))
	if observed := probeObservationOf(t, "tu-q2"); observed != shared.SandboxProbeUnproven {
		t.Fatalf("garbage stdout effect = %q, want unproven", observed)
	}

	// UNMARKED call printing the wrapper line: a forged effect — never
	// observed. This is the authority chain: only the gate's anchored-shape
	// admission grants the marker, and only the marker makes the wrapper an
	// observation.
	appendPostEvent(t, shared.TraceEvent{
		Type:      shared.EventToolStarted,
		ToolUseID: "tu-f1",
		Payload:   map[string]interface{}{"tool_name": "Bash", "read_class": true},
	})
	callHook(t, postUsePayload(t, "Bash", "tu-f1",
		map[string]interface{}{"command": "echo " + probeWrapperLine(shared.SandboxProbeRefused)},
		map[string]interface{}{"stdout": probeWrapperLine(shared.SandboxProbeRefused)}))
	for _, ev := range readHookTrace(t, "run-1") {
		if ev.Type == shared.EventToolResult && ev.ToolUseID == "tu-f1" {
			if _, has := ev.Payload["sandbox_probe"]; has {
				t.Fatal("an unmarked call's wrapper line must never become a sandbox_probe observation (forged effect)")
			}
		}
	}
}

// probeObservationOf returns the sandbox_probe effect recorded on one call's
// tool.result ("" when none).
func probeObservationOf(t *testing.T, toolUseID string) string {
	t.Helper()
	for _, ev := range readHookTrace(t, "run-1") {
		if ev.Type == shared.EventToolResult && ev.ToolUseID == toolUseID {
			m, _ := ev.Payload["sandbox_probe"].(map[string]interface{})
			effect, _ := m["effect"].(string)
			return effect
		}
	}
	return ""
}

// TestL2CheckResultWrittenOnCompletion: an admitted L2 execution concludes
// from the OBSERVED exit — pass with the point-in-time tier wording, fail
// with the budget tick, unobservable exit with an honest BLOCKED.
func TestL2CheckResultWrittenOnCompletion(t *testing.T) {
	useHookEnv(t)
	def := writePostManifest(t, "unit", "go", []string{"test", "./..."})
	startRun(t)
	// The paired refused probe BEFORE the admission.
	appendPostEvent(t, shared.TraceEvent{
		Type:      shared.EventToolStarted,
		ToolUseID: "tu-q1",
		Payload:   map[string]interface{}{"tool_name": "Bash", "read_class": true, "sandbox_probe": "v1"},
	})
	appendPostEvent(t, shared.TraceEvent{
		Type:      shared.EventToolResult,
		ToolUseID: "tu-q1",
		Payload:   map[string]interface{}{"sandbox_probe": map[string]interface{}{"effect": shared.SandboxProbeRefused}},
	})

	// Passing execution.
	admittedL2(t, "tu-c1", def)
	if code, stderr := callHook(t, postUsePayload(t, "Bash", "tu-c1",
		map[string]interface{}{"command": "go test ./..."},
		map[string]interface{}{"stdout": "ok", "exit_code": 0})); code != 0 {
		t.Fatalf("L2 pass exit = %d (stderr: %q)", code, stderr)
	}
	res := checkResultOf(t, "tu-c1")
	if res["status"] != shared.CheckStatusPass || res["trust"] != "project" {
		t.Fatalf("L2 pass result = %+v", res)
	}
	wantTier := fmt.Sprintf("L2(basis:paired_probe@%s, point-in-time)", "tu-q1")
	if res["tier"] != wantTier {
		t.Fatalf("tier = %v, want %q (point-in-time declared, never claimed end-to-end)", res["tier"], wantTier)
	}
	if res["digest"] != shared.CheckDefDigest(def) {
		t.Fatalf("digest = %v, want the current registry digest", res["digest"])
	}

	// Failing execution: status fail + the failed-check budget ticks.
	admittedL2(t, "tu-c2", def)
	if code, stderr := callHook(t, postUsePayload(t, "Bash", "tu-c2",
		map[string]interface{}{"command": "go test ./..."},
		map[string]interface{}{"stdout": "FAIL", "exit_code": 1})); code != 0 {
		t.Fatalf("L2 fail exit = %d (stderr: %q)", code, stderr)
	}
	if res := checkResultOf(t, "tu-c2"); res["status"] != shared.CheckStatusFail {
		t.Fatalf("L2 fail result = %+v", res)
	}
	if got := reloadHookRun(t).Counters.FailedChecks; got != 1 {
		t.Fatalf("FailedChecks = %d, want 1 (a failing L2 execution ticks the budget)", got)
	}

	// Unobservable exit: honest BLOCKED, no budget tick, no tier claim.
	admittedL2(t, "tu-c3", def)
	if code, stderr := callHook(t, postUsePayload(t, "Bash", "tu-c3",
		map[string]interface{}{"command": "go test ./..."},
		map[string]interface{}{"stdout": "ok"})); code != 0 {
		t.Fatalf("L2 unobservable exit = %d (stderr: %q)", code, stderr)
	}
	res = checkResultOf(t, "tu-c3")
	if res["status"] != shared.CheckStatusBlocked || res["reason"] != "exit_unobservable" {
		t.Fatalf("unobservable result = %+v, want blocked/exit_unobservable", res)
	}
	if _, has := res["tier"]; has {
		t.Fatal("a BLOCKED result must never claim a tier")
	}
	if got := reloadHookRun(t).Counters.FailedChecks; got != 1 {
		t.Fatalf("FailedChecks = %d, want still 1 (BLOCKED never ticks)", got)
	}
}

// TestL2CheckResultBlockedWithoutPairing: an admitted execution whose probe
// pairing does not hold at result time (no probe before the admission) is an
// honest BLOCKED sandbox_unproven — never credit.
func TestL2CheckResultBlockedWithoutPairing(t *testing.T) {
	useHookEnv(t)
	def := writePostManifest(t, "unit", "go", []string{"test", "./..."})
	startRun(t)
	admittedL2(t, "tu-c1", def)
	if code, stderr := callHook(t, postUsePayload(t, "Bash", "tu-c1",
		map[string]interface{}{"command": "go test ./..."},
		map[string]interface{}{"exit_code": 0})); code != 0 {
		t.Fatalf("exit = %d (stderr: %q)", code, stderr)
	}
	if res := checkResultOf(t, "tu-c1"); res["status"] != shared.CheckStatusBlocked || res["reason"] != "sandbox_unproven" {
		t.Fatalf("unpaired result = %+v, want blocked/sandbox_unproven", res)
	}
}

// TestL2CheckResultDefinitionDrift: an admission binding whose digest no
// longer matches the registry (the definition changed mid-flight) concludes
// BLOCKED definition_changed — no credit under a definition nobody reviewed.
func TestL2CheckResultDefinitionDrift(t *testing.T) {
	useHookEnv(t)
	def := writePostManifest(t, "unit", "go", []string{"test", "./..."})
	startRun(t)
	// Admission under the CURRENT digest, then the registry changes.
	admittedL2(t, "tu-c1", def)
	writePostManifest(t, "unit", "go", []string{"test", "./...", "-count=1"})
	if code, stderr := callHook(t, postUsePayload(t, "Bash", "tu-c1",
		map[string]interface{}{"command": "go test ./..."},
		map[string]interface{}{"exit_code": 0})); code != 0 {
		t.Fatalf("exit = %d (stderr: %q)", code, stderr)
	}
	if res := checkResultOf(t, "tu-c1"); res["status"] != shared.CheckStatusBlocked || res["reason"] != "definition_changed" {
		t.Fatalf("drifted result = %+v, want blocked/definition_changed", res)
	}
}

// TestOrdinaryBashResultUnjoined: a read-class Bash call with no admission
// markers gets its plain tool.result and NEVER a check.result.
func TestOrdinaryBashResultUnjoined(t *testing.T) {
	useHookEnv(t)
	startRun(t)
	appendPostEvent(t, shared.TraceEvent{
		Type:      shared.EventToolStarted,
		ToolUseID: "tu-r1",
		Payload:   map[string]interface{}{"tool_name": "Bash", "read_class": true},
	})
	if code, stderr := callHook(t, postUsePayload(t, "Bash", "tu-r1",
		map[string]interface{}{"command": "go test ./..."},
		map[string]interface{}{"exit_code": 0})); code != 0 {
		t.Fatalf("ordinary read exit = %d (stderr: %q)", code, stderr)
	}
	if res := eventsOfType(readHookTrace(t, "run-1"), shared.EventCheckResult); len(res) != 0 {
		t.Fatalf("unadmitted executions must never produce check.results, got %+v", res)
	}
}

// checkResultOf returns the check.result payload of one tool_use_id's
// execution (nil when absent).
func checkResultOf(t *testing.T, toolUseID string) map[string]interface{} {
	t.Helper()
	for _, ev := range readHookTrace(t, "run-1") {
		if ev.Type == shared.EventCheckResult && ev.ToolUseID == toolUseID {
			return ev.Payload
		}
	}
	t.Fatalf("no check.result for %s", toolUseID)
	return nil
}
