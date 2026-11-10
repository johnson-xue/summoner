package main

// main_test.go — Task 8 TDD suite: the read-only status viewer (spec §4 只读视图
// + controller Ruling 4). ONE output format serves (a) the full status view and
// (b) the pending-checkpoint block the model embeds verbatim — the Stop hook's
// display verification accepts any message carrying the checkpoint id + nonce,
// which this render guarantees by construction. Hermeticity follows the sibling
// hook suites: SUMMONER_STATE_DIR / SUMMONER_TRACE_DIR / CLAUDE_PROJECT_DIR /
// CLAUDE_CONFIG_DIR into t.TempDir (go1.16: no t.Setenv), plus the injectable
// contract.SandboxProbe so the default probe never touches the real home.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnson-xue/summoner/hooks/contract"
	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// --- hermetic environment --------------------------------------------------------

// setenv16 — t.Setenv is go1.17+; this repo pins go1.16.
func setenv16(t *testing.T, key, val string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	if err := os.Setenv(key, val); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	})
}

// useStatusEnv redirects v2 state, traces and the project key into fresh temp
// dirs so no test ever touches real Summoner state.
func useStatusEnv(t *testing.T) {
	t.Helper()
	setenv16(t, "SUMMONER_STATE_DIR", t.TempDir())
	setenv16(t, "SUMMONER_TRACE_DIR", t.TempDir())
	setenv16(t, "CLAUDE_PROJECT_DIR", t.TempDir())
}

// stubProbe pins the injectable sandbox probe (contract.SandboxProbe is the
// package var hooks/contract ships exactly for this).
func stubProbe(t *testing.T, sandboxed bool) {
	t.Helper()
	old := contract.SandboxProbe
	contract.SandboxProbe = func() bool { return sandboxed }
	t.Cleanup(func() { contract.SandboxProbe = old })
}

// useSandboxSettings points CLAUDE_CONFIG_DIR at a temp config dir, optionally
// carrying a settings.json with sandbox.enabled=true. enabled=false writes NO
// settings file (the unconfigured case).
func useSandboxSettings(t *testing.T, enabled bool) {
	t.Helper()
	dir := t.TempDir()
	if enabled {
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"sandbox":{"enabled":true}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setenv16(t, "CLAUDE_CONFIG_DIR", dir)
}

// callStatus runs the whole viewer on a payload; returns (exit, stdout, stderr).
func callStatus(t *testing.T, payload string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runStatus(strings.NewReader(payload), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// --- fixtures ---------------------------------------------------------------------

const sess = "sess-1"

func limits() shared.BudgetLimits {
	return shared.BudgetLimits{PhaseToolCalls: 40, FailedChecks: 3, RunRework: 2, Delegations: 5}
}

func newRun(t *testing.T) *shared.RunState {
	t.Helper()
	r, err := shared.StartRun("run-1", sess, "fix", limits())
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	return r
}

func decide(t *testing.T, r *shared.RunState, cp shared.CheckpointState, d shared.Decision) {
	t.Helper()
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatalf("MarkDisplayed(%s): %v", cp.ID, err)
	}
	if err := r.RecordDecision(cp.ID, d, ""); err != nil {
		t.Fatalf("RecordDecision(%s, %s): %v", cp.ID, d, err)
	}
}

// waitUserDiagnosis drives fix to its second checkpoint: context accepted,
// diagnosis submitted (WAIT_USER, undisplayed cp-2), counters/stale set for the
// render pins.
func waitUserDiagnosis(t *testing.T) (*shared.RunState, shared.CheckpointState) {
	t.Helper()
	r := newRun(t)
	if err := r.OpenPhase("context"); err != nil {
		t.Fatal(err)
	}
	cp1, err := r.SubmitPhase(shared.PhaseSubmission{
		PhaseID:     "context",
		Summary:     "memory retrieval done",
		Artifacts:   []shared.ArtifactRef{{Path: "docs/ctx.md", Digest: "c0ffee0000000000000000000000", Kind: "notes"}},
		CriteriaIDs: []string{"crit-ctx"},
	})
	if err != nil {
		t.Fatal(err)
	}
	decide(t, r, cp1, shared.DecisionContinue)
	if err := r.OpenPhase("diagnosis"); err != nil {
		t.Fatal(err)
	}
	cp2, err := r.SubmitPhase(shared.PhaseSubmission{
		PhaseID:     "diagnosis",
		Summary:     "root cause located",
		Artifacts:   []shared.ArtifactRef{{Path: "docs/diag.md", Digest: "a1b2c3d4e5f6a7b8", Kind: "report"}},
		CriteriaIDs: []string{"crit-root", "crit-evid"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// OpenPhase reset the per-phase counter; pin the render fixtures post-submit.
	r.Counters.PhaseToolCalls = 4
	r.Counters.Delegations = 1
	r.Stale = []string{"crit-stale-1"}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	return r, cp2
}

func stateFileBytes(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(os.Getenv("SUMMONER_STATE_DIR"), shared.ProjectKey(), sess, "run-1.json"))
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	return b
}

// --- the canonical render (Ruling 4: one format, two uses) ------------------------

// TestRenderWaitUser pins the full WAIT_USER render: nonce, budget fields,
// sandbox tier, waiting-on field and the criteria list — plus the route, visit
// states, artifact versions, default recall and the decision menu whose labels
// LEAD with the decision keywords (Ruling 5).
func TestRenderWaitUser(t *testing.T) {
	useStatusEnv(t)
	useSandboxSettings(t, false)
	stubProbe(t, false)
	_, cp := waitUserDiagnosis(t)

	code, out, stderr := callStatus(t, `{"session_id":"sess-1"}`)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	for _, want := range []string{
		"⚡ SUMMONER STATUS — run run-1 · workflow fix · WAIT_USER",
		"route: context → diagnosis → reproduce → fix → regression → review",
		"- context: accepted",
		"- diagnosis: submitted (awaiting decision)",
		"docs/diag.md @ a1b2c3d4e5f6",
		"- crit-stale-1 (stale)",
		"awaiting: user decision at checkpoint cp-2",
		"default recall: context (latest accepted prior phase)",
		"budget: phase_tool_calls 4/40 · failed_checks 0/3 · run_rework 0/2 · delegations 1/5",
		"sandbox: L1（未配置沙箱）",
		"⚡ SUMMONER CHECKPOINT cp-2 — nonce " + cp.Nonce + " — PHASE",
		"phase: diagnosis",
		"criteria: crit-evid, crit-root",
		"menu:",
		"continue:",
		"skip:",
		"done:",
		"recall:",
		"review:",
		"stop:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\n--- render ---\n%s", want, out)
		}
	}
	// The render satisfies the Stop hook's display verification as-is: the
	// checkpoint id and nonce both appear (hasRenderBlock contract).
	if !strings.Contains(out, cp.ID) || !strings.Contains(out, cp.Nonce) {
		t.Fatalf("render must carry checkpoint id %s and nonce %s verbatim", cp.ID, cp.Nonce)
	}
}

// TestRenderUnlimitedBudget: zero-value limits render as unlimited.
func TestRenderUnlimitedBudget(t *testing.T) {
	useStatusEnv(t)
	useSandboxSettings(t, false)
	stubProbe(t, false)
	r, err := shared.StartRun("run-1", sess, "fix", shared.BudgetLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.OpenPhase("context"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SubmitPhase(shared.PhaseSubmission{PhaseID: "context"}); err != nil {
		t.Fatal(err)
	}
	_, out, _ := callStatus(t, `{"session_id":"sess-1"}`)
	if !strings.Contains(out, "budget: phase_tool_calls 0/∞ · failed_checks 0/∞ · run_rework 0/∞ · delegations 0/∞") {
		t.Fatalf("unlimited budget line missing:\n%s", out)
	}
}

// TestRenderMenusByKind: menus mirror shared.legalDecisionForKind — phase takes
// the full set, change-confirm only {accept, stop}, recovery only
// {continue, recall, stop}, review-result the full default set.
func TestRenderMenusByKind(t *testing.T) {
	// menu options render as two-space-indented "  keyword: label" lines.
	has := func(out string, kw string) bool { return strings.Contains(out, "  "+kw+":") }

	t.Run("change-confirm", func(t *testing.T) {
		useStatusEnv(t)
		r := newRun(t)
		if err := r.OpenRequirementChange("rename field"); err != nil {
			t.Fatal(err)
		}
		if _, err := r.ApplyRequirementDelta(shared.RequirementDelta{Changed: []string{"crit-1"}}); err != nil {
			t.Fatal(err)
		}
		out := renderStatus(r, "L1（未配置沙箱）")
		if !has(out, "accept") || !has(out, "stop") {
			t.Fatalf("change-confirm menu must offer accept+stop:\n%s", out)
		}
		for _, kw := range []string{"continue", "skip", "done", "recall", "review"} {
			if has(out, kw) {
				t.Fatalf("change-confirm menu must not offer %s:\n%s", kw, out)
			}
		}
		if !strings.Contains(out, "CHANGE_CONFIRM") {
			t.Fatalf("checkpoint kind missing:\n%s", out)
		}
	})

	t.Run("recovery", func(t *testing.T) {
		useStatusEnv(t)
		r := newRun(t)
		r.MarkOutcomeUnknown("tu-1")
		if err := r.Save(); err != nil {
			t.Fatal(err)
		}
		if err := r.ResolveOutcome("tu-1", shared.OutcomeFailed); err != nil {
			t.Fatal(err)
		}
		out := renderStatus(r, "L2")
		for _, kw := range []string{"continue", "recall", "stop"} {
			if !has(out, kw) {
				t.Fatalf("recovery menu must offer %s:\n%s", kw, out)
			}
		}
		for _, kw := range []string{"skip", "done", "review", "accept"} {
			if has(out, kw) {
				t.Fatalf("recovery menu must not offer %s:\n%s", kw, out)
			}
		}
		if !strings.Contains(out, "RECOVERY") {
			t.Fatalf("checkpoint kind missing:\n%s", out)
		}
	})

	t.Run("review-result", func(t *testing.T) {
		useStatusEnv(t)
		r := newRun(t)
		env := shared.Envelope{
			SchemaVersion: "2",
			RunID:         "run-1",
			ReviewID:      "rev-1",
			ArtifactSet:   []shared.ArtifactRef{{Path: "docs/diag.md", Digest: "a1b2c3d4e5f6a7b8"}},
		}
		if _, err := r.RequestReview(env); err != nil {
			t.Fatal(err)
		}
		if err := r.RecordReviewResult(shared.ReviewResult{
			ReviewID:            "rev-1",
			ArtifactDigest:      "a1b2c3d4e5f6a7b8",
			RunStatus:           shared.ReviewRunCompleted,
			Verdict:             shared.VerdictPass,
			EvidenceToolCallIDs: []string{"toolu_1"},
		}); err != nil {
			t.Fatal(err)
		}
		out := renderStatus(r, "L1（未配置沙箱）")
		for _, kw := range []string{"continue", "skip", "done", "recall", "review", "stop"} {
			if !has(out, kw) {
				t.Fatalf("review-result menu must offer %s (note text guides the choice):\n%s", kw, out)
			}
		}
		if has(out, "accept") {
			t.Fatalf("review-result menu must not offer accept:\n%s", out)
		}
		if !strings.Contains(out, "REVIEW_PENDING") && !strings.Contains(out, "COMPLETED PASS") {
			// status line is WAIT_USER; the note carries the verdict instead
			if !strings.Contains(out, "WAIT_USER") {
				t.Fatalf("review-result render must show WAIT_USER and the verdict note:\n%s", out)
			}
		}
	})
}

// TestRenderAwaitingFields: the waiting-on line names the counterpart for each
// run posture — ACTIVE phase work, REVIEW_PENDING reviewer, OUTCOME_UNKNOWN
// probes, POST_GAME_PENDING questionnaire, CHANGE_OPEN delta.
func TestRenderAwaitingFields(t *testing.T) {
	t.Run("active phase work", func(t *testing.T) {
		useStatusEnv(t)
		r := newRun(t)
		if err := r.OpenPhase("context"); err != nil {
			t.Fatal(err)
		}
		out := renderStatus(r, "L2")
		if !strings.Contains(out, "awaiting: phase work in \"context\"") {
			t.Fatalf("active render:\n%s", out)
		}
	})
	t.Run("review pending", func(t *testing.T) {
		useStatusEnv(t)
		r := newRun(t)
		env := shared.Envelope{
			SchemaVersion: "2", RunID: "run-1", ReviewID: "rev-9",
			ArtifactSet: []shared.ArtifactRef{{Path: "a.go", Digest: "d1"}},
		}
		if _, err := r.RequestReview(env); err != nil {
			t.Fatal(err)
		}
		out := renderStatus(r, "L2")
		if !strings.Contains(out, "awaiting: reviewer rev-9") {
			t.Fatalf("review-pending render:\n%s", out)
		}
	})
	t.Run("outcome unknown", func(t *testing.T) {
		useStatusEnv(t)
		r := newRun(t)
		r.MarkOutcomeUnknown("tu-x")
		if err := r.Save(); err != nil {
			t.Fatal(err)
		}
		out := renderStatus(r, "L2")
		if !strings.Contains(out, "awaiting: reconciliation probes (1 unresolved)") {
			t.Fatalf("outcome-unknown render:\n%s", out)
		}
	})
	t.Run("post-game pending", func(t *testing.T) {
		useStatusEnv(t)
		r := newRun(t)
		r.Status = shared.StatusPostGamePending
		if err := r.Save(); err != nil {
			t.Fatal(err)
		}
		out := renderStatus(r, "L2")
		if !strings.Contains(out, "awaiting: post-game questionnaire") {
			t.Fatalf("post-game render:\n%s", out)
		}
	})
	t.Run("change open", func(t *testing.T) {
		useStatusEnv(t)
		r := newRun(t)
		if err := r.OpenRequirementChange("tweak"); err != nil {
			t.Fatal(err)
		}
		out := renderStatus(r, "L2")
		if !strings.Contains(out, "awaiting: requirement change open") {
			t.Fatalf("change-open render:\n%s", out)
		}
	})
}

// TestRenderUnknownWorkflowRoute: a workflow outside WorkflowPhases renders its
// route from the run's own visits instead of failing.
func TestRenderUnknownWorkflowRoute(t *testing.T) {
	useStatusEnv(t)
	r, err := shared.StartRun("run-1", sess, "ship", shared.BudgetLimits{})
	if err != nil {
		t.Fatal(err)
	}
	out := renderStatus(r, "L2")
	if !strings.Contains(out, "route: (no framework sequence; visits: none yet)") {
		t.Fatalf("unknown-workflow route line:\n%s", out)
	}
}

// TestSandboxTiers: the tier line wraps contract.ClassifySandbox with the local
// settings read — L2 when the probe confirms, L1 honestly unconfigured, BLOCKED
// when settings claim a sandbox the probe disproves.
func TestSandboxTiers(t *testing.T) {
	t.Run("probe active overrides settings", func(t *testing.T) {
		useStatusEnv(t)
		useSandboxSettings(t, false)
		stubProbe(t, true)
		if got := sandboxTier(); got != "L2" {
			t.Fatalf("tier = %q, want L2", got)
		}
	})
	t.Run("inactive + unconfigured", func(t *testing.T) {
		useStatusEnv(t)
		useSandboxSettings(t, false)
		stubProbe(t, false)
		if got := sandboxTier(); got != "L1（未配置沙箱）" {
			t.Fatalf("tier = %q, want L1（未配置沙箱）", got)
		}
	})
	t.Run("inactive + configured not effective", func(t *testing.T) {
		useStatusEnv(t)
		useSandboxSettings(t, true)
		stubProbe(t, false)
		if got := sandboxTier(); got != "BLOCKED（配置了沙箱但未生效）" {
			t.Fatalf("tier = %q, want BLOCKED（配置了沙箱但未生效）", got)
		}
	})
}

// TestStatusIsReadOnly: rendering never mutates the state file and never
// appends a trace event — the viewer only reads.
func TestStatusIsReadOnly(t *testing.T) {
	useStatusEnv(t)
	useSandboxSettings(t, false)
	stubProbe(t, false)
	waitUserDiagnosis(t)
	before := stateFileBytes(t)

	code, _, _ := callStatus(t, `{"session_id":"sess-1"}`)
	if code != 0 {
		t.Fatal("exit != 0")
	}
	if after := stateFileBytes(t); string(before) != string(after) {
		t.Fatal("state file must be byte-identical after a status render")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("SUMMONER_TRACE_DIR"), shared.ProjectKey(), "run-1.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("status must never write a trace (stat err = %v)", err)
	}
}

// TestStatusNoActiveRun: zero live runs → the brief no-active-run note, exit 0.
func TestStatusNoActiveRun(t *testing.T) {
	useStatusEnv(t)
	useSandboxSettings(t, false)
	stubProbe(t, false)
	code, out, stderr := callStatus(t, `{"session_id":"sess-1"}`)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if !strings.Contains(out, "No active Summoner run") {
		t.Fatalf("out = %q", out)
	}
}

// TestStatusAmbiguousRunsNamesBoth: >1 live run is surfaced, never guessed —
// both run ids appear, exit 0 (read-only viewer never blocks).
func TestStatusAmbiguousRunsNamesBoth(t *testing.T) {
	useStatusEnv(t)
	useSandboxSettings(t, false)
	stubProbe(t, false)
	if _, err := shared.StartRun("run-1", sess, "fix", shared.BudgetLimits{}); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.StartRun("run-2", sess, "fix", shared.BudgetLimits{}); err != nil {
		t.Fatal(err)
	}
	code, out, _ := callStatus(t, `{"session_id":"sess-1"}`)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "run-1") || !strings.Contains(out, "run-2") || !strings.Contains(out, "ambiguous") {
		t.Fatalf("ambiguous render must name both runs:\n%s", out)
	}
}

// TestStatusSessionlessFallback: `{}` (no session id) resolves the project's
// live runs — exactly one renders; two are named.
func TestStatusSessionlessFallback(t *testing.T) {
	useSandboxSettings(t, false)
	stubProbe(t, false)

	t.Run("single live run renders", func(t *testing.T) {
		useStatusEnv(t)
		waitUserDiagnosis(t)
		code, out, _ := callStatus(t, `{}`)
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		if !strings.Contains(out, "⚡ SUMMONER STATUS — run run-1") {
			t.Fatalf("fallback must render the single live run:\n%s", out)
		}
	})
	t.Run("two live runs named", func(t *testing.T) {
		useStatusEnv(t)
		if _, err := shared.StartRun("run-1", "sess-a", "fix", shared.BudgetLimits{}); err != nil {
			t.Fatal(err)
		}
		if _, err := shared.StartRun("run-2", "sess-b", "new", shared.BudgetLimits{}); err != nil {
			t.Fatal(err)
		}
		code, out, _ := callStatus(t, `{}`)
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		if !strings.Contains(out, "run-1") || !strings.Contains(out, "run-2") {
			t.Fatalf("fallback ambiguity must name both:\n%s", out)
		}
	})
}

// TestStatusUnreadableStateIsNotAnInputError: corrupt state is surfaced on
// stdout/stderr but stays exit 0 — exit 2 is reserved for unreadable INPUT.
func TestStatusUnreadableStateIsNotAnInputError(t *testing.T) {
	useStatusEnv(t)
	dir := filepath.Join(os.Getenv("SUMMONER_STATE_DIR"), shared.ProjectKey(), sess)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run-corrupt.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := callStatus(t, `{"session_id":"sess-1"}`)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if !strings.Contains(out+stderr, "unreadable") {
		t.Fatalf("unreadable state must be surfaced, out=%q stderr=%q", out, stderr)
	}
}

// TestStatusParseFailureFailsClosed: unparseable stdin and trailing garbage are
// the one exit-2 path (Ruling 3).
func TestStatusParseFailureFailsClosed(t *testing.T) {
	useStatusEnv(t)
	for name, payload := range map[string]string{
		"not json":       "this is not json",
		"trailing bytes": `{"session_id":"x"} trailing`,
	} {
		var stdout, stderr bytes.Buffer
		code := runStatus(strings.NewReader(payload), &stdout, &stderr)
		if code != 2 {
			t.Errorf("%s: exit = %d, want 2 (stderr: %q)", name, code, stderr)
		}
	}
}

// --- payload decoding --------------------------------------------------------------

// TestParseInputAcceptsHookEnvelope: the viewer parses the same JSON envelope
// the hooks do — extra fields are tolerated, session_id is the one field read.
func TestParseInputAcceptsHookEnvelope(t *testing.T) {
	var in *statusInput
	var err error
	raw := `{"hook_event_name":"UserPromptSubmit","session_id":"s-9","prompt":"/summoner:status"}`
	if in, err = parseInput(strings.NewReader(raw)); err != nil {
		t.Fatalf("parseInput: %v", err)
	}
	if in.SessionID != "s-9" {
		t.Fatalf("session id = %q, want s-9", in.SessionID)
	}
	if in, err = parseInput(strings.NewReader(`{}`)); err != nil || in.SessionID != "" {
		t.Fatalf("empty object must parse with empty session id: %v %+v", err, in)
	}
}
