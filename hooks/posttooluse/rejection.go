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

// disclosure states the applied effects plainly, including the absence of
// any — the model must be able to tell "nothing happened" from "something
// did".
func (a *appliedEffects) disclosure() string {
	var b strings.Builder
	b.WriteString("本次回调已生效的部分（裁决本身未记录，run 仍停在该检查点等待裁决）：\n")
	if len(a.drops) == 0 && a.extended == nil {
		b.WriteString("  - 无：未记录任何 drop，未追加预算。\n")
		return b.String()
	}
	if len(a.drops) == 0 {
		b.WriteString("  - 未记录任何 drop。\n")
	} else {
		fmt.Fprintf(&b, "  - 已记录 drop：%s（已生效；重问时不必再带这些 drop 题）。\n", strings.Join(a.drops, ", "))
	}
	if a.extended == nil {
		b.WriteString("  - 未追加预算。\n")
	} else {
		fmt.Fprintf(&b, "  - 已追加预算：%s（已生效；重问时不要再带追加题——预算追加按 tool_use_id 去重，重问会再追加一次）。\n", budgetLimitsKey(*a.extended))
	}
	return b.String()
}

// budgetLimitsKey spells the SET axes of a delta the way a reader counts
// them.
func budgetLimitsKey(l shared.BudgetLimits) string {
	var axes []string
	if l.PhaseToolCalls > 0 {
		axes = append(axes, fmt.Sprintf("phase_tool_calls +%d", l.PhaseToolCalls))
	}
	if l.FailedChecks > 0 {
		axes = append(axes, fmt.Sprintf("failed_checks +%d", l.FailedChecks))
	}
	if l.RunRework > 0 {
		axes = append(axes, fmt.Sprintf("run_rework +%d", l.RunRework))
	}
	if l.Delegations > 0 {
		axes = append(axes, fmt.Sprintf("delegations +%d", l.Delegations))
	}
	if len(axes) == 0 {
		return "（无）"
	}
	return strings.Join(axes, ", ")
}

// emitRejection writes the block decision of a rejected decision attempt.
// problem is the recorder's own reason; next is the legal path at this
// checkpoint; applied is what the callback already changed.
func emitRejection(outW io.Writer, cp *shared.CheckpointState, problem string, applied *appliedEffects) {
	if outW == nil {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Summoner：本次裁决未记录（checkpoint %s，nonce %s）。\n原因：%s\n", cp.ID, cp.Nonce, problem)
	if applied != nil {
		b.WriteString(applied.disclosure())
	}
	b.WriteString(nextSteps(cp))
	if err := json.NewEncoder(outW).Encode(map[string]string{"decision": "block", "reason": b.String()}); err != nil {
		fmt.Fprintf(os.Stderr, "summoner posttooluse: writing the rejection block failed: %v\n", err)
	}
}

// nextSteps is the legal path at a pending checkpoint, for EVERY rejection.
// The model cannot tell a mistyped option from typed feedback — in a real
// dialog the user's words often arrive as the question's answer value — so
// the text covers both, each under its own condition, and names no move the
// checkpoint lacks (a kind without recall is not told to recall). While the
// run waits, the phase is closed and writes are denied: read-only
// investigation, the question itself, and the user's recall / change / stop
// are the only moves there are.
func nextSteps(cp *shared.CheckpointState) string {
	var b strings.Builder
	b.WriteString("下一步（等待裁决期间写操作被拒、阶段已关闭——以下才是本检查点上的合法路径）：\n")
	b.WriteString("  A. 选项不对：按原样重新发起这道裁决。" + questionRecipe(cp))
	b.WriteString("  B. 用户输入的是文字：文字不推进 run，按内容处理——\n")
	b.WriteString("     ① 它是一份要求：先做它要求的只读调查（Read / Grep / LSP / 只读 Bash），把结论写给用户，再重新发起裁决；\n")
	if offersRecall(cp) {
		b.WriteString("     ② 方向错了：请用户选 recall:——回到对应的历史阶段重做，阶段重做后重新提交；\n")
	}
	b.WriteString("     ③ 需求本身变了：用 Skill(summoner:change)（open → apply → accept: 确认）调整基线；\n")
	if !offersRecall(cp) {
		b.WriteString("     ④ 以上都不适用时，只能由用户选 stop:（保留状态退出）；\n")
	}
	b.WriteString("  C. summoner-status 提示检查点内容已变（content changed since display）：先重新粘贴规范消息，再发起裁决。\n")
	return b.String()
}

// questionRecipe spells the decision question exactly as the gate admits
// it: the nonce and page 1's labels verbatim.
func questionRecipe(cp *shared.CheckpointState) string {
	page := shared.DecisionPage(*cp)
	var b strings.Builder
	fmt.Fprintf(&b, "题干带 nonce %s，选项 = 第 1 页 %d 个标签逐字：", cp.Nonce, len(page))
	for _, label := range page {
		fmt.Fprintf(&b, "\n       - %s", label)
	}
	b.WriteString("\n     （more:/back: 只是翻页反馈：用目标页的标签原样重问，不记录任何东西。）\n")
	return b.String()
}

// offersRecall reports whether any persisted page of the checkpoint carries
// a recall action.
func offersRecall(cp *shared.CheckpointState) bool {
	for _, pg := range cp.MenuPages {
		for _, l := range pg.Labels {
			if labelKeyword(l) == "recall" {
				return true
			}
		}
	}
	return false
}
