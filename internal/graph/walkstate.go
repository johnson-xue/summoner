package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// DirectiveKind enumerates what the walker tells SKILL.md to do next.
type DirectiveKind string

const (
	RunNode      DirectiveKind = "RUN_NODE"   // run ①②③④ of a node
	RunReview    DirectiveKind = "RUN_REVIEW" // spawn review-agent for an envelope
	BackEdgeKind DirectiveKind = "BACK_EDGE"  // cross-node back-edge (handoff_reject); renamed from BackEdge to avoid collision with the BackEdge struct type
	NodeRetry    DirectiveKind = "NODE_RETRY" // same-node ⑤ retry (node_review_retry)
	Checkpoint   DirectiveKind = "CHECKPOINT" // surface to human
	Halt         DirectiveKind = "HALT"       // budget exhausted
)

// Directive is what the walker prints for SKILL.md each turn.
type Directive struct {
	Kind       DirectiveKind `json:"kind"`
	Node       string        `json:"node,omitempty"`
	Label      string        `json:"label,omitempty"`
	Attempt    int           `json:"attempt,omitempty"`
	EnvelopeID string        `json:"envelope_id,omitempty"`
	Snapshot   bool          `json:"snapshot,omitempty"` // SKILL.md: node-snapshot.sh save before ②
	Restore    bool          `json:"restore,omitempty"`  // SKILL.md: node-snapshot.sh restore before retry
	CleanCtx   bool          `json:"clean_context,omitempty"`
	Skip       []string      `json:"skip,omitempty"` // nodes to skip on a cross-node back-edge
}

// WalkState is the mutable machine state (§10.2). Lives in a file, not the LLM head.
type WalkState struct {
	SessionID     string              `json:"session_id"`
	CurrentNode   string              `json:"current_node"`
	Attempt       int                 `json:"attempt"`
	GraphTurns    int                 `json:"graph_turns"`
	TokensUsed    int                 `json:"tokens_used"`
	BackEdges     int                 `json:"back_edges"`               // cross-node only
	FindingsSeen  map[string]int      `json:"findings_seen"`            // finding-text → count (3× escalation)
	Windows       map[string][]string `json:"windows"`                  // per-node last N finding texts (alternating)
	PendingReview string              `json:"pending_review,omitempty"` // envelope_id awaiting review_verdict
	RouteMap      []routeEntry        `json:"route_map"`                // for explain render
}

type routeEntry struct {
	Node   string `json:"node"`
	Label  string `json:"label"`
	Status string `json:"status"` // "pass" | "needs_fix" | "current" | "skipped"
}

// stateDirOverride lets tests redirect walk-state into a temp directory so the
// suite stays hermetic (Save/LoadState otherwise touch the real home dir, which
// leaks state across runs and breaks test isolation). It is empty in production.
var stateDirOverride string

// statePath returns the walk-state file location (§10.2).
func statePath(sessionID string) (string, error) {
	dir := stateDirOverride
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".claude", "plugins", "summoner", "walk-state")
	}
	return filepath.Join(dir, sessionID+".json"), nil
}

// LoadState reads walk-state for a session, or returns a zero state.
func LoadState(sessionID string) (*WalkState, error) {
	p, err := statePath(sessionID)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &WalkState{SessionID: sessionID, FindingsSeen: map[string]int{}, Windows: map[string][]string{}}, nil
		}
		return nil, err
	}
	var s WalkState
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.FindingsSeen == nil {
		s.FindingsSeen = map[string]int{}
	}
	if s.Windows == nil {
		s.Windows = map[string][]string{}
	}
	return &s, nil
}

// Save writes walk-state.
func (s *WalkState) Save() error {
	p, err := statePath(s.SessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}
