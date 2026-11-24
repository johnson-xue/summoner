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
//     asked", "Stopped the run per your instructions", "遵照你的要求，已
//     停止") — is honored everywhere. Request-tied forms allow an object
//     between the stop word and the request clause (the run, 旧服务).
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
	// stopWordRe is the broad marker list (English inflections or Chinese
	// 停止/终止/中止/结束/停下). It only gates the REQUEST-TIED form: 结束 /
	// 中止 / 停下 are what natural acknowledgments use ("已按你的要求结束了
	// 本次工作"), and they are also ordinary status words ("上下文收集结束。").
	// The broad set must never end a turn on its own (ship review round 3:
	// it did, and the checkpoint was never shown).
	stopWordRe = regexp.MustCompile(`(?i)\b(?:stop(?:ped|ping)?|halt(?:ed|ing)?|abort(?:ed|ing)?)\b|停止|终止|中止|结束|停下`)

	// bareStopWordRe is the narrow list a BARE stop may use: 停止 / 终止 and
	// stop / halt / abort, bounded by the edge-and-brevity rule.
	bareStopWordRe = regexp.MustCompile(`(?i)\b(?:stop(?:ped|ping)?|halt(?:ed|ing)?|abort(?:ed|ing)?)\b|停止|终止`)

	// stopRequestRe ties a stop word to the USER'S request in the same
	// clause, in either order, in Chinese or English. The request marker
	// must sit right next to the stop word ("按要求排查…服务停止" is
	// content: too many words in between).
	stopRequestRe = regexp.MustCompile(`(?i)(?:按要求|按照要求|按你的要求|按您的要求|按你要求|按您要求|按你的指示|按您的指示|按你指示|按您指示|按照你(?:的)?(?:要求|指示)|按照您(?:的)?(?:要求|指示)|遵照你(?:的)?(?:要求|指示)|遵照您(?:的)?(?:要求|指示)|依照你(?:的)?(?:要求|指示)|依照您(?:的)?(?:要求|指示)|遵从你(?:的)?(?:要求|指示)|根据你的(?:要求|指示)|根据您的(?:要求|指示)|(?:你|您|用户)(?:要求|让|叫|希望|要)(?:我|我们)?)[，,、\s]*(?:我|我们|已|已经|现|现在|将|把)?[^。，,.！!？?；;：:\n]{0,6}(?:停止|终止|中止|结束|停下)` +
		`|(?:停止|终止|中止|结束|停下)了?[^。\n]{0,8}(?:按照|按|遵照|依照|遵从|根据)(?:你|您|用户)(?:的)?(?:要求|指示)` +
		// 应 / 依 only behind a boundary: bare, they matched the 应 of 响应
		// ("服务停止响应你的要求。" — ship review round 4, closure N1).
		`|(?:^|[，,、\s：:])(?:应|依)(?:照)?(?:你|您)(?:的)?(?:要求|指示)[，,、\s]*(?:我|我们|已|已经|现|现在|将|把)?[^。，,.！!？?；;：:\n]{0,6}(?:停止|终止|中止|结束|停下)` +
		`|\b(?:as|per|at|on|upon|since)\s+(?:you\s+|your\s+|the\s+user(?:'s)?\s+)?(?:asked|requested|instructed|wanted|request|instruction|direction)[^.!?\n]{0,30}\b(?:stop|halt|abort)` +
		`|\b(?:stop|halt|abort)(?:ped|ping|ed|ing)?\b[^.!?\n]{0,24}?\b(?:as|per|at|on|upon|since|because)\s+(?:you\s+|your\s+|the\s+user(?:'s)?\s+)?(?:asked|requested|instructed|wanted|request|instruction)` +
		`|\b(?:you|the\s+user)\s+(?:asked|requested|told|instructed|wanted)\s+me\s+to\s+(?:stop|halt|abort)`)

	// optionTalkRe names the other decisions: a message that lists options
	// is describing the menu, not stopping.
	optionTalkRe = regexp.MustCompile(`(?i)\b(?:continue|skip|done|recall|review)\b|继续|跳过|回城|审查|裁决|决定`)

	// rejectionLinePrefixes open the lines of the rejection block whose tail
	// varies (the recorder's reason, the applied effects).
	rejectionLinePrefixes = []string{
		"Summoner：本次裁决未记录", RejectionCausePrefix, "本次回调已生效的部分", MappedContinueHeader,
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
	texts = append(texts, CheckpointDetail(run, *cp), "model: "+DecisionAskInstruction(*cp))
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
	if requestTiedStop(authored) {
		return true // an explicit acknowledgment of the user's request
	}
	if strict || pasted >= pasteThreshold {
		// Strict: the question is owed, a false hit is the stall. A PASTE
		// (mostly framework text): whatever stop words survive in its
		// leftovers — a stale conclusion line, say — belong to the pasted
		// content, not to the model's intent.
		return false
	}
	// A bare stop is a short statement that BEGINS or ENDS with a NARROW
	// stop word ("已停止。", "Halting now."): a stop word buried mid-sentence
	// ("…服务在升级时停止响应的原因是…") is something being described, and
	// the broad status words never qualify on their own.
	authored = strings.TrimSpace(authored)
	if utf8.RuneCountInString(authored) > stopAckBrevity {
		return false
	}
	return stopWordAtEdge(authored)
}

// requestTiedStop reports a stop word tied to the user's request in a
// clause that is not NEGATED. "I did not stop because you asked" and "没有
// 按要求停止" describe a stop that did not happen; any non-negated match is
// the acknowledgment.
func requestTiedStop(authored string) bool {
	for _, loc := range stopRequestRe.FindAllStringIndex(authored, -1) {
		if !negatedStop(authored, loc) {
			return true
		}
	}
	return false
}

var (
	// stopNegationEnRe: the clause before the match ends in a negation (up
	// to two short words may follow it: "has not been").
	stopNegationEnRe = regexp.MustCompile(`(?i)\b(?:not|never|no|cannot|can't|won't|don't|doesn't|didn't|isn't|wasn't|n't)\b[^A-Za-z]{0,3}(?:[A-Za-z]{1,12}\b[^A-Za-z]{0,2}){0,2}$`)
	// stopNegationZhRe: the last characters before the match are a negation.
	stopNegationZhRe = regexp.MustCompile(`(?:没有|没|未|不|别|无需|无须|非|尚)$`)
	// stopNegationInRe: a negation between the request marker and the stop
	// word ("按要求未停止").
	stopNegationInRe = regexp.MustCompile(`没有|未|没|不|别`)
)

func negatedStop(authored string, loc []int) bool {
	if stopNegationInRe.MatchString(authored[loc[0]:loc[1]]) {
		return true
	}
	// A period ends a clause too: the "not" of the PREVIOUS sentence does
	// not negate this one ("It's not reproducible. Stopping as you asked.").
	clauseStart := strings.LastIndexAny(authored[:loc[0]], "。！？!?；;\n.") + 1
	before := strings.TrimSpace(authored[clauseStart:loc[0]])
	if isASCII(before) {
		return stopNegationEnRe.MatchString(before)
	}
	return stopNegationZhRe.MatchString(lastRunes(before, 4))
}

func lastRunes(s string, n int) string {
	r := []rune(strings.Trim(s, "，,、：: "))
	if len(r) > n {
		r = r[len(r)-n:]
	}
	return string(r)
}

// stopWordAtEdge reports a bare stop. Two shapes count:
//
//   - the stop word OPENS the statement, behind nothing or an interjection
//     ("Halting the workflow now.", "好的，已停止。"), and what follows is
//     bounded: in Chinese only aspect particles and sentence punctuation
//     ("已停止了。"), in English at most three short words that are not a
//     participle — "已停止响应。" and "Stopped responding after the
//     upgrade." describe something that stopped responding;
//   - the stop word CLOSES a short statement ("系统已停止。").
//
// A subject-led sentence ("The service stopped responding after SIGTERM.",
// "网关已停止响应。") or a conclusion that happens to end in a stop word is
// something being described, not a stop (ship review rounds 2–4).
func stopWordAtEdge(s string) bool {
	for _, loc := range bareStopWordRe.FindAllStringIndex(s, -1) {
		before, after := s[:loc[0]], s[loc[1]:]
		if isInterjectionLeadIn(before) {
			// The stop word opens the statement. Its own clause must be
			// bounded (boundedStopTail); a FIRST-PERSON statement may run
			// on ("I'll stop here and wait for your decision.") as long as
			// nothing in it is something being described (a participle).
			if boundedStopTail(after) || firstPersonLeadIn(before) && !hasParticiple(after) {
				return true
			}
		}
		if tail(after) && utf8.RuneCountInString(before) <= 10 {
			return true
		}
	}
	return false
}

var (
	// zhStopTailRe: what may follow an opening Chinese stop word — the
	// aspect particle 了 and sentence punctuation, nothing else.
	zhStopTailRe = regexp.MustCompile(`^了?[。．.!！?？…~～，,；;]*$`)
	// enInterjections / zhInterjections: the only words that may stand
	// between the start of the statement and an opening stop word.
	enInterjections = map[string]bool{"i": true, "i'll": true, "i've": true, "i'm": true, "i'd": true, "will": true,
		"shall": true, "am": true, "we": true, "we'll": true, "we've": true, "okay": true, "ok": true, "yes": true,
		"sure": true, "understood": true, "alright": true, "right": true, "now": true, "so": true, "then": true,
		"and": true, "got": true, "it": true}
	zhInterjections = map[string]bool{"好": true, "好的": true, "明白": true, "收到": true, "已": true, "已经": true,
		"现": true, "现在": true, "我": true, "我们": true, "那就": true, "那么": true, "遵命": true, "嗯": true,
		"行": true, "这就": true}
)

func isInterjectionLeadIn(before string) bool {
	b := strings.TrimSpace(before)
	if b == "" {
		return true
	}
	if isASCII(b) {
		words := strings.Fields(strings.ToLower(strings.Trim(b, ",.!?:;")))
		if len(words) > 3 {
			return false
		}
		for _, w := range words {
			if !enInterjections[w] {
				return false
			}
		}
		return true
	}
	for _, seg := range strings.FieldsFunc(b, func(r rune) bool { return strings.ContainsRune("，,、：: \t", r) }) {
		if !zhInterjections[seg] {
			return false
		}
	}
	return true
}

// boundedStopTail reports whether an OPENING stop word ends cleanly within
// its own clause or sentence — what comes after the next boundary is another
// sentence ("Stopping here. I won't make any further changes."). In Chinese
// only 了 and clause punctuation may follow before the boundary; in English
// at most three short words that are not a participle.
func boundedStopTail(after string) bool {
	if isASCII(after) {
		clause := cutAtAny(after, ".!?;\n")
		trimmed := strings.TrimSpace(strings.TrimRight(clause, " .!;"))
		if trimmed == "" {
			return true
		}
		words := strings.Fields(trimmed)
		if len(words) > 3 || utf8.RuneCountInString(trimmed) > 20 {
			return false
		}
		return !hasParticiple(trimmed)
	}
	return zhStopTailRe.MatchString(cutAtAny(after, "，,。！？!?；;\n"))
}

// cutAtAny returns s up to (excluding) its first character from set.
func cutAtAny(s, set string) string {
	if i := strings.IndexAny(s, set); i >= 0 {
		return s[:i]
	}
	return s
}

// hasParticiple: an English word ending in "ing" — "responding",
// "forwarding": something described, not a stop.
func hasParticiple(s string) bool {
	for _, w := range strings.Fields(s) {
		if strings.HasSuffix(strings.ToLower(strings.Trim(w, ",.!;")), "ing") {
			return true
		}
	}
	return false
}

// firstPersonLeadIn: nothing or "I"/"we" with an auxiliary — the statement is
// the model's own act, not a description.
// firstPersonLeadIn: "I'll / I'm / we'll / we're / I will / we will" — a
// statement of what the model is doing now. The past tense ("I stopped the
// old service before the upgrade.") describes something that was stopped.
func firstPersonLeadIn(before string) bool {
	words := strings.Fields(strings.ToLower(strings.Trim(strings.TrimSpace(before), ",.!?:;")))
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "i'll", "i'm", "we'll", "we're":
		return true
	case "i", "we":
		return len(words) > 1 && (words[1] == "will" || words[1] == "am" || words[1] == "are" || words[1] == "shall")
	}
	return false
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
