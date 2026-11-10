package shared

// reducer.go — single-loop run-state machine v2 (spec
// docs/superpowers/specs/2026-11-09-single-loop-checkpoint-design.md §4/§5).
//
// The reducer owns run status, phase/checkpoint lifecycle, review lifecycle,
// requirement-change loop, recall history, OUTCOME_UNKNOWN recovery and
// budgets. It validates every transition against the legality matrix before
// mutating; illegal transitions return an error and never persist (the hook
// binary decides the exit-2 wording). Error-returning transitions persist
// atomically (temp file + rename) before returning; the void methods
// (CountToolCall, MarkOutcomeUnknown) mutate in memory only — callers Save.
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
	"time"
)

// ErrRunNotFound is returned by LoadRun when no state file matches the run id.
var ErrRunNotFound = errors.New("run state not found")

// --- enums ------------------------------------------------------------------

// RunStatus is the reducer state machine (spec §4): a phase closing sets
// WAIT_USER; only a recorded user decision (or a review result opening a new
// checkpoint) moves it on. A skill start or reviewer PASS never authorizes.
type RunStatus string

const (
	StatusActive          RunStatus = "ACTIVE"
	StatusWaitUser        RunStatus = "WAIT_USER"
	StatusReviewPending   RunStatus = "REVIEW_PENDING"
	StatusOutcomeUnknown  RunStatus = "OUTCOME_UNKNOWN"
	StatusPostGamePending RunStatus = "POST_GAME_PENDING"
	StatusClosed          RunStatus = "CLOSED"
)

// Decision is the checkpoint decision enum; decisions arrive only through the
// platform-injected AskUserQuestion tool_response (PostToolUse hook).
type Decision string

const (
	DecisionContinue                Decision = "CONTINUE"
	DecisionSkip                    Decision = "SKIP"
	DecisionDone                    Decision = "DONE"
	DecisionRecall                  Decision = "RECALL"
	DecisionStop                    Decision = "STOP"
	DecisionReviewOnDemand          Decision = "REVIEW_ON_DEMAND"
	DecisionAcceptRequirementChange Decision = "ACCEPT_REQUIREMENT_CHANGE"
)

// ReviewState is the ⑤ review lifecycle (spec §3 崩溃恢复).
type ReviewState string

const (
	ReviewPrepared ReviewState = "PREPARED"
	ReviewRunning  ReviewState = "RUNNING"
	ReviewReviewed ReviewState = "REVIEWED"
	ReviewFailed   ReviewState = "FAILED"
	ReviewBlocked  ReviewState = "BLOCKED"
)

// CheckpointKind distinguishes what a checkpoint asks the user to confirm.
type CheckpointKind string

const (
	CheckpointPhase         CheckpointKind = "PHASE"
	CheckpointChangeConfirm CheckpointKind = "CHANGE_CONFIRM"
	CheckpointReviewResult  CheckpointKind = "REVIEW_RESULT"
)

// Review result wire enums (spec §3 证据与 verdict): run_status and the
// quality verdict are separate axes.
const (
	ReviewRunCompleted = "COMPLETED"
	ReviewRunBlocked   = "BLOCKED"
	VerdictPass        = "PASS"
	VerdictNeedsFix    = "NEEDS-FIX"
)

// OUTCOME_UNKNOWN probe results (spec §5: accepted / failed / indeterminate).
const (
	OutcomeAccepted      = "accepted"
	OutcomeFailed        = "failed"
	OutcomeIndeterminate = "indeterminate"
)

// --- review contract types (spec §3) ----------------------------------------
// Envelope/ReviewResult live here so every hook shares one shape; validation
// functions are added in envelope.go (Task 7) around these types.

// CriterionMethod declares how a criterion is verified.
type CriterionMethod string

const (
	MethodMechanical CriterionMethod = "mechanical"
	MethodJudgment   CriterionMethod = "judgment"
)

// ArtifactRef names one immutable artifact version.
type ArtifactRef struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Kind   string `json:"kind,omitempty"`
}

// Criterion is one exit criterion of the frozen acceptance view.
type Criterion struct {
	ID       string          `json:"id"`
	Goal     string          `json:"goal,omitempty"`
	Scope    string          `json:"scope,omitempty"`
	Method   CriterionMethod `json:"method"`
	Expected string          `json:"expected,omitempty"`
	Required bool            `json:"required,omitempty"`
	CheckID  string          `json:"check_id,omitempty"` // mechanical: registered check
	Rubric   string          `json:"rubric,omitempty"`   // judgment: rubric pointer
}

// FrozenRequirement maps one accepted requirement to its criteria and scope.
type FrozenRequirement struct {
	ID          string   `json:"id"`
	CriteriaIDs []string `json:"criteria_ids,omitempty"`
	Artifacts   []string `json:"artifacts,omitempty"`
}

// FrozenView is the acceptance view frozen at user acceptance; producers
// cannot trim it.
type FrozenView struct {
	Version      int                 `json:"version,omitempty"`
	Requirements []FrozenRequirement `json:"requirements,omitempty"`
}

// VerificationScope bounds what a reviewer may read and run.
type VerificationScope struct {
	Readable     []string `json:"readable,omitempty"`
	CheckIDs     []string `json:"check_ids,omitempty"`
	OutputSchema string   `json:"output_schema,omitempty"`
}

// Envelope is the sole review input contract (spec §3).
type Envelope struct {
	SchemaVersion     string            `json:"schema_version"`
	RunID             string            `json:"run_id"`
	ReviewID          string            `json:"review_id"`
	ArtifactSet       []ArtifactRef     `json:"artifact_set"`
	Requirements      FrozenView        `json:"requirements"`
	ExitCriteria      []Criterion       `json:"exit_criteria"`
	VerificationScope VerificationScope `json:"verification_scope"`
}

// CriterionVerdict is the per-criterion conclusion of a review.
type CriterionVerdict struct {
	CriterionID string `json:"criterion_id"`
	Status      string `json:"status"`
	Note        string `json:"note,omitempty"`
}

// Finding is one review finding; it carries no next-phase choice.
type Finding struct {
	CriterionID string `json:"criterion_id"`
	Path        string `json:"path"`
	Line        int    `json:"line,omitempty"`
	Failure     string `json:"failure"`
	Suggestion  string `json:"suggestion,omitempty"`
}

// ReviewResult is the structured reviewer return (spec §3 证据与 verdict).
type ReviewResult struct {
	ReviewID            string             `json:"review_id"`
	ArtifactDigest      string             `json:"artifact_digest"`
	RunStatus           string             `json:"run_status"`            // COMPLETED|BLOCKED
	ReasonCode          string             `json:"reason_code,omitempty"` // BLOCKED only
	Verdict             string             `json:"verdict,omitempty"`     // PASS|NEEDS-FIX, COMPLETED only
	CriterionVerdicts   []CriterionVerdict `json:"criterion_verdicts,omitempty"`
	Findings            []Finding          `json:"findings,omitempty"`
	EvidenceToolCallIDs []string           `json:"evidence_tool_call_ids,omitempty"`
}

// --- state types ------------------------------------------------------------

// DevPosition is where implementation stood when a requirement change opened
// (current task, test progress, pending verifications — spec §1 变更闭环).
type DevPosition struct {
	CurrentTask          string   `json:"current_task,omitempty"`
	TestProgress         string   `json:"test_progress,omitempty"`
	PendingVerifications []string `json:"pending_verifications,omitempty"`
}

// ChangeContext is the CHANGE_OPEN sub-state of ACTIVE: why the change opened
// and the frozen development position restored on resume.
type ChangeContext struct {
	Note          string      `json:"note,omitempty"`
	OpenedAt      string      `json:"opened_at,omitempty"`
	SavedPosition DevPosition `json:"saved_position"`
}

// RequirementDelta is the explicit change statement (what changed) plus the
// impact assessment (which ids are affected). The assessment method belongs
// to the chosen requirement/plan skill, not the framework.
type RequirementDelta struct {
	Note     string   `json:"note,omitempty"`
	Changed  []string `json:"changed,omitempty"`
	Affected []string `json:"affected,omitempty"`
}

// ImpactReport summarizes one applied delta.
type ImpactReport struct {
	RequirementsVersion int      `json:"requirements_version"`
	NewlyStale          []string `json:"newly_stale,omitempty"`
	Preserved           []string `json:"preserved,omitempty"`
}

// PhaseState tracks one phase's lifecycle inside the run.
type PhaseState struct {
	ID               string   `json:"id"`
	Open             bool     `json:"open"`
	OpenedAt         string   `json:"opened_at,omitempty"`
	ClosedAt         string   `json:"closed_at,omitempty"`
	Outcome          string   `json:"outcome,omitempty"` // accepted|skipped|recalled
	CriteriaIDs      []string `json:"criteria_ids,omitempty"`
	AcceptedCriteria []string `json:"accepted_criteria,omitempty"`
	CheckpointID     string   `json:"checkpoint_id,omitempty"`
}

// CheckpointState is one human checkpoint (spec §4). A decision is accepted
// only after the checkpoint was displayed with a matching content digest.
type CheckpointState struct {
	ID               string            `json:"id"`
	Kind             CheckpointKind    `json:"kind"`
	PhaseID          string            `json:"phase_id,omitempty"`
	Note             string            `json:"note,omitempty"`
	Nonce            string            `json:"nonce"`
	ArtifactVersions map[string]string `json:"artifact_versions,omitempty"`
	CriteriaIDs      []string          `json:"criteria_ids,omitempty"`
	OpenedAt         string            `json:"opened_at,omitempty"`
	Displayed        bool              `json:"displayed"`
	DisplayDigest    string            `json:"display_digest,omitempty"`
	DisplayedAt      string            `json:"displayed_at,omitempty"`
	Decided          bool              `json:"decided"`
	Decision         Decision          `json:"decision,omitempty"`
	DecidedAt        string            `json:"decided_at,omitempty"`
}

// ReviewRecord is one ⑤ review's lifecycle record.
type ReviewRecord struct {
	ReviewID       string      `json:"review_id"`
	Status         ReviewState `json:"status"`
	EnvelopeDigest string      `json:"envelope_digest,omitempty"`
	ArtifactDigest string      `json:"artifact_digest,omitempty"`
	Verdict        string      `json:"verdict,omitempty"`
	ReasonCode     string      `json:"reason_code,omitempty"`
	RequestedAt    string      `json:"requested_at,omitempty"`
	StartedAt      string      `json:"started_at,omitempty"`
	ConcludedAt    string      `json:"concluded_at,omitempty"`
}

// OutcomeUnknown is one possibly-executed-but-unpersisted tool side effect
// (spec §5). Mutating actions are never auto-replayed; the capability's
// one-shot read-only probe resolves each entry.
type OutcomeUnknown struct {
	ToolUseID   string    `json:"tool_use_id"`
	PriorStatus RunStatus `json:"prior_status,omitempty"`
	RecordedAt  string    `json:"recorded_at,omitempty"`
	Resolved    bool      `json:"resolved"`
	Outcome     string    `json:"outcome,omitempty"` // accepted|failed|indeterminate
	ResolvedAt  string    `json:"resolved_at,omitempty"`
}

// BudgetLimits are the run-start budget confirmations (spec §5); zero = unlimited.
type BudgetLimits struct {
	PhaseToolCalls int `json:"phase_tool_calls,omitempty"`
	FailedChecks   int `json:"failed_checks,omitempty"`
	RunRework      int `json:"run_rework,omitempty"`
	Delegations    int `json:"delegations,omitempty"`
}

// BudgetCounters are the hook-trusted counters checked against BudgetLimits.
type BudgetCounters struct {
	PhaseToolCalls int `json:"phase_tool_calls"`
	FailedChecks   int `json:"failed_checks"`
	RunRework      int `json:"run_rework"`
	Delegations    int `json:"delegations"`
}

// RecallRecord is one applied recall (spec §4).
type RecallRecord struct {
	FromPhaseID   string `json:"from_phase_id,omitempty"`
	TargetPhaseID string `json:"target_phase_id,omitempty"`
	At            string `json:"at,omitempty"`
}

// RunState is the persisted run document. State root:
// $SUMMONER_STATE_DIR (or ~/.summoner/state-v2) / {project_key} / {session_id}
// / {run_id}.json — version-independent so plugin-cache wipes never orphan it.
type RunState struct {
	SchemaVersion       string            `json:"schema_version"`
	RunID               string            `json:"run_id"`
	SessionID           string            `json:"session_id"`
	Workflow            string            `json:"workflow,omitempty"`
	Status              RunStatus         `json:"status"`
	RequirementsVersion int               `json:"requirements_version"`
	DevPosition         DevPosition       `json:"dev_position"`
	Change              *ChangeContext    `json:"change,omitempty"` // non-nil = CHANGE_OPEN
	Stale               []string          `json:"stale,omitempty"`
	Phases              []PhaseState      `json:"phases,omitempty"`
	Checkpoints         []CheckpointState `json:"checkpoints,omitempty"`
	Reviews             []ReviewRecord    `json:"reviews,omitempty"`
	Recalls             []RecallRecord    `json:"recalls,omitempty"`
	OutcomeUnknowns     []OutcomeUnknown  `json:"outcome_unknowns,omitempty"`
	Limits              BudgetLimits      `json:"budget_limits"`
	Counters            BudgetCounters    `json:"budget_counters"`
	StartedAt           string            `json:"started_at,omitempty"`
	UpdatedAt           string            `json:"updated_at,omitempty"`
}

// --- persistence ------------------------------------------------------------

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
// rename. Callers may invoke it directly; transitions do so themselves.
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
	return &r, nil
}

// --- run lifecycle ----------------------------------------------------------

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
	if _, err := LoadRun(runID); err == nil {
		return nil, fmt.Errorf("start run %s: already exists (resume via LoadRun; refusing to overwrite)", runID)
	} else if !errors.Is(err, ErrRunNotFound) {
		return nil, err
	}
	r := &RunState{
		SchemaVersion:       "2",
		RunID:               runID,
		SessionID:           sessionID,
		Workflow:            workflow,
		Status:              StatusActive,
		RequirementsVersion: 1,
		Limits:              limits,
		StartedAt:           nowUTC(),
	}
	if err := r.Save(); err != nil {
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

// CurrentCheckpoint returns the open (undecided) checkpoint, or nil.
func (r *RunState) CurrentCheckpoint() *CheckpointState {
	if len(r.Checkpoints) == 0 {
		return nil
	}
	last := &r.Checkpoints[len(r.Checkpoints)-1]
	if last.Decided {
		return nil
	}
	return last
}

func (r *RunState) openPhaseState() *PhaseState {
	for i := range r.Phases {
		if r.Phases[i].Open {
			return &r.Phases[i]
		}
	}
	return nil
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

// newNonce returns a short random-looking nonce (crypto/rand, never math/rand)
// that the AskUserQuestion payload must carry to bind a decision to a checkpoint.
func newNonce() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (r *RunState) addCheckpoint(kind CheckpointKind, phaseID, note string, artifacts []ArtifactRef, criteria []string) (CheckpointState, error) {
	nonce, err := newNonce()
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

// CheckpointContentDigest is the canonical digest of a checkpoint's current
// version. MarkDisplayed binds exactly this; a render of a stale or altered
// checkpoint cannot produce it, so "展示版本错配" fails closed.
func CheckpointContentDigest(cp CheckpointState, requirementsVersion int) string {
	h := sha256.New()
	fmt.Fprintf(h, "summoner-checkpoint/v1\nid=%s\nnonce=%s\nkind=%s\nreq=%d\n",
		cp.ID, cp.Nonce, cp.Kind, requirementsVersion)
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

// --- phase & checkpoint transitions -----------------------------------------

// OpenPhase opens a phase for work: legal only in ACTIVE with no phase open
// and no requirement change in flight. Resets the per-phase tool budget.
func (r *RunState) OpenPhase(id string) error {
	if err := r.requireStatus(StatusActive, "OpenPhase"); err != nil {
		return err
	}
	if r.Change != nil {
		return errors.New("OpenPhase: requirement change open (resume before opening a phase)")
	}
	if ph := r.openPhaseState(); ph != nil {
		return fmt.Errorf("OpenPhase(%s): phase %q is already open", id, ph.ID)
	}
	r.Phases = append(r.Phases, PhaseState{ID: id, Open: true, OpenedAt: nowUTC()})
	r.Counters.PhaseToolCalls = 0
	return r.Save()
}

// PhaseSubmission is the summoner:phase-submit payload: the phase's result —
// artifacts, proposed criteria ids, and references to registered-check results.
type PhaseSubmission struct {
	PhaseID     string        `json:"phase_id"`
	Summary     string        `json:"summary,omitempty"`
	Artifacts   []ArtifactRef `json:"artifacts,omitempty"`
	CriteriaIDs []string      `json:"criteria_ids,omitempty"`
	CheckRefs   []string      `json:"check_refs,omitempty"`
}

// SubmitPhase closes the open phase and opens its checkpoint (WAIT_USER).
// Illegal while a checkpoint is pending, no phase is open, the payload names a
// different phase, or a requirement change is open (CHANGE_OPEN).
func (r *RunState) SubmitPhase(sub PhaseSubmission) (CheckpointState, error) {
	if err := r.requireStatus(StatusActive, "SubmitPhase"); err != nil {
		return CheckpointState{}, err
	}
	if r.Change != nil {
		return CheckpointState{}, errors.New("SubmitPhase: rejected — requirement change open (CHANGE_OPEN)")
	}
	ph := r.openPhaseState()
	if ph == nil {
		return CheckpointState{}, errors.New("SubmitPhase: no open phase (OpenPhase first)")
	}
	if sub.PhaseID != ph.ID {
		return CheckpointState{}, fmt.Errorf("SubmitPhase: phase id mismatch (payload %q, open %q)", sub.PhaseID, ph.ID)
	}
	now := nowUTC()
	ph.Open = false
	ph.ClosedAt = now
	ph.CriteriaIDs = append([]string(nil), sub.CriteriaIDs...)
	cp, err := r.addCheckpoint(CheckpointPhase, ph.ID, sub.Summary, sub.Artifacts, sub.CriteriaIDs)
	if err != nil {
		return CheckpointState{}, err
	}
	ph.CheckpointID = cp.ID
	r.Status = StatusWaitUser
	if err := r.Save(); err != nil {
		return CheckpointState{}, err
	}
	return cp, nil
}

// MarkDisplayed binds a checkpoint to a verified render: the digest must
// equal the checkpoint's canonical content digest. Idempotent for the same
// digest; any mismatch fails closed.
func (r *RunState) MarkDisplayed(checkpointID, contentDigest string) error {
	if err := r.requireStatus(StatusWaitUser, "MarkDisplayed"); err != nil {
		return err
	}
	cp := r.CurrentCheckpoint()
	if cp == nil || cp.ID != checkpointID {
		return fmt.Errorf("MarkDisplayed: %q is not the open checkpoint", checkpointID)
	}
	expected := CheckpointContentDigest(*cp, r.RequirementsVersion)
	if contentDigest != expected {
		return fmt.Errorf("MarkDisplayed: display digest mismatch for %s (render does not match the current checkpoint version)", checkpointID)
	}
	cp.Displayed = true
	cp.DisplayDigest = contentDigest
	cp.DisplayedAt = nowUTC()
	return r.Save()
}

// phaseForDecision maps a checkpoint to the phase its CONTINUE/SKIP settles:
// the phase it closed, or — after a review — the latest phase still awaiting
// acceptance.
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

func (r *RunState) decidePhase(cp *CheckpointState, accepted bool) {
	p := r.phaseForDecision(cp)
	if p == nil {
		return
	}
	if accepted {
		p.Outcome = "accepted"
		p.AcceptedCriteria = append([]string(nil), p.CriteriaIDs...)
	} else {
		p.Outcome = "skipped"
	}
}

// planRecall resolves the recall target — the most recent accepted prior
// phase (spec §4 default), or the checkpoint's own phase when none exists.
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

func (r *RunState) applyRecallAt(cp *CheckpointState, targetIdx int) {
	for i := targetIdx + 1; i < len(r.Phases); i++ {
		for _, c := range r.Phases[i].CriteriaIDs {
			r.addStale(c)
		}
		for _, c := range r.Phases[i].AcceptedCriteria {
			r.addStale(c)
		}
	}
	tgt := &r.Phases[targetIdx]
	tgt.Open = true
	tgt.ClosedAt = ""
	tgt.Outcome = ""
	tgt.AcceptedCriteria = nil
	r.Counters.PhaseToolCalls = 0 // a redone phase gets a fresh tool budget
	r.Recalls = append(r.Recalls, RecallRecord{FromPhaseID: cp.PhaseID, TargetPhaseID: tgt.ID, At: nowUTC()})
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

// RecordDecision applies a user decision to the open checkpoint. The
// checkpoint must be displayed with a bound digest; the decision must be
// legal for the checkpoint kind (ACCEPT_REQUIREMENT_CHANGE only at a
// requirement-change confirm checkpoint; only ACCEPT/STOP decide one).
func (r *RunState) RecordDecision(checkpointID string, d Decision) error {
	if err := r.requireStatus(StatusWaitUser, "RecordDecision"); err != nil {
		return err
	}
	cp := r.CurrentCheckpoint()
	if cp == nil || cp.ID != checkpointID {
		return fmt.Errorf("RecordDecision: %q is not the open checkpoint", checkpointID)
	}
	if !cp.Displayed || cp.DisplayDigest == "" {
		return fmt.Errorf("RecordDecision: checkpoint %s was never displayed — decision rejected", checkpointID)
	}
	if err := validDecision(d); err != nil {
		return err
	}
	if cp.Kind == CheckpointChangeConfirm {
		if d != DecisionAcceptRequirementChange && d != DecisionStop {
			return fmt.Errorf("RecordDecision: %s illegal at change-confirm checkpoint %s (want ACCEPT_REQUIREMENT_CHANGE or STOP)", d, checkpointID)
		}
	} else if d == DecisionAcceptRequirementChange {
		return fmt.Errorf("RecordDecision: ACCEPT_REQUIREMENT_CHANGE illegal at %s checkpoint %s (no requirement change open)", cp.Kind, checkpointID)
	}
	// Pre-validate every effect before mutating anything.
	recallIdx := -1
	if d == DecisionRecall {
		var err error
		if recallIdx, err = r.planRecall(cp); err != nil {
			return err
		}
	}
	if d == DecisionAcceptRequirementChange && r.Change == nil {
		return fmt.Errorf("RecordDecision: no requirement change open for checkpoint %s", checkpointID)
	}

	cp.Decided = true
	cp.Decision = d
	cp.DecidedAt = nowUTC()
	switch d {
	case DecisionContinue:
		r.decidePhase(cp, true)
		r.Status = StatusActive
	case DecisionSkip:
		r.decidePhase(cp, false)
		r.Status = StatusActive
	case DecisionDone:
		r.decidePhase(cp, true)
		r.Status = StatusPostGamePending // CLOSED only after post-game persists
	case DecisionRecall:
		if p := r.phaseForDecision(cp); p != nil && p.Outcome == "" {
			p.Outcome = "recalled"
		}
		r.applyRecallAt(cp, recallIdx)
	case DecisionReviewOnDemand:
		r.Status = StatusActive // review request follows via RequestReview
	case DecisionStop:
		r.Status = StatusPostGamePending // minimal exit journal is the caller's duty
	case DecisionAcceptRequirementChange:
		r.resumeAfterChange() // validated above; restores position, closes loop
	}
	return r.Save()
}

// --- requirement-change loop (spec §1 增补) ----------------------------------

func copyDevPosition(p DevPosition) DevPosition {
	q := p
	q.PendingVerifications = append([]string(nil), p.PendingVerifications...)
	return q
}

// OpenRequirementChange freezes the current DevPosition and enters the
// CHANGE_OPEN sub-state of ACTIVE. While open, SubmitPhase is refused (no
// submitting from a half-changed baseline).
func (r *RunState) OpenRequirementChange(note string) error {
	if err := r.requireStatus(StatusActive, "OpenRequirementChange"); err != nil {
		return err
	}
	if r.Change != nil {
		return errors.New("OpenRequirementChange: a change is already open")
	}
	r.Change = &ChangeContext{
		Note:          note,
		OpenedAt:      nowUTC(),
		SavedPosition: copyDevPosition(r.DevPosition),
	}
	return r.Save()
}

// ApplyRequirementDelta applies an explicit delta: RequirementsVersion bumps,
// affected ids go stale, untouched accepted results stay valid. It then opens
// the single-point confirm checkpoint (ACCEPT_REQUIREMENT_CHANGE resumes).
func (r *RunState) ApplyRequirementDelta(delta RequirementDelta) (ImpactReport, error) {
	if err := r.requireStatus(StatusActive, "ApplyRequirementDelta"); err != nil {
		return ImpactReport{}, err
	}
	if r.Change == nil {
		return ImpactReport{}, errors.New("ApplyRequirementDelta: no requirement change open (OpenRequirementChange first)")
	}
	if cp := r.CurrentCheckpoint(); cp != nil {
		return ImpactReport{}, fmt.Errorf("ApplyRequirementDelta: checkpoint %s is pending — a change-confirm checkpoint is already open", cp.ID)
	}
	r.RequirementsVersion++
	var newly []string
	for _, id := range append(append([]string(nil), delta.Changed...), delta.Affected...) {
		if id == "" || containsID(r.Stale, id) {
			continue
		}
		r.addStale(id)
		newly = append(newly, id)
	}
	var preserved []string
	for i := range r.Phases {
		for _, c := range r.Phases[i].AcceptedCriteria {
			if !containsID(r.Stale, c) && !containsID(preserved, c) {
				preserved = append(preserved, c)
			}
		}
	}
	phaseID := ""
	if ph := r.openPhaseState(); ph != nil {
		phaseID = ph.ID
	}
	if _, err := r.addCheckpoint(CheckpointChangeConfirm, phaseID, delta.Note, nil, nil); err != nil {
		return ImpactReport{}, err
	}
	r.Status = StatusWaitUser
	if err := r.Save(); err != nil {
		return ImpactReport{}, err
	}
	return ImpactReport{RequirementsVersion: r.RequirementsVersion, NewlyStale: newly, Preserved: preserved}, nil
}

// resumeAfterChange restores the frozen DevPosition and clears CHANGE_OPEN.
// A still-pending change-confirm checkpoint is closed as accepted. Status
// returns to ACTIVE only when no checkpoint remains owed.
func (r *RunState) resumeAfterChange() error {
	if r.Change == nil {
		return errors.New("no requirement change open")
	}
	if cp := r.CurrentCheckpoint(); cp != nil {
		if cp.Kind != CheckpointChangeConfirm {
			return fmt.Errorf("checkpoint %s (%s) is still owed", cp.ID, cp.Kind)
		}
		cp.Decided = true
		cp.Decision = DecisionAcceptRequirementChange
		cp.DecidedAt = nowUTC()
	}
	r.DevPosition = r.Change.SavedPosition
	r.Change = nil
	if r.CurrentCheckpoint() == nil && r.Status == StatusWaitUser {
		r.Status = StatusActive
	}
	return nil
}

// ResumeAfterChange closes the change loop without a decision checkpoint —
// the framework-level resume (RecordDecision(ACCEPT_REQUIREMENT_CHANGE) is
// the platform-anchored path and performs the same resume).
func (r *RunState) ResumeAfterChange() error {
	if r.Status != StatusActive && r.Status != StatusWaitUser {
		return fmt.Errorf("ResumeAfterChange: illegal status %s", r.Status)
	}
	if err := r.resumeAfterChange(); err != nil {
		return err
	}
	return r.Save()
}

// --- review lifecycle (spec §3) ----------------------------------------------

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
// run to REVIEW_PENDING. Re-requesting the same id with the same envelope is
// idempotent (crash recovery); a different envelope or a concluded review is
// refused, and only one review may be in flight at a time.
func (r *RunState) RequestReview(env Envelope) (string, error) {
	if err := r.requireStatus(StatusActive, "RequestReview"); err != nil {
		return "", err
	}
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
	if rv := r.findReview(env.ReviewID); rv != nil {
		if rv.Status != ReviewPrepared && rv.Status != ReviewRunning {
			return "", fmt.Errorf("RequestReview: review %s already concluded (%s) — use a new review id", env.ReviewID, rv.Status)
		}
		if rv.EnvelopeDigest != envelopeDigest(env) {
			return "", fmt.Errorf("RequestReview: review %s already registered with a different envelope", env.ReviewID)
		}
		return rv.ReviewID, nil // same envelope: idempotent
	}
	for i := range r.Reviews {
		if r.Reviews[i].Status == ReviewPrepared || r.Reviews[i].Status == ReviewRunning {
			return "", fmt.Errorf("RequestReview: review %s still in flight — one review at a time", r.Reviews[i].ReviewID)
		}
	}
	r.Reviews = append(r.Reviews, ReviewRecord{
		ReviewID:       env.ReviewID,
		Status:         ReviewPrepared,
		EnvelopeDigest: envelopeDigest(env),
		RequestedAt:    nowUTC(),
	})
	r.Status = StatusReviewPending
	if err := r.Save(); err != nil {
		return "", err
	}
	return env.ReviewID, nil
}

// MarkReviewRunning moves a PREPARED review to RUNNING (the reviewer agent
// actually started).
func (r *RunState) MarkReviewRunning(reviewID string) error {
	if err := r.requireStatus(StatusReviewPending, "MarkReviewRunning"); err != nil {
		return err
	}
	rv := r.findReview(reviewID)
	if rv == nil {
		return fmt.Errorf("MarkReviewRunning: unknown review %q", reviewID)
	}
	if rv.Status != ReviewPrepared {
		return fmt.Errorf("MarkReviewRunning: review %s is %s, not PREPARED", reviewID, rv.Status)
	}
	rv.Status = ReviewRunning
	rv.StartedAt = nowUTC()
	return r.Save()
}

// RecordReviewResult concludes the in-flight review and opens a review-result
// checkpoint (PASS still leaves the run WAIT_USER — spec §3). Enum violations
// are protocol errors: the review is marked FAILED and an error returned.
// NEEDS-FIX consumes run rework budget; BLOCKED does not.
func (r *RunState) RecordReviewResult(res ReviewResult) error {
	if err := r.requireStatus(StatusReviewPending, "RecordReviewResult"); err != nil {
		return err
	}
	rv := r.findReview(res.ReviewID)
	if rv == nil {
		return fmt.Errorf("RecordReviewResult: unknown review %q", res.ReviewID)
	}
	if rv.Status != ReviewPrepared && rv.Status != ReviewRunning {
		return fmt.Errorf("RecordReviewResult: review %s already concluded (%s) — late results must not affect a new generation", res.ReviewID, rv.Status)
	}
	problem := ""
	switch res.RunStatus {
	case ReviewRunCompleted:
		if res.Verdict != VerdictPass && res.Verdict != VerdictNeedsFix {
			problem = fmt.Sprintf("verdict must be %s or %s when run_status is COMPLETED", VerdictPass, VerdictNeedsFix)
		}
	case ReviewRunBlocked:
		if res.ReasonCode == "" {
			problem = "BLOCKED requires a reason_code"
		}
	default:
		problem = fmt.Sprintf("run_status must be %s or %s", ReviewRunCompleted, ReviewRunBlocked)
	}
	if problem != "" {
		rv.Status = ReviewFailed
		rv.ConcludedAt = nowUTC()
		if err := r.Save(); err != nil {
			return fmt.Errorf("RecordReviewResult: %s (review %s marked FAILED; save failed: %v)", problem, res.ReviewID, err)
		}
		return fmt.Errorf("RecordReviewResult: %s — review %s marked FAILED", problem, res.ReviewID)
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
			r.Counters.RunRework++
		}
	}
	rv.ConcludedAt = now
	note := string(res.RunStatus)
	if res.Verdict != "" {
		note += " " + res.Verdict
	}
	if _, err := r.addCheckpoint(CheckpointReviewResult, "", note, nil, nil); err != nil {
		return err
	}
	r.Status = StatusWaitUser
	return r.Save()
}

// --- OUTCOME_UNKNOWN recovery (spec §5) --------------------------------------

// MarkOutcomeUnknown records a tool call whose side effect may have happened
// but whose result was never persisted (crash window). Idempotent per
// toolUseID. Mutates in memory only — callers persist via Save; recovery
// probes never auto-replay mutating actions.
func (r *RunState) MarkOutcomeUnknown(toolUseID string) {
	if toolUseID == "" || r.Status == StatusClosed || r.Status == StatusPostGamePending {
		return
	}
	for i := range r.OutcomeUnknowns {
		if r.OutcomeUnknowns[i].ToolUseID == toolUseID {
			return
		}
	}
	prior := r.Status
	if prior == StatusOutcomeUnknown && len(r.OutcomeUnknowns) > 0 {
		prior = r.OutcomeUnknowns[0].PriorStatus
	}
	if prior == StatusOutcomeUnknown {
		prior = StatusActive
	}
	r.OutcomeUnknowns = append(r.OutcomeUnknowns, OutcomeUnknown{
		ToolUseID:   toolUseID,
		PriorStatus: prior,
		RecordedAt:  nowUTC(),
	})
	r.Status = StatusOutcomeUnknown
}

// ResolveOutcome records a probe verdict (accepted|failed|indeterminate) for
// an OUTCOME_UNKNOWN entry. The run stays OUTCOME_UNKNOWN until the last
// entry resolves, then returns to its prior status. A late duplicate resolve
// with the same outcome is an idempotent no-op; a conflicting one is an error.
func (r *RunState) ResolveOutcome(toolUseID, outcome string) error {
	switch outcome {
	case OutcomeAccepted, OutcomeFailed, OutcomeIndeterminate:
	default:
		return fmt.Errorf("ResolveOutcome: invalid outcome %q (want accepted|failed|indeterminate)", outcome)
	}
	var entry *OutcomeUnknown
	for i := range r.OutcomeUnknowns {
		if r.OutcomeUnknowns[i].ToolUseID == toolUseID {
			entry = &r.OutcomeUnknowns[i]
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
	if err := r.requireStatus(StatusOutcomeUnknown, "ResolveOutcome"); err != nil {
		return err
	}
	entry.Resolved = true
	entry.Outcome = outcome
	entry.ResolvedAt = nowUTC()
	for i := range r.OutcomeUnknowns {
		if !r.OutcomeUnknowns[i].Resolved {
			return r.Save()
		}
	}
	if entry.PriorStatus != "" && entry.PriorStatus != StatusOutcomeUnknown {
		r.Status = entry.PriorStatus
	} else {
		r.Status = StatusActive
	}
	return r.Save()
}

// --- budget (spec §5 预算与终止) ----------------------------------------------

// CountToolCall records one tool call against the current phase's budget.
// Mutates in memory only (no error return) — callers persist via Save.
func (r *RunState) CountToolCall() { r.Counters.PhaseToolCalls++ }

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
