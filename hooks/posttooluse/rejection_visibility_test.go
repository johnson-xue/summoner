package main

// rejection_visibility_test.go — 0.7.0 D4: a rejected decision is VISIBLE to
// the model. Before 0.7.0 a rejection wrote its trace event and exited 0
// with the reason on stderr — which the model never sees for a zero-exit
// hook. So when the user typed text instead of clicking an option, the model
// saw a plain dialog result and re-asked the same question (run df0ed2e6:
// the same question three times over a typed correction). The hook now
// answers with {"decision":"block","reason":…} on stdout: the reason, what
// (if anything) already took effect in the same callback, and the legal
// next step at this checkpoint.

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// wholeStdoutObject asserts the hook's ENTIRE stdout is one JSON object (a
// PostToolUse block decision is parsed whole — anything else breaks the
// hook) and returns it.
func wholeStdoutObject(t *testing.T, stdout string) map[string]interface{} {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	var obj map[string]interface{}
	if err := dec.Decode(&obj); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\nstdout: %q", err, stdout)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("stdout carries more than one JSON value: %q", stdout)
	}
	return obj
}

func blockReason(t *testing.T, stdout string) string {
	t.Helper()
	obj := wholeStdoutObject(t, stdout)
	if obj["decision"] != "block" {
		t.Fatalf("decision = %v, want block (stdout %q)", obj["decision"], stdout)
	}
	reason, _ := obj["reason"].(string)
	if reason == "" {
		t.Fatalf("a block decision must carry a reason: %q", stdout)
	}
	return reason
}

// TestRejectedTypedAnswerIsVisibleToTheModel — acceptance criterion 7, the
// incident's case: the user typed text instead of choosing an option. The
// model gets the typed words back, the fact that nothing was recorded, the
// legal paths at this checkpoint, and the nonce to re-ask with.
func TestRejectedTypedAnswerIsVisibleToTheModel(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := openDisplayedCheckpoint(t, r, "diagnosis")
	q, qtext := cpQuestion(cp.Nonce, false, shared.DecisionMenuLabels(shared.CheckpointPhase)...)

	code, stdout, stderr := callHookOut(t, askDecision(t, "tu-ask-1", q, qtext, "",
		map[string]interface{}{"response": "先解释一下为什么会禁用英雄"}))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, stderr)
	}
	reason := blockReason(t, stdout)
	for _, want := range []string{
		cp.ID, cp.Nonce,
		"先解释一下为什么会禁用英雄", // the user's words, back to the model
		"未记录",             // the decision itself
		"只读调查",            // path 1: do what the text asks, read-only
		"recall:",         // path 2: a PHASE checkpoint offers recall
		"summoner:change", // path 3: the baseline moved
		"未追加预算",           // disclosure: nothing else took effect
		"drop",
	} {
		if !strings.Contains(reason, want) {
			t.Errorf("block reason lost %q:\n%s", want, reason)
		}
	}
	// Nothing moved: the rejection is still a rejection.
	run := reloadRun(t, "run-1")
	if run.Status != shared.StatusWaitUser || checkpointByID(t, run, cp.ID).Decided {
		t.Fatalf("a rejected decision must decide nothing: status %s", run.Status)
	}
	if n := len(eventsOfType(readHookTrace(t, "run-1"), shared.EventCheckpointDecisionRejected)); n != 1 {
		t.Fatalf("checkpoint.decision_rejected events = %d, want 1", n)
	}
}

// TestRejectedAnswerNamesTheExactQuestion: a malformed answer (not an
// offered label) comes back with the nonce and the page's exact labels —
// the model re-asks correctly instead of paraphrasing again.
func TestRejectedAnswerNamesTheExactQuestion(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := openDisplayedCheckpoint(t, r, "diagnosis")
	page := shared.DecisionPage(cp)
	q, qtext := cpQuestion(cp.Nonce, false, page...)

	code, stdout, _ := callHookOut(t, askDecision(t, "tu-ask-1", q, qtext, "Continue", nil))
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	reason := blockReason(t, stdout)
	for _, want := range append([]string{cp.Nonce, "未记录", "not an offered option label"}, page...) {
		if !strings.Contains(reason, want) {
			t.Errorf("block reason lost %q:\n%s", want, reason)
		}
	}
	// A real dialog delivers typed words as the question's ANSWER VALUE — the
	// same path as a mistyped label — so the guidance must cover both.
	for _, want := range []string{"选项不对", "用户输入的是文字", "只读调查", "recall:"} {
		if !strings.Contains(reason, want) {
			t.Errorf("block reason must cover both a mistyped option and typed feedback, lost %q:\n%s", want, reason)
		}
	}
}

// TestRejectionDisclosesWhatAlreadyTookEffect — review B2: a callback can
// apply its ride-along effects BEFORE the decision is rejected (an
// extension, a criterion drop). The block reason must say so, and must warn
// against re-asking a settled extend — extensions dedupe by tool_use_id, so
// a re-ask would extend twice.
func TestRejectionDisclosesWhatAlreadyTookEffect(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := openFirstCheckpoint(t, r)
	if code, stderr := callHook(t, askDecision(t, "tu-1", mustQuestion(t, cp.Nonce, cp.MenuPages[0].Labels), questionTextOf(cp.Nonce), cp.MenuPages[0].Labels[0], nil)); code != 0 {
		t.Fatalf("continue exit = %d (stderr %q)", code, stderr)
	}
	run := reloadHookRun(t)
	if err := run.OpenPhase("diagnosis"); err != nil {
		t.Fatal(err)
	}
	run.Limits.PhaseToolCalls = 1
	run.CountToolCall("")
	cp2, err := run.SubmitPhaseUnstaged(shared.PhaseSubmission{
		PhaseID: "diagnosis", Summary: "diagnosis done",
		Outcome:     shared.ConclusionConfirmed,
		Conclusion:  &shared.Conclusion{Summary: "s", Evidence: []string{"e"}, Approach: "a"},
		CriteriaIDs: []string{"crit-drop-me"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cp2.ExtendAsk == nil {
		t.Fatal("fixture: the exhaustion checkpoint must carry the extend offer")
	}
	if err := run.MarkDisplayed(cp2.ID, shared.CheckpointContentDigest(cp2, run.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	cur := run.CurrentCheckpoint()

	extendText := "Extend the budget?"
	extendQ := map[string]interface{}{
		"question": extendText, "header": "Extend",
		"options": []interface{}{
			map[string]interface{}{"label": cur.ExtendAsk.ExtendLabel},
			map[string]interface{}{"label": cur.ExtendAsk.HoldLabel},
		},
	}
	dropText := "Drop criterion crit-drop-me?"
	dropQ := map[string]interface{}{
		"question": dropText, "header": "Drop",
		"options": []interface{}{
			map[string]interface{}{"label": "keep: crit-drop-me"},
			map[string]interface{}{"label": "drop: crit-drop-me"},
		},
	}

	// The user answers both rides and types TEXT for the decision — which a
	// real dialog delivers as the decision question's answer value.
	code, stdout, stderr := callHookOut(t, postUsePayload(t, "AskUserQuestion", "tu-ask-2",
		map[string]interface{}{"questions": []interface{}{mustQuestion(t, cur.Nonce, cur.MenuPages[0].Labels), extendQ, dropQ}},
		map[string]interface{}{
			"answers": map[string]interface{}{
				questionTextOf(cur.Nonce): "先看下另一个方向",
				extendText:                cur.ExtendAsk.ExtendLabel,
				dropText:                  "drop: crit-drop-me",
			},
		}))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, stderr)
	}
	reason := blockReason(t, stdout)
	for _, want := range []string{"已追加", "不要再带", "crit-drop-me", "未记录", "先看下另一个方向"} {
		if !strings.Contains(reason, want) {
			t.Errorf("block reason must disclose the applied effects, lost %q:\n%s", want, reason)
		}
	}
	after := reloadRun(t, "run-1")
	if len(after.BudgetExtensions) != 1 || after.Limits.PhaseToolCalls != 2 {
		t.Fatalf("fixture: the extension must have applied once, got %d receipts limits %+v", len(after.BudgetExtensions), after.Limits)
	}
	dropped := false
	for _, d := range after.CriteriaDrops {
		dropped = dropped || d.ID == "crit-drop-me"
	}
	if !dropped {
		t.Fatal("fixture: the drop must have been recorded")
	}
	if checkpointByID(t, after, cp2.ID).Decided || after.Status != shared.StatusWaitUser {
		t.Fatal("the decision itself must stay unrecorded")
	}
}

// TestRecordedDecisionEmitsNothing: the visible channel is for rejections
// only — a recorded decision leaves stdout empty (a block there would tell
// the model a decision failed when it did not).
func TestRecordedDecisionEmitsNothing(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := openDisplayedCheckpoint(t, r, "diagnosis")
	page := shared.DecisionPage(cp)
	q, qtext := cpQuestion(cp.Nonce, false, page...)

	code, stdout, stderr := callHookOut(t, askDecision(t, "tu-ask-1", q, qtext, page[0], nil))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, stderr)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("a recorded decision must write nothing to stdout, got %q", stdout)
	}
	if !checkpointByID(t, reloadRun(t, "run-1"), cp.ID).Decided {
		t.Fatal("the decision must have been recorded")
	}
}

// TestNavigationAnswerIsNotARejection (ship review round 1): `more:` /
// `back:` typed as the decision answer is paging feedback — not a failed
// decision. Nothing is recorded (no checkpoint.decision_rejected), and the
// block names the TARGET page's labels to re-ask with.
func TestNavigationAnswerIsNotARejection(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := openDisplayedCheckpoint(t, r, "diagnosis")
	if len(cp.MenuPages) < 2 {
		t.Fatalf("fixture: a PHASE checkpoint must have several pages, got %d", len(cp.MenuPages))
	}
	page1, page2 := cp.MenuPages[0].Labels, cp.MenuPages[1].Labels

	ask := func(id, answer string, labels []string) (int, string) {
		t.Helper()
		q, qtext := cpQuestion(cp.Nonce, false, labels...)
		code, stdout, stderr := callHookOut(t, askDecision(t, id, q, qtext, answer, nil))
		if code != 0 {
			t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
		}
		return code, stdout
	}
	assertNavigation := func(name, stdout string, labels []string) {
		t.Helper()
		obj := wholeStdoutObject(t, stdout)
		if obj["decision"] != "block" {
			t.Fatalf("%s: decision = %v, want block", name, obj["decision"])
		}
		reason, _ := obj["reason"].(string)
		for _, want := range append([]string{"翻页", cp.Nonce}, labels...) {
			if !strings.Contains(reason, want) {
				t.Errorf("%s: the navigation block lost %q:\n%s", name, want, reason)
			}
		}
		if strings.Contains(reason, "本次裁决未记录") {
			t.Errorf("%s: paging feedback is not a rejected decision:\n%s", name, reason)
		}
	}

	_, stdout := ask("tu-ask-1", "more: 查看其余选项", page1)
	assertNavigation("more from page 1", stdout, page2)
	_, stdout = ask("tu-ask-2", "back: 返回上一页", page2)
	assertNavigation("back from page 2", stdout, page1)

	// Already on the last page: say so and repeat it.
	last := cp.MenuPages[len(cp.MenuPages)-1].Labels
	_, stdout = ask("tu-ask-3", "more:", last)
	assertNavigation("more on the last page", stdout, last)
	if !strings.Contains(stdout, "最后一页") {
		t.Errorf("more on the last page must say so:\n%s", stdout)
	}

	// Nothing was recorded: no rejection, no decision, rides untouched.
	for _, ev := range readHookTrace(t, "run-1") {
		if ev.Type == shared.EventCheckpointDecisionRejected {
			t.Errorf("a navigation answer must not be traced as a rejected decision: %+v", ev)
		}
	}
	after := reloadRun(t, "run-1")
	if after.Status != shared.StatusWaitUser || checkpointByID(t, after, cp.ID).Decided {
		t.Fatal("nothing may be decided by a navigation answer")
	}
}

// TestRejectionKeepsTheUsersWordsWhole: a CJK answer longer than the
// truncation bound used to be cut at a BYTE boundary and reached the model
// (and the trace) as broken UTF-8 ("\xe6\x9c…").
func TestRejectionKeepsTheUsersWordsWhole(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := openDisplayedCheckpoint(t, r, "diagnosis")
	page := shared.DecisionPage(cp)
	q, qtext := cpQuestion(cp.Nonce, false, page...)
	typed := "为什么会禁用英雄，先解释，另外修复方案要覆盖跨服和本服两条路径，还要给出回归范围和上线步骤的说明，谢谢"

	code, stdout, stderr := callHookOut(t, askDecision(t, "tu-ask-1", q, qtext, typed, nil))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	reason := blockReason(t, stdout)
	if !strings.Contains(reason, typed) {
		t.Errorf("the user's words must reach the model whole, got:\n%s", reason)
	}
	if !utf8.ValidString(reason) {
		t.Error("the block reason is not valid UTF-8")
	}
	if strings.Contains(reason, `\x`) {
		t.Errorf("a byte-boundary cut leaked into the reason:\n%s", reason)
	}
	for _, ev := range readHookTrace(t, "run-1") {
		if ev.Type == shared.EventCheckpointDecisionRejected && !utf8.ValidString(fmt.Sprint(ev.Payload["reason"])) {
			t.Errorf("the trace payload is not valid UTF-8: %+v", ev)
		}
	}
}
