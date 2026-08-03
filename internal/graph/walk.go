package graph

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// TraceWriter appends a JSONL event. The CLI wires this to the trace file.
type TraceWriter interface {
	Append(event map[string]interface{}) error
}

// HandoffEnvelope is the ④ product (§2.2).
type HandoffEnvelope struct {
	EnvelopeID      string          `json:"envelope_id"`
	FromNode        string          `json:"from_node"`
	ToNode          string          `json:"to_node"`
	Label           string          `json:"label"`
	Artifacts       []string        `json:"artifacts"`
	ExitCriteria    []ExitCriterion `json:"exit_criteria"`
	FactualClaim    string          `json:"factual_claim"`
	AttemptHistory  []AttemptEntry  `json:"attempt_history"`
	BudgetRemaining BudgetRemaining `json:"budget_remaining"`
	Stripped        []string        `json:"stripped"`
}

type AttemptEntry struct {
	Node     string `json:"node"`
	Attempts int    `json:"attempts"`
	Verifier string `json:"verifier"`
}

type BudgetRemaining struct {
	GraphTurnsLeft  int `json:"graph_turns_left"`
	TokenBudgetLeft int `json:"token_budget_left"`
}

// ReviewVerdict is the ⑤ result (§2.2.1).
type ReviewVerdict struct {
	EnvelopeID        string    `json:"envelope_id"`
	Node              string    `json:"node"`
	Reviewer          string    `json:"reviewer"`
	Verdict           string    `json:"verdict"` // PASS | NEEDS-FIX
	Findings          []Finding `json:"findings,omitempty"`
	EvidenceToolCalls []string  `json:"evidence_tool_calls"`
}

type Finding struct {
	File  string `json:"file"`
	Line  int    `json:"line,omitempty"`
	Issue string `json:"issue"`
	Fix   string `json:"fix"`
}

// Walker is the graph router + bookkeeper.
type Walker struct {
	g     *Graph
	state *WalkState
	trace TraceWriter
}

func NewWalker(g *Graph, sessionID string, trace TraceWriter) *Walker {
	s, _ := LoadState(sessionID)
	if s.CurrentNode == "" {
		s.CurrentNode = g.firstNode()
		s.Attempt = 1
	}
	return &Walker{g: g, state: s, trace: trace}
}

// Next returns the directive for the current state (bootstrap → first node).
func (w *Walker) Next() Directive {
	if w.state.GraphTurns >= w.g.Budget.MaxGraphTurns {
		return Directive{Kind: Halt}
	}
	node, ok := w.g.NodeByID(w.state.CurrentNode)
	if !ok {
		return Directive{Kind: Halt}
	}
	// budget check
	turnsLeft := w.g.Budget.MaxGraphTurns - w.state.GraphTurns
	tokensLeft := w.g.Budget.TotalTokenBudget - w.state.TokensUsed
	if turnsLeft <= 0 || tokensLeft <= 0 {
		return Directive{Kind: Halt}
	}
	return Directive{
		Kind:     RunNode,
		Node:     node.ID,
		Label:    node.Label,
		Attempt:  w.state.Attempt,
		Snapshot: node.Mutating,
		CleanCtx: node.CleanContext,
	}
}

// RecordHandoff records a ④ handoff, emits the handoff trace event + a node_turn,
// and returns RUN_REVIEW for that envelope (B4: walker schedules ⑤).
func (w *Walker) RecordHandoff(env HandoffEnvelope) (Directive, error) {
	// emit handoff event
	w.trace.Append(toMap(env))
	w.trace.Append(map[string]interface{}{
		"type": "node_turn", "node": env.ToNode, "label": env.Label,
		"step":             "review-scheduled",
		"walker_directive": "RUN_REVIEW envelope_id=" + env.EnvelopeID,
	})
	w.state.PendingReview = env.EnvelopeID
	// decrement budget (§2.2: decrements across all nodes; bootstrap h-000 already pre-decremented)
	if env.EnvelopeID != "h-000" {
		w.state.GraphTurns++
	}
	w.state.Save()
	return Directive{Kind: RunReview, EnvelopeID: env.EnvelopeID, Node: env.ToNode, Label: env.Label}, nil
}

// RecordReviewVerdict records a ⑤ verdict and routes the next directive.
func (w *Walker) RecordReviewVerdict(v ReviewVerdict) (Directive, error) {
	w.trace.Append(toMap(v))
	if v.Verdict == "PASS" {
		// advance to next forward node
		next := w.g.forwardFrom(v.Node)
		w.state.CurrentNode = next
		w.state.Attempt = 1
		w.state.PendingReview = ""
		w.state.Save()
		if next == "" {
			return Directive{Kind: Checkpoint, Node: v.Node}, nil // terminal
		}
		return Directive{Kind: Checkpoint, Node: v.Node, Label: w.labelOf(v.Node)}, nil
	}
	// NEEDS-FIX: same-node (node_review_retry) vs cross-node (handoff_reject)
	findingText := findingKey(v.Findings)
	backTarget := w.g.backEdgeTarget(v.Node)
	if backTarget == v.Node || backTarget == "" {
		// same-node retry (node_review_retry) — bounded by max_inner_turns
		node, ok := w.g.NodeByID(v.Node)
		if !ok {
			// unknown/empty node id — cannot route a same-node retry; escalate to human
			return Directive{Kind: Checkpoint, Node: v.Node}, nil
		}
		if w.state.Attempt >= node.MaxInnerTurns {
			// exhausted → escalate to checkpoint
			return Directive{Kind: Checkpoint, Node: v.Node}, nil
		}
		w.trace.Append(map[string]interface{}{
			"type": "node_review_retry", "envelope_id": v.EnvelopeID,
			"from_node": v.Node, "reason": "review_needs_fix",
			"findings": findingStrings(v.Findings), "executor": "walker",
		})
		w.state.Attempt++
		w.state.Save()
		return Directive{Kind: NodeRetry, Node: v.Node, Attempt: w.state.Attempt,
			Restore: node.Mutating, Label: node.Label}, nil
	}
	// cross-node back-edge (handoff_reject) — increments global counter + 3× escalation
	w.state.BackEdges++
	w.state.FindingsSeen[findingText]++
	// 3× same-finding escalation (§2.7 #2)
	if w.state.FindingsSeen[findingText] >= 3 {
		return Directive{Kind: Checkpoint, Node: v.Node}, nil
	}
	if w.state.BackEdges >= w.g.Budget.MaxBackEdgesTotal {
		return Directive{Kind: Checkpoint, Node: v.Node}, nil
	}
	skip := w.g.backEdgeSkip(v.Node)
	w.trace.Append(map[string]interface{}{
		"type": "handoff_reject", "envelope_id": v.EnvelopeID,
		"from_node": v.Node, "reason": "review_needs_fix",
		"findings": findingStrings(v.Findings), "executor": "walker",
	})
	w.state.CurrentNode = backTarget
	w.state.Attempt = 1
	w.state.Save()
	return Directive{Kind: BackEdgeKind, Node: backTarget, Skip: skip, Label: w.labelOf(backTarget)}, nil
}

// --- Graph helpers ---

func (g *Graph) forwardFrom(nodeID string) string {
	for _, e := range g.Edges {
		if e.From == nodeID {
			return e.To
		}
	}
	return ""
}
func (g *Graph) backEdgeTarget(nodeID string) string {
	for _, be := range g.BackEdges {
		if be.From == nodeID {
			return be.To
		}
	}
	return ""
}
func (g *Graph) backEdgeSkip(nodeID string) []string {
	for _, be := range g.BackEdges {
		if be.From == nodeID {
			return be.Skip
		}
	}
	return nil
}
func (w *Walker) labelOf(id string) string {
	if n, ok := w.g.NodeByID(id); ok {
		return n.Label
	}
	return id
}

func findingKey(fs []Finding) string {
	if len(fs) == 0 {
		return ""
	}
	return fs[0].File + ":" + strconv.Itoa(fs[0].Line)
}
func findingStrings(fs []Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = fmt.Sprintf("%s:%d %s", f.File, f.Line, f.Issue)
	}
	return out
}

func toMap(v interface{}) map[string]interface{} {
	b, _ := json.Marshal(v)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	m["type"] = typeOf(v)
	return m
}
func typeOf(v interface{}) string {
	switch v.(type) {
	case HandoffEnvelope:
		return "handoff"
	case ReviewVerdict:
		return "review_verdict"
	default:
		return "event"
	}
}
