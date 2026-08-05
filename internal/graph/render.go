package graph

import (
	"fmt"
	"strings"
)

// Explain renders the human-facing checkpoint view (M9): node label (not id),
// hidden ①②③④⑤, "you are here" route map, budget, precomputed default recall.
func (w *Walker) Explain() string {
	node, _ := w.g.NodeByID(w.state.CurrentNode)
	label := w.state.CurrentNode
	if node != nil {
		label = node.Label
	}
	var route []string
	for _, re := range w.state.RouteMap {
		mark := "?"
		switch re.Status {
		case "pass":
			mark = "✓"
		case "needs_fix":
			mark = "✗"
		case "current":
			mark = "▶"
		case "skipped":
			mark = "⊘"
		}
		route = append(route, mark+re.Label)
	}
	if len(route) == 0 {
		route = []string{"▶" + label}
	}
	turnsLeft := w.g.Budget.MaxGraphTurns - w.state.GraphTurns
	tokensLeft := w.g.Budget.TotalTokenBudget - w.state.TokensUsed
	return fmt.Sprintf("📍 %s (第 %d 次尝试) · 路线 %s · 预算 %d/%d · %dk/%dk · %d/%d\n默认: [recall to %s]",
		label, w.state.Attempt, strings.Join(route, "→"),
		turnsLeft, w.g.Budget.MaxGraphTurns, tokensLeft/1000, w.g.Budget.TotalTokenBudget/1000,
		w.state.BackEdges, w.g.Budget.MaxBackEdgesTotal, label)
}

// Status renders raw machine state for debugging + scorers (§2.9).
func (w *Walker) Status() string {
	return fmt.Sprintf("node=%s attempt=%d/%d graph_turns=%d/%d budget=%d/%d back_edges=%d/%d",
		w.state.CurrentNode, w.state.Attempt, maxInner(w.g, w.state.CurrentNode),
		w.state.GraphTurns, w.g.Budget.MaxGraphTurns,
		w.g.Budget.TotalTokenBudget-w.state.TokensUsed, w.g.Budget.TotalTokenBudget,
		w.state.BackEdges, w.g.Budget.MaxBackEdgesTotal)
}

func maxInner(g *Graph, id string) int {
	if n, ok := g.NodeByID(id); ok {
		return n.MaxInnerTurns
	}
	return 0
}
