package main

// decision.go — the AskUserQuestion decision channel: turning the
// platform-injected tool_response of a question that carries the current
// checkpoint's nonce into a recorded decision (spec §1.4 用户裁决 + §4 决策
//绑定 + §7 checkpoint.decided / checkpoint.decision_rejected).
//
// The model cannot forge tool_response (the platform injects it), which is
// why this path is the ONLY source of run advancement — UserPromptSubmit and
// free text never are. The PreToolUse gate already enforced the
// nonce-in-question at WAIT_USER; everything here re-verifies (defense in
// depth). A validation failure is a REJECTED ATTEMPT — trace event + exit 0,
// never a hook error — and never writes a decision. The decision-time digest
// recheck and the per-kind legality matrix live in the reducer
// (shared.RecordDecision); this file only maps shapes, never re-implements
// kind guards.

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// recordDecisionAttempt runs the ruling-3 validation chain against the
// session's current checkpoint and records the decision when every step
// passes. Safe by construction against double-recording: the decision path is
// entered only with a WAIT_USER run and a current (hence undecided)
// checkpoint, so a successful RecordDecision here is always a fresh decision
// and the checkpoint.decided trace is appended exactly once per callback.
func recordDecisionAttempt(run *shared.RunState, cp *shared.CheckpointState, in *hookInput, errW io.Writer) int {
	reject := func(reason string) int {
		if err := appendTrace(run, in, shared.TraceEvent{
			Type: shared.EventCheckpointDecisionRejected,
			Payload: map[string]interface{}{
				"checkpoint_id": cp.ID,
				"reason":        reason,
			},
		}, errW); err != nil {
			return 2
		}
		return 0
	}

	// 1. Find the decision question: the tool_input question whose text
	// carries the CURRENT checkpoint's nonce (the gate enforced this before
	// the question was asked; re-verify).
	q, notFound := findDecisionQuestion(run, cp, in.ToolInput)
	if q == nil {
		return reject(notFound)
	}

	// 2. The answer object must be the platform-injected shape.
	resp, ok := in.ToolResponse.(map[string]interface{})
	if !ok {
		return reject("tool_response is not the platform-injected answer object")
	}

	// 3. A typed free-form response is content feedback, never advancement.
	if s, ok := resp[fieldResponse].(string); ok && s != "" {
		return reject(fmt.Sprintf("user typed a free-form response (%q) instead of choosing an option — free text never advances the run", truncate(s, 80)))
	}

	// 4. An auto-resolved dialog is not a human decision (spec §1.5
	// injection/timeout guard, runtime half).
	if _, ok := resp[fieldAFKTimeout]; ok {
		return reject("dialog auto-resolved by afk timeout (afkTimeoutMs present) — not a human decision")
	}

	// 5. The chosen answer must exist for exactly the decision question.
	answers, ok := resp[fieldAnswers].(map[string]interface{})
	if !ok {
		return reject("tool_response carries no answers object")
	}
	answer, ok := answers[q.text].(string)
	if !ok || answer == "" {
		return reject(fmt.Sprintf("no answer for the decision question (%q)", truncate(q.text, 80)))
	}

	// 6. The answer must EXACTLY match one offered option label; a
	// comma-joined multi-select is not one decision.
	label := matchOfferedLabel(q.options, answer)
	if label == "" {
		if n := countNonEmpty(strings.Split(answer, ",")); n > 1 {
			return reject(fmt.Sprintf("multi-select answer (%q) on the decision question — pick exactly one option", truncate(answer, 80)))
		}
		return reject(fmt.Sprintf("answer %q is not an offered option label of the decision question", truncate(answer, 80)))
	}

	// 7. Map the label by leading keyword (lowercased first word,
	// punctuation stripped): continue/skip/done/recall/stop/review/accept.
	d, ok := decisionForKeyword(label)
	if !ok {
		return reject(fmt.Sprintf("option %q leads with a keyword that maps to no decision", truncate(label, 80)))
	}

	// 8. Record through the reducer: display binding, decision-time digest
	// recheck and kind legality live there. Its errors are rejections, not
	// hook errors.
	if err := run.RecordDecision(cp.ID, d, in.ToolUseID); err != nil {
		return reject(err.Error())
	}
	if err := appendTrace(run, in, shared.TraceEvent{
		Type: shared.EventCheckpointDecided,
		Payload: map[string]interface{}{
			"checkpoint_id": cp.ID,
			"tool_use_id":   in.ToolUseID,
		},
	}, errW); err != nil {
		return 2
	}
	// The change loop's single-point close (T10): a successful
	// ACCEPT_REQUIREMENT_CHANGE resumes the run in place — record the resume
	// AFTER the decision it belongs to (change.opened → change.delta_applied
	// → checkpoint.decided → change.resumed).
	if d == shared.DecisionAcceptRequirementChange {
		if err := appendTrace(run, in, shared.TraceEvent{
			Type: shared.EventChangeResumed,
			Payload: map[string]interface{}{
				"checkpoint_id": cp.ID,
			},
		}, errW); err != nil {
			return 2
		}
	}
	return 0
}

// decisionQuestion is the tool_input question bound to the current
// checkpoint: its full text (the answers map key) and offered options.
type decisionQuestion struct {
	text    string
	options []interface{}
}

// findDecisionQuestion locates the question whose text contains the current
// checkpoint's nonce. When none does, it returns a reject reason that
// distinguishes a wrong-checkpoint binding (some question carries ANOTHER
// checkpoint's nonce) from a payload carrying no nonce at all.
func findDecisionQuestion(run *shared.RunState, cp *shared.CheckpointState, toolInput map[string]interface{}) (*decisionQuestion, string) {
	qs, _ := toolInput[fieldQuestions].([]interface{})
	for _, raw := range qs {
		m, _ := raw.(map[string]interface{})
		text, _ := m[fieldQuestion].(string)
		if text != "" && strings.Contains(text, cp.Nonce) {
			options, _ := m[fieldOptions].([]interface{})
			return &decisionQuestion{text: text, options: options}, ""
		}
	}
	for _, raw := range qs {
		m, _ := raw.(map[string]interface{})
		text, _ := m[fieldQuestion].(string)
		for i := range run.Checkpoints {
			other := &run.Checkpoints[i]
			if other.ID != cp.ID && strings.Contains(text, other.Nonce) {
				return nil, fmt.Sprintf("question carries the nonce of %s (%s), not the current checkpoint %s (%s)", other.ID, other.Nonce, cp.ID, cp.Nonce)
			}
		}
	}
	return nil, fmt.Sprintf("no question carries the current checkpoint %s nonce %s — the answer cannot bind to the checkpoint", cp.ID, cp.Nonce)
}

// matchOfferedLabel returns the offered option label the answer exactly
// equals, or "" when it matches none.
func matchOfferedLabel(options []interface{}, answer string) string {
	for _, raw := range options {
		m, _ := raw.(map[string]interface{})
		label, _ := m[fieldLabel].(string)
		if label != "" && label == answer {
			return label
		}
	}
	return ""
}

// decisionForKeyword maps an offered label to its decision by leading
// keyword: the lowercased first word with punctuation stripped. Labels must
// LEAD with the verb ("Continue: …", "Skip: …", "Review: …"); a decorative
// prefix such as "[v]" does not map.
func decisionForKeyword(label string) (shared.Decision, bool) {
	fields := strings.Fields(label)
	if len(fields) == 0 {
		return "", false
	}
	switch normalizeKeyword(fields[0]) {
	case "continue":
		return shared.DecisionContinue, true
	case "skip":
		return shared.DecisionSkip, true
	case "done":
		return shared.DecisionDone, true
	case "recall":
		return shared.DecisionRecall, true
	case "stop":
		return shared.DecisionStop, true
	case "review":
		return shared.DecisionReviewOnDemand, true
	case "accept":
		return shared.DecisionAcceptRequirementChange, true
	}
	return "", false
}

// normalizeKeyword lowercases a word and strips punctuation ("Continue:" →
// "continue", "Done?" → "done").
func normalizeKeyword(w string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(w) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func countNonEmpty(parts []string) int {
	n := 0
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			n++
		}
	}
	return n
}

// truncate keeps a trace payload reason bounded (cosmetic byte cut).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
