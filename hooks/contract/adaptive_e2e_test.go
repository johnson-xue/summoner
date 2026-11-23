package contract

// adaptive_e2e_test.go — 0.7.0 adaptive checkpoints over the REAL hook
// binaries (stdin/stdout seam; design
// docs/superpowers/specs/2026-11-23-adaptive-checkpoints-design.md §6). Each
// test pins one acceptance criterion of the design against the shipped
// artifacts, not the in-process packages.

import (
	"strings"
	"testing"
	"unicode/utf8"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// statusView runs the summoner-status binary (the viewer the model invokes
// over Bash) with a payload and returns its stdout.
func statusView(t *testing.T, payload string) string {
	t.Helper()
	code, out, stderr := runBinary(t, "summoner-status", payload, nil)
	if code != 0 {
		t.Fatalf("summoner-status exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	return out
}

// TestAdaptiveRenderLeadsWithConclusionOverBinaries — acceptance criterion
// 6: a diagnosis checkpoint submitted with the LARGEST legal conclusion
// renders in ≤ 42 lines, carries no review criteria, environment line,
// budget keys or page projection, and keeps exactly one navigation hint.
// The conclusion is the first thing after the history block.
func TestAdaptiveRenderLeadsWithConclusionOverBinaries(t *testing.T) {
	useContractEnv(t)
	r := seedLiveRun(t, "run-render", "fix")

	// context: skill-less submit (the gate auto-opens it), then a human
	// CONTINUE through the stop / gate / posttooluse binaries.
	submitPhaseViaGate(t, "tu-ps-1", "context", nil)
	r, _ = shared.LoadRun("run-render")
	renderAndDecide(t, "run-render", "tu-ask-1", r.CurrentCheckpoint(), 0,
		func(labels []string) string { return firstLabelWithKeyword(labels, "continue") })

	// diagnosis: auto-opened by the submit (the next expected phase), with
	// every conclusion part at its cap and the outcome unclear (the widest
	// shape: an unclear conclusion adds the open-questions block).
	long := strings.Repeat("查", 200)
	short := strings.Repeat("证", 120)
	code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Skill", "tu-ps-2", map[string]interface{}{
		"skill": "summoner:phase-submit",
		"args": mustJSON(t, shared.PhaseSubmission{
			PhaseID: "diagnosis",
			Summary: "nil deref in the task loader",
			Outcome: shared.ConclusionUnclear,
			Conclusion: &shared.Conclusion{
				Summary:       long,
				Evidence:      []string{short, short, short, short},
				Approach:      long,
				OpenQuestions: []string{short, short, short},
				Impact:        short,
			},
		}),
	}), nil)
	if code != 0 {
		t.Fatalf("phase-submit(diagnosis) exit = %d, want 0 (stderr: %q)", code, stderr)
	}

	out := statusView(t, `{"session_id":"`+contractSession+`","view":"checkpoint"}`)
	if n := strings.Count(out, "\n"); n > 42 {
		t.Fatalf("the largest legal diagnosis checkpoint renders %d lines, over the 42-line cap:\n%s", n, out)
	}
	for _, absent := range []string{"crit-", "[judgment", "[mechanical", "tier=", "phase_tool_calls=", "page 1/", "recall targets:", "validation_environment"} {
		if strings.Contains(out, absent) {
			t.Errorf("the checkpoint render must not carry %q:\n%s", absent, out)
		}
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	hints := 0
	for _, ln := range lines {
		if strings.Contains(ln, "more:") && strings.Contains(ln, "summoner-status") {
			hints++
		}
	}
	if hints != 1 {
		t.Fatalf("the render must carry exactly ONE navigation-hint line, got %d:\n%s", hints, out)
	}
	// The conclusion leads: its (clipped) summary line comes before the
	// evidence list, the evidence before the open questions, and the first
	// thing after the identity line is the history block.
	idx := func(substr string) int {
		for i, ln := range lines {
			if strings.Contains(ln, substr) {
				return i
			}
		}
		return -1
	}
	a, b, c, d := idx("▶ 经过"), idx("▶ 结论 — 未查清"), idx("  证据:"), idx("没查清 · 需要你判断")
	if !(0 < a && a < b && b < c && c < d) {
		t.Fatalf("reading order broken (经过 %d, 结论 %d, 证据 %d, 没查清 %d):\n%s", a, b, c, d, out)
	}
	// …and the line right under the 结论 head is the summary, all 200 runes
	// of it (at the cap, so nothing was clipped).
	if s := strings.TrimPrefix(lines[b+1], "║   "); !strings.HasPrefix(s, strings.Repeat("查", 40)) || utf8.RuneCountInString(s) != 200 {
		t.Fatalf("the conclusion summary must be the first line of 结论 at its full 200-rune cap, got %d runes:\n%s", utf8.RuneCountInString(s), out)
	}
}
