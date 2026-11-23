package shared

// decisiontext.go — the model-facing text of the DECISION channel, in one
// place. PostToolUse prints it (the rejection block); the Stop hook's stop
// acknowledgment must IGNORE it (stopack.go). Two hooks judging the same
// words need the same source: a reason the framework wrote is never a
// model-authored stop acknowledgment.

import (
	"fmt"
	"strings"
)

// RejectionHeader opens the rejection block of a decision attempt.
func RejectionHeader(cp CheckpointState) string {
	return fmt.Sprintf("Summoner：本次裁决未记录（checkpoint %s，nonce %s）。", cp.ID, cp.Nonce)
}

// RejectionCausePrefix introduces the recorder's own reason line.
const RejectionCausePrefix = "原因："

// RejectionDisclosure states what the SAME callback already changed before
// its decision was rejected — including the absence of any change: a
// settled extend must not be asked again (extensions dedupe by
// tool_use_id, so a re-ask extends twice), and a recorded drop is not
// rolled back.
func RejectionDisclosure(drops []string, extended *BudgetLimits) string {
	var b strings.Builder
	b.WriteString("本次回调已生效的部分（裁决本身未记录，run 仍停在该检查点等待裁决）：\n")
	if len(drops) == 0 && extended == nil {
		b.WriteString("  - 无：未记录任何 drop，未追加预算。\n")
		return b.String()
	}
	if len(drops) == 0 {
		b.WriteString("  - 未记录任何 drop。\n")
	} else {
		fmt.Fprintf(&b, "  - 已记录 drop：%s（已生效；重问时不必再带这些 drop 题）。\n", strings.Join(drops, ", "))
	}
	if extended == nil {
		b.WriteString("  - 未追加预算。\n")
	} else {
		fmt.Fprintf(&b, "  - 已追加预算：%s（已生效；重问时不要再带追加题——预算追加按 tool_use_id 去重，重问会再追加一次）。\n", setAxesKey(*extended))
	}
	return b.String()
}

// setAxesKey spells the SET axes of a budget value the way a reader counts
// them.
func setAxesKey(l BudgetLimits) string {
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

// RejectionReason assembles the whole rejection block: header, the
// recorder's reason, the disclosure, and the legal next steps.
func RejectionReason(cp CheckpointState, problem string, drops []string, extended *BudgetLimits) string {
	return RejectionHeader(cp) + "\n" + RejectionCausePrefix + problem + "\n" +
		RejectionDisclosure(drops, extended) + DecisionNextSteps(cp)
}

// DecisionNextSteps is the legal path at a pending checkpoint, for EVERY
// rejection. The model cannot tell a mistyped option from typed feedback —
// in a real dialog the user's words often arrive as the question's answer
// value — so the text covers both, each under its own condition, and names
// no move the checkpoint lacks (a kind without recall is not told to
// recall). While the run waits, the phase is closed and writes are denied:
// read-only investigation, the question itself, and the user's recall /
// change / stop are the only moves there are.
func DecisionNextSteps(cp CheckpointState) string {
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
	b.WriteString("  C. 裁决被拒且原因为“content changed since display”（检查点内容在展示后变了）：重新粘贴当前规范消息并结束回合——结束回合的钩子会重新绑定展示，再发起裁决。\n")
	return b.String()
}

// MappedContinueHeader opens the block that tells the model a "continue"
// answer took effect as a recall.
const MappedContinueHeader = "Summoner：裁决已记录——"

// MappedContinueReason explains a "continue" that re-entered a phase: the
// route had nothing left to continue into. Model-facing (PostToolUse block);
// the Stop hook strips it as framework text by its header.
func MappedContinueReason(rec RecallRecord, reopenedVisit string) string {
	return MappedContinueHeader + "你在“继续”上的选择已生效。本检查点之后路线上没有未完成的阶段可继续，" +
		"所以已回到最近已验收的开发阶段 " + rec.TargetPhaseID + "（recall 自 " + rec.TargetVisitID +
		"；当前为 " + reopenedVisit + "，已重新开启）。" +
		"立即在这个阶段继续工作，完成后用 summoner:phase-submit 重新提交；提交后会得到新的检查点供用户裁决。"
}

// questionRecipe spells the decision question exactly as the gate admits
// it: the nonce and page 1's labels verbatim.
func questionRecipe(cp CheckpointState) string {
	page := DecisionPage(cp)
	var b strings.Builder
	fmt.Fprintf(&b, "题干带 nonce %s，选项 = 第 1 页 %d 个标签逐字：", cp.Nonce, len(page))
	for _, label := range page {
		fmt.Fprintf(&b, "\n       - %s", label)
	}
	if len(cp.MenuPages) > 1 {
		b.WriteString("\n     （more:/back: 只是翻页反馈：用目标页的标签原样重问，不记录任何东西。）\n")
	} else {
		b.WriteString("\n     （选项已全部列出：本题没有翻页。）\n")
	}
	return b.String()
}

// offersRecall reports whether any persisted page of the checkpoint carries
// a recall action.
func offersRecall(cp CheckpointState) bool {
	for _, pg := range cp.MenuPages {
		for _, l := range pg.Labels {
			if menuKeyword(l) == "recall" {
				return true
			}
		}
	}
	return false
}
