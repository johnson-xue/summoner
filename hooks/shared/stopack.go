package shared

// stopack.go — the Stop hook's stop-acknowledgment test (ruling 3: stopping
// must always be possible; §1.6 停止不对称).
//
// The test is a keyword match, and a keyword match is only as good as the
// text it runs over. Two things went wrong with running it over the whole
// final message:
//
//   - FRAMEWORK text matched. Every decision menu carries a "stop: …" label,
//     and 0.7.0 put more framework words into what the model pastes — the
//     status detail's instruction line named "the Stop hook", and the
//     checkpoint's own 结论 carries free text in which 停止 / 终止 / abort
//     are ordinary diagnosis vocabulary. A model that re-pasted the
//     checkpoint, or pasted the status view, was read as acknowledging a
//     stop: the turn ended and the dialog never came — the very stall 0.7.0
//     exists to remove.
//   - OPTION TALK matched. "请选择 continue、skip、done 或 stop" is a list of
//     options, not a stop.
//
// So the test runs over the model's OWN words only (every framework-rendered
// line is removed first) and comes in two strengths:
//
//   - An explicit acknowledgment of the USER'S REQUEST — a stop word tied to
//     that request in the same clause ("已按要求停止", "Stopped as you
//     asked") — is honored everywhere.
//   - A bare stop is honored only in the lenient strength (a checkpoint not
//     yet displayed: nothing is owed to the user yet, and stopping must
//     stay possible), and only as a short statement that opens or closes
//     with the stop word ("好的，已停止。", "Halting now."): a stop word with
//     an object after it, buried mid-sentence, or inside a long explanation
//     is something being described, not a stop.
//
// At a displayed checkpoint whose question was never asked the strict
// strength applies. A miss there costs one click — the hook blocks, the
// model raises the question, and the user picks `stop:` — and a recorded
// STOP decision is the better ending anyway. A false hit costs the stall.
//
// Known residual: model-authored prose that ties a stop word to the user's
// request without meaning the run ("按你的要求停止了改动…") is read as an
// acknowledgment.

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	// stopWordRe is the documented stop/halt/abort marker list (English
	// inflections or Chinese 停止/终止).
	stopWordRe = regexp.MustCompile(`(?i)\b(?:stop(?:ped|ping)?|halt(?:ed|ing)?|abort(?:ed|ing)?)\b|停止|终止`)

	// stopRequestRe ties a stop word to the USER'S request in the same
	// clause, in either order, in Chinese or English. The request marker
	// must sit right next to the stop word ("按要求排查…服务停止" is
	// content: too many words in between).
	stopRequestRe = regexp.MustCompile(`(?i)(?:按要求|按你的要求|按您的要求|按你要求|按您要求|按你的指示|按您的指示|按你指示|按您指示|根据你的(?:要求|指示)|根据您的(?:要求|指示)|应你要求|应您要求|(?:你|您|用户)(?:要求|让|叫|希望|要)(?:我|我们)?)[，,、\s]*(?:我|我们|已|已经|现|现在|将|把)?[^。，,.！!？?；;：:\n]{0,4}(?:停止|终止|中止|结束|停下)` +
		`|\b(?:as|per|at)\s+(?:you\s+)?(?:asked|requested|instructed|your\s+(?:request|instruction|direction))\b[^.!?\n]{0,30}\b(?:stop|halt|abort)` +
		`|\b(?:stop|halt|abort)(?:ped|ping|ed|ing)?\s+(?:now\s+)?(?:as|per)\s+(?:you\s+)?(?:asked|requested|instructed|wanted|your\s+(?:request|instruction))` +
		`|\byou\s+(?:asked|requested|told|instructed|wanted)\s+me\s+to\s+(?:stop|halt|abort)`)

	// optionTalkRe names the other decisions: a message that lists options
	// is describing the menu, not stopping.
	optionTalkRe = regexp.MustCompile(`(?i)\b(?:continue|skip|done|recall|review)\b|继续|跳过|回城|审查|裁决|决定`)

	// rejectionLinePrefixes open the lines of the rejection block whose tail
	// varies (the recorder's reason, the applied effects).
	rejectionLinePrefixes = []string{
		"Summoner：本次裁决未记录", RejectionCausePrefix, "本次回调已生效的部分",
		"- 无：未记录任何 drop", "- 已记录 drop：", "- 已追加预算：", "- 未记录任何 drop。", "- 未追加预算。",
	}

	// stopAckBrevity bounds a bare-stop acknowledgment in the lenient
	// strength (runes of the model's own words).
	stopAckBrevity = 60
)

// FrameworkDecisionLines returns every line the framework itself may have
// put into an assistant message for this checkpoint: the canonical message
// of EVERY checkpoint of the run, current or not (a model that re-pastes a
// superseded or stale block by mistake is still pasting framework text),
// the current status detail block, the rejection block's fixed parts and
// the decision next steps. extra carries the lines a caller hook adds on
// its own (its block reasons). Lines are trimmed.
func FrameworkDecisionLines(run *RunState, cp *CheckpointState, extra ...string) []string {
	if cp == nil {
		return trimLines(extra)
	}
	texts := []string{
		RejectionHeader(*cp),
		RejectionDisclosure(nil, nil),
		DecisionNextSteps(*cp),
	}
	// The given checkpoint first — the caller's "current" one is stripped
	// whether or not the run's list already holds it.
	if run != nil {
		texts = append(texts, RenderCheckpointMessage(run, *cp), DisplayBlock(run, *cp))
	}
	if run != nil {
		for i := range run.Checkpoints {
			texts = append(texts, RenderCheckpointMessage(run, run.Checkpoints[i]), DisplayBlock(run, run.Checkpoints[i]))
		}
	}
	texts = append(texts, CheckpointDetail(run, *cp))
	texts = append(texts, extra...)
	for _, drops := range [][]string{nil, {"x"}} {
		texts = append(texts, RejectionDisclosure(drops, &BudgetLimits{RunRework: 1}))
	}
	return trimLines(texts)
}

func trimLines(texts []string) []string {
	var out []string
	for _, text := range texts {
		for _, ln := range strings.Split(text, "\n") {
			if t := strings.TrimSpace(ln); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}

// IsStopAcknowledgment reports whether the model's OWN words end the run on
// the user's request. The framework's lines (and the menu labels) are
// removed from message first. strict selects the strength (see the file
// comment). cp may be nil (no checkpoint to strip for).
func IsStopAcknowledgment(run *RunState, cp *CheckpointState, message string, strict bool, extraFramework ...string) bool {
	authored, pasted := authoredText(run, cp, message, extraFramework...)
	if !stopWordRe.MatchString(authored) {
		return false
	}
	if optionTalkRe.MatchString(authored) {
		return false // a message that lists options is describing the menu
	}
	if stopRequestRe.MatchString(authored) {
		return true // an explicit acknowledgment of the user's request
	}
	if strict || pasted >= pasteThreshold {
		// Strict: the question is owed, a false hit is the stall. A PASTE
		// (mostly framework text): whatever stop words survive in its
		// leftovers — a stale conclusion line, say — belong to the pasted
		// content, not to the model's intent.
		return false
	}
	// A bare stop is a short statement that BEGINS or ENDS with the stop
	// word ("已停止。", "Halting now."): a stop word buried mid-sentence
	// ("…服务在升级时停止响应的原因是…") is something being described.
	authored = strings.TrimSpace(authored)
	if utf8.RuneCountInString(authored) > stopAckBrevity {
		return false
	}
	return stopWordAtEdge(authored)
}

// stopWordAtEdge reports a bare stop: the stop word opens the statement (a
// short lead-in like "好的，" or "I will" may precede it) or closes it (a
// short English tail like "here." may follow). A stop word with an object
// after it ("…停止响应。") or buried mid-sentence is something being
// described, not a stop.
func stopWordAtEdge(s string) bool {
	for _, loc := range stopWordRe.FindAllStringIndex(s, -1) {
		if leadIn(s[:loc[0]]) || tail(s[loc[1]:]) {
			return true
		}
	}
	return false
}

func leadIn(before string) bool {
	if utf8.RuneCountInString(before) <= 6 {
		return true
	}
	return isASCII(before) && len(strings.Fields(before)) <= 2
}

func tail(after string) bool {
	if utf8.RuneCountInString(after) <= 1 {
		return true
	}
	return isASCII(after) && len(strings.Fields(after)) <= 2 && utf8.RuneCountInString(after) <= 8
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// pasteThreshold is the number of framework lines whose removal marks a
// message as a paste of framework text rather than the model's own words.
const pasteThreshold = 3

// AuthoredText is message minus everything the framework rendered for this
// checkpoint: whole lines equal to a framework line (also behind a
// blockquote or fence prefix), the rejection block's variable lines, and
// the menu labels wherever they occur.
func AuthoredText(run *RunState, cp *CheckpointState, message string, extraFramework ...string) string {
	authored, _ := authoredText(run, cp, message, extraFramework...)
	return authored
}

// authoredText is AuthoredText plus the number of framework lines it removed.
func authoredText(run *RunState, cp *CheckpointState, message string, extraFramework ...string) (string, int) {
	framework := make(map[string]bool)
	for _, ln := range FrameworkDecisionLines(run, cp, extraFramework...) {
		framework[ln] = true
	}
	var keep []string
	pasted := 0
	for _, ln := range strings.Split(message, "\n") {
		t := strings.TrimSpace(strings.TrimLeft(ln, "> \t`"))
		if t == "" {
			continue
		}
		if framework[t] || hasAnyPrefix(t, rejectionLinePrefixes) {
			pasted++
			continue
		}
		if cp != nil {
			for _, label := range DecisionMenuLabels(cp.Kind) {
				t = strings.ReplaceAll(t, label, "")
			}
		}
		if strings.TrimSpace(t) != "" {
			keep = append(keep, t)
		}
	}
	return strings.Join(keep, "\n"), pasted
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
