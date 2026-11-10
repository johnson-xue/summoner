package main

// menu_test.go — T16 TDD suite, gate half: the two-turn decision surface.
// (a) same-turn deny: a nonce-carrying AskUserQuestion while !cp.Displayed is
// denied with the two-turn instruction (S1: same-turn render+ask can never be
// recorded — RecordDecision requires the display the Stop hook binds at turn
// end). (b) offered-set: the nonce question's options must EQUAL one persisted
// menu page exactly. (d) route: the review workflow's phase-submit opens its
// route phases (no route existed before T16).

import (
	"fmt"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// bindDisplay performs the display-binding transition the Stop hook executes
// at the end of the render turn (MarkDisplayed + canonical content digest).
// Tests never pre-seed Displayed by hand — this is the sole setter's action.
func bindDisplay(t *testing.T, r *shared.RunState, cp *shared.CheckpointState) {
	t.Helper()
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(*cp, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed(%s): %v", cp.ID, err)
	}
}

// askPageInput builds an AskUserQuestion tool_input whose single question
// carries the checkpoint nonce and offers exactly the given labels.
func askPageInput(nonce string, labels []string) map[string]interface{} {
	opts := make([]interface{}, 0, len(labels))
	for _, l := range labels {
		opts = append(opts, map[string]interface{}{"label": l, "description": "decision option"})
	}
	return map[string]interface{}{"questions": []interface{}{map[string]interface{}{
		"question":    fmt.Sprintf("Checkpoint decision (nonce %s): how should the run proceed?", nonce),
		"header":      "Checkpoint",
		"options":     opts,
		"multiSelect": false,
	}}}
}

// waitUserPhase opens and submits one phase, returning the pending checkpoint.
func waitUserPhase(t *testing.T, r *shared.RunState, phaseID string) *shared.CheckpointState {
	t.Helper()
	if err := r.OpenPhase(phaseID); err != nil {
		t.Fatalf("OpenPhase(%s): %v", phaseID, err)
	}
	cp, err := r.SubmitPhase(shared.PhaseSubmission{PhaseID: phaseID, Summary: phaseID + " done"})
	if err != nil {
		t.Fatalf("SubmitPhase(%s): %v", phaseID, err)
	}
	return &cp
}

// (a) TestAskSameTurnDeniedBeforeDisplay: WAIT_USER with an UNDISPLAYED
// checkpoint — the nonce-carrying ask is denied with the two-turn
// instruction; after the display binds (the Stop hook's transition), the same
// ask passes.
func TestAskSameTurnDeniedBeforeDisplay(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	cp := waitUserPhase(t, r, "context")

	if len(cp.MenuPages) == 0 {
		t.Fatal("checkpoint must carry the persisted menu projection")
	}
	code, stderr := callGate(t, toolPayload(t, "AskUserQuestion", askPageInput(cp.Nonce, cp.MenuPages[0].Labels)))
	wantExit2(t, code, stderr, "two-turn")

	// Nothing was consumed by the denial: still WAIT_USER, still undisplayed.
	after := reloadRun(t, "run-1")
	cpNow := after.CurrentCheckpoint()
	if cpNow == nil || cpNow.Displayed || cpNow.Decided {
		t.Fatalf("same-turn deny must consume nothing, checkpoint = %+v", cpNow)
	}
	if after.Status != shared.StatusWaitUser {
		t.Fatalf("status = %s, want WAIT_USER", after.Status)
	}

	// Turn N+1 (display bound by the Stop hook's transition): the ask passes.
	bindDisplay(t, after, cpNow)
	code, stderr = callGate(t, toolPayload(t, "AskUserQuestion", askPageInput(cp.Nonce, cp.MenuPages[0].Labels)))
	if code != 0 {
		t.Fatalf("page-pinned ask after display: exit %d, want 0 (stderr %q)", code, stderr)
	}
}

// (b) TestAskOfferedSetMustEqualPersistedPage: while displayed, an offered
// set that is not EXACTLY one persisted page is denied; both pages of the
// full menu are individually legal to offer.
func TestAskOfferedSetMustEqualPersistedPage(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	cp := waitUserPhase(t, r, "context")
	bindDisplay(t, r, cp)

	page1 := cp.MenuPages[0].Labels
	page2 := cp.MenuPages[1].Labels

	cases := []struct {
		name   string
		labels []string
	}{
		{"single foreign label", []string{"Continue: 继续下一阶段"}},
		{"subset of page 1", page1[:3]},
		{"page 1 plus one extra", append(append([]string{}, page1...), "bogus: x")},
		{"page 2 minus one", page2[:3]},
		{"no options at all", nil},
	}
	for _, tc := range cases {
		code, stderr := callGate(t, toolPayload(t, "AskUserQuestion", askPageInput(cp.Nonce, tc.labels)))
		wantExit2(t, code, stderr, "menu page")
	}

	// Both pages are legal to offer — and set equality is order-insensitive.
	code, stderr := callGate(t, toolPayload(t, "AskUserQuestion", askPageInput(cp.Nonce, page1)))
	if code != 0 {
		t.Fatalf("exact page 1 offered: exit %d, want 0 (stderr %q)", code, stderr)
	}
	code, stderr = callGate(t, toolPayload(t, "AskUserQuestion", askPageInput(cp.Nonce, page2)))
	if code != 0 {
		t.Fatalf("exact page 2 offered: exit %d, want 0 (stderr %q)", code, stderr)
	}
	reordered := []string{page1[3], page1[2], page1[1], page1[0]}
	code, stderr = callGate(t, toolPayload(t, "AskUserQuestion", askPageInput(cp.Nonce, reordered)))
	if code != 0 {
		t.Fatalf("reordered page 1 offered: exit %d, want 0 (stderr %q)", code, stderr)
	}
}

// TestAskNonDecisionQuestionStillNeedsNonce: a nonce-free question while
// WAIT_USER stays denied (ruling 8 unchanged — navigation is content
// feedback, not a question channel).
func TestAskNonDecisionQuestionStillNeedsNonce(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	waitUserPhase(t, r, "context")

	code, stderr := callGate(t, toolPayload(t, "AskUserQuestion", askInput("show me the other menu page?")))
	wantExit2(t, code, stderr, "nonce")
}

// (d) TestReviewWorkflowRoutePhaseSubmit: the review route (context → review)
// drives phase opening — phase-submit auto-opens context (skillless first
// phase), and after context is accepted, phase-submit opens review.
func TestReviewWorkflowRoutePhaseSubmit(t *testing.T) {
	useGateEnv(t)
	startRunWF(t, "review", shared.BudgetLimits{})

	code, stderr := callGate(t, toolPayload(t, "Skill", skillInput("summoner:phase-submit", submitArgs(t, "context"))))
	if code != 0 {
		t.Fatalf("review phase-submit(context): exit %d, want 0 (stderr %q)", code, stderr)
	}
	after := reloadRun(t, "run-1")
	if after.Status != shared.StatusWaitUser || len(after.Phases) != 1 || after.Phases[0].ID != "context" || after.Phases[0].Open {
		t.Fatalf("after context submit: status %s phases %+v", after.Status, after.Phases)
	}

	// Decide CONTINUE at cp-1 (display via the Stop hook's transition).
	cp := after.CurrentCheckpoint()
	bindDisplay(t, after, cp)
	if err := after.RecordDecision(cp.ID, shared.DecisionContinue, ""); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}

	// Next expected phase on the review route is review; a skillless
	// phase-submit for it auto-opens and submits.
	code, stderr = callGate(t, toolPayload(t, "Skill", skillInput("summoner:phase-submit", submitArgs(t, "review"))))
	if code != 0 {
		t.Fatalf("review phase-submit(review): exit %d, want 0 (stderr %q)", code, stderr)
	}
	after = reloadRun(t, "run-1")
	if len(after.Phases) != 2 || after.Phases[1].ID != "review" {
		t.Fatalf("review route must open review after context, phases = %+v", after.Phases)
	}
	if cp2 := after.CurrentCheckpoint(); cp2 == nil || cp2.PhaseID != "review" {
		t.Fatalf("review checkpoint missing: %+v", after.Checkpoints)
	}
}

// TestNewVerbRoutesOpenFirstPhase: every newly routed verb opens its first
// phase on a sequence-driving Skill call (table-driven).
func TestNewVerbRoutesOpenFirstPhase(t *testing.T) {
	for wf, first := range map[string]string{
		"review":  "context",
		"ship":    "prepare",
		"ops":     "context",
		"release": "planning",
	} {
		useGateEnv(t)
		startRunWF(t, wf, shared.BudgetLimits{})
		code, stderr := callGate(t, toolPayload(t, "Skill", skillInput("project-skill", "")))
		if code != 0 {
			t.Fatalf("%s: first skill call exit %d, want 0 (stderr %q)", wf, code, stderr)
		}
		after := reloadRun(t, "run-1")
		if len(after.Phases) != 1 || !after.Phases[0].Open || after.Phases[0].ID != first {
			t.Fatalf("%s: first phase must be %s, got %+v", wf, first, after.Phases)
		}
	}
}
