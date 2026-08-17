package graph

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
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

// NewWalker loads persisted state for a session and returns a ready Walker.
// B11 (止血): the OLD code did `s, _ := LoadState(sessionID)` and then
// dereferenced s — if the state file existed but was corrupt (partial write
// from a crashed process), LoadState returned (nil, err), and the very next
// line `s.CurrentNode` panicked with nil-deref. A corrupt walk-state file
// thus killed the whole CLI invocation with a panic instead of recovering.
// Now: on ANY LoadState error, fall back to a fresh zero state (same as the
// os.IsNotExist branch inside LoadState) and log the recovery. The walk
// restarts from the first node with a clean slate rather than crashing.
func NewWalker(g *Graph, sessionID string, trace TraceWriter) *Walker {
	s, err := LoadState(sessionID)
	if err != nil || s == nil {
		s = &WalkState{SessionID: sessionID, FindingsSeen: map[string]int{}, Windows: map[string][]string{}}
	}
	w := &Walker{g: g, state: s, trace: trace}
	// A5: heal any crash-induced RouteMap/CurrentNode disagreement and refresh
	// stale labels/prune renamed nodes from a prior process before any Save.
	w.reconcileRoute()
	if s.CurrentNode == "" {
		s.CurrentNode = g.firstNode()
		s.Attempt = 1
		// Bootstrap the first node as "current" (in-memory only; the first
		// transition's saveStateOrLog persists RouteMap). No Save here: some
		// callers (e.g. TestExplain_ShowsLabelNotId) invoke NewWalker without
		// withTempStateDir and a Save would leak a file to the real home dir.
		w.setRoute(s.CurrentNode, "current")
	}
	return w
}

// saveStateOrLog persists walk-state, logging (not dropping silently) on
// failure (B11). Walk-state is best-effort persistence: the routing decision
// is already made by the time Save is called, so a persistence failure must
// not change the returned Directive. But silently dropping the error hid
// "state file not writable" (disk full, permissions) — the walk appeared to
// progress but no checkpoint survived, so the next process restarted from
// scratch. Now it logs; the caller keeps the Directive as decided.
func (w *Walker) saveStateOrLog() {
	if err := w.state.Save(); err != nil {
		log.Printf("walk-state save failed (session=%s): %v — routing continues but checkpoint may not persist", w.state.SessionID, err)
	}
}

// traceOrLog appends a trace event, logging on failure (B10). The trace is
// the audit log; a write failure (broken pipe, disk full) was previously
// dropped at all 5 call sites with no signal. Now it logs. Trace failure
// never changes routing — the walk continues with a degraded audit trail.
func (w *Walker) traceOrLog(event map[string]interface{}) {
	if err := w.trace.Append(event); err != nil {
		log.Printf("trace append failed (session=%s): %v — walk continues with degraded audit trail", w.state.SessionID, err)
	}
}

// setRoute is the SINGLE mutator for w.state.RouteMap (A5). It upserts a
// routeEntry keyed by node ID: if an entry for nodeID exists its Status and
// Label are overwritten in place; otherwise a new entry is appended.
//
// CRITICAL CONTRACT (the bug the adversarial panel caught): setRoute NEVER
// touches any entry other than nodeID. The responsibility for demoting a
// prior "current" belongs to the CALLER, at the transition that KNOWS the
// old node's fate. An earlier draft auto-swept every "current"→"pass" on
// each write; that let the cross-node back-edge path (which sets
// backTarget="current") silently demote the FAILING node (v.Node) to "pass",
// rendering a ✗ as a ✓ — a lie. By not sweeping, setRoute keeps v.Node's
// explicitly-written "needs_fix" intact. RouteMap is dedup-by-Node, so its
// size is bounded by len(g.Nodes) — O(nodes), never O(turns): a 200-turn
// walk with a self-looping node yields a 3-entry route, not 200.
func (w *Walker) setRoute(nodeID, status string) {
	for i := range w.state.RouteMap {
		if w.state.RouteMap[i].Node == nodeID {
			w.state.RouteMap[i].Status = status
			w.state.RouteMap[i].Label = w.labelOf(nodeID) // refresh on overwrite
			return
		}
	}
	w.state.RouteMap = append(w.state.RouteMap, routeEntry{Node: nodeID, Label: w.labelOf(nodeID), Status: status})
}

// reconcileRoute is the load-time self-heal (A5). Called in NewWalker after
// LoadState, before any Save. It repairs three crash/evolution hazards the
// adversarial panel raised:
//  1. stale entries after a node is renamed/removed from the graph YAML —
//     pruned (only entries whose Node still exists in g survive).
//  2. duplicate entries for one node (a buggy write) — dedup, keeping the
//     last occurrence.
//  3. a dangling "current" marker disagreeing with CurrentNode (a process
//     crash between mutating CurrentNode and mutating RouteMap) — the entry
//     for the (now-loaded) CurrentNode is forced "current"; any other
//     "current" is neutralized to "pass".
// All surviving labels are refreshed from the live graph, so a node marked
// "pass" at invocation 1 whose YAML label later changed shows the NEW label
// on the next load without needing a re-touching write. O(nodes) per load.
// This makes the single-"current" invariant a load-time GUARANTEE, not a hope.
func (w *Walker) reconcileRoute() {
	valid := map[string]bool{}
	for _, n := range w.g.Nodes {
		valid[n.ID] = true
	}
	// prune stale + dedup-by-Node keeping LAST occurrence, refreshing labels.
	last := map[string]int{}
	for i, re := range w.state.RouteMap {
		if !valid[re.Node] {
			continue
		}
		last[re.Node] = i
	}
	kept := w.state.RouteMap[:0]
	for i, re := range w.state.RouteMap {
		if valid[re.Node] && last[re.Node] == i {
			re.Label = w.labelOf(re.Node)
			kept = append(kept, re)
		}
	}
	w.state.RouteMap = kept
	// enforce single-"current" invariant vs CurrentNode.
	for i := range w.state.RouteMap {
		if w.state.RouteMap[i].Node == w.state.CurrentNode {
			w.state.RouteMap[i].Status = "current"
		} else if w.state.RouteMap[i].Status == "current" {
			w.state.RouteMap[i].Status = "pass" // dangling current (crash mid-transition) → pass
		}
	}
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
	w.traceOrLog(toMap(env))
	w.traceOrLog(map[string]interface{}{
		"type": "node_turn", "node": env.ToNode, "label": env.Label,
		"step":             "review-scheduled",
		"walker_directive": "RUN_REVIEW envelope_id=" + env.EnvelopeID,
	})
	w.state.PendingReview = env.EnvelopeID
	// decrement budget (§2.2: decrements across all nodes; bootstrap h-000 already pre-decremented)
	if env.EnvelopeID != "h-000" {
		w.state.GraphTurns++
	}
	w.saveStateOrLog()
	return Directive{Kind: RunReview, EnvelopeID: env.EnvelopeID, Node: env.ToNode, Label: env.Label}, nil
}

// RecordReviewVerdict records a ⑤ verdict and routes the next directive.
func (w *Walker) RecordReviewVerdict(v ReviewVerdict) (Directive, error) {
	w.traceOrLog(toMap(v))
	if v.Verdict == "PASS" {
		// A5: v.Node passed → "pass". The caller demotes explicitly (no sweep);
		// setRoute won't touch any other entry, so the prior "current" on v.Node
		// is overwritten to "pass" here, and next (if any) becomes "current".
		w.setRoute(v.Node, "pass")
		// advance to next forward node
		next := w.g.forwardFrom(v.Node)
		w.state.CurrentNode = next
		w.state.Attempt = 1
		w.state.PendingReview = ""
		if next != "" {
			w.setRoute(next, "current") // mark new current; terminal (next=="") leaves NO current — walk done
		}
		w.saveStateOrLog()
		if next == "" {
			return Directive{Kind: Checkpoint, Node: v.Node}, nil // terminal
		}
		return Directive{Kind: Checkpoint, Node: v.Node, Label: w.labelOf(v.Node)}, nil
	}
	// A5: NEEDS-FIX preamble — v.Node failed review → "needs_fix". This runs
	// BEFORE the same/cross split, so ALL escalation early-returns (unknown
	// node, max_inner_turns, alternating window, 3×, MaxBackEdgesTotal) and
	// the cross-node advance persist "needs_fix" on the failing node. Because
	// setRoute does NOT sweep, the legitimate "current" elsewhere is untouched.
	w.setRoute(v.Node, "needs_fix")
	// NEEDS-FIX: same-node (node_review_retry) vs cross-node (handoff_reject)
	findingText := findingKey(v.Findings)
	backTarget := w.g.backEdgeTarget(v.Node)
	if backTarget == v.Node || backTarget == "" {
		// same-node retry (node_review_retry) — bounded by max_inner_turns
		node, ok := w.g.NodeByID(v.Node)
		if !ok {
			// unknown/empty node id — cannot route a same-node retry; escalate to human
			w.saveStateOrLog()
			return Directive{Kind: Checkpoint, Node: v.Node}, nil
		}
		if w.state.Attempt >= node.MaxInnerTurns {
			// exhausted → escalate to checkpoint
			w.saveStateOrLog()
			return Directive{Kind: Checkpoint, Node: v.Node}, nil
		}
		// alternating_finding_window (M7): same-node ⑤ keeps raising different
		// findings → rotate escalation. N≤0 disables. Check fires AFTER
		// max_inner_turns (precedence §2.7 #2) and BEFORE node_review_retry
		// (mirrors the 3× rule's early-return-before-append at line ~160).
		if w.g.Budget.AlternatingFindingWin > 0 {
			w.state.Windows[v.Node] = append(w.state.Windows[v.Node], findingText)
			win := w.state.Windows[v.Node]
			n := w.g.Budget.AlternatingFindingWin
			if len(win) > n {
				win = win[len(win)-n:]
				w.state.Windows[v.Node] = win
			}
			if alternatingWindowEscalates(win, n) {
				w.saveStateOrLog()
				return Directive{Kind: Checkpoint, Node: v.Node}, nil
			}
		}
		w.traceOrLog(map[string]interface{}{
			"type": "node_review_retry", "envelope_id": v.EnvelopeID,
			"from_node": v.Node, "reason": "review_needs_fix",
			"findings": findingStrings(v.Findings), "executor": "walker",
		})
		// A5 (S8 fix): the NEEDS-FIX preamble marked v.Node "needs_fix", but a
		// same-node RETRY means the node is still ACTIVE (being re-run). Re-promote
		// it to "current" so the route renders ▶ (not ✗ with zero current — the
		// render regression the S8 review caught on self-loop graphs). The failure
		// is still conveyed by the "(第 N 次尝试)" suffix in Explain's header; the
		// route's job is to show WHERE the walk is, which is still this node. The
		// escalation early-returns above (unknown-node, max_inner, alternating)
		// do NOT reach here, so they correctly leave v.Node as "needs_fix" (✗,
		// walk stopped). setRoute overwrites in place (no sweep) — safe.
		w.setRoute(v.Node, "current")
		w.state.Attempt++
		w.saveStateOrLog()
		return Directive{Kind: NodeRetry, Node: v.Node, Attempt: w.state.Attempt,
			Restore: node.Mutating, Label: node.Label}, nil
	}
	// cross-node back-edge (handoff_reject) — increments global counter + 3× escalation
	w.state.BackEdges++
	w.state.FindingsSeen[findingText]++
	// 3× same-finding escalation (§2.7 #2)
	if w.state.FindingsSeen[findingText] >= 3 {
		w.saveStateOrLog()
		return Directive{Kind: Checkpoint, Node: v.Node}, nil
	}
	if w.state.BackEdges >= w.g.Budget.MaxBackEdgesTotal {
		w.saveStateOrLog()
		return Directive{Kind: Checkpoint, Node: v.Node}, nil
	}
	skip := w.g.backEdgeSkip(v.Node)
	w.traceOrLog(map[string]interface{}{
		"type": "handoff_reject", "envelope_id": v.EnvelopeID,
		"from_node": v.Node, "reason": "review_needs_fix",
		"findings": findingStrings(v.Findings), "executor": "walker",
	})
	// A5: going BACK to a prior node — mark it "current". It was "pass" (or
	// "needs_fix" from a prior visit); overwriting to "current" signals a
	// revisit so the route shows non-linear progress (▶ on an earlier node
	// after the failed node's ✗). NO sweep → v.Node stays "needs_fix" (the
	// bug-fix for the panel's caught cross-node-demotes-failing-node defect).
	w.setRoute(backTarget, "current")
	w.state.CurrentNode = backTarget
	w.state.Attempt = 1
	w.saveStateOrLog()
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

// findingKey returns a stable, order-insensitive dedup key for a verdict's
// finding set. The 3× same-finding escalation (§2.7 #2) and the cross-node
// FindingsSeen counter key on this: two verdicts are "the same finding" iff
// their finding SETS are equal, not merely iff their FIRST finding matches.
//
// OLD BUG (B12): only fs[0] was used. Two verdicts [{A:1},{B:2}] and
// [{A:1},{C:3}] collapsed to "A:1" and counted as the same finding — so a
// reviewer cycling through different 2nd findings would wrongly hit the 3×
// escalation after the 3rd verdict even though no single finding repeated.
// Also, an empty finding set returned "" — colliding ALL no-finding verdicts
// into one bucket, so any 3 NEEDS-FIX-with-no-findings would escalate even
// though the reviewer reported nothing repeatable.
//
// Fix: sort all file:line entries (order-insensitive), join with "|". For a
// single finding the key is unchanged ("file:line") — so existing single-
// finding tests keep asserting the literal key. Empty set returns a sentinel
// that won't collide with any real file:line key.
func findingKey(fs []Finding) string {
	if len(fs) == 0 {
		return "__no_findings__"
	}
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = f.File + ":" + strconv.Itoa(f.Line)
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
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
