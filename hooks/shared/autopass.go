package shared

// autopass.go — adaptive checkpoints (0.7.0 D3): the phases whose checkpoint
// passes WITHOUT a user decision, and the one-shot cue that keeps the run
// moving afterwards.
//
// Why: every checkpoint used to cost a dialog, including the ones nobody
// needs to judge. The incident review of 2026-11-23 (run df0ed2e6) showed
// the cost both ways — five dialogs for a diagnosis whose conclusion never
// changed, and four typed "continue"s to keep the run alive in between.
//
// What auto-passes (the policy table, mirrored by
// scorers/deterministic/user-gate-check.sh — change both together):
//
//	context, reproduce, verify, regression   — when the submission is
//	                                           confirmed with evidence
//	diagnosis, fix, and every other phase    — never
//
// An auto-pass is never a shortcut around a decision the user owns:
//
//   - the submission must be CONFIRMED, state a conclusion, and carry real
//     evidence — an unclear or failed self-report, blank items, or one
//     contradicted by a FAIL among its own mechanical checks, owes the human
//     gate; a checkpoint the user has already SEEN is theirs to decide;
//   - no budget question may ride the checkpoint (an extension offer) and
//     the budget must not read exhausted — passing would swallow the user's
//     budget decision and drop the run back into "exhausted, cannot work";
//   - the checkpoint record is kept and ACCEPTED (acceptance frozen, next
//     visit authorized) exactly as a CONTINUE would — only the actor differs.
//
// The trace records it as checkpoint.auto, never checkpoint.decided (whose
// tool_use_id must be a real AskUserQuestion call).

import (
	"fmt"
	"strings"
)

// AutoPassActor names the framework in the acceptance record of an
// auto-passed phase (FrozenRequirement.AcceptedBy / FrozenView.AcceptedBy) —
// never a tool_use_id, so no platform call is ever implied.
const AutoPassActor = "auto-pass"

// AutoPassablePhase reports whether a phase's checkpoint may pass without a
// user decision: the phases that gather context or confirm what a diagnosis
// or a fix already established under the user's eyes.
func AutoPassablePhase(phaseID string) bool {
	switch phaseID {
	case "context", "reproduce", "verify", "regression":
		return true
	}
	return false
}

// AutoPassBlocker returns why the checkpoint cannot pass without a user
// decision ("" = it can). Pure: reads only the given state.
// checkFailed says whether THIS submission's mechanical checks produced a
// FAIL — the gate ran them and reports the outcome; a confirmed claim that
// its own checks contradict is exactly the case the user must see.
func AutoPassBlocker(run *RunState, cp CheckpointState, checkFailed bool) string {
	switch {
	case cp.Kind != CheckpointPhase:
		return fmt.Sprintf("only a phase-submit checkpoint can auto-pass (kind %s)", cp.Kind)
	case !AutoPassablePhase(cp.PhaseID):
		return fmt.Sprintf("phase %s owes the human gate (only context, reproduce, verify and regression may auto-pass)", cp.PhaseID)
	case cp.Displayed:
		return "the checkpoint was already displayed — the user has seen it, so the decision is theirs"
	case cp.Outcome != ConclusionConfirmed:
		return fmt.Sprintf("the submission is not confirmed (outcome %q)", cp.Outcome)
	case cp.Conclusion == nil || strings.TrimSpace(cp.Conclusion.Summary) == "":
		return "a confirmed submission needs a stated conclusion to auto-pass"
	case !hasRealEvidence(cp.Conclusion.Evidence):
		return "a confirmed submission needs evidence to auto-pass (at least one non-blank item)"
	case len(cp.Conclusion.OpenQuestions) > 0:
		return "the conclusion lists open questions"
	case cp.ExtendAsk != nil:
		return "the checkpoint carries a budget extension question, and the user's budget decision cannot be skipped"
	case checkFailed:
		return "a FAIL among this submission's mechanical checks contradicts the confirmed claim"
	}
	if exhausted, why := run.BudgetExhausted(); exhausted {
		return "the budget is exhausted (" + why + ")"
	}
	if routeCompleteAfter(run, cp.PhaseID) {
		return "nothing follows on the route — an auto-pass would leave the run with no checkpoint to decide from"
	}
	return ""
}

// hasRealEvidence reports whether at least one evidence item says something.
// A blank item is not evidence: `evidence: [""]` is a confirmed claim with
// nothing behind it (ship review round 1).
func hasRealEvidence(items []string) bool {
	for _, it := range items {
		if !isBlankText(it) {
			return true
		}
	}
	return false
}

// routeCompleteAfter reports whether nothing would be owed on the route once
// phaseID settles — no shipped route ends with an auto-passable phase, so an
// auto-pass there would strand the run ACTIVE with no checkpoint to offer
// done or stop from. An unknown route counts as complete: nothing may be
// assumed about it.
func routeCompleteAfter(run *RunState, phaseID string) bool {
	seq, err := WorkflowPhases(run.Workflow)
	if err != nil {
		return true
	}
	for _, id := range seq {
		if id == phaseID {
			continue // the passing phase counts as settled
		}
		var last *PhaseState
		for i := range run.Phases {
			if run.Phases[i].ID == id {
				last = &run.Phases[i]
			}
		}
		if last == nil {
			return false
		}
		switch last.Outcome {
		case "accepted", "skipped":
		default:
			return false
		}
	}
	return true
}

// AutoPassCheckpoint accepts the CURRENT open phase checkpoint without a
// user decision and returns the run to ACTIVE. The checkpoint record is
// kept: it is marked decided with DecisionAutoPass (no tool_use_id — no
// platform answer exists), the phase is frozen into the acceptance view with
// the framework as the accepting actor, and the next visit's budget bucket
// is authorized exactly as a CONTINUE authorizes it. ContinueNudgeFor is set
// so the Stop hook can nudge the model on once (see ContinueNudgePending).
//
// Everything that owes the human gate is refused inside the commit and
// leaves the state untouched: a non-current checkpoint, a phase outside the
// policy, an unconfirmed or unevidenced submission, a FAIL among its checks,
// a riding extension question or an exhausted budget (AutoPassBlocker).
func (r *RunState) AutoPassCheckpoint(checkpointID string, checkFailed bool) error {
	return r.commit(func(s *RunState) error {
		if err := s.requireStatus(StatusWaitUser, "AutoPassCheckpoint"); err != nil {
			return err
		}
		cp := s.CurrentCheckpoint()
		if cp == nil || cp.ID != checkpointID {
			return fmt.Errorf("AutoPassCheckpoint: %q is not the open checkpoint", checkpointID)
		}
		if reason := AutoPassBlocker(s, *cp, checkFailed); reason != "" {
			return fmt.Errorf("AutoPassCheckpoint: %s (%s) keeps the human gate: %s", cp.ID, cp.PhaseID, reason)
		}
		cp.Decided = true
		cp.Decision = DecisionAutoPass
		cp.DecidedAt = nowUTC()
		s.decidePhase(cp, true)
		// I1 (adjudication §5): the pass authorizes the next visit —
		// pre-provider work charges the upcoming bucket; OpenPhase adopts.
		s.newVisitBucket()
		s.Status = StatusActive
		s.ContinueNudgeFor = cp.ID
		return nil
	})
}

// ClearContinueNudge consumes the one-shot continuation cue (the Stop hook
// calls it before nudging, so each continuation nudges at most once even if
// the block is lost). Harmless when nothing is set.
func (r *RunState) ClearContinueNudge() error {
	return r.commit(func(s *RunState) error {
		s.ContinueNudgeFor = ""
		return nil
	})
}

// ContinueNudgePending reports whether the run sits "right after" a
// decision that returned it to ACTIVE — the framework's auto-pass
// (checkpoint.auto) or the user's continue / skip (checkpoint.decided) —
// with nothing started since: the cue is set, and the trace's LAST
// lifecycle event (phase.* / checkpoint.*) is that decision, naming the
// cued checkpoint. Tool and check events do not count — after a submit or
// an answer there are always tool.result lines — and a later phase.started
// ends it (a recall opens its target phase at once). An unreadable trace is
// an error: the caller then nudges nothing (never a guess).
func ContinueNudgePending(run *RunState) (bool, error) {
	if run.ContinueNudgeFor == "" {
		return false, nil
	}
	evs, err := LoadRunEvents(run.RunID)
	if err != nil {
		return false, err
	}
	var last *TraceEvent
	for i := range evs {
		if isLifecycleEvent(evs[i].Type) {
			last = &evs[i]
		}
	}
	if last == nil || (last.Type != EventCheckpointAuto && last.Type != EventCheckpointDecided) {
		return false, nil
	}
	id, _ := last.Payload["checkpoint_id"].(string)
	return id == run.ContinueNudgeFor, nil
}

// isLifecycleEvent names the phase/checkpoint events that mark run
// progression (phase_submitted.rejected is a submission outcome, not one).
func isLifecycleEvent(typ string) bool {
	return strings.HasPrefix(typ, "phase.") || strings.HasPrefix(typ, "checkpoint.")
}

// NextExpectedPhase derives which phase the route allows to open next: walk
// the workflow sequence and, for each phase id, look at its LATEST visit
// (recall appends fresh visits); a phase is settled only when that visit's
// outcome is accepted or skipped. The first unsettled phase is the next
// expected one. A recalled phase (outcome "recalled", or no visit at all)
// re-enters ahead of any later phase. "" = every phase is settled, nothing
// may open. The gate's sequence-driven opening, the status view's
// "auto-passed → go straight on" line and the Stop hook's continuation nudge
// all name the next phase through this one walk.
func NextExpectedPhase(run *RunState) string {
	seq, err := WorkflowPhases(run.Workflow)
	if err != nil {
		return ""
	}
	lastVisit := func(id string) *PhaseState {
		var last *PhaseState
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

// acceptanceActor names who accepted on the checkpoint's behalf: the
// platform tool_use_id of the human decision, or AutoPassActor when the
// framework passed the checkpoint itself.
func (cp *CheckpointState) acceptanceActor() string {
	if cp.Decision == DecisionAutoPass {
		return AutoPassActor
	}
	return cp.DecisionToolUseID
}
