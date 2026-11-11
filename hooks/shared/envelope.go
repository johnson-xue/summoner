package shared

// envelope.go — the ⑤ review-envelope validation layer (spec
// docs/superpowers/specs/2026-11-09-single-loop-checkpoint-design.md §3).
//
// The wire types (Envelope, FrozenView, Criterion, ReviewResult) live in
// types.go so every hook shares one shape; this file adds ONLY the functions
// that validate and reconcile them: strict decode, frozen-set equality
// against the run's frozen acceptance view (minus recorded user drops),
// check-registry digest freezing, evidence reconciliation against the
// reviewer's own transcript, the rubber-stamp rule and same-digest reuse.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// --- decode -----------------------------------------------------------------------

// DecodeEnvelope decodes the review-submit registration args: IDENTITY ONLY.
// The producer supplies {schema_version, run_id, review_id} and nothing else —
// an unknown field anywhere is a parse error (spec §3 严格 allow-list 拒未知字
// 段), and no producer-supplied semantic (artifact/requirement/criterion/scope
// content) exists on this wire at all: the reducer MINTS the complete envelope
// from the pinned acceptance snapshot (final-fix wave, C1).
func DecodeEnvelope(raw []byte) (Envelope, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var env Envelope
	if err := dec.Decode(&env); err != nil {
		return Envelope{}, fmt.Errorf("decode envelope: strict JSON: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return Envelope{}, errors.New("decode envelope: trailing data after the JSON object")
	}
	switch {
	case env.SchemaVersion == "":
		return Envelope{}, errors.New("decode envelope: schema_version is required")
	case env.SchemaVersion != "2":
		return Envelope{}, fmt.Errorf("decode envelope: schema_version must be exactly \"2\" (got %q)", env.SchemaVersion)
	case env.RunID == "":
		return Envelope{}, errors.New("decode envelope: run_id is required")
	case env.ReviewID == "":
		return Envelope{}, errors.New("decode envelope: review_id is required")
	}
	return env, nil
}

// --- frozen acceptance view ---------------------------------------------------------

// ResolveFrozenView collapses a serialized frozen view per requirement ID,
// LAST-ENTRY-WINS (mirrors the M11 last-visit-per-ID phase semantics: a
// re-accepted phase's later freeze supersedes its earlier one). The writer
// (freezeAcceptance) already replaces per ID; this read-side rule is the belt
// for state files assembled by other hands. Since the final-fix wave the view
// carries the complete semantics (criteria ledger, artifact set, check defs,
// scope, environment) — all of it passes through resolution untouched.
func ResolveFrozenView(view FrozenView) FrozenView {
	var out FrozenView
	idx := make(map[string]int, len(view.Requirements))
	for _, fr := range view.Requirements {
		if i, ok := idx[fr.ID]; ok {
			out.Requirements[i] = fr
			continue
		}
		idx[fr.ID] = len(out.Requirements)
		out.Requirements = append(out.Requirements, fr)
	}
	out.Version = view.Version
	out.Criteria = append([]Criterion(nil), view.Criteria...)
	out.ArtifactSet = append([]ArtifactRef(nil), view.ArtifactSet...)
	out.CheckDefs = append([]CheckDef(nil), view.CheckDefs...)
	out.CheckScope = view.CheckScope
	out.Environment = view.Environment
	out.SourceVisitID = view.SourceVisitID
	out.SourceCheckpointID = view.SourceCheckpointID
	out.AcceptedBy = view.AcceptedBy
	return out
}

// AcceptanceView returns the run's cumulative frozen acceptance view with
// per-requirement last-entry-wins applied — the view ValidateEnvelope checks
// envelopes against (信封 criteria 集 = 冻结集 − 显式 drop 记录).
func (r *RunState) AcceptanceView() FrozenView { return ResolveFrozenView(r.Acceptance) }

// --- check registry ------------------------------------------------------------------

// CheckDef is one registered mechanical check (spec §3 check registry): the
// command line, its working directory, the env-var allow-list, the definition
// digest and its trust tier (framework|project). Registry storage and
// manifest wiring arrive with T9; validation consumes the defs from here.
type CheckDef struct {
	CheckID  string   `json:"check_id"`
	Command  string   `json:"command"`
	Args     []string `json:"args,omitempty"`
	Cwd      string   `json:"cwd,omitempty"`
	EnvAllow []string `json:"env_allow,omitempty"`
	Digest   string   `json:"digest,omitempty"`
	Trust    string   `json:"trust,omitempty"` // framework|project
}

// CheckDefDigest is the definition digest of a registered check: sha256 over
// the canonical JSON of every content field (check_id, command, args, cwd,
// env allow-list, trust). The stored Digest label is excluded because it IS
// this digest; validation paths RECOMPUTE it, so a stale or lying label
// cannot hide a changed definition.
func CheckDefDigest(c CheckDef) string {
	c.Digest = ""
	b, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// DigestChanged reports whether two versions of a registered check differ in
// ANY field — content (recomputed digest) or the stored digest label. Any
// change upgrades (spec §3: 变更即信封版本升级): an envelope frozen against the
// old definition must not run the new one.
func DigestChanged(a, b CheckDef) bool {
	return a.Digest != b.Digest || CheckDefDigest(a) != CheckDefDigest(b)
}

// parseScopeCheckRef splits one verification_scope.check_ids entry: a bare
// "check_id" (no frozen digest — rejected for REFERENCED mechanical checks
// when a registry is present) or "check_id@digest" (the definition digest
// frozen at envelope creation). An empty digest after "@" is malformed and
// errors — a corrupted freeze must not silently downgrade to no-freeze.
func parseScopeCheckRef(entry string) (id, digest string, err error) {
	if entry == "" {
		return "", "", errors.New("empty verification_scope.check_ids entry")
	}
	parts := strings.SplitN(entry, "@", 2)
	if parts[0] == "" {
		return "", "", fmt.Errorf("malformed check_ids entry %q (no check id before @)", entry)
	}
	if len(parts) == 2 && parts[1] == "" {
		return "", "", fmt.Errorf("malformed check_ids entry %q (empty digest after @)", entry)
	}
	if len(parts) == 1 {
		return parts[0], "", nil // bare check id — no frozen digest
	}
	return parts[0], parts[1], nil
}

// --- framework default review criteria ---------------------------------------------

// defaultReviewCriteria is the framework's COMPLETE default verification set
// for every workflow with a review phase: the five review axes plus Prove-It,
// each with FULL criterion meaning (goal/scope/method/expected/rubric) — never
// bare ids. Merging these into every staged acceptance snapshot is what makes
// a no-manifest or id-only submission still review against complete shipped
// semantics (final-fix wave C1; the ids are DefaultReviewCriteriaIDs).
var defaultReviewCriteria = []Criterion{
	{
		ID: "crit-correctness", Method: MethodJudgment, Required: true, Scope: "accepted artifacts",
		Goal:     "The accepted requirement is actually met by the accepted artifact versions",
		Expected: "Every accepted exit criterion holds on the frozen artifact digests; behavior matches the requirement content",
		Rubric:   "references/review-rubric.md correctness axis",
	},
	{
		ID: "crit-idiom", Method: MethodJudgment, Required: true, Scope: "changed code",
		Goal:     "Changes follow the project's language and framework idioms",
		Expected: "No unidiomatic constructs introduced; naming, error handling and structure match the codebase",
		Rubric:   "references/review-rubric.md idiom axis",
	},
	{
		ID: "crit-architecture", Method: MethodJudgment, Required: true, Scope: "changed code",
		Goal:     "Changes respect the existing architecture and layering",
		Expected: "Boundaries and dependencies preserved; no accidental coupling or duplicated concepts",
		Rubric:   "references/review-rubric.md architecture axis",
	},
	{
		ID: "crit-security", Method: MethodJudgment, Required: true, Scope: "changed code",
		Goal:     "No security regression is introduced",
		Expected: "Input validation, secret handling and privilege boundaries unchanged or improved",
		Rubric:   "references/review-rubric.md security axis",
	},
	{
		ID: "crit-impact", Method: MethodJudgment, Required: true, Scope: "affected surface",
		Goal:     "Blast radius of the change is understood and bounded",
		Expected: "Callers and dependents of changed surfaces identified; no unexamined downstream behavior change",
		Rubric:   "references/review-rubric.md impact axis",
	},
	{
		ID: "crit-prove-it", Method: MethodJudgment, Required: true, Scope: "accepted artifacts",
		Goal:     "Claims are demonstrated, not asserted",
		Expected: "Fixes carry a reproducing test; behavior changes carry direct evidence from the reviewed artifacts",
		Rubric:   "references/review-rubric.md prove-it axis",
	},
}

// DefaultReviewCriteria returns a deep copy of the framework's COMPLETE default
// criteria (full meaning, not just ids).
func DefaultReviewCriteria() []Criterion {
	out := make([]Criterion, len(defaultReviewCriteria))
	copy(out, defaultReviewCriteria)
	return out
}

// defaultCriterion returns the shipped default criterion for one id, if any.
func defaultCriterion(id string) (Criterion, bool) {
	for _, c := range defaultReviewCriteria {
		if c.ID == id {
			return c, true
		}
	}
	return Criterion{}, false
}

// DefaultCriteriaDigest is the digest of the shipped default criteria set —
// the defaults_digest of a verification environment (a change to the shipped
// set is an environment change that blocks cross-run reuse).
func DefaultCriteriaDigest() string {
	b, err := json.Marshal(DefaultReviewCriteria())
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// WorkflowHasReviewPhase reports whether the workflow's fixed sequence embeds
// a review phase — the workflows the mandatory default review criteria and the
// terminal ⑤ debt bind to (controller QA-2: ops/release have none, mint no
// UNREVIEWED debt, and close through the normal post-game flow).
func WorkflowHasReviewPhase(workflow string) bool {
	seq, err := WorkflowPhases(workflow)
	if err != nil {
		return false
	}
	for _, id := range seq {
		if id == "review" {
			return true
		}
	}
	return false
}

// --- minted envelope (final-fix wave C1) ---------------------------------------------

// HashArtifactFile computes the hook-owned sha256 of one project-relative
// artifact path. Missing file → ("", error): an EXISTING artifact ref must
// hash real current bytes; a path that does not exist yet is planned scope,
// never a frozen artifact (the caller decides which).
func HashArtifactFile(rel string) (string, error) {
	if rel == "" {
		return "", errors.New("artifact path is empty")
	}
	b, err := os.ReadFile(filepath.Join(ProjectDir(), rel))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// VerifyArtifactFreshness re-hashes every ref's CURRENT bytes and reports the
// first drift. Used at acceptance, dispatch and conclusion: a review must
// never freeze, dispatch or conclude over artifacts that no longer match the
// pinned snapshot (drift demands an updated VISIBLE proposal, never a silent
// substitution).
func VerifyArtifactFreshness(refs []ArtifactRef) error {
	for _, a := range refs {
		if a.Digest == "" {
			return fmt.Errorf("artifact %q carries no pinned digest", a.Path)
		}
		got, err := HashArtifactFile(a.Path)
		if err != nil {
			return fmt.Errorf("artifact %q unreadable since the snapshot: %w", a.Path, err)
		}
		if got != a.Digest {
			return fmt.Errorf("artifact %q drifted since the snapshot (pinned %s, current %s) — an updated visible proposal is required, not a silent substitution", a.Path, a.Digest, got)
		}
	}
	return nil
}

// ObserveVerificationEnvironment captures ONLY observed environment facts:
// OS/arch/runtime from the executing hook, the shipped-defaults digest, and
// the manifest digest when a manifest is readable. Unknown properties listed
// by the frozen view are preserved and disable reuse — an observed
// environment never certifies isolation.
func ObserveVerificationEnvironment(frozen VerificationEnvironment) VerificationEnvironment {
	env := VerificationEnvironment{
		OS:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		Runtime:        "claude-code-hooks",
		Tier:           "framework",
		DefaultsDigest: DefaultCriteriaDigest(),
		Unknown:        append([]string(nil), frozen.Unknown...),
	}
	if m, err := LoadRuntimeManifest(); err == nil && m != nil {
		if b, err := json.Marshal(m); err == nil {
			sum := sha256.Sum256(b)
			env.ManifestDigest = hex.EncodeToString(sum[:])
		}
	}
	return env
}

// ExpectedCriteriaIDs computes the run's ACTIVE criterion id set: the frozen
// view's criteria ledger (full meaning, staged at submit — proposed criteria
// PLUS the framework defaults on review-phase workflows) minus the recorded
// user drops. Legacy state with an empty ledger falls back to the union of
// requirement criteria ids (the pre-final-wave derivation) so hand-assembled
// runs stay decidable.
func ExpectedCriteriaIDs(view FrozenView, dropped []CriteriaDrop) map[string]bool {
	expected := make(map[string]bool)
	if len(view.Criteria) > 0 {
		for _, c := range view.Criteria {
			expected[c.ID] = true
		}
	} else {
		for _, fr := range view.Requirements {
			for _, id := range fr.CriteriaIDs {
				expected[id] = true
			}
		}
	}
	for _, d := range dropped {
		delete(expected, d.ID)
	}
	return expected
}

// MintEnvelope builds the COMPLETE review envelope from the run's pinned
// acceptance snapshot — the only envelope authority (final-fix wave C1: the
// producer's registration carries identity only; every semantic byte comes
// from what the user's verified acceptance froze). Steps:
//
//   - identity checks (schema exactly "2", run id match, non-empty review id);
//   - the active criterion set is ExpectedCriteriaIDs(snapshot − drops); the
//     vacuous case (nothing accepted, nothing dropped) refuses — no review is
//     due over an empty view;
//   - criterion CONTENT comes from the frozen ledger, then the shipped
//     defaults, then — for a legacy id-only freeze — an honest judgment
//     criterion that names the id without inventing meaning. Never empty
//     meaning, never producer-supplied;
//   - artifacts come from the frozen artifact set; CURRENT bytes are re-hashed
//     and drift refuses the mint (freshness at dispatch);
//   - mechanical criteria must cite a check present in the registry — an
//     absent registry cannot waive mechanical validation — and each cited
//     definition digest is recomputed (a changed definition demands a new
//     acceptance, not a silent re-freeze);
//   - scope and environment come from the frozen view plus live observation;
//     unknown properties never support reuse.
//
// MintEnvelope is pure with respect to run state (it reads files for
// freshness but never mutates the run).
func MintEnvelope(run *RunState, identity Envelope, registry []CheckDef) (SemanticEnvelope, error) {
	if run == nil {
		return SemanticEnvelope{}, errors.New("MintEnvelope: no run")
	}
	switch {
	case identity.SchemaVersion != "2":
		return SemanticEnvelope{}, fmt.Errorf("MintEnvelope: schema_version must be exactly \"2\" (got %q)", identity.SchemaVersion)
	case identity.RunID != run.RunID:
		return SemanticEnvelope{}, fmt.Errorf("MintEnvelope: envelope run_id %q does not match run %q", identity.RunID, run.RunID)
	case identity.ReviewID == "":
		return SemanticEnvelope{}, errors.New("MintEnvelope: review_id is required")
	}
	frozen := run.AcceptanceView()
	active := ExpectedCriteriaIDs(frozen, run.CriteriaDrops)
	if len(active) == 0 && len(run.CriteriaDrops) == 0 {
		return SemanticEnvelope{}, errors.New("MintEnvelope: the frozen acceptance view is empty and nothing was dropped — nothing has been accepted, no review is due (spec §3)")
	}

	// Content resolution: ledger → shipped defaults → honest legacy fallback.
	ledger := make(map[string]Criterion, len(frozen.Criteria))
	for _, c := range frozen.Criteria {
		ledger[c.ID] = c
	}
	env := SemanticEnvelope{
		SchemaVersion: identity.SchemaVersion,
		RunID:         identity.RunID,
		ReviewID:      identity.ReviewID,
		Requirements:  frozen,
	}
	ids := make([]string, 0, len(active))
	for id := range active {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c, ok := ledger[id]
		if !ok {
			if d, isDefault := defaultCriterion(id); isDefault {
				c = d
			} else {
				// Legacy id-only freeze: the acceptance record is the id; give
				// the reviewer the honest judgment form rather than inventing
				// semantics the user never saw.
				c = Criterion{
					ID:       id,
					Method:   MethodJudgment,
					Required: true,
					Goal:     fmt.Sprintf("User-accepted criterion %q (no model-authored goal was staged; judge against the frozen artifacts)", id),
					Expected: "The accepted artifacts satisfy the criterion as recorded at acceptance",
					Scope:    "accepted artifacts",
				}
			}
		}
		if c.ID == "" {
			c.ID = id // belt: a ledger entry must never lose its key
		}
		if c.Method != MethodMechanical && c.Method != MethodJudgment {
			return SemanticEnvelope{}, fmt.Errorf("MintEnvelope: criterion %q method %q must be %s or %s", id, c.Method, MethodMechanical, MethodJudgment)
		}
		if c.Goal == "" && c.Expected == "" {
			return SemanticEnvelope{}, fmt.Errorf("MintEnvelope: criterion %q carries no goal and no expected meaning — an acceptance staged an empty criterion", id)
		}
		if c.Method == MethodMechanical && c.CheckID == "" {
			return SemanticEnvelope{}, fmt.Errorf("MintEnvelope: mechanical criterion %q must reference a registered check_id", id)
		}
		env.ExitCriteria = append(env.ExitCriteria, c)
	}

	// Artifacts: the frozen set (freezeAcceptance maintains it), freshness at
	// dispatch. An empty set is legal only when nothing existing was accepted.
	env.ArtifactSet = append([]ArtifactRef(nil), frozen.ArtifactSet...)
	if err := VerifyArtifactFreshness(env.ArtifactSet); err != nil {
		return SemanticEnvelope{}, fmt.Errorf("MintEnvelope: %v", err)
	}

	// Check definitions: every cited check must be REGISTERED (absent registry
	// cannot waive mechanical validation) and digest-frozen at the CURRENT
	// recomputed definition.
	defs := make(map[string]CheckDef, len(registry))
	for _, d := range registry {
		defs[d.CheckID] = d
	}
	cited := map[string]bool{}
	for _, c := range env.ExitCriteria {
		if c.Method != MethodMechanical {
			continue
		}
		def, ok := defs[c.CheckID]
		if !ok {
			return SemanticEnvelope{}, fmt.Errorf("MintEnvelope: criterion %q cites unregistered check %q — no registry entry can waive mechanical validation", c.ID, c.CheckID)
		}
		if cited[c.CheckID] {
			continue
		}
		cited[c.CheckID] = true
		frozenRef := c.CheckID + "@" + CheckDefDigest(def)
		env.CheckDefs = append(env.CheckDefs, def)
		env.VerificationScope.CheckIDs = append(env.VerificationScope.CheckIDs, frozenRef)
	}
	// Frozen scope from the view; readable roots default to the project dir.
	env.VerificationScope.Readable = append([]string(nil), frozen.CheckScope.Readable...)
	if len(env.VerificationScope.Readable) == 0 {
		env.VerificationScope.Readable = []string{ProjectDir()}
	}
	env.VerificationScope.OutputSchema = frozen.CheckScope.OutputSchema
	if env.VerificationScope.OutputSchema == "" {
		env.VerificationScope.OutputSchema = "review-result/v1"
	}
	env.Environment = ObserveVerificationEnvironment(frozen.Environment)
	return env, nil
}

// ValidateEnvelope checks a MINTED envelope for internal consistency against
// the frozen acceptance view (spec §3 冻结验收视图). Since the final-fix wave
// no producer-supplied envelope exists on any wire; this is the belt for
// state assembled by other hands and for the reducer's own mint (a mint that
// drifts from the snapshot fails here, never silently):
//
//   - the exit-criteria ID set MUST equal (frozen ledger − drops), BOTH
//     directions: a missing id is a trim, an unknown id an invention;
//   - ids unique; methods mechanical|judgment; mechanical citations reference
//     a registered check whose RECOMPUTED digest matches the frozen
//     check_id@digest scope entry (a stale label cannot hide a changed
//     definition);
//   - criterion meaning is present (goal or expected) — a shape-only freeze
//     is refused;
//   - artifacts carry pinned digests.
func ValidateEnvelope(env SemanticEnvelope, frozen FrozenView, dropped []CriteriaDrop, registry []CheckDef) error {
	resolved := ResolveFrozenView(frozen)
	expected := ExpectedCriteriaIDs(resolved, dropped)
	if len(expected) == 0 && len(dropped) == 0 {
		return errors.New("ValidateEnvelope: the frozen acceptance view is empty and nothing was dropped — nothing has been accepted yet, no review is due (spec §3)")
	}
	got := make(map[string]bool, len(env.ExitCriteria))
	for _, c := range env.ExitCriteria {
		if c.ID == "" {
			return errors.New("ValidateEnvelope: exit criterion with empty id")
		}
		if got[c.ID] {
			return fmt.Errorf("ValidateEnvelope: duplicate exit criterion %q (criterion ids are unique, spec §3)", c.ID)
		}
		got[c.ID] = true
		switch c.Method {
		case MethodMechanical, MethodJudgment:
		default:
			return fmt.Errorf("ValidateEnvelope: criterion %q method %q must be %s or %s", c.ID, c.Method, MethodMechanical, MethodJudgment)
		}
		if c.Goal == "" && c.Expected == "" {
			return fmt.Errorf("ValidateEnvelope: criterion %q carries no criterion meaning (goal/expected) — a shape-only freeze is not an acceptance (spec §3)", c.ID)
		}
		if c.Method == MethodMechanical && c.CheckID == "" {
			return fmt.Errorf("ValidateEnvelope: mechanical criterion %q must reference a registered check_id", c.ID)
		}
	}
	var missing, invented []string
	for id := range expected {
		if !got[id] {
			missing = append(missing, id)
		}
	}
	for id := range got {
		if !expected[id] {
			invented = append(invented, id)
		}
	}
	sort.Strings(missing)
	sort.Strings(invented)
	if len(missing) > 0 {
		return fmt.Errorf("ValidateEnvelope: envelope omits frozen criteria %s — the frozen acceptance view cannot be trimmed (spec §3)", strings.Join(missing, ", "))
	}
	if len(invented) > 0 {
		return fmt.Errorf("ValidateEnvelope: envelope criteria %s are not in the frozen acceptance view — invented criteria are rejected (spec §3)", strings.Join(invented, ", "))
	}
	for _, a := range env.ArtifactSet {
		if a.Path == "" || a.Digest == "" {
			return fmt.Errorf("ValidateEnvelope: artifact ref %+v carries no pinned digest — an envelope reviews exact versions", a)
		}
	}
	if len(registry) == 0 {
		return nil
	}
	defs := make(map[string]CheckDef, len(registry))
	for _, c := range registry {
		defs[c.CheckID] = c
	}
	frozenRefs := make(map[string]string, len(env.VerificationScope.CheckIDs))
	for _, entry := range env.VerificationScope.CheckIDs {
		id, digest, err := parseScopeCheckRef(entry)
		if err != nil {
			return fmt.Errorf("ValidateEnvelope: %v", err)
		}
		frozenRefs[id] = digest
	}
	for _, c := range env.ExitCriteria {
		if c.Method != MethodMechanical {
			continue
		}
		def, ok := defs[c.CheckID]
		if !ok {
			return fmt.Errorf("ValidateEnvelope: criterion %q references unregistered check %q", c.ID, c.CheckID)
		}
		want, ok := frozenRefs[c.CheckID]
		if !ok {
			return fmt.Errorf("ValidateEnvelope: criterion %q freezes no digest for check %q — verification_scope.check_ids must carry check_id@digest for every mechanical check (spec §3)", c.ID, c.CheckID)
		}
		if want == "" {
			return fmt.Errorf("ValidateEnvelope: bare check id %q carries no frozen digest — the check-definition freeze is mandatory (spec §3)", c.CheckID)
		}
		if want != CheckDefDigest(def) {
			return fmt.Errorf("ValidateEnvelope: check %q definition changed since the envelope froze digest %s (now %s) — the envelope must version-upgrade", c.CheckID, want, CheckDefDigest(def))
		}
	}
	return nil
}

// --- evidence ----------------------------------------------------------------------

// ReconcileEvidence checks every claimed evidence id against the reviewer's
// own subagent transcript (spec §3 证据对账). A claimed id proves something
// only when it is a SUCCESSFUL OBSERVED tool call of the CHILD: a tool_use
// record paired with a tool_result of the SAME call that is not an error
// (final-fix wave C1c — a failed Read proves nothing), issued by an
// OBSERVATION tool (Read/Grep/Glob/Bash — the handback echo and any control
// or question call are not evidence), and carrying no criterion-anchor claims
// of its own. An empty claim list is refused — vacuous success would launder
// rubber-stamps. A claimed-but-absent id is a forged-id protocol error.
func ReconcileEvidence(claimed []string, transcriptPath string) error {
	if len(claimed) == 0 {
		return errors.New("evidence reconciliation: no evidence ids claimed — nothing to reconcile")
	}
	b, err := os.ReadFile(transcriptPath)
	if err != nil {
		return fmt.Errorf("evidence reconciliation: read transcript: %w", err)
	}
	pairs, err := ChildEvidencePairs(string(b))
	if err != nil {
		return fmt.Errorf("evidence reconciliation: %v", err)
	}
	for _, id := range claimed {
		if id == "" {
			return errors.New("evidence reconciliation: empty evidence id")
		}
		if _, ok := pairs[id]; !ok {
			if containsString(lineToolUseIDsList(string(b)), id) {
				return fmt.Errorf("evidence reconciliation: evidence id %q exists in %s but is not a successful observed child tool call (failed result, handback echo or non-observation tool) — it proves nothing (spec §3)", id, transcriptPath)
			}
			return fmt.Errorf("evidence reconciliation: evidence id %q is not a tool_use record in %s — forged evidence id (spec §3)", id, transcriptPath)
		}
	}
	return nil
}

// observationTools are the child tools whose successful calls can carry
// review evidence. SubagentHandback is deliberately absent: the report
// channel cannot be its own proof.
var observationTools = map[string]bool{
	"Read": true, "Grep": true, "Glob": true, "Bash": true, "ToolSearch": true,
}

// ChildEvidencePairs parses one child transcript (JSONL) and returns the
// tool_use ids that have a SUCCESSFUL observed tool_result: the tool_use
// block names an observation tool and the paired result exists with no
// is_error. Everything else — failed reads, the handback, unpaired calls,
// foreign control tools — proves nothing.
func ChildEvidencePairs(transcript string) (map[string]EvidenceAnchor, error) {
	type useBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	type resultBlock struct {
		Type      string          `json:"type"`
		ToolUseID string          `json:"tool_use_id"`
		IsError   bool            `json:"is_error"`
		Content   json.RawMessage `json:"content"`
	}
	uses := make(map[string]string) // id → tool name
	ok := make(map[string]bool)      // id → successful result seen
	for _, line := range strings.Split(transcript, "\n") {
		if !strings.Contains(line, "tool_use") && !strings.Contains(line, "tool_result") {
			continue
		}
		var entry struct {
			Message json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil || len(entry.Message) == 0 {
			continue
		}
		var msg struct {
			Content []json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(entry.Message, &msg); err != nil {
			// content may be a plain string (user turn echo) — nothing to mine
			continue
		}
		for _, raw := range msg.Content {
			var kind struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(raw, &kind) != nil {
				continue
			}
			switch kind.Type {
			case "tool_use":
				var u useBlock
				if json.Unmarshal(raw, &u) == nil && u.ID != "" {
					uses[u.ID] = u.Name
				}
			case "tool_result":
				var rb resultBlock
				if json.Unmarshal(raw, &rb) == nil && rb.ToolUseID != "" {
					ok[rb.ToolUseID] = !rb.IsError
				}
			}
		}
	}
	out := make(map[string]EvidenceAnchor)
	for id, name := range uses {
		if !observationTools[name] {
			continue
		}
		if success, paired := ok[id]; paired && success {
			out[id] = EvidenceAnchor{ToolUseID: id}
		}
	}
	return out, nil
}

// lineToolUseIDs pulls the ids of every tool_use content block from one
// transcript (assistant) line. A line that is not JSON, or has no such block,
// yields nothing.
func lineToolUseIDs(line string) []string {
	var entry struct {
		Message struct {
			Content []struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return nil
	}
	var ids []string
	for _, blk := range entry.Message.Content {
		if blk.Type == "tool_use" && blk.ID != "" {
			ids = append(ids, blk.ID)
		}
	}
	return ids
}

func lineToolUseIDsList(transcript string) []string {
	var ids []string
	for _, line := range strings.Split(transcript, "\n") {
		ids = append(ids, lineToolUseIDs(line)...)
	}
	return ids
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ValidateEvidence enforces the spec §3 空证据 rule: a PASS-track result
// (run_status COMPLETED + verdict PASS) with empty evidence_tool_call_ids is
// a rubber-stamp protocol error (未检查/无工具/不能判 ≠ PASS). BLOCKED and
// NEEDS-FIX results are exempt — BLOCKED reports a reason code instead of a
// quality claim, and NEEDS-FIX findings carry path:line anchors.
func ValidateEvidence(res ReviewResult) error {
	if res.RunStatus == ReviewRunCompleted && res.Verdict == VerdictPass && len(res.EvidenceToolCallIDs) == 0 {
		return errors.New("PASS verdict requires non-empty evidence_tool_call_ids — empty evidence is a rubber-stamp (spec §3)")
	}
	return nil
}

// --- reuse / void ---------------------------------------------------------------------

// ArtifactSetDigest is the canonical digest of an envelope's artifact set:
// sha256 over the artifact refs sorted by the full (path, digest, kind) key,
// each contributing path/digest/kind. The full key keeps the digest
// deterministic for duplicate paths; the reducer mints the set and recomputes
// this digest, and a reviewer's reported artifact_digest is compared against
// it at conclusion (a drifted or lying digest is a protocol error).
func ArtifactSetDigest(env SemanticEnvelope) string {
	refs := append([]ArtifactRef(nil), env.ArtifactSet...)
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Path != refs[j].Path {
			return refs[i].Path < refs[j].Path
		}
		if refs[i].Digest != refs[j].Digest {
			return refs[i].Digest < refs[j].Digest
		}
		return refs[i].Kind < refs[j].Kind
	})
	h := sha256.New()
	for _, a := range refs {
		h.Write([]byte(a.Path))
		h.Write([]byte{0})
		h.Write([]byte(a.Digest))
		h.Write([]byte{0})
		h.Write([]byte(a.Kind))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ReusableReview reports whether a concluded review can be reused for this
// minted envelope (spec §3 同版本验收复用): a REVIEWED record whose stored CONTENT
// digest (envelope content minus review_id — the spec triple: artifacts +
// frozen criteria + verification environment) AND artifact digest both match.
// Reuse means no re-review, no review-of-review, no recursion — including
// for a later ⑤ request that mints a fresh review_id over identical content.
// A content-identical record whose artifact digest drifted (artifacts
// changed around the review) is NOT reusable and its prior verdict is VOID —
// callers must not cite it; a fresh review is required (产物 digest 变更 →
// verdict 作废重审). Pure predicate: it never mutates the run.
func ReusableReview(run *RunState, env SemanticEnvelope) (*ReviewRecord, bool) {
	if run == nil {
		return nil, false
	}
	wantEnv := envelopeContentDigest(env)
	wantArt := ArtifactSetDigest(env)
	for i := range run.Reviews {
		rv := &run.Reviews[i]
		if rv.Status != ReviewReviewed {
			continue
		}
		if rv.EnvelopeDigest == wantEnv && rv.ArtifactDigest == wantArt {
			return rv, true
		}
	}
	return nil, false
}

// --- exact reviewer launch capsule (adjudication §2) ---------------------------------

// ReviewCapsule is the ONLY legal reviewer Agent prompt: a single compact
// JSON object serialized by shared Go — full stored envelope, attempt id and
// one-shot token, expected content/artifact digests. No producer prose, no
// history, no verdict, no budget. The gate compares the WHOLE normalized
// prompt against these exact bytes; any unknown field or prefix/suffix
// rejects — "the reviewer was told to ignore it" is not a defense.
type ReviewCapsule struct {
	SchemaVersion   string           `json:"schema_version"` // summoner-review-capsule/1
	RunID           string           `json:"run_id"`
	ReviewID        string           `json:"review_id"`
	AttemptID       string           `json:"attempt_id"`
	Token           string           `json:"token"`
	Envelope        SemanticEnvelope `json:"envelope"`
	EnvelopeDigest  string           `json:"envelope_digest"`
	ArtifactDigest  string           `json:"artifact_digest"`
	TranscriptHint  string           `json:"transcript_hint,omitempty"`
	InstructionsRef string           `json:"instructions_ref,omitempty"`
}

// CapsuleSchema is the fixed capsule schema tag.
const CapsuleSchema = "summoner-review-capsule/1"

// VerifyChildOwnership verifies the QA-1 ownership correlation (final-fix
// wave, adjudication §3): the child transcript's FIRST record must be the
// platform-origin INITIAL task record — a type:"user" entry whose
// message.content is a PLAIN STRING byte-identical (under the display
// normalization) to the EXACT launch capsule of the review's ACTIVE attempt,
// the capsule the gate admitted at dispatch (bound to one admitted parent
// Agent tool_use id on the append-only attempt receipt).
//
// The correlation is checked BEFORE both verdict acceptance and evidence
// reconciliation: an unowned transcript can neither conclude nor fail the
// review on its own words. Unproven correlation — no admitted dispatch
// receipt (a PREPARED attempt nobody dispatched), a missing/malformed
// initial record, a non-string content, or any byte mismatch (a near-miss
// capsule included) — returns the quarantine reason; ownership is NEVER
// inferred from tokens echoed inside the transcript, ordering, timestamps or
// agent-type guesses.
func VerifyChildOwnership(run *RunState, rv *ReviewRecord, transcriptPath string) error {
	att := rv.ActiveAttempt(rv.ActiveAttemptID)
	if att == nil || att.State != "RUNNING" || att.ParentToolUseID == "" {
		return fmt.Errorf("child ownership: review %s attempt %s has no admitted dispatch receipt (never dispatched, or receipt unbound) — the transcript has no owner and ownership is never inferred (QA-1)", rv.ReviewID, rv.ActiveAttemptID)
	}
	capsule, ok := BuildReviewCapsule(run, rv.ReviewID, rv.ActiveAttemptID)
	if !ok {
		return fmt.Errorf("child ownership: no launch capsule for review %s attempt %s", rv.ReviewID, rv.ActiveAttemptID)
	}
	want, err := capsule.Bytes()
	if err != nil {
		return fmt.Errorf("child ownership: capsule bytes: %v", err)
	}
	initial, err := initialTaskRecord(transcriptPath)
	if err != nil {
		return fmt.Errorf("child ownership: %v", err)
	}
	if NormalizePrompt(initial) != NormalizePrompt(string(want)) {
		return errors.New("child ownership: the transcript's initial task record does not equal the admitted launch capsule — the child is not the dispatched reviewer; quarantined, never inferred ownership (QA-1)")
	}
	return nil
}

// initialTaskRecord extracts the FIRST record's initial task content from a
// child transcript (JSONL): a type:"user" entry whose message.content is a
// PLAIN STRING — the platform's initial launch record. Anything else (an
// assistant-first transcript, a content block array, a non-user first
// record, an empty/unreadable file) is not a platform-origin initial record.
func initialTaskRecord(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read transcript: %w", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue // tolerate leading blank lines
		}
		var entry struct {
			Type    string          `json:"type"`
			Message json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(line, &entry); err != nil || len(entry.Message) == 0 {
			return "", errors.New("the first transcript record is not a platform initial task record (unparseable)")
		}
		if entry.Type != "user" {
			return "", fmt.Errorf("the first transcript record is type %q, not the platform initial task record (type user)", entry.Type)
		}
		var msg struct {
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(entry.Message, &msg); err != nil || len(msg.Content) == 0 {
			return "", errors.New("the initial task record has no content")
		}
		var s string
		if json.Unmarshal(msg.Content, &s) != nil {
			return "", errors.New("the initial task content is not a plain string (the platform launch record never carries structured blocks)")
		}
		return s, nil
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read transcript: %v", err)
	}
	return "", errors.New("the transcript is empty — no initial task record")
}

// BuildReviewCapsule assembles the exact launch capsule for one attempt of a
// stored review record. The envelope and digests come from the RECORD (what
// the reducer minted), never from the dispatching model's text.
func BuildReviewCapsule(run *RunState, reviewID, attemptID string) (ReviewCapsule, bool) {
	if run == nil {
		return ReviewCapsule{}, false
	}
	rv := run.findReview(reviewID)
	if rv == nil || attemptID == "" {
		return ReviewCapsule{}, false
	}
	for i := range rv.Attempts {
		if rv.Attempts[i].AttemptID == attemptID {
			capsule := ReviewCapsule{
				SchemaVersion:   CapsuleSchema,
				RunID:           run.RunID,
				ReviewID:        rv.ReviewID,
				AttemptID:       attemptID,
				Token:           rv.Attempts[i].Token,
				Envelope:        rv.Envelope,
				EnvelopeDigest:  rv.EnvelopeDigest,
				ArtifactDigest:  rv.ArtifactDigest,
				TranscriptHint:  "Return the structured result through SubagentHandback; the reducer validates every field against this envelope.",
				InstructionsRef: "agents/reviewer.md",
			}
			return capsule, true
		}
	}
	return ReviewCapsule{}, false
}

// Bytes is the exact wire form: one compact JSON line (encoding/json field
// order, no HTML escaping changes) that BOTH the status view serves read-only
// and the Agent gate compares against.
func (c ReviewCapsule) Bytes() ([]byte, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// NormalizePrompt applies the display normalization contract to a prompt:
// CRLF→LF and at most one trailing LF, nothing more. The whole-message
// comparison runs on this form; there is no token or containment matching.
func NormalizePrompt(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimSuffix(s, "\n")
}
