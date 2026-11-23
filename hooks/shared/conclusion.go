package shared

// conclusion.go — validation and normalization of a phase submission's
// structured conclusion (0.7.0 D1). The conclusion is what the checkpoint
// leads with: the incident review of 2026-11-23 (run df0ed2e6) showed five
// checkpoints whose render carried sixty lines of mechanism and never said
// what the bug was or how it was looked for. So the conclusion is a first-
// class, display-bound part of the submission.
//
// Two layers, one shape:
//
//   - ValidateSubmissionConclusion is the GATE's pre-effect check: the
//     outcome enum, and the parts a human cannot do without on a diagnosis.
//     A violation denies the submission before anything is consumed.
//   - NormalizedConclusion is the STORED form: total (never fails) and
//     conservative — an outcome outside the enum can never claim confirmed,
//     and open questions always downgrade a confirmed claim to unclear.

import (
	"fmt"
	"strings"
	"unicode"
)

// ValidateSubmissionConclusion returns the reason a phase submission's
// conclusion fields may not proceed ("" = valid). Only diagnosis forces the
// conclusion parts: the root cause (summary) and how it was established
// (approach). Every other phase may submit conclusion-free — the checkpoint
// then shows exactly what is missing instead of hiding it.
func ValidateSubmissionConclusion(sub *PhaseSubmission) string {
	switch strings.TrimSpace(sub.Outcome) {
	case "", ConclusionConfirmed, ConclusionFailed, ConclusionUnclear:
	default:
		return fmt.Sprintf("outcome %q is not one of confirmed, failed or unclear — state what the phase actually established", sub.Outcome)
	}
	if sub.PhaseID == "diagnosis" {
		var summary, approach string
		if sub.Conclusion != nil {
			summary, approach = sub.Conclusion.Summary, sub.Conclusion.Approach
		}
		if strings.TrimSpace(summary) == "" {
			return "diagnosis submission requires conclusion.summary — the root cause stated as a conclusion (what is broken and why), not a narrative of the search"
		}
		if strings.TrimSpace(approach) == "" {
			return "diagnosis submission requires conclusion.approach — how the root cause was established (the evidence path the user can re-check)"
		}
	}
	return ""
}

// NormalizedConclusion returns the stored (outcome, conclusion) of a
// submission. An omitted outcome is `unclear` (nothing was claimed); an
// outcome outside the enum degrades to `unclear` — a foreign value can never
// read as a confirmation; open questions force `unclear` even under a
// `confirmed` claim, because a conclusion with holes is not confirmed. A
// conclusion whose fields are all empty is stored as absent, so an empty
// `conclusion: {}` and a omitted one are the same content.
func NormalizedConclusion(outcome string, c *Conclusion) (string, *Conclusion) {
	out := strings.TrimSpace(outcome)
	switch out {
	case ConclusionConfirmed, ConclusionFailed:
	case "", ConclusionUnclear:
		out = ConclusionUnclear
	default:
		out = ConclusionUnclear
	}
	stored := cloneConclusion(c)
	if stored != nil && isEmptyConclusion(stored) {
		stored = nil
	}
	if stored != nil && len(stored.OpenQuestions) > 0 {
		out = ConclusionUnclear
	}
	return out, stored
}

// cloneConclusion deep-copies a conclusion (the slices are re-sliced, never
// aliased to the caller's submission).
func cloneConclusion(c *Conclusion) *Conclusion {
	if c == nil {
		return nil
	}
	return &Conclusion{
		Summary:       c.Summary,
		Evidence:      append([]string(nil), c.Evidence...),
		Approach:      c.Approach,
		OpenQuestions: append([]string(nil), c.OpenQuestions...),
		Impact:        c.Impact,
	}
}

func isEmptyConclusion(c *Conclusion) bool {
	any := func(items []string) bool {
		for _, it := range items {
			if !isBlankText(it) {
				return true
			}
		}
		return false
	}
	return isBlankText(c.Summary) && !any(c.Evidence) &&
		isBlankText(c.Approach) && !any(c.OpenQuestions) &&
		isBlankText(c.Impact)
}

// isBlankText reports whether s has no visible content: whitespace and
// invisible format / combining characters only. A zero-width space is not
// evidence and a blank item is not content — folding either into the digest
// would make the checkpoint undecidable after a rollback for no visible
// reason (ship review round 2).
func isBlankText(s string) bool {
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Mn, r) {
			continue
		}
		return false
	}
	return true
}
