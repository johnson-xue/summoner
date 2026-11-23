package main

import (
	"strings"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// autopass_test.go — adaptive checkpoints at the gate's submit site (0.7.0
// D3; design acceptance criteria 1–3). A confirmed, evidenced submission on
// a phase that needs no human judgment passes in the same call that
// submitted it: the run returns to ACTIVE with nothing pending and the trace
// records checkpoint.auto — never checkpoint.decided.

// confirmedArgs builds a phase-submit args JSON that satisfies every
// auto-pass condition.
func confirmedArgs(t *testing.T, phaseID string, refs ...string) string {
	t.Helper()
	sub := shared.PhaseSubmission{
		PhaseID: phaseID, Summary: phaseID + " established",
		Outcome: shared.ConclusionConfirmed,
		Conclusion: &shared.Conclusion{
			Summary:  phaseID + " conclusion",
			Evidence: []string{"evidence-1", "evidence-2"},
			Approach: "read the code and ran the existing checks",
		},
		CheckRefs: refs,
	}
	return mustJSONStr(t, sub)
}

// outcomeArgs builds a phase-submit args JSON with the given outcome and
// evidence ("" evidence = none).
func outcomeArgs(t *testing.T, phaseID, outcome string, evidence ...string) string {
	t.Helper()
	return mustJSONStr(t, shared.PhaseSubmission{
		PhaseID: phaseID, Summary: phaseID + " submitted",
		Outcome:    outcome,
		Conclusion: &shared.Conclusion{Summary: phaseID + " conclusion", Evidence: evidence, Approach: "how"},
	})
}

func gateSubmit(t *testing.T, id, args string) (int, string) {
	t.Helper()
	return callGate(t, toolPayloadID(t, "Skill", id, skillInput("summoner:phase-submit", args)))
}

func countType(evs []shared.TraceEvent, typ string) int {
	n := 0
	for _, ev := range evs {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

// TestConfirmedContextSubmissionAutoPasses — acceptance criterion 1 through
// the real gate: after a confirmed context submission the run is ACTIVE
// with no pending checkpoint, the record is accepted by the framework, the
// trace carries exactly one checkpoint.auto and no checkpoint.decided — and
// the model goes straight on to a phase that still asks.
func TestConfirmedContextSubmissionAutoPasses(t *testing.T) {
	useGateEnv(t)
	startFixRun(t, shared.BudgetLimits{})

	code, stderr := gateSubmit(t, "tu-ps-1", confirmedArgs(t, "context"))
	if code != 0 {
		t.Fatalf("confirmed context submission: exit %d, want 0 (stderr: %q)", code, stderr)
	}
	after := reloadRun(t, "run-1")
	if after.Status != shared.StatusActive {
		t.Fatalf("status = %s, want ACTIVE (no checkpoint waits for the user)", after.Status)
	}
	if got := after.CurrentCheckpoint(); got != nil {
		t.Fatalf("no checkpoint may stay pending, got %+v", got)
	}
	cp := after.Checkpoints[0]
	if !cp.Decided || cp.Decision != shared.DecisionAutoPass || cp.DecisionToolUseID != "" || cp.Displayed {
		t.Fatalf("the record must read accepted-by-the-framework: decided %v decision %q tool_use_id %q displayed %v",
			cp.Decided, cp.Decision, cp.DecisionToolUseID, cp.Displayed)
	}
	if len(after.Acceptance.Requirements) != 1 || after.Acceptance.Requirements[0].AcceptedBy != shared.AutoPassActor {
		t.Fatalf("the acceptance record must name the framework as the actor: %+v", after.Acceptance.Requirements)
	}

	evs := readGateTrace(t, "run-1")
	want := []string{shared.EventToolStarted, shared.EventPhaseStarted, shared.EventPhaseClosed, shared.EventCheckpointOpened, shared.EventCheckpointAuto}
	if got := traceTypes(evs); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("trace types = %v, want %v", got, want)
	}
	auto := evs[4]
	if auto.Payload["checkpoint_id"] != cp.ID || auto.Payload["phase_id"] != "context" || auto.Payload["outcome"] != shared.ConclusionConfirmed {
		t.Fatalf("checkpoint.auto payload = %v, want the checkpoint, its phase and the confirmed outcome", auto.Payload)
	}
	if auto.ToolUseID != "tu-ps-1" {
		t.Fatalf("checkpoint.auto tool_use_id = %q, want the submitting call's id", auto.ToolUseID)
	}
	if n := countType(evs, shared.EventCheckpointDecided) + countType(evs, shared.EventCheckpointDisplayed); n != 0 {
		t.Fatalf("an auto-pass records no decision and no display, got %d such events", n)
	}

	// The model goes straight on: the next phase opens and its checkpoint
	// is a human one.
	code, stderr = callGate(t, toolPayload(t, "Skill", map[string]interface{}{"skill": "my-debug-skill"}))
	if code != 0 {
		t.Fatalf("provider skill for diagnosis: exit %d, want 0 (stderr: %q)", code, stderr)
	}
	code, stderr = gateSubmit(t, "tu-ps-2", confirmedArgs(t, "diagnosis"))
	if code != 0 {
		t.Fatalf("confirmed diagnosis submission: exit %d, want 0 (stderr: %q)", code, stderr)
	}
	after = reloadRun(t, "run-1")
	if after.Status != shared.StatusWaitUser || after.CurrentCheckpoint() == nil || after.CurrentCheckpoint().Decided {
		t.Fatalf("diagnosis must stop for the user: status %s, checkpoint %+v", after.Status, after.CurrentCheckpoint())
	}
	if n := countType(readGateTrace(t, "run-1"), shared.EventCheckpointAuto); n != 1 {
		t.Fatalf("checkpoint.auto events = %d, want exactly 1 (diagnosis never auto-passes)", n)
	}
}

// TestReproduceAutoPassesOnlyWhenConfirmedWithEvidence — acceptance
// criterion 2: confirmed + evidence passes; a failed outcome and an omitted
// outcome stay on a pending checkpoint.
func TestReproduceAutoPassesOnlyWhenConfirmedWithEvidence(t *testing.T) {
	cases := []struct {
		name  string
		args  func(*testing.T) string
		auto  bool
		outco string
	}{
		{"confirmed with evidence", func(t *testing.T) string {
			return outcomeArgs(t, "reproduce", shared.ConclusionConfirmed, "repro.log:12")
		}, true, ""},
		{"failed", func(t *testing.T) string { return outcomeArgs(t, "reproduce", shared.ConclusionFailed, "repro.log:12") }, false, shared.ConclusionFailed},
		{"outcome omitted", func(t *testing.T) string { return outcomeArgs(t, "reproduce", "", "repro.log:12") }, false, shared.ConclusionUnclear},
	}
	for _, c := range cases {
		useGateEnv(t)
		r := startFixRun(t, shared.BudgetLimits{})
		acceptPhase(t, r, "context")
		acceptPhase(t, r, "diagnosis")
		if err := r.OpenPhase("reproduce"); err != nil {
			t.Fatalf("%s: OpenPhase(reproduce): %v", c.name, err)
		}
		code, stderr := gateSubmit(t, "tu-ps-r", c.args(t))
		if code != 0 {
			t.Fatalf("%s: exit %d, want 0 (stderr: %q)", c.name, code, stderr)
		}
		after := reloadRun(t, "run-1")
		got := countType(readGateTrace(t, "run-1"), shared.EventCheckpointAuto)
		if c.auto {
			if after.Status != shared.StatusActive || after.CurrentCheckpoint() != nil || got != 1 {
				t.Errorf("%s: want ACTIVE, nothing pending, one checkpoint.auto — got %s / %+v / %d", c.name, after.Status, after.CurrentCheckpoint(), got)
			}
			continue
		}
		if after.Status != shared.StatusWaitUser || after.CurrentCheckpoint() == nil || after.CurrentCheckpoint().Decided || got != 0 {
			t.Errorf("%s: want WAIT_USER on an undecided checkpoint and no checkpoint.auto — got %s / %+v / %d", c.name, after.Status, after.CurrentCheckpoint(), got)
		}
		if cp := after.CurrentCheckpoint(); cp.Outcome != c.outco {
			t.Errorf("%s: stored outcome = %q, want %q", c.name, cp.Outcome, c.outco)
		}
	}
}

// TestExhaustionCheckpointNeverAutoPasses — acceptance criterion 3 (the
// review's B1): a checkpoint that carries the budget extension question
// stays with the user — passing it would swallow the budget decision and
// drop the run back into "exhausted, cannot work".
func TestExhaustionCheckpointNeverAutoPasses(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{PhaseToolCalls: 1})
	if err := r.OpenPhase("context"); err != nil {
		t.Fatal(err)
	}
	r.CountToolCall("tu-pre")
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}

	code, stderr := gateSubmit(t, "tu-ps-1", confirmedArgs(t, "context")) // phase-submit is budget-exempt
	if code != 0 {
		t.Fatalf("exit %d, want 0 (stderr: %q)", code, stderr)
	}
	after := reloadRun(t, "run-1")
	cp := after.CurrentCheckpoint()
	if cp == nil || cp.ExtendAsk == nil {
		t.Fatalf("fixture: the checkpoint must open at exhaustion and carry the extend offer, got %+v", cp)
	}
	if after.Status != shared.StatusWaitUser || cp.Decided {
		t.Fatalf("an exhaustion checkpoint must stay with the user: status %s decided %v", after.Status, cp.Decided)
	}
	if n := countType(readGateTrace(t, "run-1"), shared.EventCheckpointAuto); n != 0 {
		t.Fatalf("checkpoint.auto events = %d, want 0", n)
	}
}

// TestFailedCheckStopsTheAutoPass — acceptance criterion 3: a confirmed
// claim that one of its OWN mechanical checks contradicts owes the human
// gate; the same submission with a passing check passes.
func TestFailedCheckStopsTheAutoPass(t *testing.T) {
	for _, c := range []struct {
		name  string
		ref   string
		auto  bool
		fails bool
	}{
		{"passing check", "check://report-has", true, false},
		{"failing check", "check://count-tags", false, true},
	} {
		useGateEnv(t)
		writeLadderManifest(t,
			ladderCheck("report-has", shared.CheckOpFileContains, []string{"report.md", "needle"}, "framework"),
			ladderCheck("count-tags", shared.CheckOpGrepCount, []string{"report.md", "tag", "3"}, "framework"),
		)
		writeL1Artifact(t, "report.md", "needle here\ntag one\ntag two\n") // 2 tags: count-tags FAILS
		startFixRun(t, shared.BudgetLimits{})

		code, stderr := gateSubmit(t, "tu-ps-1", confirmedArgs(t, "context", c.ref))
		if code != 0 {
			t.Fatalf("%s: exit %d, want 0 — a FAIL never un-submits (stderr: %q)", c.name, code, stderr)
		}
		after := reloadRun(t, "run-1")
		got := countType(readGateTrace(t, "run-1"), shared.EventCheckpointAuto)
		if c.auto && (after.Status != shared.StatusActive || got != 1) {
			t.Errorf("%s: want ACTIVE with one checkpoint.auto, got %s / %d", c.name, after.Status, got)
		}
		if !c.auto && (after.Status != shared.StatusWaitUser || after.CurrentCheckpoint() == nil || after.CurrentCheckpoint().Decided || got != 0) {
			t.Errorf("%s: a FAIL must stop the auto-pass — got %s / %+v / %d checkpoint.auto", c.name, after.Status, after.CurrentCheckpoint(), got)
		}
		if got := after.Counters.FailedChecks == 1; got != c.fails {
			t.Errorf("%s: FailedChecks ticked = %v, want %v", c.name, got, c.fails)
		}
	}
}

// TestDiagnosisAndFixNeverAutoPass: the two phases that carry the user's
// judgment always stop, however confident the submission.
func TestDiagnosisAndFixNeverAutoPass(t *testing.T) {
	for _, phase := range []string{"diagnosis", "fix"} {
		useGateEnv(t)
		r := startFixRun(t, shared.BudgetLimits{})
		acceptPhase(t, r, "context")
		if phase == "fix" {
			acceptPhase(t, r, "diagnosis")
			acceptPhase(t, r, "reproduce")
		}
		if err := r.OpenPhase(phase); err != nil {
			t.Fatalf("OpenPhase(%s): %v", phase, err)
		}
		code, stderr := gateSubmit(t, "tu-ps-1", confirmedArgs(t, phase))
		if code != 0 {
			t.Fatalf("%s: exit %d, want 0 (stderr: %q)", phase, code, stderr)
		}
		after := reloadRun(t, "run-1")
		if after.Status != shared.StatusWaitUser || after.CurrentCheckpoint() == nil || after.CurrentCheckpoint().Decided {
			t.Errorf("%s: a confirmed submission must still stop for the user — got %s / %+v", phase, after.Status, after.CurrentCheckpoint())
		}
	}
}

// TestBlankEvidenceDoesNotAutoPass (ship review round 1): a confirmed claim
// whose only "evidence" is blank items stays with the user — through the
// real gate, where the payload was accepted before the fix.
func TestBlankEvidenceDoesNotAutoPass(t *testing.T) {
	for name, args := range map[string]string{
		"blank evidence":   `{"phase_id":"context","outcome":"confirmed","conclusion":{"summary":"context established","evidence":[""],"approach":"read"}}`,
		"blank conclusion": `{"phase_id":"context","outcome":"confirmed","conclusion":{"summary":"  ","evidence":["go.mod:3"],"approach":"read"}}`,
	} {
		useGateEnv(t)
		startFixRun(t, shared.BudgetLimits{})
		code, stderr := gateSubmit(t, "tu-ps-1", args)
		if code != 0 {
			t.Fatalf("%s: exit %d, want 0 — the submission itself is valid (stderr: %q)", name, code, stderr)
		}
		after := reloadRun(t, "run-1")
		if after.Status != shared.StatusWaitUser || after.CurrentCheckpoint() == nil || after.CurrentCheckpoint().Decided {
			t.Errorf("%s: a confirmed claim without real evidence/conclusion must stay with the user — got %s / %+v", name, after.Status, after.CurrentCheckpoint())
		}
		if n := countType(readGateTrace(t, "run-1"), shared.EventCheckpointAuto); n != 0 {
			t.Errorf("%s: checkpoint.auto events = %d, want 0", name, n)
		}
	}
}

// TestWriteClassCheckLosesL2Admission (ship review round 1): a project
// check whose command became write-class in 0.7.0 (`make test`) can no
// longer be an L2 check — the denial says so, instead of leaving a silent
// regression in the registry.
func TestWriteClassCheckLosesL2Admission(t *testing.T) {
	useGateEnv(t)
	writeLadderManifest(t, ladderCheck("make-test", "make", []string{"test"}, "project"))
	startFixRun(t, shared.BudgetLimits{}) // diagnosis not accepted: freeze on

	code, stderr := callGate(t, toolPayload(t, "Bash", bashInput("make test")))
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr: %q)", code, stderr)
	}
	for _, want := range []string{"[写类规则: write-class verb: make]", "make-test", "L2", "read-class"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("denial must carry %q: %q", want, stderr)
		}
	}

	// Read-only fan-out hints: xargs and find -exec are write-class by
	// long-standing design; the denial names the read-class alternative.
	for cmd, hint := range map[string]string{
		"find . -name '*.go' | xargs wc -l":    "xargs",
		"find . -name '*.go' -exec wc -l {} +": "find -exec",
	} {
		code, stderr = callGate(t, toolPayload(t, "Bash", bashInput(cmd)))
		if code != 2 {
			t.Fatalf("%q: exit = %d, want 2", cmd, code)
		}
		if !strings.Contains(stderr, hint) || !strings.Contains(stderr, "grep -r") {
			t.Errorf("%q: the denial should name the write-class rule and the read-class fan-out alternative: %q", cmd, stderr)
		}
	}
}
