// stop — the Stop hook of the single-loop rewrite (spec
// docs/superpowers/specs/2026-11-09-single-loop-checkpoint-design.md §1.3
// 展示核验, §1.6 停止不对称, §4 展示绑定, §5 Stop row of the 事件×责任表:
// 展示核验 + WAIT_USER 保状态 + 收尾前不 CLOSED + OUTCOME_UNKNOWN 留档;
// 不在检查点删活动运行).
//
// A DISPLAY VERIFIER, NOT A WORK GATE — the stop asymmetry. The gate denies
// work fail-closed; this hook must never hard-fail the turn on ambiguity:
// unreadable state or multiple live runs degrade to a stderr warning + allow.
// Generated text ≠ shown text: in WAIT_USER with an undisplayed current
// checkpoint, the turn may only end after last_assistant_message CONTAINS
// the checkpoint's canonical block (an explanation may precede it) — which
// is itself the display act: MarkDisplayed binds the checkpoint's canonical
// content digest and checkpoint.displayed is traced. Binding is not the end:
// the hook then BLOCKS with the exact decision question, and keeps doing so
// until the question is asked or the user decides (0.7.0 D2 — the user never
// types "continue" between a shown checkpoint and its dialog).
//
// 8-BLOCK CAP (verified platform fact): the platform overrides the 9th
// consecutive Stop block and ends the turn regardless. Every block is
// therefore idempotent — the same reason string, demanding nothing new on
// re-fire — and each demands exactly one tool call (the ask), which resets
// the counter; cap exhaustion degrades to "turn ends, run state kept", which
// is exactly the stop-asymmetry contract: state is NEVER deleted here, and
// stopping must always remain possible:
//   - an explicit stop/halt/abort acknowledgment (the documented marker list)
//     allows the stop at an undisplayed checkpoint, and at a displayed one
//     whose question was never asked, keeping state;
//   - OUTCOME_UNKNOWN entries are strictly retained (留档不清理) — the
//     reconciliation debt outlives the turn;
//   - NO mtime sweeping of v2 run state, EVER: a WAIT_USER run may legally
//     outlive any age threshold. Cleanup happens only through CLOSED
//     transitions (post-game persistence, T8/T13).
//
// The un-brick path is SUMMONER_HOOKS_DISABLE == "1" exactly: enforce nothing,
// exit 0 — but using it while exactly one run is live appends one
// hooks.disabled event first (a recorded contract violation).
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// stopSource stamps every trace event this binary appends.
const stopSource = "stop"

// Platform event name (ruling 2). Empty is tolerated — the field is common
// but not guaranteed on every payload generation.
const eventStop = "Stop"

// stopAckRe is the DOCUMENTED stop-acknowledgment marker list (ruling 3): an
// explicit stop/halt/abort (English inflections or Chinese 停止/终止) as the
// dominant intent of last_assistant_message. Stopping must always be possible
// (§1.6), so a match allows the stop without the render block; the cost of a
// wrong guess is bounded by the 8-block cap on the opposite error. The regex
// alone is NOT the ack test — see stopAck: every rendered decision menu
// contains a "stop: …" line, so the canonical labels are stripped first (T16
// carry from T10: a full status render must never satisfy the ack).
var stopAckRe = regexp.MustCompile(`(?i)\b(?:stop(?:ped|ping)?|halt(?:ed|ing)?|abort(?:ed|ing)?)\b|停止|终止`)

// stopAck reports an explicit stop acknowledgment in the message OUTSIDE the
// canonical decision-menu labels: each label of the pending checkpoint's kind
// (e.g. "stop: 停止——保留状态退出") is removed before the marker regex runs,
// so pasting the full render — the display act — can never pose as an ack
// and skip display binding. A genuine acknowledgment ("Stopped as you
// asked.") survives the strip untouched.
func stopAck(msg string, cp *shared.CheckpointState) bool {
	if cp != nil {
		for _, label := range shared.DecisionMenuLabels(cp.Kind) {
			msg = strings.ReplaceAll(msg, label, "")
		}
	}
	return stopAckRe.MatchString(msg)
}

// stopInput is the parsed Stop payload: exactly the typed fields the hook
// needs (ruling 2). stop_hook_active is carried for documentation — this hook
// needs no loop guard because the platform-side cap bounds consecutive blocks.
type stopInput struct {
	EventName            string
	SessionID            string
	StopHookActive       bool
	LastAssistantMessage string
}

// parseInput decodes one Stop payload. Anything that is not a single JSON
// object is an error — the caller fails closed (exit 2), the one fail-closed
// path of this hook. Trailing garbage after the object is also refused.
func parseInput(r io.Reader) (*stopInput, error) {
	var raw struct {
		HookEventName        string `json:"hook_event_name"`
		SessionID            string `json:"session_id"`
		StopHookActive       bool   `json:"stop_hook_active"`
		LastAssistantMessage string `json:"last_assistant_message"`
	}
	dec := json.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("stdin is not a hook JSON object: %v", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing data after the JSON object")
	}
	return &stopInput{
		EventName:            raw.HookEventName,
		SessionID:            raw.SessionID,
		StopHookActive:       raw.StopHookActive,
		LastAssistantMessage: raw.LastAssistantMessage,
	}, nil
}

func main() {
	os.Exit(runStop(os.Stdin, os.Stdout, os.Stderr))
}

// runStop is the whole hook. Returns the process exit code: 0 allowed or
// blocked (a block is a stdout JSON decision, never an error), 2 only for
// unparseable input.
func runStop(stdin io.Reader, outW, errW io.Writer) int {
	in, err := parseInput(stdin)
	if err != nil {
		fmt.Fprintf(errW, "summoner stop: unreadable hook input — fail-closed: %v\n", err)
		return 2
	}
	if in.EventName != "" && in.EventName != eventStop {
		fmt.Fprintf(errW, "summoner stop: unknown hook_event_name %q (this binary serves Stop) — ignoring\n", in.EventName)
		return 0
	}

	// SUMMONER_HOOKS_DISABLE — the un-brick hatch (spec §5), armed by the
	// exact value "1" only. Enforce nothing. Using it while exactly one run
	// is live is itself a recorded contract violation (hooks.disabled); zero
	// live or ambiguous runs have nothing unambiguously live to violate —
	// exit 0 with no events. A state-read error here does not re-brick the
	// session: the hatch outranks state.
	if os.Getenv("SUMMONER_HOOKS_DISABLE") == "1" {
		if runs, err := shared.SessionRuns(in.SessionID); err == nil {
			if live := liveRuns(runs); len(live) == 1 {
				if err := shared.AppendEvent(live[0].RunID, in.SessionID, shared.TraceEvent{
					Type:    shared.EventHooksDisabled,
					Source:  stopSource,
					Payload: map[string]interface{}{"stop_hook_active": in.StopHookActive},
				}); err != nil {
					fmt.Fprintf(errW, "summoner stop: hooks.disabled trace append failed: %v\n", err)
				}
			}
		}
		return 0
	}

	// Run resolution — warn, never fail: Stop is a display verifier, not a
	// work gate; it must not hard-fail the turn (the deliberate asymmetry vs
	// the gate's fail-closed exit 2 on the same conditions).
	runs, err := shared.SessionRuns(in.SessionID)
	if err != nil {
		fmt.Fprintf(errW, "summoner stop: session run-state unreadable — cannot verify display, allowing the stop (state kept): %v\n", err)
		return 0
	}
	live := liveRuns(runs)
	switch {
	case len(live) == 0:
		return 0 // no live Summoner run: not our business — silent
	case len(live) > 1:
		ids := make([]string, 0, len(live))
		for _, r := range live {
			ids = append(ids, r.RunID)
		}
		fmt.Fprintf(errW, "summoner stop: %d live Summoner runs in this session (%s) — ambiguous; cannot name one checkpoint to verify, allowing the stop (state kept)\n",
			len(live), strings.Join(ids, ", "))
		return 0
	}
	run := live[0]

	// T19 crash scan (spec §5): a MUTATING tool intent whose result never
	// landed is an outcome-unknown side effect — mark it before anything else
	// looks at status (a marked WAIT_USER run becomes OUTCOME_UNKNOWN here, so
	// the display logic below correctly defers to the reconciliation episode;
	// the episode's restore returns the run to WAIT_USER where the render
	// obligation resurfaces). Tools only: a live worker delegation may
	// legitimately still be running at a normal per-turn Stop — a live
	// delegation is not a crash (the SessionStart scan converts those; there
	// the previous session's workers are dead by construction). This scan
	// NEVER blocks the stop — it records, warns, and the stop asymmetry holds.
	markDanglingIntents(run, in, errW)

	if run.Status == shared.StatusWaitUser {
		return waitUserStop(run, in, outW, errW)
	}

	// Not WAIT_USER (ACTIVE / REVIEW_PENDING / OUTCOME_UNKNOWN /
	// POST_GAME_PENDING …): allow, keep state. A non-emergency stop of a live
	// run owes the post-game review — remind, delete nothing (the mandatory
	// questionnaire wiring itself lands in T8/T13).
	if !stopAck(in.LastAssistantMessage, run.CurrentCheckpoint()) {
		fmt.Fprintf(errW, "\n⚡ Summoner: run %s is still %s (not CLOSED) — the post-game review is still owed; run state was kept and can be resumed in a later session.\n\n",
			run.RunID, run.Status)
	}
	return 0
}

// markDanglingIntents is the Stop crash scan (T19): mutating tool intents of
// the live run whose results never landed become OUTCOME_UNKNOWN entries —
// each with its tool.outcome_unknown marking (detected_by stop). Idempotent:
// already-marked ids and terminal states no-op, so the 8-block re-fire path
// records nothing twice. A Save or trace failure only warns: the stop itself
// is never blocked over bookkeeping (state stays unmarked and the next
// SessionStart/Stop rescan finds the same intents).
func markDanglingIntents(run *shared.RunState, in *stopInput, errW io.Writer) {
	if run.Status == shared.StatusClosed || run.Status == shared.StatusPostGamePending {
		return
	}
	evs, err := shared.LoadRunEvents(run.RunID)
	if err != nil {
		fmt.Fprintf(errW, "summoner stop: crash scan skipped — trace unreadable: %v\n", err)
		return
	}
	before := len(run.OutcomeUnknowns)
	for _, d := range shared.MutatingDanglingIntents(evs, false) {
		// Recovery round I3: the entry freezes the intent's pre-effect
		// declaration — the matcher resolves against exactly what was
		// declared at admission.
		run.MarkOutcomeUnknownWithDecl(d.ToolUseID, d.Decl)
	}
	if len(run.OutcomeUnknowns) == before {
		return
	}
	if err := run.Save(); err != nil {
		fmt.Fprintf(errW, "summoner stop: crash scan could not persist run %s (state unchanged, next SessionStart rescans): %v\n", run.RunID, err)
		return
	}
	for _, e := range run.OutcomeUnknowns[before:] {
		if err := shared.AppendEvent(run.RunID, in.SessionID, shared.TraceEvent{
			Type:      shared.EventToolOutcomeUnknown,
			Source:    stopSource,
			ToolUseID: e.ToolUseID,
			Payload:   map[string]interface{}{"tool_use_id": e.ToolUseID, "detected_by": "stop"},
		}); err != nil {
			fmt.Fprintf(errW, "summoner stop: tool.outcome_unknown trace append failed for %s: %v\n", e.ToolUseID, err)
		}
	}
}

// waitUserStop is the WAIT_USER half of the hook (0.7.0 D2 — explain, then
// ask, with no "continue" in between):
//
//   - UNDISPLAYED checkpoint: a message that carries the canonical block
//     (shared.VerifyDisplayContainment — an explanation may precede it and
//     the tail may differ) IS the display act: the hook binds it and BLOCKS
//     with the exact decision question to raise. Without the block, an
//     explicit stop acknowledgment ends the turn with state kept; anything
//     else is blocked with the demand to render.
//   - DISPLAYED and undecided: the question is owed now. A stop
//     acknowledgment still ends the turn (stopping is always possible);
//     every other sign-off is blocked with the same question.
//
// The containment test runs BEFORE the stop-ack test: a pasted block is the
// display act whatever words the explanation around it uses ("先停止服务再
// 复现" is not a request to halt the run). Before 0.7.0 the hook allowed the
// stop right after binding — a model that pasted the checkpoint and ended
// its turn left the user with a dialog that never came (run df0ed2e6 waited
// 62 minutes on exactly that). Each block demands one tool call, and every
// tool call resets the platform's consecutive-block counter.
func waitUserStop(run *shared.RunState, in *stopInput, outW, errW io.Writer) int {
	cp := run.CurrentCheckpoint()
	if cp == nil {
		return 0 // WAIT_USER with nothing owed
	}
	if !cp.Displayed {
		if shared.VerifyDisplayContainment(run, *cp, in.LastAssistantMessage) {
			return bindDisplay(run, cp, in, outW, errW)
		}
		if stopAck(in.LastAssistantMessage, cp) {
			return 0 // explicit stop signal: allow, keep state, emit nothing
		}
		emitRenderBlock(outW, cp, run)
		return 0
	}
	if stopAck(in.LastAssistantMessage, cp) {
		return 0 // stopping stays possible at a displayed checkpoint
	}
	emitAskBlock(outW, cp, run)
	return 0
}

// bindDisplay binds the contained render (verified by
// shared.VerifyDisplayContainment before this runs): the checkpoint gets
// MarkDisplayed with its canonical content digest — the digest of exactly
// the content whose block was matched — plus one checkpoint.displayed event;
// the hook then hands back the ask block. If the binding itself fails the
// state stays undisplayed (commit never half-applies) and the SAME render is
// demanded again — idempotent under the platform's block cap.
func bindDisplay(run *shared.RunState, cp *shared.CheckpointState, in *stopInput, outW, errW io.Writer) int {
	if err := run.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(*cp, run.RequirementsVersion)); err != nil {
		fmt.Fprintf(errW, "summoner stop: binding display of checkpoint %s failed (state unchanged): %v\n", cp.ID, err)
		emitRenderBlock(outW, cp, run)
		return 0
	}
	if err := shared.AppendEvent(run.RunID, in.SessionID, shared.TraceEvent{
		Type:    shared.EventCheckpointDisplayed,
		Source:  stopSource,
		Payload: map[string]interface{}{"checkpoint_id": cp.ID, "nonce": cp.Nonce},
	}); err != nil {
		// The binding (state) succeeded — it is what gates decisions; the
		// trace is evidence. Warn and continue rather than hard-fail the turn.
		fmt.Fprintf(errW, "summoner stop: checkpoint.displayed trace append failed (display binding kept): %v\n", err)
	}
	emitAskBlock(outW, cp, run)
	return 0
}

// emitBlock writes one block decision: stdout JSON {"decision":"block",
// "reason":…}. Deterministic from run state — the same checkpoint yields
// byte-identical output on every re-fire, which is what makes it safe under
// the platform's consecutive-block cap (nothing new is demanded on re-fire).
func emitBlock(outW io.Writer, reason string) {
	if err := json.NewEncoder(outW).Encode(map[string]string{
		"decision": "block",
		"reason":   reason,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "summoner stop: writing block decision failed: %v\n", err)
	}
}

// emitRenderBlock demands the canonical render of THIS checkpoint (obtainable
// byte-exact from `summoner-status` view:checkpoint). The message only has
// to CONTAIN the block: an explanation may precede it and the tail line may
// differ.
func emitRenderBlock(outW io.Writer, cp *shared.CheckpointState, run *shared.RunState) {
	reason := fmt.Sprintf("渲染 checkpoint %s (nonce %s) 的规范消息后再结束：消息中须完整包含该规范块（summoner-status view:checkpoint 提供逐字节原文；可在块前写解释，尾行 ╚ 长短不限）", cp.ID, cp.Nonce)
	if n := unresolvedOutcomes(run); n > 0 {
		reason += fmt.Sprintf("；另有 %d 项 OUTCOME_UNKNOWN 待对账", n)
	}
	emitBlock(outW, reason)
}

// emitAskBlock hands back the exact decision question of a BOUND checkpoint:
// the nonce for the question text and page 1's labels verbatim — the gate
// admits nothing else — plus the one ride the answer may carry. The labels
// come from the persisted page, never from memory: a hand-paraphrased label
// is the most common rejected ask.
func emitAskBlock(outW io.Writer, cp *shared.CheckpointState, run *shared.RunState) {
	page := shared.DecisionPage(*cp)
	var b strings.Builder
	fmt.Fprintf(&b, "检查点 %s（nonce %s）已展示并绑定。现在用 AskUserQuestion 发起裁决——题干带 nonce %s，选项 = 第 1 页 %d 个标签逐字：\n", cp.ID, cp.Nonce, cp.Nonce, len(page))
	for _, label := range page {
		fmt.Fprintf(&b, "  - %s\n", label)
	}
	b.WriteString("用户回复 more: 时用下一页标签重问（全部页见 summoner-status）；不必重复粘贴渲染。")
	if cp.ExtendAsk != nil {
		fmt.Fprintf(&b, "\n预算已耗尽：同一调用再附带一道不带 nonce 的追加题，选项恰为：%s / %s。", cp.ExtendAsk.ExtendLabel, cp.ExtendAsk.HoldLabel)
	}
	emitBlock(outW, b.String())
}

// unresolvedOutcomes counts OUTCOME_UNKNOWN entries still awaiting their
// read-only reconciliation probe (spec §5).
func unresolvedOutcomes(run *shared.RunState) int {
	n := 0
	for i := range run.OutcomeUnknowns {
		if !run.OutcomeUnknowns[i].Resolved {
			n++
		}
	}
	return n
}

// liveRuns filters out CLOSED runs — a closed run is not verified or recorded.
func liveRuns(runs []*shared.RunState) []*shared.RunState {
	var live []*shared.RunState
	for _, r := range runs {
		if r.Status != shared.StatusClosed {
			live = append(live, r)
		}
	}
	return live
}
