package shared

// reducer.go — single-loop run-state machine v2 (spec
// docs/superpowers/specs/2026-11-09-single-loop-checkpoint-design.md §1/§3/§4/§5).
//
// The reducer owns run status, phase/checkpoint lifecycle, review lifecycle,
// requirement-change loop, recall history, OUTCOME_UNKNOWN recovery and
// budgets. It validates every transition against the legality matrix before
// mutating; illegal transitions return an error and never persist (the hook
// binary decides the exit-2 wording). Persisting transitions run through the
// commit pattern (see commit below): they mutate a deep-copy snapshot and
// swap it in only after the Save succeeds, so a failed Save never leaves
// half-applied state behind. The void methods (CountToolCall,
// MarkOutcomeUnknown) mutate in memory only — callers Save.
//
// Nothing here interprets DAGs, issues instructions or does domain work.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrRunNotFound is returned by LoadRun when no state file matches the run id.
var ErrRunNotFound = errors.New("run state not found")

// --- persistence ----------------------------------------------------------------

// runStateDirOverride redirects v2 run-state into a test directory so the
// suite stays hermetic (same pattern as internal/graph.stateDirOverride).
// Empty in production.
var runStateDirOverride string

func runStateRoot() (string, error) {
	if runStateDirOverride != "" {
		return runStateDirOverride, nil
	}
	if d := os.Getenv("SUMMONER_STATE_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".summoner", "state-v2"), nil
}

// validID reports whether s is a safe single path segment (run/session ids go
// straight into file paths).
func validID(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-' || c == '_' || c == '.':
		default:
			return false
		}
	}
	return true
}

func runStatePath(sessionID, runID string) (string, error) {
	if !validID(runID) {
		return "", fmt.Errorf("invalid run id %q", runID)
	}
	if !validID(sessionID) {
		return "", fmt.Errorf("invalid session id %q", sessionID)
	}
	root, err := runStateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ProjectKey(), sessionID, runID+".json"), nil
}

// Save persists the run atomically: temp file in the same directory, then
// rename. Callers may invoke it directly; transitions commit through it.
func (r *RunState) Save() error {
	p, err := runStatePath(r.SessionID, r.RunID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	r.UpdatedAt = nowUTC()
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".runstate-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, p)
}

// validateRunState is the load-time integrity gate (task-2.5 M8): schema
// version, identity fields, status enums and phase→checkpoint referential
// integrity. fileRunID is the run id the file was found under ("" skips the
// cross-check); any violation is a corrupt-state error — a damaged state file
// must never load as a plausible run.
func validateRunState(r *RunState, fileRunID string) error {
	if r.SchemaVersion != RunStateSchemaVersion {
		return fmt.Errorf("schema_version %q is not %q (old or foreign state file)", r.SchemaVersion, RunStateSchemaVersion)
	}
	if r.RunID == "" {
		return fmt.Errorf("run_id is empty")
	}
	if fileRunID != "" && r.RunID != fileRunID {
		return fmt.Errorf("run_id %q does not match its state file %q", r.RunID, fileRunID)
	}
	if r.SessionID == "" {
		return fmt.Errorf("session_id is empty")
	}
	if r.Workflow == "" {
		return fmt.Errorf("workflow is empty")
	}
	switch r.Status {
	case StatusActive, StatusWaitUser, StatusReviewPending, StatusOutcomeUnknown, StatusPostGamePending, StatusClosed:
	default:
		return fmt.Errorf("status %q is not a run status", r.Status)
	}
	switch r.RecoveryPrior {
	case "", StatusActive, StatusWaitUser, StatusReviewPending, StatusOutcomeUnknown, StatusPostGamePending, StatusClosed:
	default:
		return fmt.Errorf("recovery_prior %q is not a run status", r.RecoveryPrior)
	}
	cpIDs := make(map[string]bool, len(r.Checkpoints))
	for i := range r.Checkpoints {
		cpIDs[r.Checkpoints[i].ID] = true
	}
	for i := range r.Phases {
		if id := r.Phases[i].CheckpointID; id != "" && !cpIDs[id] {
			return fmt.Errorf("phase %s references unknown checkpoint %s", r.Phases[i].ID, id)
		}
	}
	return nil
}

// LoadRun loads a run by id. The session that owns it is resolved by a
// single-match search under {root}/{project_key}/*/{run_id}.json.
func LoadRun(runID string) (*RunState, error) {
	if !validID(runID) {
		return nil, fmt.Errorf("invalid run id %q", runID)
	}
	root, err := runStateRoot()
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(filepath.Join(root, ProjectKey(), "*", runID+".json"))
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("load run %s: %w", runID, ErrRunNotFound)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("load run %s: ambiguous — %d state files match", runID, len(matches))
	}
	b, err := os.ReadFile(matches[0])
	if err != nil {
		return nil, err
	}
	var r RunState
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("load run %s: %w", runID, err)
	}
	if err := validateRunState(&r, runID); err != nil {
		return nil, fmt.Errorf("load run %s: corrupt state file: %w", runID, err)
	}
	return &r, nil
}

// SessionRuns loads every run state recorded for one session, ordered by run
// id for deterministic resolution. The PreToolUse gate resolves "the session's
// live runs" through this helper. Only a missing session directory means "no
// runs yet": any other read error — an unreadable directory, an unparseable
// or corrupt run file — is an error (fail-closed), because corrupted state
// must never silently shrink (or empty) the set of runs a gate could enforce.
func SessionRuns(sessionID string) ([]*RunState, error) {
	if !validID(sessionID) {
		return nil, fmt.Errorf("session runs: invalid session id %q", sessionID)
	}
	root, err := runStateRoot()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, ProjectKey(), sessionID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no runs recorded for this session yet
		}
		return nil, fmt.Errorf("session runs: reading %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if e.IsDir() {
			// A run-state path that became a directory is detectable
			// corruption — erroring beats silently shrinking the run set
			// (which the gate would read as "zero live runs").
			return nil, fmt.Errorf("session runs: %s is a directory — corrupted state, failing closed", filepath.Join(dir, e.Name()))
		}
		names = append(names, e.Name())
	}
	sort.Strings(names) // deterministic order for ambiguity reporting
	runs := make([]*RunState, 0, len(names))
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		var r RunState
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, fmt.Errorf("session runs: %s is unparseable: %w", name, err)
		}
		if err := validateRunState(&r, strings.TrimSuffix(name, ".json")); err != nil {
			return nil, fmt.Errorf("session runs: %s is corrupt: %w", name, err)
		}
		runs = append(runs, &r)
	}
	return runs, nil
}

// --- atomic transitions (task-2.5 M9) ---------------------------------------------

// deepCopy returns a value-independent copy of the run state via its own JSON
// schema — every field is JSON-round-trippable, so this is a true deep copy.
func (r *RunState) deepCopy() (RunState, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return RunState{}, fmt.Errorf("commit: snapshot: %w", err)
	}
	var c RunState
	if err := json.Unmarshal(b, &c); err != nil {
		return RunState{}, fmt.Errorf("commit: snapshot: %w", err)
	}
	return c, nil
}

// commit is THE transition wrapper (task-2.5 M9 + T3 handoff 14b). It deep-
// copies the state, runs mutate against the snapshot, persists the snapshot,
// and only then swaps it into *r. A mutation error or a Save failure leaves
// *r exactly as it was — memory never runs ahead of disk, so a retry after
// repair re-executes the real transition instead of mis-reporting idempotent
// success.
//
// CALLER CONTRACT (14b): although *r stays self-consistent on a Save failure,
// a run whose Save failed is POISONED for decision-making — disk and memory
// may have diverged in ways the caller cannot see (another hook may have
// persisted a different transition in between). After any error that carries
// a failed Save, the caller MUST reload with LoadRun before issuing further
// transitions on this object. No new API is needed — the contract is this
// comment.
func (r *RunState) commit(mutate func(*RunState) error) error {
	snap, err := r.deepCopy()
	if err != nil {
		return err
	}
	if err := mutate(&snap); err != nil {
		return err
	}
	if err := snap.Save(); err != nil {
		return err
	}
	*r = snap
	return nil
}

// --- run lifecycle ---------------------------------------------------------------

// StartRun creates and persists a new run in ACTIVE. Budget limits are
// confirmed at run start (spec §5); zero-value limits mean unlimited. An
// existing run id is refused — resume goes through LoadRun.
func StartRun(runID, sessionID, workflow string, limits BudgetLimits) (*RunState, error) {
	if !validID(runID) {
		return nil, fmt.Errorf("start run: invalid run id %q", runID)
	}
	if !validID(sessionID) {
		return nil, fmt.Errorf("start run: invalid session id %q", sessionID)
	}
	// F2 (review round 1): validateRunState rejects an empty workflow at load
	// time, so StartRun must never write such a file — it would brick the
	// session's next SessionRuns.
	if workflow == "" {
		return nil, errors.New("start run: workflow is required")
	}
	if _, err := LoadRun(runID); err == nil {
		return nil, fmt.Errorf("start run %s: already exists (resume via LoadRun; refusing to overwrite)", runID)
	} else if !errors.Is(err, ErrRunNotFound) {
		return nil, err
	}
	r := &RunState{
		SchemaVersion:       RunStateSchemaVersion,
		RunID:               runID,
		SessionID:           sessionID,
		Workflow:            workflow,
		Status:              StatusActive,
		RequirementsVersion: 1,
		Limits:              limits,
		StartedAt:           nowUTC(),
	}
	// Persisted through commit like every mutator (M9): on failure the run
	// simply does not exist yet.
	if err := r.commit(func(*RunState) error { return nil }); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *RunState) requireStatus(want RunStatus, op string) error {
	if r.Status != want {
		return fmt.Errorf("%s: illegal status %s (want %s)", op, r.Status, want)
	}
	return nil
}

// CurrentCheckpoint returns the open (undecided, unsuperseded) checkpoint —
// the newest checkpoint still owed. Decided and superseded checkpoints from
// the tail are skipped: a superseded checkpoint is replaced by its re-rendered
// successor (M2), and a checkpoint below a decided recovery checkpoint
// legitimately resurfaces (B3/B4 — an episode must not bypass it).
func (r *RunState) CurrentCheckpoint() *CheckpointState {
	for i := len(r.Checkpoints) - 1; i >= 0; i-- {
		if r.Checkpoints[i].Decided || r.Checkpoints[i].Superseded {
			continue
		}
		return &r.Checkpoints[i]
	}
	return nil
}

// openPhaseState returns the open phase visit. At most one visit is open at
// any time (recall appends the fresh visit, M11), so the scan is unambiguous.
func (r *RunState) openPhaseState() *PhaseState {
	for i := range r.Phases {
		if r.Phases[i].Open {
			return &r.Phases[i]
		}
	}
	return nil
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

// newNonce returns one short random-looking nonce (crypto/rand, never
// math/rand) that the AskUserQuestion payload must carry to bind a decision
// to a checkpoint.
func newNonce() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// drawNonce draws a nonce, re-drawing while reject returns true, at most 8
// attempts (task-2.5 m1). The bound keeps a pathological rejector from
// spinning; crypto/rand failures surface immediately.
func drawNonce(reject func(string) bool) (string, error) {
	const maxDraws = 8
	for i := 0; i < maxDraws; i++ {
		n, err := newNonce()
		if err != nil {
			return "", err
		}
		if reject == nil || !reject(n) {
			return n, nil
		}
	}
	return "", fmt.Errorf("checkpoint nonce: no unique nonce after %d draws", maxDraws)
}

// uniqueNonce draws a nonce distinct from every checkpoint nonce already in
// the run — a repeated nonce would let one answer bind to two checkpoints.
func (r *RunState) uniqueNonce() (string, error) {
	return drawNonce(func(n string) bool {
		for i := range r.Checkpoints {
			if r.Checkpoints[i].Nonce == n {
				return true
			}
		}
		return false
	})
}

func (r *RunState) addCheckpoint(kind CheckpointKind, phaseID, note string, artifacts []ArtifactRef, criteria []string) (CheckpointState, error) {
	nonce, err := r.uniqueNonce()
	if err != nil {
		return CheckpointState{}, fmt.Errorf("checkpoint nonce: %w", err)
	}
	cp := CheckpointState{
		ID:               fmt.Sprintf("cp-%d", len(r.Checkpoints)+1),
		Kind:             kind,
		PhaseID:          phaseID,
		Note:             note,
		Nonce:            nonce,
		OpenedAt:         nowUTC(),
		ArtifactVersions: artifactVersionMap(artifacts),
		CriteriaIDs:      append([]string(nil), criteria...),
	}
	r.Checkpoints = append(r.Checkpoints, cp)
	return cp, nil
}

func artifactVersionMap(artifacts []ArtifactRef) map[string]string {
	if len(artifacts) == 0 {
		return nil
	}
	m := make(map[string]string, len(artifacts))
	for _, a := range artifacts {
		m[a.Path] = a.Digest
	}
	return m
}

func copyArtifactVersions(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func containsID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func (r *RunState) addStale(id string) {
	if id != "" && !containsID(r.Stale, id) {
		r.Stale = append(r.Stale, id)
	}
}

// removeStale drops one id from Stale — re-verification through re-acceptance
// restores a criterion's validity (task-2.5 M10).
func (r *RunState) removeStale(id string) {
	for i, x := range r.Stale {
		if x == id {
			r.Stale = append(r.Stale[:i], r.Stale[i+1:]...)
			return
		}
	}
}

// CheckpointContentDigest is the canonical digest of a checkpoint's current
// content: id, nonce, kind, phase, requirements version, note, criteria
// (sorted) and artifact versions (sorted). MarkDisplayed binds exactly this;
// RecordDecision re-computes it and refuses any decision whose checkpoint
// content drifted from what was displayed (task-2.5 B1) — a render of a stale
// or altered checkpoint can never produce it, so "展示版本错配" fails closed.
func CheckpointContentDigest(cp CheckpointState, requirementsVersion int) string {
	h := sha256.New()
	fmt.Fprintf(h, "summoner-checkpoint/v1\nid=%s\nnonce=%s\nkind=%s\nphase=%s\nreq=%d\nnote=%s\n",
		cp.ID, cp.Nonce, cp.Kind, cp.PhaseID, requirementsVersion, cp.Note)
	criteria := append([]string(nil), cp.CriteriaIDs...)
	sort.Strings(criteria)
	for _, c := range criteria {
		fmt.Fprintf(h, "criterion=%s\n", c)
	}
	paths := make([]string, 0, len(cp.ArtifactVersions))
	for p := range cp.ArtifactVersions {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		fmt.Fprintf(h, "artifact=%s@%s\n", p, cp.ArtifactVersions[p])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// --- phase & checkpoint transitions ----------------------------------------------

// OpenPhase opens a phase for work: legal only in ACTIVE with no phase open
// and no requirement change in flight. Resets the per-phase tool budget.
func (r *RunState) OpenPhase(id string) error {
	return r.commit(func(s *RunState) error {
		if err := s.requireStatus(StatusActive, "OpenPhase"); err != nil {
			return err
		}
		if s.Change != nil {
			return errors.New("OpenPhase: requirement change open (resume before opening a phase)")
		}
		if ph := s.openPhaseState(); ph != nil {
			return fmt.Errorf("OpenPhase(%s): phase %q is already open", id, ph.ID)
		}
		s.Phases = append(s.Phases, PhaseState{ID: id, Open: true, OpenedAt: nowUTC()})
		s.Counters.PhaseToolCalls = 0
		return nil
	})
}

// SubmitPhase closes the open phase and opens its checkpoint (WAIT_USER).
// Illegal while a checkpoint is pending, no phase is open, the payload names a
// different phase, or a requirement change is open (CHANGE_OPEN).
func (r *RunState) SubmitPhase(sub PhaseSubmission) (CheckpointState, error) {
	var cp CheckpointState
	err := r.commit(func(s *RunState) error {
		if err := s.requireStatus(StatusActive, "SubmitPhase"); err != nil {
			return err
		}
		if s.Change != nil {
			return errors.New("SubmitPhase: rejected — requirement change open (CHANGE_OPEN)")
		}
		ph := s.openPhaseState()
		if ph == nil {
			return errors.New("SubmitPhase: no open phase (OpenPhase first)")
		}
		if sub.PhaseID != ph.ID {
			return fmt.Errorf("SubmitPhase: phase id mismatch (payload %q, open %q)", sub.PhaseID, ph.ID)
		}
		now := nowUTC()
		ph.Open = false
		ph.ClosedAt = now
		ph.CriteriaIDs = append([]string(nil), sub.CriteriaIDs...)
		added, err := s.addCheckpoint(CheckpointPhase, ph.ID, sub.Summary, sub.Artifacts, sub.CriteriaIDs)
		if err != nil {
			return err
		}
		ph.CheckpointID = added.ID
		s.Status = StatusWaitUser
		cp = added
		return nil
	})
	if err != nil {
		return CheckpointState{}, err
	}
	return cp, nil
}

// MarkDisplayed binds a checkpoint to a verified render: the digest must
// equal the checkpoint's canonical content digest. Re-binding after a
// re-render overwrites the binding with the current content's digest; any
// mismatch fails closed.
func (r *RunState) MarkDisplayed(checkpointID, contentDigest string) error {
	return r.commit(func(s *RunState) error {
		if err := s.requireStatus(StatusWaitUser, "MarkDisplayed"); err != nil {
			return err
		}
		cp := s.CurrentCheckpoint()
		if cp == nil || cp.ID != checkpointID {
			return fmt.Errorf("MarkDisplayed: %q is not the open checkpoint", checkpointID)
		}
		expected := CheckpointContentDigest(*cp, s.RequirementsVersion)
		if contentDigest != expected {
			return fmt.Errorf("MarkDisplayed: display digest mismatch for %s (render does not match the current checkpoint version)", checkpointID)
		}
		cp.Displayed = true
		cp.DisplayDigest = contentDigest
		cp.DisplayedAt = nowUTC()
		return nil
	})
}

// phaseForDecision maps a checkpoint to the phase its CONTINUE/SKIP settles:
// the phase visit it closed (CheckpointID match), or — after a review — the
// latest phase visit still awaiting acceptance.
func (r *RunState) phaseForDecision(cp *CheckpointState) *PhaseState {
	if cp.Kind == CheckpointPhase {
		for i := range r.Phases {
			if r.Phases[i].CheckpointID == cp.ID {
				return &r.Phases[i]
			}
		}
		return nil
	}
	for i := len(r.Phases) - 1; i >= 0; i-- {
		if !r.Phases[i].Open && r.Phases[i].Outcome == "" {
			return &r.Phases[i]
		}
	}
	return nil
}

// decidePhase settles the phase a checkpoint decides. Re-accepting a phase
// re-validates its criteria: each one leaves Stale (task-2.5 M10) — stale
// means "needs re-verification", and the user just re-verified it.
func (r *RunState) decidePhase(cp *CheckpointState, accepted bool) {
	p := r.phaseForDecision(cp)
	if p == nil {
		return
	}
	if accepted {
		p.Outcome = "accepted"
		p.AcceptedCriteria = append([]string(nil), p.CriteriaIDs...)
		for _, c := range p.AcceptedCriteria {
			r.removeStale(c)
		}
	} else {
		p.Outcome = "skipped"
	}
}

// planRecall resolves the recall target — the most recent accepted prior
// phase visit (spec §4 default), or the checkpoint's own phase when none
// exists.
func (r *RunState) planRecall(cp *CheckpointState) (int, error) {
	for i := len(r.Phases) - 1; i >= 0; i-- {
		if r.Phases[i].Outcome == "accepted" {
			return i, nil
		}
	}
	for i := len(r.Phases) - 1; i >= 0; i-- {
		if r.Phases[i].CheckpointID == cp.ID {
			return i, nil
		}
	}
	return 0, errors.New("RecordDecision: RECALL has no phase target")
}

// applyRecallAt applies a recall at the target visit (task-2.5 M11): the old
// visit keeps its full history with Outcome "recalled" (its acceptance is
// revoked), and a FRESH visit of the same id is appended open — checkpoint
// history stays complete and "the phase" resolves to the latest visit. Every
// later visit's criteria go stale.
func (r *RunState) applyRecallAt(cp *CheckpointState, targetIdx int) {
	for i := targetIdx + 1; i < len(r.Phases); i++ {
		for _, c := range r.Phases[i].CriteriaIDs {
			r.addStale(c)
		}
		for _, c := range r.Phases[i].AcceptedCriteria {
			r.addStale(c)
		}
	}
	target := &r.Phases[targetIdx]
	target.Open = false
	target.Outcome = "recalled"
	r.Phases = append(r.Phases, PhaseState{ID: target.ID, Open: true, OpenedAt: nowUTC()})
	r.Counters.PhaseToolCalls = 0 // a redone phase gets a fresh tool budget
	r.Recalls = append(r.Recalls, RecallRecord{FromPhaseID: cp.PhaseID, TargetPhaseID: target.ID, At: nowUTC()})
	r.Status = StatusActive
}

func validDecision(d Decision) error {
	switch d {
	case DecisionContinue, DecisionSkip, DecisionDone, DecisionRecall,
		DecisionStop, DecisionReviewOnDemand, DecisionAcceptRequirementChange:
		return nil
	}
	return fmt.Errorf("unknown decision %q", d)
}

// legalDecisionForKind is the per-kind legality matrix: a change-confirm
// checkpoint decides only ACCEPT_REQUIREMENT_CHANGE/STOP; a recovery
// checkpoint only CONTINUE/RECALL/STOP (B3); everything else takes the full
// menu except ACCEPT_REQUIREMENT_CHANGE.
func legalDecisionForKind(kind CheckpointKind, d Decision) error {
	switch kind {
	case CheckpointChangeConfirm:
		if d != DecisionAcceptRequirementChange && d != DecisionStop {
			return fmt.Errorf("%s illegal at change-confirm checkpoint (want ACCEPT_REQUIREMENT_CHANGE or STOP)", d)
		}
	case CheckpointRecovery:
		if d != DecisionContinue && d != DecisionRecall && d != DecisionStop {
			return fmt.Errorf("%s illegal at recovery checkpoint (want CONTINUE, RECALL or STOP)", d)
		}
	default:
		if d == DecisionAcceptRequirementChange {
			return fmt.Errorf("ACCEPT_REQUIREMENT_CHANGE illegal at %s checkpoint (no requirement change open)", kind)
		}
	}
	return nil
}

// restoreRecoveryPrior ends an OUTCOME_UNKNOWN episode: the run returns to
// the status frozen when the episode started (RecoveryPrior, B4) and the
// prior is cleared. Empty/invalid priors fall back to ACTIVE.
func (r *RunState) restoreRecoveryPrior() {
	switch r.RecoveryPrior {
	case StatusActive, StatusWaitUser, StatusReviewPending:
		r.Status = r.RecoveryPrior
	default:
		r.Status = StatusActive
	}
	r.RecoveryPrior = ""
}

// RecordDecision applies a user decision to the open checkpoint. The
// checkpoint must be displayed, its content still byte-identical to the
// displayed render (B1), and the decision legal for the checkpoint kind.
// REVIEW_ON_DEMAND records a persisted review obligation instead of
// authorizing work (B2).
func (r *RunState) RecordDecision(checkpointID string, d Decision) error {
	return r.commit(func(s *RunState) error {
		if err := s.requireStatus(StatusWaitUser, "RecordDecision"); err != nil {
			return err
		}
		// A superseded checkpoint is dead: the re-rendered successor decides.
		for i := range s.Checkpoints {
			if s.Checkpoints[i].ID == checkpointID && s.Checkpoints[i].Superseded {
				return fmt.Errorf("RecordDecision: checkpoint %s was superseded by a requirement change — decide its re-rendered successor instead", checkpointID)
			}
		}
		cp := s.CurrentCheckpoint()
		if cp == nil || cp.ID != checkpointID {
			return fmt.Errorf("RecordDecision: %q is not the open checkpoint", checkpointID)
		}
		if !cp.Displayed || cp.DisplayDigest == "" {
			return fmt.Errorf("RecordDecision: checkpoint %s was never displayed — decision rejected", checkpointID)
		}
		// B1 display binding: the decision binds the displayed content. If
		// anything changed since (note, criteria, phase, requirements
		// version), the render is stale — a new display pass is required.
		if CheckpointContentDigest(*cp, s.RequirementsVersion) != cp.DisplayDigest {
			return fmt.Errorf("RecordDecision: checkpoint %s content changed since display — re-render required", checkpointID)
		}
		if err := validDecision(d); err != nil {
			return err
		}
		if err := legalDecisionForKind(cp.Kind, d); err != nil {
			return fmt.Errorf("RecordDecision: %s", err)
		}
		// Pre-validate every effect before mutating anything.
		recallIdx := -1
		if d == DecisionRecall {
			var err error
			if recallIdx, err = s.planRecall(cp); err != nil {
				return err
			}
		}
		if d == DecisionAcceptRequirementChange && s.Change == nil {
			return fmt.Errorf("RecordDecision: no requirement change open for checkpoint %s", checkpointID)
		}

		cp.Decided = true
		cp.Decision = d
		cp.DecidedAt = nowUTC()
		switch d {
		case DecisionContinue:
			if cp.Kind == CheckpointRecovery {
				// Accept the status quo; restore the episode prior (B3).
				s.restoreRecoveryPrior()
			} else {
				s.decidePhase(cp, true)
				s.Status = StatusActive
			}
		case DecisionSkip:
			s.decidePhase(cp, false)
			s.Status = StatusActive
		case DecisionDone:
			s.decidePhase(cp, true)
			s.Status = StatusPostGamePending // CLOSED only after post-game persists
		case DecisionRecall:
			if p := s.phaseForDecision(cp); p != nil && p.Outcome == "" {
				p.Outcome = "recalled"
				// F1 (review round 1): when the decision came from a
				// checkpoint ABOVE the visit's own (review-result / recovery
				// fallback), the recalled visit's still-PENDING checkpoint
				// can never be decided — RecordDecision needs WAIT_USER and
				// every WAIT_USER entry appends a newer checkpoint — so it
				// would resurface as an orphan in ACTIVE and wedge the change
				// loop. Supersede it (CurrentCheckpoint skips superseded). A
				// checkpoint already decided (e.g. [v] then review RECALL),
				// and the decision checkpoint itself, are left alone.
				if p.CheckpointID != "" && p.CheckpointID != cp.ID {
					for i := range s.Checkpoints {
						if s.Checkpoints[i].ID == p.CheckpointID && !s.Checkpoints[i].Decided {
							s.Checkpoints[i].Superseded = true
							break
						}
					}
				}
			}
			s.applyRecallAt(cp, recallIdx)
			s.RecoveryPrior = "" // a recall supersedes the episode's restore
		case DecisionReviewOnDemand:
			// B2: park the run under a persisted obligation — no work may
			// open until the matching review is requested and concluded.
			s.ReviewObligation = &ReviewObligation{
				SourceCheckpointID: cp.ID,
				PhaseID:            cp.PhaseID,
				At:                 nowUTC(),
			}
			s.Status = StatusReviewPending
		case DecisionStop:
			s.Status = StatusPostGamePending // minimal exit journal is the caller's duty
			s.RecoveryPrior = ""
		case DecisionAcceptRequirementChange:
			if err := s.resumeAfterChange(); err != nil { // validated above
				return err
			}
		}
		return nil
	})
}

// --- requirement-change loop (spec §1 增补) ---------------------------------------

func copyDevPosition(p DevPosition) DevPosition {
	q := p
	q.PendingVerifications = append([]string(nil), p.PendingVerifications...)
	return q
}

// OpenRequirementChange freezes the current DevPosition and enters the
// CHANGE_OPEN sub-state. Legal from ACTIVE (mid-phase change) and from
// WAIT_USER (spec §1 增补: 检查点也可显式进入) — a checkpoint-entered change
// supersedes that checkpoint (M2): it stays in history, undecidable, and a
// fresh re-rendered successor re-asks it after the change closes. While
// open, SubmitPhase is refused (no submitting from a half-changed baseline).
func (r *RunState) OpenRequirementChange(note string) error {
	return r.commit(func(s *RunState) error {
		if s.Status != StatusActive && s.Status != StatusWaitUser {
			return fmt.Errorf("OpenRequirementChange: illegal status %s (want ACTIVE or WAIT_USER)", s.Status)
		}
		if s.Change != nil {
			return errors.New("OpenRequirementChange: a change is already open")
		}
		if s.Status == StatusWaitUser {
			if cp := s.CurrentCheckpoint(); cp != nil {
				cp.Superseded = true
			}
		}
		s.Change = &ChangeContext{
			Note:          note,
			OpenedAt:      nowUTC(),
			PriorStatus:   s.Status,
			SavedPosition: copyDevPosition(s.DevPosition),
		}
		return nil
	})
}

// ApplyRequirementDelta applies an explicit delta: RequirementsVersion bumps,
// affected ids go stale, untouched accepted results stay valid. It then opens
// the single-point confirm checkpoint (ACCEPT_REQUIREMENT_CHANGE resumes).
// Legal from ACTIVE (mid-phase change) and — since M2 — from a WAIT_USER
// entry, where the superseded checkpoint means nothing is pending and the
// confirm checkpoint opens as usual.
func (r *RunState) ApplyRequirementDelta(delta RequirementDelta) (ImpactReport, error) {
	var report ImpactReport
	err := r.commit(func(s *RunState) error {
		fromWaitUser := s.Change != nil && s.Change.PriorStatus == StatusWaitUser
		if s.Status != StatusActive && !(s.Status == StatusWaitUser && fromWaitUser) {
			return fmt.Errorf("ApplyRequirementDelta: illegal status %s (want ACTIVE, or WAIT_USER for a WAIT_USER-entered change)", s.Status)
		}
		if s.Change == nil {
			return errors.New("ApplyRequirementDelta: no requirement change open (OpenRequirementChange first)")
		}
		if cp := s.CurrentCheckpoint(); cp != nil {
			return fmt.Errorf("ApplyRequirementDelta: checkpoint %s is pending — a change-confirm checkpoint is already open", cp.ID)
		}
		s.RequirementsVersion++
		var newly []string
		for _, id := range append(append([]string(nil), delta.Changed...), delta.Affected...) {
			if id == "" || containsID(s.Stale, id) {
				continue
			}
			s.addStale(id)
			newly = append(newly, id)
		}
		var preserved []string
		for i := range s.Phases {
			for _, c := range s.Phases[i].AcceptedCriteria {
				if !containsID(s.Stale, c) && !containsID(preserved, c) {
					preserved = append(preserved, c)
				}
			}
		}
		phaseID := ""
		if ph := s.openPhaseState(); ph != nil {
			phaseID = ph.ID
		}
		if _, err := s.addCheckpoint(CheckpointChangeConfirm, phaseID, delta.Note, nil, nil); err != nil {
			return err
		}
		s.Change.Applied = true
		s.Status = StatusWaitUser
		report = ImpactReport{RequirementsVersion: s.RequirementsVersion, NewlyStale: newly, Preserved: preserved}
		return nil
	})
	if err != nil {
		return ImpactReport{}, err
	}
	return report, nil
}

// rerenderCheckpoint re-renders the checkpoint a WAIT_USER-entered
// requirement change superseded (M2): same kind and phase, fresh nonce, note
// annotated with the new requirements version, artifact versions and criteria
// carried over from the superseded original. The owning phase visit is
// re-linked so a CONTINUE here decides it normally. Returns false when no
// superseded-undecided checkpoint exists (a WAIT_USER entry with nothing
// owed — the caller lands ACTIVE).
func (r *RunState) rerenderCheckpoint() bool {
	var src *CheckpointState
	for i := len(r.Checkpoints) - 1; i >= 0; i-- {
		if r.Checkpoints[i].Superseded && !r.Checkpoints[i].Decided {
			src = &r.Checkpoints[i]
			break
		}
	}
	if src == nil {
		return false
	}
	nonce, err := r.uniqueNonce()
	if err != nil {
		return false // fail-closed: land ACTIVE rather than fabricate a checkpoint
	}
	note := src.Note
	if note != "" {
		note += " — "
	}
	note += fmt.Sprintf("re-rendered after requirement change v%d", r.RequirementsVersion)
	cp := CheckpointState{
		ID:               fmt.Sprintf("cp-%d", len(r.Checkpoints)+1),
		Kind:             src.Kind,
		PhaseID:          src.PhaseID,
		Note:             note,
		Nonce:            nonce,
		OpenedAt:         nowUTC(),
		ArtifactVersions: copyArtifactVersions(src.ArtifactVersions),
		CriteriaIDs:      append([]string(nil), src.CriteriaIDs...),
	}
	r.Checkpoints = append(r.Checkpoints, cp)
	for i := len(r.Phases) - 1; i >= 0; i-- {
		if r.Phases[i].CheckpointID == src.ID {
			r.Phases[i].CheckpointID = cp.ID
			break
		}
	}
	return true
}

// resumeAfterChange restores the frozen DevPosition and clears CHANGE_OPEN.
// It requires an APPLIED delta (M2 kills the open→resume bypass: a change
// closes only through its single-point confirm) and refuses whenever any
// checkpoint still awaits decision. The landing status depends on the entry:
// an ACTIVE-entered change resumes ACTIVE; a WAIT_USER-entered change
// re-renders the checkpoint it superseded and stays WAIT_USER — the user
// re-decides the interrupted question against the new baseline (单点闭环:
// one re-render, not a phase-by-phase reopen).
func (r *RunState) resumeAfterChange() error {
	if r.Change == nil {
		return errors.New("no requirement change open")
	}
	if !r.Change.Applied {
		return errors.New("requirement change has no applied delta — apply the delta and close through the confirm checkpoint (ACCEPT_REQUIREMENT_CHANGE)")
	}
	if cp := r.CurrentCheckpoint(); cp != nil {
		return fmt.Errorf("requirement change cannot resume: checkpoint %s (%s) awaits decision — acceptance only via RecordDecision(ACCEPT_REQUIREMENT_CHANGE)", cp.ID, cp.Kind)
	}
	r.DevPosition = r.Change.SavedPosition
	prior := r.Change.PriorStatus
	r.Change = nil
	if prior == StatusWaitUser && r.rerenderCheckpoint() {
		r.Status = StatusWaitUser
	} else {
		r.Status = StatusActive
	}
	return nil
}

// ResumeAfterChange is the framework-level resume entry. Since M2 it cannot
// bypass the confirm checkpoint: without an applied delta it refuses, and
// with one the pending confirm checkpoint must be accepted through
// RecordDecision(ACCEPT_REQUIREMENT_CHANGE) — the platform-anchored path that
// performs the same resume. Kept as the belt that refuses un-applied changes.
func (r *RunState) ResumeAfterChange() error {
	if r.Status != StatusActive && r.Status != StatusWaitUser {
		return fmt.Errorf("ResumeAfterChange: illegal status %s", r.Status)
	}
	return r.commit(func(s *RunState) error {
		return s.resumeAfterChange()
	})
}

// --- review lifecycle (spec §3) ----------------------------------------------------

func envelopeDigest(env Envelope) string {
	b, err := json.Marshal(env)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (r *RunState) findReview(reviewID string) *ReviewRecord {
	for i := range r.Reviews {
		if r.Reviews[i].ReviewID == reviewID {
			return &r.Reviews[i]
		}
	}
	return nil
}

// RequestReview registers a review envelope (basic shape checks here; the
// strict allow-list validation lives in envelope.go, Task 7) and moves the
// run to REVIEW_PENDING. The status gate comes FIRST (task-2.5 M1a): a NEW
// review requires ACTIVE (the 终局⑤ path) — or REVIEW_PENDING under a [v]
// obligation (B2), which the request consumes into the record — and a
// same-envelope re-send is legal only in REVIEW_PENDING while that review is
// PREPARED/RUNNING (spec §3 崩溃恢复: crash + reconnect must not duplicate the
// review or rerun the producer; re-spawning the reviewer consumes remaining
// delegation budget, accounted by the caller). CLOSED, WAIT_USER and
// OUTCOME_UNKNOWN refuse everything — a terminal-state re-send never
// succeeds. A different envelope or a concluded review id is refused, and a
// new review id with nothing authorizing it is refused.
func (r *RunState) RequestReview(env Envelope) (string, error) {
	// Pure input validation first.
	if env.SchemaVersion == "" {
		return "", errors.New("RequestReview: envelope schema_version is required")
	}
	if env.RunID != r.RunID {
		return "", fmt.Errorf("RequestReview: envelope run_id %q does not match run %q", env.RunID, r.RunID)
	}
	if env.ReviewID == "" {
		return "", errors.New("RequestReview: envelope review_id is required")
	}
	if len(env.ArtifactSet) == 0 {
		return "", errors.New("RequestReview: envelope artifact_set must be non-empty")
	}

	// M1a status gate before the fast path.
	if r.Status != StatusActive && r.Status != StatusReviewPending {
		return "", fmt.Errorf("RequestReview: illegal status %s (want ACTIVE for a new review, or REVIEW_PENDING for a same-envelope re-send / an outstanding [v] obligation)", r.Status)
	}

	if rv := r.findReview(env.ReviewID); rv != nil {
		// Re-send of a registered review: only mid-flight, only in
		// REVIEW_PENDING, only the exact same envelope.
		if err := r.requireStatus(StatusReviewPending, "RequestReview"); err != nil {
			return "", err
		}
		if rv.Status != ReviewPrepared && rv.Status != ReviewRunning {
			return "", fmt.Errorf("RequestReview: review %s already concluded (%s) — use a new review id", env.ReviewID, rv.Status)
		}
		if rv.EnvelopeDigest != envelopeDigest(env) {
			return "", fmt.Errorf("RequestReview: review %s already registered with a different envelope", env.ReviewID)
		}
		return rv.ReviewID, nil // same envelope: idempotent re-send
	}

	// New review id: ACTIVE (终局⑤) or REVIEW_PENDING under a [v] obligation (B2).
	if r.ReviewObligation != nil {
		if err := r.requireStatus(StatusReviewPending, "RequestReview"); err != nil {
			return "", err
		}
	} else if err := r.requireStatus(StatusActive, "RequestReview"); err != nil {
		return "", err
	}
	for i := range r.Reviews {
		if r.Reviews[i].Status == ReviewPrepared || r.Reviews[i].Status == ReviewRunning {
			return "", fmt.Errorf("RequestReview: review %s still in flight — one review at a time", r.Reviews[i].ReviewID)
		}
	}
	err := r.commit(func(s *RunState) error {
		rec := ReviewRecord{
			ReviewID:       env.ReviewID,
			Status:         ReviewPrepared,
			EnvelopeDigest: envelopeDigest(env),
			RequestedAt:    nowUTC(),
		}
		if s.ReviewObligation != nil {
			rec.SourceCheckpointID = s.ReviewObligation.SourceCheckpointID
			s.ReviewObligation = nil // consumed: the review record now carries it
		}
		s.Reviews = append(s.Reviews, rec)
		s.Status = StatusReviewPending
		return nil
	})
	if err != nil {
		return "", err
	}
	return env.ReviewID, nil
}

// MarkReviewRunning moves a PREPARED review to RUNNING (the reviewer agent
// actually started). Kept as the single-step primitive; the PreToolUse gate
// uses StartDelegation instead, which binds this transition to the delegation
// and tool-call counters in ONE Save (review round 2, R1).
func (r *RunState) MarkReviewRunning(reviewID string) error {
	return r.commit(func(s *RunState) error {
		if err := s.requireStatus(StatusReviewPending, "MarkReviewRunning"); err != nil {
			return err
		}
		rv := s.findReview(reviewID)
		if rv == nil {
			return fmt.Errorf("MarkReviewRunning: unknown review %q", reviewID)
		}
		if rv.Status != ReviewPrepared {
			return fmt.Errorf("MarkReviewRunning: review %s is %s, not PREPARED", reviewID, rv.Status)
		}
		rv.Status = ReviewRunning
		rv.StartedAt = nowUTC()
		return nil
	})
}

// StartDelegation consumes one delegation slot atomically: for a reviewer it
// moves the PREPARED review to RUNNING, and every delegation counts against
// the delegation budget and the phase tool budget — all validated first,
// then persisted in ONE Save (review round 2, R1). The delegation budget is
// ALSO enforced here (task-2.5 14a): the gate pre-checks it, but direct
// reducer callers must not be able to exceed the limit either — belt and
// braces. A reviewer delegation is legal only in REVIEW_PENDING with that
// review PREPARED; a worker delegation only in ACTIVE. Since M9 the whole
// transition runs in a commit snapshot: a failed Save leaves memory and disk
// untouched, so a retry after repair is clean (and the caller must LoadRun
// before further use — see commit).
func (r *RunState) StartDelegation(reviewID string, isReviewer bool) error {
	return r.commit(func(s *RunState) error {
		if l := s.Limits.Delegations; l > 0 && s.Counters.Delegations >= l {
			return fmt.Errorf("StartDelegation: delegation budget exhausted (%d/%d) — only the user can add budget", s.Counters.Delegations, l)
		}
		if isReviewer {
			if err := s.requireStatus(StatusReviewPending, "StartDelegation"); err != nil {
				return err
			}
			rv := s.findReview(reviewID)
			if rv == nil {
				return fmt.Errorf("StartDelegation: unknown review %q", reviewID)
			}
			if rv.Status != ReviewPrepared {
				return fmt.Errorf("StartDelegation: review %s is %s, not PREPARED", reviewID, rv.Status)
			}
			rv.Status = ReviewRunning
			rv.StartedAt = nowUTC()
		} else {
			if err := s.requireStatus(StatusActive, "StartDelegation"); err != nil {
				return err
			}
		}
		s.Counters.Delegations++
		s.CountToolCall()
		return nil
	})
}

// RecordReviewResult concludes the in-flight review and opens a review-result
// checkpoint (PASS still leaves the run WAIT_USER — spec §3). Enum violations
// are protocol errors: the review is marked FAILED, the failure routes to a
// REVIEW_RESULT checkpoint so the run stays recoverable, the routing IS
// persisted, and an error is returned to the caller. BLOCKED and the quality
// verdict are mutually exclusive (task-2.5 M12): BLOCKED must not pose as a
// quality conclusion and COMPLETED must not carry a reason code. NEEDS-FIX
// consumes run rework budget; BLOCKED does not.
func (r *RunState) RecordReviewResult(res ReviewResult) error {
	// protocolErr carries the enum problem out AFTER the routing persisted —
	// returning it from the closure would roll the routing back.
	var protocolErr error
	err := r.commit(func(s *RunState) error {
		if err := s.requireStatus(StatusReviewPending, "RecordReviewResult"); err != nil {
			return err
		}
		rv := s.findReview(res.ReviewID)
		if rv == nil {
			return fmt.Errorf("RecordReviewResult: unknown review %q", res.ReviewID)
		}
		if rv.Status != ReviewPrepared && rv.Status != ReviewRunning {
			return fmt.Errorf("RecordReviewResult: review %s already concluded (%s) — late results must not affect a new generation", res.ReviewID, rv.Status)
		}
		problem := ""
		switch res.RunStatus {
		case ReviewRunCompleted:
			switch {
			case res.Verdict != VerdictPass && res.Verdict != VerdictNeedsFix:
				problem = fmt.Sprintf("verdict must be %s or %s when run_status is COMPLETED", VerdictPass, VerdictNeedsFix)
			case res.ReasonCode != "":
				problem = "reason_code must be empty when run_status is COMPLETED (a completion carries no block reason)"
			}
		case ReviewRunBlocked:
			switch {
			case res.ReasonCode == "":
				problem = "BLOCKED requires a reason_code"
			case res.Verdict != "":
				problem = fmt.Sprintf("verdict must be empty when run_status is BLOCKED (got %q) — BLOCKED never poses as a quality conclusion", res.Verdict)
			}
		default:
			problem = fmt.Sprintf("run_status must be %s or %s", ReviewRunCompleted, ReviewRunBlocked)
		}
		if problem != "" {
			rv.Status = ReviewFailed
			rv.ConcludedAt = nowUTC()
			// A protocol error must not brick the run: route it to a checkpoint
			// a human can still decide (spec §3 review.failed 语义), persist
			// that routing, and report the problem to the caller.
			cp, err := s.addCheckpoint(CheckpointReviewResult, "", "FAILED "+problem, nil, nil)
			if err != nil {
				return err
			}
			s.Status = StatusWaitUser
			protocolErr = fmt.Errorf("RecordReviewResult: %s — review %s marked FAILED, routed to checkpoint %s", problem, res.ReviewID, cp.ID)
			return nil
		}

		now := nowUTC()
		rv.ArtifactDigest = res.ArtifactDigest
		rv.ReasonCode = res.ReasonCode
		if res.RunStatus == ReviewRunBlocked {
			rv.Status = ReviewBlocked
		} else {
			rv.Status = ReviewReviewed
			rv.Verdict = res.Verdict
			if res.Verdict == VerdictNeedsFix {
				s.Counters.RunRework++
			}
		}
		rv.ConcludedAt = now
		note := string(res.RunStatus)
		if res.ReasonCode != "" {
			note += " " + res.ReasonCode
		}
		// M12: the verdict joins the note only for a COMPLETED run — a
		// BLOCKED note must never read like a quality conclusion.
		if res.RunStatus == ReviewRunCompleted && res.Verdict != "" {
			note += " " + res.Verdict
		}
		if _, err := s.addCheckpoint(CheckpointReviewResult, "", note, nil, nil); err != nil {
			return err
		}
		s.Status = StatusWaitUser
		return nil
	})
	if err != nil {
		return err
	}
	return protocolErr
}

// --- OUTCOME_UNKNOWN recovery (spec §5) ---------------------------------------------

// MarkOutcomeUnknown records a tool call whose side effect may have happened
// but whose result was never persisted (crash window). Idempotent per
// toolUseID. When the episode STARTS (status not already OUTCOME_UNKNOWN) it
// freezes RecoveryPrior — the restore target for the whole batch, so a later
// episode never inherits an earlier one's prior (task-2.5 B4). Mutates in
// memory only — callers persist via Save; recovery probes never auto-replay
// mutating actions.
func (r *RunState) MarkOutcomeUnknown(toolUseID string) {
	if toolUseID == "" || r.Status == StatusClosed || r.Status == StatusPostGamePending {
		return
	}
	for i := range r.OutcomeUnknowns {
		if r.OutcomeUnknowns[i].ToolUseID == toolUseID {
			return
		}
	}
	episode := 0
	if n := len(r.OutcomeUnknowns); n > 0 {
		episode = r.OutcomeUnknowns[n-1].Episode
	}
	if r.Status != StatusOutcomeUnknown {
		r.RecoveryPrior = r.Status
		episode++
	}
	r.OutcomeUnknowns = append(r.OutcomeUnknowns, OutcomeUnknown{
		ToolUseID:   toolUseID,
		PriorStatus: r.RecoveryPrior,
		Episode:     episode,
		RecordedAt:  nowUTC(),
	})
	r.Status = StatusOutcomeUnknown
}

// currentOutcomeEpisode returns the episode number of the latest entry (0
// when none). Entries of older episodes are fully resolved by construction —
// an episode only ends when every entry resolved.
func (r *RunState) currentOutcomeEpisode() int {
	if n := len(r.OutcomeUnknowns); n > 0 {
		return r.OutcomeUnknowns[n-1].Episode
	}
	return 0
}

// ResolveOutcome records a probe verdict (accepted|failed|indeterminate) for
// an OUTCOME_UNKNOWN entry. Mid-episode resolves only persist the verdict.
// When the LAST entry of the episode resolves: all-accepted restores the
// episode prior directly; any failed/indeterminate opens a recovery
// checkpoint instead (B3) — the run goes WAIT_USER and the user decides
// CONTINUE (accept the status quo, restore prior) / RECALL / STOP; a mutating
// action is never auto-replayed. A late duplicate resolve with the same
// outcome is an idempotent no-op; a conflicting one is an error.
func (r *RunState) ResolveOutcome(toolUseID, outcome string) error {
	switch outcome {
	case OutcomeAccepted, OutcomeFailed, OutcomeIndeterminate:
	default:
		return fmt.Errorf("ResolveOutcome: invalid outcome %q (want accepted|failed|indeterminate)", outcome)
	}
	return r.commit(func(s *RunState) error {
		var entry *OutcomeUnknown
		for i := range s.OutcomeUnknowns {
			if s.OutcomeUnknowns[i].ToolUseID == toolUseID {
				entry = &s.OutcomeUnknowns[i]
				break
			}
		}
		if entry == nil {
			return fmt.Errorf("ResolveOutcome: unknown tool_use_id %q", toolUseID)
		}
		if entry.Resolved {
			if entry.Outcome == outcome {
				return nil // late duplicate callback: no-op
			}
			return fmt.Errorf("ResolveOutcome: %s already resolved as %s (conflicting %s)", toolUseID, entry.Outcome, outcome)
		}
		if err := s.requireStatus(StatusOutcomeUnknown, "ResolveOutcome"); err != nil {
			return err
		}
		entry.Resolved = true
		entry.Outcome = outcome
		entry.ResolvedAt = nowUTC()
		for i := range s.OutcomeUnknowns {
			if !s.OutcomeUnknowns[i].Resolved {
				return nil // episode still open — nothing to restore yet
			}
		}
		// Episode complete. Any failed/indeterminate verdict (of THIS
		// episode — older episodes already had their checkpoint) demands a
		// human recovery decision instead of an automatic restore.
		episode := s.currentOutcomeEpisode()
		bad := false
		for i := range s.OutcomeUnknowns {
			e := &s.OutcomeUnknowns[i]
			if e.Episode == episode && (e.Outcome == OutcomeFailed || e.Outcome == OutcomeIndeterminate) {
				bad = true
			}
		}
		if bad {
			parts := make([]string, 0, 2)
			for i := range s.OutcomeUnknowns {
				e := &s.OutcomeUnknowns[i]
				if e.Episode == episode {
					parts = append(parts, fmt.Sprintf("%s=%s", e.ToolUseID, e.Outcome))
				}
			}
			sort.Strings(parts)
			if _, err := s.addCheckpoint(CheckpointRecovery, "", "outcome probes: "+strings.Join(parts, "; "), nil, nil); err != nil {
				return err
			}
			s.Status = StatusWaitUser // RecoveryPrior stays frozen for the CONTINUE
		} else {
			s.restoreRecoveryPrior()
		}
		return nil
	})
}

// --- budget (spec §5 预算与终止) ------------------------------------------------------

// CountToolCall records one tool call against the current phase's budget.
// With no open phase it is a no-op (task-2.5 M1b): the budget is per phase,
// and phase-less calls (run start, review dispatch from a concluded run)
// charge nothing. Mutates in memory only (no error return) — callers persist
// via Save.
func (r *RunState) CountToolCall() {
	if r.openPhaseState() == nil {
		return
	}
	r.Counters.PhaseToolCalls++
}

// BudgetExhausted reports whether any limit set at run start has been
// reached, with a reason naming the first exhausted budget. Zero-value
// limits are unlimited.
func (r *RunState) BudgetExhausted() (bool, string) {
	if l := r.Limits.PhaseToolCalls; l > 0 && r.Counters.PhaseToolCalls >= l {
		return true, fmt.Sprintf("phase tool-call budget exhausted (%d/%d)", r.Counters.PhaseToolCalls, l)
	}
	if l := r.Limits.FailedChecks; l > 0 && r.Counters.FailedChecks >= l {
		return true, fmt.Sprintf("failed-check budget exhausted (%d/%d)", r.Counters.FailedChecks, l)
	}
	if l := r.Limits.RunRework; l > 0 && r.Counters.RunRework >= l {
		return true, fmt.Sprintf("run rework budget exhausted (%d/%d)", r.Counters.RunRework, l)
	}
	if l := r.Limits.Delegations; l > 0 && r.Counters.Delegations >= l {
		return true, fmt.Sprintf("delegation budget exhausted (%d/%d)", r.Counters.Delegations, l)
	}
	return false, ""
}
