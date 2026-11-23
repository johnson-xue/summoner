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
// suite stays hermetic (production resolves the real root below).
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
		SchemaVersion: RunStateSchemaVersion,
		RunID:         runID,
		SessionID:     sessionID,
		Workflow:      workflow,
		Status:        StatusActive,
		// I1 (adjudication §5): the run start is the first lifecycle boundary —
		// pre-first-OpenPhase work charges the upcoming visit's bucket, and the
		// first OpenPhase adopts that accrual instead of resetting it.
		UpcomingVisit:       true,
		RequirementsVersion: 1,
		Limits:              limits,
		// T18 (0.6.1): budgets settle HERE — the manifest's suggested_budgets
		// (resolved by the caller) applies as configuration, else unlimited.
		// No run-start ask exists; exhaustion still offers extensions.
		BudgetConfirmed: true,
		BudgetSettledBy: "run-start",
		StartedAt:       nowUTC(),
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
		MenuPages:        MenuProjection(kind),
	}
	// T18 (0.6.1 rev): the run-start budget ask is GONE — budgets are
	// manifest-authored (suggested_budgets applies at StartRun) or unlimited;
	// no checkpoint ever carries a budget question. The EXTENSION offer
	// survives: any checkpoint opened while the budget is EXHAUSTED carries
	// the persisted extension offer (余量为零 → 检查点呈现追加额度) —
	// digest-bound content whose later change invalidates the display binding.
	if exhausted, _ := r.BudgetExhausted(); exhausted {
		cp.ExtendAsk = r.newExtendAsk()
	}
	// Recovery round I4 (§5): the checkpoint offers the run's REAL accepted
	// visit history as named recall targets — the digest-bound provenance a
	// later "Recall: <target>" answer resolves against. One target per
	// accepted visit (label = its durable visit id), oldest first, plus the
	// "latest" default pointing at the most recent accepted visit. No
	// accepted history → no named offers (the §4 default still applies).
	latest := -1
	for i := range r.Phases {
		if r.Phases[i].Outcome == "accepted" {
			cp.RecallTargets = append(cp.RecallTargets, RecallTarget{
				VisitID: r.Phases[i].VisitID,
				Label:   r.Phases[i].VisitID,
				PhaseID: r.Phases[i].ID,
			})
			latest = i
		}
	}
	if latest >= 0 {
		cp.RecallTargets = append([]RecallTarget{{
			VisitID: r.Phases[latest].VisitID,
			Label:   "latest",
			PhaseID: r.Phases[latest].ID,
		}}, cp.RecallTargets...)
	}
	// Re-review F1: the named targets must be OFFERABLE — the S2 gate pins
	// every decision question's options to a persisted page, so a checkpoint
	// with real targets persists a target page ("Recall: latest" plus one
	// "Recall: <visit>" per offered visit, pages of ≤4). The kind pages keep
	// the shipped description label, which the recorder maps to the DEFAULT
	// recall.
	if len(cp.RecallTargets) > 0 && menuHasRecallAction(kind) {
		targets := []string{"Recall: latest"}
		for _, t := range cp.RecallTargets {
			if t.Label != "latest" {
				targets = append(targets, "Recall: "+t.Label)
			}
		}
		for i := 0; i < len(targets); i += menuPageMaxOptions {
			end := i + menuPageMaxOptions
			if end > len(targets) {
				end = len(targets)
			}
			cp.MenuPages = append(cp.MenuPages, MenuPage{
				Labels: append([]string(nil), targets[i:end]...),
			})
		}
		for i := range cp.MenuPages {
			cp.MenuPages[i].NavNext = ""
			cp.MenuPages[i].NavPrev = ""
			if i+1 < len(cp.MenuPages) {
				cp.MenuPages[i].NavNext = MenuNavNext
			}
			if i > 0 {
				cp.MenuPages[i].NavPrev = MenuNavPrev
			}
		}
	}
	r.Checkpoints = append(r.Checkpoints, cp)
	return cp, nil
}

// newExtendAsk builds the exhaustion checkpoint's extension offer: per SET
// axis the current limit again (追加已显示额度 — add the displayed amount once
// more), unlimited axes stay unlimited (a zero limit is not a cap to raise).
func (r *RunState) newExtendAsk() *ExtendAsk {
	return &ExtendAsk{
		Delta: BudgetLimits{
			PhaseToolCalls: r.Limits.PhaseToolCalls,
			FailedChecks:   r.Limits.FailedChecks,
			RunRework:      r.Limits.RunRework,
			Delegations:    r.Limits.Delegations,
		},
		ExtendLabel: BudgetExtendLabel,
		HoldLabel:   BudgetHoldLabel,
	}
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
// (sorted), artifact versions (sorted) and — since T18 — the persisted
// budget/extension OFFERS (the displayed proposal and deltas are bound
// content; a changed offer after display invalidates the binding, while the
// APPLIED Limits/Counters are deliberately excluded — approving them must not
// re-render the checkpoint). Since 0.7.0 (D1) the structured conclusion and
// its outcome are bound content too: the checkpoint leads with them, so a
// conclusion edited after display must never keep its binding. A checkpoint
// carrying neither (every pre-0.7.0 checkpoint) hashes exactly as before.
// MarkDisplayed binds exactly this;
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
	if cp.BudgetAsk != nil {
		fmt.Fprintf(h, "budget_ask=%s|%s\n", budgetLimitsKey(cp.BudgetAsk.Proposal.Limits), cp.BudgetAsk.Proposal.Source)
	}
	if cp.ExtendAsk != nil {
		fmt.Fprintf(h, "extend_ask=%s\n", budgetLimitsKey(cp.ExtendAsk.Delta))
	}
	// 0.7.0 D1: the conclusion is display-bound content. Written only when
	// something is there, so a pre-0.7.0 checkpoint keeps its digest.
	if cp.Outcome != "" || cp.Conclusion != nil {
		if b, err := json.Marshal(cp.Conclusion); err == nil {
			fmt.Fprintf(h, "outcome=%s\nconclusion=%s\n", cp.Outcome, b)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// budgetLimitsKey is the canonical one-line form of a BudgetLimits value in
// digest-bound checkpoint content.
func budgetLimitsKey(l BudgetLimits) string {
	return fmt.Sprintf("phase_tool_calls=%d,failed_checks=%d,run_rework=%d,delegations=%d",
		l.PhaseToolCalls, l.FailedChecks, l.RunRework, l.Delegations)
}

// --- phase & checkpoint transitions ----------------------------------------------

// OpenPhase opens a phase for work: legal only in ACTIVE with no phase open
// and no requirement change in flight. Visit semantics (final-fix wave I1,
// adjudication §5): with an authorized UPCOMING visit the pre-provider work
// already charged this bucket — opening ADOPTS the accrual; without one this
// open authorizes its own fresh visit and resets the bucket. Either way the
// cumulative counters (rework, failed checks, delegations) are never touched.
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
		visitID := s.nextVisitID(id)
		s.Phases = append(s.Phases, PhaseState{ID: id, VisitID: visitID, Open: true, OpenedAt: nowUTC()})
		if s.UpcomingVisit {
			// Same-visit adoption: keep the pre-provider accrual.
			s.UpcomingVisit = false
		} else {
			s.Counters.PhaseToolCalls = 0
			s.ChargedToolUseIDs = nil
		}
		return nil
	})
}

// newVisitBucket authorizes the UPCOMING visit's budget bucket at the
// lifecycle decision boundary (final-fix wave I1, adjudication §5): a
// continue/skip decision just returned the run to ACTIVE, so real work
// between here and the next OpenPhase charges this bucket and OpenPhase
// adopts the accrual. Only this boundary resets the per-visit bucket — and
// it never erases the cumulative counters (rework, failed checks,
// delegations).
func (s *RunState) newVisitBucket() {
	s.Counters.PhaseToolCalls = 0
	s.ChargedToolUseIDs = nil
	s.UpcomingVisit = true
}

// SubmitPhase closes the open phase and opens its checkpoint (WAIT_USER).
// Illegal while a checkpoint is pending, no phase is open, the payload names a
// different phase, or a requirement change is open (CHANGE_OPEN).
//
// The gate stages the complete semantic snapshot (StagedSnapshot — hook-hashed
// artifacts, complete criterion meaning, frozen check defs, scope); the
// reducer pins it onto the pending checkpoint with acceptance PROVENANCE
// (source visit/checkpoint). Digests on sub.Artifacts are the HOOK-recomputed
// ones by contract (the gate replaced producer digests before allowing the
// call); a direct reducer caller bypassing the gate gets its digests stored
// as-is and the mint's freshness check will catch any lie.
func (r *RunState) SubmitPhase(sub PhaseSubmission, staged *StagedSnapshot) (CheckpointState, error) {
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
		// 0.7.0 D1: the structured conclusion is checkpoint content — stored in
		// its normalized form (a direct reducer caller gets the same
		// conservative shape the gate would have stored).
		added.Outcome, added.Conclusion = NormalizedConclusion(sub.Outcome, sub.Conclusion)
		added.Snapshot = s.buildPendingSnapshot(ph, &added, sub, staged)
		s.Checkpoints[len(s.Checkpoints)-1] = added
		s.Status = StatusWaitUser
		cp = added
		return nil
	})
	if err != nil {
		return CheckpointState{}, err
	}
	return cp, nil
}

// SubmitPhaseUnstaged is the legacy one-argument form used by reducer-level
// tests and direct callers: no gate staging, so the belt path in
// buildPendingSnapshot supplies honest default meaning for id-only criteria.
func (r *RunState) SubmitPhaseUnstaged(sub PhaseSubmission) (CheckpointState, error) {
	return r.SubmitPhase(sub, nil)
}

// buildPendingSnapshot assembles the COMPLETE pending semantics of one phase
// checkpoint from the gate's staged data plus run provenance. Reducer-side
// belt: any staged criterion id still lacking meaning gets the honest
// judgment fallback (never empty meaning, never invented semantics); the
// snapshot records where the phase visit came from and is completed by
// freezeAcceptance at acceptance time.
func (s *RunState) buildPendingSnapshot(ph *PhaseState, cp *CheckpointState, sub PhaseSubmission, staged *StagedSnapshot) FrozenView {
	view := FrozenView{
		Version:            s.RequirementsVersion,
		SourceVisitID:      ph.VisitID,
		SourceCheckpointID: cp.ID,
	}
	if staged != nil {
		view.Criteria = append([]Criterion(nil), staged.Criteria...)
		view.ArtifactSet = append([]ArtifactRef(nil), staged.ArtifactSet...)
		view.CheckDefs = append([]CheckDef(nil), staged.CheckDefs...)
		view.CheckScope = staged.CheckScope
		view.Requirements = requirementsFromProposal(staged.Requirements, ph, sub)
	} else {
		view.Requirements = requirementsFromProposal(sub.Requirements, ph, sub)
		view.ArtifactSet = append([]ArtifactRef(nil), sub.Artifacts...)
	}
	// Belt: every submitted criterion id carries complete meaning.
	known := make(map[string]bool, len(view.Criteria))
	for _, c := range view.Criteria {
		known[c.ID] = true
	}
	for _, id := range sub.CriteriaIDs {
		if known[id] {
			continue
		}
		if d, isDefault := defaultCriterion(id); isDefault {
			view.Criteria = append(view.Criteria, d)
			continue
		}
		view.Criteria = append(view.Criteria, Criterion{
			ID:       id,
			Method:   MethodJudgment,
			Required: true,
			Goal:     fmt.Sprintf("User-visible criterion %q (no model-authored goal was staged; judge against the accepted artifacts)", id),
			Expected: "The submitted artifacts satisfy the criterion as presented at the checkpoint",
			Scope:    "submitted artifacts",
		})
	}
	// Defaults merge, reducer-side belt: on a review-phase workflow the
	// COMPLETE shipped default criteria (plus readable manifest additions)
	// join every staged snapshot even when the gate staging is absent — the
	// gate rejects divergent redefinitions, the reducer guarantees presence.
	if WorkflowHasReviewPhase(s.Workflow) {
		for _, d := range DefaultReviewCriteria() {
			if !known[d.ID] {
				view.Criteria = append(view.Criteria, d)
				known[d.ID] = true
			}
		}
		if m, err := LoadRuntimeManifest(); err == nil && m != nil {
			for _, mc := range m.ReviewCriteria {
				if mc.ID != "" && !known[mc.ID] {
					view.Criteria = append(view.Criteria, mc)
					known[mc.ID] = true
				}
			}
		}
	}
	view.Environment = ObserveVerificationEnvironment(FrozenView{}.Environment)
	return view
}

// requirementsFromProposal normalizes the submission's proposed requirement
// meaning into FrozenRequirement staging. With no proposal, one requirement
// is synthesized for the phase (v2 maps phase→requirement 1:1) carrying the
// summary as content — the acceptance record never loses the phase's stated
// meaning.
func requirementsFromProposal(proposed []ProposedRequirement, ph *PhaseState, sub PhaseSubmission) []FrozenRequirement {
	var out []FrozenRequirement
	seen := make(map[string]bool)
	for _, p := range proposed {
		if p.ID == "" || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		content := p.Content
		if content == "" {
			content = sub.Summary
		}
		if content == "" && sub.Conclusion != nil {
			// 0.7.0 D1: a submission may state its meaning only as the
			// conclusion — the acceptance record must not lose it.
			content = sub.Conclusion.Summary
		}
		ids := p.CriteriaIDs
		if len(ids) == 0 {
			ids = sub.CriteriaIDs
		}
		out = append(out, FrozenRequirement{
			ID:          p.ID,
			Content:     content,
			Version:     p.Version,
			CriteriaIDs: ids,
			Artifacts:   p.Artifacts,
		})
	}
	if len(out) == 0 {
		paths := make([]string, 0, len(sub.Artifacts))
		for _, a := range sub.Artifacts {
			paths = append(paths, a.Path)
		}
		content := sub.Summary
		if content == "" {
			content = fmt.Sprintf("Phase %q acceptance (no summary submitted)", ph.ID)
		}
		out = append(out, FrozenRequirement{
			ID:          ph.ID,
			Content:     content,
			CriteriaIDs: append([]string(nil), sub.CriteriaIDs...),
			Artifacts:   paths,
		})
	}
	return out
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
// means "needs re-verification", and the user just re-verified it. An
// ACCEPTANCE also freezes the visit into the run's cumulative acceptance
// view (spec §3 冻结验收视图) inside the caller's commit.
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
		r.freezeAcceptance(p, cp)
		r.clearDroppedCriteria(p.AcceptedCriteria)
	} else {
		p.Outcome = "skipped"
	}
}

// freezeAcceptance records one accepted visit in the cumulative frozen
// acceptance view (spec §3: 需求被用户接受那一刻由 hooks 生成并冻结，生产者不
// 能裁剪). v2 maps phase→requirement 1:1: FrozenRequirement.ID is the phase id,
// the criteria are the visit's accepted criteria, and the artifact scope is
// the deciding checkpoint's artifact versions. Since the final-fix wave the
// frozen entry carries the COMPLETE semantics staged on the checkpoint
// (requirement content/version, artifact refs with hook-hashed digests,
// planned scope, provenance, the deciding answer's tool_use id) and the
// run-level view accumulates the criteria LEDGER (complete criterion meaning
// — proposed plus shipped defaults on review-phase workflows — merged per id,
// never truncating earlier accepted criteria), the cumulative artifact set,
// check defs and scope. The entry is REPLACED per requirement ID (the
// serialization-side last-entry-wins of ResolveFrozenView stays the belt for
// hand-assembled state), and the view's version is stamped with the
// RequirementsVersion at freeze time.
func (r *RunState) freezeAcceptance(p *PhaseState, cp *CheckpointState) {
	artifacts := make([]string, 0, len(cp.ArtifactVersions))
	for path := range cp.ArtifactVersions {
		artifacts = append(artifacts, path)
	}
	sort.Strings(artifacts)
	entry := FrozenRequirement{
		ID:                 p.ID,
		CriteriaIDs:        append([]string(nil), p.AcceptedCriteria...),
		Artifacts:          artifacts,
		SourceVisitID:      p.VisitID,
		SourceCheckpointID: cp.ID,
		AcceptedBy:         cp.DecisionToolUseID,
	}
	// Complete semantics from the staged snapshot: requirement content and
	// artifact refs, planned scope, criteria meaning.
	var content string
	var refs []ArtifactRef
	var planned []string
	if idx := indexOfRequirement(cp.Snapshot.Requirements, p.ID); idx >= 0 {
		content = cp.Snapshot.Requirements[idx].Content
		refs = cp.Snapshot.Requirements[idx].ArtifactRefs
		planned = cp.Snapshot.Requirements[idx].PlannedScope
		if len(cp.Snapshot.Requirements[idx].CriteriaIDs) > 0 {
			entry.CriteriaIDs = append([]string(nil), cp.Snapshot.Requirements[idx].CriteriaIDs...)
		}
	} else if len(cp.Snapshot.Requirements) > 0 {
		// A proposal named a different requirement id for this phase: freeze
		// its meaning under the phase's canonical id.
		content = cp.Snapshot.Requirements[0].Content
		refs = cp.Snapshot.Requirements[0].ArtifactRefs
		planned = cp.Snapshot.Requirements[0].PlannedScope
	}
	entry.Content = content
	entry.ArtifactRefs = refs
	entry.PlannedScope = planned
	if len(entry.ArtifactRefs) == 0 {
		entry.ArtifactRefs = cp.Snapshot.ArtifactSet
	}
	entry.Version = r.RequirementsVersion

	replaced := false
	for i := range r.Acceptance.Requirements {
		if r.Acceptance.Requirements[i].ID == p.ID {
			r.Acceptance.Requirements[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		r.Acceptance.Requirements = append(r.Acceptance.Requirements, entry)
	}
	// Criteria LEDGER: merge complete meaning per id. Earlier accepted
	// criteria are preserved (never silently discarded); a re-accepted id's
	// NEWEST staged meaning wins.
	for _, c := range cp.Snapshot.Criteria {
		merged := false
		for i := range r.Acceptance.Criteria {
			if r.Acceptance.Criteria[i].ID == c.ID {
				r.Acceptance.Criteria[i] = c
				merged = true
				break
			}
		}
		if !merged {
			r.Acceptance.Criteria = append(r.Acceptance.Criteria, c)
		}
	}
	// Cumulative artifact set: per-path replace (a re-accepted path pins its
	// newest hash; other paths stay).
	for _, a := range cp.Snapshot.ArtifactSet {
		replacedPath := false
		for i := range r.Acceptance.ArtifactSet {
			if r.Acceptance.ArtifactSet[i].Path == a.Path {
				r.Acceptance.ArtifactSet[i] = a
				replacedPath = true
				break
			}
		}
		if !replacedPath {
			r.Acceptance.ArtifactSet = append(r.Acceptance.ArtifactSet, a)
		}
	}
	// Check defs (digests recomputed at staging) and effective scope.
	for _, d := range cp.Snapshot.CheckDefs {
		merged := false
		for i := range r.Acceptance.CheckDefs {
			if r.Acceptance.CheckDefs[i].CheckID == d.CheckID {
				r.Acceptance.CheckDefs[i] = d
				merged = true
				break
			}
		}
		if !merged {
			r.Acceptance.CheckDefs = append(r.Acceptance.CheckDefs, d)
		}
	}
	r.Acceptance.CheckScope.Readable = mergeUnique(r.Acceptance.CheckScope.Readable, cp.Snapshot.CheckScope.Readable)
	r.Acceptance.CheckScope.CheckIDs = mergeUnique(r.Acceptance.CheckScope.CheckIDs, cp.Snapshot.CheckScope.CheckIDs)
	if cp.Snapshot.CheckScope.OutputSchema != "" {
		r.Acceptance.CheckScope.OutputSchema = cp.Snapshot.CheckScope.OutputSchema
	}
	r.Acceptance.Environment = ObserveVerificationEnvironment(cp.Snapshot.Environment)
	r.Acceptance.SourceCheckpointID = cp.ID
	r.Acceptance.AcceptedBy = cp.DecisionToolUseID
	r.Acceptance.Version = r.RequirementsVersion
}

func indexOfRequirement(reqs []FrozenRequirement, id string) int {
	for i := range reqs {
		if reqs[i].ID == id {
			return i
		}
	}
	return -1
}

func mergeUnique(into, from []string) []string {
	out := append([]string(nil), into...)
	for _, x := range from {
		if !containsID(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// clearDroppedCriteria removes drop-ledger entries whose criterion id was
// just re-accepted — a re-freeze supersedes an earlier drop of the same id
// (fix-round F3 ruling; same philosophy as M10's stale-clearing: the latest
// user decision about an id governs, so a re-accepted criterion returns to
// the envelope's expected set instead of staying silently dropped).
func (r *RunState) clearDroppedCriteria(criteria []string) {
	if len(r.CriteriaDrops) == 0 || len(criteria) == 0 {
		return
	}
	reaccepted := make(map[string]bool, len(criteria))
	for _, c := range criteria {
		reaccepted[c] = true
	}
	kept := r.CriteriaDrops[:0]
	for _, d := range r.CriteriaDrops {
		if !reaccepted[d.ID] {
			kept = append(kept, d)
		}
	}
	r.CriteriaDrops = kept
}

// RecordCriteriaDrop records ONE user-explicit criterion drop observed in a
// decision call's drop:/keep: ride-along (spec §3 冻结验收视图: 信封 criteria
// 集 = 冻结集 − 有记录的用户显式 drop——逐 id、逐次决定). Drops are an
// observation of the DECISION channel: legal only in WAIT_USER, at the
// CURRENT checkpoint (a superseded checkpoint is by definition not current),
// after its display was bound, and only for an id among that checkpoint's
// pending criteria ∪ the frozen acceptance view. The entry carries the
// platform tool_use_id of the answering call — the same binding decisions
// get. Re-dropping an id ALREADY dropped at this checkpoint is an idempotent
// no-op ((false, nil), nothing re-recorded — a replayed callback re-observes
// the same answer), and so is any callback whose tool_use_id already recorded
// this id's drop or concluded ANY decision (an answered call never records
// again — the T4 DecisionToolUseID precedent); a later checkpoint may drop
// the same id again only through a FRESH tool_use_id (a fresh human answer).
// This method never clears: a subsequent accepting decision re-accepts the
// criterion and clears the entry through decidePhase → clearDroppedCriteria.
func (r *RunState) RecordCriteriaDrop(checkpointID, criterionID, toolUseID string) (recorded bool, err error) {
	if criterionID == "" {
		return false, errors.New("RecordCriteriaDrop: criterion id is required")
	}
	if toolUseID == "" {
		return false, errors.New("RecordCriteriaDrop: tool_use_id is required (the drop binds to the answering AskUserQuestion call)")
	}
	recorded = false
	err = r.commit(func(s *RunState) error {
		if err := s.requireStatus(StatusWaitUser, "RecordCriteriaDrop"); err != nil {
			return err
		}
		cur := s.CurrentCheckpoint()
		if cur == nil {
			return errors.New("RecordCriteriaDrop: no open checkpoint")
		}
		if cur.ID != checkpointID {
			return fmt.Errorf("RecordCriteriaDrop: checkpoint %q is not the open checkpoint %q", checkpointID, cur.ID)
		}
		if !cur.Displayed {
			return fmt.Errorf("RecordCriteriaDrop: checkpoint %s is not displayed (a drop rides a display-verified ask)", checkpointID)
		}
		legal := containsID(cur.CriteriaIDs, criterionID)
		if !legal {
			for _, fr := range s.AcceptanceView().Requirements {
				if containsID(fr.CriteriaIDs, criterionID) {
					legal = true
					break
				}
			}
		}
		if !legal {
			return fmt.Errorf("RecordCriteriaDrop: criterion %q is neither among the checkpoint's criteria (%v) nor the frozen acceptance view", criterionID, cur.CriteriaIDs)
		}
		for _, d := range s.CriteriaDrops {
			if d.ID == criterionID && d.CheckpointID == checkpointID {
				return nil // per id per checkpoint: idempotent re-observation
			}
		}
		// Replay guards (fix-round I1, the DecisionToolUseID precedent): a
		// tool_use_id that already answered once can never record again — not
		// through a per-(id, tool_use_id) receipt (DURABLE: it survives the
		// re-acceptance that clears the live ledger, so a re-delivered
		// callback carrying an OLD human answer cannot re-drop a criterion
		// the user just re-accepted), and not through a decision it recorded.
		// The guard is deliberately per (id, tool_use_id): one call may still
		// record drops for SEVERAL ids.
		for _, rc := range s.DropReceipts {
			if rc.ID == criterionID && rc.ToolUseID == toolUseID {
				return nil // this call's answer for this id is already on record
			}
		}
		for i := range s.Checkpoints {
			if s.Checkpoints[i].DecisionToolUseID == toolUseID {
				return nil // the call concluded a decision; it cannot record more
			}
		}
		s.CriteriaDrops = append(s.CriteriaDrops, CriteriaDrop{
			ID:           criterionID,
			At:           nowUTC(),
			CheckpointID: checkpointID,
			ToolUseID:    toolUseID,
		})
		s.DropReceipts = append(s.DropReceipts, CriteriaDropReceipt{
			ID:        criterionID,
			ToolUseID: toolUseID,
			At:        nowUTC(),
		})
		recorded = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return recorded, nil
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

// planRecallTarget resolves a NAMED recall target (recovery round I4,
// adjudication §5): the label remainder of the "Recall: <target>" answer
// against the checkpoint's offered targets. "" and "latest" keep the §4
// default; an exact label re-enters exactly that offered visit; anything
// else is a decision rejection naming the offers — the target must be part
// of the digest-bound display the user actually decided on.
func (r *RunState) planRecallTarget(cp *CheckpointState, target string) (int, error) {
	if target == "" || target == "latest" {
		return r.planRecall(cp)
	}
	for _, t := range cp.RecallTargets {
		if t.Label == target {
			for i := range r.Phases {
				if r.Phases[i].VisitID == t.VisitID && r.Phases[i].ID == t.PhaseID {
					return i, nil
				}
			}
		}
	}
	labels := make([]string, 0, len(cp.RecallTargets))
	for _, t := range cp.RecallTargets {
		labels = append(labels, t.Label)
	}
	return 0, fmt.Errorf("RecordDecision: recall target %q is not an offered recall target of checkpoint %s (offered: %s; \"latest\" is the default)", target, cp.ID, strings.Join(labels, ", "))
}

// applyRecallAt applies a recall at the target visit (task-2.5 M11): the old
// visit keeps its full history with Outcome "recalled" (its acceptance is
// revoked), and a FRESH visit of the same id is appended open — checkpoint
// history stays complete and "the phase" resolves to the latest visit. Every
// later visit's criteria go stale. Recovery round I4 (§5): the record names
// the exact target visit and the human decision's provenance, and a recall
// ticks the run-rework budget.
func (r *RunState) applyRecallAt(cp *CheckpointState, targetIdx int, target, label, toolUseID string) {
	for i := targetIdx + 1; i < len(r.Phases); i++ {
		for _, c := range r.Phases[i].CriteriaIDs {
			r.addStale(c)
		}
		for _, c := range r.Phases[i].AcceptedCriteria {
			r.addStale(c)
		}
	}
	targetPhase := &r.Phases[targetIdx]
	targetPhase.Open = false
	targetPhase.Outcome = "recalled"
	reason := target
	if reason == "" {
		reason = "latest"
	}
	if label == "" {
		label = "Recall: " + reason
	}
	freshVisitID := r.nextVisitID(targetPhase.ID)
	r.Phases = append(r.Phases, PhaseState{ID: targetPhase.ID, VisitID: freshVisitID, Open: true, OpenedAt: nowUTC()})
	r.Counters.PhaseToolCalls = 0 // a redone phase gets a fresh tool budget
	r.Counters.RunRework++        // a recall IS rework (§5 work-budget visits)
	r.Recalls = append(r.Recalls, RecallRecord{
		TargetVisitID: targetPhase.VisitID,
		Provenance:    "ask:" + cp.ID + "/" + toolUseID,
		Reason:        reason,
		Label:         label,
		FromPhaseID:   cp.PhaseID,
		TargetPhaseID: targetPhase.ID,
		At:            nowUTC(),
	})
	r.Status = StatusActive
}

// nextVisitID mints the durable visit id <phase>@v<n> — n counts this run's
// visits of the same phase id INCLUDING the one being appended (call BEFORE
// the append; v1 is the first).
func (r *RunState) nextVisitID(phaseID string) string {
	n := 0
	for i := range r.Phases {
		if r.Phases[i].ID == phaseID {
			n++
		}
	}
	return fmt.Sprintf("%s@v%d", phaseID, n+1)
}

// terminalReviewSettled reports whether the mandatory terminal ⑤ (spec §0
// item 2, ruling 72) is discharged for a DONE: either the review phase's
// latest visit was explicitly skipped (UNREVIEWED), or a review concluded
// REVIEWED with a PASS verdict that still covers the CURRENT acceptance
// view — freshness by re-mint digest equality, so a PASS over an older,
// since-superseded view is stale and does not discharge the debt.
func (s *RunState) terminalReviewSettled() bool {
	var latestReviewVisit *PhaseState
	for i := range s.Phases {
		if s.Phases[i].ID == "review" {
			latestReviewVisit = &s.Phases[i]
		}
	}
	if latestReviewVisit != nil && latestReviewVisit.Outcome == "skipped" {
		return true
	}
	// Fresh-mint the CURRENT view and compare content digests — the same
	// authority RequestReview/RecordReviewResult pin (final-fix C1).
	var registry []CheckDef
	if m, err := LoadRuntimeManifest(); err == nil && m != nil {
		registry = m.CheckRegistry
	}
	fresh, err := MintEnvelope(s, Envelope{SchemaVersion: "2", RunID: s.RunID, ReviewID: "terminal-debt-check"}, registry)
	if err != nil {
		return false // nothing freezable now — no review could be current
	}
	freshDigest := envelopeContentDigest(fresh)
	for i := range s.Reviews {
		rv := &s.Reviews[i]
		if rv.Status == ReviewReviewed && rv.Result != nil && rv.Result.Verdict == VerdictPass &&
			rv.EnvelopeDigest == freshDigest {
			return true
		}
	}
	return false
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
// checkpoint only CONTINUE/RECALL/STOP (B3); a review-debt checkpoint only
// REVIEW_ON_DEMAND/SKIP/CONTINUE/STOP (no done — the debt is what done
// could not discharge); everything else takes the full menu except
// ACCEPT_REQUIREMENT_CHANGE.
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
	case CheckpointReviewDebt:
		switch d {
		case DecisionReviewOnDemand, DecisionSkip, DecisionContinue, DecisionStop:
		default:
			return fmt.Errorf("%s illegal at review-debt checkpoint (want REVIEW, SKIP, CONTINUE or STOP — done cannot discharge the mandatory terminal review)", d)
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
//
// toolUseID binds the decision to the platform AskUserQuestion call that
// carried it (spec §7): a replayed callback with the same id is an idempotent
// no-op (nil, nothing re-recorded — checked BEFORE the status gate, since the
// original decision may have already moved the run off WAIT_USER); a
// different id on an already-decided checkpoint is an error — a second
// callback never overwrites a recorded decision. Direct (non-platform)
// callers may pass "" — replays of "" are idempotent the same way.
func (r *RunState) RecordDecision(checkpointID string, d Decision, toolUseID string) error {
	return r.RecordDecisionWithTarget(checkpointID, d, toolUseID, "")
}

// RecordDecisionWithTarget applies a decision carrying its named target
// (recovery round I4, adjudication §5): the remainder of a "Recall: <target>"
// answer resolves against the checkpoint's OFFERED recall targets — the
// digest-bound provenance of what the user actually saw. "" and "latest" keep
// the default (most recent accepted prior phase); an exact label re-enters
// exactly that visit; anything else is a decision rejection, never a guess.
// Non-recall decisions ignore the target.
func (r *RunState) RecordDecisionWithTarget(checkpointID string, d Decision, toolUseID, target string) error {
	return r.commit(func(s *RunState) error {
		return s.applyDecision(checkpointID, d, toolUseID, target)
	})
}

// RecordDecisionWithBudget applies a decision AND its same-callback budget
// settlement (T18 controller clarification 3) in ONE commit: the settlement
// and the decision persist atomically — a validation failure or a failed Save
// leaves NEITHER applied (state authority never half-applies one human
// callback). settle == nil degrades to RecordDecision exactly.
func (r *RunState) RecordDecisionWithBudget(checkpointID string, d Decision, toolUseID string, settle *BudgetSettlement) error {
	return r.RecordDecisionWithBudgetTarget(checkpointID, d, toolUseID, "", settle)
}

// RecordDecisionWithBudgetTarget is the named-target form (recovery round
// I4): settlement and a "Recall: <target>" decision still land in ONE commit.
func (r *RunState) RecordDecisionWithBudgetTarget(checkpointID string, d Decision, toolUseID, target string, settle *BudgetSettlement) error {
	return r.commit(func(s *RunState) error {
		if settle != nil {
			if _, err := s.applyBudgetSettlement(settle); err != nil {
				return err
			}
		}
		return s.applyDecision(checkpointID, d, toolUseID, target)
	})
}

// applyDecision is the decision core shared by RecordDecision and
// RecordDecisionWithBudget — everything the landed RecordDecision validated,
// unchanged, running against the commit snapshot s.
func (s *RunState) applyDecision(checkpointID string, d Decision, toolUseID, target string) error {
	// Replay idempotency first: it must not depend on the current status.
	for i := range s.Checkpoints {
		cp := &s.Checkpoints[i]
		if cp.ID != checkpointID || !cp.Decided {
			continue
		}
		if cp.DecisionToolUseID == toolUseID {
			if cp.Decision == d {
				return nil // same callback replayed: idempotent no-op
			}
			return fmt.Errorf("RecordDecision: checkpoint %s already decided %s via %q — a conflicting replay (%s) is rejected", checkpointID, cp.Decision, cp.DecisionToolUseID, d)
		}
		return fmt.Errorf("RecordDecision: checkpoint %s already decided via tool_use %q — callback %q cannot overwrite it", checkpointID, cp.DecisionToolUseID, toolUseID)
	}
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
	// anything changed since (note, criteria, phase, requirements version,
	// budget offers), the render is stale — a new display pass is required.
	if CheckpointContentDigest(*cp, s.RequirementsVersion) != cp.DisplayDigest {
		return fmt.Errorf("RecordDecision: checkpoint %s content changed since display — re-render required", checkpointID)
	}
	if err := validDecision(d); err != nil {
		return err
	}
	if err := legalDecisionForKind(cp.Kind, d); err != nil {
		return fmt.Errorf("RecordDecision: %s", err)
	}
	// m4 (fix round): a [v] with NOTHING frozen would park the run in an
	// unrecoverable REVIEW_PENDING — obligation recorded, no open
	// checkpoint, phase-submit/change/review-submit all refused, and the
	// vacuous envelope can never cite anything. Refuse the [v] itself; the
	// checkpoint stays open for continue:/skip: and the [v] becomes legal
	// as soon as an acceptance freezes the view.
	if d == DecisionReviewOnDemand && len(s.AcceptanceView().Requirements) == 0 {
		return fmt.Errorf("RecordDecision: REVIEW_ON_DEMAND refused — the frozen acceptance view is empty (nothing accepted yet, so no review can be scoped and the envelope channel has nothing to cite); continue: or skip: this checkpoint first — [v] is legal once an acceptance freezes the view")
	}
	// Pre-validate every effect before mutating anything.
	recallIdx := -1
	if d == DecisionRecall {
		var err error
		if recallIdx, err = s.planRecallTarget(cp, target); err != nil {
			return err
		}
	}
	if d == DecisionAcceptRequirementChange && s.Change == nil {
		return fmt.Errorf("RecordDecision: no requirement change open for checkpoint %s", checkpointID)
	}

	cp.Decided = true
	cp.Decision = d
	cp.DecidedAt = nowUTC()
	cp.DecisionToolUseID = toolUseID
	switch d {
	case DecisionContinue:
		// Recovery round C3: CONTINUE on a retry-pending review-result
		// checkpoint IS the user's funded same-envelope retry authorization
		// (§3 line 38 — no new decision channel); budget extension rides the
		// same answer through the settlement that already applied.
		if cp.ReviewRetry != nil {
			return s.rotateReviewAttempt(cp)
		}
		if cp.Kind == CheckpointRecovery {
			// Accept the status quo; restore the episode prior (B3).
			s.restoreRecoveryPrior()
		} else {
			s.decidePhase(cp, true)
			// I1 (adjudication §5): the decision authorizes the next visit —
			// pre-provider work charges the upcoming bucket; OpenPhase adopts.
			s.newVisitBucket()
			s.Status = StatusActive
		}
	case DecisionSkip:
		// Recovery round C3: SKIP on a retry-pending review-result checkpoint
		// declines the retry — the review ends visibly UNREVIEWED and work
		// resumes (the mandatory terminal ⑤ debt machinery re-engages at
		// DONE: SKIPPED never settles it).
		if cp.ReviewRetry != nil {
			return s.skipReviewRetry(cp)
		}
		// At the review-debt checkpoint, skip IS the explicit UNREVIEWED
		// finish (spec §0 item 2: 可显式跳过 → UNREVIEWED) — the debt is
		// discharged as unreviewed, visibly, and the run closes through the
		// normal post-game path.
		if cp.Kind == CheckpointReviewDebt {
			s.decidePhase(cp, false)
			s.Status = StatusPostGamePending
			return nil
		}
		s.decidePhase(cp, false)
		// I1: like continue, a skip that returns the run to ACTIVE authorizes
		// the next visit's bucket (post-game skips never reach here).
		s.newVisitBucket()
		s.Status = StatusActive
	case DecisionDone:
		// M4 / spec §0 item 2 (ruling 72, controller QA-2): on a workflow
		// WITH a fixed review phase, a DONE whose terminal ⑤ is unsettled
		// may not silently enter post-game. Unsettled = no concluded PASS
		// review over the CURRENT acceptance view AND no explicit skip. The
		// run parks at a REVIEW_DEBT checkpoint instead — the positive
		// completion path stays reachable without ever fabricating PASS.
		if WorkflowHasReviewPhase(s.Workflow) && !s.terminalReviewSettled() {
			s.decidePhase(cp, true)
			if _, err := s.addCheckpoint(CheckpointReviewDebt, "", "mandatory terminal review unsettled — review ([v]), skip to finish UNREVIEWED, or continue working", nil, nil); err != nil {
				return err
			}
			s.Status = StatusWaitUser
			return nil
		}
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
		s.applyRecallAt(cp, recallIdx, target, "", toolUseID)
		s.RecoveryPrior = "" // a recall supersedes the episode's restore
	case DecisionReviewOnDemand:
		// B2: park the run under a persisted obligation — no work may
		// open until the matching review is requested and concluded.
		// New#2 (recovery round): pin the source checkpoint's DISPLAYED
		// pending snapshot at [v] time — the owed review reviews what the
		// user saw, not whatever the state becomes by request time.
		s.ReviewObligation = &ReviewObligation{
			Snapshot:           cp.Snapshot,
			SourceCheckpointID: cp.ID,
			PhaseID:            cp.PhaseID,
			At:                 nowUTC(),
		}
		s.Status = StatusReviewPending
	case DecisionStop:
		// F4 (re-review): STOP on a retry-pending checkpoint settles the
		// review the way SKIP does — visibly UNREVIEWED — instead of leaving
		// an in-flight review dangling over a stopped run. The stop itself
		// stands (state kept, exit journal is the caller's duty).
		if cp.ReviewRetry != nil {
			if err := s.stopReviewRetry(cp); err != nil {
				return err
			}
		}
		s.Status = StatusPostGamePending // minimal exit journal is the caller's duty
		s.RecoveryPrior = ""
	case DecisionAcceptRequirementChange:
		if err := s.resumeAfterChange(); err != nil { // validated above
			return err
		}
	}
	return nil
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
// The runtime channel (summoner:change) has no DevPosition writer of its
// own, so this form freezes whatever the live state carries — direct callers
// that can supply a real position use OpenRequirementChangeAt.
func (r *RunState) OpenRequirementChange(note string) error {
	return r.openRequirementChange(note, r.DevPosition)
}

// OpenRequirementChangeAt opens a requirement change with an EXPLICIT
// development position — the form the summoner:change Skill payload drives:
// the position is model-proposed CONTENT, never authorization (status
// legality is unchanged). A non-empty current_task is required — the freeze
// is the change loop's whole restore story, and an always-empty position
// (what the live DevPosition is in real runs today) would freeze nothing.
// The supplied position is deep-copied and captured together with the
// open/supersede in ONE commit snapshot: a failed Save leaves memory and
// disk unchanged (T10 controller addendum).
func (r *RunState) OpenRequirementChangeAt(note string, position DevPosition) error {
	if position.CurrentTask == "" {
		return errors.New("OpenRequirementChangeAt: position.current_task is required — freeze a real development position, not an empty one")
	}
	return r.openRequirementChange(note, position)
}

// openRequirementChange is the shared open path: legality checks, the
// checkpoint supersede for a WAIT_USER entry, and the change context with
// the deep-copied position — all inside a single commit.
func (r *RunState) openRequirementChange(note string, position DevPosition) error {
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
			SavedPosition: copyDevPosition(position),
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
		MenuPages:        MenuProjection(src.Kind),
		// 0.7.0 D1: the interrupted question is re-asked against the new
		// baseline — the conclusion the user is asked to judge travels with it.
		Outcome:    src.Outcome,
		Conclusion: cloneConclusion(src.Conclusion),
	}
	// T18 (0.6.1): a superseded checkpoint's budget ask is INERT legacy state —
	// no new ask is ever created, and the render no longer displays one. The
	// carry keeps pre-0.6.1 state byte-stable across a supersede (digest
	// continuity) and nothing reads it.
	if src.BudgetAsk != nil && !r.BudgetConfirmed {
		cp.BudgetAsk = src.BudgetAsk
	}
	if src.ExtendAsk != nil {
		cp.ExtendAsk = src.ExtendAsk
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

// envelopeContentDigest is the CONTENT digest of a MINTED review envelope:
// sha256 over the canonical bytes of everything EXCEPT review_id — the spec §3
// reuse triple's envelope half (artifact_set + requirements/exit_criteria +
// verification scope/environment, plus the per-run-constant
// schema_version/run_id). review_id is the review INSTANCE key and is
// excluded on purpose: a later ⑤ request that mints a FRESH review id over
// identical content still matches the stored digest, which is what makes
// ReusableReview fire (同版本验收复用). The same-envelope re-send check loses
// nothing: it resolves the record by review_id FIRST (findReview), the
// digest then pins the content.
func envelopeContentDigest(env SemanticEnvelope) string {
	env.ReviewID = ""
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

// FindReview is the exported lookup of one review record by id (read-only).
func (r *RunState) FindReview(reviewID string) *ReviewRecord { return r.findReview(reviewID) }

// RequestReview registers a review from an IDENTITY-ONLY registration
// ({schema_version, run_id, review_id} — the strict wire form of the
// final-fix wave). The COMPLETE envelope is MINTED here from the run's pinned
// acceptance snapshot (minus recorded drops) and stored on the record; no
// producer-supplied semantic exists anywhere on this path. Minting re-hashes
// CURRENT artifact bytes and recomputes check-definition digests — drift
// refuses the registration (an updated VISIBLE proposal is required, not a
// silent substitution).
//
// The status gate comes FIRST (task-2.5 M1a): a NEW review requires ACTIVE
// (the 终局⑤ path) — or REVIEW_PENDING under a [v] obligation (B2), which
// the request consumes into the record — and a same-envelope re-send is legal
// only in REVIEW_PENDING while that review is PREPARED/RUNNING (spec §3 崩溃
// 恢复). CLOSED, WAIT_USER and OUTCOME_UNKNOWN refuse everything. A
// different envelope or a concluded review id is refused, and a new review id
// with nothing authorizing it is refused.
//
// Every record starts with one append-only ATTEMPT receipt in PREPARED state
// (final-fix wave C3): the dispatch site (StartDelegation) fills in the
// admitted parent Agent tool_use id; ownership binds the child's INITIAL
// platform record to the EXACT capsule bytes of exactly that attempt.
func (r *RunState) RequestReview(identity Envelope, registry []CheckDef) (string, error) {
	// Pure input validation first.
	if identity.SchemaVersion == "" {
		return "", errors.New("RequestReview: envelope schema_version is required")
	}
	if identity.RunID != r.RunID {
		return "", fmt.Errorf("RequestReview: envelope run_id %q does not match run %q", identity.RunID, r.RunID)
	}
	if identity.ReviewID == "" {
		return "", errors.New("RequestReview: envelope review_id is required")
	}

	// M1a status gate before the fast path.
	if r.Status != StatusActive && r.Status != StatusReviewPending {
		return "", fmt.Errorf("RequestReview: illegal status %s (want ACTIVE for a new review, or REVIEW_PENDING for a same-envelope re-send / an outstanding [v] obligation)", r.Status)
	}

	// Recovery round C3 (§3 line 37): the recovery form — the identity
	// triple plus strict expected_attempt_id — is the wedged-attempt
	// recovery operation, budget-exempt, on the EXISTING channel. It is not
	// a registration: no envelope minting, no re-send semantics.
	if identity.ExpectedAttemptID != "" {
		if err := r.RecoverReview(identity.ReviewID, identity.ExpectedAttemptID); err != nil {
			return "", fmt.Errorf("RequestReview (recovery): %v", err)
		}
		return identity.ReviewID, nil
	}

	if rv := r.findReview(identity.ReviewID); rv != nil {
		// Re-send of a registered review: only mid-flight, only in
		// REVIEW_PENDING, only the content-identical envelope (the record is
		// resolved by review_id first; the stored content digest then pins
		// everything else). The mint doubles as the drift check: re-sending
		// over drifted artifacts/files refuses.
		if err := r.requireStatus(StatusReviewPending, "RequestReview"); err != nil {
			return "", err
		}
		if rv.Status != ReviewPrepared && rv.Status != ReviewRunning {
			return "", fmt.Errorf("RequestReview: review %s already concluded (%s) — use a new review id", identity.ReviewID, rv.Status)
		}
		env, err := MintEnvelope(r, identity, registry)
		if err != nil {
			return "", fmt.Errorf("RequestReview: %v", err)
		}
		if rv.EnvelopeDigest != envelopeContentDigest(env) {
			return "", fmt.Errorf("RequestReview: review %s already registered with a different envelope", identity.ReviewID)
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
	// Mint BEFORE the commit: a mint failure must leave state untouched.
	env, err := MintEnvelope(r, identity, registry)
	if err != nil {
		return "", fmt.Errorf("RequestReview: %v", err)
	}
	if err := ValidateEnvelope(env, r.AcceptanceView(), r.CriteriaDrops, registry); err != nil {
		return "", fmt.Errorf("RequestReview: %v", err)
	}
	contentDigest := envelopeContentDigest(env)
	artifactDigest := ArtifactSetDigest(env)
	attemptID := fmt.Sprintf("%s-a1", identity.ReviewID)
	token, err := newNonce()
	if err != nil {
		return "", fmt.Errorf("RequestReview: attempt token: %w", err)
	}
	defaultDebt := WorkflowHasReviewPhase(r.Workflow)
	err = r.commit(func(s *RunState) error {
		// DefaultDebt marks the mandatory default ⑤ obligation (M4): only a
		// workflow WITH a review phase mints it — fix/new/review/ship; ops and
		// release have no review phase, so no UNREVIEWED debt and normal
		// post-game closure (controller QA-2).
		rec := ReviewRecord{
			ReviewID:        identity.ReviewID,
			Status:          ReviewPrepared,
			Envelope:        env,
			EnvelopeDigest:  contentDigest,
			ArtifactDigest:  artifactDigest,
			ActiveAttemptID: attemptID,
			Attempts: []ReviewAttempt{{
				AttemptID:    attemptID,
				Token:        token,
				State:        "PREPARED",
				DispatchedAt: nowUTC(),
			}},
			DefaultDebt: defaultDebt,
			RequestedAt: nowUTC(),
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
	return identity.ReviewID, nil
}

// RecoverReview is the wedged-RUNNING-attempt recovery operation (recovery
// round C3, adjudication §3 line 37): re-send no-ops, a RUNNING review has no
// servable dispatch (StartDelegation needs PREPARED) and new review ids are
// refused — a RUNNING review whose child died with no result callback was an
// unrecoverable production state. The recovery is USER-initiated through the
// review-submit recovery form (strict review_id + expected_attempt_id,
// budget-exempt) and parks the review at an EXISTING-KIND review-result
// checkpoint carrying the ReviewRetry obligation. The attempt stays RUNNING:
// its late result may still conclude at exactly this checkpoint (§3 line 39).
// Retirement is the rotation's atomic step, not the recovery's.
func (r *RunState) RecoverReview(reviewID, expectedAttemptID string) error {
	if expectedAttemptID == "" {
		return errors.New("RecoverReview: expected_attempt_id is required (strict recovery metadata — never a guess)")
	}
	return r.commit(func(s *RunState) error {
		if err := s.requireStatus(StatusReviewPending, "RecoverReview"); err != nil {
			return err
		}
		rv := s.findReview(reviewID)
		if rv == nil {
			return fmt.Errorf("RecoverReview: unknown review %q", reviewID)
		}
		if rv.Status != ReviewRunning {
			return fmt.Errorf("RecoverReview: review %s is %s, not RUNNING — the recovery targets a dispatched attempt whose result never arrived (a PREPARED review is already dispatchable; a concluded review needs a new review id)", reviewID, rv.Status)
		}
		att := rv.ActiveAttempt(rv.ActiveAttemptID)
		if att == nil {
			return fmt.Errorf("RecoverReview: review %s has no active attempt", reviewID)
		}
		if att.AttemptID != expectedAttemptID {
			return fmt.Errorf("RecoverReview: expected_attempt_id %q does not match the active attempt %q — strict metadata pins exactly the wedged attempt", expectedAttemptID, att.AttemptID)
		}
		if att.State != "RUNNING" {
			return fmt.Errorf("RecoverReview: attempt %s is %s, not RUNNING — nothing is wedged", att.AttemptID, att.State)
		}
		cp, err := s.addCheckpoint(CheckpointReviewResult, "", "RUNNING review attempt produced no result — retry the same envelope (funded) or finish the review UNREVIEWED", nil, nil)
		if err != nil {
			return err
		}
		// addCheckpoint returns a value copy: stamp the obligation on the
		// STORED checkpoint (recovery-round fix — copy mutation never
		// persisted checkpoint side data).
		stored := &s.Checkpoints[len(s.Checkpoints)-1]
		if stored.ID != cp.ID { // belt: never stamp the wrong element
			return fmt.Errorf("RecoverReview: checkpoint index drift for %s", cp.ID)
		}
		stored.ReviewID = rv.ReviewID
		stored.ReviewDisposition = "RETRY_PENDING"
		stored.ReviewRetry = &ReviewRetry{
			ReviewID:       rv.ReviewID,
			AttemptID:      att.AttemptID,
			EnvelopeDigest: rv.EnvelopeDigest,
			ResumeTarget:   "active",
			RetryLabel:     "Continue: retry the review — one funded same-envelope attempt",
			SkipLabel:      "Skip: finish this review UNREVIEWED and resume work",
		}
		s.Status = StatusWaitUser
		return nil
	})
}

// rotateReviewAttempt is the funded same-envelope retry (§3 line 38):
// CONTINUE on a retry checkpoint atomically retires A, mints PREPARED B
// under the SAME pinned envelope, consumes the obligation and returns the
// run to REVIEW_PENDING so the gate dispatches B (admitted/charged once —
// StartDelegation owns that charge). No free Agent and no invented budget
// number: after any riding settlement the delegation limit must cover the
// retry, else the decision is refused and the checkpoint stays decidable.
func (s *RunState) rotateReviewAttempt(cp *CheckpointState) error {
	rr := cp.ReviewRetry
	rv := s.findReview(rr.ReviewID)
	if rv == nil {
		return fmt.Errorf("RecordDecision: retry obligation names unknown review %s", rr.ReviewID)
	}
	if rv.Status != ReviewRunning {
		return fmt.Errorf("RecordDecision: retry obligation names review %s in state %s, not RUNNING", rr.ReviewID, rv.Status)
	}
	if l := s.Limits.Delegations; l > 0 && s.Counters.Delegations >= l {
		return fmt.Errorf("RecordDecision: the retry needs delegation budget (%d/%d used) — extend the budget with this answer or skip the retry; no free Agent and no invented budget", s.Counters.Delegations, l)
	}
	att := rv.ActiveAttempt(rv.ActiveAttemptID)
	if att == nil || att.AttemptID != rr.AttemptID || att.State != "RUNNING" {
		return fmt.Errorf("RecordDecision: retry obligation pins attempt %s, which is no longer the RUNNING active attempt %s", rr.AttemptID, rv.ActiveAttemptID)
	}
	now := nowUTC()
	att.State = "RETIRED"
	att.RetiredAt = now
	token, err := newNonce()
	if err != nil {
		return fmt.Errorf("RecordDecision: retry attempt token: %w", err)
	}
	bID := fmt.Sprintf("%s-a%d", rv.ReviewID, len(rv.Attempts)+1)
	rv.Attempts = append(rv.Attempts, ReviewAttempt{
		AttemptID:    bID,
		Token:        token,
		State:        "PREPARED",
		DispatchedAt: now,
	})
	rv.ActiveAttemptID = bID
	rv.Status = ReviewPrepared
	cp.ReviewRetry = nil // consumed: one authorization, one attempt
	s.Status = StatusReviewPending
	return nil
}

// stopReviewRetry settles a pending retry obligation on STOP (re-review F4):
// the review ends visibly UNREVIEWED — SKIPPED, no verdict, attempt retired —
// never dangling in flight over a stopped run. SKIPPED ≠ settled for the
// terminal ⑤ debt (a resumed run re-engages the debt machinery). Unlike
// skip, the run does not resume work: the caller's STOP routing stands.
func (s *RunState) stopReviewRetry(cp *CheckpointState) error {
	rr := cp.ReviewRetry
	rv := s.findReview(rr.ReviewID)
	if rv == nil {
		return fmt.Errorf("RecordDecision: retry obligation names unknown review %s", rr.ReviewID)
	}
	if rv.Status != ReviewRunning {
		return fmt.Errorf("RecordDecision: stop of a retry obligation whose review %s is %s, not RUNNING", rr.ReviewID, rv.Status)
	}
	if att := rv.ActiveAttempt(rv.ActiveAttemptID); att != nil && att.State == "RUNNING" {
		att.State = "RETIRED"
		att.RetiredAt = nowUTC()
	}
	rv.Status = ReviewSkipped
	rv.ConcludedAt = nowUTC()
	cp.ReviewRetry = nil
	return nil
}

// skipReviewRetry declines the pending retry (§3 line 38's sibling): the
// review concludes visibly UNREVIEWED (terminal SKIPPED, no verdict — never
// a fabricated PASS, and terminalReviewSettled keeps refusing: SKIPPED ≠
// settled) and the run resumes work at the obligation's target.
func (s *RunState) skipReviewRetry(cp *CheckpointState) error {
	rr := cp.ReviewRetry
	rv := s.findReview(rr.ReviewID)
	if rv == nil {
		return fmt.Errorf("RecordDecision: retry obligation names unknown review %s", rr.ReviewID)
	}
	if rv.Status != ReviewRunning {
		return fmt.Errorf("RecordDecision: skip of a retry obligation whose review %s is %s, not RUNNING", rr.ReviewID, rv.Status)
	}
	if att := rv.ActiveAttempt(rv.ActiveAttemptID); att != nil && att.State == "RUNNING" {
		att.State = "RETIRED"
		att.RetiredAt = nowUTC()
	}
	rv.Status = ReviewSkipped
	rv.ConcludedAt = nowUTC()
	cp.ReviewRetry = nil
	s.decidePhase(cp, false)
	// Work resumes: the next visit's bucket is authorized (I1 semantics).
	s.newVisitBucket()
	s.Status = StatusActive
	return nil
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
// moves the active PREPARED attempt to RUNNING and stamps the ATTEMPT
// RECEIPT with the admitted parent Agent tool_use id (C3: append-only
// receipts — the dispatch site owns the binding, the id is the genuine
// PreToolUse payload key), and every delegation counts against the
// delegation budget and the phase tool budget — all validated first, then
// persisted in ONE Save (review round 2, R1). The delegation budget is ALSO
// enforced here (task-2.5 14a): the gate pre-checks it, but direct reducer
// callers must not be able to exceed the limit either. A reviewer delegation
// is legal only in REVIEW_PENDING with that review's ACTIVE attempt
// PREPARED; a worker delegation only in ACTIVE.
func (r *RunState) StartDelegation(reviewID string, isReviewer bool, parentToolUseID string) error {
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
			att := rv.ActiveAttempt(rv.ActiveAttemptID)
			if att == nil {
				return fmt.Errorf("StartDelegation: review %s has no attempt %q to dispatch", reviewID, rv.ActiveAttemptID)
			}
			if att.State != "PREPARED" {
				return fmt.Errorf("StartDelegation: attempt %s is %s, not PREPARED", att.AttemptID, att.State)
			}
			if parentToolUseID == "" {
				return errors.New("StartDelegation: a reviewer dispatch requires the admitted parent Agent tool_use_id (the attempt receipt's ownership key)")
			}
			if att.ParentToolUseID != "" && att.ParentToolUseID != parentToolUseID {
				return fmt.Errorf("StartDelegation: attempt %s already bound to parent tool_use %s", att.AttemptID, att.ParentToolUseID)
			}
			att.ParentToolUseID = parentToolUseID
			att.State = "RUNNING"
			rv.Status = ReviewRunning
			rv.StartedAt = nowUTC()
		} else {
			if err := s.requireStatus(StatusActive, "StartDelegation"); err != nil {
				return err
			}
		}
		s.Counters.Delegations++
		s.CountToolCall(parentToolUseID)
		return nil
	})
}

// ActiveAttempt returns the attempt receipt with the given id, or nil.
func (rv *ReviewRecord) ActiveAttempt(attemptID string) *ReviewAttempt {
	for i := range rv.Attempts {
		if rv.Attempts[i].AttemptID == attemptID {
			return &rv.Attempts[i]
		}
	}
	return nil
}

// RecordReviewResult concludes the in-flight review and opens a review-result
// checkpoint (PASS still leaves the run WAIT_USER — spec §3). Enum violations
// and empty-evidence PASS results are protocol errors: the review is marked
// FAILED, the failure routes to a REVIEW_RESULT checkpoint so the run stays
// recoverable, the routing IS persisted, and an error is returned to the
// caller. BLOCKED and the quality verdict are mutually exclusive
// (task-2.5 M12): BLOCKED must not pose as a quality conclusion and COMPLETED
// must not carry a reason code. NEEDS-FIX consumes run rework budget; BLOCKED
// does not.
func (r *RunState) RecordReviewResult(res ReviewResult) error {
	// protocolErr carries the problem out AFTER the routing persisted —
	// returning it from the closure would roll the routing back.
	var protocolErr error
	err := r.commit(func(s *RunState) error {
		// Recovery round C3 (§3 line 39): attempt A may still conclude while
		// the run parks at ITS OWN still-undecided ReviewRetry checkpoint —
		// one commit validates the result, supersedes the retry checkpoint
		// and opens a fresh result display. Any other WAIT_USER conclusion
		// still refuses (the outer gate below).
		atRetry := false
		if s.Status == StatusWaitUser {
			if cur := s.CurrentCheckpoint(); cur != nil && cur.Kind == CheckpointReviewResult && cur.ReviewRetry != nil && cur.ReviewRetry.ReviewID == res.ReviewID {
				atRetry = true
			}
		}
		if !atRetry {
			if err := s.requireStatus(StatusReviewPending, "RecordReviewResult"); err != nil {
				return err
			}
		}
		rv := s.findReview(res.ReviewID)
		if rv == nil {
			return fmt.Errorf("RecordReviewResult: unknown review %q", res.ReviewID)
		}
		if rv.Status != ReviewPrepared && rv.Status != ReviewRunning {
			return fmt.Errorf("RecordReviewResult: review %s already concluded (%s) — late results must not affect a new generation", res.ReviewID, rv.Status)
		}
		if atRetry {
			// The fresh display (verdict or FAILED routing) that follows
			// supersedes the retry window — CurrentCheckpoint skips it.
			if cur := s.CurrentCheckpoint(); cur != nil && cur.ReviewRetry != nil {
				cur.Superseded = true
			}
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
		if problem == "" {
			// 空证据 (spec §3): a PASS-track result with no evidence ids is a
			// rubber-stamp — same protocol-error routing as the enum checks.
			if err := ValidateEvidence(res); err != nil {
				problem = err.Error()
			}
		}
		// C1b: the claimed artifact digest is COMPARED, never stored blind,
		// and CURRENT artifact bytes must still match the pinned snapshot
		// (freshness at conclusion — 过期摘要 = review.failed).
		if problem == "" {
			if want := ArtifactSetDigest(rv.Envelope); res.ArtifactDigest != want {
				problem = fmt.Sprintf("artifact_digest %q does not match the minted envelope's artifact set digest %s", res.ArtifactDigest, want)
			} else if err := VerifyArtifactFreshness(rv.Envelope.ArtifactSet); err != nil {
				problem = err.Error()
			}
		}
		// C1b: exact all-active-criterion coverage against the MINTED
		// envelope — wrong/missing/duplicate/foreign criterion ids, bad
		// enums, aggregate/verdict contradictions and optional-blocked
		// laundering are all protocol errors.
		if problem == "" {
			if p := ValidateCriterionVerdicts(rv.Envelope, res); p != "" {
				problem = p
			}
		}
		// C3: a late RETIRED attempt's result never concludes the successor —
		// only the ACTIVE attempt's result does.
		if problem == "" {
			if res.AttemptID == "" {
				res.AttemptID = rv.ActiveAttemptID
			}
			if res.AttemptID != rv.ActiveAttemptID {
				if att := rv.ActiveAttempt(res.AttemptID); att != nil {
					// F2 (adjudication §3): the id names a real but NON-ACTIVE
					// attempt of THIS review — a late A after retry/rotation.
					// A is archival only: its result may neither conclude NOR
					// FAIL the live successor. Refuse from the closure with NO
					// mutation (commit leaves the state exactly as it was) —
					// B stays in flight, no FAILED routing, no checkpoint.
					return fmt.Errorf("RecordReviewResult: result names attempt %q — a retired (non-active) attempt is archival only and may not conclude or fail the live successor (active attempt %q stays in flight)", res.AttemptID, rv.ActiveAttemptID)
				}
				problem = fmt.Sprintf("result names attempt %q (not an attempt of this review) — only the active attempt %q may conclude the review", res.AttemptID, rv.ActiveAttemptID)
			}
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
			// Stamp the STORED checkpoint (addCheckpoint returns a copy).
			for i := range s.Checkpoints {
				if s.Checkpoints[i].ID == cp.ID {
					s.Checkpoints[i].ReviewID = rv.ReviewID
					s.Checkpoints[i].ReviewDisposition = "FAILED"
					break
				}
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
		if att := rv.ActiveAttempt(rv.ActiveAttemptID); att != nil {
			att.State = "RETIRED"
			att.RetiredAt = now
		}
		note := string(res.RunStatus)
		if res.ReasonCode != "" {
			note += " " + res.ReasonCode
		}
		// M12: the verdict joins the note only for a COMPLETED run — a
		// BLOCKED note must never read like a quality conclusion.
		if res.RunStatus == ReviewRunCompleted && res.Verdict != "" {
			note += " " + res.Verdict
		}
		cp, err := s.addCheckpoint(CheckpointReviewResult, "", note, nil, nil)
		if err != nil {
			return err
		}
		// Stamp the STORED checkpoint (addCheckpoint returns a copy — the
		// recovery-round fix; these fields never persisted before).
		for i := range s.Checkpoints {
			if s.Checkpoints[i].ID == cp.ID {
				s.Checkpoints[i].ReviewID = rv.ReviewID
				s.Checkpoints[i].ReviewDisposition = string(res.RunStatus)
				if res.Verdict != "" {
					s.Checkpoints[i].ReviewDisposition += " " + res.Verdict
				}
				break
			}
		}
		s.Status = StatusWaitUser
		return nil
	})
	if err != nil {
		return err
	}
	return protocolErr
}

// ValidateCriterionVerdicts enforces exact all-active-criterion coverage of a
// result against the MINTED envelope (C1b, spec §3 逐 criterion 结论):
//
//   - with run_status COMPLETED, every ACTIVE criterion (the envelope's exit
//     criteria) has exactly one verdict; duplicates, foreign ids and missing
//     ids are protocol errors. A BLOCKED report may carry partial verdicts
//     (the block reason replaces the quality claim) but every verdict it
//     DOES carry must be well-formed and reference an active criterion;
//   - per-criterion status is pass|fail|blocked (enum);
//   - a pass verdict carries at least one evidence anchor (the observing
//     child tool_use id) — a bare pass is a rubber-stamp;
//   - any active FAIL prevents an aggregate PASS verdict;
//   - a REQUIRED blocked criterion forces run_status BLOCKED (no verdict);
//   - an optional blocked criterion is explicit and never counts as pass.
//
// Returns "" when the coverage is consistent.
func ValidateCriterionVerdicts(env SemanticEnvelope, res ReviewResult) string {
	active := make(map[string]Criterion, len(env.ExitCriteria))
	for _, c := range env.ExitCriteria {
		active[c.ID] = c
	}
	seen := make(map[string]bool, len(res.CriterionVerdicts))
	for _, v := range res.CriterionVerdicts {
		c, known := active[v.CriterionID]
		if !known {
			return fmt.Sprintf("criterion_verdicts names %q which is not an active criterion of this envelope (active: the minted exit criteria)", v.CriterionID)
		}
		if seen[v.CriterionID] {
			return fmt.Sprintf("criterion_verdicts carries a duplicate verdict for %q", v.CriterionID)
		}
		seen[v.CriterionID] = true
		switch v.Status {
		case "pass", "fail", "blocked":
		default:
			return fmt.Sprintf("criterion %q verdict status %q must be pass, fail or blocked", v.CriterionID, v.Status)
		}
		if v.Status == "blocked" && c.Required && res.RunStatus == ReviewRunCompleted {
			return fmt.Sprintf("required criterion %q is BLOCKED — run_status must be BLOCKED with a reason_code, not COMPLETED", v.CriterionID)
		}
		if v.Status == "pass" && len(v.Evidence) == 0 {
			return fmt.Sprintf("criterion %q claims pass with no evidence anchor — a pass needs the observing child tool_use ids", v.CriterionID)
		}
	}
	if res.RunStatus == ReviewRunCompleted {
		var missing []string
		for id := range active {
			if !seen[id] {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return fmt.Sprintf("criterion_verdicts omits active criteria %s — coverage must be exact", strings.Join(missing, ", "))
		}
	}
	if res.RunStatus == ReviewRunCompleted && res.Verdict == VerdictPass {
		for _, v := range res.CriterionVerdicts {
			if v.Status == "fail" {
				return fmt.Sprintf("aggregate PASS contradicts criterion %q FAIL — any active FAIL prevents PASS", v.CriterionID)
			}
			if v.Status == "blocked" {
				return fmt.Sprintf("aggregate PASS counts blocked criterion %q — an optional blocked criterion is explicit and never a pass", v.CriterionID)
			}
		}
	}
	// Criterion mapping: every result-level evidence id must be anchored to a
	// criterion verdict (the anchors ARE the mapping; an id that maps to
	// nothing proves nothing in particular).
	if res.RunStatus == ReviewRunCompleted && len(res.EvidenceToolCallIDs) > 0 {
		anchored := make(map[string]bool)
		for _, v := range res.CriterionVerdicts {
			for _, a := range v.Evidence {
				anchored[a.ToolUseID] = true
			}
		}
		for _, id := range res.EvidenceToolCallIDs {
			if !anchored[id] {
				return fmt.Sprintf("evidence id %q is claimed at the result level but anchored to no criterion — every evidence id needs its criterion mapping", id)
			}
		}
	}
	return ""
}

// RecordReviewProtocolError routes a reviewer protocol error the result
// recorder could not accept — T17's evidence reconciliation failures (a
// claimed evidence id that is not a tool_use record of the reviewer's own
// transcript: forged evidence, spec §3 证据对账). It is the mirror of
// RecordReviewResult's landed problem path: the in-flight review
// (PREPARED/RUNNING) is marked FAILED, the failure routes to a REVIEW_RESULT
// checkpoint so the run stays decidable (WAIT_USER), and the routing IS
// persisted — the returned error reports the problem AFTER the state is
// durable. Returns (routed, err): routed=true means the FAILED routing
// persisted (err then carries the problem for the caller to trace/log);
// routed=false means nothing was routed and err says why (illegal status,
// unknown or already-concluded review, or a failed Save — commit rollback
// leaves memory and disk untouched).
func (r *RunState) RecordReviewProtocolError(reviewID, reason string) (bool, error) {
	if reason == "" {
		return false, errors.New("RecordReviewProtocolError: reason is required")
	}
	var problem error
	err := r.commit(func(s *RunState) error {
		if err := s.requireStatus(StatusReviewPending, "RecordReviewProtocolError"); err != nil {
			return err
		}
		rv := s.findReview(reviewID)
		if rv == nil {
			return fmt.Errorf("RecordReviewProtocolError: unknown review %q", reviewID)
		}
		if rv.Status != ReviewPrepared && rv.Status != ReviewRunning {
			return fmt.Errorf("RecordReviewProtocolError: review %s already concluded (%s) — late protocol errors must not affect a concluded review", reviewID, rv.Status)
		}
		rv.Status = ReviewFailed
		rv.ConcludedAt = nowUTC()
		// Same recoverability contract as RecordReviewResult's problem path:
		// a protocol error must not brick the run — route it to a checkpoint
		// a human can still decide, persist that routing, then report.
		cp, err := s.addCheckpoint(CheckpointReviewResult, "", "FAILED "+reason, nil, nil)
		if err != nil {
			return err
		}
		s.Status = StatusWaitUser
		problem = fmt.Errorf("RecordReviewProtocolError: %s — review %s marked FAILED, routed to checkpoint %s", reason, reviewID, cp.ID)
		return nil
	})
	if err != nil {
		return false, err
	}
	return true, problem
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
	r.MarkOutcomeUnknownWithDecl(toolUseID, nil)
}

// MarkOutcomeUnknownWithDecl is the crash-scan marking entry (recovery round
// I3): it also freezes the intent's pre-effect declaration onto the entry —
// the one-shot probe matcher resolves against exactly what was declared at
// admission, never a later lookup (adjudication §4 line 44).
func (r *RunState) MarkOutcomeUnknownWithDecl(toolUseID string, decl *MutationDecl) {
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
		ToolUseID:    toolUseID,
		PriorStatus:  r.RecoveryPrior,
		Episode:      episode,
		RecordedAt:   nowUTC(),
		MutationDecl: decl,
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
	_, err := r.resolveOutcome(toolUseID, outcome, "")
	return err
}

// ResolveOutcomeProbed is the probe-submit resolution entry (T19): it carries
// the resolving call's OWN probe receipt (the read-class Bash observation the
// gate validated against the trace) and persists it on the entry. Replay
// semantics: an already-resolved entry accepts only the SAME (outcome, probe)
// pair silently — (false, nil), nothing re-recorded; a different probe or
// outcome for a resolved id is an error, and a probe receipt may never be
// re-used against a different unknown (the gate's batch validation rejects
// reused ids before this point; the entry-side check is the belt). Returns
// whether THIS call newly resolved the entry.
func (r *RunState) ResolveOutcomeProbed(toolUseID, outcome, probeToolUseID string) (bool, error) {
	if probeToolUseID == "" {
		return false, errors.New("ResolveOutcomeProbed: probe_tool_use_id is required (every unknown resolves through its own observed probe)")
	}
	return r.resolveOutcome(toolUseID, outcome, probeToolUseID)
}

// ResolveOutcomeMechanical is the recovery-round I3 resolution entry
// (adjudication §4 line 47): the verdict comes from the GATE's observed
// matcher over the frozen declaration — never a model-supplied outcome — and
// the reducer belt coerces to INDETERMINATE whenever the entry cannot support
// the claim (no declaration, unsupported capability, or a declared capability
// with no expected effect): unsupported/uncertain NEVER auto-restores.
func (r *RunState) ResolveOutcomeMechanical(toolUseID, verdict, probeToolUseID string) (bool, error) {
	if probeToolUseID == "" {
		return false, errors.New("ResolveOutcomeMechanical: probe_tool_use_id is required (every unknown resolves through its own observed probe)")
	}
	switch verdict {
	case OutcomeAccepted, OutcomeFailed, OutcomeIndeterminate:
	default:
		return false, fmt.Errorf("ResolveOutcomeMechanical: invalid verdict %q (want accepted|failed|indeterminate — the matcher's observed result)", verdict)
	}
	// The belt runs against the CURRENT entry (a resolved entry replays
	// exactly below; coercion never rewrites history).
	for i := range r.OutcomeUnknowns {
		if r.OutcomeUnknowns[i].ToolUseID != toolUseID || r.OutcomeUnknowns[i].Resolved {
			continue
		}
		d := r.OutcomeUnknowns[i].MutationDecl
		supported := d != nil &&
			(d.Capability == "file.write" || d.Capability == "file.edit") &&
			d.Expect != "" && d.Preimage != ""
		if !supported && verdict != OutcomeIndeterminate {
			verdict = OutcomeIndeterminate
		}
		break
	}
	return r.resolveOutcome(toolUseID, verdict, probeToolUseID)
}

// resolveOutcome is the shared resolution core (ResolveOutcome without a
// receipt, ResolveOutcomeProbed with one).
func (r *RunState) resolveOutcome(toolUseID, outcome, probeToolUseID string) (bool, error) {
	switch outcome {
	case OutcomeAccepted, OutcomeFailed, OutcomeIndeterminate:
	default:
		return false, fmt.Errorf("ResolveOutcome: invalid outcome %q (want accepted|failed|indeterminate)", outcome)
	}
	newly := false
	err := r.commit(func(s *RunState) error {
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
			if entry.Outcome == outcome && (probeToolUseID == "" || entry.ProbeToolUseID == probeToolUseID) {
				return nil // late duplicate callback: no-op
			}
			return fmt.Errorf("ResolveOutcome: %s already resolved as %s via probe %q (conflicting %s/%s)", toolUseID, entry.Outcome, entry.ProbeToolUseID, outcome, probeToolUseID)
		}
		if err := s.requireStatus(StatusOutcomeUnknown, "ResolveOutcome"); err != nil {
			return err
		}
		entry.Resolved = true
		entry.Outcome = outcome
		entry.ResolvedAt = nowUTC()
		entry.ProbeToolUseID = probeToolUseID
		newly = true
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
	if err != nil {
		return false, err
	}
	return newly, nil
}

// --- budget (spec §5 预算与终止) ------------------------------------------------------

// CountToolCall records one admitted tool call against the current visit's
// budget bucket (final-fix wave I1, adjudication §5). The bucket exists
// whenever work is lifecycle-authorized: an OPEN phase, or an authorized
// UPCOMING visit (a continue/skip decision returned the run to ACTIVE and
// the next phase's provider call has not opened the phase yet — pre-provider
// work charges here; OpenPhase adopts the accrual). Outside a visit (no run
// phase and no upcoming bucket — run start, review dispatch from a concluded
// run, post-game) it is a no-op. Admitted tool_use ids are deduped: one call
// charges exactly once, hook replay included. Mutates in memory only (no
// error return) — callers persist via Save.
func (r *RunState) CountToolCall(toolUseID string) {
	if toolUseID != "" && containsString(r.ChargedToolUseIDs, toolUseID) {
		return
	}
	if r.openPhaseState() == nil && !r.UpcomingVisit {
		return
	}
	r.Counters.PhaseToolCalls++
	if toolUseID != "" {
		r.ChargedToolUseIDs = append(r.ChargedToolUseIDs, toolUseID)
	}
}

// CountFailedCheck records one FAILED check result (T19, spec §5 失败检查预
// 算): only a real fail ticks the budget — an honest BLOCKED (sandbox
// unproven, L3 unsupported, unobservable exit) is an inability, never a
// failure. Mutates in memory only — the observing hook persists via Save.
func (r *RunState) CountFailedCheck() {
	r.Counters.FailedChecks++
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

// --- budget settlement (T18: SetBudgetLimits / ExtendBudget) -----------------------

// SetBudgetLimits is INERT since 0.6.1 (T18 rev): budgets settle at StartRun
// (manifest-authored or unlimited). It validates its input (negatives are
// corruption) and then no-ops — Confirm payloads probing the retired
// run-start ask are ignored, never applied. Kept for pre-0.6.1 replay
// tolerance; ExtendBudget is the sole surviving limit raiser.
func (r *RunState) SetBudgetLimits(limits BudgetLimits, toolUseID string) (applied bool, err error) {
	if err := validateBudgetLimits(limits, "SetBudgetLimits"); err != nil {
		return false, err
	}
	err = r.commit(func(s *RunState) error {
		var serr error
		applied, serr = s.applyBudgetSettlement(&BudgetSettlement{ToolUseID: toolUseID, Confirm: &limits})
		return serr
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

// ExtendBudget is the sole LIMIT RAISER (T18): it applies the CURRENT
// checkpoint's persisted extension offer — the delta comes from
// CheckpointState.ExtendAsk, never from the caller (panel OQ3: offered deltas
// are persisted content, applied as persisted). Legal only in WAIT_USER at
// the current DISPLAYED exhaustion checkpoint; per-axis the delta adds to the
// set limit while a zero (unlimited) axis stays unlimited. The receipt is
// append-only per tool_use_id: a re-delivered callback never extends twice,
// while a FRESH human answer may extend again.
func (r *RunState) ExtendBudget(toolUseID string) (extended bool, err error) {
	err = r.commit(func(s *RunState) error {
		var serr error
		extended, serr = s.applyBudgetSettlement(&BudgetSettlement{ToolUseID: toolUseID, Extend: true})
		return serr
	})
	if err != nil {
		return false, err
	}
	return extended, nil
}

// validateBudgetLimits rejects negative counters-of-limit values (zero is the
// meaningful "unlimited", negatives are corruption).
func validateBudgetLimits(l BudgetLimits, op string) error {
	if l.PhaseToolCalls < 0 || l.FailedChecks < 0 || l.RunRework < 0 || l.Delegations < 0 {
		return fmt.Errorf("%s: negative budget values are illegal (%s)", op, budgetLimitsKey(l))
	}
	return nil
}

// applyBudgetSettlement applies one settlement against the commit snapshot.
// Returns whether anything was newly applied (false + nil error = idempotent
// no-op: a replayed callback, or a confirm after the budget already settled).
func (s *RunState) applyBudgetSettlement(settle *BudgetSettlement) (bool, error) {
	if settle == nil || settle.ToolUseID == "" {
		return false, errors.New("budget settlement requires the answering call's tool_use_id")
	}
	applied := false
	// 0.6.1: checkpoint budget settlement is inert — budgets settle at
	// StartRun (manifest-authored or unlimited) and the parser rejects
	// `budget:` ride-alongs outright, so a Confirm payload can only come
	// from a pre-0.6.1 trace replay; nothing to apply.
	if settle.Extend {
		for _, x := range s.BudgetExtensions {
			if x.ToolUseID == settle.ToolUseID {
				return applied, nil // this call's extension is already on record
			}
		}
		if err := s.requireStatus(StatusWaitUser, "ExtendBudget"); err != nil {
			return false, err
		}
		cp := s.CurrentCheckpoint()
		if cp == nil {
			return false, errors.New("ExtendBudget: no open checkpoint")
		}
		if !cp.Displayed {
			return false, fmt.Errorf("ExtendBudget: checkpoint %s is not displayed (the extend question rides a display-verified ask)", cp.ID)
		}
		if cp.ExtendAsk == nil {
			return false, fmt.Errorf("ExtendBudget: checkpoint %s carries no persisted extension offer — extension questions exist only at exhaustion checkpoints", cp.ID)
		}
		if err := validateBudgetLimits(cp.ExtendAsk.Delta, "ExtendBudget"); err != nil {
			return false, err
		}
		if cp.ExtendAsk.Delta == (BudgetLimits{}) {
			return false, errors.New("ExtendBudget: the persisted extension offer is empty (nothing to add)")
		}
		// Per axis: add the delta to a SET limit; a zero (unlimited) axis
		// stays unlimited — an extension never caps an unlimited axis.
		if d := cp.ExtendAsk.Delta.PhaseToolCalls; d > 0 && s.Limits.PhaseToolCalls > 0 {
			s.Limits.PhaseToolCalls += d
		}
		if d := cp.ExtendAsk.Delta.FailedChecks; d > 0 && s.Limits.FailedChecks > 0 {
			s.Limits.FailedChecks += d
		}
		if d := cp.ExtendAsk.Delta.RunRework; d > 0 && s.Limits.RunRework > 0 {
			s.Limits.RunRework += d
		}
		if d := cp.ExtendAsk.Delta.Delegations; d > 0 && s.Limits.Delegations > 0 {
			s.Limits.Delegations += d
		}
		s.BudgetExtensions = append(s.BudgetExtensions, BudgetExtension{
			Delta:     cp.ExtendAsk.Delta,
			ToolUseID: settle.ToolUseID,
			At:        nowUTC(),
		})
		applied = true
	}
	return applied, nil
}

// --- post-game closure (T18: observation ledger + the sole CLOSED writer) -----------

// postGameMarkers are the PINNED questionnaire markers (T18): the required
// question set of every post-game questionnaire — references/
// post-game-review.md's per-type questions 1..4 (the trailing 备注 questions
// are optional by that protocol and pinned no markers of their own). Each
// marker binds through the "[pg:<id>]" question-text token; observations
// require actual non-timeout platform answers.
var postGameMarkers = []string{"q1", "q2", "q3", "q4"}

// PostGameMarkers returns a copy of the pinned questionnaire marker ids.
func PostGameMarkers() []string {
	out := make([]string, len(postGameMarkers))
	copy(out, postGameMarkers)
	return out
}

// PostGameMarkerToken returns the pinned question-text token of one marker
// ("[pg:q1]") — the string a questionnaire question's text must carry for
// its answer to observe that marker.
func PostGameMarkerToken(marker string) string { return "[pg:" + marker + "]" }

// journalRootOverride redirects the fixed journal tree into a test directory
// (hermetic suite, the runStateDirOverride pattern). Empty in production.
var journalRootOverride string

// JournalPath returns the FIXED post-game journal path of a run (T18):
// {project}/.summoner/journal/{run_id}.md — run-id keyed, never date-keyed
// (the controller's binding contract; the old journal/{date}-{workflow} path
// is retired).
func JournalPath(runID string) string {
	root := ProjectDir()
	if journalRootOverride != "" {
		root = journalRootOverride
	}
	return filepath.Join(root, ".summoner", "journal", runID+".md")
}

// IsJournalPath reports whether a Write/Edit file_path targets the run's
// fixed journal path (either the project-relative or absolute spelling).
func IsJournalPath(p, runID string) bool {
	if p == "" || !validID(runID) {
		return false
	}
	c := filepath.Clean(p)
	return c == filepath.Join(".summoner", "journal", runID+".md") || c == JournalPath(runID)
}

// WriteMinimalExitJournal writes the hooks-owned minimal exit journal of an
// emergency STOP (T18, spec §6: 紧急 stop = 最小 journal + 补答 — written
// hook-side IMMEDIATELY, no model turn needed) and returns its content
// digest. The full questionnaire is still owed; the run stays
// POST_GAME_PENDING and completes later in the OWNING session.
func WriteMinimalExitJournal(r *RunState) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nrun_id: %s\nworkflow: %s\nkind: emergency_minimal\nended_at: %s\n---\n\n", r.RunID, r.Workflow, nowUTC())
	fmt.Fprintf(&b, "# Emergency stop — minimal exit journal (run %s)\n\n", r.RunID)
	b.WriteString("Stopped by an explicit STOP decision. The post-game questionnaire is still\n")
	b.WriteString("owed (markers q1..q4) and completes in the run's owning session; CLOSED comes\n")
	b.WriteString("only after the observations persist.\n")
	path := JournalPath(r.RunID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	content := []byte(b.String())
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), nil
}

// StartPostGame records the post-game start of a DONE/STOPped run: kind
// "normal" (DONE) or "emergency_minimal" (STOP + hook-written minimal
// journal). Idempotent — the first kind recorded governs.
func (r *RunState) StartPostGame(kind string) error {
	switch kind {
	case "normal", "emergency_minimal":
	default:
		return fmt.Errorf("StartPostGame: unknown post-game kind %q", kind)
	}
	return r.commit(func(s *RunState) error {
		if err := s.requireStatus(StatusPostGamePending, "StartPostGame"); err != nil {
			return err
		}
		if s.PostGame == nil {
			s.PostGame = &PostGame{Kind: kind, StartedAt: nowUTC()}
		} else if s.PostGame.Kind == "" || s.PostGame.Kind == "unknown" {
			// An "unknown" ledger (F5 process-death fallback) takes the
			// real kind when a later StartPostGame carries one.
			s.PostGame.Kind = kind
		}
		return nil
	})
}

// RecordPostGameQuestionnaire commits the already shape-validated FOUR answers
// from ONE platform call atomically. No partial questionnaire receives credit.
func (r *RunState) RecordPostGameQuestionnaire(toolUseID string) (bool, error) {
	if toolUseID == "" {
		return false, errors.New("questionnaire requires tool_use_id")
	}
	if r.PostGame != nil && len(r.PostGame.Answers) == 4 {
		for _, id := range postGameMarkers {
			if r.PostGame.Answers[id] != toolUseID {
				return false, errors.New("questionnaire already answered by a different call")
			}
		}
		return false, nil
	}
	err := r.commit(func(s *RunState) error {
		if err := s.requireStatus(StatusPostGamePending, "RecordPostGameQuestionnaire"); err != nil {
			return err
		}
		if s.PostGame == nil {
			s.PostGame = &PostGame{Kind: "unknown", StartedAt: nowUTC()}
		}
		s.PostGame.Answers = map[string]string{}
		for _, id := range postGameMarkers {
			s.PostGame.Answers[id] = toolUseID
		}
		return nil
	})
	return err == nil, err
}

// RecordPostGameAnswer records ONE observed questionnaire marker answer
// (actual platform answer of a question whose text carried the marker token).
// Idempotent per marker — the FIRST observed answer governs, and the pinned
// questionnaire is deliberately FOUR questions in ONE call (the platform caps
// 4 questions/call; exactly the four markers), so one tool_use_id legitimately
// settles several markers: there is deliberately no one-marker-per-call
// receipt (unlike drops, the answers never clear, so per-marker idempotency
// is the whole replay story — a re-delivered callback re-observes the same
// markers and records nothing).
func (r *RunState) RecordPostGameAnswer(marker, toolUseID string) (recorded bool, err error) {
	if !containsID(postGameMarkers, marker) {
		return false, fmt.Errorf("RecordPostGameAnswer: %q is not a pinned questionnaire marker (%v)", marker, postGameMarkers)
	}
	if toolUseID == "" {
		return false, errors.New("RecordPostGameAnswer: tool_use_id is required (the answer binds to the answering AskUserQuestion call)")
	}
	recorded = false
	err = r.commit(func(s *RunState) error {
		if err := s.requireStatus(StatusPostGamePending, "RecordPostGameAnswer"); err != nil {
			return err
		}
		if s.PostGame == nil {
			// F5 (fix round 1): a nil ledger here means the process died
			// between the terminal decision and its conclusion — the true
			// kind (normal vs emergency_minimal) is unknowable. Nothing
			// switches on Kind (status render + trace payloads only), so an
			// explicit "unknown" is honest where "normal" asserted a
			// normalcy nobody observed.
			s.PostGame = &PostGame{Kind: "unknown", StartedAt: nowUTC()}
		}
		if s.PostGame.Answers == nil {
			s.PostGame.Answers = map[string]string{}
		}
		if _, seen := s.PostGame.Answers[marker]; seen {
			return nil // first observed answer governs
		}
		s.PostGame.Answers[marker] = toolUseID
		recorded = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return recorded, nil
}

// RecordPostGameJournal records the observed journal Write/Edit of a
// POST_GAME_PENDING run: the content digest captured AT OBSERVATION time. A
// LATER observed write overwrites the record — the latest observed version
// governs closure (controller clarification 4); once CLOSED the record is
// final and no later edit reopens anything.
func (r *RunState) RecordPostGameJournal(digest, toolUseID string) error {
	if digest == "" {
		return errors.New("RecordPostGameJournal: digest is required (captured at observation time)")
	}
	return r.commit(func(s *RunState) error {
		if err := s.requireStatus(StatusPostGamePending, "RecordPostGameJournal"); err != nil {
			return err
		}
		if s.PostGame == nil {
			// F5: same process-death window as RecordPostGameAnswer — stamp
			// "unknown", never an unobserved "normal".
			s.PostGame = &PostGame{Kind: "unknown", StartedAt: nowUTC()}
		}
		s.PostGame.Journal = &JournalRecord{
			Path:      JournalPath(s.RunID),
			Digest:    digest,
			ToolUseID: toolUseID,
			At:        nowUTC(),
		}
		return nil
	})
}

// PostGameReady reports whether the closure preconditions are all observed:
// every pinned questionnaire marker answered AND a journal write observed.
// (The journal FILE match is validated at completion, against the latest
// observed digest — never a detached caller's claim.)
func (r *RunState) PostGameReady() bool {
	if r.PostGame == nil {
		return false
	}
	for _, m := range postGameMarkers {
		if r.PostGame.Answers[m] == "" {
			return false
		}
	}
	return r.PostGame.Journal != nil
}

// CompletePostGame is the SOLE CLOSED writer (T18, spec §6 正常结束须问卷+
// journal 持久化后才 CLOSED; §7 one terminal event pair — no workflow.aborted
// writer). It validates the reducer's OWN records — every pinned marker
// observed, a journal record present — then re-reads the journal file and
// requires it to still match the LATEST observed digest (finalization
// preserves the observed version). CLOSED is terminal: this method refuses
// everything else, and nothing reopens a closed run.
func (r *RunState) CompletePostGame() error {
	return r.commit(func(s *RunState) error {
		if err := s.requireStatus(StatusPostGamePending, "CompletePostGame"); err != nil {
			return err
		}
		var missing []string
		if s.PostGame == nil {
			missing = postGameMarkers
		} else {
			for _, m := range postGameMarkers {
				if s.PostGame.Answers[m] == "" {
					missing = append(missing, m)
				}
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("CompletePostGame: questionnaire markers not yet observed: %v", missing)
		}
		if s.PostGame.Journal == nil {
			return errors.New("CompletePostGame: no journal write observed at the fixed path .summoner/journal/" + s.RunID + ".md")
		}
		b, err := os.ReadFile(s.PostGame.Journal.Path)
		if err != nil {
			return fmt.Errorf("CompletePostGame: journal unreadable at %s: %w", s.PostGame.Journal.Path, err)
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != s.PostGame.Journal.Digest {
			return fmt.Errorf("CompletePostGame: journal %s no longer matches the latest observed version — rewrite it so the observation binds the current content", s.PostGame.Journal.Path)
		}
		s.Status = StatusClosed
		if s.PostGame == nil {
			s.PostGame = &PostGame{}
		}
		s.PostGame.CompletedAt = nowUTC()
		return nil
	})
}

// PendingPostGameRuns scans every session of the current project for
// POST_GAME_PENDING runs — the cross-session, DISPLAY-ONLY surfacing of owed
// post-games (T18: completion happens in the OWNING session; other sessions
// merely point). A missing project state dir simply means none.
func PendingPostGameRuns() ([]*RunState, error) {
	root, err := runStateRoot()
	if err != nil {
		return nil, err
	}
	sessions, err := os.ReadDir(filepath.Join(root, ProjectKey()))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*RunState
	for _, s := range sessions {
		if !s.IsDir() {
			continue
		}
		runs, err := SessionRuns(s.Name())
		if err != nil {
			return nil, err
		}
		for _, r := range runs {
			if r.Status == StatusPostGamePending {
				out = append(out, r)
			}
		}
	}
	return out, nil
}
