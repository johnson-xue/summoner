package main

// recovery_decl_test.go — recovery-round I3, gate half (adjudication §4):
// pre-effect mutation declarations on Write/Edit intents (ambiguous or
// non-applying literal edits DENIED before effects; NotebookEdit unsupported
// and denied), the deterministic one-shot probe ticket (the exact declared
// command binds the unknown's probe call), and the mechanical probe-submit
// resolve form — no model-supplied outcome anywhere.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

func shaHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// activeWriteRun starts a fix run with the diagnosis accepted — the state
// where file writes are admissible.
func activeWriteRun(t *testing.T) *shared.RunState {
	t.Helper()
	r := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r, "diagnosis")
	return r
}

func lastStarted(t *testing.T, runID string) map[string]interface{} {
	t.Helper()
	var last map[string]interface{}
	for _, ev := range readGateTrace(t, runID) {
		if ev.Type == shared.EventToolStarted {
			last = ev.Payload
		}
	}
	if last == nil {
		t.Fatal("no tool.started event in the trace")
	}
	return last
}

// TestWriteMutationDeclMinted — R8: an admitted Write freezes its
// declaration on the intent — capability, target, the ACTUAL preimage hash
// (or "absent"), and the exact expected resulting hash.
func TestWriteMutationDeclMinted(t *testing.T) {
	useGateEnv(t)
	activeWriteRun(t)
	// Pre-existing bytes: the preimage is their hash.
	if err := os.WriteFile(filepath.Join(shared.ProjectDir(), "w1.md"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stderr := callGate(t, toolPayloadID(t, "Write", "tu-w1", map[string]interface{}{
		"file_path": "w1.md", "content": "new\n",
	}))
	if code != 0 {
		t.Fatalf("Write exit = %d, want 0 (stderr %q)", code, stderr)
	}
	decl, _ := lastStarted(t, "run-1")["mutation_decl"].(map[string]interface{})
	if decl == nil {
		t.Fatal("admitted Write carries no mutation_decl on tool.started")
	}
	// F5 (adjudication §4 "normalized target"): the decl freezes the RESOLVED
	// absolute target — a relative file_path and its absolute spelling mint
	// byte-identical declarations (and identical probe commands).
	absW1 := filepath.Join(shared.ProjectDir(), "w1.md")
	if decl["capability"] != "file.write" || decl["target"] != absW1 {
		t.Fatalf("decl = %+v, want file.write over the resolved %s", decl, absW1)
	}
	if decl["preimage"] != shaHex("old\n") {
		t.Fatalf("preimage = %v, want the hash of the actual current bytes", decl["preimage"])
	}
	if decl["expect"] != shaHex("new\n") {
		t.Fatalf("expect = %v, want the hash of the exact resulting bytes", decl["expect"])
	}
	if s, _ := decl["input_digest"].(string); s == "" {
		t.Fatal("input_digest is empty")
	}

	// A NEW file's preimage is the "absent" marker.
	code, stderr = callGate(t, toolPayloadID(t, "Write", "tu-w2", map[string]interface{}{
		"file_path": "w2.md", "content": "fresh\n",
	}))
	if code != 0 {
		t.Fatalf("new-file Write exit = %d (stderr %q)", code, stderr)
	}
	decl, _ = lastStarted(t, "run-1")["mutation_decl"].(map[string]interface{})
	if decl["preimage"] != "absent" {
		t.Fatalf("new-file preimage = %v, want absent", decl["preimage"])
	}

	// F5 normalization: the SAME file declared by its ABSOLUTE path mints the
	// byte-identical target — relative and absolute spellings are one decl.
	code, stderr = callGate(t, toolPayloadID(t, "Write", "tu-w3", map[string]interface{}{
		"file_path": absW1, "content": "newer\n",
	}))
	if code != 0 {
		t.Fatalf("absolute-path Write exit = %d (stderr %q)", code, stderr)
	}
	decl2, _ := lastStarted(t, "run-1")["mutation_decl"].(map[string]interface{})
	if decl2["target"] != absW1 {
		t.Fatalf("absolute-path decl target = %v, want %s (normalized)", decl2["target"], absW1)
	}
}

// TestEditDeclValidation — R8: the literal edit validates over the ACTUAL
// preimage before any effect: unique occurrence (or explicit replace_all)
// admits with the exact expect hash; ambiguous/non-applying edits are DENIED
// pre-effect.
func TestEditDeclValidation(t *testing.T) {
	useGateEnv(t)
	activeWriteRun(t)
	if err := os.WriteFile(filepath.Join(shared.ProjectDir(), "e1.md"), []byte("x a y a z"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Ambiguous: two occurrences, no replace_all.
	code, stderr := callGate(t, toolPayloadID(t, "Edit", "tu-e1", map[string]interface{}{
		"file_path": "e1.md", "old_string": "a", "new_string": "B",
	}))
	wantExit2(t, code, stderr, "exactly once")

	// Non-applying: the literal is absent.
	code, stderr = callGate(t, toolPayloadID(t, "Edit", "tu-e2", map[string]interface{}{
		"file_path": "e1.md", "old_string": "nope", "new_string": "B",
	}))
	wantExit2(t, code, stderr, "not present")

	// Ambiguity resolved by explicit replace_all: admitted with the exact
	// expect hash of the fully replaced bytes.
	code, stderr = callGate(t, toolPayloadID(t, "Edit", "tu-e3", map[string]interface{}{
		"file_path": "e1.md", "old_string": "a", "new_string": "B", "replace_all": true,
	}))
	if code != 0 {
		t.Fatalf("replace_all Edit exit = %d (stderr %q)", code, stderr)
	}
	decl, _ := lastStarted(t, "run-1")["mutation_decl"].(map[string]interface{})
	if decl["capability"] != "file.edit" || decl["expect"] != shaHex("x B y B z") {
		t.Fatalf("decl = %+v, want file.edit with the replace_all expect hash", decl)
	}
}

// TestNotebookEditDeniedPreEffect — §4 line 45: Notebook edits have no
// shipped declaration — denied BEFORE effects.
func TestNotebookEditDeniedPreEffect(t *testing.T) {
	useGateEnv(t)
	activeWriteRun(t)
	code, stderr := callGate(t, toolPayload(t, "NotebookEdit", notebookInput()))
	wantExit2(t, code, stderr, "no supported")
}

// declaredUnknown parks the run OUTCOME_UNKNOWN with one declared entry on
// disk AND its tool.outcome_unknown marking in the trace — the entry and the
// trace agree, as production marking guarantees.
func declaredUnknown(t *testing.T, r *shared.RunState, id string, decl *shared.MutationDecl) {
	t.Helper()
	r.MarkOutcomeUnknownWithDecl(id, decl)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	appendTraceEvent(t, r.RunID, shared.TraceEvent{
		Type:      shared.EventToolOutcomeUnknown,
		Source:    "stop",
		ToolUseID: id,
		Payload:   map[string]interface{}{"tool_use_id": id, "detected_by": "stop"},
	})
}

// TestProbeTicketBinding — R6: only the EXACT declared command binds the
// unknown's probe ticket (one-shot); an unrelated read-class call binds
// nothing; a second exact call never rebinds.
func TestProbeTicketBinding(t *testing.T) {
	useGateEnv(t)
	r := activeWriteRun(t)
	decl := &shared.MutationDecl{Capability: "file.write", Target: "w1.md", Preimage: "p0", Expect: "e1"}
	declaredUnknown(t, r, "tu-crash-1", decl)
	cmd := "shasum -a 256 'w1.md'"

	code, stderr := callGate(t, toolPayloadID(t, "Bash", "tu-p1", bashInput(cmd)))
	if code != 0 {
		t.Fatalf("exact probe exit = %d (stderr %q)", code, stderr)
	}
	started := lastStarted(t, "run-1")
	if started["command"] != cmd || started["probe_of"] != "tu-crash-1" {
		t.Fatalf("started = %+v, want the exact command bound to tu-crash-1", started)
	}

	// Unrelated read-class command: observed, never bound.
	code, stderr = callGate(t, toolPayloadID(t, "Bash", "tu-p2", bashInput("ls -la")))
	if code != 0 {
		t.Fatalf("unrelated read exit = %d (stderr %q)", code, stderr)
	}
	started = lastStarted(t, "run-1")
	if _, bound := started["probe_of"]; bound {
		t.Fatalf("unrelated command must never bind a ticket: %+v", started)
	}

	// One-shot: the second exact call does not rebind.
	code, stderr = callGate(t, toolPayloadID(t, "Bash", "tu-p3", bashInput(cmd)))
	if code != 0 {
		t.Fatalf("second exact call exit = %d (stderr %q)", code, stderr)
	}
	started = lastStarted(t, "run-1")
	if _, bound := started["probe_of"]; bound {
		t.Fatalf("a bound ticket is one-shot: %+v", started)
	}
}

// observeMatchedProbe simulates the posttooluse record of a probe whose
// observation the matcher already judged (or lost).
func observeMatchedProbe(t *testing.T, runID, probeID, unknownID, verdict, observed string) {
	t.Helper()
	decl := &shared.MutationDecl{Capability: "file.write", Target: "w1.md"}
	cmd := decl.ExactProbeCommand()
	appendTraceEvent(t, runID, shared.TraceEvent{
		Type:      shared.EventToolStarted,
		ToolUseID: probeID,
		Payload: map[string]interface{}{
			"tool_name": "Bash", "read_class": true, "command": cmd, "probe_of": unknownID,
		},
	})
	m := map[string]interface{}{"unknown_id": unknownID, "verdict": verdict}
	if observed != "" {
		m["observed"] = observed
	}
	appendTraceEvent(t, runID, shared.TraceEvent{
		Type:      shared.EventToolResult,
		ToolUseID: probeID,
		Payload:   map[string]interface{}{"tool_name": "Bash", "response_sha256": "rh", "probe_match": m},
	})
}

// TestProbeSubmitMechanicalForm — R6/R7: the model-supplied outcome is GONE
// from the wire; an unbound probe never resolves; the recorded matcher
// verdict is the outcome; a lost observation resolves indeterminate.
func TestProbeSubmitMechanicalForm(t *testing.T) {
	useGateEnv(t)
	r := activeWriteRun(t)
	decl := &shared.MutationDecl{Capability: "file.write", Target: "w1.md", Preimage: shaHex("old\n"), Expect: shaHex("new\n")}
	declaredUnknown(t, r, "tu-crash-1", decl)

	// A properly observed probe for tu-p1 exists in the trace — the form
	// validation is the ONLY thing that can reject the old wire form.
	observeMatchedProbe(t, "run-1", "tu-p1", "tu-crash-1", shared.OutcomeAccepted, decl.Expect)
	// And a properly observed read-class call that never consumed the ticket.
	appendTraceEvent(t, "run-1", shared.TraceEvent{
		Type:      shared.EventToolStarted,
		ToolUseID: "tu-loose",
		Payload:   map[string]interface{}{"tool_name": "Bash", "read_class": true, "command": "ls -la"},
	})
	appendTraceEvent(t, "run-1", shared.TraceEvent{
		Type:      shared.EventToolResult,
		ToolUseID: "tu-loose",
		Payload:   map[string]interface{}{"tool_name": "Bash", "response_sha256": "rh"},
	})

	// OLD form (outcome on the wire): rejected — the matcher owns the verdict.
	code, stderr := callGate(t, toolPayload(t, "Skill", skillInput("summoner:probe-submit",
		`{"unknown_tool_use_ids":["tu-crash-1"],"outcome":"accepted","probe_tool_use_ids":["tu-p1"]}`)))
	wantExit2(t, code, stderr, "matcher")

	// NEW form, unbound probe: rejected (no evidence at all).
	code, stderr = callGate(t, toolPayload(t, "Skill", skillInput("summoner:probe-submit",
		`{"op":"resolve","unknown_tool_use_ids":["tu-crash-1"],"probe_tool_use_ids":["tu-unbound"]}`)))
	wantExit2(t, code, stderr, "not valid evidence")

	// NEW form, OBSERVED-but-unbound probe (a real read-class call with a
	// result that never consumed the ticket): rejected — R6's false-allow.
	code, stderr = callGate(t, toolPayload(t, "Skill", skillInput("summoner:probe-submit",
		`{"op":"resolve","unknown_tool_use_ids":["tu-crash-1"],"probe_tool_use_ids":["tu-loose"]}`)))
	wantExit2(t, code, stderr, "does not bind")

	// NEW form over the recorded matcher verdicts, one fresh run each.
	for name, tc := range map[string]struct {
		verdict string
	}{
		"accepted (observed == expect)":      {shared.OutcomeAccepted},
		"failed (observed == preimage)":      {shared.OutcomeFailed},
		"indeterminate (observed something)": {shared.OutcomeIndeterminate},
		"lost (no recorded match)":           {shared.OutcomeIndeterminate},
	} {
		t.Run(name, func(t *testing.T) {
			useGateEnv(t)
			r := activeWriteRun(t)
			declaredUnknown(t, r, "tu-crash-1", decl)
			if tc.verdict == shared.OutcomeIndeterminate && name == "lost (no recorded match)" {
				observeMatchedProbe(t, "run-1", "tu-p1", "tu-crash-1", "", "")
				// strip the verdict: a result WITHOUT probe_match at all
			} else if name == "lost (no recorded match)" {
				observeMatchedProbe(t, "run-1", "tu-p1", "tu-crash-1", "", "")
			} else {
				observed := decl.Expect
				if tc.verdict == shared.OutcomeFailed {
					observed = decl.Preimage
				}
				if tc.verdict == shared.OutcomeIndeterminate {
					observed = shaHex("something else\n")
				}
				observeMatchedProbe(t, "run-1", "tu-p1", "tu-crash-1", tc.verdict, observed)
			}
			code, stderr := callGate(t, toolPayload(t, "Skill", skillInput("summoner:probe-submit",
				fmt.Sprintf(`{"op":"resolve","unknown_tool_use_ids":["tu-crash-1"],"probe_tool_use_ids":["tu-p1"]}`))))
			if code != 0 {
				t.Fatalf("resolve exit = %d (stderr %q)", code, stderr)
			}
			disk := reloadRun(t, "run-1")
			e := disk.OutcomeUnknowns[0]
			if !e.Resolved || e.Outcome != tc.verdict || e.ProbeToolUseID != "tu-p1" {
				t.Fatalf("entry = %+v, want resolved %s via tu-p1", e, tc.verdict)
			}
		})
	}
}
