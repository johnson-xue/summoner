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

// envelopeWithCriteria builds a MINTED-shape envelope whose exit criteria are
// exactly ids (all judgment, so no registry is needed for the set-equality
// cases). Since the final-fix wave this is the stored/minted form — the wire
// form is identity-only (see testEnvelope).
func envelopeWithCriteria(ids ...string) SemanticEnvelope {
	env := testSemanticEnvelope()
	crit := make([]Criterion, 0, len(ids))
	for _, id := range ids {
		crit = append(crit, Criterion{ID: id, Goal: "goal " + id, Method: MethodJudgment, Rubric: "rubric-1"})
	}
	env.ExitCriteria = crit
	env.VerificationScope = VerificationScope{Readable: []string{"."}}
	return env
}

// testSemanticEnvelope is the minted-envelope twin of testEnvelope: the shape
// MintEnvelope produces and ValidateEnvelope/ReusableReview consume.
func testSemanticEnvelope() SemanticEnvelope {
	return SemanticEnvelope{
		SchemaVersion: "2",
		RunID:         "run-1",
		ReviewID:      "rev-1",
		ArtifactSet:   []ArtifactRef{{Path: "pkg/x.go", Digest: "abc", Kind: "code"}},
		Requirements: FrozenView{Version: 1, Requirements: []FrozenRequirement{
			{ID: "req-1", CriteriaIDs: []string{"c1"}, Artifacts: []string{"pkg/x.go"}},
		}},
		ExitCriteria: []Criterion{
			{ID: "c1", Goal: "export works", Method: MethodJudgment, Rubric: "rubric-1"},
		},
		VerificationScope: VerificationScope{Readable: []string{"pkg/"}},
	}
}

// coveringResult builds a result that covers EVERY active criterion of the
// run's minted envelope with the given per-criterion status — the exact
// coverage the recorder requires since C1b. evidence ids anchor each verdict.
func coveringResult(r *RunState, reviewID, status string) ReviewResult {
	res := ReviewResult{ReviewID: reviewID, RunStatus: ReviewRunCompleted, EvidenceToolCallIDs: []string{"toolu_01"}}
	for _, rv := range r.Reviews {
		if rv.ReviewID != reviewID {
			continue
		}
		res.ArtifactDigest = rv.ArtifactDigest
		for _, c := range rv.Envelope.ExitCriteria {
			res.CriterionVerdicts = append(res.CriterionVerdicts, CriterionVerdict{
				CriterionID: c.ID,
				Status:      status,
				Evidence:    []EvidenceAnchor{{ToolUseID: "toolu_01"}},
			})
		}
	}
	if status == "fail" {
		res.Verdict = VerdictNeedsFix
	} else {
		res.Verdict = VerdictPass
	}
	return res
}

// pendingReviewRun drives a run to REVIEW_PENDING with review rev-1 in flight.
func pendingReviewRun(t *testing.T) *RunState {
	t.Helper()
	r := startTestRun(t, BudgetLimits{})
	openAndSubmit(t, r, "p1", "c1")
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue)
	if _, err := r.RequestReview(testEnvelope(), nil); err != nil {
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

// --- DecodeEnvelope (identity-only wire, final-fix wave C1) ----------------------

// TestRegistrationShapeIsIdentityOnly: the review-submit wire form is EXACTLY
// {schema_version: "2", run_id, review_id} — an unknown field anywhere is a
// parse error, semantic fields (artifact_set, exit_criteria, requirements,
// verification_scope) are THEMSELVES unknown fields on this wire, and the
// correlation keys are required non-empty with schema exactly "2".
func TestRegistrationShapeIsIdentityOnly(t *testing.T) {
	valid, err := json.Marshal(testEnvelope())
	if err != nil {
		t.Fatalf("marshal identity registration: %v", err)
	}
	if _, err := DecodeEnvelope(valid); err != nil {
		t.Fatalf("valid identity registration rejected: %v", err)
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

	// PRODUCER-SUPPLIED SEMANTICS on the wire are rejected as unknown fields —
	// the pre-final-wave envelope body is now itself contraband: the reducer
	// mints every semantic byte from the pinned acceptance snapshot.
	for name, semantic := range map[string]interface{}{
		"artifact_set":       []interface{}{map[string]interface{}{"path": "x.go", "digest": "lie"}},
		"exit_criteria":      []interface{}{map[string]interface{}{"id": "c1", "method": "judgment", "goal": "always pass"}},
		"requirements":       map[string]interface{}{"version": 900},
		"verification_scope": map[string]interface{}{"readable": []interface{}{"/"}},
		"environment":        map[string]interface{}{"os": "lie"},
	} {
		top := map[string]interface{}{}
		if err := json.Unmarshal(valid, &top); err != nil {
			t.Fatalf("unmarshal top: %v", err)
		}
		top[name] = semantic
		poisoned, _ := json.Marshal(top)
		if _, err := DecodeEnvelope(poisoned); err == nil {
			t.Fatalf("producer-supplied %s must be an unknown-field parse error (identity-only wire)", name)
		}
	}

	// Required keys non-empty; schema exactly "2".
	for name, mutate := range map[string]func(*Envelope){
		"schema_version empty": func(e *Envelope) { e.SchemaVersion = "" },
		"schema_version not 2": func(e *Envelope) { e.SchemaVersion = "unexpected-version" },
		"run_id empty":         func(e *Envelope) { e.RunID = "" },
		"review_id empty":      func(e *Envelope) { e.ReviewID = "" },
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

// TestReacceptanceClearsDrop pins the fix-round F3 ruling: a re-freeze
// supersedes an earlier drop of the same criterion id — decidePhase(accepted)
// removes ledger entries whose id is among the re-accepted criteria (same
// philosophy as M10's stale-clearing), so the criterion returns to the frozen
// set ValidateEnvelope expects.
func TestReacceptanceClearsDrop(t *testing.T) {
	r := startTestRun(t, BudgetLimits{})
	openAndSubmit(t, r, "p1", "c-1") // frozen: p1 → [c-1]
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue)

	// A recorded user drop of c-1 (recording path arrives with T10).
	r.CriteriaDrops = []CriteriaDrop{{ID: "c-1", At: "2026-11-10T00:00:00Z", CheckpointID: "cp-9"}}
	if err := r.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// While dropped, the envelope set omits c-1 (frozen − drops).
	frozen := FrozenView{Version: r.RequirementsVersion, Requirements: r.Acceptance.Requirements}
	if err := ValidateEnvelope(envelopeWithCriteria(), frozen, r.CriteriaDrops, nil); err != nil {
		t.Fatalf("dropped criterion must leave the expected set: %v", err)
	}

	// The same criterion is re-accepted via a later phase acceptance → the
	// drop leaves the ledger.
	openAndSubmit(t, r, "p1", "c-1") // re-opened p1 with c-1 back in its criteria
	displayCurrent(t, r)
	decideCurrent(t, r, DecisionContinue)
	if len(r.CriteriaDrops) != 0 {
		t.Fatalf("re-acceptance must clear the drop ledger entry: %+v", r.CriteriaDrops)
	}

	// The criterion is back in the expected set: an envelope WITH c-1
	// validates, one WITHOUT it is a producer trim.
	refrozen := FrozenView{Version: r.RequirementsVersion, Requirements: r.Acceptance.Requirements}
	if err := ValidateEnvelope(envelopeWithCriteria("c-1"), refrozen, r.CriteriaDrops, nil); err != nil {
		t.Fatalf("re-frozen criterion must be expected again: %v", err)
	}
	if err := ValidateEnvelope(envelopeWithCriteria(), refrozen, r.CriteriaDrops, nil); err == nil {
		t.Fatal("envelope omitting the re-frozen criterion must be rejected")
	}
}

// TestArtifactSetDigestDeterministicWithDuplicatePaths pins fix-round F4: the
// artifact-set digest sorts by the full (path, digest, kind) key — duplicate
// paths hash in a stable order, and input order never changes the digest.
func TestArtifactSetDigestDeterministicWithDuplicatePaths(t *testing.T) {
	base := testSemanticEnvelope()
	base.ArtifactSet = []ArtifactRef{
		{Path: "pkg/x.go", Digest: "aaa", Kind: "code"},
		{Path: "pkg/x.go", Digest: "bbb", Kind: "code"},
		{Path: "pkg/y.go", Digest: "ccc", Kind: "code"},
	}
	first := ArtifactSetDigest(base)
	for i := 0; i < 8; i++ {
		// Shuffle deterministically (rotate) and re-digest: same set, same digest.
		rotated := append([]ArtifactRef{base.ArtifactSet[len(base.ArtifactSet)-1]}, base.ArtifactSet[:len(base.ArtifactSet)-1]...)
		shuffled := base
		shuffled.ArtifactSet = rotated
		if got := ArtifactSetDigest(shuffled); got != first {
			t.Fatalf("ArtifactSetDigest is order-sensitive: %s vs %s", got, first)
		}
		base.ArtifactSet = rotated
	}
}

// TestValidateEnvelopeRejectsVacuousReview pins the fix-round F5 ruling: an
// empty frozen acceptance view with an empty drop ledger is the vacuous case —
// nothing has been accepted yet, no review is due — while an all-dropped empty
// exit-criteria set stays legitimate.
func TestValidateEnvelopeRejectsVacuousReview(t *testing.T) {
	empty := envelopeWithCriteria() // zero exit criteria
	if err := ValidateEnvelope(empty, FrozenView{}, nil, nil); err == nil {
		t.Fatal("empty frozen view + empty drop ledger must reject (no review is due)")
	}
	// A frozen view whose requirements carry no criteria is equally vacuous.
	barren := FrozenView{Version: 1, Requirements: []FrozenRequirement{{ID: "p1"}}}
	if err := ValidateEnvelope(empty, barren, nil, nil); err == nil {
		t.Fatal("frozen view with zero criteria + no drops must reject")
	}
	// All-dropped is legitimate: the view had criteria, the user dropped them
	// all — the empty envelope set is exactly frozen − drops.
	dropped := FrozenView{Version: 1, Requirements: []FrozenRequirement{
		{ID: "p1", CriteriaIDs: []string{"c-1"}},
	}}
	drops := []CriteriaDrop{{ID: "c-1", At: "2026-11-10T00:00:00Z", CheckpointID: "cp-2"}}
	if err := ValidateEnvelope(empty, dropped, drops, nil); err != nil {
		t.Fatalf("all-dropped empty exit criteria must stay legitimate: %v", err)
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

	mechanical := func(checkRef string) SemanticEnvelope {
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

	// Fix-round F2: with a registry present the freeze is MANDATORY — a bare
	// check id (no frozen digest) is rejected, not silently accepted.
	bare := mechanical("check-export")
	if err := ValidateEnvelope(bare, frozen, nil, []CheckDef{def}); err == nil {
		t.Fatal("bare check id (no frozen digest) must be rejected when a registry is present")
	}

	// A missing scope entry for a referenced mechanical check is rejected.
	missing := mechanical("check-export")
	missing.VerificationScope.CheckIDs = []string{"check-other@" + def.Digest}
	if err := ValidateEnvelope(missing, frozen, nil, []CheckDef{def}); err == nil {
		t.Fatal("mechanical criterion without a scope entry for its check must be rejected")
	}

	// A malformed entry with an empty digest after @ is rejected at parse.
	malformed := mechanical("check-export@")
	if err := ValidateEnvelope(malformed, frozen, nil, []CheckDef{def}); err == nil {
		t.Fatal("check_ids entry with an empty digest after @ must be rejected")
	}

	// Unregistered check: reject.
	ghost := envelopeWithCriteria("c-1")
	ghost.ExitCriteria[0].Method = MethodMechanical
	ghost.ExitCriteria[0].CheckID = "check-ghost"
	ghost.VerificationScope.CheckIDs = []string{"check-ghost"}
	if err := ValidateEnvelope(ghost, frozen, nil, []CheckDef{def}); err == nil {
		t.Fatal("unregistered check must be rejected")
	}

	// A nil registry keeps the lenient path — the registry arrives with T9,
	// and until then there is nothing to freeze against.
	if err := ValidateEnvelope(ghost, frozen, nil, nil); err != nil {
		t.Fatalf("nil registry must skip the registry checks: %v", err)
	}
}

// --- evidence reconciliation ---------------------------------------------------

// TestEvidenceReconcile: every claimed evidence id must be a SUCCESSFUL
// OBSERVED child tool call — a tool_use of an observation tool paired with a
// non-error tool_result in the reviewer's own JSONL transcript. A claimed-but-
// absent id is a forged-id error; an UNPAIRED call, a FAILED result and the
// handback echo prove nothing (final-fix wave C1c). Empty claims refuse
// (vacuous success would launder rubber-stamps).
func TestEvidenceReconcile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	lines := []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"reading"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01","name":"Read","input":{"file_path":"a.md"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_02","name":"Bash","input":{"command":"make test"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_03","name":"Bash","input":{"command":"make test"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_04","name":"Read","input":{"file_path":"b.md"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_05","name":"SubagentHandback","input":{}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"ok"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_03","content":"ok","is_error":false}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_04","content":"file does not exist","is_error":true}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_05","content":"done"}]}}`,
		`this line is not json and must be skipped`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	if err := ReconcileEvidence([]string{"toolu_01", "toolu_03"}, path); err != nil {
		t.Fatalf("successful observed pairs must reconcile: %v", err)
	}
	// An UNPAIRED call (toolu_02 has no result) proves nothing.
	err := ReconcileEvidence([]string{"toolu_02"}, path)
	if err == nil || !strings.Contains(err.Error(), "not a successful observed child tool call") {
		t.Fatalf("unpaired call must be refused as proof, got: %v", err)
	}
	// A FAILED result (toolu_04 is_error) proves nothing.
	err = ReconcileEvidence([]string{"toolu_04"}, path)
	if err == nil || !strings.Contains(err.Error(), "not a successful observed child tool call") {
		t.Fatalf("failed result must be refused as proof, got: %v", err)
	}
	// The handback echo (toolu_05) is not an observation tool.
	err = ReconcileEvidence([]string{"toolu_05"}, path)
	if err == nil || !strings.Contains(err.Error(), "not a successful observed child tool call") {
		t.Fatalf("handback echo must be refused as proof, got: %v", err)
	}
	err = ReconcileEvidence([]string{"toolu_01", "toolu_fake"}, path)
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
	r := pendingReviewRun(t)
	if err := r.RecordReviewResult(ReviewResult{
		ReviewID: "rev-1", RunStatus: ReviewRunBlocked, ReasonCode: "L2_SANDBOX_INACTIVE",
		ArtifactDigest: r.Reviews[0].ArtifactDigest,
	}); err != nil {
		t.Fatalf("RecordReviewResult(BLOCKED): %v", err)
	}
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

	r2 := pendingReviewRun(t)
	r2 = concludedReviewRun(t, coveringResult(r2, "rev-1", "pass"))
	rv2 := r2.Reviews[0]
	if rv2.Status != ReviewReviewed || rv2.Verdict != VerdictPass || rv2.ReasonCode != "" {
		t.Fatalf("COMPLETED record = %+v, want verdict only", rv2)
	}
	if r2.Counters.RunRework != 0 {
		t.Fatalf("PASS consumed rework budget: %d, want 0", r2.Counters.RunRework)
	}

	r3 := pendingReviewRun(t)
	r3 = concludedReviewRun(t, coveringResult(r3, "rev-1", "fail"))
	if r3.Counters.RunRework != 1 {
		t.Fatalf("NEEDS-FIX rework = %d, want 1", r3.Counters.RunRework)
	}
}

// --- reuse / void -------------------------------------------------------------------

// TestSameDigestReuse: a concluded REVIEWED record with the same envelope
// digest AND artifact digest is reusable — no re-review, no recursion. Any
// other envelope, and non-REVIEWED records, are not.
func TestSameDigestReuse(t *testing.T) {
	r := pendingReviewRun(t)
	r = concludedReviewRun(t, coveringResult(r, "rev-1", "pass"))
	env := r.Reviews[0].Envelope
	rv, ok := ReusableReview(r, env)
	if !ok || rv == nil {
		t.Fatal("same envelope digest + artifact digest must be reusable")
	}
	if rv.ReviewID != "rev-1" || rv.Verdict != VerdictPass {
		t.Fatalf("reusable record = %+v", rv)
	}

	// Reuse fires under a NEW review_id with identical content — the stored
	// digest is a CONTENT digest (review_id excluded): a fresh ⑤ request for
	// the same artifacts + frozen criteria + verification environment reuses
	// the concluded review instead of re-reviewing (spec §3 同版本验收复用).
	fresh := env
	fresh.ReviewID = "rev-2"
	rvFresh, okFresh := ReusableReview(r, fresh)
	if !okFresh || rvFresh == nil || rvFresh.ReviewID != "rev-1" {
		t.Fatalf("new review id with identical content must reuse rev-1, got (%+v, %v)", rvFresh, okFresh)
	}

	// A genuine content difference (artifact digest changed) is not covered.
	other := env
	other.ArtifactSet[0].Digest = "changed"
	if rv, ok := ReusableReview(r, other); ok || rv != nil {
		t.Fatal("different content must not reuse")
	}

	// A BLOCKED conclusion is never reusable, even with matching digests.
	blockedRun := pendingReviewRun(t)
	if err := blockedRun.RecordReviewResult(ReviewResult{
		ReviewID: "rev-1", RunStatus: ReviewRunBlocked, ReasonCode: "L2_SANDBOX_INACTIVE",
		ArtifactDigest: blockedRun.Reviews[0].ArtifactDigest,
	}); err != nil {
		t.Fatalf("RecordReviewResult(BLOCKED): %v", err)
	}
	if rv, ok := ReusableReview(blockedRun, blockedRun.Reviews[0].Envelope); ok || rv != nil {
		t.Fatal("BLOCKED record must not be reusable")
	}
}

// TestWrongArtifactDigestIsProtocolError pins C1b: a result whose
// artifact_digest does not match the minted envelope's artifact set digest is
// REJECTED at recording — the review is marked FAILED and routed to a
// REVIEW_RESULT checkpoint, never stored as REVIEWED (the pre-final-wave bug
// stored the lie verbatim). Drifted CURRENT bytes are equally refused
// (freshness at conclusion).
func TestWrongArtifactDigestIsProtocolError(t *testing.T) {
	r := pendingReviewRun(t)
	want := r.Reviews[0].ArtifactDigest
	res := coveringResult(r, "rev-1", "pass")
	res.ArtifactDigest = strings.Repeat("0", 64)
	err := r.RecordReviewResult(res)
	if err == nil || !strings.Contains(err.Error(), "artifact_digest") {
		t.Fatalf("lying artifact digest must be a named protocol error, got: %v", err)
	}
	if r.Reviews[0].Status != ReviewFailed {
		t.Fatalf("review = %s, want FAILED", r.Reviews[0].Status)
	}
	if r.Status != StatusWaitUser || r.CurrentCheckpoint() == nil || r.CurrentCheckpoint().Kind != CheckpointReviewResult {
		t.Fatalf("protocol error must route to a REVIEW_RESULT checkpoint, status=%s", r.Status)
	}

	// Freshness at conclusion: correct digest but the artifact changed on disk
	// since the mint — refused the same way.
	r2 := pendingReviewRun(t)
	res2 := coveringResult(r2, "rev-1", "pass")
	path := filepath.Join(projectDirForTest(), r2.Reviews[0].Envelope.ArtifactSet[0].Path)
	if err := os.WriteFile(path, []byte("tampered after mint\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = want
	if err := r2.RecordReviewResult(res2); err == nil || !strings.Contains(err.Error(), "drifted") {
		t.Fatalf("post-mint artifact drift must refuse conclusion, got: %v", err)
	}
	if r2.Reviews[0].Status != ReviewFailed {
		t.Fatalf("drifted review = %s, want FAILED", r2.Reviews[0].Status)
	}
}

// TestCoverageValidation pins C1b's exact-coverage rule through the real
// recorder: a foreign criterion id, a missing active criterion and a
// PASS-despite-FAIL aggregate are all protocol errors; complete coverage
// records REVIEWED.
func TestCoverageValidation(t *testing.T) {
	r := pendingReviewRun(t)
	active := r.Reviews[0].Envelope.ExitCriteria
	if len(active) == 0 {
		t.Fatal("minted envelope carries no active criteria")
	}
	// Foreign id.
	res := coveringResult(r, "rev-1", "pass")
	res.CriterionVerdicts[0].CriterionID = "not-an-accepted-criterion"
	if err := r.RecordReviewResult(res); err == nil || !strings.Contains(err.Error(), "not an active criterion") {
		t.Fatalf("foreign criterion must be a named protocol error, got: %v", err)
	}

	// Missing active criterion.
	r2 := pendingReviewRun(t)
	res2 := coveringResult(r2, "rev-1", "pass")
	res2.CriterionVerdicts = res2.CriterionVerdicts[1:]
	if err := r2.RecordReviewResult(res2); err == nil || !strings.Contains(err.Error(), "omits active criteria") {
		t.Fatalf("missing criterion must be a named protocol error, got: %v", err)
	}

	// Aggregate PASS over a FAIL criterion.
	r3 := pendingReviewRun(t)
	res3 := coveringResult(r3, "rev-1", "pass")
	res3.CriterionVerdicts[0].Status = "fail"
	if err := r3.RecordReviewResult(res3); err == nil || !strings.Contains(err.Error(), "prevents PASS") {
		t.Fatalf("PASS over FAIL must be a named protocol error, got: %v", err)
	}
	if r3.Reviews[0].Status != ReviewFailed {
		t.Fatalf("review = %s, want FAILED", r3.Reviews[0].Status)
	}
}

// TestReuseFiresUnderNewReviewID pins the fix-round F1 semantics end to end:
// the digest RequestReview stores is a CONTENT digest (everything except
// review_id — the spec §3 triple: artifacts + frozen criteria + verification
// environment), so a later ⑤ request that mints a fresh review_id over
// identical content reuses the concluded review — no re-review, no recursion.
func TestReuseFiresUnderNewReviewID(t *testing.T) {
	r := pendingReviewRun(t)
	r = concludedReviewRun(t, coveringResult(r, "rev-1", "pass"))

	// The same content under a new review id: reusable, and the reusable
	// record is the PRIOR review's.
	second := r.Reviews[0].Envelope
	second.ReviewID = "rev-2"
	rv, ok := ReusableReview(r, second)
	if !ok || rv == nil || rv.ReviewID != "rev-1" || rv.Verdict != VerdictPass {
		t.Fatalf("content-identical envelope under rev-2: got (%+v, %v), want reuse of rev-1", rv, ok)
	}

	// Any content-side change breaks the match: criteria, scope, artifact set.
	mutants := map[string]func(*SemanticEnvelope){
		"artifact_set": func(e *SemanticEnvelope) { e.ArtifactSet[0].Digest = "different" },
		"criteria":     func(e *SemanticEnvelope) { e.ExitCriteria[0].Goal = "different goal" },
		"scope":        func(e *SemanticEnvelope) { e.VerificationScope.Readable = []string{"elsewhere/"} },
	}
	for name, mutate := range mutants {
		m := r.Reviews[0].Envelope
		m.ReviewID = "rev-2"
		mutate(&m)
		if rv, ok := ReusableReview(r, m); ok || rv != nil {
			t.Fatalf("%s change must not reuse", name)
		}
	}
}
