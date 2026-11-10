package shared

// types.go — the v2 run-state schema: enums, the review wire contract (spec
// §3) and the persisted state document (spec §4/§5). Split from reducer.go so
// the state machine and the schema it mutates stay readable apart. v2 is
// unreleased: schema additions carry no migration burden.

// RunStateSchemaVersion is the schema tag of every v2 run-state file; load
// paths validate against it (task-2.5 M8).
const RunStateSchemaVersion = "2"

// --- enums ----------------------------------------------------------------------

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
	// CheckpointRecovery is the spec §5 recovery checkpoint: an
	// OUTCOME_UNKNOWN episode that resolved failed/indeterminate must not
	// auto-restore — the user decides retry/rollback/accept at this
	// checkpoint (task-2.5 B3).
	CheckpointRecovery CheckpointKind = "RECOVERY"
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

// --- review contract types (spec §3) ---------------------------------------------
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

// --- state types -----------------------------------------------------------------

// DevPosition is where implementation stood when a requirement change opened
// (current task, test progress, pending verifications — spec §1 变更闭环).
type DevPosition struct {
	CurrentTask          string   `json:"current_task,omitempty"`
	TestProgress         string   `json:"test_progress,omitempty"`
	PendingVerifications []string `json:"pending_verifications,omitempty"`
}

// ChangeContext is the CHANGE_OPEN sub-state: why the change opened, the
// status that preceded it, and the frozen development position to restore.
// Applied records whether a delta has been applied — the change may only
// close through its single-point confirm checkpoint (task-2.5 M2), never by
// a bare resume.
type ChangeContext struct {
	Note          string      `json:"note,omitempty"`
	OpenedAt      string      `json:"opened_at,omitempty"`
	PriorStatus   RunStatus   `json:"prior_status,omitempty"`
	Applied       bool        `json:"applied,omitempty"`
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

// PhaseState tracks one phase visit inside the run. A recall (M11) appends a
// FRESH visit of the same id instead of reopening in place — the old visit
// keeps its history (Outcome "recalled"), and "the phase's" state is the
// LAST visit with that id.
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
// Superseded marks the checkpoint a WAIT_USER-entered requirement change
// replaced: it is no longer current or decidable — its re-rendered successor
// is (task-2.5 M2).
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
	Superseded       bool              `json:"superseded,omitempty"`
	Decision         Decision          `json:"decision,omitempty"`
	DecidedAt        string            `json:"decided_at,omitempty"`
}

// ReviewObligation is the persisted [v] (REVIEW_ON_DEMAND) obligation
// (task-2.5 B2): the on-demand review the user asked for and owes before any
// work may (re)start. RequestReview consumes it into the review record.
type ReviewObligation struct {
	SourceCheckpointID string `json:"source_checkpoint_id"`
	PhaseID            string `json:"phase_id,omitempty"`
	At                 string `json:"at,omitempty"`
}

// ReviewRecord is one ⑤ review's lifecycle record.
type ReviewRecord struct {
	ReviewID           string      `json:"review_id"`
	Status             ReviewState `json:"status"`
	SourceCheckpointID string      `json:"source_checkpoint_id,omitempty"` // set when [v]-originated (B2)
	EnvelopeDigest     string      `json:"envelope_digest,omitempty"`
	ArtifactDigest     string      `json:"artifact_digest,omitempty"`
	Verdict            string      `json:"verdict,omitempty"`
	ReasonCode         string      `json:"reason_code,omitempty"`
	RequestedAt        string      `json:"requested_at,omitempty"`
	StartedAt          string      `json:"started_at,omitempty"`
	ConcludedAt        string      `json:"concluded_at,omitempty"`
}

// OutcomeUnknown is one possibly-executed-but-unpersisted tool side effect
// (spec §5). Mutating actions are never auto-replayed; the capability's
// one-shot read-only probe resolves each entry. Episode scopes the entry to
// its OUTCOME_UNKNOWN batch so a later episode never inherits an earlier
// one's failed/indeterminate verdicts (task-2.5 B3/B4).
type OutcomeUnknown struct {
	ToolUseID   string    `json:"tool_use_id"`
	PriorStatus RunStatus `json:"prior_status,omitempty"`
	Episode     int       `json:"episode,omitempty"`
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

// RecallRecord is one applied recall (spec §4: 保存决定+原因+新 visit). Reason
// is wired from AskUserQuestion annotations in Task 4; the field exists now
// so the schema is complete (task-2.5 M11).
type RecallRecord struct {
	FromPhaseID   string `json:"from_phase_id,omitempty"`
	TargetPhaseID string `json:"target_phase_id,omitempty"`
	Reason        string `json:"reason,omitempty"`
	At            string `json:"at,omitempty"`
}

// RunState is the persisted run document. State root:
// $SUMMONER_STATE_DIR (or ~/.summoner/state-v2) / {project_key} / {session_id}
// / {run_id}.json — version-independent so plugin-cache wipes never orphan it.
// RecoveryPrior is the status frozen when the CURRENT OUTCOME_UNKNOWN episode
// started (empty = no active episode); restore targets it, never an earlier
// episode's prior (task-2.5 B4).
type RunState struct {
	SchemaVersion       string            `json:"schema_version"`
	RunID               string            `json:"run_id"`
	SessionID           string            `json:"session_id"`
	Workflow            string            `json:"workflow,omitempty"`
	Status              RunStatus         `json:"status"`
	RequirementsVersion int               `json:"requirements_version"`
	DevPosition         DevPosition       `json:"dev_position"`
	Change              *ChangeContext    `json:"change,omitempty"` // non-nil = CHANGE_OPEN
	ReviewObligation    *ReviewObligation `json:"review_obligation,omitempty"`
	RecoveryPrior       RunStatus         `json:"recovery_prior,omitempty"`
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

// PhaseSubmission is the summoner:phase-submit payload: the phase's result —
// artifacts, proposed criteria ids, and references to registered-check results.
type PhaseSubmission struct {
	PhaseID     string        `json:"phase_id"`
	Summary     string        `json:"summary,omitempty"`
	Artifacts   []ArtifactRef `json:"artifacts,omitempty"`
	CriteriaIDs []string      `json:"criteria_ids,omitempty"`
	CheckRefs   []string      `json:"check_refs,omitempty"`
}
