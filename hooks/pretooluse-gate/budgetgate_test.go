package main

// budgetgate_test.go — T18 gate suite: the isStatusInvocation shape check (the
// exact read-only render call is the budget escape; anything else is judged
// normally) and the human-control budget exemption surface at exhaustion
// (status render, WAIT_USER/POST_GAME_PENDING asks, the change loop, the
// fixed journal path, OUTCOME_UNKNOWN read-class probes).

import (
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

func TestIsStatusInvocation(t *testing.T) {
	canonical := `"${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status" <<< '{}'`
	yes := []string{
		canonical,
		`"${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status" <<< '{"session_id":"s"}'`, // commands/status.md spelling
		"/abs/path/hooks/bin/summoner-status <<< '{\"session_id\":\"s\"}'",
		"summoner-status",
		`env summoner-status <<< '{}'`,
		`command summoner-status`,
	}
	for _, cmd := range yes {
		if !isStatusInvocation(cmd) {
			t.Errorf("isStatusInvocation(%q) = false, want true", cmd)
		}
	}
	no := []string{
		// F1 reviewer repro: the payload carries '>' — a redirect in
		// disguise, which the old {…} shape check accepted and the budget
		// exemption then let EXECUTE at exhaustion (probe created `pwned}`).
		`summoner-status <<< {a:>pwned}`,
		`summoner-status <<< {a:<pwned}`,  // redirect symmetry twin
		`summoner-status <<< {a:>>pwned}`, // doubled redirect twin
		`summoner-status <<< {a: 1<x}`,    // input-redirect twin
		// F1 verb-spelling tightening: any directory prefix OTHER than the
		// plugin-bin path is a different binary, never budget-exempt.
		`some/dir/summoner-status <<< '{}'`,
		`/usr/local/bin/summoner-status`,
		`./summoner-status`,
		`summoner-status --help`,
		`summoner-status --format json`,
		`summoner-status <<< '{}' > /tmp/out`,     // redirect: not the read-only shape
		`summoner-status <<< '{}'; rm -rf /tmp/x`, // a second command
		`summoner-status | tee /tmp/out`,          // piped
		`summoner-status-extra <<< '{}'`,          // a different binary
		`cat /tmp/x`,
		``,
	}
	for _, cmd := range no {
		if isStatusInvocation(cmd) {
			t.Errorf("isStatusInvocation(%q) = true, want false", cmd)
		}
	}
	// Belt: the canonical form must also be read-class (no '>' anywhere in it).
	if isWriteClassCommand(canonical) {
		t.Error("the canonical status invocation must stay read-class")
	}
}

// TestF1WritingPayloadFallsBackToJudgment pins the reviewer's repro
// END-TO-END: `summoner-status <<< {a:>pwned}` is no longer budget-exempt
// AND is write-class, so at exhaustion it takes the ordinary budget denial
// (the write never executes).
func TestF1WritingPayloadFallsBackToJudgment(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{PhaseToolCalls: 1})
	acceptPhase(t, r, "context")
	acceptPhase(t, r, "diagnosis")
	if err := r.OpenPhase("reproduce"); err != nil {
		t.Fatal(err)
	}
	if code, _ := callGate(t, toolPayload(t, "Read", readInput())); code != 0 {
		t.Fatal("first read within budget must pass")
	}
	// The '>' payload is genuinely write-class; the '<' payload is read-class
	// in the write-class model (input redirect reads) — BOTH lose the budget
	// exemption and take the ordinary budget denial at exhaustion.
	if !isWriteClassCommand(`summoner-status <<< {a:>pwned}`) {
		t.Error("isWriteClassCommand(>' payload) = false, want true (redirect payload)")
	}
	for _, cmd := range []string{
		`summoner-status <<< {a:>pwned}`,
		`summoner-status <<< {a:<pwned}`,
	} {
		code, stderr := callGate(t, toolPayload(t, "Bash", bashInput(cmd)))
		wantExit2(t, code, stderr, "余量为零")
	}
	// The canonical read-only invocation still passes at the same state.
	if code, _ := callGate(t, toolPayload(t, "Bash", bashInput(`"${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status" <<< '{}'`))); code != 0 {
		t.Fatal("the canonical status invocation must stay runnable at exhaustion")
	}
}

// TestBudgetEscapeSurfaceAtExhaustion pins the human-control exemptions: with
// the budget at zero, the surfaces that carry the run to its checkpoint (and
// the user's answer back) stay usable; everything else waits for extension.
func TestBudgetEscapeSurfaceAtExhaustion(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{PhaseToolCalls: 1})
	acceptPhase(t, r, "context")
	acceptPhase(t, r, "diagnosis")
	if err := r.OpenPhase("reproduce"); err != nil {
		t.Fatal(err)
	}
	// Spend the one-call budget.
	if code, _ := callGate(t, toolPayload(t, "Read", readInput())); code != 0 {
		t.Fatal("first read within budget must pass")
	}

	// The EXACT status invocation passes at exhaustion; anything else Bash
	// does not (including a same binary with a flag).
	if code, _ := callGate(t, toolPayload(t, "Bash", bashInput(`"${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status" <<< '{}'`))); code != 0 {
		t.Fatal("the canonical status invocation must stay runnable at exhaustion")
	}
	code, stderr := callGate(t, toolPayload(t, "Bash", bashInput(`summoner-status --help`)))
	wantExit2(t, code, stderr, "余量为零")
	code, stderr = callGate(t, toolPayload(t, "Read", readInput()))
	wantExit2(t, code, stderr, "余量为零")
	// The change loop is the replan surface — exempt.
	if code, stderr := callGate(t, toolPayload(t, "Skill", skillInput("summoner:change", `{"subcommand":"open","note":"replan","position":{"current_task":"x"}}`))); code != 0 {
		t.Fatalf("summoner:change at exhaustion exit = %d, want 0 (stderr %q)", code, stderr)
	}
	// Close the change again through the reducer so later steps see a clean
	// run (the gate's change-open persisted to disk — reload first).
	if _, err := reloadRun(t, "run-1").ApplyRequirementDelta(shared.RequirementDelta{Note: "replan"}); err != nil {
		t.Fatal(err)
	}
	cur := reloadRun(t, "run-1")
	confirm := cur.CurrentCheckpoint()
	if err := cur.MarkDisplayed(confirm.ID, shared.CheckpointContentDigest(*confirm, cur.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	if err := cur.RecordDecision(confirm.ID, shared.DecisionAcceptRequirementChange, ""); err != nil {
		t.Fatal(err)
	}

	// OUTCOME_UNKNOWN read-class probes pass even at exhaustion; write-class
	// probes do not.
	r = reloadRun(t, "run-1")
	r.MarkOutcomeUnknown("tu-crash-1")
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	if code, _ := callGate(t, toolPayload(t, "Bash", bashInput("ls /tmp"))); code != 0 {
		t.Fatal("a read-class OUTCOME_UNKNOWN probe must pass at exhaustion")
	}
	// A write-class probe at OUTCOME_UNKNOWN AND exhaustion hits the budget
	// denial (only read-class probes are exempt; the OUTCOME_UNKNOWN write
	// denial applies once the budget allows probing at all).
	code, stderr = callGate(t, toolPayload(t, "Bash", bashInput("ls /tmp > /tmp/out")))
	wantExit2(t, code, stderr, "余量为零")
}

// TestPostGamePendingBudgetExemptions pins the closure-debt surface: with the
// budget exhausted, a POST_GAME_PENDING run may still be asked its
// questionnaire and write ONLY its fixed journal path.
func TestPostGamePendingBudgetExemptions(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{PhaseToolCalls: 1})
	acceptPhase(t, r, "context")
	// Land POST_GAME_PENDING with an exhausted budget: decide DONE at the
	// diagnosis checkpoint after burning the budget.
	if err := r.OpenPhase("diagnosis"); err != nil {
		t.Fatal(err)
	}
	r.CountToolCall()
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	cp, err := r.SubmitPhase(shared.PhaseSubmission{PhaseID: "diagnosis"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordDecision(cp.ID, shared.DecisionDone, "tu-done"); err != nil {
		t.Fatal(err)
	}
	if err := r.StartPostGame("normal"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := reloadRun(t, "run-1").BudgetExhausted(); !ok {
		t.Fatal("fixture must be exhausted")
	}

	// Questionnaire asks pass at POST_GAME_PENDING even with zero budget.
	if code, _ := callGate(t, toolPayload(t, "AskUserQuestion", askInput("Post-game q1 (run run-1)? [pg:q1]"))); code != 0 {
		t.Fatal("the questionnaire ask must stay runnable at an exhausted POST_GAME_PENDING run")
	}
	// The FIXED journal path is writable; any other write waits for budget.
	journal := map[string]interface{}{"file_path": ".summoner/journal/run-1.md", "content": "x"}
	if code, _ := callGate(t, toolPayload(t, "Write", journal)); code != 0 {
		t.Fatal("the fixed journal path must stay writable at exhaustion")
	}
	code, stderr := callGate(t, toolPayload(t, "Write", writeInput()))
	wantExit2(t, code, stderr, "余量为零")
	// Ordinary Bash still waits (no status shape).
	code, stderr = callGate(t, toolPayload(t, "Bash", bashInput("ls")))
	wantExit2(t, code, stderr, "余量为零")
}

// TestAskUserBudgetRideValidatedAtDoor: the gate rejects a malformed budget
// ride-along (foreign pointer labels) while WAIT_USER — the decision channel
// never launders a stale budget question.
func TestAskUserBudgetRideValidatedAtDoor(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	if err := r.OpenPhase("context"); err != nil {
		t.Fatal(err)
	}
	cp, err := r.SubmitPhase(shared.PhaseSubmission{PhaseID: "context"})
	if err != nil {
		t.Fatal(err)
	}
	if cp.BudgetAsk == nil {
		t.Fatal("the first checkpoint must carry the budget ask")
	}
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	page := cp.MenuPages[0]
	pageOpts := make([]interface{}, 0, len(page.Labels))
	for _, l := range page.Labels {
		pageOpts = append(pageOpts, map[string]interface{}{"label": l})
	}
	decisionQ := map[string]interface{}{
		"question": "Checkpoint decision (nonce " + cp.Nonce + "): how?",
		"options":  pageOpts,
	}
	badRide := map[string]interface{}{
		"question": "Budget?",
		"options": []interface{}{
			map[string]interface{}{"label": "budget: 50"}, // a label-parsed number
			map[string]interface{}{"label": cp.BudgetAsk.UnlimitedLabel},
		},
	}
	code, stderr := callGate(t, toolPayload(t, "AskUserQuestion", map[string]interface{}{"questions": []interface{}{
		decisionQ,
		badRide,
	}}))
	wantExit2(t, code, stderr, "persisted pointer labels")

	// The well-formed ride passes the door untouched.
	goodRide := map[string]interface{}{
		"question": "Budget?",
		"options": []interface{}{
			map[string]interface{}{"label": cp.BudgetAsk.ConfirmLabel},
			map[string]interface{}{"label": cp.BudgetAsk.UnlimitedLabel},
		},
	}
	if code, stderr := callGate(t, toolPayload(t, "AskUserQuestion", map[string]interface{}{"questions": []interface{}{
		decisionQ,
		goodRide,
	}})); code != 0 {
		t.Fatalf("well-formed budget ride: exit %d stderr %q", code, stderr)
	}
}
