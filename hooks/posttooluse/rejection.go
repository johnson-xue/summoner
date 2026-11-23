package main

// rejection.go — the VISIBLE rejection channel of the decision recorder
// (0.7.0 D4). Before 0.7.0 a rejected decision wrote its trace event and
// exited 0 with the reason on stderr — and a zero-exit hook's stderr never
// reaches the model. So a user who typed text instead of clicking an option
// got the same question again: the model saw a dialog result, nothing else
// (run df0ed2e6 asked one question three times over a typed correction).
//
// A rejection is now answered on stdout with {"decision":"block",
// "reason":…} — the platform shows the reason beside the tool result. The
// WHOLE stdout is that one object (a PostToolUse hook's stdout is parsed
// whole), and the reason carries three things the model cannot do without:
//
//   - what was wrong, in the platform's own terms (the nonce, the exact
//     labels — never a paraphrase);
//   - what the SAME callback already changed (a recorded drop, an applied
//     extension): those effects are not rolled back, and a settled extend
//     must not be asked again — extensions dedupe by tool_use_id, so a
//     re-ask extends twice;
//   - the next step, which must be a LEGAL path at this checkpoint: while
//     the run waits, writes are denied and the phase is closed — the model
//     can only investigate read-only, re-ask, or route the user to recall /
//     a requirement change / stop.
//
// The text itself still advances nothing: only a platform answer records a
// decision.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// appliedEffects is what one callback already changed before its decision
// was rejected. Ride-along effects land BEFORE the decision by design (a
// drop must precede the CONTINUE that may clear it; an extension rides the
// same commit only when the decision completes), so a rejection can arrive
// with state already moved.
type appliedEffects struct {
	drops    []string             // criterion ids recorded in this callback
	extended *shared.BudgetLimits // the delta of an extension applied in this callback
}

// emitRejection writes the block decision of a rejected decision attempt.
// problem is the recorder's own reason; next is the legal path at this
// checkpoint; applied is what the callback already changed.
func emitRejection(outW io.Writer, cp *shared.CheckpointState, problem string, applied *appliedEffects) {
	if outW == nil {
		return
	}
	var drops []string
	var extended *shared.BudgetLimits
	if applied != nil {
		drops, extended = applied.drops, applied.extended
	}
	// The text lives in shared (decisiontext.go): the Stop hook must ignore
	// these same words when it judges a stop acknowledgment (stopack.go).
	reason := shared.RejectionReason(*cp, problem, drops, extended)
	if err := json.NewEncoder(outW).Encode(map[string]string{"decision": "block", "reason": reason}); err != nil {
		fmt.Fprintf(os.Stderr, "summoner posttooluse: writing the rejection block failed: %v\n", err)
	}
}
