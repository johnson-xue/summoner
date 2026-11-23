package shared

import (
	"strings"
	"testing"
)

// stopack_test.go — the Stop hook's stop-acknowledgment test, at the seam
// where it can be judged against the REAL renderers. The ship review of
// 0.7.0 found the framework's own words ending the turn: a checkpoint whose
// conclusion said 停止, re-pasted, read as the model acknowledging a stop.

// stopWordState is an in-memory run at a diagnosis checkpoint whose
// conclusion is full of stop words — ordinary diagnosis vocabulary.
func stopWordState() (*RunState, CheckpointState) {
	run := &RunState{RunID: "run-1", Workflow: "fix", RequirementsVersion: 1}
	cp := CheckpointState{
		ID: "cp-2", Kind: CheckpointPhase, PhaseID: "diagnosis", Nonce: "1a2b3c4d",
		MenuPages: MenuProjection(CheckpointPhase),
		Outcome:   ConclusionUnclear,
		Conclusion: &Conclusion{
			Summary:  "服务在收到 SIGTERM 后停止响应，握手被 abort，进程终止",
			Evidence: []string{"svc/handler.go:88 — 收到 SIGTERM 即停止监听"},
			Approach: "先停止服务再复现",
		},
	}
	return run, cp
}

func blockquote(s string) string {
	return "> " + strings.ReplaceAll(strings.TrimSuffix(s, "\n"), "\n", "\n> ") + "\n"
}

// TestStopAcknowledgmentTable pins both strengths over text built from the
// real renderers.
func TestStopAcknowledgmentTable(t *testing.T) {
	run, cp := stopWordState()
	block := RenderCheckpointMessage(run, cp)
	detail := CheckpointDetail(run, cp)
	statusView := "⚡ SUMMONER STATUS — run run-1 · workflow fix · WAIT_USER\nawaiting: user decision at checkpoint cp-2\n" + detail + "\n" + block
	rejection := RejectionReason(cp, `answer "请停止" is not an offered option label of the decision question`, nil, nil)

	cases := []struct {
		name    string
		msg     string
		lenient bool // at a checkpoint not yet displayed
		strict  bool // displayed, question owed
	}{
		// --- the framework's own words are never a stop (the review finding)
		{"the checkpoint", block, false, false},
		{"the checkpoint in a blockquote", blockquote(block), false, false},
		{"the checkpoint without its tail", DisplayBlock(run, cp), false, false},
		{"the status detail", detail, false, false},
		{"the status view", statusView, false, false},
		{"the status view in a blockquote", blockquote(statusView), false, false},
		{"the model instruction line", DecisionAskInstruction(cp), false, false},
		{"sign-off + instruction line", "先到这里。\n" + DecisionAskInstruction(cp), false, false},
		{"the rejection block", rejection, false, false},
		{"the decision next steps", DecisionNextSteps(cp), false, false},
		{"explanation + checkpoint", "根因见下方。\n\n" + block, false, false},

		// --- listing the options is describing the menu
		{"option list", "请选择 continue、skip、done 或 stop。", false, false},
		{"option list, english", "You can continue, skip, done, recall, review or stop.", false, false},

		// --- content that happens to contain a stop word
		{"long explanation", "排查结论：网关在升级窗口内停止转发，进程终止后没有自动重启；上游因此超时，表现为整场战斗卡在结算，等到升级窗口结束后才自行恢复。建议先看网关的升级日志。", false, false},
		{"request marker, stop not adjacent", "按你的要求排查后确认：服务在升级时停止响应的原因是配置缺失。", false, false},
		{"service description", "服务在收到 SIGTERM 后停止响应。", false, false},
		{"stop word mid-sentence", "握手在这个过程中被 abort 了两次。", false, false},
		{"upload aborted", "The upload was aborted by the proxy.", false, false},

		// --- the user asked to stop and the model says so
		{"stopped as you asked", "Stopped as you asked.", true, true},
		{"aborted per your request", "Stopped. The run is aborted per your request.", true, true},
		{"按要求停止", "好的，已按要求停止运行。状态已保留，之后可以用 /summoner:status 恢复。", true, true},
		{"用户要求终止", "用户要求终止，已停止。", true, true},
		{"按您的要求停止了", "已按您的要求停止了本次工作流。运行状态已保留，之后可以用 /summoner:status 恢复，所以不需要额外操作。", true, true},
		{"stopping as requested", "Stopping here as requested — run halted.", true, true},

		// --- a bare stop: only before anything is displayed
		{"bare stop", "好的，已停止。", true, false},
		{"halting now", "Halting the workflow now.", true, false},
		{"i will stop here", "I will stop here.", true, false},
		{"stopping for now", "Stopping for now.", true, false},

		{"bare stop, strict", "已停止。", true, false},
	}
	for _, c := range cases {
		if got := IsStopAcknowledgment(run, &cp, c.msg, false); got != c.lenient {
			t.Errorf("%s: lenient = %v, want %v", c.name, got, c.lenient)
		}
		if got := IsStopAcknowledgment(run, &cp, c.msg, true); got != c.strict {
			t.Errorf("%s: strict = %v, want %v", c.name, got, c.strict)
		}
	}
}

// TestStopAcknowledgmentIgnoresTheHooksOwnReasons: a model that echoes the
// Stop hook's block (it lists the `stop:` label) is not stopping.
func TestStopAcknowledgmentIgnoresTheHooksOwnReasons(t *testing.T) {
	run, cp := stopWordState()
	ask := "检查点 cp-2（nonce 1a2b3c4d）已展示并绑定。现在用 AskUserQuestion 发起裁决——题干带 nonce 1a2b3c4d，选项 = 第 1 页 4 个标签逐字：\n" +
		strings.Join(DecisionPage(cp), "\n")
	for _, strict := range []bool{false, true} {
		if IsStopAcknowledgment(run, &cp, "照办：\n"+ask, strict, ask) {
			t.Errorf("strict=%v: an echoed block reason must not read as a stop", strict)
		}
	}
}

// TestAuthoredTextLeavesOnlyTheModelsWords pins the strip itself.
func TestAuthoredTextLeavesOnlyTheModelsWords(t *testing.T) {
	run, cp := stopWordState()
	msg := "根因定位在结算逻辑，证据如下。\n\n" + blockquote(RenderCheckpointMessage(run, cp)) + "\n结论先看第一条证据。"
	got := AuthoredText(run, &cp, msg)
	want := "根因定位在结算逻辑，证据如下。\n结论先看第一条证据。"
	if got != want {
		t.Fatalf("AuthoredText = %q, want %q", got, want)
	}
	if IsStopAcknowledgment(run, &cp, msg, true) {
		t.Fatal("an explanation with a pasted checkpoint is not a stop")
	}
}

// TestStopAcknowledgmentWithoutACheckpoint: with no checkpoint to strip for
// (an ACTIVE run) the plain keyword test applies to the whole message.
func TestStopAcknowledgmentWithoutACheckpoint(t *testing.T) {
	run := &RunState{RunID: "run-1", Workflow: "fix"}
	if IsStopAcknowledgment(run, nil, "好的，按你的选择继续下一阶段。", false) {
		t.Fatal("no stop word, no stop")
	}
	if !IsStopAcknowledgment(run, nil, "I will stop here.", false) {
		t.Fatal("a short bare stop is honored when nothing is owed")
	}
	if !IsStopAcknowledgment(run, nil, "Stopped as you asked.", true) {
		t.Fatal("an explicit acknowledgment is honored in both strengths")
	}
}
