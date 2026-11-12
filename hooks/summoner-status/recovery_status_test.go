package main

// recovery_status_test.go — recovery-round I3, status half: while a run sits
// OUTCOME_UNKNOWN, the status render names every unresolved unknown with its
// target and the EXACT probe command the gate's ticket binds to (status is
// the server of those bytes; it never mints authority of its own).
// Plus the I4 tail: the default-recall line mirrors the checkpoint's REAL
// offered RecallTargets, not a status-side recomputation.

import (
	"strings"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// TestStatusRecallLineMirrorsOfferedTargets (I4 tail): with a decidable
// checkpoint carrying RecallTargets, the default-recall line resolves
// "latest" to its visit and lists exactly the offered labels — the set
// "Recall: <target>" accepts at this checkpoint (planRecallTarget's offers).
func TestStatusRecallLineMirrorsOfferedTargets(t *testing.T) {
	useStatusEnv(t)
	useSandboxSettings(t, false)
	stubProbe(t, false)
	_, cp := waitUserDiagnosis(t) // context accepted → targets: latest, context@v1
	if len(cp.RecallTargets) == 0 {
		t.Fatal("fixture premise: checkpoint carries no RecallTargets")
	}

	code, out, stderr := callStatus(t, `{"session_id":"sess-1"}`)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	want := "default recall: latest → context@v1 (offered: latest, context@v1)"
	if !strings.Contains(out, want) {
		t.Errorf("render missing %q\n--- render ---\n%s", want, out)
	}
}

func TestRenderOutcomeUnknownProbeCommands(t *testing.T) {
	useStatusEnv(t)
	useSandboxSettings(t, false)
	stubProbe(t, false)
	r := newRun(t)
	r.MarkOutcomeUnknownWithDecl("tu-crash-1", &shared.MutationDecl{
		Capability: shared.CapabilityFileWrite, Target: "docs/w1.md",
		Preimage: "p0", Expect: "e1",
	})
	r.MarkOutcomeUnknownWithDecl("tu-crash-2", nil)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}

	code, out, stderr := callStatus(t, `{"session_id":"sess-1"}`)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	for _, want := range []string{
		"workflow fix · OUTCOME_UNKNOWN",
		"awaiting: reconciliation probes (2 unresolved)",
		"outcome unknowns (2 unresolved):",
		"tu-crash-1: file.write docs/w1.md — exact probe: shasum -a 256 'docs/w1.md'",
		"tu-crash-2: unsupported — no exact probe (resolves indeterminate-only)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\n--- render ---\n%s", want, out)
		}
	}
}
