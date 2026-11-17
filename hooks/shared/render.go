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

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// RenderCheckpointMessage renders the canonical checkpoint message of the
// run's CURRENT open checkpoint. Pure: reads only the given state.
func RenderCheckpointMessage(run *RunState, cp CheckpointState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "⚡ SUMMONER CHECKPOINT %s — nonce %s — %s\n", escapeScalar(cp.ID), escapeScalar(cp.Nonce), escapeScalar(string(cp.Kind)))
	phase := cp.PhaseID
	if phase == "" {
		phase = "—"
	}
	fmt.Fprintf(&b, "run: %s · workflow: %s · requirements v%d\n", escapeScalar(run.RunID), escapeScalar(run.Workflow), run.RequirementsVersion)
	fmt.Fprintf(&b, "phase: %s\n", escapeScalar(phase))
	note := cp.Note
	if note == "" {
		note = "—"
	}
	fmt.Fprintf(&b, "note: %s\n", escapeScalar(note))

	// Complete criterion meaning — never bare ids. The staged snapshot's
	// criteria carry goal/expected/scope/method/required; ids without staged
	// meaning are listed honestly as pending-meaning ids.
	b.WriteString("criteria:\n")
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
		b.WriteString("  —\n")
	}
	for _, id := range ids {
		c, ok := byID[id]
		if !ok {
			fmt.Fprintf(&b, "  - %s (no staged meaning; judged at acceptance)\n", escapeScalar(id))
			continue
		}
		required := "optional"
		if c.Required {
			required = "required"
		}
		fmt.Fprintf(&b, "  - %s [%s · %s]\n", escapeScalar(c.ID), escapeScalar(string(c.Method)), required)
		if c.Goal != "" {
			fmt.Fprintf(&b, "      goal: %s\n", escapeScalar(c.Goal))
		}
		if c.Expected != "" {
			fmt.Fprintf(&b, "      expected: %s\n", escapeScalar(c.Expected))
		}
		if c.Scope != "" {
			fmt.Fprintf(&b, "      scope: %s\n", escapeScalar(c.Scope))
		}
		if c.CheckID != "" {
			fmt.Fprintf(&b, "      check: %s\n", escapeScalar(c.CheckID))
		}
		if c.Rubric != "" {
			fmt.Fprintf(&b, "      rubric: %s\n", escapeScalar(c.Rubric))
		}
	}

	// Versioned artifact digests — the pinned bytes a decision accepts.
	paths := make([]string, 0, len(cp.ArtifactVersions))
	for p := range cp.ArtifactVersions {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		b.WriteString("artifacts: —\n")
	} else {
		b.WriteString("artifacts (pinned versions):\n")
		for _, p := range paths {
			fmt.Fprintf(&b, "  - %s @ %s\n", escapeScalar(p), escapeScalar(cp.ArtifactVersions[p]))
		}
	}

	// Requirement meaning staged for this acceptance.
	if len(cp.Snapshot.Requirements) > 0 {
		b.WriteString("requirements:\n")
		for _, fr := range cp.Snapshot.Requirements {
			content := fr.Content
			if content == "" {
				content = "—"
			}
			ver := ""
			if fr.Version > 0 {
				ver = fmt.Sprintf(" v%d", fr.Version)
			}
			fmt.Fprintf(&b, "  - %s%s: %s\n", escapeScalar(fr.ID), ver, escapeScalar(content))
			if len(fr.CriteriaIDs) > 0 {
				crit := append([]string(nil), fr.CriteriaIDs...)
				sort.Strings(crit)
				fmt.Fprintf(&b, "      criteria: %s\n", escapeScalar(strings.Join(crit, ", ")))
			}
		}
	}
	if planned := cp.Snapshot.PlannedScopeOf(); len(planned) > 0 {
		sort.Strings(planned)
		fmt.Fprintf(&b, "planned scope (not yet existing): %s\n", escapeScalar(strings.Join(planned, ", ")))
	}

	// Verification environment/tier (observed facts only).
	env := cp.Snapshot.Environment
	fmt.Fprintf(&b, "verification: tier=%s os=%s arch=%s runtime=%s defaults=%s\n",
		escapeScalar(firstNonEmpty(env.Tier, "unobserved")),
		escapeScalar(firstNonEmpty(env.OS, "unobserved")),
		escapeScalar(firstNonEmpty(env.Arch, "unobserved")),
		escapeScalar(firstNonEmpty(env.Runtime, "unobserved")),
		escapeScalar(shortDigestOr(env.DefaultsDigest, "unobserved")))

	// Budget: applied limits and counters, plus any persisted digest-bound offer.
	fmt.Fprintf(&b, "budget: limits %s · counters %s\n", budgetLimitsKey(run.Limits), budgetCountersKey(run.Counters))
	if cp.BudgetAsk != nil {
		source := cp.BudgetAsk.Proposal.Source
		if source == "" {
			source = "framework"
		}
		fmt.Fprintf(&b, "budget ask (%s proposal — rides the decision call): confirm=\"%s\" unlimited=\"%s\" limits %s\n",
			source, escapeScalar(cp.BudgetAsk.ConfirmLabel), escapeScalar(cp.BudgetAsk.UnlimitedLabel),
			budgetLimitsKey(cp.BudgetAsk.Proposal.Limits))
	}
	if cp.ExtendAsk != nil {
		fmt.Fprintf(&b, "extend ask (budget exhausted): extend=\"%s\" hold=\"%s\" delta %s\n",
			escapeScalar(cp.ExtendAsk.ExtendLabel), escapeScalar(cp.ExtendAsk.HoldLabel),
			budgetLimitsKey(cp.ExtendAsk.Delta))
	}

	// The actual legal menu (full label set; pages as persisted).
	b.WriteString("menu:\n")
	for _, opt := range DecisionMenuLabels(cp.Kind) {
		b.WriteString("  " + escapeScalar(opt) + "\n")
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
			fmt.Fprintf(&b, "  page %d/%d: %s%s\n", i+1, len(cp.MenuPages), escapeScalar(strings.Join(pg.Labels, " · ")), escapeScalar(nav))
		}
	}

	// Recall targets actually offered at this checkpoint.
	if len(cp.RecallTargets) > 0 {
		b.WriteString("recall targets:\n")
		for _, t := range cp.RecallTargets {
			fmt.Fprintf(&b, "  - %s (phase %s, visit %s)\n", escapeScalar(t.Label), escapeScalar(t.PhaseID), escapeScalar(t.VisitID))
		}
	}

	// A pending funded same-envelope retry offer.
	if cp.ReviewRetry != nil {
		fmt.Fprintf(&b, "review retry: review %s attempt %s — resume %s · retry=\"%s\" skip=\"%s\"\n",
			escapeScalar(cp.ReviewRetry.ReviewID), escapeScalar(cp.ReviewRetry.AttemptID),
			escapeScalar(cp.ReviewRetry.ResumeTarget), escapeScalar(cp.ReviewRetry.RetryLabel),
			escapeScalar(cp.ReviewRetry.SkipLabel))
	}

	fmt.Fprintf(&b, "decide on the NEXT turn with an AskUserQuestion whose question carries nonce %s and whose options equal one menu page exactly.\n", escapeScalar(cp.Nonce))
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
			b.WriteString(fmt.Sprintf("\\u%04x", r))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// QuoteLabel renders one menu label for a question payload (kept beside the
// renderer so the ask side and the render side cannot drift).
func QuoteLabel(s string) string { return strconv.Quote(s) }
