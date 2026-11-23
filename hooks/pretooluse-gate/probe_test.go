package main

// probe_test.go — T19 suite, gate half: the summoner:probe-submit channel
// (OUTCOME_UNKNOWN resolution with per-unknown probe evidence, whole-batch
// validation, replay idempotency) and the Bash-side check trust ladder (L2
// admission with the paired in-band probe, L3 honest BLOCKED, write-class
// checks never admitted; L1 refs at phase-submit).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// --- ladder fixtures -----------------------------------------------------------

// ladderCheck is one check_registry entry with its digest computed.
func ladderCheck(checkID, command string, args []string, trust string) string {
	def := shared.CheckDef{CheckID: checkID, Command: command, Args: args, Cwd: ".", Trust: trust}
	return fmt.Sprintf("  - check_id: %s\n    command: %s\n    args: [%s]\n    cwd: .\n    digest: %s\n    trust: %s\n",
		checkID, command, yamlList(args), shared.CheckDefDigest(def), trust)
}

func yamlList(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, it := range items {
		quoted = append(quoted, "'"+it+"'")
	}
	return strings.Join(quoted, ", ")
}

// writeLadderManifest drops a summoner.yaml with a full check_registry.
func writeLadderManifest(t *testing.T, entries ...string) {
	t.Helper()
	writeProjectManifest(t, fmt.Sprintf(
		"version: \"2\"\nproject:\n  name: ladder-proj\nphases:\n  diagnosis:\n    skill: none\ncheck_registry:\n%s", strings.Join(entries, "")))
}

// appendTraceEvent is the test-side trace writer (chain-verified by the
// shared package; readGateTrace verifies it back).
func appendTraceEvent(t *testing.T, runID string, ev shared.TraceEvent) {
	t.Helper()
	if err := shared.AppendEvent(runID, gateSession, ev); err != nil {
		t.Fatalf("append %s: %v", ev.Type, err)
	}
}

// observeProbe simulates the gate+posttooluse record of ONE in-band sandbox
// probe: an admitted read-class Bash call (the anchored invocation — marker
// recorded by the gate) whose result carries the wrapper observation.
func observeProbe(t *testing.T, runID, toolUseID, effect string) {
	t.Helper()
	appendTraceEvent(t, runID, shared.TraceEvent{
		Type:      shared.EventToolStarted,
		ToolUseID: toolUseID,
		Payload:   map[string]interface{}{"tool_name": "Bash", "read_class": true, "sandbox_probe": "v1"},
	})
	appendTraceEvent(t, runID, shared.TraceEvent{
		Type:      shared.EventToolResult,
		ToolUseID: toolUseID,
		Payload:   map[string]interface{}{"tool_name": "Bash", "sandbox_probe": map[string]interface{}{"effect": effect}},
	})
}

// observeRecoveryProbe simulates the record of a read-class Bash probe whose
// EVIDENCE value (outcome resolution) matters, not its sandbox effect. The
// call never consumed a ticket (no probe_of) and carries no recorded match.
func observeRecoveryProbe(t *testing.T, runID, toolUseID string) {
	t.Helper()
	appendTraceEvent(t, runID, shared.TraceEvent{
		Type:      shared.EventToolStarted,
		ToolUseID: toolUseID,
		Payload:   map[string]interface{}{"tool_name": "Bash", "read_class": true, "command": "ls -la"},
	})
	appendTraceEvent(t, runID, shared.TraceEvent{
		Type:      shared.EventToolResult,
		ToolUseID: toolUseID,
		Payload:   map[string]interface{}{"tool_name": "Bash"},
	})
}

// recoveryDecl is the canonical declared-unknown fixture: a supported
// file.write declaration whose exact probe command exists.
func recoveryDecl(target string) *shared.MutationDecl {
	return &shared.MutationDecl{
		Capability: shared.CapabilityFileWrite, Target: target,
		Preimage: "p0-" + target, Expect: "e1-" + target,
	}
}

// observeBoundProbe simulates the full mechanical record (recovery round I3):
// the gate admission that consumed the unknown's one-shot ticket (probe_of +
// the exact declared command) and posttooluse's matcher verdict on its result.
func observeBoundProbe(t *testing.T, runID, probeID, unknownID string, decl *shared.MutationDecl, verdict string) {
	t.Helper()
	appendTraceEvent(t, runID, shared.TraceEvent{
		Type:      shared.EventToolStarted,
		ToolUseID: probeID,
		Payload: map[string]interface{}{
			"tool_name": "Bash", "read_class": true,
			"command": decl.ExactProbeCommand(), "probe_of": unknownID,
		},
	})
	appendTraceEvent(t, runID, shared.TraceEvent{
		Type:      shared.EventToolResult,
		ToolUseID: probeID,
		Payload: map[string]interface{}{
			"tool_name":   "Bash",
			"probe_match": map[string]interface{}{"unknown_id": unknownID, "verdict": verdict},
		},
	})
}

// parkOutcomeUnknown drives a fix run into OUTCOME_UNKNOWN with one marked
// entry (the marking event appended exactly as session-start/stop do).
func parkOutcomeUnknown(t *testing.T, r *shared.RunState, unknownID string) {
	t.Helper()
	parkOutcomeUnknownDecl(t, r, unknownID, nil)
}

// parkOutcomeUnknownDecl is the declared form: the entry freezes the intent's
// pre-effect declaration (the mechanical matcher resolves against it).
func parkOutcomeUnknownDecl(t *testing.T, r *shared.RunState, unknownID string, decl *shared.MutationDecl) {
	t.Helper()
	acceptPhase(t, r, "diagnosis") // writes legal afterwards
	r.MarkOutcomeUnknownWithDecl(unknownID, decl)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	appendTraceEvent(t, r.RunID, shared.TraceEvent{
		Type:      shared.EventToolOutcomeUnknown,
		ToolUseID: unknownID,
		Payload:   map[string]interface{}{"tool_use_id": unknownID, "detected_by": "stop"},
	})
}

// probeSubmitPayload builds the Skill(summoner:probe-submit) gate payload with
// a custom tool_use_id — the mechanical resolve form (recovery round I3: no
// outcome on the wire; the matcher's recorded verdicts decide).
func probeSubmitPayloadAt(t *testing.T, toolUseID string, unknowns, probes []string) string {
	t.Helper()
	return mustJSONStr(t, map[string]interface{}{
		"session_id":  gateSession,
		"tool_name":   "Skill",
		"tool_use_id": toolUseID,
		"tool_input": map[string]interface{}{
			"skill": "summoner:probe-submit",
			"args": mustJSONStr(t, map[string]interface{}{
				"op":                   "resolve",
				"unknown_tool_use_ids": unknowns,
				"probe_tool_use_ids":   probes,
			}),
		},
	})
}

// --- probe-submit channel ------------------------------------------------------

// TestProbeSubmitChannelLegality: the channel requires a live run in
// OUTCOME_UNKNOWN; malformed batches never partially apply.
func TestProbeSubmitChannelLegality(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})

	// No live run at all (a session with no runs).
	code, stderr := callGate(t, strings.Replace(probeSubmitPayloadAt(t, "tu-ps0", []string{"tu-x"}, []string{"tu-p"}), gateSession, "sess-none", 1))
	wantExit2(t, code, stderr, "requires an active run")

	// ACTIVE (no episode): status denial.
	code, stderr = callGate(t, probeSubmitPayloadAt(t, "tu-ps1", []string{"tu-x"}, []string{"tu-p"}))
	wantExit2(t, code, stderr, "OUTCOME_UNKNOWN")

	// WAIT_USER: the checkpoint discipline blocks it first.
	cp := submitDisplayedPhase(t, r, "diagnosis", nil)
	_ = cp
	code, stderr = callGate(t, probeSubmitPayloadAt(t, "tu-ps2", []string{"tu-x"}, []string{"tu-p"}))
	wantExit2(t, code, stderr, "WAIT_USER")
	if err := r.RecordDecision(cp.ID, shared.DecisionContinue, ""); err != nil {
		t.Fatal(err)
	}
	acceptPhase(t, r, "diagnosis") // ensure the phase visit settles; writes legal

	// Episode with one DECLARED entry (the mechanical path needs a decl).
	decl := recoveryDecl("w1.md")
	r.MarkOutcomeUnknownWithDecl("tu-u1", decl)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	appendTraceEvent(t, "run-1", shared.TraceEvent{
		Type: shared.EventToolOutcomeUnknown, ToolUseID: "tu-u1",
		Payload: map[string]interface{}{"tool_use_id": "tu-u1", "detected_by": "stop"},
	})

	// Shape failures: arity mismatch, empty batch, and the REMOVED outcome
	// field (the wire form is gone — verdicts belong to the matcher).
	code, stderr = callGate(t, probeSubmitPayloadAt(t, "tu-ps3", []string{"tu-u1", "tu-u2"}, []string{"tu-p"}))
	wantExit2(t, code, stderr, "one-for-one")
	code, stderr = callGate(t, probeSubmitPayloadAt(t, "tu-ps4", nil, nil))
	wantExit2(t, code, stderr, "at least one")
	code, stderr = callGate(t, toolPayload(t, "Skill", skillInput("summoner:probe-submit",
		`{"unknown_tool_use_ids":["tu-u1"],"outcome":"maybe","probe_tool_use_ids":["tu-p"]}`)))
	wantExit2(t, code, stderr, "matcher")

	// Foreign/invented unknown id.
	code, stderr = callGate(t, probeSubmitPayloadAt(t, "tu-ps6", []string{"tu-invented"}, []string{"tu-p"}))
	wantExit2(t, code, stderr, "no such OUTCOME_UNKNOWN entry")

	// Probe with no observed call/result.
	code, stderr = callGate(t, probeSubmitPayloadAt(t, "tu-ps7", []string{"tu-u1"}, []string{"tu-ghost"}))
	wantExit2(t, code, stderr, "not valid evidence")

	// Write-class probe (no read_class marker on its started event).
	appendTraceEvent(t, "run-1", shared.TraceEvent{
		Type: shared.EventToolStarted, ToolUseID: "tu-pw",
		Payload: map[string]interface{}{"tool_name": "Bash"},
	})
	appendTraceEvent(t, "run-1", shared.TraceEvent{
		Type: shared.EventToolResult, ToolUseID: "tu-pw",
		Payload: map[string]interface{}{"tool_name": "Bash"},
	})
	code, stderr = callGate(t, probeSubmitPayloadAt(t, "tu-ps8", []string{"tu-u1"}, []string{"tu-pw"}))
	wantExit2(t, code, stderr, "not valid evidence")

	// Stale probe: its result predates the marking event.
	appendTraceEvent(t, "run-1", shared.TraceEvent{
		Type: shared.EventToolResult, ToolUseID: "tu-old",
		Payload: map[string]interface{}{"tool_name": "Bash"},
	})
	code, stderr = callGate(t, probeSubmitPayloadAt(t, "tu-ps9", []string{"tu-u1"}, []string{"tu-old"}))
	wantExit2(t, code, stderr, "not valid evidence")

	// An observed read-class probe that never consumed the ticket: valid
	// evidence, but a DECLARED unknown resolves only through its bound exact
	// command (R6 false-allow).
	observeRecoveryProbe(t, "run-1", "tu-loose")
	code, stderr = callGate(t, probeSubmitPayloadAt(t, "tu-psL", []string{"tu-u1"}, []string{"tu-loose"}))
	wantExit2(t, code, stderr, "does not bind")

	// A bound probe with the matcher's accepted verdict resolves the episode:
	// tool.resolved appended, run ACTIVE.
	observeBoundProbe(t, "run-1", "tu-p1", "tu-u1", decl, shared.OutcomeAccepted)
	code, stderr = callGate(t, probeSubmitPayloadAt(t, "tu-psA", []string{"tu-u1"}, []string{"tu-p1"}))
	if code != 0 {
		t.Fatalf("valid probe-submit exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	after := reloadRun(t, "run-1")
	if after.Status != shared.StatusActive {
		t.Fatalf("all-accepted episode must restore ACTIVE, got %s", after.Status)
	}
	resolved := gateEventsOfType(t, "run-1", shared.EventToolResolved)
	if len(resolved) != 1 || resolved[0].Payload["tool_use_id"] != "tu-u1" || resolved[0].Payload["probe_tool_use_id"] != "tu-p1" {
		t.Fatalf("tool.resolved events = %+v, want one tu-u1 via tu-p1", resolved)
	}

	// Identical replay: silent no-op (exit 0, no second tool.resolved).
	code, stderr = callGate(t, probeSubmitPayloadAt(t, "tu-psB", []string{"tu-u1"}, []string{"tu-p1"}))
	if code != 0 {
		t.Fatalf("idempotent replay exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if n := len(gateEventsOfType(t, "run-1", shared.EventToolResolved)); n != 1 {
		t.Fatalf("replay appended %d tool.resolved events, want still 1", n)
	}

	// A DIFFERENT probe for the resolved id is rejected even with fresh evidence.
	observeRecoveryProbe(t, "run-1", "tu-p2")
	code, stderr = callGate(t, probeSubmitPayloadAt(t, "tu-psC", []string{"tu-u1"}, []string{"tu-p2"}))
	wantExit2(t, code, stderr, "already resolved")
}

// TestProbeSubmitTwinAndReuseRejection: one probe never resolves two unknowns
// (inside one batch or across episodes), and every whole-batch failure leaves
// EVERY entry unresolved.
func TestProbeSubmitTwinAndReuseRejection(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r, "diagnosis")
	declA, declB := recoveryDecl("a.md"), recoveryDecl("b.md")
	r.MarkOutcomeUnknownWithDecl("tu-a", declA)
	r.MarkOutcomeUnknownWithDecl("tu-b", declB)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"tu-a", "tu-b"} {
		appendTraceEvent(t, "run-1", shared.TraceEvent{
			Type: shared.EventToolOutcomeUnknown, ToolUseID: id,
			Payload: map[string]interface{}{"tool_use_id": id, "detected_by": "stop"},
		})
	}
	observeRecoveryProbe(t, "run-1", "tu-shared")

	// One probe for two unknowns in one batch.
	code, stderr := callGate(t, probeSubmitPayloadAt(t, "tu-ps1", []string{"tu-a", "tu-b"}, []string{"tu-shared", "tu-shared"}))
	wantExit2(t, code, stderr, "reuses probe tu-shared")
	// Mismatched array (the twin via arity).
	code, stderr = callGate(t, probeSubmitPayloadAt(t, "tu-ps2", []string{"tu-a", "tu-b"}, []string{"tu-shared"}))
	wantExit2(t, code, stderr, "one-for-one")
	for _, id := range []string{"tu-a", "tu-b"} {
		if e := entryOf(t, reloadRun(t, "run-1"), id); e.Resolved {
			t.Fatalf("%s must stay unresolved after a rejected batch", id)
		}
	}

	// A DIFFERENT probe exists; a two-pair batch where the SECOND pair is
	// invalid must resolve NOTHING (whole-batch validation before transition).
	observeBoundProbe(t, "run-1", "tu-good", "tu-a", declA, shared.OutcomeAccepted)
	code, stderr = callGate(t, probeSubmitPayloadAt(t, "tu-ps3", []string{"tu-a", "tu-b"}, []string{"tu-good", "tu-ghost"}))
	wantExit2(t, code, stderr, "not valid evidence")
	for _, id := range []string{"tu-a", "tu-b"} {
		if e := entryOf(t, reloadRun(t, "run-1"), id); e.Resolved {
			t.Fatalf("%s must stay unresolved — the whole batch was invalid", id)
		}
	}

	// The clean two-pair batch resolves both (episode completes; restore).
	observeBoundProbe(t, "run-1", "tu-good2", "tu-b", declB, shared.OutcomeAccepted)
	code, stderr = callGate(t, probeSubmitPayloadAt(t, "tu-ps4", []string{"tu-a", "tu-b"}, []string{"tu-good", "tu-good2"}))
	if code != 0 {
		t.Fatalf("valid batch exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	after := reloadRun(t, "run-1")
	if after.Status != shared.StatusActive {
		t.Fatalf("all-accepted two-entry episode must restore ACTIVE, got %s", after.Status)
	}

	// A new episode's unknown cannot cite the used probe receipts.
	r2 := reloadRun(t, "run-1")
	r2.MarkOutcomeUnknown("tu-c")
	if err := r2.Save(); err != nil {
		t.Fatal(err)
	}
	appendTraceEvent(t, "run-1", shared.TraceEvent{
		Type: shared.EventToolOutcomeUnknown, ToolUseID: "tu-c",
		Payload: map[string]interface{}{"tool_use_id": "tu-c", "detected_by": "stop"},
	})
	code, stderr = callGate(t, probeSubmitPayloadAt(t, "tu-ps5", []string{"tu-c"}, []string{"tu-good"}))
	wantExit2(t, code, stderr, "stale")
}

// TestProbeSubmitFailedOutcomeOpensRecoveryCheckpoint: a failed/indeterminate
// episode verdict opens the RECOVERY checkpoint instead of auto-restoring —
// the human decides; nothing auto-replays. The failed verdict comes from the
// matcher's record (observed == preimage), never the wire.
func TestProbeSubmitFailedOutcomeOpensRecoveryCheckpoint(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	decl := recoveryDecl("w1.md")
	parkOutcomeUnknownDecl(t, r, "tu-u1", decl)
	observeBoundProbe(t, "run-1", "tu-p1", "tu-u1", decl, shared.OutcomeFailed)

	code, stderr := callGate(t, probeSubmitPayloadAt(t, "tu-ps1", []string{"tu-u1"}, []string{"tu-p1"}))
	if code != 0 {
		t.Fatalf("failed-outcome probe-submit exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	after := reloadRun(t, "run-1")
	if after.Status != shared.StatusWaitUser {
		t.Fatalf("failed episode must land WAIT_USER at a recovery checkpoint, got %s", after.Status)
	}
	cp := after.CurrentCheckpoint()
	if cp == nil || cp.Kind != shared.CheckpointRecovery {
		t.Fatalf("open checkpoint = %+v, want RECOVERY", cp)
	}
}

// entryOf returns one run's OUTCOME_UNKNOWN entry by id.
func entryOf(t *testing.T, r *shared.RunState, id string) shared.OutcomeUnknown {
	t.Helper()
	for _, e := range r.OutcomeUnknowns {
		if e.ToolUseID == id {
			return e
		}
	}
	t.Fatalf("no OUTCOME_UNKNOWN entry %s", id)
	return shared.OutcomeUnknown{}
}

// --- the Bash ladder ------------------------------------------------------------

// TestL2AdmissionRequiresPairedProbe: an exact-match project check runs only
// with a latest refused in-band probe; otherwise the refusal is an honest
// traced BLOCKED, never silence.
func TestL2AdmissionRequiresPairedProbe(t *testing.T) {
	useGateEnv(t)
	writeLadderManifest(t,
		ladderCheck("unit", "go", []string{"test", "./..."}, "project"),
		ladderCheck("netcheck", "curl", []string{"-s", "https://example.com"}, "project"),
	)
	r := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r, "diagnosis")

	// No probe yet: deny + check.result BLOCKED sandbox_unproven.
	code, stderr := callGate(t, toolPayloadAt(t, "tu-c1", "Bash", bashInput("go test ./...")))
	wantExit2(t, code, stderr, "sandbox_unproven")
	blocked := gateEventsOfType(t, "run-1", shared.EventCheckResult)
	if len(blocked) != 1 || blocked[0].Payload["check_id"] != "unit" || blocked[0].Payload["status"] != "blocked" || blocked[0].Payload["reason"] != "sandbox_unproven" {
		t.Fatalf("blocked check.results = %+v, want one unit/sandbox_unproven", blocked)
	}

	// A writable probe (no sandbox in effect): still blocked.
	observeProbe(t, "run-1", "tu-q1", shared.SandboxProbeWritable)
	code, stderr = callGate(t, toolPayloadAt(t, "tu-c2", "Bash", bashInput("go test ./...")))
	wantExit2(t, code, stderr, "sandbox_unproven")

	// Refused probe: admitted — tool.started carries read_class + the L2
	// binding of check id, digest, cwd, env_allow.
	observeProbe(t, "run-1", "tu-q2", shared.SandboxProbeRefused)
	code, stderr = callGate(t, toolPayloadAt(t, "tu-c3", "Bash", bashInput("go test ./...")))
	if code != 0 {
		t.Fatalf("paired L2 execution exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	evs := readGateTrace(t, "run-1")
	var started map[string]interface{}
	for _, ev := range evs {
		if ev.Type == shared.EventToolStarted && ev.ToolUseID == "tu-c3" {
			started = ev.Payload
		}
	}
	if started == nil || started["read_class"] != true {
		t.Fatalf("admitted check tool.started = %+v, want read_class", started)
	}
	binding, _ := started["l2_check"].(map[string]interface{})
	if binding == nil || binding["check_id"] != "unit" {
		t.Fatalf("l2_check binding = %+v, want unit", binding)
	}
	def := shared.CheckDef{CheckID: "unit", Command: "go", Args: []string{"test", "./..."}, Cwd: ".", Trust: "project"}
	if binding["digest"] != shared.CheckDefDigest(def) {
		t.Fatalf("binding digest = %v, want the current registry digest", binding["digest"])
	}

	// A LATER writable probe supersedes: latest evidence governs.
	observeProbe(t, "run-1", "tu-q3", shared.SandboxProbeWritable)
	code, stderr = callGate(t, toolPayloadAt(t, "tu-c4", "Bash", bashInput("go test ./...")))
	wantExit2(t, code, stderr, "sandbox_unproven")
}

// TestL2PairingOneProbePerAdmission (fix-round M1): after an L2 admission
// consumes a refused probe, the same check line without a FRESH probe is
// denied (probe already consumed) — only a new refused probe re-opens
// admission. A probe at seq 2 can never authorize a check at seq 1000 once
// an admission in between paired it.
func TestL2PairingOneProbePerAdmission(t *testing.T) {
	useGateEnv(t)
	writeLadderManifest(t, ladderCheck("unit", "go", []string{"test", "./..."}, "project"))
	r := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r, "diagnosis")

	observeProbe(t, "run-1", "tu-q1", shared.SandboxProbeRefused)
	if code, stderr := callGate(t, toolPayloadAt(t, "tu-c1", "Bash", bashInput("go test ./..."))); code != 0 {
		t.Fatalf("first admitted execution exit = %d, want 0 (stderr: %q)", code, stderr)
	}

	// The same line again: the probe is CONSUMED by admission tu-c1 — denial
	// names it, and a second BLOCKED check.result is traced.
	code, stderr := callGate(t, toolPayloadAt(t, "tu-c2", "Bash", bashInput("go test ./...")))
	wantExit2(t, code, stderr, "consumed")
	for _, ev := range gateEventsOfType(t, "run-1", shared.EventCheckResult) {
		if ev.ToolUseID == "tu-c2" && (ev.Payload["reason"] != "sandbox_unproven" || ev.Payload["status"] != "blocked") {
			t.Fatalf("consumed denial check.result = %+v, want blocked/sandbox_unproven", ev.Payload)
		}
	}

	// A FRESH refused probe re-opens admission.
	observeProbe(t, "run-1", "tu-q2", shared.SandboxProbeRefused)
	if code, stderr := callGate(t, toolPayloadAt(t, "tu-c3", "Bash", bashInput("go test ./..."))); code != 0 {
		t.Fatalf("re-admitted after fresh probe exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	var binding map[string]interface{}
	for _, ev := range readGateTrace(t, "run-1") {
		if ev.Type == shared.EventToolStarted && ev.ToolUseID == "tu-c3" {
			binding, _ = ev.Payload["l2_check"].(map[string]interface{})
		}
	}
	if binding == nil || binding["check_id"] != "unit" {
		t.Fatalf("re-admission binding = %+v, want unit", binding)
	}
}

// TestL3NetworkCheckRefusedHonestly: a network-class registered check is
// never executed — the refusal is a traced BLOCKED l3_unsupported.
func TestL3NetworkCheckRefusedHonestly(t *testing.T) {
	useGateEnv(t)
	writeLadderManifest(t, ladderCheck("netcheck", "curl", []string{"-s", "https://example.com"}, "project"))
	r := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r, "diagnosis")
	observeProbe(t, "run-1", "tu-q1", shared.SandboxProbeRefused)

	code, stderr := callGate(t, toolPayloadAt(t, "tu-c1", "Bash", bashInput("curl -s https://example.com")))
	wantExit2(t, code, stderr, "l3_unsupported")
	res := gateEventsOfType(t, "run-1", shared.EventCheckResult)
	if len(res) != 1 || res[0].Payload["check_id"] != "netcheck" || res[0].Payload["reason"] != "l3_unsupported" || res[0].Payload["status"] != "blocked" {
		t.Fatalf("L3 check.results = %+v, want one netcheck/l3_unsupported/blocked", res)
	}
}

// TestSandboxProbeChannelAndWriteClassChecks: the anchored probe invocation is
// admitted with its markers; byte-variants of the check line and write-class
// check commands never earn admission.
func TestSandboxProbeChannelAndWriteClassChecks(t *testing.T) {
	useGateEnv(t)
	writeLadderManifest(t,
		ladderCheck("unit", "go", []string{"test", "./..."}, "project"),
		ladderCheck("cleaner", "rm", []string{"-rf", "build"}, "project"),
	)
	r := startFixRun(t, shared.BudgetLimits{})

	// PRE-DIAGNOSIS: the write-class registered check command is denied by the
	// iron law — the write matrix runs before any admission consideration.
	code, stderr := callGate(t, toolPayloadAt(t, "tu-c3", "Bash", bashInput("rm -rf build")))
	wantExit2(t, code, stderr, "diagnosis-before-code")

	acceptPhase(t, r, "diagnosis")

	// The anchored probe invocation: allowed, marker recorded.
	code, stderr = callGate(t, toolPayloadAt(t, "tu-q1", "Bash", bashInput(`"${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-sandbox-probe"`)))
	if code != 0 {
		t.Fatalf("anchored probe exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	evs := readGateTrace(t, "run-1")
	var probeStarted map[string]interface{}
	for _, ev := range evs {
		if ev.Type == shared.EventToolStarted && ev.ToolUseID == "tu-q1" {
			probeStarted = ev.Payload
		}
	}
	if probeStarted == nil || probeStarted["read_class"] != true || probeStarted["sandbox_probe"] != "v1" {
		t.Fatalf("probe tool.started payload = %+v, want read_class + sandbox_probe marker", probeStarted)
	}

	// A near-miss command line (trailing space) is an ordinary read, never
	// admitted: no l2_check binding even with a refused probe on record.
	observeProbe(t, "run-1", "tu-q2", shared.SandboxProbeRefused)
	code, stderr = callGate(t, toolPayloadAt(t, "tu-c1", "Bash", bashInput("go test ./... ")))
	if code != 0 {
		t.Fatalf("ordinary read exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	for _, ev := range readGateTrace(t, "run-1") {
		if ev.Type == shared.EventToolStarted && ev.ToolUseID == "tu-c1" {
			if _, has := ev.Payload["l2_check"]; has {
				t.Fatal("a non-exact command line must never earn an L2 binding")
			}
		}
	}

	// The WRITE-CLASS registered check command after diagnosis runs as an
	// ordinary WRITE — no L2 admission, no binding, no credit.
	code, stderr = callGate(t, toolPayloadAt(t, "tu-c2", "Bash", bashInput("rm -rf build")))
	if code != 0 {
		t.Fatalf("write-class check as ordinary write exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	for _, ev := range readGateTrace(t, "run-1") {
		if ev.Type == shared.EventToolStarted && ev.ToolUseID == "tu-c2" {
			if _, has := ev.Payload["l2_check"]; has {
				t.Fatal("a mutating check command must never earn an L2 binding (checks must be read-class to be creditable)")
			}
			if _, has := ev.Payload["read_class"]; has {
				t.Fatal("a write-class call carries no read_class marker (the crash scan keys on its absence)")
			}
		}
	}
}

// toolPayloadAt builds a gate payload with an explicit tool_use_id.
func toolPayloadAt(t *testing.T, toolUseID, toolName string, toolInput map[string]interface{}) string {
	t.Helper()
	return mustJSONStr(t, map[string]interface{}{
		"session_id":  gateSession,
		"tool_name":   toolName,
		"tool_use_id": toolUseID,
		"tool_input":  toolInput,
	})
}

// --- L1 at phase-submit ----------------------------------------------------------

// writeL1Artifact gives the L1 checks something real to read inside the test
// project dir.
func writeL1Artifact(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(os.Getenv("CLAUDE_PROJECT_DIR"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("CLAUDE_PROJECT_DIR"), name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// submitPhaseWithRefs drives phase-submit carrying check refs.
func submitPhaseWithRefs(t *testing.T, phaseID string, refs []string, artifacts ...shared.ArtifactRef) (int, string) {
	t.Helper()
	args := mustJSONStr(t, func() shared.PhaseSubmission {
		sub := defaultSubmission(phaseID)
		sub.CheckRefs, sub.Artifacts = refs, artifacts
		return sub
	}())
	return callGate(t, toolPayloadAt(t, "tu-sub-"+phaseID+"-"+fmt.Sprint(time.Now().UnixNano()), "Skill", skillInput("summoner:phase-submit", args)))
}

// TestL1RunsInGateAtSubmit: framework refs execute in-gate — pass and fail
// both trace check.results (tier L1); only fail ticks the failed-check budget;
// blocked never ticks.
func TestL1RunsInGateAtSubmit(t *testing.T) {
	useGateEnv(t)
	writeLadderManifest(t,
		ladderCheck("report-has", shared.CheckOpFileContains, []string{"report.md", "needle"}, "framework"),
		ladderCheck("count-tags", shared.CheckOpGrepCount, []string{"report.md", "tag", "3"}, "framework"),
		ladderCheck("missing-file", shared.CheckOpFileContains, []string{"absent.md", "x"}, "framework"),
		ladderCheck("bad-op", "go", []string{"test", "./..."}, "framework"),
	)
	writeL1Artifact(t, "report.md", "needle here\ntag one\ntag two\n")
	startFixRun(t, shared.BudgetLimits{})

	// Unknown framework op is refused at validation time (never exec'd).
	code, stderr := submitPhaseWithRefs(t, "diagnosis", []string{"check://bad-op"})
	wantExit2(t, code, stderr, "shipped L1 adapter")
	// Malformed ref.
	code, stderr = submitPhaseWithRefs(t, "diagnosis", []string{"unit-test"})
	wantExit2(t, code, stderr, "malformed")
	// Unknown id.
	code, stderr = submitPhaseWithRefs(t, "diagnosis", []string{"check://ghost"})
	wantExit2(t, code, stderr, "no check")

	// Pass + fail + blocked in one submit: all three traced, only fail ticks.
	code, stderr = submitPhaseWithRefs(t, "context", []string{"check://report-has", "check://count-tags/x", "check://missing-file"})
	if code != 0 {
		t.Fatalf("L1 submit exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	byID := map[string]shared.TraceEvent{}
	for _, ev := range gateEventsOfType(t, "run-1", shared.EventCheckResult) {
		byID[ev.Payload["check_id"].(string)] = ev
	}
	if len(byID) != 3 {
		t.Fatalf("check.results = %d, want 3 (duplicate ref collapsed)", len(byID))
	}
	if s := byID["report-has"].Payload["status"]; s != shared.CheckStatusPass || byID["report-has"].Payload["tier"] != "L1" {
		t.Fatalf("report-has = %+v, want pass/L1", byID["report-has"].Payload)
	}
	if s := byID["count-tags"].Payload["status"]; s != shared.CheckStatusFail {
		t.Fatalf("count-tags = %+v, want fail", byID["count-tags"].Payload)
	}
	if s := byID["missing-file"].Payload["status"]; s != shared.CheckStatusBlocked {
		t.Fatalf("missing-file = %+v, want blocked", byID["missing-file"].Payload)
	}
	if got := reloadRun(t, "run-1").Counters.FailedChecks; got != 1 {
		t.Fatalf("FailedChecks = %d, want 1 (only the fail ticks)", got)
	}
}

// TestL2RefsAtSubmitNeedObservedPass: a project ref cites an OBSERVED PASSING
// result under the CURRENT definition — missing, blocked, failed and stale
// results give no credit; the manifest inside the artifacts refuses refs.
func TestL2RefsAtSubmitNeedObservedPass(t *testing.T) {
	useGateEnv(t)
	writeLadderManifest(t, ladderCheck("unit", "go", []string{"test", "./..."}, "project"))
	startFixRun(t, shared.BudgetLimits{})
	def := shared.CheckDef{CheckID: "unit", Command: "go", Args: []string{"test", "./..."}, Cwd: ".", Trust: "project"}
	digest := shared.CheckDefDigest(def)

	// No observed result.
	code, stderr := submitPhaseWithRefs(t, "context", []string{"check://unit"})
	wantExit2(t, code, stderr, "no observed check.result")

	// A blocked newest result: no credit.
	appendTraceEvent(t, "run-1", shared.TraceEvent{Type: shared.EventCheckResult, Payload: map[string]interface{}{
		"check_id": "unit", "trust": "project", "status": "blocked", "reason": "sandbox_unproven", "digest": digest,
	}})
	code, stderr = submitPhaseWithRefs(t, "context", []string{"check://unit"})
	wantExit2(t, code, stderr, "newest result")

	// A stale-digest pass: no credit.
	appendTraceEvent(t, "run-1", shared.TraceEvent{Type: shared.EventCheckResult, Payload: map[string]interface{}{
		"check_id": "unit", "trust": "project", "status": "pass", "digest": strings.Repeat("0", 64),
	}})
	code, stderr = submitPhaseWithRefs(t, "context", []string{"check://unit"})
	wantExit2(t, code, stderr, "stale")

	// The current-definition pass: credit.
	appendTraceEvent(t, "run-1", shared.TraceEvent{Type: shared.EventCheckResult, Payload: map[string]interface{}{
		"check_id": "unit", "trust": "project", "status": "pass", "digest": digest,
	}})
	code, stderr = submitPhaseWithRefs(t, "context", []string{"check://unit"})
	if code != 0 {
		t.Fatalf("current passing result must earn the ref, exit = %d (stderr: %q)", code, stderr)
	}

	// The manifest among the artifacts refuses every ref (next phase: the
	// context checkpoint must be decided first — display-bind + CONTINUE).
	after := reloadRun(t, "run-1")
	if cp := after.CurrentCheckpoint(); cp == nil {
		t.Fatal("the accepted submit must leave an open checkpoint")
	}
	decideCurrentContinue(t, after)
	code, stderr = submitPhaseWithRefs(t, "diagnosis", []string{"check://unit"},
		shared.ArtifactRef{Path: "summoner.yaml", Digest: "d-manifest"})
	wantExit2(t, code, stderr, "artifacts include summoner.yaml")
}

// decideCurrentContinue display-binds and CONTINUEs the run's open checkpoint
// (the Stop hook owns display binding in production; tests bind directly).
func decideCurrentContinue(t *testing.T, r *shared.RunState) {
	t.Helper()
	cp := r.CurrentCheckpoint()
	if cp == nil {
		t.Fatal("no open checkpoint to decide")
	}
	if !cp.Displayed {
		if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(*cp, r.RequirementsVersion)); err != nil {
			t.Fatalf("MarkDisplayed(%s): %v", cp.ID, err)
		}
	}
	if err := r.RecordDecision(cp.ID, shared.DecisionContinue, ""); err != nil {
		t.Fatalf("RecordDecision(%s): %v", cp.ID, err)
	}
}
