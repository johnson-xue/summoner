package shared

// render.go — the ONE canonical checkpoint renderer (final-fix wave C4,
// adjudication §2; rewritten for 0.7.0 D1). Every consumer of checkpoint
// display bytes uses RenderCheckpointMessage: the read-only status
// view:checkpoint output, and the Stop hook's display binding. There is no
// second renderer, so "what the user saw" and "what the hooks bound" cannot
// drift.
//
// The render is the user's DECISION SURFACE, and it leads with the
// conclusion. Incident review 2026-11-23 (run df0ed2e6): five checkpoints
// whose render carried sixty lines of mechanism — criteria tables, artifact
// digests, environment, budget counters, a page projection — and never said
// what the bug was or how it was looked for. So the order is fixed:
//
//	经过   the run's settled phases, each with its conclusion (the user can
//	       read any checkpoint without the conversation)
//	结论   what this phase established: summary → evidence → how → what is
//	       still open → impact (every missing part reads 未提供)
//	 rides  only what the decision cannot do without: an exhaustion extend
//	       offer, a pending review retry
//	请决定 page 1 of the options, the nonce, and ONE navigation hint
//
// Everything mechanical moved to the summoner-status default view
// (CheckpointDetail): complete criteria, artifact digests, requirement
// snapshot, environment, page projection, every recall target.
//
// The message is deterministic from run state: same state in, same bytes
// out. Scalar strings (notes, conclusions, labels, paths) are escaped so
// control characters, bidirectional overrides and reserved delimiters can
// never forge or hide structure; conclusion prose is folded to one line
// before it is capped, so a paragraph can never open a second rail line.
//
// Layout (user ruling 2026-11-19): a LEFT-RAIL structured frame — ╔ header,
// ║ rails with ▶ section heads, ╠ decision divider, ╚ tail. The rail is
// deliberately left-only: no right edge means no CJK/full-width alignment
// math and no runewidth dependency, so the bytes stay deterministic on every
// terminal while the frame still gives the checkpoint its table readability.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// renderFrameTail closes the frame; fixed width, never content-dependent.
const renderFrameTail = "╚════════════════════════════════════\n"

// reviewFailedPrefix is the note prefix of a review-result checkpoint that
// routes a FRAMEWORK-side failure (a result the protocol could not accept) —
// never a verdict about the change under review.
const reviewFailedPrefix = "FAILED "

// Display caps (0.7.0 D1). The checkpoint render stays under renderLineCap
// lines on the largest legal submission (pinned by
// TestRenderMaxLegalSubmissionStaysUnderLineCap); what a cap rolls off is
// COUNTED in the render and read in full in the summoner-status view.
const (
	// renderLineCap counts newlines of the canonical render.
	renderLineCap = 42
	// capConclusionText bounds the summary and the approach (runes).
	capConclusionText = 200
	// capConclusionShort bounds the impact, one evidence item, one open
	// question and one 经过 entry (runes).
	capConclusionShort   = 120
	capEvidenceItems     = 4
	capOpenQuestionItems = 3
	capJourneyLines      = 6
)

// railf writes one railed content line: "║ " + the formatted text.
func railf(b *strings.Builder, format string, args ...interface{}) {
	b.WriteString("║ ")
	fmt.Fprintf(b, format, args...)
}

// railBlank writes the bare rail spacer line.
func railBlank(b *strings.Builder) { b.WriteString("║\n") }

// railSection writes a ▶ section head line.
func railSection(b *strings.Builder, name string) { railf(b, "▶ %s\n", name) }

// RenderCheckpointMessage renders the canonical checkpoint message of the
// run's CURRENT open checkpoint. Pure: reads only the given state.
func RenderCheckpointMessage(run *RunState, cp CheckpointState) string {
	var b strings.Builder
	phase := cp.PhaseID
	if phase == "" {
		phase = "—"
	}
	fmt.Fprintf(&b, "╔═ ⚡ SUMMONER CHECKPOINT %s — nonce %s — %s ══════════\n", escapeScalar(cp.ID), escapeScalar(cp.Nonce), escapeScalar(string(cp.Kind)))
	railf(&b, "run: %s · workflow: %s · phase: %s · requirements v%d\n",
		escapeScalar(run.RunID), escapeScalar(run.Workflow), escapeScalar(phase), run.RequirementsVersion)

	// ▶ 经过 — the conclusions the user already has.
	if journey := JourneyLines(run); len(journey) > 0 {
		railBlank(&b)
		railSection(&b, "经过")
		for _, ln := range journey {
			railf(&b, "  %s\n", ln)
		}
	}

	// ▶ 结论 — what this phase established.
	railBlank(&b)
	if cp.Kind == CheckpointPhase {
		railSection(&b, "结论 — "+conclusionLabel(cp.Outcome))
		writeConclusion(&b, cp)
	} else if strings.HasPrefix(cp.Note, reviewFailedPrefix) {
		// A framework-side validation failure is not a verdict about the
		// change: say what went wrong, and what it means for the user —
		// never let a protocol error read like a quality conclusion.
		railSection(&b, "结论 — 框架未能完成审查")
		railf(&b, "  原因：%s\n", conclusionText(strings.TrimPrefix(cp.Note, reviewFailedPrefix), capConclusionText))
		railf(&b, "  对你的影响：改动尚未被审查\n")
	} else {
		// Change-confirm, recovery, review-result and review-debt carry
		// their meaning in the note (the delta, the probe outcomes, the
		// verdict) — there is no submission to structure.
		railSection(&b, "结论")
		railf(&b, "  %s\n", conclusionText(cp.Note, capConclusionText))
	}

	// Rides the decision cannot do without.
	if cp.ExtendAsk != nil || cp.ReviewRetry != nil {
		railBlank(&b)
	}
	if cp.ExtendAsk != nil {
		railf(&b, "预算已耗尽 · 本次裁决可顺带追加: %s / %s（追加 %s）\n",
			escapeScalar(cp.ExtendAsk.ExtendLabel), escapeScalar(cp.ExtendAsk.HoldLabel),
			escapeScalar(extendOfferKey(cp.ExtendAsk.Delta)))
	}
	if cp.ReviewRetry != nil {
		railf(&b, "review retry: review %s attempt %s — resume %s · retry=\"%s\" skip=\"%s\"\n",
			escapeScalar(cp.ReviewRetry.ReviewID), escapeScalar(cp.ReviewRetry.AttemptID),
			escapeScalar(cp.ReviewRetry.ResumeTarget), escapeScalar(cp.ReviewRetry.RetryLabel),
			escapeScalar(cp.ReviewRetry.SkipLabel))
	}

	// The decision divider: page 1 of the options, the nonce, one hint.
	b.WriteString("╠═ 请决定 — AskUserQuestion（选项须与本页完全一致）\n")
	for _, label := range DecisionPage(cp) {
		b.WriteString("║   ")
		b.WriteString(escapeScalar(label))
		b.WriteString("\n")
	}
	railf(&b, "nonce %s · %s\n", escapeScalar(cp.Nonce), navigationHint(cp))
	b.WriteString(renderFrameTail)
	return b.String()
}

// DecisionPage returns page 1 of the checkpoint's persisted option pages —
// the labels the render prints and the first decision question must offer
// verbatim. State that predates the paged menu (no projection) falls back to
// the kind's full menu: the gate refuses to admit a question against such a
// checkpoint anyway, and the render must never be the thing that hides it.
func DecisionPage(cp CheckpointState) []string {
	if len(cp.MenuPages) > 0 {
		return cp.MenuPages[0].Labels
	}
	return DecisionMenuLabels(cp.Kind)
}

// navigationHint is the ONE line that keeps the rest reachable: how to see
// the other option pages, and where the mechanism the render left out lives.
func navigationHint(cp CheckpointState) string {
	if len(cp.MenuPages) > 1 {
		return "其余选项（recall / review / 回城目标）回复 more: 翻页 · 验收标准与产物全文见 summoner-status"
	}
	return "选项已全部列出 · 验收标准与产物全文见 summoner-status"
}

// conclusionLabel names what the phase established. An omitted outcome (every
// pre-0.7.0 checkpoint, a conclusion-free submission) reads unclear: nothing
// was claimed.
func conclusionLabel(outcome string) string {
	switch outcome {
	case ConclusionConfirmed:
		return "已确认"
	case ConclusionFailed:
		return "未达成"
	default:
		return "未查清"
	}
}

// writeConclusion renders a phase checkpoint's conclusion in its fixed inner
// order. A submission without one is never silently blank: the phase summary
// stands in as the conclusion and every missing part reads 未提供.
func writeConclusion(b *strings.Builder, cp CheckpointState) {
	var c Conclusion
	if cp.Conclusion != nil {
		c = *cp.Conclusion
	}
	summary := c.Summary
	if strings.TrimSpace(summary) == "" {
		summary = cp.Note
	}
	railf(b, "  %s\n", conclusionText(summary, capConclusionText))

	if len(c.Evidence) == 0 {
		railf(b, "  证据: 未提供\n")
	} else {
		railf(b, "  证据:\n")
		writeCappedList(b, c.Evidence, capEvidenceItems)
	}
	railf(b, "  %s: %s\n", approachLabel(cp.PhaseID), conclusionText(c.Approach, capConclusionText))
	if cp.Outcome != ConclusionConfirmed && cp.Outcome != ConclusionFailed {
		// Unclear — stated or merely unclaimed: the user is the one who
		// judges, and an unlisted hole is still a hole.
		railf(b, "  没查清 · 需要你判断:\n")
		if len(c.OpenQuestions) == 0 {
			railf(b, "    未提供\n")
		} else {
			writeCappedList(b, c.OpenQuestions, capOpenQuestionItems)
		}
	}
	railf(b, "  影响: %s\n", conclusionText(c.Impact, capConclusionShort))
}

// writeCappedList writes at most cap items of a conclusion list and COUNTS
// what rolled off — a cap never silently drops a reason.
func writeCappedList(b *strings.Builder, items []string, cap int) {
	for i, it := range items {
		if i == cap {
			railf(b, "    …另 %d 条，见 summoner-status\n", len(items)-i)
			return
		}
		railf(b, "    - %s\n", conclusionText(it, capConclusionShort))
	}
}

// conclusionText folds one conclusion scalar to a single railed line's worth:
// whitespace-collapsed, capped in runes (an overflow ends in …), escaped,
// and 未提供 when there is nothing to show.
func conclusionText(s string, cap int) string {
	s = clipRunes(singleLine(s), cap)
	if s == "" {
		return "未提供"
	}
	return escapeScalar(s)
}

// approachLabel names the "how" line the way the phase works: investigative
// phases say how it was LOOKED FOR, building phases how it was DONE.
func approachLabel(phaseID string) string {
	switch phaseID {
	case "context", "diagnosis", "reproduce", "verify", "regression", "review", "prepare":
		return "怎么查的"
	default:
		return "怎么做的"
	}
}

// JourneyLines renders the run's settled phase visits oldest first — how each
// was settled (accepted, auto-passed, skipped, recalled) and its conclusion
// — capped at capJourneyLines with the overflow counted. A phase visited more
// than once is named by its visit id. Already escaped for the rail.
func JourneyLines(run *RunState) []string {
	visits := make(map[string]int, len(run.Phases))
	for _, p := range run.Phases {
		visits[p.ID]++
	}
	var lines []string
	for _, p := range run.Phases {
		mark := ""
		switch p.Outcome {
		case "accepted":
			mark = "✓ 已验收"
		case "skipped":
			mark = "— 已跳过"
		case "recalled":
			mark = "↩ 已回城"
		default:
			continue // open or undecided: not history yet
		}
		title := ""
		for i := range run.Checkpoints {
			if cp := &run.Checkpoints[i]; cp.ID == p.CheckpointID {
				title = strings.TrimSpace(cp.Note)
				if title == "" && cp.Conclusion != nil {
					title = strings.TrimSpace(cp.Conclusion.Summary)
				}
				if p.Outcome == "accepted" && cp.Decision == DecisionAutoPass {
					mark = "✓ 自动通过"
				}
				break
			}
		}
		if title == "" {
			title = "（未提供结论）"
		}
		name := p.ID
		if visits[p.ID] > 1 {
			name = p.VisitID
		}
		prefix := escapeScalar(name) + " " + mark + ": "
		lines = append(lines, prefix+escapeScalar(clipRunes(singleLine(title), capConclusionShort-utf8.RuneCountInString(prefix))))
	}
	if len(lines) > capJourneyLines {
		rolled := len(lines) - (capJourneyLines - 1)
		overflow := "…另 " + strconv.Itoa(rolled) + " 个阶段，见 summoner-status"
		lines = append([]string{overflow}, lines[rolled:]...)
	}
	return lines
}

// extendOfferKey names only the SET axes of an extension offer — the ones
// the answer would actually add.
func extendOfferKey(l BudgetLimits) string {
	var axes []string
	if l.PhaseToolCalls > 0 {
		axes = append(axes, "phase_tool_calls +"+strconv.Itoa(l.PhaseToolCalls))
	}
	if l.FailedChecks > 0 {
		axes = append(axes, "failed_checks +"+strconv.Itoa(l.FailedChecks))
	}
	if l.RunRework > 0 {
		axes = append(axes, "run_rework +"+strconv.Itoa(l.RunRework))
	}
	if l.Delegations > 0 {
		axes = append(axes, "delegations +"+strconv.Itoa(l.Delegations))
	}
	if len(axes) == 0 {
		return "（无）"
	}
	return strings.Join(axes, ", ")
}

// DisplayBlock is the canonical message minus its tail line — the text the
// Stop hook's display binding looks for. The tail is excluded on purpose: it
// is the one line a paste may get wrong (a longer or shorter ╚ rule)
// without changing a word of what the user was shown.
func DisplayBlock(run *RunState, cp CheckpointState) string {
	return strings.TrimSuffix(RenderCheckpointMessage(run, cp), renderFrameTail)
}

// VerifyDisplayContainment is the display binding (0.7.0 D2, replacing
// whole-message equality): the normalized last_assistant_message must
// CONTAIN the canonical block of the CURRENT checkpoint, entire and
// contiguous. An explanation before it and prose after it are fine — the
// model may say what it found and then show the checkpoint; the incident of
// 2026-11-23 lost an hour to equality rejecting exactly that. A truncated
// block, an altered line, a hand-written frame or the render of another
// (stale) checkpoint all fail: only the real block binds.
func VerifyDisplayContainment(run *RunState, cp CheckpointState, message string) bool {
	return strings.Contains(NormalizePrompt(message), NormalizePrompt(DisplayBlock(run, cp)))
}

// PlannedScopeOf projects the staged planned scope from the snapshot. Kept as
// a method so the render and the staging share one source of truth.
func (v FrozenView) PlannedScopeOf() []string {
	var out []string
	for _, fr := range v.Requirements {
		out = append(out, fr.PlannedScope...)
	}
	return out
}

// CheckpointCriteriaIDs returns every criterion the checkpoint's acceptance
// covers: the submitted ids plus the staged set, sorted — the list the
// status view prints and the acceptance freezes.
func CheckpointCriteriaIDs(cp CheckpointState) []string {
	byID := make(map[string]Criterion, len(cp.Snapshot.Criteria))
	for _, c := range cp.Snapshot.Criteria {
		byID[c.ID] = c
	}
	ids := append([]string(nil), cp.CriteriaIDs...)
	for _, c := range cp.Snapshot.Criteria {
		if !containsID(ids, c.ID) {
			ids = append(ids, c.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// singleLine folds all whitespace runs (newlines included) into single
// spaces.
func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// clipRunes keeps the first max runes; an overflow ends in an ellipsis that
// replaces the last kept rune, so the result is exactly max runes. Counts
// RUNES, never bytes: the range variable's first value is a byte offset, and
// on CJK text (3 bytes a rune) an offset used as a count both over-runs and
// stops early.
func clipRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	if max < 1 {
		return ""
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n == max-1 {
			b.WriteString("…")
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func shortDigestOr(d, fallback string) string {
	if d == "" {
		return fallback
	}
	return d
}

// escapeScalar neutralizes one scalar string for the canonical render:
// control characters (other than the plain space) and Unicode bidirectional
// /format controls become their \uXXXX escapes, backslash is doubled, and
// newlines/carriage returns become escaped literals so a scalar can never
// inject forged lines into the message structure.
func escapeScalar(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString("\\\\")
		case r == '\n':
			b.WriteString("\\n")
		case r == '\r':
			b.WriteString("\\r")
		case r == '\t':
			b.WriteString("\\t")
		case unicode.IsControl(r), unicode.Is(unicode.Bidi_Control, r), unicode.Is(unicode.Cf, r):
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// QuoteLabel renders one menu label for a question payload (kept beside the
// renderer so the ask side and the render side cannot drift).
func QuoteLabel(s string) string { return strconv.Quote(s) }
