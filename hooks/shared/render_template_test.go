package shared

// render_template_test.go — references/checkpoint-protocol.md carries the
// ONLY in-repo copy of the checkpoint template, and the protocol tells the
// model to read real renders against it. A template that drifts from the
// binary (ship review round 1: a blank rail line the render never draws, a
// navigation hint it no longer prints) documents a surface that does not
// exist. These tests hold the document to the renderer.

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

const protocolDoc = "../../references/checkpoint-protocol.md"

// canonicalTemplate returns the fenced block under the canonical marker,
// inner lines only.
func canonicalTemplate(t *testing.T, doc string) []string {
	t.Helper()
	marker := strings.Index(doc, "canonical:checkpoint-format")
	if marker < 0 {
		t.Fatal("the protocol doc lost its canonical:checkpoint-format marker")
	}
	rest := doc[marker:]
	open := strings.Index(rest, "\n```\n")
	if open < 0 {
		t.Fatal("no fenced block follows the canonical marker")
	}
	body := rest[open+len("\n```\n"):]
	close := strings.Index(body, "\n```\n")
	if close < 0 {
		t.Fatal("the canonical block is not closed")
	}
	return strings.Split(body[:close], "\n")
}

// templatePattern turns one template line into a regular expression: every
// balanced {placeholder} matches any non-empty text, everything else must
// appear literally.
func templatePattern(line string) (*regexp.Regexp, error) {
	var sb strings.Builder
	sb.WriteString(`\A`)
	depth, start := 0, 0
	for i, r := range line {
		switch r {
		case '{':
			if depth == 0 {
				sb.WriteString(regexp.QuoteMeta(line[start:i]))
			}
			depth++
		case '}':
			depth--
			if depth == 0 {
				sb.WriteString(`.+`)
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("unbalanced placeholder in %q", line)
	}
	sb.WriteString(regexp.QuoteMeta(line[start:]))
	sb.WriteString(`\z`)
	return regexp.Compile(sb.String())
}

func widestCheckpoint() (*RunState, CheckpointState) {
	// One line per variable-length list, every optional section present.
	run := &RunState{
		RunID: "run-doc", Workflow: "fix", RequirementsVersion: 1,
		Phases:      []PhaseState{{VisitID: "context@v1", ID: "context", Outcome: "accepted", CheckpointID: "cp-1"}},
		Checkpoints: []CheckpointState{{ID: "cp-1", Note: "context established"}},
	}
	cp := CheckpointState{
		ID: "cp-2", Kind: CheckpointPhase, PhaseID: "diagnosis", Nonce: "1a2b3c4d", Outcome: ConclusionUnclear,
		Conclusion: &Conclusion{Summary: "s", Evidence: []string{"e"}, Approach: "a", OpenQuestions: []string{"q"}, Impact: "i"},
		ExtendAsk:  &ExtendAsk{Delta: BudgetLimits{PhaseToolCalls: 40}, ExtendLabel: "extend: 追加预算", HoldLabel: "hold: 不追加"},
		ReviewRetry: &ReviewRetry{ReviewID: "rev-1", AttemptID: "att-1", ResumeTarget: "implement",
			RetryLabel: "retry: 重审", SkipLabel: "skip-review: 跳过审查"},
		MenuPages: []MenuPage{
			{Labels: []string{"continue: 继续下一阶段"}, NavNext: "more:"},
			{Labels: []string{"recall: 回城"}, NavPrev: "back:"},
		},
	}
	return run, cp
}

// TestCanonicalTemplateIsTheRender — with its {placeholders} as wildcards,
// the documented template matches the render of the widest shape line for
// line: same length, each line described.
func TestCanonicalTemplateIsTheRender(t *testing.T) {
	raw, err := os.ReadFile(protocolDoc)
	if err != nil {
		t.Fatal(err)
	}
	template := canonicalTemplate(t, string(raw))
	run, cp := widestCheckpoint()
	got := strings.Split(strings.TrimSuffix(RenderCheckpointMessage(run, cp), "\n"), "\n")
	if len(template) != len(got) {
		t.Errorf("the template has %d lines, the widest render %d — the document and the binary draw different frames:\n--- template\n%s\n--- render\n%s",
			len(template), len(got), strings.Join(template, "\n"), strings.Join(got, "\n"))
	}
	for i := 0; i < len(template) && i < len(got); i++ {
		pattern, err := templatePattern(template[i])
		if err != nil {
			t.Fatal(err)
		}
		if !pattern.MatchString(got[i]) {
			t.Errorf("template line %d does not describe the render's:\n  template: %q\n  render:  %q", i+1, template[i], got[i])
		}
	}
}

// TestProtocolVariantLinesAreReal — the alternative lines the protocol
// documents (in its table and prose) must be lines the render really
// produces. Each row names a shape, the lines its render must carry, and —
// where the document states it as a form — the document's own wording.
func TestProtocolVariantLinesAreReal(t *testing.T) {
	raw, err := os.ReadFile(protocolDoc)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	settled := func(n int) []PhaseState {
		var ps []PhaseState
		for i := 0; i < n; i++ {
			ps = append(ps, PhaseState{ID: fmt.Sprintf("p%d", i), Outcome: "accepted", CheckpointID: fmt.Sprintf("c%d", i)})
		}
		return ps
	}
	base := func() *RunState { return &RunState{RunID: "run-doc", Workflow: "fix", RequirementsVersion: 1} }
	phaseCP := func() CheckpointState {
		return CheckpointState{ID: "cp-2", Kind: CheckpointPhase, PhaseID: "diagnosis", Nonce: "1a2b3c4d",
			Outcome: ConclusionUnclear, MenuPages: []MenuPage{{Labels: []string{"continue: 继续下一阶段"}}}}
	}
	type line struct{ render, documented string }
	rows := []struct {
		name string
		run  *RunState
		cp   CheckpointState
		want []line
	}{
		{"a submission without a conclusion", base(), func() CheckpointState {
			cp := phaseCP()
			cp.Note = "found it"
			return cp
		}(), []line{
			{"║ ▶ 结论 — 未查清", ""}, {"║   证据: 未提供", ""}, {"║     未提供", ""}, {"║   怎么查的: 未提供", "怎么查的: 未提供"},
		}},
		{"lists past their caps", base(), func() CheckpointState {
			cp := phaseCP()
			cp.Conclusion = &Conclusion{Evidence: []string{"a", "b", "c", "d", "e", "f"}, OpenQuestions: []string{"1", "2", "3", "4"}}
			return cp
		}(), []line{
			{"║     …另 2 条，见 summoner-status", "…另 {n} 条，见 summoner-status"},
		}},
		{"text past its cap", base(), func() CheckpointState {
			cp := phaseCP()
			cp.Conclusion = &Conclusion{Summary: strings.Repeat("句", 300)}
			return cp
		}(), []line{
			{"…（全文见 summoner-status）", ""},
		}},
		{"a journey past its cap", func() *RunState {
			r := base()
			r.Phases = settled(8)
			return r
		}(), phaseCP(), []line{
			{"║   …另 3 个阶段，见 summoner-status", "个阶段，见 summoner-status"},
		}},
		{"the four ways a visit was settled", func() *RunState {
			r := base()
			r.Phases = []PhaseState{
				{ID: "context", Outcome: "accepted", CheckpointID: "c1"},
				{ID: "reproduce", Outcome: "accepted", CheckpointID: "c2"},
				{ID: "fix", Outcome: "skipped", CheckpointID: "c3"},
				{ID: "regression", Outcome: "recalled", CheckpointID: "c4"},
			}
			r.Checkpoints = []CheckpointState{
				{ID: "c1", Note: "ctx"}, {ID: "c2", Note: "repo", Decision: DecisionAutoPass}, {ID: "c3", Note: "fix"}, {ID: "c4", Note: "reg"},
			}
			return r
		}(), phaseCP(), []line{
			{"║   context ✓ 已验收: ctx", "✓ 已验收: "}, {"║   reproduce ✓ 自动通过: repo", "✓ 自动通过: "},
			{"║   fix — 已跳过: fix", "— 已跳过: "}, {"║   regression ↩ 已回城: reg", "↩ 已回城: "},
		}},
		{"a menu that fits one page", base(), func() CheckpointState {
			cp := phaseCP()
			cp.PhaseID, cp.Kind = "", CheckpointChangeConfirm
			return cp
		}(), []line{
			{"选项已全部列出 · 验收标准与产物全文见 summoner-status", ""},
		}},
		{"a checkpoint without a submission", base(), CheckpointState{ID: "cp-3", Kind: CheckpointReviewResult, Nonce: "1a2b3c4d",
			Note: "review rev-1: PASS", MenuPages: []MenuPage{{Labels: []string{"continue: 继续下一阶段"}}}}, []line{
			{"║ ▶ 结论\n║   review rev-1: PASS", "║ ▶ 结论\n║   {note}"},
		}},
		{"a review the framework could not finish", base(), CheckpointState{ID: "cp-3", Kind: CheckpointReviewResult, Nonce: "1a2b3c4d",
			Note: reviewFailedPrefix + "the reviewer child died", MenuPages: []MenuPage{{Labels: []string{"continue: 继续下一阶段"}}}}, []line{
			{"║ ▶ 结论 — 框架未能完成审查", ""}, {"║   原因：the reviewer child died", "║   原因：{…}"}, {"║   对你的影响：改动尚未被审查", ""},
		}},
	}
	for _, row := range rows {
		render := RenderCheckpointMessage(row.run, row.cp)
		for _, l := range row.want {
			if !strings.Contains(render, l.render) {
				t.Errorf("%s: the render does not carry %q:\n%s", row.name, l.render, render)
				continue
			}
			documented := l.documented
			if documented == "" {
				documented = l.render
			}
			if !strings.Contains(doc, documented) {
				t.Errorf("%s: the render says %q but the protocol document never states it (%q not found) — document the line or the form", row.name, l.render, documented)
			}
		}
	}
}
