// pretooluse-gate — the all-tool PreToolUse gate of the single-loop rewrite
// (spec docs/superpowers/specs/2026-11-09-single-loop-checkpoint-design.md §5:
// PreToolUse（全工具） owns 写入资格、等待门、Skill/Agent/AskUserQuestion 负载
// 校验、phase-submit 提交门、检查入口与预算).
//
// stdin is one PreToolUse JSON payload; exit 0 allows the call, exit 2 denies
// it with a human-readable reason on stderr. Fail-closed everywhere: unreadable
// or malformed input denies (exit 2) — the old fail-open behavior is gone.
//
// Non-Summoner sessions stay untouched: with zero non-CLOSED runs for the
// session the gate is a pure pass-through (exit 0, no state change, no trace).
// The un-brick path is SUMMONER_HOOKS_DISABLE (any non-empty value): enforce
// nothing, exit 0 — but using it while exactly one run is live appends one
// hooks.disabled event first (a recorded contract violation).
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// gateSource stamps every trace event this binary appends.
const gateSource = "pretooluse-gate"

func main() {
	os.Exit(runGate(os.Stdin, os.Stderr))
}

// gateInput is the parsed hook payload: generic decoding with exactly the
// typed fields the gate needs (ruling 2). tool_input stays a raw map — the
// gate knows the per-tool shapes it inspects, nothing more.
type gateInput struct {
	SessionID string
	ToolName  string
	ToolUseID string
	ToolInput map[string]interface{}
}

// parseInput decodes one PreToolUse payload. Anything that is not a single
// JSON object whose tool_input (when present) is an object is an error — the
// caller denies. Trailing garbage after the object is also refused.
func parseInput(r io.Reader) (*gateInput, error) {
	var raw struct {
		SessionID string                 `json:"session_id"`
		ToolName  string                 `json:"tool_name"`
		ToolUseID string                 `json:"tool_use_id"`
		ToolInput map[string]interface{} `json:"tool_input"`
	}
	dec := json.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("stdin is not a PreToolUse JSON object: %v", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing data after the JSON object")
	}
	return &gateInput{
		SessionID: raw.SessionID,
		ToolName:  raw.ToolName,
		ToolUseID: raw.ToolUseID,
		ToolInput: raw.ToolInput,
	}, nil
}

// runGate is the whole hook. Returns the process exit code: 0 allow, 2 deny.
func runGate(stdin io.Reader, errW io.Writer) int {
	in, err := parseInput(stdin)
	if err != nil {
		fmt.Fprintf(errW, "summoner gate: unreadable hook input — denying (fail-closed): %v\n", err)
		return 2
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
					Type:      shared.EventHooksDisabled,
					Source:    gateSource,
					ToolUseID: in.ToolUseID,
					Payload:   map[string]interface{}{"tool_name": in.ToolName},
				}); err != nil {
					fmt.Fprintf(errW, "summoner gate: hooks.disabled trace append failed: %v\n", err)
				}
			}
		}
		return 0
	}

	runs, err := shared.SessionRuns(in.SessionID)
	if err != nil {
		fmt.Fprintf(errW, "summoner gate: session run-state unreadable — denying (fail-closed): %v\n", err)
		return 2
	}
	live := liveRuns(runs)

	switch {
	case len(live) == 0:
		// No active Summoner run: not our business — unless the model is
		// trying to drive the protocol without a run.
		if in.ToolName == "Skill" {
			if skill := stringField(in.ToolInput, "skill"); skill == skillPhaseSubmit || skill == skillChange || skill == skillReviewSubmit || skill == skillProbeSubmit {
				fmt.Fprintf(errW, "summoner gate: %s requires an active run — this session has none\n", skill)
				return 2
			}
		}
		return 0
	case len(live) > 1:
		ids := make([]string, 0, len(live))
		for _, r := range live {
			ids = append(ids, r.RunID)
		}
		fmt.Fprintf(errW, "summoner gate: %d live Summoner runs in this session (%s) — ambiguous; every tool call is denied until exactly one remains\n",
			len(live), strings.Join(ids, ", "))
		return 2
	}

	run := live[0]
	if err := validateShape(in); err != nil {
		fmt.Fprintf(errW, "summoner gate: malformed payload — denying (fail-closed): %v\n", err)
		return 2
	}
	d := decide(in, run)
	if !d.allow {
		if d.reject != nil {
			payload := map[string]interface{}{"reason": d.reason}
			if d.reject.phaseID != "" {
				payload["phase_id"] = d.reject.phaseID
			}
			if err := shared.AppendEvent(run.RunID, run.SessionID, shared.TraceEvent{
				Type:      shared.EventPhaseSubmittedRejected,
				Source:    gateSource,
				ToolUseID: in.ToolUseID,
				Payload:   payload,
			}); err != nil {
				fmt.Fprintf(errW, "summoner gate: phase_submitted.rejected trace append failed: %v\n", err)
			}
		}
		// T19: a denial that owes honest outcomes (the ladder's BLOCKED
		// check.results) traces them — a refused check is an observed
		// outcome, never silence. A trace failure here cannot un-deny: warn
		// and keep the denial.
		for _, ev := range d.denyEvents {
			ev.Source = gateSource
			ev.ToolUseID = in.ToolUseID
			if err := shared.AppendEvent(run.RunID, run.SessionID, ev); err != nil {
				fmt.Fprintf(errW, "summoner gate: check.result trace append failed: %v\n", err)
			}
		}
		fmt.Fprintln(errW, d.reason)
		return 2
	}
	if err := apply(run, in, d.eff, errW); err != nil {
		return 2
	}
	return 0
}

// liveRuns filters out CLOSED runs — a closed run is not enforced (ruling 5).
func liveRuns(runs []*shared.RunState) []*shared.RunState {
	var live []*shared.RunState
	for _, r := range runs {
		if r.Status != shared.StatusClosed {
			live = append(live, r)
		}
	}
	return live
}
