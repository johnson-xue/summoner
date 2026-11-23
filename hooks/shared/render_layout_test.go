package shared

// render_layout_test.go — the checkpoint render contract (0.7.0 D1). The
// render is the user's DECISION SURFACE: it leads with the conclusion —
// what was found, on what evidence, how, what is still open, what it affects
// — carries the conclusions of the earlier phases (经过), and then the page
// of options. Everything mechanical (full criteria, artifact digests,
// requirement snapshot, environment, budget keys, page projection, recall
// target list) lives in the summoner-status default view, never here.
//
// The incident this replaces (2026-11-23): five checkpoints whose render
// carried sixty lines of mechanism and never said what the bug was.
//
// The rules here are structural, not golden-byte: the rail is LEFT-ONLY (no
// right edge ⇒ no CJK width alignment, deterministic bytes), the header
// signature (id — nonce — kind) is verbatim (the validate-repo nonce lint and
// every contains-based consumer key on it), the reading order is fixed, and
// the largest legal submission stays under the line cap.

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func longText(n int) string { return strings.Repeat("查", n) }

// layoutRun builds an in-memory run+checkpoint carrying the LARGEST legal
// content the render must cope with. History (8 settled visits, oldest
// first): context accepted then RECALLED; diagnosis and reproduce accepted;
// a fix accepted; the regression checkpoint SKIPPED; the context re-run
// AUTO-PASSED by the framework; the diagnosis and fix re-accepted. The
// checkpoint is the second regression visit, with a max-size conclusion,
// staged criteria with meaning, a pending-meaning id, pinned artifacts,
// frozen requirements with planned scope, environment, a paged menu and
// recall targets. (An exhaustion extend offer and a review retry are added
// by the tests that need them.)
func layoutRun() (*RunState, CheckpointState) {
	run := &RunState{
		RunID:               "run-1",
		Workflow:            "fix",
		RequirementsVersion: 2,
		Limits:              BudgetLimits{PhaseToolCalls: 40, FailedChecks: 3, RunRework: 2, Delegations: 5},
		Counters:            BudgetCounters{PhaseToolCalls: 4, Delegations: 1},
		Phases: []PhaseState{
			{ID: "context", VisitID: "context@v1", Outcome: "recalled", CheckpointID: "cp-1"},
			{ID: "diagnosis", VisitID: "diagnosis@v1", Outcome: "accepted", CheckpointID: "cp-2"},
			{ID: "reproduce", VisitID: "reproduce@v1", Outcome: "accepted", CheckpointID: "cp-3"},
			{ID: "fix", VisitID: "fix@v1", Outcome: "accepted", CheckpointID: "cp-4"},
			{ID: "regression", VisitID: "regression@v1", Outcome: "skipped", CheckpointID: "cp-5"},
			{ID: "context", VisitID: "context@v2", Outcome: "accepted", CheckpointID: "cp-6"},
			{ID: "diagnosis", VisitID: "diagnosis@v2", Outcome: "accepted", CheckpointID: "cp-7"},
			{ID: "fix", VisitID: "fix@v2", Outcome: "accepted", CheckpointID: "cp-8"},
			{ID: "regression", VisitID: "regression@v2", Open: true},
		},
		Checkpoints: []CheckpointState{
			{ID: "cp-1", Kind: CheckpointPhase, PhaseID: "context", Nonce: "n0nc0001", Note: "monorepo, Go 1.16, entry cmd/server", Decided: true, Decision: DecisionContinue},
			{ID: "cp-2", Kind: CheckpointPhase, PhaseID: "diagnosis", Nonce: "n0nc0002", Outcome: ConclusionConfirmed, Conclusion: &Conclusion{Summary: "nil deref in the task loader"}, Decided: true, Decision: DecisionContinue},
			{ID: "cp-3", Kind: CheckpointPhase, PhaseID: "reproduce", Nonce: "n0nc0003", Note: "repro script", Decided: true, Decision: DecisionContinue},
			{ID: "cp-4", Kind: CheckpointPhase, PhaseID: "fix", Nonce: "n0nc0004", Note: "fix applied", Decided: true, Decision: DecisionContinue},
			{ID: "cp-5", Kind: CheckpointPhase, PhaseID: "regression", Nonce: "n0nc0005", Note: "diff under 5 lines", Decided: true, Decision: DecisionSkip},
			// cp-6: passed by the framework, no human decision
			{ID: "cp-6", Kind: CheckpointPhase, PhaseID: "context", Nonce: "n0nc0006", Outcome: ConclusionConfirmed, Conclusion: &Conclusion{Summary: "same repo, new branch"}, Decided: true, Decision: DecisionAutoPass},
			{ID: "cp-7", Kind: CheckpointPhase, PhaseID: "diagnosis", Nonce: "n0nc0007", Note: "second root cause", Decided: true, Decision: DecisionContinue},
			{ID: "cp-8", Kind: CheckpointPhase, PhaseID: "fix", Nonce: "n0nc0008", Note: "fix re-applied", Decided: true, Decision: DecisionContinue},
		},
	}

	cp := CheckpointState{
		ID:          "cp-9",
		Kind:        CheckpointPhase,
		PhaseID:     "regression",
		Nonce:       "1a2b3c4d",
		CriteriaIDs: []string{"crit-evid", "crit-root", "crit-legacy"},
		Outcome:     ConclusionUnclear,
		Conclusion: &Conclusion{
			Summary:       "回归未完全通过: 3 个用例在 team 模式下失败",
			Evidence:      []string{"task_test.go:88 TestTeamLoad", "task_test.go:120 TestTeamRecall", "task_test.go:141 TestSubTaskDone"},
			Approach:      "跑了 go test ./task/... 并按失败用例回溯调用链",
			OpenQuestions: []string{"team 模式下英雄禁用规则是否应该作用于子任务"},
			Impact:        "只影响 team 模式的任务列表加载",
		},
		Snapshot: FrozenView{
			Criteria: []Criterion{
				{ID: "crit-evid", Method: MethodMechanical, Required: true, Goal: "复现证据链完整", Expected: "repro.log + 定位文件", Scope: "logs/"},
				{ID: "crit-root", Method: MethodJudgment, Required: true, Goal: "根因唯一且已证实", Rubric: "rubrics/root.md"},
			},
			Requirements: []FrozenRequirement{
				{ID: "fr-1", Version: 2, Content: "登录后任务列表可加载", CriteriaIDs: []string{"crit-evid", "crit-root"}, PlannedScope: []string{"player/task/", "internal/api/"}},
			},
			Environment: VerificationEnvironment{Tier: "L2", OS: "darwin", Arch: "amd64", Runtime: "go1.16"},
		},
		ArtifactVersions: map[string]string{"docs/reviews/npe.md": "5f4a3b2c1d0e"},
		MenuPages: []MenuPage{
			{Labels: []string{"continue: 继续下一阶段", "skip: 跳过该阶段（标 UNVERIFIED / UNREVIEWED，不能伪造 PASS）", "done: 完成——进入收尾（post-game）", "stop: 停止——保留状态退出"}, NavNext: MenuNavNext},
			{Labels: []string{"continue: 继续下一阶段", "recall: 回城——回到指定历史阶段重做", "review: [v] 对本阶段产物发起独立审查", "stop: 停止——保留状态退出"}, NavPrev: MenuNavPrev},
		},
		RecallTargets: []RecallTarget{{Label: "latest", PhaseID: "fix", VisitID: "fix@v1"}},
	}
	return run, cp
}

// railLines splits a render into lines and asserts the left-rail invariant:
// every line carries the rail prefix (╔═ header / ║ content or spacer / ╠═
// divider / ╚ tail) — there is never unrailed content, so the frame cannot
// be half-open and a scalar can never smuggle a bare line.
func railLines(t *testing.T, render string) []string {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(render, "\n"), "\n")
	if len(lines) < 8 {
		t.Fatalf("render too short to be a framed checkpoint (%d lines):\n%s", len(lines), render)
	}
	for i, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "╔═"), strings.HasPrefix(ln, "╚"), strings.HasPrefix(ln, "╠═"):
		case ln == "║":
		case strings.HasPrefix(ln, "║ "):
		default:
			t.Fatalf("line %d breaks the left rail: %q", i+1, ln)
		}
	}
	return lines
}

func lineIndex(t *testing.T, lines []string, substr string) int {
	t.Helper()
	for i, ln := range lines {
		if strings.Contains(ln, substr) {
			return i
		}
	}
	t.Fatalf("no line carries %q:\n%s", substr, strings.Join(lines, "\n"))
	return -1
}

// TestRenderLayoutLeftRailFrame pins the frame: the header line keeps the
// canonical signature verbatim, every line is railed, and the tail closes
// the frame.
func TestRenderLayoutLeftRailFrame(t *testing.T) {
	run, cp := layoutRun()
	lines := railLines(t, RenderCheckpointMessage(run, cp))

	head := lines[0]
	if !strings.Contains(head, "⚡ SUMMONER CHECKPOINT cp-9 — nonce 1a2b3c4d — PHASE") {
		t.Fatalf("header must keep the canonical signature verbatim, got: %q", head)
	}
	if !strings.HasPrefix(head, "╔═ ") {
		t.Fatalf("header must open the frame with ╔═, got: %q", head)
	}
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "╚") || strings.Trim(last, "╚═") != "" {
		t.Fatalf("last line must be a bare ╚═ tail, got: %q", last)
	}
}

// TestRenderLayoutLeadsWithTheConclusion pins the reading order on the
// largest legal content: identity, the earlier phases (经过), the conclusion
// in its fixed inner order (summary → evidence → how → open questions →
// impact), then the decision divider with page 1 and the navigation hint.
func TestRenderLayoutLeadsWithTheConclusion(t *testing.T) {
	run, cp := layoutRun()
	lines := railLines(t, RenderCheckpointMessage(run, cp))

	if !strings.Contains(lines[1], "run: run-1 · workflow: fix · phase: regression · requirements v2") {
		t.Fatalf("second line must carry run identity and the phase, got: %q", lines[1])
	}
	order := []string{
		"║ ▶ 经过",
		"║ ▶ 结论 — 未查清",
		"回归未完全通过: 3 个用例在 team 模式下失败",
		"证据:",
		"task_test.go:88 TestTeamLoad",
		"task_test.go:120 TestTeamRecall",
		"task_test.go:141 TestSubTaskDone",
		"怎么查的: 跑了 go test ./task/... 并按失败用例回溯调用链",
		"没查清 · 需要你判断:",
		"team 模式下英雄禁用规则是否应该作用于子任务",
		"影响: 只影响 team 模式的任务列表加载",
	}
	prev := 1
	for _, want := range order {
		idx := lineIndex(t, lines, want)
		if idx <= prev {
			t.Fatalf("%q out of order (index %d after %d):\n%s", want, idx, prev, strings.Join(lines, "\n"))
		}
		prev = idx
	}

	// The decision divider follows the conclusion; page 1 follows it
	// verbatim, then ONE navigation-hint line carrying the nonce, then the
	// tail.
	divider := lineIndex(t, lines, "╠═ 请决定")
	if divider <= prev {
		t.Fatalf("the decision divider must follow the conclusion (divider %d, conclusion %d)", divider, prev)
	}
	page := cp.MenuPages[0].Labels
	if len(lines) != divider+1+len(page)+1+1 {
		t.Fatalf("after the divider the render must be page 1 (%d labels) + one hint + tail, got %d lines:\n%s", len(page), len(lines)-divider-1, strings.Join(lines, "\n"))
	}
	for i, label := range page {
		if got := lines[divider+1+i]; got != "║   "+label {
			t.Fatalf("page-1 label %d = %q, want %q", i, got, "║   "+label)
		}
	}
	hint := lines[divider+1+len(page)]
	if !strings.Contains(hint, "nonce 1a2b3c4d") || !strings.Contains(hint, "more:") || !strings.Contains(hint, "summoner-status") {
		t.Fatalf("the navigation hint must carry the nonce, the more: cue and the status pointer, got: %q", hint)
	}
}

// TestRenderOutcomeLabels: the conclusion head names what the phase
// established; only an unclear conclusion opens the "需要你判断" block.
func TestRenderOutcomeLabels(t *testing.T) {
	run, cp := layoutRun()
	base := *cp.Conclusion
	base.OpenQuestions = nil

	cp.Outcome, cp.Conclusion = ConclusionConfirmed, &base
	confirmed := RenderCheckpointMessage(run, cp)
	if !strings.Contains(confirmed, "║ ▶ 结论 — 已确认") || strings.Contains(confirmed, "没查清") {
		t.Fatalf("a confirmed conclusion must read 已确认 with no open-question block:\n%s", confirmed)
	}

	cp.Outcome = ConclusionFailed
	failed := RenderCheckpointMessage(run, cp)
	if !strings.Contains(failed, "║ ▶ 结论 — 未达成") || strings.Contains(failed, "没查清") {
		t.Fatalf("a failed conclusion must read 未达成 with no open-question block:\n%s", failed)
	}

	// Unclear with NO listed question still names the block and says the
	// submitter provided none — a hole is never hidden by omission.
	cp.Outcome = ConclusionUnclear
	unclear := RenderCheckpointMessage(run, cp)
	if !strings.Contains(unclear, "没查清 · 需要你判断:") || !strings.Contains(unclear, "未提供") {
		t.Fatalf("an unclear conclusion with no listed questions must say so:\n%s", unclear)
	}
}

// TestRenderJourneyCarriesEarlierConclusions: the 经过 block is the run's
// settled history — each line a phase, how it was settled and its title — so
// a checkpoint can be read without the conversation.
func TestRenderJourneyCarriesEarlierConclusions(t *testing.T) {
	run, cp := layoutRun()
	lines := railLines(t, RenderCheckpointMessage(run, cp))

	start := lineIndex(t, lines, "║ ▶ 经过")
	end := lineIndex(t, lines, "║ ▶ 结论")
	if lines[end-1] != "║" {
		t.Fatalf("the 经过 block must close with a blank rail line, got %q", lines[end-1])
	}
	end--
	if got := end - start - 1; got != capJourneyLines {
		t.Fatalf("经过 renders %d lines with eight settled visits, want the cap %d", got, capJourneyLines)
	}
	body := lines[start+1 : end]
	if !strings.Contains(body[0], "另 3 个阶段") {
		t.Fatalf("with more settled visits than the cap the first line must count the overflow, got: %q", body[0])
	}
	if len(body) != capJourneyLines {
		t.Fatalf("经过 body = %d lines, want %d", len(body), capJourneyLines)
	}
	// Oldest first, the five newest visits after the overflow count.
	want := []string{
		"fix@v1 ✓ 已验收: fix applied",
		"regression@v1 — 已跳过: diff under 5 lines",
		"context@v2 ✓ 自动通过: same repo, new branch",
		"diagnosis@v2 ✓ 已验收: second root cause",
		"fix@v2 ✓ 已验收: fix re-applied",
	}
	for i, w := range want {
		if !strings.Contains(body[i+1], w) {
			t.Errorf("经过 line %d = %q, want it to carry %q", i+1, body[i+1], w)
		}
	}
	// The three oldest visits rolled off with the overflow count, and the
	// visit still in work (regression@v2) is not history.
	joined := strings.Join(body, "\n")
	for _, absent := range []string{"monorepo, Go 1.16", "nil deref in the task loader", "repro script", "regression@v2"} {
		if strings.Contains(joined, absent) {
			t.Errorf("经过 must not carry %q:\n%s", absent, joined)
		}
	}
}

// TestRenderLayoutMovesMechanismToStatus: the checkpoint render carries no
// acceptance criteria, artifact digests, requirement snapshot, planned scope,
// environment, budget keys, page projection or recall-target list — and no
// option beyond page 1. They are what summoner-status is for.
func TestRenderLayoutMovesMechanismToStatus(t *testing.T) {
	run, cp := layoutRun()
	render := RenderCheckpointMessage(run, cp)

	// (The navigation hint may NAME criteria and artifacts as pointers; the
	// content itself must not be here.)
	for _, absent := range []string{
		"crit-evid", "crit-root", "crit-legacy", "[mechanical", "[judgment",
		"5f4a3b2c1d0e", "@ ",
		"登录后任务列表可加载", "需求快照", "planned", "internal/api/",
		"tier=", "验证环境", "defaults=",
		"phase_tool_calls=", "counters ", "预算",
		"page 1/", "page 2/", "paging",
		"recall targets:", "(phase fix, visit",
		"recall: 回城", "review: [v]",
	} {
		if strings.Contains(render, absent) {
			t.Errorf("the checkpoint render must not carry %q (it lives in summoner-status):\n%s", absent, render)
		}
	}
}

// TestRenderLayoutKeepsDecisionRelevantRides: the two rides the user cannot
// decide without stay in the render — the exhaustion extend offer (its
// answer rides the decision call) and a pending review retry (it redefines
// what continue/skip mean at that checkpoint).
func TestRenderLayoutKeepsDecisionRelevantRides(t *testing.T) {
	run, cp := layoutRun()
	cp.ExtendAsk = &ExtendAsk{Delta: BudgetLimits{RunRework: 2}, ExtendLabel: "extend: 追加已显示额度", HoldLabel: "extend: 不追加"}
	render := railLines(t, RenderCheckpointMessage(run, cp))
	joined := strings.Join(render, "\n")
	for _, want := range []string{"extend: 追加已显示额度", "extend: 不追加", "run_rework +2"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the exhaustion extend offer must stay in the render, lost %q:\n%s", want, joined)
		}
	}

	retry := CheckpointState{
		ID: "cp-10", Kind: CheckpointReviewResult, Nonce: "deadbeef", Note: "COMPLETED NEEDS-FIX",
		ReviewRetry: &ReviewRetry{ReviewID: "rev-1", AttemptID: "att-1", ResumeTarget: "fix@v1", RetryLabel: "Continue: retry the review", SkipLabel: "Skip: finish UNREVIEWED"},
	}
	joined = strings.Join(railLines(t, RenderCheckpointMessage(run, retry)), "\n")
	for _, want := range []string{"COMPLETED NEEDS-FIX", "review retry: review rev-1 attempt att-1", `retry="Continue: retry the review"`, `skip="Skip: finish UNREVIEWED"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("a pending review retry must stay in the render, lost %q:\n%s", want, joined)
		}
	}
}

// TestRenderCapsTheConclusion: every conclusion part is capped — items per
// list, runes per line — and an overflow is COUNTED, never silently dropped.
func TestRenderCapsTheConclusion(t *testing.T) {
	run, cp := layoutRun()
	cp.Conclusion = &Conclusion{
		Summary:       longText(capConclusionText + 80),
		Evidence:      make([]string, 9),
		Approach:      longText(capConclusionText + 80),
		OpenQuestions: make([]string, 7),
		Impact:        longText(capConclusionShort + 80),
	}
	for i := range cp.Conclusion.Evidence {
		cp.Conclusion.Evidence[i] = "ev" + longText(capConclusionShort+40)
	}
	for i := range cp.Conclusion.OpenQuestions {
		cp.Conclusion.OpenQuestions[i] = "q" + longText(capConclusionShort+40)
	}
	cp.Outcome = ConclusionUnclear
	lines := railLines(t, RenderCheckpointMessage(run, cp))

	count := func(substr string) int {
		n := 0
		for _, ln := range lines {
			if strings.Contains(ln, substr) {
				n++
			}
		}
		return n
	}
	if got := count("    - ev"); got != capEvidenceItems {
		t.Errorf("evidence lines = %d, want the cap %d", got, capEvidenceItems)
	}
	if got := count("    - q"); got != capOpenQuestionItems {
		t.Errorf("open-question lines = %d, want the cap %d", got, capOpenQuestionItems)
	}
	for _, want := range []string{"…另 5 条，见 summoner-status", "…另 4 条，见 summoner-status"} {
		if count(want) != 1 {
			t.Errorf("an overflowed list must count what rolled off (%q):\n%s", want, strings.Join(lines, "\n"))
		}
	}

	// Runes per line: the conclusion lines stay within their cap plus the
	// ellipsis — a 280-rune summary cannot be told from the render.
	summary := lines[lineIndex(t, lines, "║   "+longText(20))]
	if n := utf8.RuneCountInString(strings.TrimPrefix(summary, "║   ")); n != capConclusionText {
		t.Errorf("an over-long summary clips to %d runes, want exactly the cap %d", n, capConclusionText)
	}
	if !strings.HasSuffix(summary, "…") {
		t.Errorf("a clipped summary must end in an ellipsis, got %q", summary)
	}
	for _, ln := range lines {
		if !strings.HasPrefix(ln, "║     - ") {
			continue
		}
		if n := utf8.RuneCountInString(strings.TrimPrefix(ln, "║     - ")); n > capConclusionShort {
			t.Errorf("list item carries %d runes, over the per-item cap %d", n, capConclusionShort)
		}
	}
}

// TestRenderMaxLegalSubmissionStaysUnderLineCap: the largest legal submission
// — six journey lines, every conclusion part at its cap, both rides — stays
// under the line cap counted in newlines.
func TestRenderMaxLegalSubmissionStaysUnderLineCap(t *testing.T) {
	run, cp := layoutRun()
	cp.Conclusion = &Conclusion{
		Summary:       longText(capConclusionText),
		Evidence:      []string{"a", "b", "c", "d"},
		Approach:      longText(capConclusionText),
		OpenQuestions: []string{"q1", "q2", "q3"},
		Impact:        longText(capConclusionShort),
	}
	cp.Outcome = ConclusionUnclear
	cp.ExtendAsk = &ExtendAsk{Delta: BudgetLimits{RunRework: 2}, ExtendLabel: "extend: 追加已显示额度", HoldLabel: "extend: 不追加"}
	// A retry cannot ride a PHASE checkpoint; the widest shape is the
	// REVIEW_RESULT kind with the retry line and a four-label page.
	cp.Kind, cp.Conclusion = CheckpointReviewResult, nil
	cp.Note = longText(capConclusionText)
	cp.ReviewRetry = &ReviewRetry{ReviewID: "rev-1", AttemptID: "att-1", ResumeTarget: "fix@v1", RetryLabel: "Continue: retry the review", SkipLabel: "Skip: finish UNREVIEWED"}

	widest := 0
	for _, shape := range []CheckpointState{layoutRunCP(), cp} {
		n := strings.Count(RenderCheckpointMessage(run, shape), "\n")
		if n > renderLineCap {
			t.Errorf("the largest legal submission renders %d lines, over the cap %d:\n%s", n, renderLineCap, RenderCheckpointMessage(run, shape))
		}
		if n > widest {
			widest = n
		}
	}
	if widest < 20 {
		t.Fatalf("fixture suspicious: the widest render is only %d lines — the cap test is not exercising the layout", widest)
	}
}

// layoutRunCP returns the max-size PHASE checkpoint (conclusion at every
// cap, both rides that can coexist with a phase submission).
func layoutRunCP() CheckpointState {
	_, cp := layoutRun()
	cp.Conclusion = &Conclusion{
		Summary:       longText(capConclusionText),
		Evidence:      []string{"a", "b", "c", "d"},
		Approach:      longText(capConclusionText),
		OpenQuestions: []string{"q1", "q2", "q3"},
		Impact:        longText(capConclusionShort),
	}
	cp.ExtendAsk = &ExtendAsk{Delta: BudgetLimits{RunRework: 2}, ExtendLabel: "extend: 追加已显示额度", HoldLabel: "extend: 不追加"}
	return cp
}

// TestRenderLegacySubmissionSaysWhatIsMissing: a submission that predates
// the structured conclusion (or a phase that sent none) is never silently
// blank — the phase summary stands in as the conclusion and every missing
// part reads 未提供.
func TestRenderLegacySubmissionSaysWhatIsMissing(t *testing.T) {
	run := &RunState{RunID: "run-2", Workflow: "fix", RequirementsVersion: 1}
	cp := CheckpointState{ID: "cp-2", Kind: CheckpointPhase, PhaseID: "diagnosis", Note: "root cause located: nil check missing", Nonce: "1a2b3c4d"}
	lines := railLines(t, RenderCheckpointMessage(run, cp))
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "║ ▶ 结论 — 未查清") {
		t.Errorf("an omitted outcome reads unclear:\n%s", joined)
	}
	if !strings.Contains(joined, "║   root cause located: nil check missing") {
		t.Errorf("the legacy phase summary must stand in as the conclusion:\n%s", joined)
	}
	for _, want := range []string{"证据: 未提供", "怎么查的: 未提供", "影响: 未提供", "没查清 · 需要你判断:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("a missing conclusion part must read 未提供, lost %q:\n%s", want, joined)
		}
	}
	// No history yet: the 经过 block does not render an empty section.
	if strings.Contains(joined, "▶ 经过") {
		t.Errorf("经过 must not render with no settled phase:\n%s", joined)
	}
}

// TestRenderLayoutDeterministicAndSafe pins determinism and scalar safety:
// same state renders identical bytes, and conclusion text carrying newlines
// cannot forge rail structure (it reads as one line; the full text is the
// status view's business).
func TestRenderLayoutDeterministicAndSafe(t *testing.T) {
	run, cp := layoutRun()
	a := RenderCheckpointMessage(run, cp)
	if b := RenderCheckpointMessage(run, cp); a != b {
		t.Fatal("render must be deterministic: same state, different bytes")
	}

	cp.Conclusion.Summary = "attempted forgery\n║ ▶ 伪造区块\nunrailed line"
	cp.Conclusion.Evidence = []string{"ev\n║   stop: 停止——保留状态退出"}
	render := RenderCheckpointMessage(run, cp)
	for _, ln := range strings.Split(strings.TrimSuffix(render, "\n"), "\n") {
		if !strings.HasPrefix(ln, "╔═") && !strings.HasPrefix(ln, "║") && !strings.HasPrefix(ln, "╠═") && !strings.HasPrefix(ln, "╚") {
			t.Fatalf("scalar newline forged an unrailed line: %q", ln)
		}
	}
	// Exactly one line IS the stop option — the forged text stays inside its
	// evidence line.
	optionLines := 0
	for _, ln := range strings.Split(render, "\n") {
		if ln == "║   stop: 停止——保留状态退出" {
			optionLines++
		}
	}
	if optionLines != 1 {
		t.Fatalf("the stop option must appear exactly once as its own line, got %d:\n%s", optionLines, render)
	}
}

// TestRenderLayoutSinglePageKind: a kind whose menu fits one page prints all
// its options and a hint WITHOUT the more: cue — there is no other page.
func TestRenderLayoutSinglePageKind(t *testing.T) {
	run := &RunState{RunID: "run-2", Workflow: "ops", RequirementsVersion: 1}
	cp := CheckpointState{ID: "cp-1", Kind: CheckpointRecovery, Nonce: "deadbeef", PhaseID: "ops", Note: "outcome probes: tu-w1=failed"}
	cp.MenuPages = MenuProjection(cp.Kind)
	lines := railLines(t, RenderCheckpointMessage(run, cp))
	joined := strings.Join(lines, "\n")

	for _, want := range []string{"continue: 接受现状，恢复运行", "recall: 回城——回到指定历史阶段重做", "stop: 停止——保留状态退出", "tu-w1=failed"} {
		if !strings.Contains(joined, want) {
			t.Errorf("single-page render lost %q:\n%s", want, joined)
		}
	}
	hint := lines[len(lines)-2]
	if !strings.Contains(hint, "nonce deadbeef") || strings.Contains(hint, "more:") || !strings.Contains(hint, "summoner-status") {
		t.Fatalf("a single-page kind needs no more: cue but keeps the nonce and the status pointer, got: %q", hint)
	}
}
