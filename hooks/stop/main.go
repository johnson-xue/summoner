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
// checkpoint, the turn may only end after last_assistant_message carries the
// checkpoint's render block (checkpoint id + nonce) — which is itself the
// display act: MarkDisplayed binds the checkpoint's canonical content digest
// and checkpoint.displayed is traced.
//
// 8-BLOCK CAP (verified platform fact): the platform overrides the 9th
// consecutive Stop block and ends the turn regardless. The block below is
// therefore idempotent — the same reason string, demanding nothing new on
// re-fire — so cap exhaustion degrades to "turn ends, run state kept", which
// is exactly the stop-asymmetry contract: state is NEVER deleted here, and
// stopping must always remain possible:
//   - an explicit stop/halt/abort acknowledgment (the documented marker list)
//     allows the stop even at an undisplayed checkpoint, keeping state;
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
// wrong guess is bounded by the 8-block cap on the opposite error.
var stopAckRe = regexp.MustCompile(`(?i)\b(?:stop(?:ped|ping)?|halt(?:ed|ing)?|abort(?:ed|ing)?)\b|停止|终止`)

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

	// The stop-acknowledgment marker: stopping is always possible (§1.6).
	ack := stopAckRe.MatchString(in.LastAssistantMessage)

	if run.Status == shared.StatusWaitUser {
		if cp := run.CurrentCheckpoint(); cp != nil && !cp.Displayed {
			if ack {
				return 0 // explicit stop signal: allow, keep state, emit nothing
			}
			if hasRenderBlock(in.LastAssistantMessage, cp) {
				return markDisplayedAndAllow(run, cp, in, outW, errW)
			}
			emitBlock(outW, cp, run)
			return 0
		}
		// Displayed (or nothing pending): the display obligation is met.
		return 0
	}

	// Not WAIT_USER (ACTIVE / REVIEW_PENDING / OUTCOME_UNKNOWN /
	// POST_GAME_PENDING …): allow, keep state. A non-emergency stop of a live
	// run owes the post-game review — remind, delete nothing (the mandatory
	// questionnaire wiring itself lands in T8/T13).
	if !ack {
		fmt.Fprintf(errW, "\n⚡ Summoner: run %s is still %s (not CLOSED) — the post-game review is still owed; run state was kept and can be resumed in a later session.\n\n",
			run.RunID, run.Status)
	}
	return 0
}

// markDisplayedAndAllow binds the verified render: the render block in
// last_assistant_message IS the display act, so the checkpoint gets
// MarkDisplayed with its canonical content digest plus one checkpoint.displayed
// event, and the stop is allowed.
func markDisplayedAndAllow(run *shared.RunState, cp *shared.CheckpointState, in *stopInput, outW, errW io.Writer) int {
	if err := run.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(*cp, run.RequirementsVersion)); err != nil {
		// State stayed undisplayed (commit never half-applies): re-demand the
		// SAME render — idempotent under the 8-block cap, nothing new asked.
		fmt.Fprintf(errW, "summoner stop: binding display of checkpoint %s failed (state unchanged): %v\n", cp.ID, err)
		emitBlock(outW, cp, run)
		return 0
	}
	if err := shared.AppendEvent(run.RunID, in.SessionID, shared.TraceEvent{
		Type:    shared.EventCheckpointDisplayed,
		Source:  stopSource,
		Payload: map[string]interface{}{"checkpoint_id": cp.ID, "nonce": cp.Nonce},
	}); err != nil {
		// The binding (state) succeeded — it is what gates decisions; the
		// trace is evidence. Warn and allow rather than hard-fail the turn.
		fmt.Fprintf(errW, "summoner stop: checkpoint.displayed trace append failed (display binding kept): %v\n", err)
	}
	return 0
}

// hasRenderBlock reports whether the message carries the checkpoint's render
// block: BOTH the checkpoint id and its nonce (spec §1.3 — the platform gives
// us only the message text, so the render block is identified by content).
func hasRenderBlock(msg string, cp *shared.CheckpointState) bool {
	return strings.Contains(msg, cp.ID) && strings.Contains(msg, cp.Nonce)
}

// emitBlock writes the block decision: stdout JSON {"decision":"block",
// "reason":…} demanding the render of THIS checkpoint. Idempotent by
// construction: same checkpoint, same nonce, same reason on every re-fire.
func emitBlock(outW io.Writer, cp *shared.CheckpointState, run *shared.RunState) {
	reason := fmt.Sprintf("渲染 checkpoint %s (nonce %s) 后再结束", cp.ID, cp.Nonce)
	if n := unresolvedOutcomes(run); n > 0 {
		reason += fmt.Sprintf("；另有 %d 项 OUTCOME_UNKNOWN 待对账", n)
	}
	if err := json.NewEncoder(outW).Encode(map[string]string{
		"decision": "block",
		"reason":   reason,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "summoner stop: writing block decision failed: %v\n", err)
	}
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
