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

	// DecisionAutoPass is NOT a recordable decision: no platform answer ever
	// produces it (RecordDecision refuses it — it is outside validDecision).
	// It marks the checkpoint the framework accepted itself through
	// AutoPassCheckpoint (0.7.0 D3), so the history can tell an auto-pass
	// from a human acceptance — never the two confused.
	DecisionAutoPass Decision = "AUTO_PASS"
)

// ReviewState is the ⑤ review lifecycle (spec §3 崩溃恢复).
type ReviewState string

const (
	ReviewPrepared ReviewState = "PREPARED"
	ReviewRunning  ReviewState = "RUNNING"
	ReviewReviewed ReviewState = "REVIEWED"
	ReviewFailed   ReviewState = "FAILED"
	ReviewBlocked  ReviewState = "BLOCKED"
	// ReviewSkipped is the visible UNREVIEWED conclusion of a review the user
	// explicitly declined (recovery round C3: skipping a pending retry ends
	// the review with NO verdict — never a fabricated PASS, and never
	// discharging the mandatory terminal ⑤ debt).
	ReviewSkipped ReviewState = "SKIPPED"
)

// CheckpointKind distinguishes what a checkpoint asks the user to confirm.
type CheckpointKind string

const (
	CheckpointPhase         CheckpointKind = "PHASE"
	CheckpointChangeConfirm CheckpointKind = "CHANGE_CONFIRM"
	CheckpointReviewResult  CheckpointKind = "REVIEW_RESULT"
	// CheckpointReviewDebt is the mandatory-terminal-⑤ debt checkpoint (spec
	// §0 item 2, ruling 72 / controller QA-2): on a workflow WITH a fixed
	// review phase, a DONE whose terminal review is unsettled (no concluded
	// PASS over the CURRENT acceptance view, no explicit skip) parks here in
	// WAIT_USER — review ([v]), skip (finish UNREVIEWED), continue working or
	// stop. It deliberately offers NO done: the debt is the only thing DONE
	// could not discharge, so re-asking it cannot wedge. Workflows without a
	// review phase (ops/release) never mint this debt.
	CheckpointReviewDebt CheckpointKind = "REVIEW_DEBT"
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
	ID                 string        `json:"id"`
	Content            string        `json:"content,omitempty"`
	Version            int           `json:"version,omitempty"`
	CriteriaIDs        []string      `json:"criteria_ids,omitempty"`
	Artifacts          []string      `json:"artifacts,omitempty"`
	ArtifactRefs       []ArtifactRef `json:"artifact_refs,omitempty"`
	PlannedScope       []string      `json:"planned_scope,omitempty"`
	SourceVisitID      string        `json:"source_visit_id,omitempty"`
	SourceCheckpointID string        `json:"source_checkpoint_id,omitempty"`
	AcceptedBy         string        `json:"accepted_by,omitempty"`
}

// FrozenView is the acceptance view frozen at user acceptance; producers
// cannot trim it.
type FrozenView struct {
	Version      int                 `json:"version,omitempty"`
	Requirements []FrozenRequirement `json:"requirements,omitempty"`
	Criteria     []Criterion         `json:"criteria,omitempty"`
	ArtifactSet  []ArtifactRef       `json:"artifact_set,omitempty"`
	CheckDefs    []CheckDef          `json:"check_defs,omitempty"`
	// VerificationScope is the frozen scope authority of the view; envelope
	// validation compares against it, and the wire form never re-carries it.
	VerificationScope  VerificationScope       `json:"-"`
	CheckScope         VerificationScope       `json:"check_scope,omitempty"`
	Environment        VerificationEnvironment `json:"environment"`
	SourceVisitID      string                  `json:"source_visit_id,omitempty"`
	SourceCheckpointID string                  `json:"source_checkpoint_id,omitempty"`
	AcceptedBy         string                  `json:"accepted_by,omitempty"`
}

// Only observed environment properties are frozen. Unknown properties disable
// cross-review reuse; a declaration never certifies native isolation.
type VerificationEnvironment struct {
	OS             string   `json:"os"`
	Arch           string   `json:"arch"`
	Runtime        string   `json:"runtime"`
	Tier           string   `json:"tier"`
	DefaultsDigest string   `json:"defaults_digest"`
	ManifestDigest string   `json:"manifest_digest,omitempty"`
	Unknown        []string `json:"unknown,omitempty"`
}

// VerificationScope bounds what a reviewer may read and run.
type VerificationScope struct {
	Readable     []string `json:"readable,omitempty"`
	CheckIDs     []string `json:"check_ids,omitempty"`
	OutputSchema string   `json:"output_schema,omitempty"`
}

// Envelope is the sole review input contract (spec §3). The producer's
// registration args are identity-only ({schema_version, run_id, review_id});
// the reducer mints the complete envelope from the pinned acceptance snapshot,
// so the wire form carries no semantic transport at all (final-fix wave).
type Envelope struct {
	SchemaVersion string `json:"schema_version"`
	RunID         string `json:"run_id"`
	ReviewID      string `json:"review_id"`
	// ExpectedAttemptID is the recovery form's strict metadata (recovery
	// round C3, adjudication §3 line 37): non-empty switches the
	// identity-only review-submit transport from registration to the
	// wedged-attempt recovery operation. It never reaches a minted envelope
	// (MintEnvelope builds from the identity triple only) and never joins a
	// content digest.
	ExpectedAttemptID string `json:"expected_attempt_id,omitempty"`
}

// SemanticEnvelope is the COMPLETE minted review input: the reducer builds it
// from the pinned acceptance snapshot (minus recorded user drops) — never from
// producer-supplied semantics. Stored on ReviewRecord; one canonical JSON form
// feeds the reviewer capsule and the digests.
type SemanticEnvelope struct {
	SchemaVersion     string                  `json:"schema_version"`
	RunID             string                  `json:"run_id"`
	ReviewID          string                  `json:"review_id"`
	ArtifactSet       []ArtifactRef           `json:"artifact_set"`
	Requirements      FrozenView              `json:"requirements"`
	ExitCriteria      []Criterion             `json:"exit_criteria"`
	VerificationScope VerificationScope       `json:"verification_scope"`
	CheckDefs         []CheckDef              `json:"check_defs,omitempty"`
	Environment       VerificationEnvironment `json:"environment"`
}

// CriterionVerdict is the per-criterion conclusion of a review.
type CriterionVerdict struct {
	CriterionID string           `json:"criterion_id"`
	Status      string           `json:"status"`
	Note        string           `json:"note,omitempty"`
	Evidence    []EvidenceAnchor `json:"evidence,omitempty"`
}

type EvidenceAnchor struct {
	ToolUseID string `json:"tool_use_id"`
	Path      string `json:"path"`
	Line      int    `json:"line"`
	CheckID   string `json:"check_id,omitempty"`
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
	AttemptID           string             `json:"attempt_id,omitempty"`
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
	VisitID          string   `json:"visit_id,omitempty"`
	ID               string   `json:"id"`
	Open             bool     `json:"open"`
	OpenedAt         string   `json:"opened_at,omitempty"`
	ClosedAt         string   `json:"closed_at,omitempty"`
	Outcome          string   `json:"outcome,omitempty"` // accepted|skipped|recalled
	CriteriaIDs      []string `json:"criteria_ids,omitempty"`
	AcceptedCriteria []string `json:"accepted_criteria,omitempty"`
	CheckpointID     string   `json:"checkpoint_id,omitempty"`
}

// CriteriaDrop is one recorded user-explicit criterion drop (spec §3 冻结验收
// 视图: 信封 criteria 集 = 冻结集 − 有记录的用户显式 drop——逐 id、逐次决定).
// The runtime recording path arrives with T10; T7 adds the field and the
// envelope validation that consumes it.
type CriteriaDrop struct {
	ID           string `json:"id"`
	At           string `json:"at,omitempty"`
	CheckpointID string `json:"checkpoint_id,omitempty"`
	// ToolUseID binds the drop to the platform AskUserQuestion call whose
	// drop: answer produced it (T17 ride-along channel) — the same binding
	// DecisionToolUseID gives decisions.
	ToolUseID string `json:"tool_use_id,omitempty"`
}

// CriteriaDropReceipt is the DURABLE per-(criterion id, tool_use_id) receipt
// of one recorded drop observation (fix-round I1). CriteriaDrops is live
// ledger state — an accepting decision CLEARS its ids — so the replay guard
// cannot live there: a re-delivered callback must stay a no-op even after
// re-acceptance emptied the ledger. Receipts are append-only and never
// cleared; they consume no authority (they only prove a call already
// answered for one id).
type CriteriaDropReceipt struct {
	ID        string `json:"id"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	At        string `json:"at,omitempty"`
}

// CheckpointState is one human checkpoint (spec §4). A decision is accepted
// only after the checkpoint was displayed with a matching content digest.
// Superseded marks the checkpoint a WAIT_USER-entered requirement change
// replaced: it is no longer current or decidable — its re-rendered successor
// is (task-2.5 M2).
type CheckpointState struct {
	RunID             string         `json:"run_id,omitempty"`
	Snapshot          FrozenView     `json:"snapshot"`
	Limits            BudgetLimits   `json:"limits"`
	Counters          BudgetCounters `json:"counters"`
	RecallTargets     []RecallTarget `json:"recall_targets,omitempty"`
	ReviewRetry       *ReviewRetry   `json:"review_retry,omitempty"`
	ReviewID          string         `json:"review_id,omitempty"`
	ReviewDisposition string         `json:"review_disposition,omitempty"`
	ID                string         `json:"id"`
	Kind              CheckpointKind `json:"kind"`
	PhaseID           string         `json:"phase_id,omitempty"`
	Note              string         `json:"note,omitempty"`
	// Outcome / Conclusion (0.7.0 D1) are the phase's structured conclusion —
	// model-authored CONTENT the hooks validate, store and bind into the
	// display digest. Empty on every checkpoint written before 0.7.0 and on
	// the non-submission kinds (change-confirm, recovery, review-result,
	// review-debt carry their meaning in Note).
	Outcome          string            `json:"outcome,omitempty"`
	Conclusion       *Conclusion       `json:"conclusion,omitempty"`
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
	// DecisionToolUseID binds the decision to the platform AskUserQuestion
	// call whose injected tool_response carried it (spec §7 平台关联键): the
	// replay-idempotency key of RecordDecision — a replayed callback with the
	// same id is a no-op, a different id never overwrites (task 4).
	DecisionToolUseID string `json:"decision_tool_use_id,omitempty"`
	// MenuPages is the persisted paged-menu projection (T16): pages of ≤4
	// option labels a decision question may offer at once (the platform caps
	// 4 options/question), set at checkpoint open and never rewritten. The
	// checkpoint message shows page 1; the status detail view shows every
	// page; the gate pins any nonce-carrying question's offered labels to
	// one page exactly.
	MenuPages []MenuPage `json:"menu_pages,omitempty"`
	// BudgetAsk (T18, retired 0.6.1) is inert legacy state: the run-start
	// budget question no longer exists (budgets settle at StartRun), no new
	// ask is ever persisted, and nothing renders or settles one. The struct
	// survives only to parse pre-0.6.1 run state.
	BudgetAsk *BudgetAsk `json:"budget_ask,omitempty"`
	// ExtendAsk (T18) persists the extension question of a checkpoint opened
	// while the budget is exhausted: the offered delta (digest-bound content)
	// and its pointer labels.
	ExtendAsk *ExtendAsk `json:"extend_ask,omitempty"`
}

// ReviewObligation is the persisted [v] (REVIEW_ON_DEMAND) obligation
// (task-2.5 B2): the on-demand review the user asked for and owes before any
// work may (re)start. RequestReview consumes it into the review record.
type ReviewObligation struct {
	Snapshot           FrozenView `json:"snapshot"`
	SourceCheckpointID string     `json:"source_checkpoint_id"`
	PhaseID            string     `json:"phase_id,omitempty"`
	At                 string     `json:"at,omitempty"`
}

// ReviewRecord is one ⑤ review's lifecycle record.
type ReviewRecord struct {
	Envelope           SemanticEnvelope `json:"envelope"`
	ActiveAttemptID    string           `json:"active_attempt_id"`
	Attempts           []ReviewAttempt  `json:"attempts"`
	Result             *ReviewResult    `json:"result,omitempty"`
	DefaultDebt        bool             `json:"default_debt,omitempty"`
	ReviewID           string           `json:"review_id"`
	Status             ReviewState      `json:"status"`
	SourceCheckpointID string           `json:"source_checkpoint_id,omitempty"` // set when [v]-originated (B2)
	EnvelopeDigest     string           `json:"envelope_digest,omitempty"`
	ArtifactDigest     string           `json:"artifact_digest,omitempty"`
	Verdict            string           `json:"verdict,omitempty"`
	ReasonCode         string           `json:"reason_code,omitempty"`
	RequestedAt        string           `json:"requested_at,omitempty"`
	StartedAt          string           `json:"started_at,omitempty"`
	ConcludedAt        string           `json:"concluded_at,omitempty"`
}

// ReviewAttempt is one append-only dispatch receipt. Ownership binds the
// platform-origin INITIAL child task record's exact capsule to exactly one
// admitted parent Agent tool_use_id; absent/ambiguous correlation is
// quarantine/BLOCKED, never guessed (controller QA-1).
type ReviewAttempt struct {
	AttemptID           string `json:"attempt_id"`
	Token               string `json:"token,omitempty"`
	ParentToolUseID     string `json:"parent_tool_use_id"`
	DispatchedAt        string `json:"dispatched_at,omitempty"`
	RetiredAt           string `json:"retired_at,omitempty"`
	State               string `json:"state"` // PREPARED|RUNNING|RETIRED
	ChildAgentID        string `json:"child_agent_id,omitempty"`
	ChildTranscriptPath string `json:"child_transcript_path,omitempty"`
	ResultReceiptDigest string `json:"result_receipt_digest,omitempty"`
}

// ReviewRetry pins the funded same-envelope retry obligation opened when a
// wedged RUNNING attempt recovers with no usable result and no budget to
// redispatch. It rides the existing REVIEW_RESULT checkpoint.
type ReviewRetry struct {
	ReviewID       string `json:"review_id"`
	AttemptID      string `json:"attempt_id"`
	EnvelopeDigest string `json:"envelope_digest"`
	ResumeTarget   string `json:"resume_target"`
	SkipLabel      string `json:"skip_label"`
	RetryLabel     string `json:"retry_label"`
}

// MutationDecl is the pre-effect capability declaration frozen at the
// admission of a MUTATING intent (recovery round I3, adjudication §4 line
// 44): capability, normalized target, the original tool input digest, the
// preimage (sha256 of the target's bytes BEFORE the mutation, or "absent"),
// and the expected effect (sha256 of the exact resulting bytes). It rides
// the intent's tool.started payload and lands on the OUTCOME_UNKNOWN entry
// the crash scan mints from it — the one-shot probe matcher resolves against
// exactly this frozen declaration, never a later lookup.
type MutationDecl struct {
	Capability  string `json:"capability"`
	Target      string `json:"target,omitempty"`
	InputDigest string `json:"input_digest,omitempty"`
	Preimage    string `json:"preimage,omitempty"`
	Expect      string `json:"expect,omitempty"`
}

// CapabilityUnsupported marks an intent with NO shipped declaration
// (undeclared write-class Bash, worker delegations, legacy intents): its
// OUTCOME_UNKNOWN entry resolves INDETERMINATE-ONLY — never auto-restored.
const CapabilityUnsupported = "unsupported"

// CapabilityFileWrite / CapabilityFileEdit are the two declared mutation
// capabilities (recovery round I3): a decl with either, a target, a preimage
// and an expect is SUPPORTED — its exact probe command exists and the
// mechanical matcher may conclude accepted/failed. Everything else is
// unsupported (indeterminate-only).
const (
	CapabilityFileWrite = "file.write"
	CapabilityFileEdit  = "file.edit"
)

// OutcomeUnknown is one possibly-executed-but-unpersisted tool side effect
// (spec §5). Mutating actions are never auto-replayed; the capability's
// one-shot read-only probe resolves each entry. Episode scopes the entry to
// its OUTCOME_UNKNOWN batch so a later episode never inherits an earlier
// one's failed/indeterminate verdicts (task-2.5 B3/B4). ProbeToolUseID (T19)
// is the durable probe receipt: the read-class Bash observation that resolved
// the entry — the replay key of probe-submit (a re-delivered resolution with
// the same pair is silent; a different probe for a resolved id is rejected).
// MutationDecl (recovery round I3) is the frozen pre-effect declaration the
// mechanical matcher resolves against; nil/unsupported = indeterminate-only.
type OutcomeUnknown struct {
	ToolUseID      string        `json:"tool_use_id"`
	PriorStatus    RunStatus     `json:"prior_status,omitempty"`
	Episode        int           `json:"episode,omitempty"`
	RecordedAt     string        `json:"recorded_at,omitempty"`
	Resolved       bool          `json:"resolved"`
	Outcome        string        `json:"outcome,omitempty"` // accepted|failed|indeterminate
	ResolvedAt     string        `json:"resolved_at,omitempty"`
	ProbeToolUseID string        `json:"probe_tool_use_id,omitempty"`
	MutationDecl   *MutationDecl `json:"mutation_decl,omitempty"`
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

// BudgetProposal is the budget offer displayed at the run's FIRST checkpoint
// (T18, spec §5 预算与终止: 默认值由用户在运行起点确认): the manifest's
// suggested_budgets projection (via the runtime manifest loader) or the
// framework default — unlimited, never article-derived numbers. The proposal
// is DIGEST-BOUND checkpoint content: changing the offer after display
// invalidates the binding and demands a re-render (the applied Limits are NOT
// that content — controller clarification 2).
type BudgetProposal struct {
	Limits BudgetLimits `json:"limits"`
	Source string       `json:"source,omitempty"` // manifest | framework
}

// BudgetAsk is the RETIRED (0.6.1) first-checkpoint budget question — kept
// only so pre-0.6.1 run state parses. No writer, no renderer, no settlement
// reads it.
type BudgetAsk struct {
	Proposal       BudgetProposal `json:"proposal"`
	ConfirmLabel   string         `json:"confirm_label"`
	UnlimitedLabel string         `json:"unlimited_label"`
}

// ExtendAsk persists the extension question of a checkpoint opened while the
// budget is EXHAUSTED (T18): the offered delta (per set axis, the current
// limit again) and its two pointer labels. The extend answer rides the same
// callback as the decision; ExtendBudget applies the PERSISTED delta
// atomically with it (panel OQ3 — deltas are never model-authored at ask
// time).
type ExtendAsk struct {
	Delta       BudgetLimits `json:"delta"`
	ExtendLabel string       `json:"extend_label"`
	HoldLabel   string       `json:"hold_label"`
}

// BudgetExtension is the durable receipt of ONE applied budget extension: the
// delta applied and the platform tool_use_id of the answering AskUserQuestion
// call. Append-only (a re-delivered callback never extends twice); extensions
// themselves are repeatable through FRESH human answers.
type BudgetExtension struct {
	Delta     BudgetLimits `json:"delta"`
	ToolUseID string       `json:"tool_use_id,omitempty"`
	At        string       `json:"at,omitempty"`
}

// JournalRecord is the observed journal of one POST_GAME_PENDING run (T18):
// the content digest captured AT OBSERVATION time (a later Write/Edit while
// still POST_GAME_PENDING overwrites the record — the latest observed version
// governs closure; once CLOSED the record is final).
type JournalRecord struct {
	Path      string `json:"path"`
	Digest    string `json:"digest"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	At        string `json:"at,omitempty"`
}

// PostGame is the post-game closure ledger (T18, spec §6: 问卷+journal 持久化
// 后才 CLOSED): the questionnaire marker answers OBSERVED by PostToolUse
// (actual non-timeout platform answers, never model-authored text), the
// observed journal record, and the completion stamp. CompletePostGame
// validates these reducer-owned records — never model claims.
type PostGame struct {
	Kind        string            `json:"kind,omitempty"` // normal | emergency_minimal
	StartedAt   string            `json:"started_at,omitempty"`
	Answers     map[string]string `json:"answers,omitempty"` // marker id → settling tool_use_id
	Journal     *JournalRecord    `json:"journal,omitempty"`
	CompletedAt string            `json:"completed_at,omitempty"`
}

// RecallRecord is one applied recall (spec §4: 保存决定+原因+新 visit). Reason
// is wired from AskUserQuestion annotations in Task 4; the field exists now
// so the schema is complete (task-2.5 M11).
type RecallRecord struct {
	TargetVisitID string `json:"target_visit_id"`
	Provenance    string `json:"provenance"`
	Reason        string `json:"reason"`
	Label         string `json:"label"`
	FromPhaseID   string `json:"from_phase_id,omitempty"`
	TargetPhaseID string `json:"target_phase_id,omitempty"`
	At            string `json:"at,omitempty"`
}

// RecallTarget is one offerable named recall destination rendered at a
// checkpoint and pinned into its decision payload.
type RecallTarget struct {
	VisitID string `json:"visit_id"`
	Label   string `json:"label"`
	PhaseID string `json:"phase_id"`
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
	// Acceptance is the cumulative frozen acceptance view (spec §3): every
	// accepted phase visit frozen at user acceptance — one FrozenRequirement
	// per phase id (v2 maps phase→requirement 1:1), read with
	// last-entry-wins. Producers cannot trim it; envelopes validate against
	// it (ValidateEnvelope). CriteriaDrops are the recorded user-explicit
	// criterion drops the envelope set subtracts (逐 id、逐次决定).
	Acceptance      FrozenView            `json:"acceptance"`
	CriteriaDrops   []CriteriaDrop        `json:"criteria_drops,omitempty"`
	DropReceipts    []CriteriaDropReceipt `json:"drop_receipts,omitempty"`
	Checkpoints     []CheckpointState     `json:"checkpoints,omitempty"`
	Reviews         []ReviewRecord        `json:"reviews,omitempty"`
	Recalls         []RecallRecord        `json:"recalls,omitempty"`
	OutcomeUnknowns []OutcomeUnknown      `json:"outcome_unknowns,omitempty"`
	Limits          BudgetLimits          `json:"budget_limits"`
	Counters        BudgetCounters        `json:"budget_counters"`
	// Visit bucket (final-fix wave I1, adjudication §5): UpcomingVisit marks
	// an authorized UPCOMING visit — a continue/skip decision returned the
	// run to ACTIVE and the next phase is not open yet. Real work in that
	// window charges Counters.PhaseToolCalls, and the next OpenPhase ADOPTS
	// the accrual instead of resetting it; only the lifecycle decision
	// boundary resets the bucket. ChargedToolUseIDs dedups admitted tool
	// call ids (one call charges exactly once, hook replay included).
	UpcomingVisit     bool     `json:"upcoming_visit,omitempty"`
	ChargedToolUseIDs []string `json:"charged_tool_use_ids,omitempty"`
	// ContinueNudgeFor (0.7.0) is the one-shot continuation cue of the
	// decision that last returned the run to ACTIVE — the framework's
	// auto-pass or the user's continue / skip: the checkpoint's id, from the
	// decision until the Stop hook nudges once or the next phase opens.
	// Empty = nothing to nudge.
	ContinueNudgeFor string `json:"continue_nudge_for,omitempty"`
	// BudgetConfirmed (T18) is TRUE from run start since 0.6.1 — the budget
	// settled as configuration (BudgetSettledBy = "run-start"; pre-0.6.1
	// state may carry an answering tool_use_id instead). BudgetExtensions
	// are the append-only receipts of applied extensions (budget.extended).
	BudgetConfirmed  bool              `json:"budget_confirmed,omitempty"`
	BudgetSettledBy  string            `json:"budget_settled_by,omitempty"`
	BudgetExtensions []BudgetExtension `json:"budget_extensions,omitempty"`
	// PostGame (T18) is the closure ledger of a DONE/STOPped run: observed
	// questionnaire markers + journal; CLOSED only through CompletePostGame
	// (the sole CLOSED writer).
	PostGame  *PostGame `json:"post_game,omitempty"`
	StartedAt string    `json:"started_at,omitempty"`
	UpdatedAt string    `json:"updated_at,omitempty"`
}

// PhaseSubmission is the summoner:phase-submit payload: the phase's result —
// artifacts, proposed criteria ids, and references to registered-check results.
type PhaseSubmission struct {
	PhaseID     string        `json:"phase_id"`
	Summary     string        `json:"summary,omitempty"`
	Artifacts   []ArtifactRef `json:"artifacts,omitempty"`
	CriteriaIDs []string      `json:"criteria_ids,omitempty"`
	CheckRefs   []string      `json:"check_refs,omitempty"`
	// Outcome / Conclusion (0.7.0 D1): what the phase established. `summary`
	// stays the short title of the acceptance record; the conclusion is what
	// the checkpoint leads with. Optional everywhere except diagnosis (the
	// root cause and how it was reached are not negotiable).
	Outcome    string      `json:"outcome,omitempty"`
	Conclusion *Conclusion `json:"conclusion,omitempty"`
	// Proposed semantics (final-fix wave): the model PROPOSES meaning; hooks
	// validate/normalize it and hash CURRENT artifact bytes. Digests arriving
	// here are advisory only — the hook recomputes from disk.
	Criteria     []Criterion           `json:"criteria,omitempty"`
	Requirements []ProposedRequirement `json:"requirements,omitempty"`
}

// StagedSnapshot is the GATE-BUILT, hook-verified semantic staging of one
// phase submission (final-fix wave C1): hook-hashed CURRENT artifact digests
// (digests the producer supplied are advisory and replaced), planned
// (not-yet-existing) scope kept separate from frozen artifacts, COMPLETE
// criterion meaning — the model's proposal merged with the shipped default
// criteria and manifest additions on review-phase workflows — the frozen
// check defs with RECOMPUTED digests, and the effective verification scope.
// The reducer stages it on the pending checkpoint; only the verified
// rendered platform acceptance freezes it into the run's cumulative view.
// Nothing here is producer authority: every field is derived or validated by
// the hooks.
type StagedSnapshot struct {
	Criteria     []Criterion           `json:"criteria,omitempty"`
	Requirements []ProposedRequirement `json:"requirements,omitempty"`
	ArtifactSet  []ArtifactRef         `json:"artifact_set,omitempty"`
	PlannedScope []string              `json:"planned_scope,omitempty"`
	CheckDefs    []CheckDef            `json:"check_defs,omitempty"`
	CheckScope   VerificationScope     `json:"check_scope,omitempty"`
}

// ProposedRequirement is the model-authored requirement meaning of one
// submission; acceptance freezes the normalized form.
type ProposedRequirement struct {
	ID          string   `json:"id"`
	Content     string   `json:"content,omitempty"`
	Version     int      `json:"version,omitempty"`
	CriteriaIDs []string `json:"criteria_ids,omitempty"`
	Artifacts   []string `json:"artifacts,omitempty"`
}

// Conclusion is the structured conclusion of one phase submission (0.7.0 D1):
// what was found, on what evidence, how, what remains open and what it
// affects. The checkpoint leads with it — the user reads the conclusion
// first and the mechanism never; every field is free model-authored content
// that the hooks validate for shape, store verbatim and render with caps.
type Conclusion struct {
	Summary       string   `json:"summary"`
	Evidence      []string `json:"evidence,omitempty"`
	Approach      string   `json:"approach,omitempty"`
	OpenQuestions []string `json:"open_questions,omitempty"`
	Impact        string   `json:"impact,omitempty"`
}

// The conclusion outcome (0.7.0 D1): a separate axis from the OUTCOME_UNKNOWN
// probe verdicts above — this one states what the PHASE established.
const (
	ConclusionConfirmed = "confirmed"
	ConclusionFailed    = "failed"
	ConclusionUnclear   = "unclear"
)
