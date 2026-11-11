// userpromptsubmit — the UserPromptSubmit hook of the single-loop rewrite
// (spec docs/superpowers/specs/2026-11-09-single-loop-checkpoint-design.md §5
// UserPromptSubmit row: 运行启动记录 — the ONLY run starter; it never gates).
//
// A RECORDER, never a blocker: hooks.json deliberately wires NO onFailure
// here (settled ruling: a stuck run-start hook must never block prompts —
// its stdout is discarded on timeout anyway), so exit 2 is only ever a
// surfaced error, never a blocked prompt.
//
// stdin is one UserPromptSubmit payload: {hook_event_name, session_id,
// prompt, …}. Run-start matching is DUAL (settled): the TYPED form — a prompt
// beginning with /summoner:<workflow> — or the EXPANDED form — the platform's
// expansion marker <command-name>summoner:<workflow></command-name> — over
// the same seven-verb framework set (fix|new|debug|review|ship|ops|release).
// No match → exit 0, no state, no events: non-Summoner sessions stay
// untouched by construction.
//
// Before starting, the session's runs are checked: ANY live (non-CLOSED) run
// means NO second run is ever created (a second live run would make every
// other hook fail closed on ambiguity) — stderr one-line note, exit 0. A
// start mints a fresh crypto/rand run id, StartRun with all-zero
// BudgetLimits (= unlimited; budgets surface at the first checkpoint, spec
// §5), and one workflow.started trace event. Unreadable state,
// StartRun/AppendEvent failures → stderr + exit 2.
//
// The un-brick path is SUMMONER_HOOKS_DISABLE == "1" exactly: enforce
// nothing, start nothing, exit 0 — but using it while exactly one run is
// live appends one hooks.disabled event first (a recorded contract
// violation); zero live or ambiguous runs have nothing unambiguously live to
// violate. The hatch runs FIRST, before any event-name check (this hook's
// ruling), and a state-read error under it does not re-brick the session:
// the hatch outranks state.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// upsSource stamps every trace event this binary appends.
const upsSource = "userpromptsubmit"

// eventUserPromptSubmit is the only platform event this binary serves; the
// matcher field is omitted in hooks.json because UserPromptSubmit supports
// none (verified 2026-11-10).
const eventUserPromptSubmit = "UserPromptSubmit"

// typedFormRe matches the TYPED delivery form: a prompt beginning with a
// Summoner slash verb as a COMPLETE token: whitespace or end, not the
// regex word boundary (which also accepts the hyphen in fix-extra).
var typedFormRe = regexp.MustCompile(`^/summoner:(fix|new|debug|review|ship|ops|release)([[:space:]]|$)`)

// workflowNames is the framework verb set both delivery forms recognize.
var workflowNames = []string{"fix", "new", "debug", "review", "ship", "ops", "release"}

// expandedFormMarker is the EXPANDED delivery marker of one verb: the
// platform expands /summoner:<name> into a prompt carrying
// <command-name>summoner:<name></command-name>.
func expandedFormMarker(name string) string {
	return "<command-name>summoner:" + name + "</command-name>"
}

// matchWorkflow resolves the Summoner verb a prompt carries, typed form
// first, then the expanded-form marker. A verb appearing as a plain word
// ("please fix this") is NOT a match — non-Summoner prompts never start runs.
func matchWorkflow(prompt string) (string, bool) {
	if m := typedFormRe.FindStringSubmatch(prompt); m != nil {
		return m[1], true
	}
	for _, name := range workflowNames {
		if strings.Contains(prompt, expandedFormMarker(name)) {
			return name, true
		}
	}
	return "", false
}

func main() {
	os.Exit(runHook(os.Stdin, os.Stderr))
}

// hookInput is the parsed payload: exactly the typed fields the hook needs.
type hookInput struct {
	EventName string
	SessionID string
	Prompt    string
}

// parseInput decodes one UserPromptSubmit payload. Anything that is not a
// single JSON object is an error — the caller fails closed (exit 2, surfaced;
// prompts are never blocked). Trailing garbage after the object is refused.
func parseInput(r io.Reader) (*hookInput, error) {
	var raw struct {
		HookEventName string `json:"hook_event_name"`
		SessionID     string `json:"session_id"`
		Prompt        string `json:"prompt"`
	}
	dec := json.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("stdin is not a hook JSON object: %v", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing data after the JSON object")
	}
	return &hookInput{
		EventName: raw.HookEventName,
		SessionID: raw.SessionID,
		Prompt:    raw.Prompt,
	}, nil
}

// runHook is the whole hook. Returns the process exit code: 0 recorded (or
// no-op), 2 fail-closed error surfaced to the platform (never a blocked
// prompt — no onFailure is wired).
func runHook(stdin io.Reader, errW io.Writer) int {
	in, err := parseInput(stdin)
	if err != nil {
		fmt.Fprintf(errW, "summoner userpromptsubmit: unreadable hook input — fail-closed: %v\n", err)
		return 2
	}

	// SUMMONER_HOOKS_DISABLE — the un-brick hatch (spec §5), FIRST, armed by
	// the exact value "1" only. Start nothing. Using it while exactly one
	// run is live is itself a recorded contract violation (hooks.disabled);
	// zero live or ambiguous runs have nothing unambiguously live to
	// violate — exit 0 with no events. No payload is recorded: prompt text
	// never enters the trace.
	if os.Getenv("SUMMONER_HOOKS_DISABLE") == "1" {
		if runs, err := shared.SessionRuns(in.SessionID); err == nil {
			if live := liveRuns(runs); len(live) == 1 {
				if err := shared.AppendEvent(live[0].RunID, in.SessionID, shared.TraceEvent{
					Type:   shared.EventHooksDisabled,
					Source: upsSource,
				}); err != nil {
					fmt.Fprintf(errW, "summoner userpromptsubmit: hooks.disabled trace append failed: %v\n", err)
				}
			}
		}
		return 0
	}

	if in.EventName != eventUserPromptSubmit {
		fmt.Fprintf(errW, "summoner userpromptsubmit: unknown hook_event_name %q (this binary serves UserPromptSubmit) — fail-closed\n", in.EventName)
		return 2
	}

	workflow, matched := matchWorkflow(in.Prompt)
	if !matched {
		return 0 // not a Summoner verb: no state, no events
	}

	// A second live run is never created: every other hook resolves "the
	// session's one live run" and fails closed on ambiguity.
	runs, err := shared.SessionRuns(in.SessionID)
	if err != nil {
		fmt.Fprintf(errW, "summoner userpromptsubmit: session run-state unreadable — fail-closed: %v\n", err)
		return 2
	}
	if live := liveRuns(runs); len(live) > 0 {
		ids := make([]string, 0, len(live))
		for _, r := range live {
			ids = append(ids, r.RunID)
		}
		fmt.Fprintf(errW, "summoner userpromptsubmit: session already has %d live Summoner run(s) (%s) — not starting a second\n",
			len(live), strings.Join(ids, ", "))
		// T18: a POST_GAME_PENDING run is closable debt, not dead weight —
		// name what it owes so the user can finish it in this (owning)
		// session instead of starting around it.
		for _, r := range live {
			if r.Status == shared.StatusPostGamePending {
				fmt.Fprintf(errW, "summoner userpromptsubmit: run %s is POST_GAME_PENDING — ask its questionnaire ([pg:q1..q4] questions carrying the run id) and write the journal at .summoner/journal/%s.md so it can CLOSE; CLOSED runs stop blocking new ones\n",
					r.RunID, r.RunID)
			}
		}
		return 0
	}

	runID, err := newRunID()
	if err != nil {
		fmt.Fprintf(errW, "summoner userpromptsubmit: minting run id failed — fail-closed: %v\n", err)
		return 2
	}
	if _, err := shared.StartRun(runID, in.SessionID, workflow, shared.BudgetLimits{}); err != nil {
		fmt.Fprintf(errW, "summoner userpromptsubmit: starting run failed — fail-closed: %v\n", err)
		return 2
	}
	if err := shared.AppendEvent(runID, in.SessionID, shared.TraceEvent{
		Type:    shared.EventWorkflowStarted,
		Source:  upsSource,
		Payload: map[string]interface{}{"workflow": workflow},
	}); err != nil {
		fmt.Fprintf(errW, "summoner userpromptsubmit: workflow.started trace append failed — fail-closed: %v\n", err)
		return 2
	}
	return 0
}

// newRunID mints a fresh run id: 8 crypto-random bytes, hex-encoded (16
// chars — the validID-safe charset by construction).
func newRunID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// liveRuns filters out CLOSED runs — a closed run neither blocks a new run
// nor has anything left to violate under the hatch.
func liveRuns(runs []*shared.RunState) []*shared.RunState {
	var live []*shared.RunState
	for _, r := range runs {
		if r.Status != shared.StatusClosed {
			live = append(live, r)
		}
	}
	return live
}
