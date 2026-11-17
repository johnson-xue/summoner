package shared

// workflow.go — the fixed framework verb sequences (spec §1: 固定命令定义阶段
// 顺序；项目清单只替换能力映射，不编写执行拓扑). Phase opening in the
// PreToolUse gate is sequence-driven: the gate cannot know project skill names
// until manifest v2, so the framework owns the order and the gate derives the
// next expected phase from the run's phase history plus this table.
//
// T16 completes the seven-verb surface: every slash command the run starter
// recognizes (fix|new|debug|review|ship|ops|release) has a framework route,
// controller-pinned against the command docs and the release skill:
//   - review  {context, review}           — commands/review.md
//   - ship    {prepare, review, merge, decide} — ship.md steps 1-4 verbatim
//   - ops     {context, operate}          — commands/ops.md (single acceptance
//     gate per operate phase; the ops skill's internal staging is its own)
//   - release {planning, changelog, publish} — 三 Phase 版本规划→changelog→
//     发布执行 (skills/summoner-release/SKILL.md)

import "fmt"

// workflowPhases is the single source of truth for workflow phase order.
// Adding or reordering entries is a protocol change — workflow_test.go pins it.
var workflowPhases = map[string][]string{
	"fix":     {"context", "diagnosis", "reproduce", "fix", "regression", "review"},
	"new":     {"context", "requirements", "plan", "implement", "verify", "review"},
	"debug":   {"context", "diagnosis"},
	"review":  {"context", "review"},
	"ship":    {"prepare", "review", "merge", "decide"},
	"ops":     {"context", "operate"},
	"release": {"planning", "changelog", "publish"},
}

// WorkflowPhases returns the ordered phase ids of a workflow. The returned
// slice is a copy. Unknown workflows are an error — a run whose workflow has
// no sequence cannot drive sequence-based phase opening.
func WorkflowPhases(workflow string) ([]string, error) {
	seq, ok := workflowPhases[workflow]
	if !ok {
		return nil, fmt.Errorf("unknown workflow %q (known: fix, new, debug, review, ship, ops, release)", workflow)
	}
	return append([]string(nil), seq...), nil
}
