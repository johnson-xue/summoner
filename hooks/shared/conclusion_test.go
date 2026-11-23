package shared

import (
	"reflect"
	"testing"
)

// conclusion_test.go — the structured conclusion of a phase submission
// (0.7.0 D1). The conclusion is model-authored CONTENT the hooks validate,
// store on the checkpoint and bind into the display digest — never authority.

// TestSubmitPhaseStoresConclusionAndOutcome: the submission's conclusion and
// outcome land on the pending checkpoint verbatim and survive a reload.
func TestSubmitPhaseStoresConclusionAndOutcome(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	if err := r.OpenPhase("diagnosis"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	sub := PhaseSubmission{
		PhaseID: "diagnosis",
		Summary: "nil deref in task loader",
		Outcome: ConclusionConfirmed,
		Conclusion: &Conclusion{
			Summary:  "TaskModule.handle() dereferences player.SubTask without a nil check",
			Evidence: []string{"task/module.go:88", "task/module_test.go:41 reproduces"},
			Approach: "read the call chain from the error site, then ran the existing test",
			Impact:   "every task list load for a player with a completed sub-task",
		},
	}
	cp, err := r.SubmitPhaseUnstaged(sub)
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	stored := checkpointByID(t, r, cp.ID)
	if stored.Outcome != ConclusionConfirmed {
		t.Fatalf("stored outcome = %q, want %q", stored.Outcome, ConclusionConfirmed)
	}
	if !reflect.DeepEqual(stored.Conclusion, sub.Conclusion) {
		t.Fatalf("stored conclusion = %+v, want %+v", stored.Conclusion, sub.Conclusion)
	}
	disk := reloadRunState(t, "run-1")
	if got := checkpointByID(t, disk, cp.ID); !reflect.DeepEqual(got.Conclusion, sub.Conclusion) || got.Outcome != ConclusionConfirmed {
		t.Fatalf("conclusion did not survive the reload: %+v / %q", got.Conclusion, got.Outcome)
	}
}

// TestConclusionNormalizationAtTheReducer: the reducer never stores an
// ambiguous conclusion. An omitted outcome is `unclear`; open questions force
// `unclear` even when the submission claims `confirmed`; an outcome value
// outside the enum can never claim confirmed — it degrades to `unclear`.
func TestConclusionNormalizationAtTheReducer(t *testing.T) {
	cases := []struct {
		name    string
		outcome string
		concl   *Conclusion
		want    string
	}{
		{"omitted outcome", "", nil, ConclusionUnclear},
		{"plain confirmed", ConclusionConfirmed, &Conclusion{Summary: "s", Evidence: []string{"e"}}, ConclusionConfirmed},
		{"plain failed", ConclusionFailed, &Conclusion{Summary: "s"}, ConclusionFailed},
		{"open questions force unclear", ConclusionConfirmed, &Conclusion{Summary: "s", Evidence: []string{"e"}, OpenQuestions: []string{"why is the hero banned"}}, ConclusionUnclear},
		{"foreign outcome degrades to unclear", "definitely-fixed", &Conclusion{Summary: "s", Evidence: []string{"e"}}, ConclusionUnclear},
	}
	for _, c := range cases {
		r := startTestRun(t, BudgetLimits{})
		if err := r.OpenPhase("diagnosis"); err != nil {
			t.Fatalf("%s: OpenPhase: %v", c.name, err)
		}
		cp, err := r.SubmitPhaseUnstaged(PhaseSubmission{PhaseID: "diagnosis", Outcome: c.outcome, Conclusion: c.concl})
		if err != nil {
			t.Fatalf("%s: SubmitPhase: %v", c.name, err)
		}
		if got := checkpointByID(t, r, cp.ID).Outcome; got != c.want {
			t.Errorf("%s: stored outcome = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestCheckpointDigestBindsTheConclusion: the conclusion is DISPLAY-BOUND
// content — any change to it (or to the outcome) after display invalidates
// the binding, exactly like a changed note or criterion.
func TestCheckpointDigestBindsTheConclusion(t *testing.T) {
	base := CheckpointState{
		ID: "cp-2", Kind: CheckpointPhase, PhaseID: "diagnosis", Nonce: "1a2b3c4d",
		Outcome:    ConclusionConfirmed,
		Conclusion: &Conclusion{Summary: "root cause X"},
	}
	digests := map[string]string{"base": CheckpointContentDigest(base, 1)}
	variants := map[string]CheckpointState{
		"summary":       {Outcome: ConclusionConfirmed, Conclusion: &Conclusion{Summary: "root cause Y"}},
		"evidence":      {Outcome: ConclusionConfirmed, Conclusion: &Conclusion{Summary: "root cause X", Evidence: []string{"a.go:1"}}},
		"approach":      {Outcome: ConclusionConfirmed, Conclusion: &Conclusion{Summary: "root cause X", Approach: "read the chain"}},
		"open question": {Outcome: ConclusionConfirmed, Conclusion: &Conclusion{Summary: "root cause X", OpenQuestions: []string{"why"}}},
		"impact":        {Outcome: ConclusionConfirmed, Conclusion: &Conclusion{Summary: "root cause X", Impact: "login"}},
		"outcome":       {Outcome: ConclusionFailed, Conclusion: &Conclusion{Summary: "root cause X"}},
	}
	seen := map[string]string{digests["base"]: "base"}
	for name, v := range variants {
		v.ID, v.Kind, v.PhaseID, v.Nonce = base.ID, base.Kind, base.PhaseID, base.Nonce
		d := CheckpointContentDigest(v, 1)
		if other, clash := seen[d]; clash {
			t.Errorf("variant %q collides with %q (digest %s)", name, other, d)
		}
		seen[d] = name
	}
}

// TestCheckpointDigestUnchangedForLegacyCheckpoints: a checkpoint written
// before 0.7.0 carries neither outcome nor conclusion, and its digest must
// stay byte-identical — an in-flight run resumed across the upgrade keeps
// its display binding. The literal was computed by the pre-0.7.0 reducer.
func TestCheckpointDigestUnchangedForLegacyCheckpoints(t *testing.T) {
	legacy := CheckpointState{
		ID: "cp-4", Kind: CheckpointPhase, PhaseID: "diagnosis", Note: "legacy note",
		Nonce: "deadbeef", CriteriaIDs: []string{"crit-b", "crit-a"},
		ArtifactVersions: map[string]string{"b.md": "2", "a.md": "1"},
	}
	const want = "24e1085649f765d85760b66cc7fa5616822867e3abe0741778f6ab16de1eaf6f"
	if got := CheckpointContentDigest(legacy, 3); got != want {
		t.Fatalf("legacy checkpoint digest = %s, want the pre-0.7.0 value %s", got, want)
	}

	// The same checkpoint carrying a conclusion is DIFFERENT content.
	withConclusion := legacy
	withConclusion.Outcome = ConclusionConfirmed
	withConclusion.Conclusion = &Conclusion{Summary: "legacy note"}
	if got := CheckpointContentDigest(withConclusion, 3); got == want {
		t.Fatal("a conclusion must change the digest of the checkpoint carrying it")
	}
}

// TestConclusionFreeCheckpointHashesAsBefore070 — ship review round 1
// (compat lens): every 0.7.0 checkpoint stores `outcome: unclear` when the
// submission claimed nothing. Folding that default into the digest made a
// checkpoint displayed under 0.7.0 undecidable under 0.6.1 (whose Stop hook
// never re-binds). The digest folds a conclusion only when there is content
// to protect.
func TestConclusionFreeCheckpointHashesAsBefore070(t *testing.T) {
	const want = "24e1085649f765d85760b66cc7fa5616822867e3abe0741778f6ab16de1eaf6f" // pre-0.7.0 reducer
	legacy := CheckpointState{
		ID: "cp-4", Kind: CheckpointPhase, PhaseID: "diagnosis", Note: "legacy note",
		Nonce: "deadbeef", CriteriaIDs: []string{"crit-b", "crit-a"},
		ArtifactVersions: map[string]string{"b.md": "2", "a.md": "1"},
	}
	// The 0.7.0 stored form of a conclusion-free submission: outcome unclear,
	// no conclusion — including a normalized EMPTY conclusion.
	for name, cp := range map[string]CheckpointState{
		"no outcome, no conclusion": legacy,
		"outcome unclear":           func() CheckpointState { c := legacy; c.Outcome = ConclusionUnclear; return c }(),
	} {
		if got := CheckpointContentDigest(cp, 3); got != want {
			t.Errorf("%s: digest = %s, want the pre-0.7.0 value (a rollback must not wedge the checkpoint)", name, got)
		}
	}
	// Real content still changes it.
	real := legacy
	real.Outcome, real.Conclusion = ConclusionConfirmed, &Conclusion{Summary: "s"}
	if CheckpointContentDigest(real, 3) == want {
		t.Error("a real conclusion must change the digest")
	}
}
