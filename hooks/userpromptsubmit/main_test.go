package main

// main_test.go — Task 6 unit suite for the run-start recorder (addendum B).
// Fixtures run through the real reducer API; hermeticity comes from the env
// overrides the shared package honors (SUMMONER_STATE_DIR / SUMMONER_TRACE_DIR
// / CLAUDE_PROJECT_DIR) — the same pattern as the gate's and posttooluse's
// suites (go1.16: no t.Setenv).

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// --- hermetic environment -------------------------------------------------------

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

// unsetenv16 — same, for vars a test must guarantee are absent.
func unsetenv16(t *testing.T, key string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	os.Unsetenv(key)
	t.Cleanup(func() {
		if had {
			os.Setenv(key, old)
		}
	})
}

// useHookEnv redirects v2 state, traces and the project key into fresh temp
// dirs so no test ever touches real Summoner state.
func useHookEnv(t *testing.T) {
	t.Helper()
	setenv16(t, "SUMMONER_STATE_DIR", t.TempDir())
	setenv16(t, "SUMMONER_TRACE_DIR", t.TempDir())
	setenv16(t, "CLAUDE_PROJECT_DIR", t.TempDir())
	unsetenv16(t, "SUMMONER_HOOKS_DISABLE")
}

// callHook runs the whole hook on a payload; returns (exit code, stderr).
func callHook(t *testing.T, payload string) (int, string) {
	t.Helper()
	var stderr bytes.Buffer
	code := runHook(strings.NewReader(payload), &stderr)
	return code, stderr.String()
}

// --- fixtures -------------------------------------------------------------------

const hookSession = "sess-1"

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(b)
}

// upsPayload builds a UserPromptSubmit payload for hookSession.
func upsPayload(t *testing.T, prompt string) string {
	t.Helper()
	return mustJSON(t, map[string]interface{}{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      hookSession,
		"prompt":          prompt,
	})
}

// sessionRunFiles lists the session's run-state files.
func sessionRunFiles(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join(os.Getenv("SUMMONER_STATE_DIR"), shared.ProjectKey(), hookSession)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read session dir: %v", err)
	}
	var files []string
	for _, e := range entries {
		files = append(files, e.Name())
	}
	return files
}

// traceEvents returns the session's runs' trace events (nil when none); the
// run id comes from the single live run's state file.
func traceEvents(t *testing.T) []shared.TraceEvent {
	t.Helper()
	files := sessionRunFiles(t)
	if len(files) == 0 {
		return nil
	}
	runID := strings.TrimSuffix(files[0], ".json")
	p := filepath.Join(os.Getenv("SUMMONER_TRACE_DIR"), shared.ProjectKey(), runID+".jsonl")
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read trace: %v", err)
	}
	var evs []shared.TraceEvent
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var ev shared.TraceEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("unmarshal trace line %q: %v", line, err)
		}
		evs = append(evs, ev)
	}
	if err := shared.VerifyChain(runID); err != nil {
		t.Fatalf("VerifyChain(%s): %v", runID, err)
	}
	return evs
}

func eventsOfType(evs []shared.TraceEvent, typ string) []shared.TraceEvent {
	var out []shared.TraceEvent
	for _, ev := range evs {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

// --- run-start matching -----------------------------------------------------------

// TestMatchWorkflowTable pins the dual matching (typed form, expanded marker,
// negatives) at the pure-function level.
func TestMatchWorkflowTable(t *testing.T) {
	expanded := "<command-name>summoner:new</command-name>\n<command-message>summoner:new</command-message>\nargs"
	cases := []struct {
		prompt string
		want   string // "" = no match
	}{
		{"/summoner:fix broken login", "fix"},
		{"/summoner:debug why does it hang", "debug"},
		{"/summoner:release v2", "release"},
		{"/summoner:ops restart db", "ops"},
		{"/summoner:fix", "fix"},          // verb at end-of-string still bounds
		{"/summoner:fixes the bug", ""},   // \b: longer verb-name is not the verb
		{"/summoner:releasev2", ""},       // ditto
		{" /summoner:fix x", ""},          // typed form is anchored at column 0
		{"/summoner:unknown x", ""},       // outside the framework set
		{"please fix the login page", ""}, // plain word is not a verb
		{expanded, "new"},                 // expanded delivery form
		{"says <command-name>summoner:ship</command-name> inline", "ship"},
		{"<command-name>summoner:unknown</command-name>", ""}, // outside the set
		{"", ""}, // empty prompt
	}
	for _, c := range cases {
		got, ok := matchWorkflow(c.prompt)
		if c.want == "" {
			if ok {
				t.Errorf("matchWorkflow(%q) = %q, want no match", c.prompt, got)
			}
			continue
		}
		if !ok || got != c.want {
			t.Errorf("matchWorkflow(%q) = (%q, %v), want (%q, true)", c.prompt, got, ok, c.want)
		}
	}
}

// TestTypedFormStartsRun: a typed /summoner:fix prompt starts exactly one
// ACTIVE run with unlimited limits and one workflow.started event whose
// payload carries the workflow name.
func TestTypedFormStartsRun(t *testing.T) {
	useHookEnv(t)
	code, stderr := callHook(t, upsPayload(t, "/summoner:fix broken login"))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	files := sessionRunFiles(t)
	if len(files) != 1 {
		t.Fatalf("run state files = %d, want exactly 1", len(files))
	}
	runs, err := shared.SessionRuns(hookSession)
	if err != nil || len(runs) != 1 {
		t.Fatalf("SessionRuns = %d runs, err=%v, want 1", len(runs), err)
	}
	r := runs[0]
	if r.Workflow != "fix" || r.Status != shared.StatusActive {
		t.Fatalf("run = workflow %s / status %s, want fix / ACTIVE", r.Workflow, r.Status)
	}
	if r.Limits != (shared.BudgetLimits{}) {
		t.Errorf("limits = %+v, want all-zero (unlimited; budgets surface at the first checkpoint)", r.Limits)
	}
	evs := traceEvents(t)
	started := eventsOfType(evs, shared.EventWorkflowStarted)
	if len(started) != 1 {
		t.Fatalf("workflow.started events = %d, want exactly 1", len(started))
	}
	if started[0].Payload["workflow"] != "fix" {
		t.Errorf("workflow.started payload = %v, want workflow fix", started[0].Payload)
	}
	if started[0].Source != upsSource {
		t.Errorf("workflow.started source = %q, want %q", started[0].Source, upsSource)
	}
}

// TestExpandedFormStartsRun: the expanded delivery marker starts the run
// with the same semantics.
func TestExpandedFormStartsRun(t *testing.T) {
	useHookEnv(t)
	prompt := "<command-name>summoner:new</command-name>\n<command-message>summoner:new</command-message>\nbuild export"
	code, stderr := callHook(t, upsPayload(t, prompt))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	runs, err := shared.SessionRuns(hookSession)
	if err != nil || len(runs) != 1 {
		t.Fatalf("SessionRuns = %d runs, err=%v, want 1", len(runs), err)
	}
	if runs[0].Workflow != "new" {
		t.Fatalf("workflow = %s, want new", runs[0].Workflow)
	}
	started := eventsOfType(traceEvents(t), shared.EventWorkflowStarted)
	if len(started) != 1 || started[0].Payload["workflow"] != "new" {
		t.Fatalf("workflow.started events = %+v, want one carrying workflow new", started)
	}
}

// TestNonSummonerPromptNoOp: a plain prompt (even containing the word "fix")
// starts nothing and records nothing.
func TestNonSummonerPromptNoOp(t *testing.T) {
	useHookEnv(t)
	code, stderr := callHook(t, upsPayload(t, "please fix the login page yourself"))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	if files := sessionRunFiles(t); len(files) != 0 {
		t.Errorf("non-Summoner prompt must start no run, got %v", files)
	}
	if evs := traceEvents(t); len(evs) != 0 {
		t.Errorf("non-Summoner prompt must leave no events, got %d", len(evs))
	}
}

// TestSecondLiveRunRefused: a run-start prompt while a run is live exits 0
// with a one-line note and never creates the second run.
func TestSecondLiveRunRefused(t *testing.T) {
	useHookEnv(t)
	if code, stderr := callHook(t, upsPayload(t, "/summoner:fix broken login")); code != 0 {
		t.Fatalf("first start: exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	code, stderr := callHook(t, upsPayload(t, "/summoner:new another feature"))
	if code != 0 {
		t.Fatalf("second start: exit = %d, want 0 — refusing is not an error (stderr: %q)", code, stderr)
	}
	if !strings.Contains(stderr, "live") {
		t.Errorf("stderr %q must note the existing live run", stderr)
	}
	if files := sessionRunFiles(t); len(files) != 1 {
		t.Fatalf("run state files = %d, want still exactly 1", len(files))
	}
	if n := len(eventsOfType(traceEvents(t), shared.EventWorkflowStarted)); n != 1 {
		t.Errorf("workflow.started events = %d, want 1", n)
	}
}

// TestClosedRunDoesNotBlockNewRun: a CLOSED run is not live — a fresh verb
// starts a fresh run in the same session. (No reducer transition to CLOSED
// exists yet — post-game persistence is T8 — so the fixture is a CLOSED
// RunState persisted through the real Save path.)
func TestClosedRunDoesNotBlockNewRun(t *testing.T) {
	useHookEnv(t)
	closed := &shared.RunState{
		SchemaVersion:       shared.RunStateSchemaVersion,
		RunID:               "run-c-0",
		SessionID:           hookSession,
		Workflow:            "debug",
		Status:              shared.StatusClosed,
		RequirementsVersion: 1,
	}
	if err := closed.Save(); err != nil {
		t.Fatal(err)
	}
	code, stderr := callHook(t, upsPayload(t, "/summoner:fix broken login"))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	if files := sessionRunFiles(t); len(files) != 2 {
		t.Fatalf("run state files = %d, want 2 (closed + new)", len(files))
	}
}

// TestEscapeHatch: SUMMONER_HOOKS_DISABLE=1 starts nothing — on one live run
// it records the violation; with no live run it stays silent.
func TestEscapeHatch(t *testing.T) {
	t.Run("live run traces the violation, nothing starts", func(t *testing.T) {
		useHookEnv(t)
		if _, err := shared.StartRun("run-h-1", hookSession, "debug", shared.BudgetLimits{}); err != nil {
			t.Fatal(err)
		}
		setenv16(t, "SUMMONER_HOOKS_DISABLE", "1")
		code, stderr := callHook(t, upsPayload(t, "/summoner:fix broken login"))
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 — the hatch enforces nothing (stderr: %q)", code, stderr)
		}
		if files := sessionRunFiles(t); len(files) != 1 {
			t.Fatalf("the hatch must start nothing, got %v", files)
		}
		evs := traceEvents(t)
		if n := len(eventsOfType(evs, shared.EventHooksDisabled)); n != 1 {
			t.Fatalf("hooks.disabled events = %d, want exactly 1", n)
		}
		if n := len(eventsOfType(evs, shared.EventWorkflowStarted)); n != 0 {
			t.Fatalf("workflow.started under the hatch = %d, want 0", n)
		}
	})
	t.Run("no live run stays silent", func(t *testing.T) {
		useHookEnv(t)
		setenv16(t, "SUMMONER_HOOKS_DISABLE", "1")
		code, stderr := callHook(t, upsPayload(t, "/summoner:fix broken login"))
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
		}
		if files := sessionRunFiles(t); len(files) != 0 {
			t.Fatalf("the hatch must start nothing, got %v", files)
		}
	})
}

// TestParseFailureFailsClosed: unparseable input, trailing garbage, a
// foreign event name and an empty one are all surfaced as exit 2 (this hook
// can never block prompts, so fail-closed means surfaced, not blocking).
func TestParseFailureFailsClosed(t *testing.T) {
	useHookEnv(t)
	for name, payload := range map[string]string{
		"not json":          "this is not json",
		"trailing data":     `{"hook_event_name":"UserPromptSubmit"} trailing`,
		"no event name":     mustJSON(t, map[string]interface{}{"session_id": hookSession, "prompt": "/summoner:fix x"}),
		"foreign event":     mustJSON(t, map[string]interface{}{"hook_event_name": "Stop", "session_id": hookSession, "prompt": "/summoner:fix x"}),
		"prompt wrong type": mustJSON(t, map[string]interface{}{"hook_event_name": "UserPromptSubmit", "session_id": hookSession, "prompt": 5}),
	} {
		code, stderr := callHook(t, payload)
		if code != 2 {
			t.Errorf("%s: exit code = %d, want 2 (stderr: %q)", name, code, stderr)
		}
	}
}

// TestUnreadableStateFailsClosed: a corrupt state file under a matched verb
// surfaces exit 2 instead of silently skipping the run start.
func TestUnreadableStateFailsClosed(t *testing.T) {
	useHookEnv(t)
	dir := filepath.Join(os.Getenv("SUMMONER_STATE_DIR"), shared.ProjectKey(), hookSession)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "corrupt.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stderr := callHook(t, upsPayload(t, "/summoner:fix broken login"))
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %q)", code, stderr)
	}
	if !strings.Contains(stderr, "unreadable") {
		t.Errorf("stderr %q must name the unreadable state", stderr)
	}
}
