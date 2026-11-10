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
//
// T18: the call may also carry nonce-free budget/extend ride-along questions
// (the run's first checkpoint before settlement, and any checkpoint opened
// while the budget was exhausted). Their validation precedes EVERY recording
// effect; when the decision completes, settlement and decision persist in ONE
// commit (RecordDecisionWithBudget — a failed Save applies neither); when the
// decision does not complete, a valid budget answer still records through its
// standalone setter (the T17 partial-answer semantics), and the post-hoc rule
// (below) rejects a CONTINUE/SKIP at the unsettled first checkpoint with
// NOTHING recorded.
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

	// 0. Fix-round I1: a callback whose tool_use_id already concluded a
	// decision is a re-delivery — no-op it BEFORE any ride-along effect
	// (drops and budget included): the whole call is spent, so no part of it
	// may record again. Silent, like RecordDecision's idempotent replay:
	// nothing is recorded, nothing is traced.
	if in.ToolUseID != "" {
		for i := range run.Checkpoints {
			if run.Checkpoints[i].DecisionToolUseID == in.ToolUseID {
				return 0
			}
		}
	}

	// 1. Find the decision question: the tool_input question whose text
	// carries the CURRENT checkpoint's nonce (the gate enforced this before
	// the question was asked; re-verify). T17: its rejection is DEFERRED to
	// step 5 — the drop ride-along channel records answered drops even in a
	// callback the gate would never have admitted (partial-answer semantics).
	q, notFound := findDecisionQuestion(run, cp, in.ToolInput)
	if q != nil {
		notFound = ""
	}

	// 1.5 Fix-round m2: a MULTI-NONCE call fails closed HERE, before any
	// ride-along effect — two questions carrying the current nonce make the
	// answers map ambiguous (which question binds the decision?), so the
	// whole callback is rejected with NOTHING recorded, drops included (T16
	// discipline; the drop channel is the decision channel's ride-along,
	// never a side door).
	if n := len(nonceQuestions(cp, in.ToolInput)); n > 1 {
		return reject(fmt.Sprintf("call carries %d questions with the current checkpoint %s nonce %s — exactly ONE decision question per call (fail-closed, nothing recorded)", n, cp.ID, cp.Nonce))
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

	// 4.2 The T18 budget ride-along channel: validate every budget/extend
	// question against the checkpoint's PERSISTED asks (shape, exact pointer
	// labels, nonce-free, one per kind) BEFORE anything records — a malformed
	// or stale budget question fails the whole callback and records nothing.
	qs, _ := in.ToolInput[fieldQuestions].([]interface{})
	budgetRides, problem := shared.ParseBudgetRideAlongs(cp, qs)
	if problem != "" {
		return reject(problem)
	}

	// 4.5 The T17 drop ride-along channel: validate every drop question and
	// drop ANSWER first (all-or-nothing — a malformed drop question fails the
	// whole callback and records nothing), then record the answered drops
	// BEFORE the decision: a CONTINUE that re-accepts a dropped criterion
	// clears its ledger entry (decidePhase → clearDroppedCriteria in the
	// reducer), which requires the drop to land first. keep is the default
	// and records nothing; an unanswered drop question records nothing.
	drops, problem := parseDropQuestions(run, cp, in.ToolInput)
	if problem != "" {
		return reject(problem)
	}
	answers, hasAnswers := resp[fieldAnswers].(map[string]interface{})
	if hasAnswers {
		if problem := validateDropAnswers(drops, answers); problem != "" {
			return reject(problem)
		}
	}

	// 4.6 Derive the budget settlement from the ANSWERS against the persisted
	// asks: an answered budget question must choose one of its two offered
	// pointer labels (anything else rejects the whole callback), and the
	// applied VALUE comes only from the persisted proposal/delta — never from
	// the label text (panel TQ3). Unanswered budget questions settle nothing
	// (partial answers ride later calls).
	var settle shared.BudgetSettlement
	settling := false
	for _, ride := range budgetRides {
		ans, _ := answers[ride.Text].(string)
		if ans == "" {
			continue // unanswered: nothing to settle for this kind
		}
		if ride.Kind == "budget" {
			switch ans {
			case ride.Labels[0]: // confirm the displayed proposal
				lim := cp.BudgetAsk.Proposal.Limits
				settle.Confirm = &lim
				settling = true
			case ride.Labels[1]: // unlimited
				lim := shared.BudgetLimits{}
				settle.Confirm = &lim
				settling = true
			default:
				return reject(fmt.Sprintf("budget question answer %q is not one of its offered pointer labels (%q / %q)", truncate(ans, 80), ride.Labels[0], ride.Labels[1]))
			}
		} else {
			switch ans {
			case ride.Labels[0]: // extend by the persisted delta
				settle.Extend = true
				settling = true
			case ride.Labels[1]: // hold: answered, no effect
			default:
				return reject(fmt.Sprintf("extend question answer %q is not one of its offered pointer labels (%q / %q)", truncate(ans, 80), ride.Labels[0], ride.Labels[1]))
			}
		}
	}

	// 4.7 Pre-resolve the decision (pure — nothing recorded) so the post-hoc
	// rule can fire BEFORE any ride-along effect.
	var dPre shared.Decision
	if q != nil {
		if answer, ok := answers[q.text].(string); ok && answer != "" {
			if label := matchOfferedLabel(q.options, answer); label != "" {
				if d, ok := decisionForKeyword(label); ok {
					dPre = d
				}
			}
		}
	}

	// 4.8 T18 post-hoc rule: at the run's unsettled FIRST checkpoint (a
	// persisted BudgetAsk the run never settled), a CONTINUE or SKIP decision
	// without a same-callback budget answer is rejected with NOTHING recorded
	// — the run-start budget settles with the same human answer that advances
	// the run (spec §5 默认值由用户在运行起点确认). STOP/DONE/RECALL/REVIEW are
	// exempt: terminal exits and replans are never budget hostages.
	if (dPre == shared.DecisionContinue || dPre == shared.DecisionSkip) &&
		cp.BudgetAsk != nil && !run.BudgetConfirmed && settle.Confirm == nil {
		return reject(fmt.Sprintf(
			"%s at checkpoint %s carries no budget answer — the run-start budget is unsettled: ask the budget question in the SAME call (options %q / %q, nonce-free ride-along); nothing was recorded",
			dPre, cp.ID, cp.BudgetAsk.ConfirmLabel, cp.BudgetAsk.UnlimitedLabel))
	}

	// 4.9 Record the answered drops (the T17 ordering: drops land before the
	// decision so an accepting CONTINUE clears them).
	for _, dq := range drops {
		ans, _ := answers[dq.text].(string)
		if ans == "" || ans == dq.keepLabel {
			continue // unanswered or keep (the default): nothing to record
		}
		// ans == dq.dropLabel — validated above.
		recorded, err := run.RecordCriteriaDrop(cp.ID, dq.id, in.ToolUseID)
		if err != nil {
			return reject(err.Error())
		}
		if !recorded {
			continue // idempotent replay: the observation is already on disk
		}
		if err := appendTrace(run, in, shared.TraceEvent{
			Type: shared.EventCriterionDropped,
			Payload: map[string]interface{}{
				"checkpoint_id": cp.ID,
				"criterion_id":  dq.id,
			},
		}, errW); err != nil {
			return 2
		}
	}

	// settleAndReject closes a callback whose budget observation is valid but
	// whose decision does not complete: the settlement records through its
	// standalone setter (T17 partial-answer semantics — the checkpoint stays
	// open and the decision is rejected), then the decision rejection traces.
	// budget.confirmed / budget.extended trace only when the reducer newly
	// applied something (replays and already-confirmed no-ops are silent).
	settleAndReject := func(reason string) int {
		if !settling {
			return reject(reason)
		}
		settle.ToolUseID = in.ToolUseID
		beforeConfirmed := run.BudgetConfirmed
		beforeExts := len(run.BudgetExtensions)
		if settle.Confirm != nil {
			if _, err := run.SetBudgetLimits(*settle.Confirm, in.ToolUseID); err != nil {
				return reject(err.Error())
			}
			if !beforeConfirmed && run.BudgetConfirmed {
				if err := appendTrace(run, in, shared.TraceEvent{
					Type: shared.EventBudgetConfirmed,
					Payload: map[string]interface{}{
						"checkpoint_id": cp.ID,
						"limits":        budgetLimitsPayload(*settle.Confirm),
					},
				}, errW); err != nil {
					return 2
				}
			}
		}
		if settle.Extend {
			if _, err := run.ExtendBudget(in.ToolUseID); err != nil {
				return reject(err.Error())
			}
			if len(run.BudgetExtensions) > beforeExts {
				if err := appendTrace(run, in, shared.TraceEvent{
					Type: shared.EventBudgetExtended,
					Payload: map[string]interface{}{
						"checkpoint_id": cp.ID,
						"delta":         budgetLimitsPayload(run.BudgetExtensions[len(run.BudgetExtensions)-1].Delta),
					},
				}, errW); err != nil {
					return 2
				}
			}
		}
		return reject(reason)
	}

	// 5. The chosen answer must exist for exactly the decision question —
	// the deferred step-1 rejection lands here (after the drop channel), so a
	// drop-only callback records its drops and still refuses the decision.
	if q == nil {
		return settleAndReject(notFound)
	}
	answer, ok := answers[q.text].(string)
	if !ok || answer == "" {
		return settleAndReject(fmt.Sprintf("no answer for the decision question (%q)", truncate(q.text, 80)))
	}

	// 6. The answer must EXACTLY match one offered option label; a
	// comma-joined multi-select is not one decision.
	label := matchOfferedLabel(q.options, answer)
	if label == "" {
		if n := countNonEmpty(strings.Split(answer, ",")); n > 1 {
			return settleAndReject(fmt.Sprintf("multi-select answer (%q) on the decision question — pick exactly one option", truncate(answer, 80)))
		}
		return settleAndReject(fmt.Sprintf("answer %q is not an offered option label of the decision question", truncate(answer, 80)))
	}

	// 7. Map the label by leading keyword (lowercased first word,
	// punctuation stripped): continue/skip/done/recall/stop/review/accept.
	d, ok := decisionForKeyword(label)
	if !ok {
		return settleAndReject(fmt.Sprintf("option %q leads with a keyword that maps to no decision", truncate(label, 80)))
	}

	// 8. Record through the reducer: display binding, decision-time digest
	// recheck and kind legality live there. Its errors are rejections, not
	// hook errors. With a settlement, ONE commit applies settlement AND
	// decision (RecordDecisionWithBudget) — a validation failure or a failed
	// Save leaves NEITHER applied.
	beforeConfirmed := run.BudgetConfirmed
	beforeExts := len(run.BudgetExtensions)
	if settling {
		settle.ToolUseID = in.ToolUseID
		if err := run.RecordDecisionWithBudget(cp.ID, d, in.ToolUseID, &settle); err != nil {
			return reject(err.Error())
		}
	} else if err := run.RecordDecision(cp.ID, d, in.ToolUseID); err != nil {
		return reject(err.Error())
	}
	// The settlement's traces precede checkpoint.decided, mirroring the
	// commit order (settlement first, decision second) — and fire only when
	// the reducer newly applied something.
	if settle.Confirm != nil && !beforeConfirmed && run.BudgetConfirmed {
		if err := appendTrace(run, in, shared.TraceEvent{
			Type: shared.EventBudgetConfirmed,
			Payload: map[string]interface{}{
				"checkpoint_id": cp.ID,
				"limits":        budgetLimitsPayload(*settle.Confirm),
			},
		}, errW); err != nil {
			return 2
		}
	}
	if settle.Extend && len(run.BudgetExtensions) > beforeExts {
		if err := appendTrace(run, in, shared.TraceEvent{
			Type: shared.EventBudgetExtended,
			Payload: map[string]interface{}{
				"checkpoint_id": cp.ID,
				"delta":         budgetLimitsPayload(run.BudgetExtensions[len(run.BudgetExtensions)-1].Delta),
			},
		}, errW); err != nil {
			return 2
		}
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
	// T18: the terminal decisions enter post-game (spec §6). DONE starts the
	// normal questionnaire; STOP writes the minimal exit journal hook-side
	// immediately (紧急 stop = 最小 journal + 补答). Both stay POST_GAME_PENDING
	// — CLOSED comes only through CompletePostGame.
	if d == shared.DecisionDone || d == shared.DecisionStop {
		return concludeTerminalDecision(run, in, d == shared.DecisionStop, errW)
	}
	return 0
}

// budgetLimitsPayload is the trace-payload form of a BudgetLimits value.
func budgetLimitsPayload(l shared.BudgetLimits) map[string]interface{} {
	return map[string]interface{}{
		"phase_tool_calls": l.PhaseToolCalls,
		"failed_checks":    l.FailedChecks,
		"run_rework":       l.RunRework,
		"delegations":      l.Delegations,
	}
}

// concludeTerminalDecision starts the post-game of a DONE/STOP decision (T18,
// spec §6: 正常结束须问卷+journal 持久化后才 CLOSED; 紧急 stop = 最小 journal +
// 补答). DONE → kind "normal" (questionnaire + full journal follow); STOP → the
// hook writes the minimal exit journal immediately and records its
// observation, kind "emergency_minimal". Both leave the run POST_GAME_PENDING;
// closure happens only in the OWNING session when every marker and the journal
// observation are on record (maybeClosePostGame).
func concludeTerminalDecision(run *shared.RunState, in *hookInput, emergency bool, errW io.Writer) int {
	kind := "normal"
	if emergency {
		kind = "emergency_minimal"
	}
	// StartPostGame first, so the ledger's kind is authoritative before any
	// observation auto-creates it.
	if err := run.StartPostGame(kind); err != nil {
		fmt.Fprintf(errW, "summoner posttooluse: starting the post-game ledger failed — fail-closed: %v\n", err)
		return 2
	}
	if emergency {
		digest, err := shared.WriteMinimalExitJournal(run)
		if err != nil {
			fmt.Fprintf(errW, "summoner posttooluse: writing the minimal exit journal failed — fail-closed: %v\n", err)
			return 2
		}
		if err := run.RecordPostGameJournal(digest, in.ToolUseID); err != nil {
			fmt.Fprintf(errW, "summoner posttooluse: recording the journal observation failed — fail-closed: %v\n", err)
			return 2
		}
	}
	if err := appendTrace(run, in, shared.TraceEvent{
		Type:    shared.EventPostGameStarted,
		Payload: map[string]interface{}{"kind": kind},
	}, errW); err != nil {
		return 2
	}
	// An emergency stop with the questionnaire already complete (a resumed
	// post-game re-stopped) may close immediately.
	if run.Status == shared.StatusPostGamePending {
		return maybeClosePostGame(run, in, errW)
	}
	return 0
}

// decisionQuestion is the tool_input question bound to the current
// checkpoint: its full text (the answers map key) and offered options.
type decisionQuestion struct {
	text    string
	options []interface{}
}

// nonceQuestions returns every tool_input question whose text carries the
// current checkpoint's nonce (the T16 predicate — shared by the multi-nonce
// fail-closed check and findDecisionQuestion).
func nonceQuestions(cp *shared.CheckpointState, toolInput map[string]interface{}) []map[string]interface{} {
	qs, _ := toolInput[fieldQuestions].([]interface{})
	var bound []map[string]interface{}
	for _, raw := range qs {
		m, _ := raw.(map[string]interface{})
		text, _ := m[fieldQuestion].(string)
		if text != "" && strings.Contains(text, cp.Nonce) {
			bound = append(bound, m)
		}
	}
	return bound
}

// findDecisionQuestion locates the question whose text contains the current
// checkpoint's nonce. T16 multi-nonce rule: exactly ONE nonce-carrying
// question per call — two or more would make the answers map ambiguous
// (which question binds the decision?), so the whole callback fails closed
// with a rejection and nothing is recorded (recordDecisionAttempt enforces
// this BEFORE any ride-along effect; the branch here is the same contract
// for any other caller). When no question carries the nonce, the reject
// reason distinguishes a wrong-checkpoint binding (some question carries
// ANOTHER checkpoint's nonce) from a payload carrying no nonce at all.
func findDecisionQuestion(run *shared.RunState, cp *shared.CheckpointState, toolInput map[string]interface{}) (*decisionQuestion, string) {
	bound := nonceQuestions(cp, toolInput)
	if len(bound) > 1 {
		return nil, fmt.Sprintf("call carries %d questions with the current checkpoint %s nonce %s — exactly ONE decision question per call (fail-closed, nothing recorded)", len(bound), cp.ID, cp.Nonce)
	}
	if len(bound) == 1 {
		options, _ := bound[0][fieldOptions].([]interface{})
		text, _ := bound[0][fieldQuestion].(string)
		return &decisionQuestion{text: text, options: options}, ""
	}
	qs, _ := toolInput[fieldQuestions].([]interface{})
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

// --- T17: the drop:/keep: ride-along channel ----------------------------------------

// dropQuestion is one parsed nonce-free drop question: the criterion id it
// offers and its two option labels (keep is the default, drop the explicit
// drop), keyed by the question's full text (the answers map key).
type dropQuestion struct {
	id        string
	text      string
	keepLabel string
	dropLabel string
}

// parseDropQuestions extracts and validates every drop question of the call
// ("" problem = all well-formed). A question is a DROP question iff any option
// label leads with the keyword drop or keep — the same leading-keyword rule
// the decision labels obey; nonce-free by construction (a drop question
// carrying the nonce is malformed: the nonce binds the DECISION question).
// Shape contract: exactly two options, `drop: <id>` and `keep: <id>` naming
// ONE id (the question text must contain it), and the id must be droppable at
// this checkpoint — among the checkpoint's criteria or the frozen acceptance
// view (pure pre-check; RecordCriteriaDrop re-validates inside its commit).
// Any violation rejects the WHOLE callback (keep-default enforcement): a
// malformed drop question must not launder a decision or a drop past the
// channel.
func parseDropQuestions(run *shared.RunState, cp *shared.CheckpointState, toolInput map[string]interface{}) ([]dropQuestion, string) {
	qs, _ := toolInput[fieldQuestions].([]interface{})
	var drops []dropQuestion
	for _, raw := range qs {
		m, _ := raw.(map[string]interface{})
		if m == nil {
			continue
		}
		options, _ := m[fieldOptions].([]interface{})
		text, _ := m[fieldQuestion].(string)
		// Detection: any option leading with drop/keep marks the question.
		isDrop := false
		for _, opt := range options {
			om, _ := opt.(map[string]interface{})
			label, _ := om[fieldLabel].(string)
			switch labelKeyword(label) {
			case "drop", "keep":
				isDrop = true
			}
			if isDrop {
				break
			}
		}
		if !isDrop {
			continue
		}
		var id, keepLabel, dropLabel string
		for _, opt := range options {
			om, _ := opt.(map[string]interface{})
			label, _ := om[fieldLabel].(string)
			keyword, rest := splitLabelKeyword(label)
			switch keyword {
			case "drop":
				if dropLabel != "" {
					return nil, fmt.Sprintf("drop question %q carries two drop: options — exactly one drop: and one keep: naming the same criterion id", truncate(text, 80))
				}
				dropLabel = label
				if id == "" {
					id = rest
				} else if id != rest {
					return nil, fmt.Sprintf("drop question %q offers drop: %s and keep: %s — the two options must name the SAME criterion id", truncate(text, 80), rest, id)
				}
			case "keep":
				if keepLabel != "" {
					return nil, fmt.Sprintf("drop question %q carries two keep: options — exactly one drop: and one keep: naming the same criterion id", truncate(text, 80))
				}
				keepLabel = label
				if id == "" {
					id = rest
				} else if id != rest {
					return nil, fmt.Sprintf("drop question %q offers drop: %s and keep: %s — the two options must name the SAME criterion id", truncate(text, 80), id, rest)
				}
			}
		}
		switch {
		case dropLabel == "" || keepLabel == "":
			return nil, fmt.Sprintf("drop question %q must carry exactly a keep: and a drop: option (keep is the default)", truncate(text, 80))
		case len(options) != 2:
			return nil, fmt.Sprintf("drop question %q must carry exactly two options (keep: and drop:), got %d", truncate(text, 80), len(options))
		case id == "":
			return nil, fmt.Sprintf("drop question %q options name no criterion id", truncate(text, 80))
		case !strings.Contains(text, id):
			return nil, fmt.Sprintf("drop question %q must name the criterion id it offers (%s)", truncate(text, 80), id)
		case strings.Contains(text, cp.Nonce):
			return nil, fmt.Sprintf("drop question %q carries the checkpoint nonce — the nonce binds the DECISION question; drop questions ride along nonce-free", truncate(text, 80))
		}
		if !droppableAt(run, cp, id) {
			return nil, fmt.Sprintf("drop question offers criterion %q which is neither among the checkpoint %s criteria nor the frozen acceptance view — nothing else may be dropped", id, cp.ID)
		}
		drops = append(drops, dropQuestion{id: id, text: text, keepLabel: keepLabel, dropLabel: dropLabel})
	}
	return drops, ""
}

// validateDropAnswers checks every ANSWERED drop question against its two
// offered labels — the same offered-option rule the decision answer obeys.
// Unanswered drop questions are keep-by-default and fine; an invalid answer
// rejects the whole callback.
func validateDropAnswers(drops []dropQuestion, answers map[string]interface{}) string {
	for _, dq := range drops {
		ans, _ := answers[dq.text].(string)
		if ans == "" {
			continue // unanswered: keep is the default
		}
		if ans != dq.keepLabel && ans != dq.dropLabel {
			return fmt.Sprintf("drop question answer %q is not one of its offered labels (%q / %q)", truncate(ans, 80), dq.keepLabel, dq.dropLabel)
		}
	}
	return ""
}

// droppableAt reports whether the criterion id may be dropped at this
// checkpoint: among the checkpoint's pending criteria or the frozen
// acceptance view (信封 criteria 集 = 冻结集 − drops — the channel exists to
// trim frozen criteria at later checkpoints).
func droppableAt(run *shared.RunState, cp *shared.CheckpointState, id string) bool {
	for _, c := range cp.CriteriaIDs {
		if c == id {
			return true
		}
	}
	for _, fr := range run.AcceptanceView().Requirements {
		for _, c := range fr.CriteriaIDs {
			if c == id {
				return true
			}
		}
	}
	return false
}

// labelKeyword returns the leading keyword of an option label (normalized).
func labelKeyword(label string) string {
	keyword, _ := splitLabelKeyword(label)
	return keyword
}

// splitLabelKeyword splits one option label into its leading keyword
// (lowercased, punctuation-stripped) and the remainder trimmed of label
// punctuation ("drop: crit-a" → "drop", "crit-a").
func splitLabelKeyword(label string) (keyword, rest string) {
	fields := strings.Fields(label)
	if len(fields) == 0 {
		return "", ""
	}
	keyword = normalizeKeyword(fields[0])
	rest = strings.TrimSpace(strings.Trim(strings.TrimSpace(strings.TrimPrefix(label, fields[0])), ":,;!?. "))
	return keyword, rest
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
