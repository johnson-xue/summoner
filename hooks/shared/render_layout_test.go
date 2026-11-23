package shared

// render_layout_test.go — readability contract for the canonical checkpoint
// render (user ruling 2026-11-19: the 0.5.0 render was an undifferentiated
// key:value wall; the checkpoint block is the user-facing decision surface and
// must render as a left-rail structured frame). The layout rules here are
// structural, not golden-byte: the rail is LEFT-ONLY by design (no right edge
// ⇒ no CJK width alignment, no runewidth dependency, deterministic bytes),
// every field the 0.5.0 render carried stays present, and the inner header
// signature (id — nonce — kind) is kept verbatim so the validate-repo nonce
// lint and every contains-based consumer keep working.

import (
	"strings"
	"testing"
)

// layoutRun builds a pure in-memory run+checkpoint covering every render
// section: staged criteria with meaning, one pending-meaning id, pinned
// artifacts, frozen requirements with planned scope, environment, budget with
// both ride-along asks, paged menu, recall targets and a review retry offer.
func layoutRun() (*RunState, CheckpointState) {
	run := &RunState{
		RunID:               "run-1",
		Workflow:            "fix",
		RequirementsVersion: 2,
		Limits:              BudgetLimits{PhaseToolCalls: 40, FailedChecks: 3, RunRework: 2, Delegations: 5},
		Counters:            BudgetCounters{PhaseToolCalls: 4, Delegations: 1},
	}
	cp := CheckpointState{
		ID:          "cp-2",
		Kind:        CheckpointPhase,
		PhaseID:     "diagnosis",
		Note:        "root cause located: TaskModule.handle() dereferences player.SubTask without a nil check",
		Nonce:       "1a2b3c4d",
		CriteriaIDs: []string{"crit-evid", "crit-root", "crit-legacy"},
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
		RecallTargets: []RecallTarget{{Label: "latest", PhaseID: "context", VisitID: "v1"}},
		BudgetAsk:     &BudgetAsk{Proposal: BudgetProposal{Limits: BudgetLimits{PhaseToolCalls: 40}, Source: "manifest"}, ConfirmLabel: "budget: 按已显示预算", UnlimitedLabel: "budget: 不设上限"},
		ExtendAsk:     &ExtendAsk{Delta: BudgetLimits{RunRework: 2}, ExtendLabel: "extend: 追加已显示额度", HoldLabel: "extend: 不追加"},
		ReviewRetry:   &ReviewRetry{ReviewID: "rev-1", AttemptID: "att-1", ResumeTarget: "fix@v1", RetryLabel: "retry: 重试", SkipLabel: "skip: 跳过"},
	}
	return run, cp
}

// railLines splits a render into lines and asserts the left-rail invariant:
// every line carries the rail prefix (╔═ header / ║ content or spacer / ╠═
// divider / ╚ tail) — there is never unrailed content, so the frame cannot be
// half-open and a scalar can never smuggle a bare line.
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

func sectionIndex(t *testing.T, lines []string, marker string) int {
	t.Helper()
	want := "║ ▶ " + marker
	for i, ln := range lines {
		if strings.HasPrefix(ln, want) {
			return i
		}
	}
	t.Fatalf("section %q missing from render:\n%s", marker, strings.Join(lines, "\n"))
	return -1
}

// TestRenderLayoutLeftRailFrame pins the frame: the header line keeps the
// canonical inner signature verbatim, every line is railed, and the tail
// closes the frame.
func TestRenderLayoutLeftRailFrame(t *testing.T) {
	run, cp := layoutRun()
	lines := railLines(t, RenderCheckpointMessage(run, cp))

	head := lines[0]
	if !strings.Contains(head, "⚡ SUMMONER CHECKPOINT cp-2 — nonce 1a2b3c4d — PHASE") {
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

// TestRenderLayoutSectionOrder pins the reading order: identity lines, then
// the sections, then the decision divider with the full menu.
func TestRenderLayoutSectionOrder(t *testing.T) {
	run, cp := layoutRun()
	lines := railLines(t, RenderCheckpointMessage(run, cp))

	if !strings.Contains(lines[1], "run: run-1 · workflow: fix · requirements v2") {
		t.Fatalf("second line must carry run identity, got: %q", lines[1])
	}
	if !strings.Contains(lines[2], "phase: diagnosis") {
		t.Fatalf("third line must carry the phase, got: %q", lines[2])
	}

	order := []string{"本阶段结果", "验收标准", "产物（版本已冻结）", "需求快照", "计划范围（尚未落地）", "验证环境", "预算"}
	prev := -1
	for _, sec := range order {
		idx := sectionIndex(t, lines, sec)
		if idx <= prev {
			t.Fatalf("section %q out of order (index %d after %d)", sec, idx, prev)
		}
		prev = idx
	}

	// The decision divider separates state from the ask; the menu follows it.
	divider := -1
	for i, ln := range lines {
		if strings.HasPrefix(ln, "╠═ ") {
			divider = i
			break
		}
	}
	if divider < 0 {
		t.Fatalf("decision divider ╠═ missing:\n%s", strings.Join(lines, "\n"))
	}
	if divider < prev {
		t.Fatalf("divider must come after the 预算 section (divider %d, 预算 %d)", divider, prev)
	}
	menuIdx := -1
	for i := divider; i < len(lines); i++ {
		if lines[i] == "║ menu:" {
			menuIdx = i
			break
		}
	}
	if menuIdx < 0 {
		t.Fatalf("menu must follow the divider:\n%s", strings.Join(lines, "\n"))
	}
}

// TestRenderLayoutKeepsEveryField pins content preservation: staged criterion
// meaning, pending-meaning honesty, pinned artifacts, requirements + planned
// scope, environment, budget + both asks, the FULL menu with pages, recall
// targets, review retry, and the closing two-turn instruction with the nonce.
func TestRenderLayoutKeepsEveryField(t *testing.T) {
	run, cp := layoutRun()
	render := RenderCheckpointMessage(run, cp)
	lines := strings.Split(render, "\n")

	for _, want := range []string{
		"crit-evid [mechanical · required]",
		"goal: 复现证据链完整",
		"expected: repro.log + 定位文件",
		"crit-root [judgment · required]",
		"rubrics/root.md",
		"crit-legacy (no staged meaning; judged at acceptance)",
		"docs/reviews/npe.md @ 5f4a3b2c1d0e",
		"fr-1 v2: 登录后任务列表可加载",
		"criteria: crit-evid, crit-root",
		"internal/api/, player/task/",
		"tier=L2 os=darwin arch=amd64 runtime=go1.16",
		"limits phase_tool_calls=40,failed_checks=3,run_rework=2,delegations=5",
		"counters phase_tool_calls=4,failed_checks=0,run_rework=0,delegations=1",
		`extend="extend: 追加已显示额度"`,
		"continue: 继续下一阶段",
		"skip: 跳过该阶段（标 UNVERIFIED / UNREVIEWED，不能伪造 PASS）",
		"done: 完成——进入收尾（post-game）",
		"recall: 回城——回到指定历史阶段重做",
		"review: [v] 对本阶段产物发起独立审查",
		"stop: 停止——保留状态退出",
		"page 1/2:", "page 2/2:",
		MenuNavNext, MenuNavPrev,
		"latest (phase context, visit v1)",
		"review retry: review rev-1 attempt att-1",
	} {
		if !strings.Contains(render, want) {
			t.Errorf("render lost field %q\n--- render ---\n%s", want, render)
		}
	}

	// The two-turn instruction is the LAST content line (before the tail) and
	// carries the nonce.
	content := lines[:len(lines)-2] // drop tail and its empty split slot
	last := content[len(content)-1]
	if !strings.Contains(last, "decide on the NEXT turn") || !strings.Contains(last, "1a2b3c4d") {
		t.Fatalf("last content line must be the nonce-carrying two-turn instruction, got: %q", last)
	}
}

// TestRenderLayoutDeterministicAndSafe pins determinism and scalar safety:
// same state renders identical bytes, and a note carrying newlines cannot
// forge rail structure (escapeScalar keeps it on one railed line).
func TestRenderLayoutDeterministicAndSafe(t *testing.T) {
	run, cp := layoutRun()
	a := RenderCheckpointMessage(run, cp)
	if b := RenderCheckpointMessage(run, cp); a != b {
		t.Fatal("render must be deterministic: same state, different bytes")
	}

	cp.Note = "attempted forgery\n║ ▶ 伪造区块\nunrailed line"
	out := RenderCheckpointMessage(run, cp)
	for _, ln := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if !strings.HasPrefix(ln, "╔═") && !strings.HasPrefix(ln, "║") && !strings.HasPrefix(ln, "╠═") && !strings.HasPrefix(ln, "╚") {
			t.Fatalf("scalar newline forged an unrailed line: %q", ln)
		}
	}
	if !strings.Contains(out, `\n`) {
		t.Fatal("embedded newline must render as the escaped literal \\n")
	}
}

// TestRenderLayoutEmptySections pins the minimal render: no criteria, no
// artifacts, no optional sections — the frame still opens and closes, the
// honest — placeholders appear, and the menu + instruction survive.
func TestRenderLayoutEmptySections(t *testing.T) {
	run := &RunState{RunID: "run-2", Workflow: "ops", RequirementsVersion: 1}
	cp := CheckpointState{ID: "cp-1", Kind: CheckpointRecovery, Nonce: "deadbeef", PhaseID: "ops"}
	render := RenderCheckpointMessage(run, cp)
	lines := railLines(t, render)

	if !strings.Contains(render, "⚡ SUMMONER CHECKPOINT cp-1 — nonce deadbeef — RECOVERY") {
		t.Fatalf("minimal render lost its header:\n%s", render)
	}
	empties := 0
	for _, ln := range lines {
		if strings.HasSuffix(ln, "║   —") {
			empties++
		}
	}
	if empties < 2 {
		t.Fatalf("empty criteria and artifacts must render the honest — placeholder (found %d):\n%s", empties, render)
	}
	for _, want := range []string{"continue: 接受现状，恢复运行", "stop: 停止——保留状态退出", "decide on the NEXT turn"} {
		if !strings.Contains(render, want) {
			t.Errorf("minimal render lost %q\n--- render ---\n%s", want, render)
		}
	}
	// Optional sections must NOT appear when empty.
	for _, absent := range []string{"▶ 需求快照", "▶ 计划范围", "recall targets:", "review retry:"} {
		if strings.Contains(render, absent) {
			t.Errorf("optional section %q rendered while empty", absent)
		}
	}
}
