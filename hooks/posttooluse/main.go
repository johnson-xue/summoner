// posttooluse — the PostToolUse / PostToolUseFailure hook of the single-loop
// rewrite (spec docs/superpowers/specs/2026-11-09-single-loop-checkpoint-design.md
// §5: PostToolUse / PostToolUseFailure owns 登记真实工具结果与退出态、
// AskUserQuestion 裁决记账、Agent 遥测; §1.4: the AskUserQuestion
// tool_response is the ONLY source of run advancement).
//
// ONE binary serves BOTH platform events; it branches on stdin
// hook_event_name. PostToolUse input: {hook_event_name, session_id, tool_name,
// tool_input, tool_response, tool_use_id}; PostToolUseFailure input adds
// {error, is_interrupt?, duration_ms?}. Gate exit-2 denials fire NEITHER
// event, so denied calls are never seen here.
//
// A PostToolUse hook conceptually cannot deny an executed tool: exit 2 is NOT
// a denial but a surfaced error, used ONLY for fail-closed conditions —
// unparseable input, ambiguous run state and trace-write failures. A decision
// attempt that fails validation is NOT a hook error: it is a
// checkpoint.decision_rejected trace event and exit 0 (no decision written).
//
// Non-Summoner sessions stay untouched: with zero non-CLOSED runs the hook is
// a pure pass-through (exit 0, no state change, no trace). The un-brick path
// is SUMMONER_HOOKS_DISABLE == "1" exactly: enforce nothing, exit 0 — but
// using it while exactly one run is live appends one hooks.disabled event
// first (a recorded contract violation); zero live or ambiguous runs have
// nothing unambiguously live to violate.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// postSource stamps every trace event this binary appends.
const postSource = "posttooluse"

// Platform event names (ruling 2). hooks.json wires BOTH events explicitly,
// so an empty hook_event_name is a miswire, not a legacy payload — it fails
// closed like any unknown event (task 6 / C1).
const (
	eventPostToolUse        = "PostToolUse"
	eventPostToolUseFailure = "PostToolUseFailure"
)

// Tool names this hook treats specially.
const (
	toolAskUserQuestion = "AskUserQuestion"
	toolAgent           = "Agent"
)

// tool_response field names of the AskUserQuestion answer object.
const (
	fieldQuestions  = "questions"
	fieldQuestion   = "question"
	fieldOptions    = "options"
	fieldLabel      = "label"
	fieldAnswers    = "answers"
	fieldResponse   = "response"
	fieldAFKTimeout = "afkTimeoutMs"
)

func main() {
	os.Exit(runHook(os.Stdin, os.Stderr))
}

// hookInput is the parsed payload of either event: generic decoding with
// exactly the typed fields the hook needs. tool_input stays a raw map and
// tool_response a raw value — the hook knows the per-tool shapes it inspects,
// nothing more.
type hookInput struct {
	EventName    string
	SessionID    string
	ToolName     string
	ToolUseID    string
	ToolInput    map[string]interface{}
	ToolResponse interface{}
	Error        string
	IsInterrupt  bool
}

// parseInput decodes one hook payload. Anything that is not a single JSON
// object whose tool_input (when present) is an object is an error — the
// caller fails closed. Trailing garbage after the object is also refused.
func parseInput(r io.Reader) (*hookInput, error) {
	var raw struct {
		HookEventName string                 `json:"hook_event_name"`
		SessionID     string                 `json:"session_id"`
		ToolName      string                 `json:"tool_name"`
		ToolUseID     string                 `json:"tool_use_id"`
		ToolInput     map[string]interface{} `json:"tool_input"`
		ToolResponse  interface{}            `json:"tool_response"`
		Error         string                 `json:"error"`
		IsInterrupt   bool                   `json:"is_interrupt"`
	}
	dec := json.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("stdin is not a hook JSON object: %v", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing data after the JSON object")
	}
	return &hookInput{
		EventName:    raw.HookEventName,
		SessionID:    raw.SessionID,
		ToolName:     raw.ToolName,
		ToolUseID:    raw.ToolUseID,
		ToolInput:    raw.ToolInput,
		ToolResponse: raw.ToolResponse,
		Error:        raw.Error,
		IsInterrupt:  raw.IsInterrupt,
	}, nil
}

// runHook is the whole hook. Returns the process exit code: 0 recorded (or
// pass-through), 2 fail-closed error surfaced to the platform.
func runHook(stdin io.Reader, errW io.Writer) int {
	in, err := parseInput(stdin)
	if err != nil {
		fmt.Fprintf(errW, "summoner posttooluse: unreadable hook input — fail-closed: %v\n", err)
		return 2
	}
	switch in.EventName {
	case eventPostToolUse, eventPostToolUseFailure:
	default:
		fmt.Fprintf(errW, "summoner posttooluse: unknown or empty hook_event_name %q (this binary serves PostToolUse and PostToolUseFailure, both wired explicitly in hooks.json) — fail-closed\n", in.EventName)
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
					Source:    postSource,
					ToolUseID: in.ToolUseID,
					Payload:   map[string]interface{}{"tool_name": in.ToolName},
				}); err != nil {
					fmt.Fprintf(errW, "summoner posttooluse: hooks.disabled trace append failed: %v\n", err)
				}
			}
		}
		return 0
	}

	if in.ToolName == "" || in.ToolUseID == "" {
		fmt.Fprintf(errW, "summoner posttooluse: payload requires tool_name and tool_use_id (spec §7 platform correlation keys) — fail-closed\n")
		return 2
	}

	// PostToolUseFailure without an error string is a malformed payload for
	// a KNOWN event — the same shape-validation principle the gate applies
	// (task 6 / C2): fail closed, record nothing.
	if in.EventName == eventPostToolUseFailure && in.Error == "" {
		fmt.Fprintf(errW, "summoner posttooluse: PostToolUseFailure payload requires a non-empty error string — fail-closed\n")
		return 2
	}

	// Run resolution, identical to the gate's (ruling 7).
	runs, err := shared.SessionRuns(in.SessionID)
	if err != nil {
		fmt.Fprintf(errW, "summoner posttooluse: session run-state unreadable — fail-closed: %v\n", err)
		return 2
	}
	live := liveRuns(runs)
	switch {
	case len(live) == 0:
		return 0 // no active Summoner run: not our business — no events
	case len(live) > 1:
		ids := make([]string, 0, len(live))
		for _, r := range live {
			ids = append(ids, r.RunID)
		}
		fmt.Fprintf(errW, "summoner posttooluse: %d live Summoner runs in this session (%s) — ambiguous; refusing to guess which run to record against\n",
			len(live), strings.Join(ids, ", "))
		return 2
	}
	run := live[0]

	if in.EventName == eventPostToolUseFailure {
		return recordFailure(run, in, errW)
	}
	return recordResult(run, in, errW)
}

// liveRuns filters out CLOSED runs — a closed run is not enforced or recorded.
func liveRuns(runs []*shared.RunState) []*shared.RunState {
	var live []*shared.RunState
	for _, r := range runs {
		if r.Status != shared.StatusClosed {
			live = append(live, r)
		}
	}
	return live
}

// appendTrace stamps and appends one trace event; a failure is a fail-closed
// exit-2 condition (a hook whose trace cannot be written must not silently
// pretend it recorded).
func appendTrace(run *shared.RunState, in *hookInput, ev shared.TraceEvent, errW io.Writer) error {
	ev.Source = postSource
	ev.ToolUseID = in.ToolUseID
	if err := shared.AppendEvent(run.RunID, in.SessionID, ev); err != nil {
		fmt.Fprintf(errW, "summoner posttooluse: trace append (%s) failed — fail-closed: %v\n", ev.Type, err)
		return err
	}
	return nil
}

// recordResult — PostToolUse of an executed tool against the session's one
// live run. AskUserQuestion in WAIT_USER against the current (non-superseded)
// checkpoint is the decision channel; everything else is observation:
// tool.result, plus agent.telemetry for Agent completions. An AskUserQuestion
// elsewhere (ACTIVE Q&A, or WAIT_USER with nothing pending) is an ordinary
// completion — it advances nothing (ruling 9).
func recordResult(run *shared.RunState, in *hookInput, errW io.Writer) int {
	if in.ToolName == toolAskUserQuestion && run.Status == shared.StatusWaitUser {
		if cp := run.CurrentCheckpoint(); cp != nil {
			return recordDecisionAttempt(run, cp, in, errW)
		}
	}
	if err := appendTrace(run, in, shared.TraceEvent{
		Type: shared.EventToolResult,
		Payload: map[string]interface{}{
			"tool_name":       in.ToolName,
			"response_sha256": responseDigest(in.ToolResponse),
		},
	}, errW); err != nil {
		return 2
	}
	if in.ToolName == toolAgent {
		if err := appendTrace(run, in, shared.TraceEvent{
			Type:    shared.EventAgentTelemetry,
			Payload: agentTelemetryPayload(in.ToolResponse),
		}, errW); err != nil {
			return 2
		}
	}
	return 0
}

// recordFailure — PostToolUseFailure: the exit state of a tool that errored
// or was interrupted. No decision path: a failed AskUserQuestion produced no
// answer, so nothing can be recorded (the pending checkpoint keeps waiting).
func recordFailure(run *shared.RunState, in *hookInput, errW io.Writer) int {
	first := in.Error
	if i := strings.IndexAny(first, "\r\n"); i >= 0 {
		first = first[:i]
	}
	if err := appendTrace(run, in, shared.TraceEvent{
		Type: shared.EventToolFailed,
		Payload: map[string]interface{}{
			"tool_name":        in.ToolName,
			"error_first_line": first,
			"is_interrupt":     in.IsInterrupt,
		},
	}, errW); err != nil {
		return 2
	}
	return 0
}

// responseDigest pins the evidence digest of a tool_response: sha256 over its
// canonical JSON (encoding/json struct order + sorted map keys — the same
// pinned serialization the trace chain uses; nil marshals as "null"). The
// response itself never enters the trace (§7 大证据本机存、trace 记摘要).
func responseDigest(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		b = nil // decoded JSON values cannot fail to re-marshal; stay deterministic
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// agentTelemetryPayload extracts the declared telemetry fields from an Agent
// tool_response (ruling 6): final_request_tokens mirrors totalTokens — the
// LAST-API-REQUEST-ONLY 口径 (V5: it misses intermediate requests and the
// main loop; a proxy metric, declared as such, never a budget) — and
// total_tool_use_count mirrors totalToolUseCount. Absent fields are omitted,
// never fabricated.
func agentTelemetryPayload(toolResponse interface{}) map[string]interface{} {
	payload := map[string]interface{}{}
	resp, ok := toolResponse.(map[string]interface{})
	if !ok {
		return payload
	}
	if v, ok := resp["totalTokens"].(float64); ok {
		payload["final_request_tokens"] = int64(v)
	}
	if v, ok := resp["totalToolUseCount"].(float64); ok {
		payload["total_tool_use_count"] = int64(v)
	}
	return payload
}
