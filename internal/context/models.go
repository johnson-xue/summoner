package context

import (
	"time"
)

// Phase represents a workflow phase
type Phase struct {
	ID               int        `db:"id"`
	WorkflowID       string     `db:"workflow_id"`
	PhaseName        string     `db:"phase_name"`
	SkillName        string     `db:"skill_name"`
	Sequence         int        `db:"sequence"`
	Status           string     `db:"status"`
	Summary          string     `db:"summary"`
	SummaryScore     int        `db:"summary_score"`
	SummaryEdited    bool       `db:"summary_edited"`
	FullOutputSize   int        `db:"full_output_size"`
	FullOutputChunks int        `db:"full_output_chunks"`
	TokenCost        int        `db:"token_cost"`
	StartedAt        time.Time  `db:"started_at"`
	CompletedAt      *time.Time `db:"completed_at"`
}

// Workflow represents a workflow
type Workflow struct {
	ID        string    `db:"id"`
	Command   string    `db:"command"`
	UserInput string    `db:"user_input"`
	Status    string    `db:"status"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// Intervention represents a user intervention
type Intervention struct {
	ID               int       `db:"id"`
	PhaseID          int       `db:"phase_id"`
	InterventionType string    `db:"intervention_type"`
	FieldName        string    `db:"field_name"`
	BeforeValue      string    `db:"before_value"`
	AfterValue       string    `db:"after_value"`
	Reason           string    `db:"reason"`
	CreatedAt        time.Time `db:"created_at"`
}

// SavePhaseRequest represents a request to save a phase
type SavePhaseRequest struct {
	WorkflowID   string
	PhaseName    string
	SkillName    string
	FullOutput   string
	ProjectGuide string // Optional: project-specific extraction guide
}

// ContextBundle represents formatted context for next phase
type ContextBundle struct {
	WorkflowID string
	Phases     []Phase
	Format     string // "text" or "json"
}
