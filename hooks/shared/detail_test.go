package shared

import (
	"strings"
	"testing"
)

// detail_test.go — the checkpoint DETAIL render (0.7.0 D1): the reference
// view carrying everything the canonical checkpoint message leaves out. The
// pair is a contract: what the message drops for the decision surface must
// be here in full, or it is nowhere.

// TestCheckpointDetailCarriesWhatTheMessageLeavesOut: every mechanical field
// the message no longer prints is in the detail, complete and uncapped —
// the conclusion in full, criteria with meaning, artifact digests, the
// requirement snapshot, planned scope, environment, page projection, recall
// targets, and the model's ask instruction.
func TestCheckpointDetailCarriesWhatTheMessageLeavesOut(t *testing.T) {
	run, cp := layoutRun()
	// The message caps these lists; the detail must not.
	for i := 0; i < 5; i++ {
		cp.Conclusion.Evidence = append(cp.Conclusion.Evidence, "extra-evidence-"+string(rune('a'+i)))
	}
	cp.Conclusion.OpenQuestions = append(cp.Conclusion.OpenQuestions, "extra-question")

	detail := CheckpointDetail(run, cp)
	for _, want := range []string{
		"checkpoint detail (cp-9 — nonce 1a2b3c4d — PHASE):",
		"conclusion (unclear 未查清):",
		"summary: 回归未完全通过: 3 个用例在 team 模式下失败",
		"evidence (8):",
		"- task_test.go:88 TestTeamLoad",
		"- task_test.go:120 TestTeamRecall",
		"- task_test.go:141 TestSubTaskDone",
		"- extra-evidence-a",
		"- extra-evidence-e",
		"怎么查的: 跑了 go test ./task/... 并按失败用例回溯调用链",
		"open questions (2):",
		"- team 模式下英雄禁用规则是否应该作用于子任务",
		"- extra-question",
		"impact: 只影响 team 模式的任务列表加载",
		"criteria (3):",
		"crit-evid [mechanical · required]",
		"goal: 复现证据链完整",
		"expected: repro.log + 定位文件",
		"scope: logs/",
		"crit-root [judgment · required]",
		"rubric: rubrics/root.md",
		"crit-legacy (no staged meaning; judged at acceptance)",
		"artifacts (frozen versions, 1):",
		"docs/reviews/npe.md @ 5f4a3b2c1d0e",
		"requirements:",
		"fr-1 v2: 登录后任务列表可加载",
		"criteria: crit-evid, crit-root",
		"planned scope (not yet built): internal/api/, player/task/",
		"environment: tier=L2 os=darwin arch=amd64 runtime=go1.16 defaults=unobserved",
		"menu pages (2):",
		"page 1/2: continue: 继续下一阶段",
		"page 2/2: continue: 继续下一阶段",
		MenuNavNext, MenuNavPrev,
		"recall targets:",
		"- latest (phase fix, visit fix@v1)",
		"model: ",
		"nonce 1a2b3c4d",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail lost %q\n--- detail ---\n%s", want, detail)
		}
	}

	// The pair is disjoint where it must be: the message carries none of this.
	message := RenderCheckpointMessage(run, cp)
	for _, mechanical := range []string{"crit-evid [mechanical · required]", "docs/reviews/npe.md @ 5f4a3b2c1d0e", "tier=L2", "page 2/2:"} {
		if strings.Contains(message, mechanical) {
			t.Errorf("field %q is in BOTH renders — it belongs to the detail only:\n%s", mechanical, message)
		}
	}
}

// TestCheckpointDetailRidesAndEmptyParts: a pending review retry and an
// exhaustion offer are spelled out with their full delta; a missing
// conclusion part reads 未提供 rather than vanishing.
func TestCheckpointDetailRidesAndEmptyParts(t *testing.T) {
	run, cp := layoutRun()
	cp.ExtendAsk = &ExtendAsk{Delta: BudgetLimits{RunRework: 2}, ExtendLabel: "extend: 追加已显示额度", HoldLabel: "extend: 不追加"}
	cp.ReviewRetry = &ReviewRetry{ReviewID: "rev-1", AttemptID: "att-1", ResumeTarget: "fix@v1", RetryLabel: "Continue: retry the review", SkipLabel: "Skip: finish UNREVIEWED"}
	detail := CheckpointDetail(run, cp)
	for _, want := range []string{
		"extend ask (budget exhausted): extend: 追加已显示额度 / extend: 不追加",
		"review retry: review rev-1 attempt att-1 — resume fix@v1",
		`retry=Continue: retry the review`,
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail lost %q:\n%s", want, detail)
		}
	}

	legacy := CheckpointState{ID: "cp-2", Kind: CheckpointPhase, PhaseID: "diagnosis", Nonce: "1a2b3c4d", Note: "root cause located"}
	legacyDetail := CheckpointDetail(&RunState{RunID: "run-2", Workflow: "fix"}, legacy)
	for _, want := range []string{
		"summary (phase summary; no structured conclusion was submitted): root cause located",
		"evidence: 未提供", "approach: 未提供", "open questions: 未提供", "impact: 未提供",
		"criteria (0):", "artifacts (frozen versions, 0):",
	} {
		if !strings.Contains(legacyDetail, want) {
			t.Errorf("legacy detail lost %q:\n%s", want, legacyDetail)
		}
	}
}

// TestCheckpointDetailDeterministic: same state, same bytes — the viewer may
// be re-run at will and a diff must mean the state changed.
func TestCheckpointDetailDeterministic(t *testing.T) {
	run, cp := layoutRun()
	if CheckpointDetail(run, cp) != CheckpointDetail(run, cp) {
		t.Fatal("detail must be deterministic: same state, different bytes")
	}
}
