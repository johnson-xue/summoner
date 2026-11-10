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
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// --- decode -----------------------------------------------------------------------

// DecodeEnvelope decodes one review envelope with a strict field allow-list:
// an unknown field anywhere — top level or nested — is a parse error, never a
// silent drop (spec §3 严格 allow-list 拒未知字段; the envelope must not carry
// what the contract never names). The platform correlation keys
// schema_version/run_id/review_id are required non-empty and artifact_set is
// required non-empty (an envelope reviews SOMETHING).
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
	case env.RunID == "":
		return Envelope{}, errors.New("decode envelope: run_id is required")
	case env.ReviewID == "":
		return Envelope{}, errors.New("decode envelope: review_id is required")
	case len(env.ArtifactSet) == 0:
		return Envelope{}, errors.New("decode envelope: artifact_set must be non-empty")
	}
	return env, nil
}

// --- frozen acceptance view ---------------------------------------------------------

// ResolveFrozenView collapses a serialized frozen view per requirement ID,
// LAST-ENTRY-WINS (mirrors the M11 last-visit-per-ID phase semantics: a
// re-accepted phase's later freeze supersedes its earlier one). The writer
// (freezeAcceptance) already replaces per ID; this read-side rule is the belt
// for state files assembled by other hands.
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
// "check_id" (no frozen digest — the current registry definition is accepted)
// or "check_id@digest" (the definition digest frozen at envelope creation).
func parseScopeCheckRef(entry string) (id, digest string, err error) {
	if entry == "" {
		return "", "", errors.New("empty verification_scope.check_ids entry")
	}
	parts := strings.SplitN(entry, "@", 2)
	if parts[0] == "" {
		return "", "", fmt.Errorf("malformed check_ids entry %q (no check id before @)", entry)
	}
	if len(parts) == 1 {
		return parts[0], "", nil // bare check id — no frozen digest
	}
	return parts[0], parts[1], nil
}

// --- envelope validation ----------------------------------------------------------

// ValidateEnvelope checks an envelope against the run's frozen acceptance
// view (spec §3 冻结验收视图 — producers cannot trim it):
//
//   - the envelope's exit-criteria ID set MUST equal (frozen set − dropped
//     IDs), BOTH directions rejected: a missing id is a producer trim, an
//     unknown id is an invented criterion (a drop whose criterion later left
//     the frozen view is inert — the ledger is a historical record);
//   - criterion ids are unique and methods are mechanical|judgment; a
//     mechanical criterion must reference a registered check_id;
//   - every referenced check must exist in the registry (when one is given),
//     and any definition digest frozen in verification_scope.check_ids as
//     "check_id@digest" must still match the registry's recomputed digest —
//     a mismatch rejects the envelope: it must version-upgrade (信封创建时
//     冻结所用 check 定义摘要；执行时复核摘要，变更即信封版本升级).
//
// A nil/empty registry skips the registry checks (no registry to check
// against); the set-equality checks always run.
func ValidateEnvelope(env Envelope, frozen FrozenView, dropped []CriteriaDrop, registry []CheckDef) error {
	resolved := ResolveFrozenView(frozen)
	expected := make(map[string]bool)
	for _, fr := range resolved.Requirements {
		for _, id := range fr.CriteriaIDs {
			expected[id] = true
		}
	}
	// The drop ledger is a per-decision historical record (逐 id、逐次决定): a
	// drop whose criterion later left the frozen view (re-acceptance with new
	// criteria) is inert — the spec arithmetic is simply frozen − drops.
	for _, d := range dropped {
		delete(expected, d.ID)
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
		return fmt.Errorf("ValidateEnvelope: envelope omits frozen criteria %s — the producer cannot trim the frozen acceptance view (spec §3)", strings.Join(missing, ", "))
	}
	if len(invented) > 0 {
		return fmt.Errorf("ValidateEnvelope: envelope criteria %s are not in the frozen acceptance view — invented criteria are rejected (spec §3)", strings.Join(invented, ", "))
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
		if want := frozenRefs[c.CheckID]; want != "" && want != CheckDefDigest(def) {
			return fmt.Errorf("ValidateEnvelope: check %q definition changed since the envelope froze digest %s (now %s) — the envelope must version-upgrade", c.CheckID, want, CheckDefDigest(def))
		}
	}
	return nil
}

// --- evidence ----------------------------------------------------------------------

// ReconcileEvidence checks every claimed evidence id against the reviewer's
// own subagent transcript: each id must appear as a tool_use record id in the
// JSONL (spec §3 证据对账: evidence id 与子 transcript 中真实 tool_use 记录对账;
// a claimed-but-absent id is a forged-id protocol error). An empty claim list
// is refused — vacuous success would launder rubber-stamps. Standalone minimal
// scan by design: subagentstop's report parser stays a separate concern.
func ReconcileEvidence(claimed []string, transcriptPath string) error {
	if len(claimed) == 0 {
		return errors.New("evidence reconciliation: no evidence ids claimed — nothing to reconcile")
	}
	b, err := os.ReadFile(transcriptPath)
	if err != nil {
		return fmt.Errorf("evidence reconciliation: read transcript: %w", err)
	}
	seen := make(map[string]bool)
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(line, `"tool_use"`) {
			continue // cheap pre-filter: tool_use lines are the minority
		}
		for _, id := range lineToolUseIDs(line) {
			seen[id] = true
		}
	}
	for _, id := range claimed {
		if id == "" {
			return errors.New("evidence reconciliation: empty evidence id")
		}
		if !seen[id] {
			return fmt.Errorf("evidence reconciliation: evidence id %q is not a tool_use record in %s — forged evidence id (spec §3)", id, transcriptPath)
		}
	}
	return nil
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
// sha256 over the artifact refs sorted by path, each contributing
// path/digest/kind. Producers, reviewers and the reducer share this one
// definition, so the digest a reviewer reports (artifact_digest) and the
// digest a later envelope freezes are comparable.
func ArtifactSetDigest(env Envelope) string {
	refs := append([]ArtifactRef(nil), env.ArtifactSet...)
	sort.Slice(refs, func(i, j int) bool { return refs[i].Path < refs[j].Path })
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
// envelope (spec §3 同版本验收复用): a REVIEWED record whose envelope digest AND
// artifact digest both match — the same frozen criteria and verification
// environment reviewing the same artifact versions. Reuse means no re-review,
// no review-of-review, no recursion. A same-envelope record whose artifact
// digest drifted (artifacts changed around the review) is NOT reusable and
// its prior verdict is VOID — callers must not cite it; a fresh review is
// required (产物 digest 变更 → verdict 作废重审). Pure predicate: it never
// mutates the run.
func ReusableReview(run *RunState, env Envelope) (*ReviewRecord, bool) {
	if run == nil {
		return nil, false
	}
	wantEnv := envelopeDigest(env)
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
