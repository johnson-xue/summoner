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
	"runtime"
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
	// 0.7.0 D5.2: a session id inherited from the test process's own
	// environment would scope every `{}` payload — keep the suite hermetic.
	unsetenvStatus(t, "SUMMONER_SESSION_ID")
}

func unsetenvStatus(t *testing.T, key string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	os.Unsetenv(key)
	t.Cleanup(func() {
		if had {
			os.Setenv(key, old)
		}
	})
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
	cp1, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{
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
	cp2, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{
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
	// The ask recipe is model-facing: stderr, not the pasted view (round 2).
	if !strings.Contains(stderr, "model: ") || strings.Contains(out, "model: ") {
		t.Errorf("the model line must be on stderr only (stderr %q)", stderr)
	}
	for _, want := range []string{
		"⚡ SUMMONER STATUS — run run-1 · workflow fix · WAIT_USER",
		"route: context → diagnosis → reproduce → fix → regression → review",
		"- context: accepted",
		"- diagnosis: submitted (awaiting decision)",
		"docs/diag.md @ a1b2c3d4e5f6",
		"- crit-stale-1 (stale)",
		"awaiting: user decision at checkpoint cp-2",
		"default recall: latest → context@v1 (offered: latest, context@v1)",
		"budget: phase_tool_calls 4/40 · failed_checks 0/3 · run_rework 0/2 · delegations 1/5 · run-start 已生效",
		"sandbox: L1（未配置沙箱）",
		// 0.7.0 D1: the mechanism the checkpoint message no longer prints
		// lives in the detail block right before it — complete and uncapped.
		"checkpoint detail (cp-2 — nonce " + cp.Nonce + " — PHASE):",
		"conclusion (unclear 未查清):",
		"summary (phase summary; no structured conclusion was submitted): root cause located",
		// (the fix route merges the six default review axes into every
		// acceptance: 2 submitted + 6 defaults — exactly the table that used
		// to bury the checkpoint message)
		"criteria (8):",
		"crit-architecture [judgment · required]",
		"goal: Changes respect the existing architecture and layering",
		"crit-evid [judgment · required]",
		"artifacts (frozen versions, 1):",
		"docs/diag.md @ a1b2c3d4e5f6a7b8",
		// The environment line reports the HOST (CI runs this suite on Linux).
		"environment: tier=framework os=" + runtime.GOOS,
		"menu pages (3):",
		"page 2/3:",
		"recall targets:",
		"- latest (phase context, visit context@v1)",
		// …and the checkpoint message still ends the default view.
		"⚡ SUMMONER CHECKPOINT cp-2 — nonce " + cp.Nonce + " — PHASE",
		"phase: diagnosis",
		"criteria: crit-evid, crit-root",
		"║ ▶ 结论 — 未查清",
		"║   root cause located",
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
	// The default view ENDS with the canonical checkpoint message, byte for
	// byte: the model pastes the tail (or the whole output — the Stop hook's
	// display binding is a containment match) and nothing follows the frame.
	render, err := shared.LoadRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out, shared.RenderCheckpointMessage(render, cp)) {
		t.Fatalf("the default view must end with the canonical checkpoint message:\n%s", out)
	}
}

// TestRenderChangeOpen pins the CHANGE_OPEN render (T10): with a requirement
// change open and its delta applied (change-confirm pending), the render
// carries the SAVED development position (the freeze the loop restores), the
// requirements version the delta bumped, the stale-criteria list, and a
// 「等待变更确认」 waiting-on line — plus the CHANGE_CONFIRM checkpoint block.
func TestRenderChangeOpen(t *testing.T) {
	useStatusEnv(t)
	useSandboxSettings(t, false)
	stubProbe(t, false)
	r := newRun(t)
	if err := r.OpenPhase("diagnosis"); err != nil {
		t.Fatal(err)
	}
	// Mid-phase change: the freeze captures the live position at open.
	r.DevPosition = shared.DevPosition{
		CurrentTask:          "diagnosis: nil deref at login.go:88",
		TestProgress:         "repro test drafted, not yet run",
		PendingVerifications: []string{"reproduce run", "root-cause evidence digest"},
	}
	if err := r.OpenRequirementChange("user wants logout flow covered too"); err != nil {
		t.Fatal(err)
	}
	report, err := r.ApplyRequirementDelta(shared.RequirementDelta{
		Note:    "scope: login → login+logout",
		Changed: []string{"crit-root"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.RequirementsVersion != 2 {
		t.Fatalf("requirements version after delta = %d, want 2", report.RequirementsVersion)
	}

	out := renderStatus(r, "L1（未配置沙箱）")
	for _, want := range []string{
		"requirement change (CHANGE_OPEN):",
		"note: user wants logout flow covered too",
		"saved position: current_task: diagnosis: nil deref at login.go:88",
		"test_progress: repro test drafted, not yet run",
		"pending_verifications: reproduce run, root-cause evidence digest",
		"requirements version: 2",
		"- crit-root (stale)",
		"awaiting: 等待变更确认",
		"CHANGE_CONFIRM",
		"accept: 接受变更并恢复开发",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("CHANGE_OPEN render missing %q\n--- render ---\n%s", want, out)
		}
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
	if _, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "context"}); err != nil {
		t.Fatal(err)
	}
	_, out, _ := callStatus(t, `{"session_id":"sess-1"}`)
	if !strings.Contains(out, "budget: phase_tool_calls 0/∞ · failed_checks 0/∞ · run_rework 0/∞ · delegations 0/∞ · run-start 已生效") {
		t.Fatalf("unlimited budget line missing:\n%s", out)
	}
}

// TestRenderBudgetAsks pins the T18 (0.6.1) checkpoint render: the FIRST
// checkpoint renders NO budget ask (budgets settle at run start), and a
// checkpoint opened at exhaustion renders the extension offer — the labels
// the extend ride-along must equal exactly.
func TestRenderBudgetAsks(t *testing.T) {
	useStatusEnv(t)
	// Limits are run-start configuration since 0.6.1 — seed one directly.
	r, err := shared.StartRun("run-1", sess, "fix", shared.BudgetLimits{PhaseToolCalls: 1})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := r.OpenPhase("context"); err != nil {
		t.Fatal(err)
	}
	cp, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "context"})
	if err != nil {
		t.Fatal(err)
	}
	if cp.BudgetAsk != nil {
		t.Fatal("the first checkpoint must carry NO budget ask (0.6.1)")
	}
	out := renderStatus(r, "L1（未配置沙箱）")
	if strings.Contains(out, "budget ask (") || strings.Contains(out, "budget: 按已显示预算") {
		t.Fatalf("the retired budget ask must not render:\n%s", out)
	}

	// Decide (display-verified) and exhaust the next phase: its checkpoint
	// carries the extension offer.
	if err := r.MarkDisplayed(cp.ID, shared.CheckpointContentDigest(cp, r.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordDecision(cp.ID, shared.DecisionContinue, "tu-b"); err != nil {
		t.Fatal(err)
	}
	if err := r.OpenPhase("diagnosis"); err != nil {
		t.Fatal(err)
	}
	r.CountToolCall("")
	cp2, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "diagnosis"})
	if err != nil {
		t.Fatal(err)
	}
	if cp2.ExtendAsk == nil {
		t.Fatal("the exhaustion checkpoint must carry the extension offer")
	}
	out = renderStatus(r, "L1（未配置沙箱）")
	for _, want := range []string{
		"extend ask (budget exhausted): extend: 追加已显示额度 / extend: 不追加",
		"delta phase_tool_calls=1",
		"run-start 已生效",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("extend ask render missing %q:\n%s", want, out)
		}
	}
}

// TestRenderPostGamePending pins the closure-debt view: owed/answered markers,
// the fixed journal path, and the awaiting line.
func TestRenderPostGamePending(t *testing.T) {
	useStatusEnv(t)
	r := newRun(t)
	if err := r.OpenPhase("context"); err != nil {
		t.Fatal(err)
	}
	cp, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "context"})
	if err != nil {
		t.Fatal(err)
	}
	decide(t, r, cp, shared.DecisionDone)
	// M4 (ruling 72): DONE parks the review-phase workflow at the REVIEW_DEBT
	// checkpoint — discharge with the explicit UNREVIEWED finish to reach the
	// POST_GAME_PENDING this render test exists to view.
	if debt := r.CurrentCheckpoint(); debt != nil && debt.Kind == shared.CheckpointReviewDebt {
		if err := r.MarkDisplayed(debt.ID, shared.CheckpointContentDigest(*debt, r.RequirementsVersion)); err != nil {
			t.Fatal(err)
		}
		if err := r.RecordDecision(debt.ID, shared.DecisionSkip, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.StartPostGame("normal"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RecordPostGameAnswer("q1", "tu-pg-1"); err != nil {
		t.Fatal(err)
	}
	out := renderStatus(r, "L1（未配置沙箱）")
	for _, want := range []string{
		"awaiting: post-game questionnaire",
		"post-game (kind normal — CLOSED only after the questionnaire and journal persist):",
		"[pg:q1] q1: answered",
		"[pg:q2] q2: owed",
		"journal: ",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("post-game render missing %q:\n%s", want, out)
		}
	}
}

// TestRenderMenusByKind: menus mirror shared.legalDecisionForKind — phase takes
// the full set, change-confirm only {accept, stop}, recovery only
// {continue, recall, stop}, review-result the full default set.
func TestRenderMenusByKind(t *testing.T) {
	// menu options render as two-space-indented "  keyword: label" lines.
	// A menu keyword is offered when a label leads with it — on its own line
	// in the checkpoint message, or inside the detail's page projection.
	has := func(out string, kw string) bool {
		return strings.Contains(out, "  "+kw+":") || strings.Contains(out, "· "+kw+":")
	}

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
		// Final-fix C1 staging: REAL artifact bytes on disk — the mint pins
		// current bytes and RecordReviewResult re-verifies freshness.
		if err := r.OpenPhase("context"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(os.Getenv("CLAUDE_PROJECT_DIR"), "docs-diag.md"), []byte("diag\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		digest, err := shared.HashArtifactFile("docs-diag.md")
		if err != nil {
			t.Fatal(err)
		}
		cp, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{
			PhaseID:     "context",
			Summary:     "context done",
			CriteriaIDs: []string{"crit-diag"},
			Artifacts:   []shared.ArtifactRef{{Path: "docs-diag.md", Digest: digest, Kind: "doc"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		decide(t, r, cp, shared.DecisionContinue)
		if _, err := r.RequestReview(shared.Envelope{SchemaVersion: "2", RunID: "run-1", ReviewID: "rev-1"}, nil); err != nil {
			t.Fatal(err)
		}
		// The MINTED digest is the authority; cover every minted criterion.
		res := shared.ReviewResult{
			ReviewID:            "rev-1",
			RunStatus:           shared.ReviewRunCompleted,
			Verdict:             shared.VerdictPass,
			EvidenceToolCallIDs: []string{"toolu_1"},
			ArtifactDigest:      r.Reviews[0].ArtifactDigest,
		}
		for _, c := range r.Reviews[0].Envelope.ExitCriteria {
			res.CriterionVerdicts = append(res.CriterionVerdicts, shared.CriterionVerdict{
				CriterionID: c.ID,
				Status:      "pass",
				Evidence:    []shared.EvidenceAnchor{{ToolUseID: "toolu_1"}},
			})
		}
		if err := r.RecordReviewResult(res); err != nil {
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
		// Freeze something first — the mint derives every semantic field
		// from the pinned acceptance snapshot (identity-only transport).
		if err := r.OpenPhase("context"); err != nil {
			t.Fatal(err)
		}
		cp, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "context", Summary: "context done"})
		if err != nil {
			t.Fatal(err)
		}
		decide(t, r, cp, shared.DecisionContinue)
		if _, err := r.RequestReview(shared.Envelope{SchemaVersion: "2", RunID: "run-1", ReviewID: "rev-9"}, nil); err != nil {
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
	r, err := shared.StartRun("run-1", sess, "custom", shared.BudgetLimits{})
	if err != nil {
		t.Fatal(err)
	}
	out := renderStatus(r, "L2")
	if !strings.Contains(out, "route: (no framework sequence; visits: none yet)") {
		t.Fatalf("unknown-workflow route line:\n%s", out)
	}
}

// TestRenderFrameworkRouteLines: every framework-routed workflow renders its
// fixed sequence (T16 seven-verb routes).
func TestRenderFrameworkRouteLines(t *testing.T) {
	want := map[string]string{
		"review":  "route: context → review",
		"ship":    "route: prepare → review → merge → decide",
		"ops":     "route: context → operate",
		"release": "route: planning → changelog → publish",
	}
	for wf, line := range want {
		useStatusEnv(t)
		r, err := shared.StartRun("run-1", sess, wf, shared.BudgetLimits{})
		if err != nil {
			t.Fatal(err)
		}
		out := renderStatus(r, "L2")
		if !strings.Contains(out, line) {
			t.Errorf("%s render missing %q:\n%s", wf, line, out)
		}
	}
}

// TestRenderPagesNote (T16): while a checkpoint is pending, the render keeps
// the FULL menu visible and adds the paging note — the persisted pages, their
// labels, and the more:/back: navigation labels the decision question's
// offered set is pinned to. Single-page kinds carry no paging note.
func TestRenderPagesNote(t *testing.T) {
	useStatusEnv(t)
	r := newRun(t)
	if err := r.OpenPhase("diagnosis"); err != nil {
		t.Fatal(err)
	}
	cp, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "diagnosis", Summary: "done"})
	if err != nil {
		t.Fatal(err)
	}
	out := renderStatus(r, "L1（未配置沙箱）")

	// Full six-action menu stays visible: page 1 in the checkpoint message,
	// every page — labels and all — in the detail's page projection.
	for _, label := range shared.DecisionMenuLabels(cp.Kind) {
		if !strings.Contains(out, label) {
			t.Errorf("render missing full-menu label %q:\n%s", label, out)
		}
	}
	// Paging block: the detail prints the persisted pages — pages, their
	// labels, and navigation.
	if !strings.Contains(out, "menu pages (2):") || !strings.Contains(out, "page 1/2:") {
		t.Fatalf("render missing the menu paging block:\n%s", out)
	}
	for _, want := range []string{
		"page 1/2:", "page 2/2:",
		shared.MenuNavNext, shared.MenuNavPrev,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("paging note missing %q:\n%s", want, out)
		}
	}
	for _, l := range cp.MenuPages[0].Labels {
		if !strings.Contains(out, l) {
			t.Errorf("paging note missing page-1 label %q:\n%s", l, out)
		}
	}

	// Single-page kind: no paging note.
	useStatusEnv(t)
	r2 := newRun(t)
	r2.DevPosition = shared.DevPosition{CurrentTask: "t"}
	if err := r2.OpenRequirementChange("rename field"); err != nil {
		t.Fatal(err)
	}
	if _, err := r2.ApplyRequirementDelta(shared.RequirementDelta{Changed: []string{"crit-1"}}); err != nil {
		t.Fatal(err)
	}
	out2 := renderStatus(r2, "L2")
	if strings.Contains(out2, "paged ask") {
		t.Errorf("single-page kind must not carry a paging note:\n%s", out2)
	}
	if !strings.Contains(out2, "  accept:") || !strings.Contains(out2, "  stop:") {
		t.Errorf("change-confirm menu must offer accept+stop:\n%s", out2)
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

// TestBareInvocationIsEmptyPayload — the protocol's own render recipe is
// `summoner-status <<< '{}'`, but a bare invocation (no stdin at all) is the
// model's natural spelling (user report 2026-11-19). Empty stdin is the
// empty object: a useful no-run view, never an exit-2 error.
func TestBareInvocationIsEmptyPayload(t *testing.T) {
	useStatusEnv(t)
	useSandboxSettings(t, false)
	stubProbe(t, false)
	code, out, stderr := callStatus(t, "")
	if code != 0 {
		t.Fatalf("bare invocation: exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if !strings.Contains(out, "No active Summoner run") {
		t.Fatalf("bare invocation should render the no-run view, got: %q", out)
	}
	// Whitespace-only stdin is the same empty payload.
	code, _, stderr = callStatus(t, "  \n")
	if code != 0 {
		t.Fatalf("whitespace invocation: exit = %d, want 0 (stderr: %q)", code, stderr)
	}
}

// TestRenderAutoPassLine (0.7.0 D3): after an in-gate auto-pass the default
// view says so and names the phase to enter — the model's cue to go straight
// on — and the visit reads as accepted by the framework. The line is gone
// once a later step supersedes it.
func TestRenderAutoPassLine(t *testing.T) {
	useStatusEnv(t)
	r := newRun(t)
	if err := r.OpenPhase("context"); err != nil {
		t.Fatal(err)
	}
	cp, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{
		PhaseID: "context", Summary: "context established",
		Outcome:    shared.ConclusionConfirmed,
		Conclusion: &shared.Conclusion{Summary: "context conclusion", Evidence: []string{"evidence-1"}, Approach: "read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.AutoPassCheckpoint(cp.ID, false); err != nil {
		t.Fatalf("AutoPassCheckpoint: %v", err)
	}

	out := renderStatus(r, "L1（未配置沙箱）")
	for _, want := range []string{
		"⚡ SUMMONER STATUS — run run-1 · workflow fix · ACTIVE",
		"auto-passed: 阶段 context 已自动通过 → 立即进入 diagnosis",
		"- context: accepted (自动通过)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("auto-pass render missing %q:\n%s", want, out)
		}
	}
	// Nothing is pending: no checkpoint block, no "awaiting a decision".
	if strings.Contains(out, "SUMMONER CHECKPOINT") || strings.Contains(out, "awaiting: user decision") {
		t.Errorf("an auto-passed run has nothing pending:\n%s", out)
	}

	// A later step supersedes the line: once the next phase is submitted the
	// newest checkpoint is a human one.
	if err := r.OpenPhase("diagnosis"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "diagnosis", Summary: "diagnosis done"}); err != nil {
		t.Fatal(err)
	}
	out = renderStatus(r, "L1（未配置沙箱）")
	if strings.Contains(out, "auto-passed:") {
		t.Errorf("the auto-pass line belongs to the run's latest step only:\n%s", out)
	}
	if !strings.Contains(out, "║   context ✓ 自动通过: context established") {
		t.Errorf("the history block must still show the framework's acceptance:\n%s", out)
	}
}

// --- 0.7.0 D5.2: session resolution ---------------------------------------------

// twoSessions leaves two live runs in two sessions of the test project:
// run-a in sess-a, run-b in sess-b.
func twoSessions(t *testing.T) {
	t.Helper()
	for _, s := range []struct{ run, session string }{{"run-a", "sess-a"}, {"run-b", "sess-b"}} {
		if _, err := shared.StartRun(s.run, s.session, "fix", limits()); err != nil {
			t.Fatalf("StartRun(%s): %v", s.run, err)
		}
	}
}

// TestStatusResolvesTheSessionFromTheEnvironment — acceptance criterion 10:
// with no session_id in the payload the viewer scopes to the session
// SessionStart exported, and names the project's other live runs so a
// cross-session run is never mistaken for this one's.
func TestStatusResolvesTheSessionFromTheEnvironment(t *testing.T) {
	useStatusEnv(t)
	twoSessions(t)
	setenv16(t, "SUMMONER_SESSION_ID", "sess-a")

	code, out, stderr := callStatus(t, `{}`)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if !strings.Contains(out, "⚡ SUMMONER STATUS — run run-a · workflow fix · ACTIVE") {
		t.Fatalf("the viewer must scope to the exported session's run:\n%s", out)
	}
	if strings.Contains(out, "ambiguous") || strings.Contains(out, "naming them, rendering none") {
		t.Fatalf("with the exported session id there is nothing ambiguous:\n%s", out)
	}
	if !strings.Contains(out, "other live runs in this project: run-b (ACTIVE) — this view is scoped to session sess-a (SUMMONER_SESSION_ID); if one of them is yours, pass its session id") {
		t.Fatalf("the project's other live runs must be named, without claiming which session is yours:\n%s", out)
	}
}

// TestStatusFallsBackWhenTheEnvironmentIsSilent: without the exported
// variable the `{}` payload keeps its project-wide scan and its honest
// ambiguity listing.
func TestStatusFallsBackWhenTheEnvironmentIsSilent(t *testing.T) {
	useStatusEnv(t)
	twoSessions(t)

	code, out, stderr := callStatus(t, `{}`)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if !strings.Contains(out, "2 live Summoner runs — ambiguous") {
		t.Fatalf("without the exported session id the fallback must name the ambiguity:\n%s", out)
	}

	// A foreign value is no session id at all — same fallback.
	setenv16(t, "SUMMONER_SESSION_ID", "../sess-a")
	code, out, _ = callStatus(t, `{}`)
	if code != 0 || !strings.Contains(out, "2 live Summoner runs — ambiguous") {
		t.Fatalf("an unsafe exported value must be ignored:\n%s", out)
	}
}

// TestCheckpointViewIgnoresTheEnvironment: view:checkpoint is the canonical
// message and nothing else — the other-runs note and every status line stay
// out of it, whatever the environment says.
func TestCheckpointViewIgnoresTheEnvironment(t *testing.T) {
	useStatusEnv(t)
	twoSessions(t)
	setenv16(t, "SUMMONER_SESSION_ID", "sess-a")
	run, err := shared.LoadRun("run-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := run.OpenPhase("context"); err != nil {
		t.Fatal(err)
	}
	cp, err := run.SubmitPhaseUnstaged(shared.PhaseSubmission{PhaseID: "context", Summary: "context done"})
	if err != nil {
		t.Fatal(err)
	}
	run, _ = shared.LoadRun("run-a")

	code, out, stderr := callStatus(t, `{"view":"checkpoint"}`)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if out != shared.RenderCheckpointMessage(run, cp) {
		t.Fatalf("view:checkpoint must be exactly the canonical message, got:\n%s", out)
	}
}

// TestRenderAutoPassNotTakenLine (ship review round 1): a confirmed
// submission that did NOT auto-pass says why. The gate prints the reason on
// stderr with exit 0 — the model never sees it, so it could never learn
// what a passing submission needs.
func TestRenderAutoPassNotTakenLine(t *testing.T) {
	park := func(t *testing.T, phaseID string, outcome string, evidence []string) *shared.RunState {
		t.Helper()
		useStatusEnv(t) // a fresh project for every shape
		r := newRun(t)
		if err := r.OpenPhase(phaseID); err != nil {
			t.Fatal(err)
		}
		_, err := r.SubmitPhaseUnstaged(shared.PhaseSubmission{
			PhaseID: phaseID, Outcome: outcome,
			Conclusion: &shared.Conclusion{Summary: "a conclusion", Evidence: evidence, Approach: "how"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	out := renderStatus(park(t, "context", shared.ConclusionConfirmed, []string{" ", ""}), "L1（未配置沙箱）")
	if !strings.Contains(out, "auto-pass not taken: 阶段 context 留给你裁决 — a confirmed submission needs evidence") {
		t.Errorf("a confirmed submission without real evidence must say why it stayed:\n%s", out)
	}
	out = renderStatus(park(t, "context", shared.ConclusionUnclear, []string{"e"}), "L1（未配置沙箱）")
	if !strings.Contains(out, "auto-pass not taken: 阶段 context 留给你裁决 — the submission is not confirmed") {
		t.Errorf("an unclear submission must say why it stayed:\n%s", out)
	}
	// Once the user has seen the checkpoint, "why it stayed" is no longer
	// the question; and phases that never auto-pass carry no such line.
	r := park(t, "context", shared.ConclusionUnclear, []string{"e"})
	cur := r.CurrentCheckpoint()
	if err := r.MarkDisplayed(cur.ID, shared.CheckpointContentDigest(*cur, r.RequirementsVersion)); err != nil {
		t.Fatal(err)
	}
	if out = renderStatus(r, "L1（未配置沙箱）"); strings.Contains(out, "auto-pass not taken") {
		t.Errorf("a displayed checkpoint is simply owed:\n%s", out)
	}
	if out = renderStatus(park(t, "diagnosis", shared.ConclusionConfirmed, []string{"e"}), "L1（未配置沙箱）"); strings.Contains(out, "auto-pass not taken") {
		t.Errorf("diagnosis never auto-passes — nothing to explain:\n%s", out)
	}
}

// TestProjectScanSkipsACorruptNeighbour — one unreadable session state must
// not hide every other live run: the stale-id fallback rests on this scan
// (ship review round 3, compat F5).
func TestProjectScanSkipsACorruptNeighbour(t *testing.T) {
	useStatusEnv(t)
	setenv16(t, "SUMMONER_SESSION_ID", "sess-gone")
	r := newRun(t)
	bad := filepath.Join(os.Getenv("SUMMONER_STATE_DIR"), shared.ProjectKey(), "sess-bad")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "run-bad.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := callStatus(t, `{}`)
	if code != 0 || !strings.Contains(out, "⚡ SUMMONER STATUS — run "+r.RunID) {
		t.Fatalf("a corrupt neighbour must not defeat the project-wide view: (code %d, stderr %q)\n%s", code, stderr, out)
	}
	if !strings.Contains(stderr, "sess-bad unreadable, skipped") {
		t.Fatalf("the skipped session must be named on stderr, got %q", stderr)
	}
}

// TestStatusExportedSessionWithoutLiveRunFallsBack (ship review round 1):
// an exported session id can be STALE — left by an earlier session of this
// project. It must not yield a confident "No active run" (or another
// session's run) while live runs exist: with no run under the exported
// session the viewer falls back to the project-wide scan and its honest
// ambiguity listing.
func TestStatusExportedSessionWithoutLiveRunFallsBack(t *testing.T) {
	useStatusEnv(t)
	twoSessions(t)
	setenv16(t, "SUMMONER_SESSION_ID", "sess-gone") // stale: nothing live there

	code, out, stderr := callStatus(t, `{}`)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if strings.Contains(out, "No active Summoner run") || strings.Contains(out, "scoped to session") {
		t.Fatalf("a stale exported session id must not hide the project's live runs:\n%s", out)
	}
	if !strings.Contains(out, "2 live Summoner runs — ambiguous") {
		t.Fatalf("with nothing live under the exported session the project-wide fallback stands:\n%s", out)
	}

	// Exactly one other live run: the fallback renders it, as before 0.7.0.
	useStatusEnv(t)
	setenv16(t, "SUMMONER_SESSION_ID", "sess-gone")
	r := newRun(t)
	_ = r
	code, out, _ = callStatus(t, `{}`)
	if code != 0 || !strings.Contains(out, "⚡ SUMMONER STATUS — run run-1") {
		t.Fatalf("the fallback must render the project's single live run:\n%s", out)
	}
	if strings.Contains(out, "other live runs") || strings.Contains(out, "scoped to session") {
		t.Fatalf("the fallback is not session-scoped:\n%s", out)
	}
}

// TestProjectScanSurfacesOnlyLiveRuns — a CLOSED run in a neighbour session
// is not surfaced by the project-wide fallback (ship review round 4, N8).
func TestProjectScanSurfacesOnlyLiveRuns(t *testing.T) {
	useStatusEnv(t)
	setenv16(t, "SUMMONER_SESSION_ID", "sess-gone")
	r := newRun(t) // live, sess-1
	closed, err := shared.StartRun("run-closed", "sess-old", "fix", limits())
	if err != nil {
		t.Fatal(err)
	}
	closed.Status = shared.StatusClosed
	if err := closed.Save(); err != nil {
		t.Fatal(err)
	}
	code, out, _ := callStatus(t, `{}`)
	if code != 0 || !strings.Contains(out, "⚡ SUMMONER STATUS — run "+r.RunID) {
		t.Fatalf("the live run must render: (code %d)\n%s", code, out)
	}
	if strings.Contains(out, "run-closed") || strings.Contains(out, "ambiguous") {
		t.Fatalf("a CLOSED neighbour must not surface or make the view ambiguous:\n%s", out)
	}
}

// TestProjectScanNamesSkippedSessionsWhenNothingRenders — when the only state
// is unreadable the stdout answer must say so, not just stderr (round 4, N9).
func TestProjectScanNamesSkippedSessionsWhenNothingRenders(t *testing.T) {
	useStatusEnv(t)
	setenv16(t, "SUMMONER_SESSION_ID", "sess-gone")
	bad := filepath.Join(os.Getenv("SUMMONER_STATE_DIR"), shared.ProjectKey(), "sess-bad")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "run-bad.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := callStatus(t, `{}`)
	if code != 0 || !strings.Contains(out, "No active Summoner run") || !strings.Contains(out, "1 session(s) could not be read and were skipped") {
		t.Fatalf("the unreadable session must be named on stdout too: (code %d)\n%s", code, out)
	}
}
