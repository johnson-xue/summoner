package main

// rejection.go — the VISIBLE rejection channel of the decision recorder
// (0.7.0 D4). Before 0.7.0 a rejected decision wrote its trace event and
// exited 0 with the reason on stderr — and a zero-exit hook's stderr never
// reaches the model. So a user who typed text instead of clicking an option
// got the same question again: the model saw a dialog result, nothing else
// (run df0ed2e6 asked one question three times over a typed correction).
//
// A rejection is now answered on stdout with {"decision":"block",
// "reason":…} — the platform shows the reason beside the tool result. The
// WHOLE stdout is that one object (a PostToolUse hook's stdout is parsed
// whole), and the reason carries three things the model cannot do without:
//
//   - what was wrong, in the platform's own terms (the nonce, the exact
//     labels — never a paraphrase);
//   - what the SAME callback already changed (a recorded drop, an applied
//     extension): those effects are not rolled back, and a settled extend
//     must not be asked again — extensions dedupe by tool_use_id, so a
//     re-ask extends twice;
//   - the next step, which must be a LEGAL path at this checkpoint: while
//     the run waits, writes are denied and the phase is closed — the model
//     can only investigate read-only, re-ask, or route the user to recall /
//     a requirement change / stop.
//
// The text itself still advances nothing: only a platform answer records a
// decision.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// appliedEffects is what one callback already changed before its decision
// was rejected. Ride-along effects land BEFORE the decision by design (a
// drop must precede the CONTINUE that may clear it; an extension rides the
// same commit only when the decision completes), so a rejection can arrive
// with state already moved.
type appliedEffects struct {
	drops    []string             // criterion ids recorded in this callback
	extended *shared.BudgetLimits // the delta of an extension applied in this callback
}

// emitRejection writes the block decision of a rejected decision attempt.
// problem is the recorder's own reason; next is the legal path at this
// checkpoint; applied is what the callback already changed.
func emitRejection(outW io.Writer, cp *shared.CheckpointState, problem string, applied *appliedEffects) {
	if outW == nil {
		return
	}
	var drops []string
	var extended *shared.BudgetLimits
	if applied != nil {
		drops, extended = applied.drops, applied.extended
	}
	// The text lives in shared (decisiontext.go): the Stop hook must ignore
	// these same words when it judges a stop acknowledgment (stopack.go).
	reason := shared.RejectionReason(*cp, problem, drops, extended)
	if err := json.NewEncoder(outW).Encode(map[string]string{"decision": "block", "reason": reason}); err != nil {
		fmt.Fprintf(os.Stderr, "summoner posttooluse: writing the rejection block failed: %v\n", err)
	}
}

// navigationDirection reads the leading keyword of a `more:` / `back:`
// answer: +1 for the next page, -1 for the previous one.
func navigationDirection(label string) (int, bool) {
	switch kw, _ := splitLabelKeyword(label); kw {
	case "more":
		return +1, true
	case "back":
		return -1, true
	}
	return 0, false
}

// emitNavigation answers paging feedback that arrived as the decision
// answer: the user typed `more:` / `back:` into the dialog. Navigation
// records nothing (references/checkpoint-protocol.md) — so it is not a
// rejection either — and the block hands back the question for the TARGET
// page, spelled out exactly as the gate admits it.
func emitNavigation(outW io.Writer, cp *shared.CheckpointState, q *decisionQuestion, dir int, answer string) {
	if outW == nil {
		return
	}
	offered := make([]string, 0, len(q.options))
	for _, raw := range q.options {
		om, _ := raw.(map[string]interface{})
		if label, _ := om[fieldLabel].(string); label != "" {
			offered = append(offered, label)
		}
	}
	cur := -1
	for i := range cp.MenuPages {
		if cp.MenuPages[i].Matches(offered) {
			cur = i
		}
	}
	if cur < 0 {
		cur = 0
	}
	target, note := cur+dir, ""
	switch {
	case target >= len(cp.MenuPages):
		target, note = len(cp.MenuPages)-1, "已是最后一页。"
	case target < 0:
		target, note = 0, "已是第一页。"
	}
	labels := cp.MenuPages[target].Labels
	var b strings.Builder
	fmt.Fprintf(&b, "Summoner：收到翻页反馈（%s）——不记录任何东西，同一回调里附带的其他答案也未生效。%s用第 %d/%d 页的标签原样重发这道裁决：题干带 nonce %s，选项 = 该页 %d 个标签逐字：\n",
		truncate(answer, 40), note, target+1, len(cp.MenuPages), cp.Nonce, len(labels))
	for _, label := range labels {
		fmt.Fprintf(&b, "  - %s\n", label)
	}
	b.WriteString("（翻页反馈不是裁决：裁决只能是对某一页标签的选择。）\n")
	if cp.ExtendAsk != nil {
		// The extend ride must be re-attached on the page the answer rides
		// (ship review round 2, interaction F14).
		fmt.Fprintf(&b, "预算已耗尽：裁决时同一调用须附带追加题，选项恰为：%s / %s。\n", cp.ExtendAsk.ExtendLabel, cp.ExtendAsk.HoldLabel)
	}
	if err := json.NewEncoder(outW).Encode(map[string]string{"decision": "block", "reason": b.String()}); err != nil {
		fmt.Fprintf(os.Stderr, "summoner posttooluse: writing the navigation block failed: %v\n", err)
	}
}
