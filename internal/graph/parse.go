package graph

import (
	"fmt"
	"io/ioutil"
	"strings"

	"gopkg.in/yaml.v3"
)

// VerdictType is DECIDABLE or SOFT (declared at plan time, §2.5).
type VerdictType string

const (
	DECIDABLE VerdictType = "DECIDABLE"
	SOFT      VerdictType = "SOFT"
)

// ExitCriterion is one entry in a node's exit_criteria list (§2.2).
type ExitCriterion struct {
	Name        string      `yaml:"name"`
	VerdictType VerdictType `yaml:"verdict_type"`
	Pin         string      `yaml:"pin,omitempty"`
	GrepPattern string      `yaml:"grep_pattern,omitempty"`
}

// Node is one node in the per-task graph.
type Node struct {
	ID            string          `yaml:"id"`
	Label         string          `yaml:"label"`
	Skill         string          `yaml:"skill"`
	ExitCriteria  []ExitCriterion `yaml:"exit_criteria"`
	MaxInnerTurns int             `yaml:"max_inner_turns"`
	Mutating      bool            `yaml:"mutating"`
	CleanContext  bool            `yaml:"clean_context"`
}

// Edge is a forward edge.
type Edge struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// ConditionalEdge names a routing rule (§2.6.1).
type ConditionalEdge struct {
	From  string   `yaml:"from"`
	Route string   `yaml:"route"`
	To    []string `yaml:"to"`
}

// BackEdge may carry a skip-set (borrow-point ②).
type BackEdge struct {
	From   string   `yaml:"from"`
	To     string   `yaml:"to"`
	Reason string   `yaml:"reason"`
	Skip   []string `yaml:"skip,omitempty"`
}

// Budget is the global bound (§2.5).
type Budget struct {
	MaxGraphTurns          int `yaml:"max_graph_turns"`
	TotalTokenBudget       int `yaml:"total_token_budget"`
	MaxBackEdgesTotal      int `yaml:"max_back_edges_total"`
	AlternatingFindingWin int `yaml:"alternating_finding_window"`
	Phase0CostTurns        int `yaml:"phase0_cost_turns,omitempty"`
	Phase0CostTokens       int `yaml:"phase0_cost_tokens,omitempty"`
}

// Graph is the parsed per-task graph.
type Graph struct {
	Budget           Budget           `yaml:"budget"`
	Nodes            []Node           `yaml:"nodes"`
	Edges            []Edge           `yaml:"edges"`
	ConditionalEdges []ConditionalEdge `yaml:"conditional_edges"`
	BackEdges        []BackEdge       `yaml:"back_edges"`
	Checkpoints      string           `yaml:"checkpoints"`
}

// ParseGraph decodes a summoner-task-graph YAML block and validates it.
func ParseGraph(b []byte) (*Graph, error) {
	var g Graph
	if err := yaml.Unmarshal(b, &g); err != nil {
		return nil, fmt.Errorf("graph yaml: %w", err)
	}
	if err := g.validate(); err != nil {
		return nil, err
	}
	return &g, nil
}

func (g *Graph) validate() error {
	declared := map[string]bool{}
	for _, n := range g.Nodes {
		if n.ID == "" {
			return fmt.Errorf("node missing id")
		}
		if n.Label == "" {
			return fmt.Errorf("node %s missing label (M9)", n.ID)
		}
		declared[n.ID] = true
		for _, c := range n.ExitCriteria {
			if c.VerdictType != DECIDABLE && c.VerdictType != SOFT {
				return fmt.Errorf("node %s criterion %s missing/invalid verdict_type (B3)", n.ID, c.Name)
			}
		}
	}
	for _, e := range g.Edges {
		if !declared[e.From] {
			return fmt.Errorf("edge from undeclared node %q", e.From)
		}
		if !declared[e.To] {
			return fmt.Errorf("edge to undeclared node %q", e.To)
		}
	}
	for _, be := range g.BackEdges {
		if !declared[be.From] {
			return fmt.Errorf("back_edge from undeclared node %q", be.From)
		}
		if !declared[be.To] {
			return fmt.Errorf("back_edge to undeclared node %q (BLOCKER: §2.5 must declare review)", be.To)
		}
	}
	for _, ce := range g.ConditionalEdges {
		if !declared[ce.From] {
			return fmt.Errorf("conditional_edge from undeclared node %q", ce.From)
		}
		// conditional_edges[].to are cross-task routing targets; the chosen one
		// must be declared in the RESOLVED per-task graph (§2.5 note). We do NOT
		// require all `to` entries to be declared here — only `from`.
	}
	return nil
}

// io_ReadAll is a test-helper alias for io/ioutil.ReadAll of a reader.
// (Kept in parse.go so the test file can use it without re-importing.)
func io_ReadAll(r interface{ Read(p []byte) (int, error) }) ([]byte, error) {
	return ioutil.ReadAll(r)
}

// NodeByID looks up a node by id.
func (g *Graph) NodeByID(id string) (*Node, bool) {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i], true
		}
	}
	return nil, false
}

// firstNode returns the entry node (target of no forward edge), or the first
// declared node if the graph is degenerate.
func (g *Graph) firstNode() string {
	targets := map[string]bool{}
	for _, e := range g.Edges {
		targets[e.To] = true
	}
	for _, n := range g.Nodes {
		if !targets[n.ID] {
			return n.ID
		}
	}
	if len(g.Nodes) > 0 {
		return g.Nodes[0].ID
	}
	return ""
}

// suppress unused-import warnings if strings is only used elsewhere later.
var _ = strings.TrimSpace
