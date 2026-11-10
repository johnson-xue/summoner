package shared

// envelope_test.go — Task 7 Step 1 contract tests for the ⑤ review-envelope
// validation layer (spec §3): strict decode, frozen-set equality (both
// directions), check-registry digests, evidence reconciliation, the
// rubber-stamp rule, BLOCKED/verdict separation and same-digest reuse.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- helpers ----------------------------------------------------------------

// frozenTwoPhase is a frozen acceptance view with two requirements (three
// criteria total) — the baseline every set-equality case runs against.
func frozenTwoPhase() FrozenView {
	return FrozenView{Version: 2, Requirements: []FrozenRequirement{
		{ID: "p1", CriteriaIDs: []string{"c-1", "c-2"}, Artifacts: []string{"a.md"}},
		{ID: "p2", CriteriaIDs: []string{"c-3"}, Artifacts: []string{"b.md"}},
	}}
}

// envelopeWithCriteria builds an envelope whose exit criteria are exactly ids
// (all judgment, so no registry is needed for the set-equality cases).
func envelopeWithCriteria(ids ...string) Envelope {
	env := testEnvelope()
	crit := make([]Criterion, 0, len(ids))
	for _, id := range ids {
		crit = append(crit, Criterion{ID: id, Goal: "goal " + id, Method: MethodJudgment, Rubric: "rubric-1"})
	}
	env.ExitCriteria = crit
	env.VerificationScope = VerificationScope{Readable: []string{"."}}
	return env
}

// pendingReviewRun drives a run to REVIEW_PENDING with review rev-1 in flight.
func pendingReviewRun(t *testing.T) *RunState {
	t.Helper()
	r := startTestRun(t, BudgetLimits{})
	openAndSubmit(t, r, "p1", "c1")
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue)
	if _, err := r.RequestReview(testEnvelope()); err != nil {
		t.Fatalf("RequestReview: %v", err)
	}
	if err := r.MarkReviewRunning("rev-1"); err != nil {
		t.Fatalf("MarkReviewRunning: %v", err)
	}
	return r
}

// concludedReviewRun drives a run through one concluded review result.
func concludedReviewRun(t *testing.T, res ReviewResult) *RunState {
	t.Helper()
	r := pendingReviewRun(t)
	if err := r.RecordReviewResult(res); err != nil {
		t.Fatalf("RecordReviewResult: %v", err)
	}
	return r
}

// --- DecodeEnvelope ----------------------------------------------------------

// TestUnknownFieldRejected: the envelope allow-list is strict — an unknown
// field anywhere (top level or nested inside exit_criteria) is a parse error,
// never a silent drop; required platform correlation keys are non-empty and
// artifact_set is non-empty.
func TestUnknownFieldRejected(t *testing.T) {
	valid, err := json.Marshal(testEnvelope())
	if err != nil {
		t.Fatalf("marshal base envelope: %v", err)
	}
	if _, err := DecodeEnvelope(valid); err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}

	// Unknown TOP-LEVEL field (the spec-banned factual_claim).
	var m map[string]interface{}
	if err := json.Unmarshal(valid, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	m["factual_claim"] = "everything works"
	b, _ := json.Marshal(m)
	if _, err := DecodeEnvelope(b); err == nil {
		t.Fatal("unknown top-level field must be a parse error")
	}

	// Unknown NESTED field (inside an exit criterion) — DisallowUnknownFields
	// applies through the whole document.
	var crit map[string]interface{}
	rawCrit, _ := json.Marshal(testEnvelope().ExitCriteria[0])
	if err := json.Unmarshal(rawCrit, &crit); err != nil {
		t.Fatalf("unmarshal criterion: %v", err)
	}
	crit["producer_verdict_self_report"] = "PASS"
	// Rebuild the envelope JSON with the poisoned criterion embedded.
	var top map[string]interface{}
	if err := json.Unmarshal(valid, &top); err != nil {
		t.Fatalf("unmarshal top: %v", err)
	}
	top["exit_criteria"] = []interface{}{crit}
	poisoned, _ := json.Marshal(top)
	if _, err := DecodeEnvelope(poisoned); err == nil {
		t.Fatal("unknown nested field must be a parse error")
	}

	// Required keys non-empty; artifact_set non-empty.
	for name, mutate := range map[string]func(*Envelope){
		"schema_version empty": func(e *Envelope) { e.SchemaVersion = "" },
		"run_id empty":         func(e *Envelope) { e.RunID = "" },
		"review_id empty":      func(e *Envelope) { e.ReviewID = "" },
		"artifact_set empty":   func(e *Envelope) { e.ArtifactSet = nil },
	} {
		bad := testEnvelope()
		mutate(&bad)
		raw, _ := json.Marshal(bad)
		if _, err := DecodeEnvelope(raw); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}

// --- ValidateEnvelope: set equality, both directions -------------------------

// TestMissingCriterionRejected: an envelope whose exit criteria omit one id of
// the frozen acceptance view is rejected, naming the missing id.
func TestMissingCriterionRejected(t *testing.T) {
	env := envelopeWithCriteria("c-1", "c-3") // c-2 missing
	err := ValidateEnvelope(env, frozenTwoPhase(), nil, nil)
	if err == nil {
		t.Fatal("missing frozen criterion must be rejected")
	}
	if !strings.Contains(err.Error(), "c-2") {
		t.Fatalf("error must name the missing criterion c-2, got: %v", err)
	}
}

// TestProducerCannotTrim: a producer trimming a whole requirement's criteria
// out of the envelope is rejected — the frozen view is not negotiable.
func TestProducerCannotTrim(t *testing.T) {
	env := envelopeWithCriteria("c-1", "c-2") // whole p2 requirement trimmed
	err := ValidateEnvelope(env, frozenTwoPhase(), nil, nil)
	if err == nil {
		t.Fatal("trimmed envelope must be rejected")
	}
	if !strings.Contains(err.Error(), "c-3") {
		t.Fatalf("error must name the trimmed criterion c-3, got: %v", err)
	}

	// The complete set is accepted.
	if err := ValidateEnvelope(envelopeWithCriteria("c-1", "c-2", "c-3"), frozenTwoPhase(), nil, nil); err != nil {
		t.Fatalf("complete envelope rejected: %v", err)
	}
}

// TestInventedCriterionRejected: an envelope criterion outside the frozen view
// (minus recorded drops) is an invented criterion — rejected.
func TestInventedCriterionRejected(t *testing.T) {
	env := envelopeWithCriteria("c-1", "c-2", "c-3", "c-99")
	err := ValidateEnvelope(env, frozenTwoPhase(), nil, nil)
	if err == nil {
		t.Fatal("invented criterion must be rejected")
	}
	if !strings.Contains(err.Error(), "c-99") {
		t.Fatalf("error must name the invented criterion c-99, got: %v", err)
	}
}

// TestDroppedCriterionExcluded: recorded user-explicit drops leave the frozen
// set — an envelope omitting exactly the dropped id is valid.
func TestDroppedCriterionExcluded(t *testing.T) {
	drops := []CriteriaDrop{{ID: "c-2", At: "2026-11-10T00:00:00Z", CheckpointID: "cp-3"}}
	env := envelopeWithCriteria("c-1", "c-3")
	if err := ValidateEnvelope(env, frozenTwoPhase(), drops, nil); err != nil {
		t.Fatalf("dropped criterion must leave the expected set: %v", err)
	}
	// The dropped id reappearing in the envelope is rejected (it is no longer
	// expected).
	envBack := envelopeWithCriteria("c-1", "c-2", "c-3")
	if err := ValidateEnvelope(envBack, frozenTwoPhase(), drops, nil); err == nil {
		t.Fatal("dropped criterion re-added to the envelope must be rejected")
	}
	// A drop whose criterion left the frozen view (re-acceptance with new
	// criteria) is inert — the ledger is a historical record.
	staleDrop := []CriteriaDrop{{ID: "c-gone"}}
	if err := ValidateEnvelope(envelopeWithCriteria("c-1", "c-2", "c-3"), frozenTwoPhase(), staleDrop, nil); err != nil {
		t.Fatalf("drop outside the frozen view must be inert: %v", err)
	}
}

// TestValidateEnvelopeCriterionShape: duplicate ids and malformed methods are
// rejected — set equality needs unique, well-formed criterion ids.
func TestValidateEnvelopeCriterionShape(t *testing.T) {
	dup := envelopeWithCriteria("c-1", "c-1", "c-2", "c-3")
	if err := ValidateEnvelope(dup, frozenTwoPhase(), nil, nil); err == nil {
		t.Fatal("duplicate criterion id must be rejected")
	}

	env := envelopeWithCriteria("c-1", "c-2", "c-3")
	env.ExitCriteria[0].Method = CriterionMethod("vibes")
	if err := ValidateEnvelope(env, frozenTwoPhase(), nil, nil); err == nil {
		t.Fatal("unknown criterion method must be rejected")
	}

	env = envelopeWithCriteria("c-1", "c-2", "c-3")
	env.ExitCriteria[0].Method = MethodMechanical
	if err := ValidateEnvelope(env, frozenTwoPhase(), nil, nil); err == nil {
		t.Fatal("mechanical criterion without check_id must be rejected")
	}
}

// TestResolveFrozenViewLastEntryWins: the frozen view resolves per requirement
// ID with LAST-ENTRY-WINS (the serialization-side twin of the M11
// last-visit-per-ID phase semantics).
func TestResolveFrozenViewLastEntryWins(t *testing.T) {
	view := FrozenView{Version: 3, Requirements: []FrozenRequirement{
		{ID: "p1", CriteriaIDs: []string{"c-old"}},
		{ID: "p2", CriteriaIDs: []string{"c-3"}},
		{ID: "p1", CriteriaIDs: []string{"c-1", "c-2"}, Artifacts: []string{"a.md"}},
	}}
	got := ResolveFrozenView(view)
	if len(got.Requirements) != 2 {
		t.Fatalf("resolved requirements = %+v, want p1 collapsed to one entry", got.Requirements)
	}
	if got.Requirements[0].ID != "p1" || len(got.Requirements[0].CriteriaIDs) != 2 {
		t.Fatalf("p1 must resolve to the LAST entry, got %+v", got.Requirements[0])
	}
	if got.Requirements[1].ID != "p2" {
		t.Fatalf("p2 must be preserved in order, got %+v", got.Requirements[1])
	}
	if got.Version != 3 {
		t.Fatalf("version = %d, want 3", got.Version)
	}
}

// --- CheckDef / registry digests ---------------------------------------------

// TestCheckDefDigestChangeUpgrades: ANY field change between two versions of a
// registered check upgrades — content changes via the recomputed digest, and a
// changed stored digest label alone also counts.
func TestCheckDefDigestChangeUpgrades(t *testing.T) {
	base := CheckDef{
		CheckID:  "check-export",
		Command:  "bin/export-check",
		Args:     []string{"--strict"},
		Cwd:      "hooks",
		EnvAllow: []string{"HOME"},
		Trust:    "framework",
	}
	base.Digest = CheckDefDigest(base)

	if DigestChanged(base, base) {
		t.Fatal("identical check defs must not report a change")
	}
	mutations := map[string]func(*CheckDef){
		"command":   func(c *CheckDef) { c.Command = "bin/export-check-v2" },
		"args":      func(c *CheckDef) { c.Args = []string{"--strict", "--more"} },
		"cwd":       func(c *CheckDef) { c.Cwd = "elsewhere" },
		"env_allow": func(c *CheckDef) { c.EnvAllow = []string{"HOME", "PATH"} },
		"trust":     func(c *CheckDef) { c.Trust = "project" },
		"check_id":  func(c *CheckDef) { c.CheckID = "check-renamed" },
	}
	for name, mutate := range mutations {
		mutated := base
		mutate(&mutated)
		mutated.Digest = CheckDefDigest(mutated)
		if !DigestChanged(base, mutated) {
			t.Fatalf("%s change must upgrade", name)
		}
	}
	// A relabeled digest with unchanged content is still a change.
	relabel := base
	relabel.Digest = "0000"
	if !DigestChanged(base, relabel) {
		t.Fatal("changed digest label alone must upgrade")
	}
	// Order-insensitive slices: same content in different order is a change to
	// the definition (args are positional) — but equal sets of env vars in a
	// different order are still serialized differently, so both count.
	reordered := base
	reordered.Args = []string{"--more", "--strict"}
	reordered.Digest = CheckDefDigest(reordered)
	if !DigestChanged(base, reordered) {
		t.Fatal("reordered args are a different definition and must upgrade")
	}
}

// TestValidateEnvelopeCheckDigestFrozen: a verification_scope entry
// "check_id@digest" freezes the check definition at envelope creation — a
// registry whose definition drifted must reject the envelope (version-upgrade
// required); a bare "check_id" accepts the current registry definition but
// still requires the check to be registered.
func TestValidateEnvelopeCheckDigestFrozen(t *testing.T) {
	frozen := FrozenView{Version: 1, Requirements: []FrozenRequirement{
		{ID: "p1", CriteriaIDs: []string{"c-1"}, Artifacts: []string{"a.md"}},
	}}
	def := CheckDef{CheckID: "check-export", Command: "bin/export-check", Args: []string{"--strict"}, Cwd: "hooks", EnvAllow: []string{"HOME"}, Trust: "framework"}
	def.Digest = CheckDefDigest(def)

	mechanical := func(checkRef string) Envelope {
		env := envelopeWithCriteria("c-1")
		env.ExitCriteria[0].Method = MethodMechanical
		env.ExitCriteria[0].CheckID = "check-export"
		env.VerificationScope.CheckIDs = []string{checkRef}
		return env
	}

	// Frozen digest matches the registry: valid.
	env := mechanical("check-export@" + def.Digest)
	if err := ValidateEnvelope(env, frozen, nil, []CheckDef{def}); err != nil {
		t.Fatalf("frozen digest match must validate: %v", err)
	}

	// Definition changed since the envelope froze its digest: reject.
	drifted := def
	drifted.Args = []string{"--strict", "--extra"}
	drifted.Digest = CheckDefDigest(drifted)
	err := ValidateEnvelope(env, frozen, nil, []CheckDef{drifted})
	if err == nil {
		t.Fatal("changed check definition must reject the envelope")
	}
	if !strings.Contains(err.Error(), "upgrade") {
		t.Fatalf("error must demand a version upgrade, got: %v", err)
	}

	// Bare check id: no frozen digest, current registry accepted.
	bare := mechanical("check-export")
	if err := ValidateEnvelope(bare, frozen, nil, []CheckDef{drifted}); err != nil {
		t.Fatalf("bare check id against the current registry must validate: %v", err)
	}

	// Unregistered check: reject.
	ghost := envelopeWithCriteria("c-1")
	ghost.ExitCriteria[0].Method = MethodMechanical
	ghost.ExitCriteria[0].CheckID = "check-ghost"
	ghost.VerificationScope.CheckIDs = []string{"check-ghost"}
	if err := ValidateEnvelope(ghost, frozen, nil, []CheckDef{def}); err == nil {
		t.Fatal("unregistered check must be rejected")
	}
}

// --- evidence reconciliation ---------------------------------------------------

// TestEvidenceReconcile: every claimed evidence id must appear as a tool_use
// record id in the reviewer's own JSONL transcript; a claimed-but-absent id is
// a forged-id error. Empty claims refuse (vacuous success would launder
// rubber-stamps).
func TestEvidenceReconcile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	lines := []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"reading"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01","name":"Read","input":{"file_path":"a.md"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_02","name":"Bash","input":{"command":"make test"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"ok"}]}}`,
		`this line is not json and must be skipped`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	if err := ReconcileEvidence([]string{"toolu_01", "toolu_02"}, path); err != nil {
		t.Fatalf("all-present evidence must reconcile: %v", err)
	}
	err := ReconcileEvidence([]string{"toolu_01", "toolu_fake"}, path)
	if err == nil {
		t.Fatal("claimed id absent from the transcript must error")
	}
	if !strings.Contains(err.Error(), "toolu_fake") {
		t.Fatalf("error must name the forged id, got: %v", err)
	}
	if err := ReconcileEvidence([]string{""}, path); err == nil {
		t.Fatal("empty evidence id must error")
	}
	if err := ReconcileEvidence(nil, path); err == nil {
		t.Fatal("empty evidence list must error (nothing claimed)")
	}
	if err := ReconcileEvidence([]string{"toolu_01"}, filepath.Join(dir, "missing.jsonl")); err == nil {
		t.Fatal("missing transcript must error")
	}
}

// --- rubber-stamp rule ----------------------------------------------------------

// TestValidateEvidenceRubberStamp: a PASS-track result (COMPLETED + PASS) with
// empty evidence_tool_call_ids is a protocol error; every other track is
// evidence-exempt (BLOCKED carries a reason code, NEEDS-FIX carries findings).
func TestValidateEvidenceRubberStamp(t *testing.T) {
	cases := []struct {
		name string
		res  ReviewResult
		want bool // want error
	}{
		{"PASS with evidence", ReviewResult{RunStatus: ReviewRunCompleted, Verdict: VerdictPass, EvidenceToolCallIDs: []string{"toolu_01"}}, false},
		{"PASS without evidence", ReviewResult{RunStatus: ReviewRunCompleted, Verdict: VerdictPass}, true},
		{"NEEDS-FIX without evidence", ReviewResult{RunStatus: ReviewRunCompleted, Verdict: VerdictNeedsFix}, false},
		{"BLOCKED without evidence", ReviewResult{RunStatus: ReviewRunBlocked, ReasonCode: "L3-UNAVAILABLE"}, false},
	}
	for _, tc := range cases {
		err := ValidateEvidence(tc.res)
		if got := err != nil; got != tc.want {
			t.Fatalf("%s: error=%v, want error=%v", tc.name, err, tc.want)
		}
	}
}

// TestRecordReviewResultEmptyPassIsProtocolError pins the reducer wiring: an
// empty-evidence PASS routes through the SAME protocol-error path as enum
// violations — review marked FAILED, failure checkpoint opened, error returned.
func TestRecordReviewResultEmptyPassIsProtocolError(t *testing.T) {
	r := pendingReviewRun(t)
	err := r.RecordReviewResult(ReviewResult{ReviewID: "rev-1", RunStatus: ReviewRunCompleted, Verdict: VerdictPass})
	if err == nil {
		t.Fatal("empty-evidence PASS must be a protocol error")
	}
	if !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("error must name the evidence problem, got: %v", err)
	}
	if r.Reviews[0].Status != ReviewFailed {
		t.Fatalf("review = %s, want FAILED", r.Reviews[0].Status)
	}
	if r.Status != StatusWaitUser || r.CurrentCheckpoint() == nil || r.CurrentCheckpoint().Kind != CheckpointReviewResult {
		t.Fatalf("protocol error must route to a REVIEW_RESULT checkpoint, status=%s cp=%+v", r.Status, r.CurrentCheckpoint())
	}
}

// --- BLOCKED vs verdict -----------------------------------------------------------

// TestBlockedSeparateFromVerdict pins M12 through RecordReviewResult: BLOCKED
// carries reason_code only (no verdict, no rework budget); COMPLETED carries a
// verdict only; only NEEDS-FIX consumes rework budget.
func TestBlockedSeparateFromVerdict(t *testing.T) {
	r := concludedReviewRun(t, ReviewResult{
		ReviewID: "rev-1", RunStatus: ReviewRunBlocked, ReasonCode: "L2_SANDBOX_INACTIVE",
	})
	rv := r.Reviews[0]
	if rv.Status != ReviewBlocked || rv.Verdict != "" || rv.ReasonCode != "L2_SANDBOX_INACTIVE" {
		t.Fatalf("BLOCKED record = %+v, want status BLOCKED, empty verdict, reason kept", rv)
	}
	if r.Counters.RunRework != 0 {
		t.Fatalf("BLOCKED consumed rework budget: %d, want 0", r.Counters.RunRework)
	}
	cp := r.CurrentCheckpoint()
	if cp == nil || !strings.Contains(cp.Note, ReviewRunBlocked) || strings.Contains(cp.Note, VerdictPass) || strings.Contains(cp.Note, VerdictNeedsFix) {
		t.Fatalf("BLOCKED checkpoint note = %+v, want run status + reason only", cp)
	}

	r2 := concludedReviewRun(t, ReviewResult{
		ReviewID: "rev-1", RunStatus: ReviewRunCompleted, Verdict: VerdictPass,
		ArtifactDigest: "dd-1", EvidenceToolCallIDs: []string{"toolu_01"},
	})
	rv2 := r2.Reviews[0]
	if rv2.Status != ReviewReviewed || rv2.Verdict != VerdictPass || rv2.ReasonCode != "" {
		t.Fatalf("COMPLETED record = %+v, want verdict only", rv2)
	}
	if r2.Counters.RunRework != 0 {
		t.Fatalf("PASS consumed rework budget: %d, want 0", r2.Counters.RunRework)
	}

	r3 := concludedReviewRun(t, ReviewResult{
		ReviewID: "rev-1", RunStatus: ReviewRunCompleted, Verdict: VerdictNeedsFix,
		Findings: []Finding{{CriterionID: "c1", Path: "pkg/x.go", Line: 7, Failure: "nope"}},
	})
	if r3.Counters.RunRework != 1 {
		t.Fatalf("NEEDS-FIX rework = %d, want 1", r3.Counters.RunRework)
	}
}

// --- reuse / void -------------------------------------------------------------------

// TestSameDigestReuse: a concluded REVIEWED record with the same envelope
// digest AND artifact digest is reusable — no re-review, no recursion. Any
// other envelope, and non-REVIEWED records, are not.
func TestSameDigestReuse(t *testing.T) {
	env := testEnvelope()
	r := concludedReviewRun(t, ReviewResult{
		ReviewID: "rev-1", RunStatus: ReviewRunCompleted, Verdict: VerdictPass,
		ArtifactDigest: ArtifactSetDigest(env), EvidenceToolCallIDs: []string{"toolu_01"},
	})
	rv, ok := ReusableReview(r, env)
	if !ok || rv == nil {
		t.Fatal("same envelope digest + artifact digest must be reusable")
	}
	if rv.ReviewID != "rev-1" || rv.Verdict != VerdictPass {
		t.Fatalf("reusable record = %+v", rv)
	}

	// A different envelope (here: different review_id → different digest) is
	// not covered by the reuse.
	other := env
	other.ReviewID = "rev-2"
	if rv, ok := ReusableReview(r, other); ok || rv != nil {
		t.Fatal("different envelope digest must not reuse")
	}

	// A BLOCKED conclusion is never reusable, even with matching digests.
	blocked := concludedReviewRun(t, ReviewResult{
		ReviewID: "rev-1", RunStatus: ReviewRunBlocked, ReasonCode: "L2_SANDBOX_INACTIVE",
		ArtifactDigest: ArtifactSetDigest(env),
	})
	if rv, ok := ReusableReview(blocked, env); ok || rv != nil {
		t.Fatal("BLOCKED record must not be reusable")
	}
}

// TestStaleArtifactVoidsVerdict: a same-envelope REVIEWED record whose artifact
// digest drifted (artifacts changed around the review) is NOT reusable and its
// prior verdict is VOID — the predicate must expose nothing reusable and must
// not mutate the record it refuses.
func TestStaleArtifactVoidsVerdict(t *testing.T) {
	env := testEnvelope()
	r := concludedReviewRun(t, ReviewResult{
		ReviewID: "rev-1", RunStatus: ReviewRunCompleted, Verdict: VerdictPass,
		ArtifactDigest: "drifted-digest", EvidenceToolCallIDs: []string{"toolu_01"},
	})
	rv, ok := ReusableReview(r, env)
	if ok || rv != nil {
		t.Fatal("artifact digest drift must not be reusable (verdict is void — re-review required)")
	}
	// Pure predicate: the prior record is untouched, still carrying the
	// verdict callers must not cite.
	if r.Reviews[0].Status != ReviewReviewed || r.Reviews[0].Verdict != VerdictPass {
		t.Fatalf("predicate mutated the record: %+v", r.Reviews[0])
	}
}
