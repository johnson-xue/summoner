package shared

// detail.go — the checkpoint DETAIL render (0.7.0 D1): everything the
// canonical checkpoint message deliberately leaves out. The checkpoint
// message is the decision surface and leads with the conclusion; the detail
// is the reference view the summoner-status default render prints right
// before it — complete and uncapped, never railed (it is read, not pasted as
// a display act).
//
// What lives here and nowhere else on the status surface: the conclusion in
// full (the message caps every part), the complete acceptance criteria with
// their meaning, the frozen artifact versions, the requirement snapshot, the
// planned scope, the verification environment, the menu page projection,
// every recall target, a pending review retry, and the instruction for the
// model that asks the decision question.

import (
	"fmt"
	"sort"
	"strings"
)

// CheckpointDetail renders the reference view of one checkpoint. Pure: reads
// only the given state. Deterministic: same state, same bytes.
func CheckpointDetail(run *RunState, cp CheckpointState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "checkpoint detail (%s — nonce %s — %s):\n", cp.ID, cp.Nonce, cp.Kind)

	if cp.Kind == CheckpointPhase {
		fmt.Fprintf(&b, "  conclusion (%s %s):\n", cp.Outcome, conclusionLabel(cp.Outcome))
		writeDetailConclusion(&b, cp)
	}

	byID := make(map[string]Criterion, len(cp.Snapshot.Criteria))
	for _, c := range cp.Snapshot.Criteria {
		byID[c.ID] = c
	}
	ids := CheckpointCriteriaIDs(cp)
	fmt.Fprintf(&b, "  criteria (%d):\n", len(ids))
	for _, id := range ids {
		c, ok := byID[id]
		if !ok {
			fmt.Fprintf(&b, "    %s (no staged meaning; judged at acceptance)\n", id)
			continue
		}
		required := "optional"
		if c.Required {
			required = "required"
		}
		fmt.Fprintf(&b, "    %s [%s · %s]\n", c.ID, c.Method, required)
		writeDetailField(&b, "goal", c.Goal, 6)
		writeDetailField(&b, "expected", c.Expected, 6)
		writeDetailField(&b, "scope", c.Scope, 6)
		// check/rubric are alternatives, not parts: a judgment criterion has
		// no check by design — an absent one is not a missing one.
		if c.CheckID != "" {
			writeDetailField(&b, "check", c.CheckID, 6)
		}
		if c.Rubric != "" {
			writeDetailField(&b, "rubric", c.Rubric, 6)
		}
	}

	paths := make([]string, 0, len(cp.ArtifactVersions))
	for p := range cp.ArtifactVersions {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	fmt.Fprintf(&b, "  artifacts (frozen versions, %d):\n", len(paths))
	for _, p := range paths {
		fmt.Fprintf(&b, "    %s @ %s\n", p, cp.ArtifactVersions[p])
	}

	if len(cp.Snapshot.Requirements) > 0 {
		b.WriteString("  requirements:\n")
		for _, fr := range cp.Snapshot.Requirements {
			content := fr.Content
			if content == "" {
				content = "—"
			}
			ver := ""
			if fr.Version > 0 {
				ver = fmt.Sprintf(" v%d", fr.Version)
			}
			fmt.Fprintf(&b, "    %s%s: %s\n", fr.ID, ver, content)
			if len(fr.CriteriaIDs) > 0 {
				crit := append([]string(nil), fr.CriteriaIDs...)
				sort.Strings(crit)
				fmt.Fprintf(&b, "      criteria: %s\n", strings.Join(crit, ", "))
			}
			if len(fr.Artifacts) > 0 {
				fmt.Fprintf(&b, "      artifacts: %s\n", strings.Join(fr.Artifacts, ", "))
			}
		}
	}
	if planned := cp.Snapshot.PlannedScopeOf(); len(planned) > 0 {
		sort.Strings(planned)
		fmt.Fprintf(&b, "  planned scope (not yet built): %s\n", strings.Join(planned, ", "))
	}

	env := cp.Snapshot.Environment
	fmt.Fprintf(&b, "  environment: tier=%s os=%s arch=%s runtime=%s defaults=%s\n",
		firstNonEmpty(env.Tier, "unobserved"), firstNonEmpty(env.OS, "unobserved"),
		firstNonEmpty(env.Arch, "unobserved"), firstNonEmpty(env.Runtime, "unobserved"),
		firstNonEmpty(env.DefaultsDigest, "unobserved"))

	if pages := len(cp.MenuPages); pages > 0 {
		fmt.Fprintf(&b, "  menu pages (%d):\n", pages)
		for i, pg := range cp.MenuPages {
			nav := ""
			if pg.NavNext != "" {
				nav += " — " + pg.NavNext
			}
			if pg.NavPrev != "" {
				nav += " — " + pg.NavPrev
			}
			fmt.Fprintf(&b, "    page %d/%d: %s%s\n", i+1, pages, strings.Join(pg.Labels, " · "), nav)
		}
	}
	if len(cp.RecallTargets) > 0 {
		b.WriteString("  recall targets:\n")
		for _, t := range cp.RecallTargets {
			fmt.Fprintf(&b, "    - %s (phase %s, visit %s)\n", t.Label, t.PhaseID, t.VisitID)
		}
	}
	if cp.ReviewRetry != nil {
		fmt.Fprintf(&b, "  review retry: review %s attempt %s — resume %s · retry=%s skip=%s\n",
			cp.ReviewRetry.ReviewID, cp.ReviewRetry.AttemptID, cp.ReviewRetry.ResumeTarget,
			cp.ReviewRetry.RetryLabel, cp.ReviewRetry.SkipLabel)
	}
	if cp.ExtendAsk != nil {
		fmt.Fprintf(&b, "  extend ask (budget exhausted): %s / %s — delta %s\n",
			cp.ExtendAsk.ExtendLabel, cp.ExtendAsk.HoldLabel, budgetLimitsKey(cp.ExtendAsk.Delta))
	}
	b.WriteString("  model: " + DecisionAskInstruction(cp) + "\n")
	return b.String()
}

// DecisionAskInstruction is the one-line instruction the status view gives
// the model for the pending checkpoint — the only place the ask recipe is
// spelled out, so the checkpoint message itself carries no model-facing
// text.
func DecisionAskInstruction(cp CheckpointState) string {
	return "paste the checkpoint message (an explanation may precede it) and end the turn right here — the end-of-turn hook binds the display and hands back the exact question; raise it with ONE AskUserQuestion whose question carries nonce " + cp.Nonce +
		" and whose options equal page 1's labels verbatim (more:/back: navigate pages — never an option)."
}

// writeDetailConclusion prints a phase conclusion in full: every part, every
// item, embedded newlines indented — nothing capped, nothing dropped.
func writeDetailConclusion(b *strings.Builder, cp CheckpointState) {
	if cp.Conclusion == nil {
		if cp.Note != "" {
			writeDetailField(b, "summary (phase summary; no structured conclusion was submitted)", cp.Note, 4)
		} else {
			b.WriteString("    summary: 未提供\n")
		}
		for _, part := range []string{"evidence", "approach", "open questions", "impact"} {
			fmt.Fprintf(b, "    %s: 未提供\n", part)
		}
		return
	}
	c := cp.Conclusion
	writeDetailField(b, "summary", c.Summary, 4)
	writeDetailList(b, "evidence", c.Evidence)
	writeDetailField(b, approachLabel(cp.PhaseID), c.Approach, 4)
	writeDetailList(b, "open questions", c.OpenQuestions)
	writeDetailField(b, "impact", c.Impact, 4)
}

func writeDetailList(b *strings.Builder, name string, items []string) {
	if len(items) == 0 {
		fmt.Fprintf(b, "    %s: 未提供\n", name)
		return
	}
	fmt.Fprintf(b, "    %s (%d):\n", name, len(items))
	for _, it := range items {
		writeDetailField(b, "-", it, 6)
	}
}

// writeDetailField prints "label: value" at the given indent (a "-" label is
// a bullet: "- value"); an empty value reads 未提供, and embedded newlines
// keep the indent.
func writeDetailField(b *strings.Builder, label, value string, indent int) {
	pad := strings.Repeat(" ", indent)
	sep := ": "
	if label == "-" {
		sep = " "
	}
	if strings.TrimSpace(value) == "" {
		fmt.Fprintf(b, "%s%s%s未提供\n", pad, label, sep)
		return
	}
	fmt.Fprintf(b, "%s%s%s%s\n", pad, label, sep, strings.ReplaceAll(strings.TrimRight(value, "\n"), "\n", "\n"+pad+"  "))
}
