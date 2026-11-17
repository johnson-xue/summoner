package shared

import (
	"strings"
	"testing"
)

// Recovery-round I3 (adjudication §4 lines 44–47): pre-effect capability
// declarations ride the mutating intent, and the OUTCOME_UNKNOWN resolution
// consumes the OBSERVED matcher verdict — never a model-supplied outcome. An
// UNSUPPORTED capability (no shipped declaration) resolves INDETERMINATE-ONLY:
// it never auto-restores the pre-episode status.

// TestMutationDeclOnUnknownAndMechanicalResolve — R7/R8 shared half: the
// declaration lands on the entry, the mechanical resolve records the matcher
// verdict, and an all-accepted episode restores the exact prior status.
func TestMutationDeclOnUnknownAndMechanicalResolve(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	decl := &MutationDecl{
		Capability:  "file.write",
		Target:      "a.md",
		InputDigest: "ix-1",
		Preimage:    "pre-h",
		Expect:      "exp-h",
	}
	r.MarkOutcomeUnknownWithDecl("tu-crash-1", decl)
	if err := r.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if r.Status != StatusOutcomeUnknown {
		t.Fatalf("status = %s, want OUTCOME_UNKNOWN", r.Status)
	}
	e := r.OutcomeUnknowns[0]
	if e.MutationDecl == nil || e.MutationDecl.Capability != "file.write" || e.MutationDecl.Expect != "exp-h" {
		t.Fatalf("entry decl = %+v, want the frozen declaration", e.MutationDecl)
	}

	newly, err := r.ResolveOutcomeMechanical("tu-crash-1", OutcomeAccepted, "tu-probe-1")
	if err != nil || !newly {
		t.Fatalf("ResolveOutcomeMechanical = (%v, %v), want (true, nil)", newly, err)
	}
	e = r.OutcomeUnknowns[0]
	if !e.Resolved || e.Outcome != OutcomeAccepted || e.ProbeToolUseID != "tu-probe-1" {
		t.Fatalf("entry = %+v, want resolved accepted via tu-probe-1", e)
	}
	// Single-entry episode, all-accepted: the exact pre-episode status
	// restores (ACTIVE here), never a replayed mutation.
	if r.Status != StatusActive {
		t.Fatalf("status = %s, want ACTIVE (all-accepted restores the prior)", r.Status)
	}
}

// TestUnsupportedNeverAutoRestores — R7: entries with an UNSUPPORTED (or
// absent) declaration coerce every claimed verdict to indeterminate — the
// recovery checkpoint opens, the run never auto-restores on them.
func TestUnsupportedNeverAutoRestores(t *testing.T) {
	for name, decl := range map[string]*MutationDecl{
		"unsupported capability": {Capability: CapabilityUnsupported, Target: "x"},
		"no declaration at all":  nil,
	} {
		t.Run(name, func(t *testing.T) {
			r := startTestRun(t, BudgetLimits{})
			r.MarkOutcomeUnknownWithDecl("tu-crash-1", decl)
			if err := r.Save(); err != nil {
				t.Fatal(err)
			}
			newly, err := r.ResolveOutcomeMechanical("tu-crash-1", OutcomeAccepted, "tu-probe-1")
			if err != nil || !newly {
				t.Fatalf("ResolveOutcomeMechanical = (%v, %v), want (true, nil)", newly, err)
			}
			if e := r.OutcomeUnknowns[0]; e.Outcome != OutcomeIndeterminate {
				t.Fatalf("coerced outcome = %s, want indeterminate (unsupported never auto-restores)", e.Outcome)
			}
			if r.Status != StatusWaitUser {
				t.Fatalf("status = %s, want WAIT_USER at the recovery checkpoint", r.Status)
			}
		})
	}
}

// TestResolveOutcomeMechanicalReplay — the mechanical entry keeps the
// resolve replay discipline: the identical (verdict, probe) pair replays
// silently; anything conflicting is an error; the verdict enum is enforced.
func TestResolveOutcomeMechanicalReplay(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	decl := &MutationDecl{Capability: "file.write", Target: "a.md", Preimage: "pre-h", Expect: "exp-h"}
	r.MarkOutcomeUnknownWithDecl("tu-crash-1", decl)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveOutcomeMechanical("tu-crash-1", "bogus", "tu-probe-1"); err == nil ||
		!strings.Contains(err.Error(), "accepted|failed|indeterminate") {
		t.Fatalf("bad verdict: want enum error, got %v", err)
	}
	if _, err := r.ResolveOutcomeMechanical("tu-ghost", OutcomeAccepted, "p"); err == nil {
		t.Fatal("unknown tool_use_id: want error, got nil")
	}
	if _, err := r.ResolveOutcomeMechanical("tu-crash-1", OutcomeAccepted, ""); err == nil {
		t.Fatal("missing probe receipt: want error, got nil")
	}
	if _, err := r.ResolveOutcomeMechanical("tu-crash-1", OutcomeAccepted, "tu-probe-1"); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	// Identical pair: silent replay even after the episode closed.
	if newly, err := r.ResolveOutcomeMechanical("tu-crash-1", OutcomeAccepted, "tu-probe-1"); err != nil || newly {
		t.Fatalf("replay = (%v, %v), want (false, nil)", newly, err)
	}
	// Conflicting pair: rejected.
	if _, err := r.ResolveOutcomeMechanical("tu-crash-1", OutcomeFailed, "tu-probe-2"); err == nil {
		t.Fatal("conflicting replay: want error, got nil")
	}
}

// TestExactProbeCommand — the deterministic probe command a declaration
// prescribes (status serves these EXACT bytes; the gate binds the ticket by
// byte equality); unsupported capabilities have none.
func TestExactProbeCommand(t *testing.T) {
	fileDecl := &MutationDecl{Capability: "file.write", Target: "docs/a b.md"}
	if got := fileDecl.ExactProbeCommand(); got != "shasum -a 256 'docs/a b.md'" {
		t.Fatalf("ExactProbeCommand = %q, want the deterministic shasum command", got)
	}
	if got := (&MutationDecl{Capability: CapabilityUnsupported}).ExactProbeCommand(); got != "" {
		t.Fatalf("unsupported ExactProbeCommand = %q, want empty", got)
	}
}
