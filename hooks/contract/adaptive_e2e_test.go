package contract

// adaptive_e2e_test.go — 0.7.0 adaptive checkpoints over the REAL hook
// binaries (stdin/stdout seam; design
// docs/superpowers/specs/2026-11-23-adaptive-checkpoints-design.md §6). Each
// test pins one acceptance criterion of the design against the shipped
// artifacts, not the in-process packages.

import (
	"encoding/json"
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

// TestAdaptiveExplainThenAskOverBinaries — acceptance criteria 4 and 5 end
// to end, in the platform's real event order: the model explains, pastes the
// checkpoint and ends the turn → the Stop hook binds the display and BLOCKS
// with the exact question → the gate admits that question → the platform's
// answer decides. No user "continue" anywhere in the sequence.
func TestAdaptiveExplainThenAskOverBinaries(t *testing.T) {
	useContractEnv(t)
	seedLiveRun(t, "run-ask", "fix")
	submitPhaseViaGate(t, "tu-ps-1", "context", nil)
	run, err := shared.LoadRun("run-ask")
	if err != nil {
		t.Fatal(err)
	}
	cp := *run.CurrentCheckpoint()
	if cp.Displayed || cp.Decided {
		t.Fatalf("fixture must sit at an undisplayed checkpoint: %+v", cp)
	}

	// The turn's final message: one line of explanation, then the canonical
	// block exactly as the viewer serves it.
	block := statusView(t, `{"session_id":"`+contractSession+`","view":"checkpoint"}`)
	code, out, stderr := runBinary(t, "stop", stopRenderPayload(t, "context 已确认：Go 1.16 单仓，入口 cmd/server。\n证据见下方检查点，请裁决。\n\n"+block), nil)
	if code != 0 {
		t.Fatalf("stop exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	var stopOut struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &stopOut); err != nil || stopOut.Decision != "block" {
		t.Fatalf("the Stop hook must hand back the ask block after binding, got (err %v) %q", err, out)
	}
	page := shared.DecisionPage(cp)
	for _, want := range append([]string{"已展示并绑定", cp.ID, cp.Nonce, "AskUserQuestion"}, page...) {
		if !strings.Contains(stopOut.Reason, want) {
			t.Errorf("ask block lost %q:\n%s", want, stopOut.Reason)
		}
	}

	run, err = shared.LoadRun("run-ask")
	if err != nil {
		t.Fatal(err)
	}
	if got := run.CurrentCheckpoint(); got == nil || !got.Displayed || got.Decided {
		t.Fatalf("the pasted block must bind the display and decide nothing: %+v", got)
	}

	// A sign-off that is neither the question nor a stop acknowledgment
	// keeps getting the ask block (idempotent, byte-identical).
	_, out2, _ := runBinary(t, "stop", stopRenderPayload(t, "等待你的决定。"), nil)
	if !strings.Contains(out2, `"decision":"block"`) || !strings.Contains(out2, cp.Nonce) {
		t.Fatalf("a displayed, un-asked checkpoint must keep demanding the question, got %q", out2)
	}

	// The gate admits exactly the question the block spelled out.
	code, _, stderr = runBinary(t, "pretooluse-gate", askQuestionPayload(t, "tu-ask-1", cp.Nonce, page), nil)
	if code != 0 {
		t.Fatalf("page-1 ask after display: exit %d, want 0 (stderr: %q)", code, stderr)
	}

	// The platform's answer (CONTINUE) lands through PostToolUse.
	code, _, stderr = runBinary(t, "posttooluse", mustJSON(t, map[string]interface{}{
		"hook_event_name": "PostToolUse",
		"session_id":      contractSession,
		"tool_name":       "AskUserQuestion",
		"tool_use_id":     "tu-ask-1",
		"tool_input":      askQuestionInput(page, cp.Nonce),
		"tool_response":   map[string]interface{}{"answers": map[string]interface{}{askQuestionText(cp.Nonce): page[0]}},
	}), nil)
	if code != 0 {
		t.Fatalf("posttooluse decision: exit %d, want 0 (stderr: %q)", code, stderr)
	}
	run, err = shared.LoadRun("run-ask")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != shared.StatusActive {
		t.Fatalf("status after CONTINUE = %s, want ACTIVE", run.Status)
	}
	for _, c := range run.Checkpoints {
		if c.ID == cp.ID && (!c.Decided || c.Decision != shared.DecisionContinue || c.DecisionToolUseID != "tu-ask-1") {
			t.Fatalf("the answer must decide cp %s CONTINUE via tu-ask-1, got %+v", cp.ID, c)
		}
	}
}

// askQuestionInput is the tool_input half of askQuestionPayload (what
// PostToolUse re-receives with the platform's answer).
func askQuestionInput(page []string, nonce string) map[string]interface{} {
	opts := make([]interface{}, 0, len(page))
	for _, l := range page {
		opts = append(opts, map[string]interface{}{"label": l, "description": "decision option"})
	}
	return map[string]interface{}{"questions": []interface{}{map[string]interface{}{
		"question":    askQuestionText(nonce),
		"header":      "Checkpoint",
		"options":     opts,
		"multiSelect": false,
	}}}
}
