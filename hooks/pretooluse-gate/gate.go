package main

// gate.go — the pure decision core: decide() inspects the parsed payload plus
// the loaded run state and returns allow/deny with the side-effect plan;
// apply() performs the I/O (reducer transitions, counters, trace appends).
// Nothing in decide touches the filesystem, so tests can drive the matrix
// without process-global state beyond the env-overridden roots.

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

const skillPhaseSubmit = "summoner:phase-submit"

// decision is decide()'s verdict: allow with an effect plan, or deny with a
// human-readable reason (the exit-2 stderr text). A deny may also carry a
// submission rejection to trace: phase_submitted.rejected is the record of
// EVERY submission-path failure during an active run (ruling 6 + review I8).
type decision struct {
	allow  bool
	reason string
	eff    *effects      // nil = allowed, nothing to mutate
	reject *submitReject // non-nil: trace phase_submitted.rejected on deny
}

// submitReject marks a denial that must persist phase_submitted.rejected.
type submitReject struct {
	phaseID string // "" when the payload never parsed far enough to carry one
}

// effects is the ordered side-effect plan apply() executes. It never mixes:
// a denied call carries no effects (its only record is the exit-2 stderr —
// except phase-submit reducer rejections, which also trace
// phase_submitted.rejected per ruling 6).
type effects struct {
	openPhase     string                  // OpenPhase(id): Skill candidate (ruling 3) or phase-submit fallback
	submit        *shared.PhaseSubmission // SubmitPhase payload (ruling 6)
	startReview   string                  // MarkReviewRunning(reviewID) — reviewer delegation
	delegation    string                  // subagent_type — Counters.Delegations++ + delegation.started
	delegateRevID string                  // review_id bound to the delegation, when reviewer
}

func deny(reason string) decision { return decision{allow: false, reason: reason} }

// denySubmit is a deny that also records phase_submitted.rejected — every
// submission-path failure during an active run leaves that trace (I8).
func denySubmit(phaseID, reason string) decision {
	return decision{allow: false, reason: reason, reject: &submitReject{phaseID: phaseID}}
}

func allow(eff *effects) decision { return decision{allow: true, eff: eff} }

// decide routes one tool call through the gate rules (rulings 3-9).
func decide(in *gateInput, run *shared.RunState) decision {
	skill := stringField(in.ToolInput, "skill")
	isSubmit := in.ToolName == "Skill" && skill == skillPhaseSubmit

	// Budget first (ruling 9), with one deliberate exception: the
	// control-plane verbs that carry an exhausted run to its checkpoint,
	// where the user adds budget or replans (spec §5 预算与终止), stay
	// usable — summoner:phase-submit always, and AskUserQuestion only
	// while WAIT_USER (the nonce-gated decision channel; review I5:
	// ordinary ACTIVE questions are budget-gated like everything else).
	if !(isSubmit || (in.ToolName == "AskUserQuestion" && run.Status == shared.StatusWaitUser)) {
		if exhausted, why := run.BudgetExhausted(); exhausted {
			return deny(fmt.Sprintf(
				"Summoner gate: %s — remaining budget is zero (余量为零). Only the user can add budget or replan: submit the phase via summoner:phase-submit so the checkpoint can present the exhausted budget.",
				why))
		}
	}

	switch in.ToolName {
	case "Skill":
		return decideSkill(in, run, skill)
	case "Edit", "Write", "NotebookEdit":
		return decideWrite(run, in.ToolName)
	case "Bash":
		if isWriteClassCommand(stringField(in.ToolInput, "command")) {
			return decideWrite(run, "Bash")
		}
		return allow(nil) // read-class Bash: observed only
	case "Agent":
		return decideAgent(in, run)
	case "AskUserQuestion":
		return decideAskUser(in, run)
	default:
		// Unknown/unmodeled tools (Read, Grep, …) are observed, not gated.
		return allow(nil)
	}
}

// decideSkill — ruling 6 (phase-submit + framework verbs) and ruling 3
// (sequence-driven phase opening for non-summoner skills).
func decideSkill(in *gateInput, run *shared.RunState, skill string) decision {
	if skill == "" {
		return deny("Summoner gate: Skill call without a skill name — cannot be verified, denying (fail-closed).")
	}

	if skill == skillPhaseSubmit {
		args, ok := in.ToolInput["args"].(string)
		if !ok {
			return denySubmit("", "Summoner gate: summoner:phase-submit requires args — a JSON string carrying the phase submission (phase_id, artifacts, criteria, check refs).")
		}
		var sub shared.PhaseSubmission
		if err := json.Unmarshal([]byte(args), &sub); err != nil {
			return denySubmit("", fmt.Sprintf("Summoner gate: summoner:phase-submit args is not valid JSON: %v", err))
		}
		if sub.PhaseID == "" {
			return denySubmit("", "Summoner gate: summoner:phase-submit payload requires phase_id.")
		}
		eff := &effects{submit: &sub}
		// Ruling 3 fallback: with no open phase, auto-open the payload's
		// phase iff it is the next expected one (protects skill-less phases,
		// e.g. a read-only P0 with no Skill call). Only attempted where
		// OpenPhase is legal (ACTIVE, no change in flight) so a rejection
		// always comes from SubmitPhase — and is traced as
		// phase_submitted.rejected. Out-of-order ids get no auto-open.
		if run.Status == shared.StatusActive && run.Change == nil && openPhaseOf(run) == nil {
			if next := nextExpectedPhase(run); next != "" && next == sub.PhaseID {
				eff.openPhase = sub.PhaseID
			}
		}
		return allow(eff)
	}

	if strings.HasPrefix(skill, "summoner:") {
		// Framework verbs other than phase-submit: recorded, never phase-opening.
		return allow(nil)
	}

	// Non-summoner skill: a phase-opening candidate when the sequence allows
	// an open (ACTIVE, no change in flight, nothing open, nothing owed).
	eff := &effects{}
	if run.Status == shared.StatusActive && run.Change == nil &&
		openPhaseOf(run) == nil && run.CurrentCheckpoint() == nil {
		if next := nextExpectedPhase(run); next != "" {
			eff.openPhase = next
		}
	}
	return allow(eff)
}

// decideWrite — the write-eligibility matrix (ruling 4). POST_GAME_PENDING
// allows writes before anything else is considered (review I3: the emergency
// stop journal is writable even when no diagnosis was ever accepted); then
// WAIT_USER/REVIEW_PENDING/OUTCOME_UNKNOWN deny; ACTIVE/CHANGE_OPEN face only
// the diagnosis iron law. CLOSED never reaches here (not a live run).
func decideWrite(run *shared.RunState, toolName string) decision {
	switch run.Status {
	case shared.StatusPostGamePending:
		return allow(nil) // journal persistence precedes CLOSED
	case shared.StatusWaitUser:
		cp := run.CurrentCheckpoint()
		where := "its checkpoint"
		if cp != nil {
			where = "checkpoint " + cp.ID
		}
		return deny(fmt.Sprintf(
			"Summoner gate: %s is blocked — the run waits for a user decision at %s (WAIT_USER). Present the checkpoint and wait for the user.", toolName, where))
	case shared.StatusReviewPending:
		return deny(fmt.Sprintf(
			"Summoner gate: %s is blocked while a review is in flight (REVIEW_PENDING) — artifacts must not change mid-review.", toolName))
	case shared.StatusOutcomeUnknown:
		return deny(fmt.Sprintf(
			"Summoner gate: %s is blocked until the unresolved tool outcome is probed (OUTCOME_UNKNOWN) — mutating actions are never auto-replayed.", toolName))
	}
	if run.Workflow == "fix" || run.Workflow == "debug" {
		if !hasAcceptedPhase(run, "diagnosis") {
			return deny(fmt.Sprintf(
				"Summoner gate: %s is blocked — workflow %s enforces diagnosis-before-code: writes are denied until the diagnosis phase is accepted at its checkpoint (Phase 1 iron law; 诊断未被接受).", toolName, run.Workflow))
		}
	}
	return allow(nil)
}

// decideAgent — ruling 7: typed delegation only (reviewer|worker), within
// budget; a reviewer must be bound to a PREPARED review whose id the prompt
// carries, and allowing it starts that review.
func decideAgent(in *gateInput, run *shared.RunState) decision {
	typ := stringField(in.ToolInput, "subagent_type")
	if typ == "" {
		return deny("Summoner gate: Agent call without subagent_type — the gate cannot verify the delegation (fail-closed).")
	}
	if typ != "reviewer" && typ != "worker" {
		return deny(fmt.Sprintf(
			"Summoner gate: Agent subagent_type %q is not allowed — only reviewer or worker (fork/general-purpose delegation is refused).", typ))
	}
	// Same semantics as BudgetExhausted: a zero limit is unlimited.
	if run.Limits.Delegations > 0 && run.Counters.Delegations >= run.Limits.Delegations {
		return deny(fmt.Sprintf(
			"Summoner gate: delegation budget exhausted (%d/%d) — only the user can add budget.",
			run.Counters.Delegations, run.Limits.Delegations))
	}
	eff := &effects{delegation: typ}
	if typ == "reviewer" {
		prompt := stringField(in.ToolInput, "prompt")
		revID := ""
		for i := range run.Reviews {
			rv := run.Reviews[i]
			if rv.Status == shared.ReviewPrepared && strings.Contains(prompt, rv.ReviewID) {
				revID = rv.ReviewID
				break
			}
		}
		if revID == "" {
			return deny("Summoner gate: reviewer delegation requires a PREPARED review whose review_id appears in the prompt — no match (the reviewer must run a registered envelope, not ad-hoc work).")
		}
		eff.startReview = revID
		eff.delegateRevID = revID
	}
	return allow(eff)
}

// decideAskUser — ruling 8: gated only while WAIT_USER, where the question
// text must carry the current checkpoint's nonce so the platform-injected
// tool_response can bind the coming answer to that checkpoint. Elsewhere it
// is ordinary Q&A: it advances nothing (PostToolUse records decisions only
// for pending checkpoints).
func decideAskUser(in *gateInput, run *shared.RunState) decision {
	if run.Status != shared.StatusWaitUser {
		return allow(nil)
	}
	cp := run.CurrentCheckpoint()
	if cp == nil {
		return deny("Summoner gate: WAIT_USER with no open checkpoint — run state is inconsistent, denying (fail-closed).")
	}
	qs, _ := in.ToolInput["questions"].([]interface{})
	for _, q := range qs {
		m, _ := q.(map[string]interface{})
		if strings.Contains(stringField(m, "question"), cp.Nonce) {
			return allow(nil)
		}
	}
	return deny(fmt.Sprintf(
		"Summoner gate: AskUserQuestion while WAIT_USER must carry the current checkpoint's nonce in a question text (checkpoint %s, nonce %s) — otherwise the answer cannot bind to the checkpoint.",
		cp.ID, cp.Nonce))
}

// apply executes the effect plan. Two orderings, by path (review I6/I8):
//
//   - submit path (summoner:phase-submit): the reducer runs FIRST — its
//     rejections consume nothing and must leave no tool.started, only
//     phase_submitted.rejected. On success the intent record (tool.started)
//     and result events (phase.started/phase.closed/checkpoint.opened) are
//     appended, then counters.
//
//   - dispatch path (everything else): intent events (tool.started,
//     delegation.started) are appended BEFORE any mutation — a trace failure
//     denies with nothing consumed (no review started, no counters spent),
//     so a retry after repair succeeds. A failure AFTER mutations began
//     (reducer Save, counter Save) denies with the mutation persisted:
//     dangling intent events are the correct OUTCOME_UNKNOWN posture.
func apply(run *shared.RunState, in *gateInput, eff *effects, errW io.Writer) error {
	appendAll := func(events []shared.TraceEvent) error {
		for _, ev := range events {
			ev.Source = gateSource
			ev.ToolUseID = in.ToolUseID
			if err := shared.AppendEvent(run.RunID, in.SessionID, ev); err != nil {
				fmt.Fprintf(errW, "Summoner gate: trace append (%s) failed — denying (fail-closed): %v\n", ev.Type, err)
				return err
			}
		}
		return nil
	}
	rejectSubmit := func(phaseID string, err error) error {
		payload := map[string]interface{}{"reason": err.Error()}
		if phaseID != "" {
			payload["phase_id"] = phaseID
		}
		if terr := shared.AppendEvent(run.RunID, run.SessionID, shared.TraceEvent{
			Type:      shared.EventPhaseSubmittedRejected,
			Source:    gateSource,
			ToolUseID: in.ToolUseID,
			Payload:   payload,
		}); terr != nil {
			fmt.Fprintf(errW, "Summoner gate: phase_submitted.rejected trace append also failed: %v\n", terr)
		}
		fmt.Fprintf(errW, "Summoner gate: summoner:phase-submit rejected — %v\n", err)
		return err
	}

	if eff != nil && eff.submit != nil {
		// --- submit path: reducer first, rejections un-intended (I8) ---
		if eff.openPhase != "" {
			if err := run.OpenPhase(eff.openPhase); err != nil {
				return rejectSubmit(eff.openPhase, err)
			}
		}
		cp, err := run.SubmitPhase(*eff.submit)
		if err != nil {
			return rejectSubmit(eff.submit.PhaseID, err)
		}
		events := []shared.TraceEvent{{
			Type:    shared.EventToolStarted,
			Payload: map[string]interface{}{"tool_name": in.ToolName},
		}}
		if eff.openPhase != "" {
			events = append(events, shared.TraceEvent{
				Type:    shared.EventPhaseStarted,
				Payload: map[string]interface{}{"phase_id": eff.openPhase},
			})
		}
		events = append(events,
			shared.TraceEvent{
				Type:    shared.EventPhaseClosed,
				Payload: map[string]interface{}{"phase_id": eff.submit.PhaseID},
			},
			shared.TraceEvent{
				Type: shared.EventCheckpointOpened,
				Payload: map[string]interface{}{
					"checkpoint_id": cp.ID,
					"phase_id":      eff.submit.PhaseID,
					"nonce":         cp.Nonce,
				},
			})
		if err := appendAll(events); err != nil {
			return err // state already moved (WAIT_USER) — deny fail-closed
		}
		run.CountToolCall()
		if err := run.Save(); err != nil {
			fmt.Fprintf(errW, "Summoner gate: persisting run state failed — denying (fail-closed): %v\n", err)
			return err
		}
		return nil
	}

	// --- dispatch path: intents before any mutation (I6) ---
	events := []shared.TraceEvent{{
		Type:    shared.EventToolStarted,
		Payload: map[string]interface{}{"tool_name": in.ToolName},
	}}
	if eff != nil && eff.delegation != "" {
		payload := map[string]interface{}{"subagent_type": eff.delegation}
		if eff.delegateRevID != "" {
			payload["review_id"] = eff.delegateRevID
		}
		events = append(events, shared.TraceEvent{
			Type:    shared.EventDelegationStarted,
			Payload: payload,
		})
	}
	if err := appendAll(events); err != nil {
		return err // nothing mutated — retry after repair is clean
	}
	if eff != nil && eff.openPhase != "" {
		if err := run.OpenPhase(eff.openPhase); err != nil {
			fmt.Fprintf(errW, "Summoner gate: OpenPhase(%s) failed — denying: %v\n", eff.openPhase, err)
			return err
		}
		if err := appendAll([]shared.TraceEvent{{
			Type:    shared.EventPhaseStarted,
			Payload: map[string]interface{}{"phase_id": eff.openPhase},
		}}); err != nil {
			return err
		}
	}
	if eff != nil && eff.startReview != "" {
		if err := run.MarkReviewRunning(eff.startReview); err != nil {
			fmt.Fprintf(errW, "Summoner gate: MarkReviewRunning(%s) failed — denying: %v\n", eff.startReview, err)
			return err
		}
	}
	if eff != nil && eff.delegation != "" {
		run.Counters.Delegations++
	}
	run.CountToolCall()
	if err := run.Save(); err != nil {
		fmt.Fprintf(errW, "Summoner gate: persisting run state failed — denying (fail-closed): %v\n", err)
		return err
	}
	return nil
}

// --- run-state read helpers (pure) -------------------------------------------

func stringField(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// validateShape enforces the known tool_input shapes before classification
// (review I7): during an active run, a malformed payload for a KNOWN tool is
// a fail-closed deny, never an observe-and-allow. Unknown tool names keep the
// observe-and-allow policy — the gate does not model their inputs.
func validateShape(in *gateInput) error {
	if in.ToolName == "" {
		return fmt.Errorf("tool_name is empty")
	}
	switch in.ToolName {
	case "Bash", "Edit", "Write", "NotebookEdit", "Agent", "Skill", "AskUserQuestion":
	default:
		return nil // unknown tool name: observe and allow
	}
	if in.ToolInput == nil {
		return fmt.Errorf("tool_input for %s must be an object", in.ToolName)
	}
	needString := func(key string) error {
		if _, ok := in.ToolInput[key].(string); !ok {
			return fmt.Errorf("%s requires a string %q", in.ToolName, key)
		}
		return nil
	}
	switch in.ToolName {
	case "Bash":
		return needString("command")
	case "Edit", "Write":
		return needString("file_path")
	case "NotebookEdit":
		return needString("notebook_path")
	case "Agent":
		if err := needString("subagent_type"); err != nil {
			return err
		}
		return needString("prompt")
	case "Skill":
		return needString("skill")
	case "AskUserQuestion":
		qs, ok := in.ToolInput["questions"].([]interface{})
		if !ok {
			return fmt.Errorf("AskUserQuestion requires a questions array")
		}
		for i, q := range qs {
			m, ok := q.(map[string]interface{})
			if !ok {
				return fmt.Errorf("AskUserQuestion questions[%d] must be an object", i)
			}
			if _, ok := m["question"].(string); !ok {
				return fmt.Errorf("AskUserQuestion questions[%d] requires a string question", i)
			}
		}
	}
	return nil
}

func openPhaseOf(run *shared.RunState) *shared.PhaseState {
	for i := range run.Phases {
		if run.Phases[i].Open {
			return &run.Phases[i]
		}
	}
	return nil
}

func hasAcceptedPhase(run *shared.RunState, id string) bool {
	for _, p := range run.Phases {
		if p.ID == id && p.Outcome == "accepted" {
			return true
		}
	}
	return false
}

// nextExpectedPhase derives which phase the sequence allows to open next
// (ruling 3, review I2): walk the workflow sequence and, for each phase id,
// look at its LATEST PhaseState (last entry per id — recall appends fresh
// visits); a phase is settled only when that visit's Outcome is accepted or
// skipped. The first unsettled phase is the next expected one. A recalled
// phase (Outcome "recalled", or no visit at all) re-enters ahead of any
// later phase. "" = every phase is settled, nothing may open.
func nextExpectedPhase(run *shared.RunState) string {
	seq, err := shared.WorkflowPhases(run.Workflow)
	if err != nil {
		return ""
	}
	lastVisit := func(id string) *shared.PhaseState {
		var last *shared.PhaseState
		for i := range run.Phases {
			if run.Phases[i].ID == id {
				last = &run.Phases[i]
			}
		}
		return last
	}
	for _, id := range seq {
		visit := lastVisit(id)
		if visit == nil {
			return id // never visited — unsettled
		}
		switch visit.Outcome {
		case "accepted", "skipped":
			continue // settled; sequencing moves on
		default:
			return id // open, undecided or recalled — the phase owes work
		}
	}
	return ""
}
