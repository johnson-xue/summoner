package main

// budget_test.go — T18 posttooluse suite: the budget/extend ride-along
// settlement (combined commit, post-hoc rule, partial answers, malformed
// answers) and the post-game observation path (markers, afk guard, journal
// observation, closure). The exhaustion cycle and the real-binary pins live
// in hooks/pretooluse-gate and hooks/contract.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// openUnsettledFirstCheckpoint opens and displays the run's FIRST checkpoint
// WITHOUT settling the budget — the budget-ask fixture.
func openUnsettledFirstCheckpoint(t *testing.T, r *shared.RunState) *shared.CheckpointState {
	t.Helper()
	if err := r.OpenPhase("context"); err != nil {
		t.Fatalf("OpenPhase: %v", err)
	}
	cp, err := r.SubmitPhase(shared.PhaseSubmission{PhaseID: "context"})
	if err != nil {
		t.Fatalf("SubmitPhase: %v", err)
	}
	if cp.BudgetAsk == nil {
		t.Fatal("the first checkpoint must carry the persisted budget ask")
	}
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed: %v", err)
	}
	return r.CurrentCheckpoint()
}

// budgetQuestion builds the budget ride-along question (labels straight from
// the persisted ask) and its text.
func budgetQuestion(cp *shared.CheckpointState) (map[string]interface{}, string) {
	text := "Budget confirmation: accept the displayed proposal?"
	return map[string]interface{}{
		"question": text,
		"header":   "Budget",
		"options": []interface{}{
			map[string]interface{}{"label": cp.BudgetAsk.ConfirmLabel},
			map[string]interface{}{"label": cp.BudgetAsk.UnlimitedLabel},
		},
	}, text
}

// budgetedDecision posts one AskUserQuestion callback whose questions carry
// the page-1 decision labels plus the given ride-alongs, with the given
// answers keyed by question text.
func budgetedDecision(t *testing.T, toolUseID string, cp *shared.CheckpointState, rides []map[string]interface{}, answers map[string]interface{}) string {
	t.Helper()
	q, qtext := cpQuestion(cp.Nonce, false, cp.MenuPages[0].Labels...)
	qs := []interface{}{q}
	for _, ride := range rides {
		qs = append(qs, ride)
	}
	return postUsePayload(t, "AskUserQuestion", toolUseID,
		map[string]interface{}{"questions": qs},
		map[string]interface{}{"answers": mergeAnswers(map[string]interface{}{qtext: cp.MenuPages[0].Labels[0]}, answers)},
	)
}

func mergeAnswers(base, extra map[string]interface{}) map[string]interface{} {
	for k, v := range extra {
		base[k] = v
	}
	return base
}

func TestBudgetRideSettlesWithDecision(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := openUnsettledFirstCheckpoint(t, r)
	ride, rtext := budgetQuestion(cp)

	// Unlimited choice + CONTINUE: one commit, both effects.
	if code, stderr := callHook(t, budgetedDecision(t, "tu-1", cp, []map[string]interface{}{ride},
		map[string]interface{}{rtext: cp.BudgetAsk.UnlimitedLabel})); code != 0 {
		t.Fatalf("hook exit = %d (stderr %q)", code, stderr)
	}
	run := reloadHookRun(t)
	if !run.BudgetConfirmed || run.Limits != (shared.BudgetLimits{}) {
		t.Fatalf("settlement = confirmed %v limits %+v, want unlimited applied", run.BudgetConfirmed, run.Limits)
	}
	if run.Status != shared.StatusActive || !run.Checkpoints[0].Decided {
		t.Fatalf("decision must record with the settlement (status %s)", run.Status)
	}
	evs := readHookTrace(t, "run-1")
	if conf := eventsOfType(evs, shared.EventBudgetConfirmed); len(conf) != 1 || conf[0].ToolUseID != "tu-1" {
		t.Fatalf("budget.confirmed = %+v, want one bound to tu-1", conf)
	}
	if decided := eventsOfType(evs, shared.EventCheckpointDecided); len(decided) != 1 {
		t.Fatalf("checkpoint.decided events = %d, want 1", len(decided))
	}
}

func TestPostHocRuleRejectsBudgetlessContinue(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := openUnsettledFirstCheckpoint(t, r)

	// CONTINUE with NO budget ride at the unsettled first checkpoint: the
	// whole callback rejects, NOTHING records.
	q, qtext := cpQuestion(cp.Nonce, false, cp.MenuPages[0].Labels...)
	if code, stderr := callHook(t, askDecision(t, "tu-1", q, qtext, cp.MenuPages[0].Labels[0], nil)); code != 0 {
		t.Fatalf("hook exit = %d (stderr %q)", code, stderr)
	}
	run := reloadHookRun(t)
	if run.Checkpoints[0].Decided || run.BudgetConfirmed || run.Status != shared.StatusWaitUser {
		t.Fatalf("the budget-less CONTINUE must record NOTHING (status %s decided %v confirmed %v)",
			run.Status, run.Checkpoints[0].Decided, run.BudgetConfirmed)
	}
	evs := readHookTrace(t, "run-1")
	rejected := eventsOfType(evs, shared.EventCheckpointDecisionRejected)
	if len(rejected) != 1 || !strings.Contains(rejected[0].Payload["reason"].(string), "budget answer") {
		t.Fatalf("decision_rejected = %+v, want the post-hoc budget reason", rejected)
	}
	if n := len(eventsOfType(evs, shared.EventCheckpointDecided)); n != 0 {
		t.Fatalf("checkpoint.decided events = %d, want 0", n)
	}

	// STOP is exempt (terminal exits are never budget hostages): it records
	// without a budget answer — and lands POST_GAME_PENDING with the minimal
	// journal hook-side.
	cp2 := run.CurrentCheckpoint()
	if code, stderr := callHook(t, askDecision(t, "tu-2",
		mustQuestion(t, cp2.Nonce, cp2.MenuPages[0].Labels), questionTextOf(cp2.Nonce),
		labelByKeyword(t, cp2.MenuPages[0].Labels, "stop"), nil)); code != 0 {
		t.Fatalf("stop exit = %d (stderr %q)", code, stderr)
	}
	run = reloadHookRun(t)
	if run.Status != shared.StatusPostGamePending || run.Checkpoints[0].Decision != shared.DecisionStop {
		t.Fatalf("STOP must record without settlement (status %s)", run.Status)
	}
}

// labelByKeyword picks the offered label whose leading keyword matches.
func labelByKeyword(t *testing.T, labels []string, keyword string) string {
	t.Helper()
	for _, l := range labels {
		fields := strings.Fields(l)
		if len(fields) > 0 && strings.TrimRight(strings.ToLower(fields[0]), ":?!") == keyword {
			return l
		}
	}
	t.Fatalf("no %q label among %v", keyword, labels)
	return ""
}

// mustQuestion builds a decision question object for a nonce without the
// answers plumbing of cpQuestion (same text form).
func mustQuestion(t *testing.T, nonce string, labels []string) map[string]interface{} {
	t.Helper()
	q, _ := cpQuestion(nonce, false, labels...)
	return q
}

func questionTextOf(nonce string) string {
	q, text := cpQuestion(nonce, false)
	_ = q
	return text
}

func TestBudgetRidePartialAnswer(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := openUnsettledFirstCheckpoint(t, r)
	ride, rtext := budgetQuestion(cp)

	// The budget answered, the DECISION question not answered: the settlement
	// records (T17 partial-answer semantics), the decision rejects, the
	// checkpoint stays open.
	rideOnly := postUsePayload(t, "AskUserQuestion", "tu-1",
		map[string]interface{}{"questions": []interface{}{mustQuestion(t, cp.Nonce, cp.MenuPages[0].Labels), ride}},
		map[string]interface{}{"answers": map[string]interface{}{rtext: cp.BudgetAsk.ConfirmLabel}})
	if code, stderr := callHook(t, rideOnly); code != 0 {
		t.Fatalf("hook exit = %d (stderr %q)", code, stderr)
	}
	run := reloadHookRun(t)
	if !run.BudgetConfirmed || run.Checkpoints[0].Decided || run.Status != shared.StatusWaitUser {
		t.Fatalf("partial answer: confirmed %v decided %v status %s — settlement records, decision does not",
			run.BudgetConfirmed, run.Checkpoints[0].Decided, run.Status)
	}
	evs := readHookTrace(t, "run-1")
	if n := len(eventsOfType(evs, shared.EventBudgetConfirmed)); n != 1 {
		t.Fatalf("budget.confirmed events = %d, want 1", n)
	}
	if n := len(eventsOfType(evs, shared.EventCheckpointDecisionRejected)); n != 1 {
		t.Fatalf("decision_rejected events = %d, want 1", n)
	}
}

func TestBudgetRideForeignAnswerRejectsWholeCallback(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := openUnsettledFirstCheckpoint(t, r)
	ride, rtext := budgetQuestion(cp)

	if code, stderr := callHook(t, budgetedDecision(t, "tu-1", cp, []map[string]interface{}{ride},
		map[string]interface{}{rtext: "budget: 999"})); code != 0 {
		t.Fatalf("hook exit = %d (stderr %q)", code, stderr)
	}
	run := reloadHookRun(t)
	if run.Checkpoints[0].Decided || run.BudgetConfirmed {
		t.Fatal("a foreign budget label must reject the WHOLE callback (nothing recorded)")
	}
	evs := readHookTrace(t, "run-1")
	if n := len(eventsOfType(evs, shared.EventBudgetConfirmed)); n != 0 {
		t.Fatalf("budget.confirmed events = %d, want 0", n)
	}
}

func TestExtendRideAppliesPersistedDelta(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	cp := openUnsettledFirstCheckpoint(t, r)
	// Confirm a real limit with the decision.
	ride, rtext := budgetQuestion(cp)
	if code, stderr := callHook(t, budgetedDecision(t, "tu-1", cp, []map[string]interface{}{ride},
		map[string]interface{}{rtext: cp.BudgetAsk.ConfirmLabel})); code != 0 {
		t.Fatalf("settle exit = %d (stderr %q)", code, stderr)
	}
	run := reloadHookRun(t)
	// The persisted proposal is the framework-unlimited one in this fixture —
	// set a real limit through the settled surface and burn to exhaustion.
	if !run.BudgetConfirmed {
		t.Fatal("fixture must be settled")
	}
	if err := run.OpenPhase("diagnosis"); err != nil {
		t.Fatal(err)
	}
	run.Limits.PhaseToolCalls = 1
	run.CountToolCall()
	cp2, err := run.SubmitPhase(shared.PhaseSubmission{PhaseID: "diagnosis"})
	if err != nil {
		t.Fatal(err)
	}
	if cp2.ExtendAsk == nil {
		t.Fatal("the exhaustion checkpoint must carry the extension offer")
	}
	if err := run.MarkDisplayed(cp2.ID, shared.CheckpointContentDigest(cp2, run.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	cur := run.CurrentCheckpoint()
	extendText := "Extend the budget?"
	extendQ := map[string]interface{}{
		"question": extendText,
		"header":   "Extend",
		"options": []interface{}{
			map[string]interface{}{"label": cur.ExtendAsk.ExtendLabel},
			map[string]interface{}{"label": cur.ExtendAsk.HoldLabel},
		},
	}
	if code, stderr := callHook(t, budgetedDecision(t, "tu-2", cur, []map[string]interface{}{extendQ},
		map[string]interface{}{extendText: cur.ExtendAsk.ExtendLabel})); code != 0 {
		t.Fatalf("extend+decision exit = %d (stderr %q)", code, stderr)
	}
	run = reloadHookRun(t)
	if len(run.BudgetExtensions) != 1 || run.Limits.PhaseToolCalls != 2 || !run.Checkpoints[1].Decided {
		t.Fatalf("extension result: receipts %d limits %+v decided %v", len(run.BudgetExtensions), run.Limits, run.Checkpoints[1].Decided)
	}
	evs := readHookTrace(t, "run-1")
	if ext := eventsOfType(evs, shared.EventBudgetExtended); len(ext) != 1 || ext[0].ToolUseID != "tu-2" {
		t.Fatalf("budget.extended = %+v, want one bound to tu-2", ext)
	}
}

// --- post-game observations --------------------------------------------------------

// pgAnswers builds a questionnaire response for the given marker→answer map
// over one question set (texts keyed by marker).
func pgQuestions(runID string, markers []string) (qs []interface{}, texts map[string]string) {
	texts = map[string]string{}
	for _, m := range markers {
		text := "Post-game " + m + " (run " + runID + "): how did it go? [pg:" + m + "]"
		texts[m] = text
		qs = append(qs, map[string]interface{}{
			"question": text,
			"header":   "Post-game",
			"options": []interface{}{
				map[string]interface{}{"label": "well: 顺利"},
				map[string]interface{}{"label": "roughly: 一般"},
			},
		})
	}
	return qs, texts
}

// runIntoPostGame drives the run to POST_GAME_PENDING via DONE.
func runIntoPostGame(t *testing.T) *shared.RunState {
	t.Helper()
	r := startRun(t)
	cp := openUnsettledFirstCheckpoint(t, r)
	ride, rtext := budgetQuestion(cp)
	if code, stderr := callHook(t, budgetedDecision(t, "tu-1", cp, []map[string]interface{}{ride},
		map[string]interface{}{rtext: cp.BudgetAsk.UnlimitedLabel})); code != 0 {
		t.Fatalf("settle exit = %d (stderr %q)", code, stderr)
	}
	r = reloadHookRun(t)
	if err := r.OpenPhase("diagnosis"); err != nil {
		t.Fatal(err)
	}
	cp2, err := r.SubmitPhase(shared.PhaseSubmission{PhaseID: "diagnosis"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.MarkDisplayed(cp2.ID, shared.CheckpointContentDigest(cp2, r.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	cur := r.CurrentCheckpoint()
	q, qtext := cpQuestion(cur.Nonce, false, cur.MenuPages[0].Labels...)
	if code, stderr := callHook(t, askDecision(t, "tu-done", q, qtext, labelByKeyword(t, cur.MenuPages[0].Labels, "done"), nil)); code != 0 {
		t.Fatalf("done exit = %d (stderr %q)", code, stderr)
	}
	r = reloadHookRun(t)
	if r.Status != shared.StatusPostGamePending {
		t.Fatalf("fixture status = %s, want POST_GAME_PENDING", r.Status)
	}
	return r
}

func reloadHookRun(t *testing.T) *shared.RunState {
	t.Helper()
	r, err := shared.LoadRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPostGameMarkerObservation(t *testing.T) {
	useHookEnv(t)
	r := runIntoPostGame(t)

	markers := shared.PostGameMarkers()
	qs, texts := pgQuestions(r.RunID, markers)
	answers := map[string]interface{}{}
	for _, m := range markers {
		answers[texts[m]] = "well: 顺利"
	}
	if code, stderr := callHook(t, postUsePayload(t, "AskUserQuestion", "tu-pg-1",
		map[string]interface{}{"questions": qs}, map[string]interface{}{"answers": answers})); code != 0 {
		t.Fatalf("questionnaire exit = %d (stderr %q)", code, stderr)
	}
	run := reloadHookRun(t)
	if len(run.PostGame.Answers) != len(markers) {
		t.Fatalf("answers = %v, want all four markers observed", run.PostGame.Answers)
	}
	if run.Status != shared.StatusPostGamePending {
		t.Fatalf("markers alone must not close (status %s)", run.Status)
	}

	// A question WITHOUT the run id observes nothing even when answered: the
	// questionnaire binds to THIS run (a cross-run paste is not an answer).
	foreign := "Post-game q1 (no run id here): how did it go? [pg:q1]"
	foreignQ := []interface{}{map[string]interface{}{
		"question": foreign, "header": "Post-game",
		"options": []interface{}{
			map[string]interface{}{"label": "well: 顺利"},
			map[string]interface{}{"label": "roughly: 一般"},
		},
	}}
	useHookEnv(t) // a fresh POST_GAME_PENDING run in a fresh env
	_ = runIntoPostGame(t)
	if code, stderr := callHook(t, postUsePayload(t, "AskUserQuestion", "tu-pg-x",
		map[string]interface{}{"questions": foreignQ},
		map[string]interface{}{"answers": map[string]interface{}{foreign: "well: 顺利"}})); code != 0 {
		t.Fatalf("foreign questionnaire exit = %d (stderr %q)", code, stderr)
	}
	run = reloadHookRun(t)
	if len(run.PostGame.Answers) != 0 {
		t.Fatalf("answers without the run id = %v, want none", run.PostGame.Answers)
	}
}

func TestPostGameAfkAndJournalClosure(t *testing.T) {
	useHookEnv(t)
	r := runIntoPostGame(t)
	markers := shared.PostGameMarkers()

	// afk-resolved: not an answer.
	qs, texts := pgQuestions(r.RunID, markers)
	afkAnswers := map[string]interface{}{}
	for _, m := range markers {
		afkAnswers[texts[m]] = "well: 顺利"
	}
	afk := map[string]interface{}{"afkTimeoutMs": 30000, "answers": afkAnswers}
	if code, stderr := callHook(t, postUsePayload(t, "AskUserQuestion", "tu-pg-afk",
		map[string]interface{}{"questions": qs}, afk)); code != 0 {
		t.Fatalf("afk questionnaire exit = %d (stderr %q)", code, stderr)
	}
	run := reloadHookRun(t)
	if len(run.PostGame.Answers) != 0 {
		t.Fatalf("afk answers = %v, want none observed", run.PostGame.Answers)
	}

	// Real answers: all four markers.
	if code, stderr := callHook(t, postUsePayload(t, "AskUserQuestion", "tu-pg-1",
		map[string]interface{}{"questions": qs}, map[string]interface{}{"answers": afkAnswers})); code != 0 {
		t.Fatalf("questionnaire exit = %d (stderr %q)", code, stderr)
	}

	// A journal Write at a DIFFERENT path observes nothing.
	if code, stderr := callHook(t, postUsePayload(t, "Write", "tu-jr-x",
		map[string]interface{}{"file_path": "/tmp/not-the-journal.md", "content": "x"},
		map[string]interface{}{})); code != 0 {
		t.Fatalf("foreign write exit = %d (stderr %q)", code, stderr)
	}
	run = reloadHookRun(t)
	if run.PostGame.Journal != nil || run.Status != shared.StatusPostGamePending {
		t.Fatalf("a foreign write must observe nothing (journal %+v status %s)", run.PostGame.Journal, run.Status)
	}

	// The journal at the FIXED path: observation → closure.
	path := shared.JournalPath(r.RunID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("journal body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stderr := callHook(t, postUsePayload(t, "Write", "tu-jr-1",
		map[string]interface{}{"file_path": path, "content": "journal body"},
		map[string]interface{}{})); code != 0 {
		t.Fatalf("journal write exit = %d (stderr %q)", code, stderr)
	}
	run = reloadHookRun(t)
	if run.Status != shared.StatusClosed {
		t.Fatalf("status = %s, want CLOSED after the last observation", run.Status)
	}
	evs := readHookTrace(t, "run-1")
	if n := len(eventsOfType(evs, shared.EventPostGameCompleted)); n != 1 {
		t.Fatalf("post_game.completed events = %d, want 1", n)
	}
	if n := len(eventsOfType(evs, shared.EventWorkflowEnded)); n != 1 {
		t.Fatalf("workflow.ended events = %d, want 1", n)
	}
	if n := len(eventsOfType(evs, shared.EventPostGameStarted)); n != 1 {
		t.Fatalf("post_game.started events = %d, want 1", n)
	}

	// CLOSED: the hook is a pass-through for the run (no live runs) — a later
	// journal edit appends nothing.
	if err := os.WriteFile(path, []byte("journal body — edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stderr := callHook(t, postUsePayload(t, "Edit", "tu-jr-2",
		map[string]interface{}{"file_path": path, "old_string": "journal body", "new_string": "journal body — edited"},
		map[string]interface{}{})); code != 0 {
		t.Fatalf("post-closed edit exit = %d (stderr %q)", code, stderr)
	}
	if n := len(eventsOfType(readHookTrace(t, "run-1"), shared.EventWorkflowEnded)); n != 1 {
		t.Fatalf("workflow.ended after closed-run edit = %d, want still 1", n)
	}
	if run2 := reloadHookRun(t); run2.Status != shared.StatusClosed {
		t.Fatal("a closed run must never reopen")
	}
}
