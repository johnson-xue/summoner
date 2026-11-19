package shared

// render.go — the ONE canonical checkpoint renderer (final-fix wave C4,
// adjudication §2). Every consumer of checkpoint display bytes uses
// RenderCheckpointMessage: the read-only status view:checkpoint output, and
// the Stop hook's whole-message display binding. There is no second
// renderer, so "what the user saw" and "what the hooks bound" cannot drift.
//
// The message is deterministic from run state: same state in, same bytes
// out. Scalar strings (notes, goals, labels, paths) are escaped so control
// characters, bidirectional overrides and reserved delimiters can never
// forge or hide structure. The comparison contract: normalize with
// NormalizePrompt (CRLF→LF, at most one trailing LF), then compare the
// ENTIRE message — a correct block plus false prose fails.
//
// Layout (user ruling 2026-11-19): a LEFT-RAIL structured frame — ╔ header,
// ║ rails with ▶ section heads, ╠ decision divider, ╚ tail. The rail is
// deliberately left-only: no right edge means no CJK/full-width alignment
// math and no runewidth dependency, so the bytes stay deterministic on every
// terminal while the frame still gives the checkpoint its table readability
// back. The inner header signature (id — nonce — kind) and every field text
// are kept verbatim from the pre-frame render; only the layout changed.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// renderFrameTail closes the frame; fixed width, never content-dependent.
const renderFrameTail = "╚════════════════════════════════════\n"

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
	fmt.Fprintf(&b, "╔═ ⚡ SUMMONER CHECKPOINT %s — nonce %s — %s ══════════\n", escapeScalar(cp.ID), escapeScalar(cp.Nonce), escapeScalar(string(cp.Kind)))
	phase := cp.PhaseID
	if phase == "" {
		phase = "—"
	}
	railf(&b, "run: %s · workflow: %s · requirements v%d\n", escapeScalar(run.RunID), escapeScalar(run.Workflow), run.RequirementsVersion)
	railf(&b, "phase: %s\n", escapeScalar(phase))

	// ▶ 本阶段结果 — the submitted phase summary.
	railBlank(&b)
	railSection(&b, "本阶段结果")
	note := cp.Note
	if note == "" {
		note = "—"
	}
	railf(&b, "  note: %s\n", escapeScalar(note))

	// ▶ 验收标准 — complete criterion meaning, never bare ids. The staged
	// snapshot's criteria carry goal/expected/scope/method/required; ids
	// without staged meaning are listed honestly as pending-meaning ids.
	railBlank(&b)
	railSection(&b, "验收标准")
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
	if len(ids) == 0 {
		railf(&b, "  —\n")
	}
	for _, id := range ids {
		c, ok := byID[id]
		if !ok {
			railf(&b, "  %s (no staged meaning; judged at acceptance)\n", escapeScalar(id))
			continue
		}
		required := "optional"
		if c.Required {
			required = "required"
		}
		railf(&b, "  %s [%s · %s]\n", escapeScalar(c.ID), escapeScalar(string(c.Method)), required)
		if c.Goal != "" {
			railf(&b, "    goal: %s\n", escapeScalar(c.Goal))
		}
		if c.Expected != "" {
			railf(&b, "    expected: %s\n", escapeScalar(c.Expected))
		}
		if c.Scope != "" {
			railf(&b, "    scope: %s\n", escapeScalar(c.Scope))
		}
		if c.CheckID != "" {
			railf(&b, "    check: %s\n", escapeScalar(c.CheckID))
		}
		if c.Rubric != "" {
			railf(&b, "    rubric: %s\n", escapeScalar(c.Rubric))
		}
	}

	// ▶ 产物（版本已冻结）— versioned artifact digests: the pinned bytes a
	// decision accepts.
	railBlank(&b)
	railSection(&b, "产物（版本已冻结）")
	paths := make([]string, 0, len(cp.ArtifactVersions))
	for p := range cp.ArtifactVersions {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		railf(&b, "  —\n")
	} else {
		for _, p := range paths {
			railf(&b, "  %s @ %s\n", escapeScalar(p), escapeScalar(cp.ArtifactVersions[p]))
		}
	}

	// ▶ 需求快照 — requirement meaning staged for this acceptance.
	if len(cp.Snapshot.Requirements) > 0 {
		railBlank(&b)
		railSection(&b, "需求快照")
		for _, fr := range cp.Snapshot.Requirements {
			content := fr.Content
			if content == "" {
				content = "—"
			}
			ver := ""
			if fr.Version > 0 {
				ver = fmt.Sprintf(" v%d", fr.Version)
			}
			railf(&b, "  %s%s: %s\n", escapeScalar(fr.ID), ver, escapeScalar(content))
			if len(fr.CriteriaIDs) > 0 {
				crit := append([]string(nil), fr.CriteriaIDs...)
				sort.Strings(crit)
				railf(&b, "    criteria: %s\n", escapeScalar(strings.Join(crit, ", ")))
			}
		}
	}

	// ▶ 计划范围（尚未落地）— the staged planned scope of the snapshot.
	if planned := cp.Snapshot.PlannedScopeOf(); len(planned) > 0 {
		sort.Strings(planned)
		railBlank(&b)
		railSection(&b, "计划范围（尚未落地）")
		railf(&b, "  %s\n", escapeScalar(strings.Join(planned, ", ")))
	}

	// ▶ 验证环境 — verification environment/tier (observed facts only).
	env := cp.Snapshot.Environment
	railBlank(&b)
	railSection(&b, "验证环境")
	railf(&b, "  tier=%s os=%s arch=%s runtime=%s defaults=%s\n",
		escapeScalar(firstNonEmpty(env.Tier, "unobserved")),
		escapeScalar(firstNonEmpty(env.OS, "unobserved")),
		escapeScalar(firstNonEmpty(env.Arch, "unobserved")),
		escapeScalar(firstNonEmpty(env.Runtime, "unobserved")),
		escapeScalar(shortDigestOr(env.DefaultsDigest, "unobserved")))

	// ▶ 预算 — applied limits and counters, plus any persisted digest-bound
	// offer.
	railBlank(&b)
	railSection(&b, "预算")
	railf(&b, "  limits %s · counters %s\n", budgetLimitsKey(run.Limits), budgetCountersKey(run.Counters))
	if cp.BudgetAsk != nil {
		source := cp.BudgetAsk.Proposal.Source
		if source == "" {
			source = "framework"
		}
		railf(&b, "  budget ask (%s proposal — rides the decision call): confirm=\"%s\" unlimited=\"%s\" limits %s\n",
			source, escapeScalar(cp.BudgetAsk.ConfirmLabel), escapeScalar(cp.BudgetAsk.UnlimitedLabel),
			budgetLimitsKey(cp.BudgetAsk.Proposal.Limits))
	}
	if cp.ExtendAsk != nil {
		railf(&b, "  extend ask (budget exhausted): extend=\"%s\" hold=\"%s\" delta %s\n",
			escapeScalar(cp.ExtendAsk.ExtendLabel), escapeScalar(cp.ExtendAsk.HoldLabel),
			budgetLimitsKey(cp.ExtendAsk.Delta))
	}

	// The decision divider: everything above is state, everything below is
	// the ask. The full legal menu follows (labels as persisted; pages too).
	b.WriteString("╠═ 下一轮裁决 — AskUserQuestion（选项须与一页菜单完全一致）\n")
	b.WriteString("║ menu:\n")
	for _, opt := range DecisionMenuLabels(cp.Kind) {
		b.WriteString("║   ")
		b.WriteString(escapeScalar(opt))
		b.WriteString("\n")
	}
	if len(cp.MenuPages) > 1 {
		for i, pg := range cp.MenuPages {
			nav := ""
			if pg.NavNext != "" {
				nav += " — " + pg.NavNext
			}
			if pg.NavPrev != "" {
				nav += " — " + pg.NavPrev
			}
			railf(&b, "  page %d/%d: %s%s\n", i+1, len(cp.MenuPages), escapeScalar(strings.Join(pg.Labels, " · ")), escapeScalar(nav))
		}
	}

	// Recall targets actually offered at this checkpoint.
	if len(cp.RecallTargets) > 0 {
		b.WriteString("║ recall targets:\n")
		for _, t := range cp.RecallTargets {
			railf(&b, "  - %s (phase %s, visit %s)\n", escapeScalar(t.Label), escapeScalar(t.PhaseID), escapeScalar(t.VisitID))
		}
	}

	// A pending funded same-envelope retry offer.
	if cp.ReviewRetry != nil {
		railf(&b, "review retry: review %s attempt %s — resume %s · retry=\"%s\" skip=\"%s\"\n",
			escapeScalar(cp.ReviewRetry.ReviewID), escapeScalar(cp.ReviewRetry.AttemptID),
			escapeScalar(cp.ReviewRetry.ResumeTarget), escapeScalar(cp.ReviewRetry.RetryLabel),
			escapeScalar(cp.ReviewRetry.SkipLabel))
	}

	railf(&b, "decide on the NEXT turn with an AskUserQuestion whose question carries nonce %s and whose options equal one menu page exactly.\n", escapeScalar(cp.Nonce))
	b.WriteString(renderFrameTail)
	return b.String()
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

// VerifyDisplayMessage is the whole-message display binding (C4): the
// normalized last_assistant_message must equal the normalized canonical
// render of the CURRENT checkpoint, ENTIRELY. Two substrings, a correct
// block plus false prose, or a stale render all fail.
func VerifyDisplayMessage(run *RunState, cp CheckpointState, message string) bool {
	want := RenderCheckpointMessage(run, cp)
	return NormalizePrompt(message) == NormalizePrompt(want)
}

// budgetCountersKey is the canonical one-line form of BudgetCounters.
func budgetCountersKey(c BudgetCounters) string {
	return fmt.Sprintf("phase_tool_calls=%d,failed_checks=%d,run_rework=%d,delegations=%d",
		c.PhaseToolCalls, c.FailedChecks, c.RunRework, c.Delegations)
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
