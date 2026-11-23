package contract

// adaptive_e2e_test.go — 0.7.0 adaptive checkpoints over the REAL hook
// binaries (stdin/stdout seam; design
// docs/superpowers/specs/2026-11-23-adaptive-checkpoints-design.md §6). Each
// test pins one acceptance criterion of the design against the shipped
// artifacts, not the in-process packages.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// TestAdaptiveAutoPassOverBinaries — acceptance criteria 1 and 12 end to
// end over the real binaries: a confirmed context submission passes in the
// submitting call (nothing pending, checkpoint.auto traced, no decision);
// the status view says what to enter next; a turn ended right after it is
// nudged on once; and the real trace scores clean on the gate scorer.
func TestAdaptiveAutoPassOverBinaries(t *testing.T) {
	useContractEnv(t)
	seedLiveRun(t, "run-auto", "fix")

	code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Skill", "tu-ps-1", map[string]interface{}{
		"skill": "summoner:phase-submit",
		"args": mustJSON(t, shared.PhaseSubmission{
			PhaseID: "context", Summary: "context established",
			Outcome: shared.ConclusionConfirmed,
			Conclusion: &shared.Conclusion{
				Summary:  "Go 1.16 monorepo, entry cmd/server",
				Evidence: []string{"go.mod:3", "cmd/server/main.go:12"},
				Approach: "read the module layout and the entry point",
			},
		}),
	}), nil)
	if code != 0 {
		t.Fatalf("confirmed context submission: exit %d, want 0 (stderr: %q)", code, stderr)
	}
	run, err := shared.LoadRun("run-auto")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != shared.StatusActive || run.CurrentCheckpoint() != nil {
		t.Fatalf("after the auto-pass: status %s, pending %+v", run.Status, run.CurrentCheckpoint())
	}
	if run.ContinueNudgeFor == "" {
		t.Fatal("the pass must leave the continuation cue")
	}

	// The real trace: opened → auto, and no decision or display anywhere.
	types := make([]string, 0)
	for _, ev := range allTraceEvents(t) {
		types = append(types, ev.Type)
	}
	if want := "tool.started,phase.started,phase.closed,checkpoint.opened,checkpoint.auto"; strings.Join(types, ",") != want {
		t.Fatalf("trace types = %v, want %v", types, want)
	}

	// The status view names the phase to enter and renders no checkpoint.
	out := statusView(t, `{"session_id":"`+contractSession+`"}`)
	if !strings.Contains(out, "auto-passed: 阶段 context 已自动通过 → 立即进入 diagnosis") {
		t.Fatalf("the status view must say the phase passed and what to enter:\n%s", out)
	}
	if strings.Contains(out, "SUMMONER CHECKPOINT") {
		t.Fatalf("a passed phase renders no checkpoint:\n%s", out)
	}

	// The turn ends right after the pass: nudged once, with the phase to enter.
	code, out, stderr = runBinary(t, "stop", stopRenderPayload(t, "context 阶段已提交。"), nil)
	if code != 0 {
		t.Fatalf("stop exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if !strings.Contains(out, `"decision":"block"`) || !strings.Contains(out, "立即进入 diagnosis") {
		t.Fatalf("a turn ended right after an auto-pass must be nudged on once, got %q", out)
	}
	code, out, stderr = runBinary(t, "stop", stopRenderPayload(t, "context 阶段已提交。"), nil)
	if code != 0 || strings.Contains(out, "block") {
		t.Fatalf("one continuation nudges at most once — second stop: (code, stdout) = (%d, %q)", code, out)
	}

	// The real binary-produced trace is what the gate scorer expects.
	trace := filepath.Join(os.Getenv("SUMMONER_TRACE_DIR"), shared.ProjectKey(), "run-auto.jsonl")
	scorer, err := filepath.Abs("../../scorers/deterministic/user-gate-check.sh")
	if err != nil {
		t.Fatal(err)
	}
	scored, err := exec.Command("bash", scorer, trace).CombinedOutput()
	if err != nil {
		t.Fatalf("user-gate-check on the real auto-pass trace: %v\n%s", err, scored)
	}
	if !strings.Contains(string(scored), "1 checkpoint(s) auto-passed") {
		t.Fatalf("the gate scorer must report the auto-pass:\n%s", scored)
	}
}

// TestAdaptiveRejectionIsVisibleOverBinaries — acceptance criterion 7 over
// the real binary, in the incident's shape: the user types words into the
// decision dialog instead of clicking an option. PostToolUse answers with a
// block whose WHOLE stdout is one JSON object carrying the decision, the
// user's words, the fact that nothing was recorded, the legal next step at
// this checkpoint and the exact question to re-ask. A valid answer right
// after is recorded and writes nothing to stdout.
func TestAdaptiveRejectionIsVisibleOverBinaries(t *testing.T) {
	useContractEnv(t)
	seedLiveRun(t, "run-reject", "fix")
	submitPhaseViaGate(t, "tu-ps-1", "context", nil)
	run, err := shared.LoadRun("run-reject")
	if err != nil {
		t.Fatal(err)
	}
	cp := *run.CurrentCheckpoint()
	page := shared.DecisionPage(cp)
	block := statusView(t, `{"session_id":"`+contractSession+`","view":"checkpoint"}`)
	if code, _, stderr := runBinary(t, "stop", stopRenderPayload(t, block), nil); code != 0 {
		t.Fatalf("stop exit = %d, want 0 (stderr: %q)", code, stderr)
	}

	ask := func(id, answer string) (int, string, string) {
		return runBinary(t, "posttooluse", mustJSON(t, map[string]interface{}{
			"hook_event_name": "PostToolUse",
			"session_id":      contractSession,
			"tool_name":       "AskUserQuestion",
			"tool_use_id":     id,
			"tool_input":      askQuestionInput(page, cp.Nonce),
			"tool_response":   map[string]interface{}{"answers": map[string]interface{}{askQuestionText(cp.Nonce): answer}},
		}), nil)
	}

	const typed = "为什么会禁用英雄，先解释"
	code, stdout, stderr := ask("tu-ask-1", typed)
	if code != 0 {
		t.Fatalf("rejected decision: exit %d, want 0 (stderr: %q)", code, stderr)
	}
	var out struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil || out.Decision != "block" {
		t.Fatalf("the rejection must be ONE JSON block object on stdout, got (err %v) %q", err, stdout)
	}
	for _, want := range append([]string{cp.Nonce, typed, "未记录", "未追加预算", "只读调查", "recall:", "summoner:change"}, page...) {
		if !strings.Contains(out.Reason, want) {
			t.Errorf("the block reason lost %q:\n%s", want, out.Reason)
		}
	}
	run, err = shared.LoadRun("run-reject")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != shared.StatusWaitUser || run.CurrentCheckpoint() == nil || run.CurrentCheckpoint().Decided {
		t.Fatalf("a rejected decision must decide nothing: status %s", run.Status)
	}

	// The model follows the recipe: a valid answer lands, silently.
	code, stdout, stderr = ask("tu-ask-2", page[0])
	if code != 0 {
		t.Fatalf("valid decision: exit %d, want 0 (stderr: %q)", code, stderr)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("a recorded decision must write nothing to stdout, got %q", stdout)
	}
	if run, _ = shared.LoadRun("run-reject"); run.Status != shared.StatusActive {
		t.Fatalf("status after a valid CONTINUE = %s, want ACTIVE", run.Status)
	}
}

// TestAdaptiveStopAckStrippingOverBinaries — the fix round's stop guard at
// the seam (ship review round 2, compat F2: no test pinned the framework-text
// stripping, so reverting it left the suite green). A checkpoint's own
// conclusion line, pasted alone, is framework text and never ends the turn —
// before the display (the render is demanded) and after it (the question is
// handed back). A request-tied acknowledgment ends the turn in both states:
// stopping must stay possible.
func TestAdaptiveStopAckStrippingOverBinaries(t *testing.T) {
	useContractEnv(t)
	seedLiveRun(t, "run-ack", "fix")
	if code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Skill", "tu-ps-1", map[string]interface{}{
		"skill": "summoner:phase-submit",
		"args": mustJSON(t, shared.PhaseSubmission{
			PhaseID: "context", Summary: "握手超时后服务停止",
			Conclusion: &shared.Conclusion{Summary: "握手超时后服务停止"},
		}),
	}), nil); code != 0 {
		t.Fatalf("phase-submit(context) exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	conclusionLine := "║   握手超时后服务停止" // the rendered conclusion, byte for byte

	code, out, _ := runBinary(t, "stop", stopRenderPayload(t, conclusionLine), nil)
	if code != 0 || !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("undisplayed: the conclusion line pasted alone must not end the turn: %q", out)
	}
	if code, out, _ = runBinary(t, "stop", stopRenderPayload(t, "已按你的要求停止了本次工作流。"), nil); code != 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("undisplayed: a request-tied stop must end the turn, got %q", out)
	}

	render := statusView(t, `{"session_id":"`+contractSession+`","view":"checkpoint"}`)
	if code, _, stderr := runBinary(t, "stop", stopRenderPayload(t, render), nil); code != 0 {
		t.Fatalf("bind exit = %d (stderr: %q)", code, stderr)
	}
	code, out, _ = runBinary(t, "stop", stopRenderPayload(t, conclusionLine), nil)
	if code != 0 || !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("displayed: the conclusion line pasted alone must hand the question back, got %q", out)
	}
	if code, out, _ = runBinary(t, "stop", stopRenderPayload(t, "I've stopped the run as you asked."), nil); code != 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("displayed: a request-tied stop must end the turn, got %q", out)
	}
}

// TestAdaptiveStopIgnoresFrameworkTextOverBinaries — ship review round 1:
// the framework's own words must never read as a stop acknowledgment. A
// checkpoint whose CONCLUSION says 停止/abort/终止 (ordinary diagnosis
// vocabulary), re-pasted by the model, or the whole status view, or a
// sentence listing the options — each must hand back the question. Only an
// explicit acknowledgment of the user's stop request ends the turn.
func TestAdaptiveStopIgnoresFrameworkTextOverBinaries(t *testing.T) {
	useContractEnv(t)
	seedLiveRun(t, "run-ack", "fix")
	code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Skill", "tu-ps-1", map[string]interface{}{
		"skill": "summoner:phase-submit",
		"args": mustJSON(t, shared.PhaseSubmission{
			PhaseID: "context", Summary: "服务停止响应",
			Conclusion: &shared.Conclusion{
				Summary:  "服务在收到 SIGTERM 后停止响应，握手被 abort，进程终止",
				Evidence: []string{"svc/handler.go:88 — 收到 SIGTERM 即停止监听"},
				Approach: "先停止服务再复现",
			},
		}),
	}), nil)
	if code != 0 {
		t.Fatalf("phase-submit(context): exit %d, want 0 (stderr: %q)", code, stderr)
	}
	run, err := shared.LoadRun("run-ack")
	if err != nil {
		t.Fatal(err)
	}
	cp := *run.CurrentCheckpoint()
	block := statusView(t, `{"session_id":"`+contractSession+`","view":"checkpoint"}`)
	fullView := statusView(t, `{"session_id":"`+contractSession+`"}`)

	ask := func(id, msg string) string {
		t.Helper()
		code, out, stderr := runBinary(t, "stop", mustJSON(t, map[string]interface{}{
			"hook_event_name": "Stop", "session_id": contractSession, "stop_hook_active": id == "re-fire",
			"last_assistant_message": msg,
		}), nil)
		if code != 0 {
			t.Fatalf("stop exit = %d, want 0 (stderr: %q)", code, stderr)
		}
		return out
	}
	// The pasted checkpoint IS the display act: it binds and asks whatever
	// its conclusion says.
	if out := ask("first", block); !strings.Contains(out, `"decision":"block"`) || !strings.Contains(out, "已展示并绑定") {
		t.Fatalf("the pasted checkpoint must bind and hand back the question, got %q", out)
	}
	if run, _ = shared.LoadRun("run-ack"); run.CurrentCheckpoint() == nil || !run.CurrentCheckpoint().Displayed {
		t.Fatal("the pasted checkpoint must bind the display")
	}
	// …and nothing the framework rendered is a stop: re-paste, whole status
	// view, option list, an explanation — all get the question again.
	for name, msg := range map[string]string{
		"checkpoint re-pasted": block,
		"whole status view":    fullView,
		"option list":          "请选择 continue、skip、done 或 stop。",
		"explanation with 停止":  "排查结论：网关在升级窗口内停止转发，进程终止后没有自动重启，所以等到窗口结束后才恢复。请决定下一步。",
	} {
		if out := ask("re-fire", msg); !strings.Contains(out, `"decision":"block"`) || !strings.Contains(out, cp.Nonce) {
			t.Errorf("%s: must hand back the question, got %q", name, out)
		}
	}
	if run, _ = shared.LoadRun("run-ack"); run.Status != shared.StatusWaitUser || run.CurrentCheckpoint().Decided {
		t.Fatal("nothing may be decided by a sign-off")
	}
	// The user asked to stop and the model says so: the turn ends, state kept.
	if out := ask("stop", "Stopped as you asked. The run state was kept."); strings.Contains(out, "block") {
		t.Fatalf("an explicit stop acknowledgment must end the turn, got %q", out)
	}
	if run, _ = shared.LoadRun("run-ack"); run.Status != shared.StatusWaitUser {
		t.Fatalf("status = %s, want WAIT_USER kept", run.Status)
	}
}

// TestAdaptiveContinuationAfterHumanDecisionOverBinaries — ship review
// round 1: the user clicks `continue:` and the model signs off instead of
// going on. Over the real binaries: the decision lands, the Stop hook
// nudges once naming the phase to enter and how the phase was settled, a
// second sign-off is silent, and once the next phase is started nothing
// is nudged.
func TestAdaptiveContinuationAfterHumanDecisionOverBinaries(t *testing.T) {
	useContractEnv(t)
	seedLiveRun(t, "run-go", "fix")
	submitPhaseViaGate(t, "tu-ps-1", "context", nil)
	run, err := shared.LoadRun("run-go")
	if err != nil {
		t.Fatal(err)
	}
	cp := *run.CurrentCheckpoint()
	block := statusView(t, `{"session_id":"`+contractSession+`","view":"checkpoint"}`)
	if code, _, stderr := runBinary(t, "stop", stopRenderPayload(t, block), nil); code != 0 {
		t.Fatalf("stop exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	page := shared.DecisionPage(cp)
	code, stdout, stderr := runBinary(t, "posttooluse", mustJSON(t, map[string]interface{}{
		"hook_event_name": "PostToolUse",
		"session_id":      contractSession,
		"tool_name":       "AskUserQuestion",
		"tool_use_id":     "tu-ask-1",
		"tool_input":      askQuestionInput(page, cp.Nonce),
		"tool_response":   map[string]interface{}{"answers": map[string]interface{}{askQuestionText(cp.Nonce): page[0]}},
	}), nil)
	if code != 0 || strings.Contains(stdout, "block") {
		t.Fatalf("the CONTINUE must land silently: (code, stdout, stderr) = (%d, %q, %q)", code, stdout, stderr)
	}
	if run, _ = shared.LoadRun("run-go"); run.Status != shared.StatusActive {
		t.Fatalf("status after CONTINUE = %s, want ACTIVE", run.Status)
	}

	stop := func(id, msg string) string {
		t.Helper()
		code, out, stderr := runBinary(t, "stop", mustJSON(t, map[string]interface{}{
			"hook_event_name": "Stop", "session_id": contractSession, "stop_hook_active": id == "again",
			"last_assistant_message": msg,
		}), nil)
		if code != 0 {
			t.Fatalf("stop exit = %d, want 0 (stderr: %q)", code, stderr)
		}
		return out
	}
	out := stop("first", "好的，按你的选择继续下一阶段。")
	for _, want := range []string{`"decision":"block"`, "context", "已由用户选择 continue", "立即进入 diagnosis"} {
		if !strings.Contains(out, want) {
			t.Errorf("the nudge must carry %q, got %q", want, out)
		}
	}
	if out = stop("again", "好的，按你的选择继续下一阶段。"); strings.Contains(out, "block") {
		t.Fatalf("one decision nudges at most once, got %q", out)
	}

	// The next phase starts (the provider skill opens it): nothing to nudge.
	code, _, stderr = runBinary(t, "pretooluse-gate", gatePayload(t, "Skill", "tu-skill-1", map[string]interface{}{"skill": "my-debug-skill"}), nil)
	if code != 0 {
		t.Fatalf("provider skill: exit %d, want 0 (stderr: %q)", code, stderr)
	}
	if out = stop("later", "开始诊断。"); strings.Contains(out, "block") {
		t.Fatalf("with the next phase started the stop is silent, got %q", out)
	}
}

// TestAdaptiveOverCapConclusionOverBinaries — ship review round 1: the
// at-cap e2e never clipped anything, so the caps were pinned at the shared
// seam only. Over the real binaries with an over-cap conclusion: the
// checkpoint stays ≤ 42 lines, lists roll off with a counted cue, the
// summary is cut after a full sentence and ends with the full-text cue, and
// the status detail carries everything.
func TestAdaptiveOverCapConclusionOverBinaries(t *testing.T) {
	useContractEnv(t)
	seedLiveRun(t, "run-wide", "fix")
	submitPhaseViaGate(t, "tu-ps-1", "context", nil)
	run, err := shared.LoadRun("run-wide")
	if err != nil {
		t.Fatal(err)
	}
	renderAndDecide(t, "run-wide", "tu-ask-1", run.CurrentCheckpoint(), 0,
		func(labels []string) string { return firstLabelWithKeyword(labels, "continue") })

	sentence := "竞技场战斗在结算阶段返回错误的根因是会话结算时读取了空的胜者字段。"
	evidence := make([]string, 9)
	for i := range evidence {
		evidence[i] = fmt.Sprintf("arena/battle_session.go:%d — 第 %d 条证据", 400+i, i+1)
	}
	code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Skill", "tu-ps-2", map[string]interface{}{
		"skill": "summoner:phase-submit",
		"args": mustJSON(t, shared.PhaseSubmission{
			PhaseID: "diagnosis", Summary: "nil deref in the task loader",
			Outcome: shared.ConclusionUnclear,
			Conclusion: &shared.Conclusion{
				Summary:       strings.Repeat(sentence, 8),
				Evidence:      evidence,
				Approach:      "从报错栈回溯调用链",
				OpenQuestions: []string{"forfeit 队伍的默认胜者应取剩余队伍还是判为平局？", "跨服模式是否还有其他依赖 winner 非空的结算路径？"},
			},
		}),
	}), nil)
	if code != 0 {
		t.Fatalf("phase-submit(diagnosis): exit %d, want 0 (stderr: %q)", code, stderr)
	}
	view := statusView(t, `{"session_id":"`+contractSession+`","view":"checkpoint"}`)
	lines := strings.Split(strings.TrimSuffix(view, "\n"), "\n")
	if len(lines) > 42 {
		t.Fatalf("an over-cap diagnosis renders %d lines, over the cap 42:\n%s", len(lines), view)
	}
	for _, want := range []string{"…另 5 条，见 summoner-status", "…（全文见 summoner-status）", "第 4 条证据"} {
		if !strings.Contains(view, want) {
			t.Errorf("the checkpoint lost %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "第 5 条证据") {
		t.Errorf("only the first 4 evidence items belong in the checkpoint:\n%s", view)
	}
	// The cut kept whole sentences and stays inside the cap.
	for _, ln := range lines {
		if !strings.Contains(ln, "…（全文见 summoner-status）") || !strings.Contains(ln, sentence) {
			continue
		}
		body := strings.TrimSuffix(strings.TrimPrefix(ln, "║   "), "…（全文见 summoner-status）")
		if !strings.HasSuffix(body, "。") || utf8.RuneCountInString(strings.TrimPrefix(ln, "║   ")) > 200 {
			t.Errorf("the summary was cut mid-sentence or over the cap: %q", ln)
		}
	}
	// The status detail carries the whole conclusion.
	full := statusView(t, `{"session_id":"`+contractSession+`"}`)
	for _, want := range []string{"evidence (9):", "第 9 条证据", strings.Repeat(sentence, 8)} {
		if !strings.Contains(full, want) {
			t.Errorf("the status detail lost %q", want)
		}
	}
}
