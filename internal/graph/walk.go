package graph

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
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
// The RUN_REVIEW directive names the PRODUCER node (env.FromNode) — the node
// whose ④ is under review per trace-protocol §2.2.1 (verdict.node), matching
// the C4 fixture (RUN_REVIEW h-001 scheduled on diagnose, not on fix).
func (w *Walker) RecordHandoff(env HandoffEnvelope) (Directive, error) {
	// input validation: the two fields the runtime gates key on. An empty
	// envelope_id would silently disable both gates (its verdict can never
	// match; the next handoff sails through unreviewed); an empty from_node
	// would skip the producer-match verdict gate entirely.
	if env.EnvelopeID == "" {
		return Directive{}, fmt.Errorf("handoff rejected: empty envelope_id (trace-protocol §2.2)")
	}
	if env.FromNode == "" {
		return Directive{}, fmt.Errorf("handoff rejected: empty from_node — the producer node is required (trace-protocol §2.2)")
	}
	// single-use envelope ids: a consumed id re-handed-off would mint a second
	// handoff+verdict pair for the same envelope_id (and re-submitting h-000
	// would evade the GraphTurns budget).
	if w.state.ConsumedEnvelopes[env.EnvelopeID] {
		return Directive{}, fmt.Errorf("handoff rejected: envelope_id %s already consumed — mint a fresh envelope id", env.EnvelopeID)
	}
	// gate: the previous envelope must have consumed its ⑤ verdict first —
	// a handoff arriving while a review is pending would silently overwrite
	// the pending slot and let an unreviewed ④ slip through (B4).
	if w.state.PendingReview != "" {
		return Directive{}, fmt.Errorf("handoff rejected: envelope %s (node %q) is still awaiting its review_verdict — run ⑤ before the next ④ handoff", w.state.PendingReview, w.state.PendingReviewNode)
	}
	if w.state.ConsumedEnvelopes == nil {
		w.state.ConsumedEnvelopes = map[string]bool{}
	}
	w.state.ConsumedEnvelopes[env.EnvelopeID] = true
	// bootstrap envelope (B5): h-000 legitimately has NO ⑤ (the C4 fixture has
	// no review_verdict for it) — do NOT schedule a review or hold the pending
	// slot (that would deadlock every later handoff). Route to the first node.
	if env.EnvelopeID == "h-000" {
		w.trace.Append(toMap(env))
		w.state.Save()
		first := w.g.firstNode()
		return Directive{Kind: RunNode, Node: first, Label: w.labelOf(first), Attempt: w.state.Attempt}, nil
	}
	// emit handoff event
	w.trace.Append(toMap(env))
	w.trace.Append(map[string]interface{}{
		"type": "node_turn", "node": env.FromNode, "label": env.Label,
		"step":             "review-scheduled",
		"walker_directive": "RUN_REVIEW envelope_id=" + env.EnvelopeID,
	})
	w.state.PendingReview = env.EnvelopeID
	w.state.PendingReviewNode = env.FromNode
	// decrement budget (§2.2: decrements across all nodes; bootstrap h-000 already pre-decremented)
	w.state.GraphTurns++
	w.state.Save()
	return Directive{Kind: RunReview, EnvelopeID: env.EnvelopeID, Node: env.FromNode, Label: env.Label}, nil
}

// AbandonPendingReview drops a pending ⑤ that will never arrive (session
// interrupted mid-review, or the review was abandoned): emits a
// review_abandoned trace event, frees the pending slot, and routes
// CHECKPOINT — the human decides; continuing re-runs the producer node and
// mints a FRESH envelope (the abandoned one stays consumed).
func (w *Walker) AbandonPendingReview(reason string) (Directive, error) {
	if w.state.PendingReview == "" {
		return Directive{}, fmt.Errorf("abandon-review rejected: no pending review")
	}
	node := w.state.PendingReviewNode
	w.trace.Append(map[string]interface{}{
		"type": "review_abandoned", "envelope_id": w.state.PendingReview,
		"node": node, "reason": reason, "executor": "walker",
	})
	w.state.PendingReview = ""
	w.state.PendingReviewNode = ""
	w.state.Save()
	return Directive{Kind: Checkpoint, Node: node, Label: w.labelOf(node)}, nil
}

// RecordReviewVerdict records a ⑤ verdict and routes the next directive.
// Runtime gate (P0): one verdict per pending envelope, verdict ∈ {PASS,
// NEEDS-FIX}, non-empty evidence (invariant #6), and verdict.node == the
// envelope's producer node. Rejected verdicts return an error and mutate
// nothing — no trace pollution, no routing.
func (w *Walker) RecordReviewVerdict(v ReviewVerdict) (Directive, error) {
	if w.state.PendingReview == "" {
		return Directive{}, fmt.Errorf("review_verdict rejected: no pending review envelope (record the ④ handoff first)")
	}
	if v.EnvelopeID != w.state.PendingReview {
		return Directive{}, fmt.Errorf("review_verdict rejected: envelope_id %q does not match pending envelope %q", v.EnvelopeID, w.state.PendingReview)
	}
	if v.Verdict != "PASS" && v.Verdict != "NEEDS-FIX" {
		return Directive{}, fmt.Errorf("review_verdict rejected: verdict %q not in {PASS, NEEDS-FIX}", v.Verdict)
	}
	if len(v.EvidenceToolCalls) == 0 {
		return Directive{}, fmt.Errorf("review_verdict rejected: empty evidence_tool_calls (rubber-stamp, invariant #6)")
	}
	if v.Node != w.state.PendingReviewNode {
		return Directive{}, fmt.Errorf("review_verdict rejected: node %q is not the envelope's producer node %q (trace-protocol §2.2.1; legacy state file? re-run ⑤ with the producer node or abandon-review)", v.Node, w.state.PendingReviewNode)
	}
	// the pending envelope's single verdict is consumed
	w.state.PendingReview = ""
	w.state.PendingReviewNode = ""
	w.trace.Append(toMap(v))
	if v.Verdict == "PASS" {
		// advance to next forward node
		next := w.g.forwardFrom(v.Node)
		w.state.CurrentNode = next
		w.state.Attempt = 1
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
			w.state.Save()
			return Directive{Kind: Checkpoint, Node: v.Node}, nil
		}
		if w.state.Attempt >= node.MaxInnerTurns {
			// exhausted → escalate to checkpoint
			w.state.Save()
			return Directive{Kind: Checkpoint, Node: v.Node}, nil
		}
		// alternating_finding_window (M7): same-node ⑤ keeps raising different
		// findings → rotate escalation. N≤0 disables. Check fires AFTER
		// max_inner_turns (precedence §2.7 #2) and BEFORE node_review_retry
		// (mirrors the 3× rule's early-return-before-append at line ~160).
		// Empty-finding verdicts are degenerate and never feed the window.
		if w.g.Budget.AlternatingFindingWin > 0 && findingText != "" {
			w.state.Windows[v.Node] = append(w.state.Windows[v.Node], findingText)
			win := w.state.Windows[v.Node]
			n := w.g.Budget.AlternatingFindingWin
			if len(win) > n {
				win = win[len(win)-n:]
				w.state.Windows[v.Node] = win
			}
			if alternatingWindowEscalates(win, n) {
				w.state.Save()
				return Directive{Kind: Checkpoint, Node: v.Node}, nil
			}
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
	// 3× same-finding escalation (§2.7 #2) — empty-finding verdicts are
	// degenerate (no locatable defect) and never count toward it.
	if findingText != "" {
		w.state.FindingsSeen[findingText]++
		if w.state.FindingsSeen[findingText] >= 3 {
			w.state.Save()
			return Directive{Kind: Checkpoint, Node: v.Node}, nil
		}
	}
	if w.state.BackEdges >= w.g.Budget.MaxBackEdgesTotal {
		w.state.Save()
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

// findingKey mints the stable dedup key for a verdict's finding SET (3× rule
// + alternating window). It hashes every finding (file:line:issue), sorted, so
// (1) reordering findings cannot dodge the 3× rule, (2) genuinely different
// issues at one file:line do not collide, and (3) subsets/extra findings mint
// different keys. Empty set → "" (callers skip counting entirely).
//
// Accepted trade-offs: the key includes the Issue text, so a reworded finding
// mints a new key — a reviewer drifting its wording evades the 3× rule and
// the window, falling back to the max_inner_turns / MaxBackEdgesTotal
// backstops; that is chosen over the old file:line-only key, which collided
// genuinely different issues at one location. There is no migration for
// state files holding old "file:line" keys: on upgrade the counters reset
// once (stale keys linger harmlessly until the session ends).
func findingKey(fs []Finding) string {
	if len(fs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fs))
	for _, f := range fs {
		parts = append(parts, fmt.Sprintf("%s:%d:%s", f.File, f.Line, f.Issue))
	}
	sort.Strings(parts)
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0x1f})
	}
	return fmt.Sprintf("%016x", h.Sum64())
}
func findingStrings(fs []Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = fmt.Sprintf("%s:%d %s", f.File, f.Line, f.Issue)
	}
	return out
}

// alternatingWindowEscalates reports whether the same-node ⑤ finding window
// shows a "rotate" (M7, §5): the window is full (len==n) AND ≥2 distinct
// finding-texts AND each of those ≥2 findings reappears non-contiguously
// (a finding at indices i<j with a different finding at some k, i<k<j).
// "Each" is the operative word from the spec — merely having ≥1 reappearing
// finding (e.g. A,B,A,C where only A reappears) is NOT a rotation: the
// reviewer is fixating on one finding, not cycling through several, so the
// 3× same-finding rule / max_inner_turns are the right backstop, not this
// window. A single repeating finding (contiguous-equivalent) also does NOT
// escalate here. The ≥2-distinct + each-non-contiguous condition IS the
// cross-finding rotation that this rule (and only this rule) bounds.
func alternatingWindowEscalates(win []string, n int) bool {
	if n <= 0 || len(win) < n {
		return false
	}
	distinct := map[string]bool{}
	nonContiguous := map[string]bool{}
	for i := 0; i < len(win); i++ {
		t := win[i]
		distinct[t] = true
		// find a later occurrence j>i with a different finding between them
		for j := i + 1; j < len(win); j++ {
			if win[j] != t {
				continue
			}
			// j is a repeat of t; is there a different finding in (i, j)?
			for k := i + 1; k < j; k++ {
				if win[k] != t {
					nonContiguous[t] = true
					break
				}
			}
			break
		}
	}
	// Spec (§5/M7): "≥2 distinct findings each reappear" — require ≥2
	// distinct findings that each reappear non-contiguously, not just ≥1.
	// len(nonContiguous)>=2 implies len(distinct)>=2 (nonContiguous ⊆
	// distinct), but both terms are kept for spec-faithful readability.
	return len(distinct) >= 2 && len(nonContiguous) >= 2
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
