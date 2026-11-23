package contract

// contract_test.go — Task 6 install-contract suite (plan Step 1 + controller
// addendum F): the REAL hook binaries driven as subprocesses (stdin = hook
// JSON payload), hermetic through the env overrides the shared package
// honors — every subprocess gets SUMMONER_STATE_DIR + SUMMONER_TRACE_DIR +
// CLAUDE_PROJECT_DIR pointing at t.TempDir(), never the real ~/.summoner.
//
// The suite pins the platform wiring a Go test cannot invoke the platform to
// prove: the PreToolUse gate entry carries onFailure:"block" + timeout, so a
// missing or crashed gate binary BLOCKS the tool call platform-side; the
// UserPromptSubmit entry deliberately carries NO onFailure (settled ruling:
// a stuck run-start hook must never block prompts — its output is discarded
// on timeout, it only records) and no matcher (the event supports none);
// PostToolUseFailure is a separate top-level event key pointing at the same
// posttooluse binary; validate-manifest stays unwired (CI CLI by design).
//
// settings-recommended.json — verified platform facts honored here (docs
// verified live 2026-11-10; user-facing docs land in T8):
//   - askUserQuestionTimeout is valid in User/managed scope ONLY: the file
//     is a recommended FRAGMENT for ~/.claude/settings.json, never project
//     settings;
//   - permission rules: "~/..." home-relative paths with "**" globs are
//     valid, deny rules match the requested OR symlink-resolved path, and
//     Edit(...) rules cover file-modification tools;
//   - sandbox filesystem paths use home-relative DIRECTORY form WITHOUT
//     globs (sandbox path style differs from permission-rule style).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// --- hermetic environment -------------------------------------------------------

// setenv16 — t.Setenv is go1.17+; this module pins go1.16.
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

// useContractEnv redirects v2 state, traces and the project key into fresh
// temp dirs so no subprocess or fixture ever touches real Summoner state.
func useContractEnv(t *testing.T) {
	t.Helper()
	setenv16(t, "SUMMONER_STATE_DIR", t.TempDir())
	setenv16(t, "SUMMONER_TRACE_DIR", t.TempDir())
	setenv16(t, "CLAUDE_PROJECT_DIR", t.TempDir())
	unsetenv16(t, "SUMMONER_HOOKS_DISABLE")
}

// --- binary resolution + subprocess driving -------------------------------------

// binDir is where subprocess tests resolve hook binaries. Default: the
// in-tree ../bin (the shipped artifacts — the point of the contract). When
// those cannot run on this platform (CI runs this suite on linux BEFORE
// `make build` refreshes the unsuffixed binaries, and the committed ones are
// darwin-built), TestMain swaps in a freshly built copy so the subprocess
// contract stays real on every platform — TestMissingBinaryBlocks still pins
// the in-tree artifacts.
var binDir = filepath.Join("..", "bin")

// TestMain probes the in-tree binaries once and falls back to a local build
// if they cannot exec here (see binDir).
func TestMain(m *testing.M) {
	if !binRunsOnThisPlatform() {
		dir, err := rebuildBinaries()
		if err != nil {
			fmt.Fprintf(os.Stderr, "contract: in-tree binaries not runnable here and rebuild failed: %v\n", err)
			os.Exit(1)
		}
		binDir = dir
	}
	os.Exit(m.Run())
}

// binRunsOnThisPlatform execs the gate binary with an empty-identity payload:
// ANY exit code proves the binary runs here (it exits 2 — the empty session
// id is rejected before any filesystem access); an exec error means the
// binary is missing or built for another platform.
func binRunsOnThisPlatform() bool {
	cmd := exec.Command(filepath.Join("..", "bin", "pretooluse-gate"))
	cmd.Stdin = strings.NewReader("{}")
	err := cmd.Run()
	var ee *exec.ExitError
	return err == nil || errors.As(err, &ee)
}

// rebuildBinaries builds every subprocess-driven hook for the CURRENT
// platform into a temp dir (validate-manifest excluded: CI CLI, unwired).
func rebuildBinaries() (string, error) {
	dir, err := os.MkdirTemp("", "summoner-contract-bin-")
	if err != nil {
		return "", err
	}
	for _, name := range []string{"session-start", "pretooluse-gate", "posttooluse", "subagentstop", "stop", "userpromptsubmit", "summoner-sandbox-probe", "summoner-status"} {
		out := filepath.Join(dir, name)
		if b, err := exec.Command("go", "build", "-o", out, "../"+name).CombinedOutput(); err != nil {
			os.RemoveAll(dir)
			return "", fmt.Errorf("go build ../%s: %v\n%s", name, err, b)
		}
	}
	return dir, nil
}

func binPath(name string) string { return filepath.Join(binDir, name) }

// hookSubprocessEnv builds the subprocess environment: the parent's env
// minus every Summoner/project override (hermetic regardless of the outer
// shell), plus this test's temp dirs, plus any extra (e.g. the hatch).
func hookSubprocessEnv(t *testing.T, extra map[string]string) []string {
	t.Helper()
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "SUMMONER_") || strings.HasPrefix(kv, "CLAUDE_PROJECT_DIR=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"SUMMONER_STATE_DIR="+os.Getenv("SUMMONER_STATE_DIR"),
		"SUMMONER_TRACE_DIR="+os.Getenv("SUMMONER_TRACE_DIR"),
		"CLAUDE_PROJECT_DIR="+os.Getenv("CLAUDE_PROJECT_DIR"),
	)
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

// runBinary executes one hook binary with payload on stdin; returns
// (exit code, stdout, stderr). A failure to exec at all is fatal to the test.
func runBinary(t *testing.T, name, payload string, extraEnv map[string]string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(binPath(name))
	cmd.Stdin = strings.NewReader(payload)
	cmd.Env = hookSubprocessEnv(t, extraEnv)
	var out, errB bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errB
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run %s (%s): %v", name, binPath(name), err)
	}
	return code, out.String(), errB.String()
}

// --- fixtures -------------------------------------------------------------------

// contractSession is the session id every payload and seeded run uses.
const contractSession = "sess-contract"

// seedLiveRun starts one live run in the temp state dir (test process, same
// env overrides the subprocesses get). Mirrors the userpromptsubmit hook:
// budgets are manifest-authored — suggested_budgets applies at run start,
// else unlimited (0.6.1, no run-start ask).
func seedLiveRun(t *testing.T, runID, workflow string) *shared.RunState {
	t.Helper()
	limits, err := shared.ResolveStartLimits()
	if err != nil {
		t.Fatalf("seed ResolveStartLimits(%s): %v", runID, err)
	}
	r, err := shared.StartRun(runID, contractSession, workflow, limits)
	if err != nil {
		t.Fatalf("seed StartRun(%s, %s): %v", runID, workflow, err)
	}
	return r
}

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(b)
}

func gatePayload(t *testing.T, tool, toolUseID string, toolInput map[string]interface{}) string {
	t.Helper()
	if toolInput == nil {
		toolInput = map[string]interface{}{}
	}
	return mustJSON(t, map[string]interface{}{
		"session_id":  contractSession,
		"tool_name":   tool,
		"tool_use_id": toolUseID,
		"tool_input":  toolInput,
	})
}

func upsPayload(t *testing.T, prompt string) string {
	t.Helper()
	return mustJSON(t, map[string]interface{}{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      contractSession,
		"prompt":          prompt,
	})
}

func ptuPayload(t *testing.T, tool, toolUseID string) string {
	t.Helper()
	return mustJSON(t, map[string]interface{}{
		"hook_event_name": "PostToolUse",
		"session_id":      contractSession,
		"tool_name":       tool,
		"tool_use_id":     toolUseID,
		"tool_input":      map[string]interface{}{"file_path": "/tmp/x"},
		"tool_response":   map[string]interface{}{"content": "x"},
	})
}

func ptufPayload(t *testing.T, tool, toolUseID, errMsg string) string {
	t.Helper()
	return mustJSON(t, map[string]interface{}{
		"hook_event_name": "PostToolUseFailure",
		"session_id":      contractSession,
		"tool_name":       tool,
		"tool_use_id":     toolUseID,
		"tool_input":      map[string]interface{}{"command": "make"},
		"error":           errMsg,
	})
}

func subagentStopPayload(t *testing.T, agentType string) string {
	t.Helper()
	return mustJSON(t, map[string]interface{}{
		"hook_event_name":       "SubagentStop",
		"session_id":            contractSession,
		"agent_id":              "ag-1",
		"agent_type":            agentType,
		"agent_transcript_path": filepath.Join(t.TempDir(), "absent-transcript.jsonl"),
	})
}

func stopPayload(t *testing.T) string {
	t.Helper()
	return mustJSON(t, map[string]interface{}{
		"hook_event_name":        "Stop",
		"session_id":             contractSession,
		"stop_hook_active":       false,
		"last_assistant_message": "all done",
	})
}

func sessionStartPayload(t *testing.T) string {
	t.Helper()
	return mustJSON(t, map[string]interface{}{
		"hook_event_name": "SessionStart",
		"session_id":      contractSession,
		"source":          "startup",
	})
}

// --- state / trace observation --------------------------------------------------

// runStateFiles lists every run-state file under the temp state dir.
func runStateFiles(t *testing.T) []string {
	t.Helper()
	root := os.Getenv("SUMMONER_STATE_DIR")
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil
	}
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".json") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk state dir: %v", err)
	}
	return files
}

// allTraceEvents parses every trace line under the temp trace dir (nil when
// nothing was ever written).
func allTraceEvents(t *testing.T) []shared.TraceEvent {
	t.Helper()
	root := os.Getenv("SUMMONER_TRACE_DIR")
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil
	}
	var evs []shared.TraceEvent
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if line == "" {
				continue
			}
			var ev shared.TraceEvent
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				return fmt.Errorf("parse %s: %v", path, err)
			}
			evs = append(evs, ev)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk trace dir: %v", err)
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

// --- hooks.json parsing ---------------------------------------------------------

type hookCommand struct {
	Type      string       `json:"type"`
	Command   string       `json:"command"`
	Timeout   *json.Number `json:"timeout"`
	OnFailure string       `json:"onFailure"`
	Async     bool         `json:"async"`
}

type hookEntry struct {
	Matcher *string       `json:"matcher"` // nil = field omitted
	Hooks   []hookCommand `json:"hooks"`
}

type hooksSpec struct {
	Description string                 `json:"description"`
	Hooks       map[string][]hookEntry `json:"hooks"`
}

func loadHooksSpec(t *testing.T) *hooksSpec {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	var spec hooksSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatalf("parse hooks.json: %v", err)
	}
	return &spec
}

// singleEntry returns the one entry wired for an event (its matcher plus its
// single hook command).
func singleEntry(t *testing.T, spec *hooksSpec, event string) hookEntry {
	t.Helper()
	entries, ok := spec.Hooks[event]
	if !ok || len(entries) == 0 {
		t.Fatalf("hooks.json: event %s is not wired", event)
	}
	if len(entries) != 1 || len(entries[0].Hooks) != 1 {
		t.Fatalf("hooks.json: event %s must wire exactly one hook command, got %d entries", event, len(entries))
	}
	return entries[0]
}

// resolveBinary maps one hooks.json command to its in-tree path (relative to
// this package): "${CLAUDE_PLUGIN_ROOT}/hooks/bin/<name>" → ../bin/<name>.
func resolveBinary(t *testing.T, command string) string {
	t.Helper()
	s := strings.Trim(strings.TrimSpace(command), `"`)
	const prefix = "${CLAUDE_PLUGIN_ROOT}/hooks/bin/"
	if !strings.HasPrefix(s, prefix) {
		t.Fatalf("command %q is not anchored at %q", command, prefix)
	}
	return filepath.Join("..", "bin", strings.TrimPrefix(s, prefix))
}

// wiredEvents is the complete v2 event → binary map (addendum A).
var wiredEvents = []string{
	"SessionStart", "UserPromptSubmit", "PreToolUse",
	"PostToolUse", "PostToolUseFailure", "SubagentStop", "Stop",
}

// --- Step 1 tests -----------------------------------------------------------------

// TestMissingBinaryBlocks: every wired command's binary exists in-tree and is
// executable, and the PreToolUse gate entry carries onFailure:"block" + a
// numeric timeout — THAT wiring is what makes a missing or crashed gate block
// the tool call platform-side (the platform itself cannot be invoked from a
// test, so the contract pins the wiring that produces the blocking).
func TestMissingBinaryBlocks(t *testing.T) {
	spec := loadHooksSpec(t)
	if spec.Description == "" {
		t.Error("hooks.json: top-level description is missing")
	}
	for _, ev := range wiredEvents {
		if _, ok := spec.Hooks[ev]; !ok {
			t.Errorf("hooks.json: event %s is not wired", ev)
		}
	}
	wired := 0
	for event, entries := range spec.Hooks {
		for _, e := range entries {
			for _, h := range e.Hooks {
				wired++
				if h.Type != "command" {
					t.Errorf("%s: hook type = %q, want \"command\"", event, h.Type)
				}
				if h.Async {
					t.Errorf("%s: entry must stay synchronous (\"async\": false)", event)
				}
				if h.OnFailure == "block" && event != "PreToolUse" {
					t.Errorf("%s: carries onFailure:block — verified to have NO effect outside PreToolUse; only the gate entry may carry it", event)
				}
				p := resolveBinary(t, h.Command)
				fi, err := os.Stat(p)
				if err != nil {
					t.Errorf("%s: wired binary missing in-tree: %s (%v)", event, p, err)
					continue
				}
				if fi.IsDir() {
					t.Errorf("%s: wired binary path is a directory: %s", event, p)
					continue
				}
				if fi.Mode().Perm()&0o111 == 0 {
					t.Errorf("%s: wired binary is not executable: %s", event, p)
				}
			}
		}
	}
	if wired < len(wiredEvents) {
		t.Errorf("wired commands = %d, want >= %d (one per event)", wired, len(wiredEvents))
	}

	gate := singleEntry(t, spec, "PreToolUse").Hooks[0]
	if gate.OnFailure != "block" {
		t.Errorf("PreToolUse onFailure = %q, want \"block\" (the fail-closed seam)", gate.OnFailure)
	}
	if m := singleEntry(t, spec, "PreToolUse").Matcher; m == nil || (*m != "" && *m != "*") {
		t.Errorf("PreToolUse matcher = %v, want match-all (\"\" — a narrow matcher silently unwires the all-tool gate)", m)
	}
	if gate.Timeout == nil {
		t.Error("PreToolUse carries no numeric timeout")
	} else if sec, err := gate.Timeout.Float64(); err != nil || sec <= 0 {
		t.Errorf("PreToolUse timeout = %v, want a positive number of seconds", gate.Timeout)
	}

	// UserPromptSubmit: no matcher (the event supports none — verified), a
	// timeout, and NO onFailure — a stuck run-start hook must never block
	// prompts; its output being discarded on timeout is the settled ruling.
	upsEntry := singleEntry(t, spec, "UserPromptSubmit")
	ups := upsEntry.Hooks[0]
	if upsEntry.Matcher != nil {
		t.Errorf("UserPromptSubmit must OMIT the matcher field (the event supports no matcher), got %q", *upsEntry.Matcher)
	}
	if ups.Timeout == nil {
		t.Error("UserPromptSubmit carries no numeric timeout")
	} else if sec, err := ups.Timeout.Float64(); err != nil || sec <= 0 {
		t.Errorf("UserPromptSubmit timeout = %v, want a positive number of seconds", ups.Timeout)
	}
	if ups.OnFailure != "" {
		t.Errorf("UserPromptSubmit onFailure = %q, want none (prompts are never blocked by run-start recording)", ups.OnFailure)
	}

	// PostToolUseFailure is a SEPARATE top-level event key pointing at the
	// same binary — one binary branches on hook_event_name.
	ptu, ptuf := singleEntry(t, spec, "PostToolUse").Hooks[0], singleEntry(t, spec, "PostToolUseFailure").Hooks[0]
	if ptuf.Command != ptu.Command {
		t.Errorf("PostToolUseFailure (%q) must point at the same binary as PostToolUse (%q)", ptuf.Command, ptu.Command)
	}
}

// TestTimeoutBlocks: the gate entry's timeout is present, numeric and ≤ 60
// (the platform ceiling); behaviorally, a normal allow-path invocation
// (ACTIVE run, benign tool) completes well under it.
func TestTimeoutBlocks(t *testing.T) {
	spec := loadHooksSpec(t)
	gate := singleEntry(t, spec, "PreToolUse").Hooks[0]
	if gate.Timeout == nil {
		t.Fatal("PreToolUse carries no numeric timeout")
	}
	sec, err := gate.Timeout.Float64()
	if err != nil || sec <= 0 {
		t.Fatalf("PreToolUse timeout = %v, want a positive number of seconds: %v", gate.Timeout, err)
	}
	if sec > 60 {
		t.Errorf("PreToolUse timeout = %v s, want <= 60 (a hung gate must not freeze the session for minutes)", sec)
	}

	useContractEnv(t)
	seedLiveRun(t, "run-tb-1", "debug")
	start := time.Now()
	code, _, stderr := runBinary(t, "pretooluse-gate",
		gatePayload(t, "Read", "tu-tb-1", map[string]interface{}{"file_path": "/tmp/x"}), nil)
	elapsed := time.Since(start)
	if code != 0 {
		t.Fatalf("allow-path gate invocation exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if bound := time.Duration(sec * float64(time.Second)); elapsed >= bound {
		t.Fatalf("allow-path gate invocation took %v, want < the wired %v s timeout", elapsed, sec)
	}
	if elapsed >= 5*time.Second {
		t.Fatalf("allow-path gate invocation took %v — far beyond a healthy subprocess run", elapsed)
	}
}

// TestInvalidJSONBlocks: garbage stdin into every stdin-reading binary fails
// closed (gate exit 2 = the tool call is blocked; recorders exit 2 =
// surfaced); session-start never reads stdin. The sweep then asserts the
// binaries' whole exit domain is {0, 2} — our contract never emits other
// codes (plan: 非 0/2 退出).
func TestInvalidJSONBlocks(t *testing.T) {
	t.Run("garbage stdin fails closed where input is read", func(t *testing.T) {
		useContractEnv(t)
		seedLiveRun(t, "run-ij-1", "debug")
		for _, name := range []string{"pretooluse-gate", "posttooluse", "subagentstop", "stop", "userpromptsubmit"} {
			code, _, stderr := runBinary(t, name, "this is not json {{{", nil)
			if code != 2 {
				t.Errorf("%s: garbage stdin exit = %d, want 2 (stderr: %q)", name, code, stderr)
			}
		}
		// session-start injects context only and never reads stdin: garbage
		// is ignored, not an error.
		code, _, _ := runBinary(t, "session-start", "this is not json {{{", nil)
		if code != 0 {
			t.Error("session-start: garbage stdin exit != 0 — the binary never reads stdin")
		}
	})

	t.Run("every exercised path exits 0 or 2 only", func(t *testing.T) {
		useContractEnv(t)
		seedLiveRun(t, "run-sw-1", "debug")
		calls := []struct{ name, label, payload string }{
			{"session-start", "context injection", sessionStartPayload(t)},
			{"userpromptsubmit", "run start", upsPayload(t, "/summoner:fix broken login")},
			{"userpromptsubmit", "second run refused", upsPayload(t, "/summoner:new another one")},
			{"userpromptsubmit", "non-summoner prompt", upsPayload(t, "please just fix it yourself")},
			{"userpromptsubmit", "garbage stdin", "not json"},
			{"userpromptsubmit", "foreign event", `{"hook_event_name":"Stop","session_id":"` + contractSession + `","prompt":"/summoner:fix x"}`},
			{"pretooluse-gate", "benign allow", gatePayload(t, "Read", "tu-sw-1", map[string]interface{}{"file_path": "/tmp/x"})},
			{"pretooluse-gate", "no-run session pass-through", mustJSON(t, map[string]interface{}{"session_id": "sess-other", "tool_name": "Read", "tool_use_id": "tu-sw-2", "tool_input": map[string]interface{}{}})},
			{"pretooluse-gate", "garbage stdin", "not json"},
			{"posttooluse", "result recording", ptuPayload(t, "Read", "tu-sw-3")},
			{"posttooluse", "failure recording", ptufPayload(t, "Bash", "tu-sw-4", "exit status 1")},
			{"posttooluse", "garbage stdin", "not json"},
			{"subagentstop", "delegated worker stop", subagentStopPayload(t, "worker")},
			{"subagentstop", "non-delegated internal agent", subagentStopPayload(t, "prompt-suggestions")},
			{"stop", "no display debt", stopPayload(t)},
			{"stop", "garbage stdin", "not json"},
		}
		for _, c := range calls {
			code, _, _ := runBinary(t, c.name, c.payload, nil)
			if code != 0 && code != 2 {
				t.Errorf("%s (%s): exit = %d — the binaries' whole exit domain is {0, 2}", c.name, c.label, code)
			}
		}
	})
}

// TestDisableEscapeHatchLogged: SUMMONER_HOOKS_DISABLE=1 exits 0 everywhere,
// and with exactly one live run the violation is RECORDED (one hooks.disabled
// event); with zero live runs there is nothing unambiguously live to violate
// — exit 0, no events.
func TestDisableEscapeHatchLogged(t *testing.T) {
	t.Run("live run records the violation", func(t *testing.T) {
		useContractEnv(t)
		seedLiveRun(t, "run-ha-1", "debug")
		code, _, stderr := runBinary(t, "pretooluse-gate",
			gatePayload(t, "Read", "tu-ha-1", map[string]interface{}{"file_path": "/tmp/x"}),
			map[string]string{"SUMMONER_HOOKS_DISABLE": "1"})
		if code != 0 {
			t.Fatalf("exit = %d, want 0 — the hatch enforces nothing (stderr: %q)", code, stderr)
		}
		evs := allTraceEvents(t)
		disabled := eventsOfType(evs, shared.EventHooksDisabled)
		if len(disabled) != 1 {
			t.Fatalf("hooks.disabled events = %d, want exactly 1 (all events: %v)", len(disabled), eventTypes(evs))
		}
		if disabled[0].RunID != "run-ha-1" {
			t.Errorf("hooks.disabled run_id = %q, want run-ha-1", disabled[0].RunID)
		}
	})
	t.Run("no live run stays silent", func(t *testing.T) {
		useContractEnv(t)
		code, _, stderr := runBinary(t, "pretooluse-gate",
			gatePayload(t, "Read", "tu-ha-2", map[string]interface{}{"file_path": "/tmp/x"}),
			map[string]string{"SUMMONER_HOOKS_DISABLE": "1"})
		if code != 0 {
			t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
		}
		if evs := allTraceEvents(t); len(evs) != 0 {
			t.Fatalf("no live run under the hatch must leave no events, got %v", eventTypes(evs))
		}
	})
}

func eventTypes(evs []shared.TraceEvent) []string {
	types := make([]string, 0, len(evs))
	for _, ev := range evs {
		types = append(types, ev.Type)
	}
	return types
}

// TestRecommendedSettingsAssertions: settings-recommended.json parses and
// carries the settled install assertions (see the file-header facts).
func TestRecommendedSettingsAssertions(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "settings-recommended.json"))
	if err != nil {
		t.Fatalf("read settings-recommended.json (it ships at the plugin root, next to plugin.json): %v", err)
	}
	var doc struct {
		AskUserQuestionTimeout string `json:"askUserQuestionTimeout"`
		Sandbox                struct {
			Filesystem struct {
				DenyRead []string `json:"denyRead"`
			} `json:"filesystem"`
		} `json:"sandbox"`
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse settings-recommended.json: %v", err)
	}
	if doc.AskUserQuestionTimeout != "never" {
		t.Errorf("askUserQuestionTimeout = %q, want \"never\" (checkpoint decisions must never be auto-resolved by timeout)", doc.AskUserQuestionTimeout)
	}
	for _, dir := range []string{"~/.summoner/state-v2", "~/.summoner/traces-v2"} {
		if !containsString(doc.Sandbox.Filesystem.DenyRead, dir) {
			t.Errorf("sandbox.filesystem.denyRead must contain %q (home-relative directory form, no globs), got %v", dir, doc.Sandbox.Filesystem.DenyRead)
		}
	}
	wantDeny := []string{
		"Read(~/.summoner/state-v2/**)",
		"Read(~/.summoner/traces-v2/**)",
		"Edit(~/.summoner/**)",
	}
	if len(doc.Permissions.Deny) != len(wantDeny) {
		t.Errorf("permissions.deny = %v, want exactly %v", doc.Permissions.Deny, wantDeny)
	} else {
		for i, want := range wantDeny {
			if doc.Permissions.Deny[i] != want {
				t.Errorf("permissions.deny[%d] = %q, want %q", i, doc.Permissions.Deny[i], want)
			}
		}
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestSandboxTierDetection: the pure classifier matrix — probe active → L2;
// probe inactive + configured → BLOCKED (never a silent degrade to L1);
// probe inactive + unconfigured → L1 (honestly unsandboxed).
func TestSandboxTierDetection(t *testing.T) {
	cases := []struct {
		name       string
		configured bool
		probe      bool
		want       string
	}{
		{"sandbox configured and effective", true, true, TierL2},
		{"sandbox effective though unconfigured", false, true, TierL2},
		{"configured but probe says inactive", true, false, TierBlocked},
		{"honestly unsandboxed", false, false, TierL1},
	}
	for _, c := range cases {
		if got := DetectSandboxTier(c.configured, c.probe); got != c.want {
			t.Errorf("%s: DetectSandboxTier(%v, %v) = %q, want %q", c.name, c.configured, c.probe, got, c.want)
		}
	}
	// The pin the plan demands on its own line: a configured sandbox whose
	// probe failed must be BLOCKED, never silently treated as L1.
	if got := DetectSandboxTier(true, false); got != TierBlocked {
		t.Errorf("configured-but-not-effective must classify BLOCKED, got %q", got)
	}
}

// --- end-to-end additions (addendum F) ---------------------------------------------

// TestEndToEndGateAllowsBenignTool: gate + ACTIVE run + benign tool → exit 0
// and a tool.started event in the temp trace.
func TestEndToEndGateAllowsBenignTool(t *testing.T) {
	useContractEnv(t)
	seedLiveRun(t, "run-ee-1", "debug")
	code, _, stderr := runBinary(t, "pretooluse-gate",
		gatePayload(t, "Read", "tu-ee-1", map[string]interface{}{"file_path": "/tmp/x"}), nil)
	if code != 0 {
		t.Fatalf("gate exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	evs := allTraceEvents(t)
	started := eventsOfType(evs, shared.EventToolStarted)
	if len(started) != 1 {
		t.Fatalf("tool.started events = %d, want 1 (all events: %v)", len(started), eventTypes(evs))
	}
	if started[0].ToolUseID != "tu-ee-1" || started[0].Source != "pretooluse-gate" {
		t.Errorf("tool.started = {tool_use_id: %q, source: %q}, want tu-ee-1 / pretooluse-gate", started[0].ToolUseID, started[0].Source)
	}
	if started[0].Payload["tool_name"] != "Read" {
		t.Errorf("tool.started tool_name = %v, want Read", started[0].Payload["tool_name"])
	}
}

// TestEndToEndRunStartTypedPrompt: "/summoner:fix …" starts the run (state
// file + workflow.started with payload workflow=fix), and a second run-start
// prompt while the first run is live starts NOTHING — exactly one run file
// remains (a second live run would make every other hook fail closed on
// ambiguity).
func TestEndToEndRunStartTypedPrompt(t *testing.T) {
	useContractEnv(t)
	code, _, stderr := runBinary(t, "userpromptsubmit", upsPayload(t, "/summoner:fix broken login"), nil)
	if code != 0 {
		t.Fatalf("userpromptsubmit exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	files := runStateFiles(t)
	if len(files) != 1 {
		t.Fatalf("run state files = %d, want exactly 1 (got %v)", len(files), files)
	}
	var doc struct {
		RunID     string `json:"run_id"`
		SessionID string `json:"session_id"`
		Workflow  string `json:"workflow"`
		Status    string `json:"status"`
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read run state: %v", err)
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse run state: %v", err)
	}
	if doc.Workflow != "fix" || doc.SessionID != contractSession || doc.Status != "ACTIVE" {
		t.Fatalf("run state = %+v, want workflow fix / session %s / ACTIVE", doc, contractSession)
	}
	if err := shared.VerifyChain(doc.RunID); err != nil {
		t.Errorf("VerifyChain(%s): %v", doc.RunID, err)
	}
	evs := allTraceEvents(t)
	started := eventsOfType(evs, shared.EventWorkflowStarted)
	if len(started) != 1 {
		t.Fatalf("workflow.started events = %d, want 1 (all events: %v)", len(started), eventTypes(evs))
	}
	if started[0].Payload["workflow"] != "fix" {
		t.Errorf("workflow.started payload workflow = %v, want fix", started[0].Payload["workflow"])
	}
	if started[0].Source != "userpromptsubmit" {
		t.Errorf("workflow.started source = %q, want userpromptsubmit", started[0].Source)
	}

	// Second run-start prompt, same session, first run still live: no second
	// run is ever created.
	code, _, stderr = runBinary(t, "userpromptsubmit", upsPayload(t, "/summoner:new another feature"), nil)
	if code != 0 {
		t.Fatalf("second run-start exit = %d, want 0 — refusing is not an error (stderr: %q)", code, stderr)
	}
	if files = runStateFiles(t); len(files) != 1 {
		t.Fatalf("run state files after second prompt = %d, want still exactly 1", len(files))
	}
	if n := len(eventsOfType(allTraceEvents(t), shared.EventWorkflowStarted)); n != 1 {
		t.Errorf("workflow.started events after second prompt = %d, want 1", n)
	}
}

// TestEndToEndRunStartExpandedForm: the expanded delivery form — a prompt
// carrying <command-name>summoner:new</command-name> — starts the run with
// workflow "new" (dual matching covers typed and expanded forms).
func TestEndToEndRunStartExpandedForm(t *testing.T) {
	useContractEnv(t)
	prompt := "<command-name>summoner:new</command-name>\n<command-message>summoner:new</command-message>\nbuild the export feature"
	code, _, stderr := runBinary(t, "userpromptsubmit", upsPayload(t, prompt), nil)
	if code != 0 {
		t.Fatalf("userpromptsubmit exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	files := runStateFiles(t)
	if len(files) != 1 {
		t.Fatalf("run state files = %d, want exactly 1", len(files))
	}
	var doc struct {
		Workflow string `json:"workflow"`
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read run state: %v", err)
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse run state: %v", err)
	}
	if doc.Workflow != "new" {
		t.Errorf("expanded-form run workflow = %q, want new", doc.Workflow)
	}
	started := eventsOfType(allTraceEvents(t), shared.EventWorkflowStarted)
	if len(started) != 1 || started[0].Payload["workflow"] != "new" {
		t.Errorf("workflow.started events = %+v, want one with workflow new", started)
	}
}

// TestEndToEndNonSummonerPromptNoOp: a plain prompt (even one containing the
// word "fix") touches nothing — no run, no events.
func TestEndToEndNonSummonerPromptNoOp(t *testing.T) {
	useContractEnv(t)
	code, _, stderr := runBinary(t, "userpromptsubmit", upsPayload(t, "please fix the login page yourself"), nil)
	if code != 0 {
		t.Fatalf("userpromptsubmit exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if files := runStateFiles(t); len(files) != 0 {
		t.Errorf("non-Summoner prompt must start no run, got %v", files)
	}
	if evs := allTraceEvents(t); len(evs) != 0 {
		t.Errorf("non-Summoner prompt must leave no events, got %v", eventTypes(evs))
	}
}

// --- T16: two-turn decision surface over the real binaries -------------------------

// stopRenderPayload builds a Stop payload whose last_assistant_message is the
// given render text (the display act).
func stopRenderPayload(t *testing.T, lastMsg string) string {
	t.Helper()
	return mustJSON(t, map[string]interface{}{
		"hook_event_name":        "Stop",
		"session_id":             contractSession,
		"stop_hook_active":       false,
		"last_assistant_message": lastMsg,
	})
}

// askQuestionPayload builds a PreToolUse AskUserQuestion payload whose single
// question carries the nonce and offers exactly the given labels.
func askQuestionPayload(t *testing.T, toolUseID, nonce string, labels []string) string {
	t.Helper()
	opts := make([]interface{}, 0, len(labels))
	for _, l := range labels {
		opts = append(opts, map[string]interface{}{"label": l, "description": "decision option"})
	}
	qtext := "Checkpoint decision (nonce " + nonce + "): how should the run proceed?"
	return mustJSON(t, map[string]interface{}{
		"session_id":  contractSession,
		"tool_name":   "AskUserQuestion",
		"tool_use_id": toolUseID,
		"tool_input": map[string]interface{}{"questions": []interface{}{map[string]interface{}{
			"question":    qtext,
			"header":      "Checkpoint",
			"options":     opts,
			"multiSelect": false,
		}}},
	})
}

// askQuestionText returns the question text askQuestionPayload builds (the
// answers map key of the platform-injected tool_response).
func askQuestionText(nonce string) string {
	return "Checkpoint decision (nonce " + nonce + "): how should the run proceed?"
}

// renderMessageFor builds the assistant message of the render turn: the
// checkpoint-only canonical view produced by the SAME production renderer
// whose block the Stop hook's display verification requires the message to
// contain — the test never hand-writes display bytes.
func renderMessageFor(run *shared.RunState, cp *shared.CheckpointState) string {
	return shared.RenderCheckpointMessage(run, *cp)
}

// TestEndToEndTwoTurnDecisionReviewRoute — T16's runtime contract over the
// REAL binaries in the REAL event order: a review run starts; phase-submit
// auto-opens the route's first phase (context); the SAME-TURN nonce ask is
// denied with the two-turn instruction; the Stop hook binds display off the
// pasted render (menu labels notwithstanding); the NEXT-turn ask passes only
// with the persisted page's exact labels; PostToolUse records the decision.
func TestEndToEndTwoTurnDecisionReviewRoute(t *testing.T) {
	useContractEnv(t)

	// Run start (typed /summoner:review prompt) — the ONLY run starter.
	code, _, stderr := runBinary(t, "userpromptsubmit", upsPayload(t, "/summoner:review the recent change"), nil)
	if code != 0 {
		t.Fatalf("userpromptsubmit exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	files := runStateFiles(t)
	if len(files) != 1 {
		t.Fatalf("run state files = %d, want 1", len(files))
	}
	runID := strings.TrimSuffix(filepath.Base(files[0]), ".json")
	run, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if run.Workflow != "review" {
		t.Fatalf("workflow = %q, want review", run.Workflow)
	}

	// Phase-submit for the route's first phase auto-opens it (no Skill call).
	code, _, stderr = runBinary(t, "pretooluse-gate", gatePayload(t, "Skill", "tu-ps-1", map[string]interface{}{
		"skill": "summoner:phase-submit",
		"args":  `{"phase_id":"context","summary":"verification criteria frozen"}`,
	}), nil)
	if code != 0 {
		t.Fatalf("phase-submit(context) exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != shared.StatusWaitUser || len(run.Phases) != 1 || run.Phases[0].ID != "context" {
		t.Fatalf("after submit: status %s phases %+v", run.Status, run.Phases)
	}
	cp := run.CurrentCheckpoint()
	if cp == nil || len(cp.MenuPages) == 0 {
		t.Fatalf("checkpoint must carry the persisted menu projection: %+v", cp)
	}
	if cp.Displayed {
		t.Fatal("fixture must be undisplayed (never pre-seed Displayed)")
	}

	// SAME-TURN ask (display not yet bound): denied with the two-turn
	// instruction — the turn must end with the render first.
	code, _, stderr = runBinary(t, "pretooluse-gate",
		askQuestionPayload(t, "tu-ask-early", cp.Nonce, cp.MenuPages[0].Labels), nil)
	if code != 2 || !strings.Contains(stderr, "two-turn") {
		t.Fatalf("same-turn ask: (code, stderr) = (%d, %q), want exit 2 + two-turn instruction", code, stderr)
	}

	// Render turn: the pasted render IS the display act — the Stop hook binds
	// it (menu labels inside the render must not satisfy the stop-ack).
	code, _, stderr = runBinary(t, "stop", stopRenderPayload(t, renderMessageFor(run, cp)), nil)
	if code != 0 {
		t.Fatalf("stop exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	cp = run.CurrentCheckpoint()
	if !cp.Displayed {
		t.Fatal("the Stop hook must bind the display off the pasted render")
	}

	// Offered-set mismatch while displayed: denied.
	code, _, stderr = runBinary(t, "pretooluse-gate",
		askQuestionPayload(t, "tu-ask-bad", cp.Nonce, cp.MenuPages[0].Labels[:3]), nil)
	if code != 2 || !strings.Contains(stderr, "menu page") {
		t.Fatalf("offered-set mismatch: (code, stderr) = (%d, %q), want exit 2 + menu-page pin", code, stderr)
	}

	// Next-turn ask with the persisted page's exact labels: allowed. T18: the
	// checkpoint is the run's first and carries the budget ask — the question
	// rides along nonce-free (the gate admits it untouched; PostToolUse
	// settles it with the decision in one commit).
	code, _, stderr = runBinary(t, "pretooluse-gate",
		askQuestionPayload(t, "tu-ask-1", cp.Nonce, cp.MenuPages[0].Labels), nil)
	if code != 0 {
		t.Fatalf("page-pinned ask exit = %d, want 0 (stderr: %q)", code, stderr)
	}

	// The platform-injected tool_response records the decision (PostToolUse).
	// The tool_input carries the SAME offered options the gate validated.
	qtext := askQuestionText(cp.Nonce)
	answer := cp.MenuPages[0].Labels[0]
	opts := make([]interface{}, 0, len(cp.MenuPages[0].Labels))
	for _, l := range cp.MenuPages[0].Labels {
		opts = append(opts, map[string]interface{}{"label": l, "description": "decision option"})
	}
	rides, rideAnswers := budgetRides(cp, "unlimited", "")
	questions := append([]interface{}{map[string]interface{}{
		"question": qtext, "header": "Checkpoint", "options": opts, "multiSelect": false,
	}}, rides...)
	allAnswers := map[string]interface{}{qtext: answer}
	for k, v := range rideAnswers {
		allAnswers[k] = v
	}
	code, _, stderr = runBinary(t, "posttooluse", mustJSON(t, map[string]interface{}{
		"hook_event_name": "PostToolUse",
		"session_id":      contractSession,
		"tool_name":       "AskUserQuestion",
		"tool_use_id":     "tu-ask-1",
		"tool_input":      map[string]interface{}{"questions": questions},
		"tool_response": map[string]interface{}{
			"questions": []interface{}{map[string]interface{}{"question": qtext}},
			"answers":   allAnswers,
		},
	}), nil)
	if code != 0 {
		t.Fatalf("posttooluse decision exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != shared.StatusActive {
		t.Fatalf("status = %s, want ACTIVE after CONTINUE", run.Status)
	}
	if got := run.Checkpoints[0]; !got.Decided || got.Decision != shared.DecisionContinue || got.DecisionToolUseID != "tu-ask-1" {
		t.Fatalf("decision = %+v, want decided CONTINUE bound to tu-ask-1", got)
	}
}

// --- T17: the review-submit channel over the REAL binaries --------------------------

// contractEnvelopeJSON builds the identity-only review registration as the
// args JSON string (final-fix C1: the reducer mints every semantic field from
// the pinned acceptance snapshot; the transport is identity-only).
func contractEnvelopeJSON(t *testing.T, runID, reviewID string) string {
	t.Helper()
	b, err := json.Marshal(shared.Envelope{
		SchemaVersion: "2",
		RunID:         runID,
		ReviewID:      reviewID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// contractReviewerCapsule returns the exact launch-capsule bytes of the run's
// PREPARED review, reloading state first (the review was registered through
// the real gate binary; BuildReviewCapsule is the same authority the gate
// matches by whole-message equality — prose prompts never bind).
func contractReviewerCapsule(t *testing.T, runID string) string {
	t.Helper()
	run, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range run.Reviews {
		rv := &run.Reviews[i]
		if rv.Status != shared.ReviewPrepared {
			continue
		}
		capsule, ok := shared.BuildReviewCapsule(run, rv.ReviewID, rv.ActiveAttemptID)
		if !ok {
			t.Fatalf("BuildReviewCapsule(%s): not dispatchable", rv.ReviewID)
		}
		b, err := capsule.Bytes()
		if err != nil {
			t.Fatalf("capsule.Bytes(): %v", err)
		}
		return string(b)
	}
	t.Fatal("no PREPARED review for the capsule")
	return ""
}

// contractPassResult builds the reviewer's COMPLIANT PASS ReviewResult for the
// run's in-flight review: the claimed artifact digest is the MINTED
// ArtifactSetDigest the registration recorded (never a hand-invented string),
// and the criterion verdicts cover EVERY minted exit criterion (frozen − drops
// + the shipped defaults), each pass anchored to the reviewer's real
// transcript tool_use id (toolu-rev-1 — the id contractReviewerTranscript
// writes).
func contractPassResult(t *testing.T, runID string) shared.ReviewResult {
	t.Helper()
	run, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	var rv *shared.ReviewRecord
	for i := range run.Reviews {
		if run.Reviews[i].Status == shared.ReviewRunning || run.Reviews[i].Status == shared.ReviewPrepared {
			rv = &run.Reviews[i]
		}
	}
	if rv == nil {
		t.Fatal("no in-flight review for the result")
	}
	res := shared.ReviewResult{
		ReviewID:            rv.ReviewID,
		ArtifactDigest:      rv.ArtifactDigest,
		RunStatus:           shared.ReviewRunCompleted,
		Verdict:             shared.VerdictPass,
		EvidenceToolCallIDs: []string{"toolu-rev-1"},
	}
	for _, c := range rv.Envelope.ExitCriteria {
		res.CriterionVerdicts = append(res.CriterionVerdicts, shared.CriterionVerdict{
			CriterionID: c.ID,
			Status:      "pass",
			Evidence:    []shared.EvidenceAnchor{{ToolUseID: "toolu-rev-1"}},
		})
	}
	return res
}

// budgetRides builds the nonce-free budget/extend ride-along questions of a
// checkpoint's PERSISTED asks (T18), plus the answers-map entries for the
// requested per-kind choice: budgetChoice "confirm"|"unlimited" (absent = no
// budget ride), extendChoice "extend"|"hold" (absent = no extend ride). The
// labels come from the persisted asks — never hand-written.
func budgetRides(cp *shared.CheckpointState, budgetChoice, extendChoice string) ([]interface{}, map[string]string) {
	var qs []interface{}
	answers := map[string]string{}
	opts := func(a, b string) []interface{} {
		return []interface{}{
			map[string]interface{}{"label": a, "description": "budget option"},
			map[string]interface{}{"label": b, "description": "budget option"},
		}
	}
	if cp.BudgetAsk != nil && budgetChoice != "" {
		ask := cp.BudgetAsk
		text := "预算确认 Budget confirmation (run-start): confirm the displayed proposal?"
		qs = append(qs, map[string]interface{}{
			"question": text, "header": "Budget", "options": opts(ask.ConfirmLabel, ask.UnlimitedLabel), "multiSelect": false,
		})
		switch budgetChoice {
		case "confirm":
			answers[text] = ask.ConfirmLabel
		case "unlimited":
			answers[text] = ask.UnlimitedLabel
		}
	}
	if cp.ExtendAsk != nil && extendChoice != "" {
		ask := cp.ExtendAsk
		text := "预算追加 Budget extension (exhausted): add the displayed amount once more?"
		qs = append(qs, map[string]interface{}{
			"question": text, "header": "Extend", "options": opts(ask.ExtendLabel, ask.HoldLabel), "multiSelect": false,
		})
		switch extendChoice {
		case "extend":
			answers[text] = ask.ExtendLabel
		case "hold":
			answers[text] = ask.HoldLabel
		}
	}
	return qs, answers
}

// renderAndDecide drives the two-turn decision cycle over the REAL binaries
// for the given already-open checkpoint: Stop binds the pasted render, the
// gate admits the page-pinned nonce question (pageIdx of the persisted
// pages; negative = the page CARRYING the review: option — re-review F1
// appends the recall-target page AFTER the kind pages, so "last page" no
// longer means "the review page"), PostToolUse records the chosen label.
// sel picks the offered label. T18: the persisted budget/extension asks ride
// along with their DEFAULT answers (unlimited / hold) so the generic cycle
// settles them; budget-specific tests use renderAndDecideSettle.
func renderAndDecide(t *testing.T, runID, toolUseID string, cp *shared.CheckpointState, pageIdx int, sel func([]string) string) {
	t.Helper()
	renderAndDecideSettle(t, runID, toolUseID, cp, pageIdx, sel, "unlimited", "hold")
}

// renderAndDecideSettle is renderAndDecide with explicit budget settlement
// choices (budgetRides' per-kind choice strings).
func renderAndDecideSettle(t *testing.T, runID, toolUseID string, cp *shared.CheckpointState, pageIdx int, sel func([]string) string, budgetChoice, extendChoice string) {
	t.Helper()
	// The render turn needs the run for the canonical renderer (run header).
	renderRun, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runBinary(t, "stop", stopRenderPayload(t, renderMessageFor(renderRun, cp)), nil); code != 0 {
		t.Fatalf("stop render exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	run, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	cp = run.CurrentCheckpoint()
	if cp == nil || !cp.Displayed {
		t.Fatalf("the Stop hook must bind the display first (cp: %+v)", cp)
	}
	if pageIdx < 0 {
		// F1: the review page is the one CARRYING a review-led label — not
		// the last page (the recall-target page is appended after it).
		pageIdx = len(cp.MenuPages) - 1
		for i, pg := range cp.MenuPages {
			if labelHasKeyword(pg.Labels, "review") {
				pageIdx = i
				break
			}
		}
	}
	page := cp.MenuPages[pageIdx]
	answer := sel(page.Labels)
	rides, rideAnswers := budgetRides(cp, budgetChoice, extendChoice)
	qtext := askQuestionText(cp.Nonce)
	opts := make([]interface{}, 0, len(page.Labels))
	for _, l := range page.Labels {
		opts = append(opts, map[string]interface{}{"label": l, "description": "decision option"})
	}
	decisionQ := map[string]interface{}{
		"question": qtext, "header": "Checkpoint", "options": opts, "multiSelect": false,
	}
	questions := append([]interface{}{decisionQ}, rides...)
	// The gate admits the nonce question page-pinned, rides along untouched.
	if code, _, stderr := runBinary(t, "pretooluse-gate", mustJSON(t, map[string]interface{}{
		"session_id":  contractSession,
		"tool_name":   "AskUserQuestion",
		"tool_use_id": toolUseID,
		"tool_input":  map[string]interface{}{"questions": questions},
	}), nil); code != 0 {
		t.Fatalf("gate ask exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	allAnswers := map[string]interface{}{qtext: answer}
	for k, v := range rideAnswers {
		allAnswers[k] = v
	}
	if code, _, stderr := runBinary(t, "posttooluse", mustJSON(t, map[string]interface{}{
		"hook_event_name": "PostToolUse",
		"session_id":      contractSession,
		"tool_name":       "AskUserQuestion",
		"tool_use_id":     toolUseID,
		"tool_input":      map[string]interface{}{"questions": questions},
		"tool_response": map[string]interface{}{
			"answers": allAnswers,
		},
	}), nil); code != 0 {
		t.Fatalf("posttooluse decision exit = %d, want 0 (stderr: %q)", code, stderr)
	}
}

// submitPhaseViaGate drives one phase-submit through the real gate binary.
// The payload carries a minimal conclusion: since 0.7.0 a diagnosis
// submission without conclusion.summary/approach is denied. The outcome is
// left out (stored unclear) — an unclear submission never auto-passes, so
// every fixture lands on a checkpoint the test then decides itself.
func submitPhaseViaGate(t *testing.T, toolUseID, phaseID string, criteria []string) {
	t.Helper()
	if code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Skill", toolUseID, map[string]interface{}{
		"skill": "summoner:phase-submit",
		"args": mustJSON(t, shared.PhaseSubmission{
			PhaseID: phaseID, Summary: phaseID + " done", CriteriaIDs: criteria,
			Conclusion: &shared.Conclusion{Summary: phaseID + " conclusion", Approach: phaseID + " approach"},
		}),
	}), nil); code != 0 {
		t.Fatalf("phase-submit(%s) exit = %d, want 0 (stderr: %q)", phaseID, code, stderr)
	}
}

// firstLabelWithKeyword picks the offered label whose leading keyword matches.
func firstLabelWithKeyword(labels []string, keyword string) string {
	for _, l := range labels {
		fields := strings.Fields(l)
		if len(fields) > 0 {
			w := strings.ToLower(strings.TrimRight(fields[0], ":?!"))
			if w == keyword {
				return l
			}
		}
	}
	return labels[0]
}

// labelHasKeyword reports whether any label leads with the keyword (no
// fallback — firstLabelWithKeyword silently returns labels[0]).
func labelHasKeyword(labels []string, keyword string) bool {
	for _, l := range labels {
		fields := strings.Fields(l)
		if len(fields) > 0 && strings.ToLower(strings.TrimRight(fields[0], ":?!")) == keyword {
			return true
		}
	}
	return false
}

// contractReviewerTranscript writes a reviewer transcript: the platform
// INITIAL task record carrying the EXACT admitted capsule (QA-1 ownership
// bind — the SubagentStop join verifies this record before any verdict), one
// REAL observed evidence call (tool_use toolu-rev-1 + its paired successful
// tool_result — the final-fix wave's reconciliation only accepts observation
// tools with a successful observed result; an unpaired tool_use proves
// nothing) plus a SubagentHandback carrying resultJSON.
func contractReviewerTranscript(t *testing.T, runID, resultJSON string) string {
	t.Helper()
	run, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	initial := "run review " + runID // overwritten below when an in-flight review owns the capsule
	for i := range run.Reviews {
		rv := &run.Reviews[i]
		if rv.Status != shared.ReviewRunning && rv.Status != shared.ReviewPrepared {
			continue
		}
		capsule, ok := shared.BuildReviewCapsule(run, rv.ReviewID, rv.ActiveAttemptID)
		if !ok {
			t.Fatalf("BuildReviewCapsule(%s): not dispatchable", rv.ReviewID)
		}
		b, err := capsule.Bytes()
		if err != nil {
			t.Fatalf("capsule.Bytes(): %v", err)
		}
		initial = string(b)
		break
	}
	lines := []string{
		mustJSON(t, map[string]interface{}{
			"type":    "user",
			"message": map[string]interface{}{"role": "user", "content": initial},
		}),
		mustJSON(t, map[string]interface{}{
			"type": "assistant",
			"message": map[string]interface{}{
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "re-deriving criteria"},
					map[string]interface{}{"type": "tool_use", "id": "toolu-rev-1", "name": "Read",
						"input": map[string]interface{}{"file_path": "docs/reviews/r.md"}},
				},
			},
		}),
		mustJSON(t, map[string]interface{}{
			"type": "user",
			"message": map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{"type": "tool_result", "tool_use_id": "toolu-rev-1",
						"is_error": false, "content": "the reviewed file's bytes"},
				},
			},
		}),
		mustJSON(t, map[string]interface{}{
			"type": "assistant",
			"message": map[string]interface{}{
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "review complete"},
					map[string]interface{}{"type": "tool_use", "id": "toolu_hb_1", "name": "SubagentHandback",
						"input": map[string]interface{}{"message": resultJSON}},
				},
			},
		}),
	}
	p := filepath.Join(t.TempDir(), "reviewer-transcript.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// contractRunAtReviewPending drives the shared prefix over the REAL binaries:
// fix run start → context submitted+accepted (criteria frozen) → diagnosis
// submitted → [v] page decision → REVIEW_PENDING under an obligation.
// Returns the run id.
func contractRunAtReviewPending(t *testing.T, frozen, pending []string) string {
	t.Helper()
	if code, _, stderr := runBinary(t, "userpromptsubmit", upsPayload(t, "/summoner:fix broken login"), nil); code != 0 {
		t.Fatalf("run start exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	files := runStateFiles(t)
	if len(files) != 1 {
		t.Fatalf("run state files = %d, want 1", len(files))
	}
	runID := strings.TrimSuffix(filepath.Base(files[0]), ".json")

	submitPhaseViaGate(t, "tu-ps-1", "context", frozen)
	run, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	renderAndDecide(t, runID, "tu-ask-1", run.CurrentCheckpoint(), 0, func(labels []string) string {
		return firstLabelWithKeyword(labels, "continue")
	})

	submitPhaseViaGate(t, "tu-ps-2", "diagnosis", pending)
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	// The [v] lives on the second kind page of the PHASE menu; the offered
	// set must equal a persisted page, so ask with THAT page's labels and
	// answer `review:` (F1 appends the recall-target page after the kind
	// pages — locate the page by its label, not by position).
	cp := run.CurrentCheckpoint()
	if code, _, stderr := runBinary(t, "stop", stopRenderPayload(t, renderMessageFor(run, cp)), nil); code != 0 {
		t.Fatalf("stop render exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	page2 := cp.MenuPages[len(cp.MenuPages)-1]
	for _, pg := range cp.MenuPages {
		if labelHasKeyword(pg.Labels, "review") {
			page2 = pg
			break
		}
	}
	if !labelHasKeyword(page2.Labels, "review") {
		t.Fatalf("no PHASE menu page carries the review label: %+v", cp.MenuPages)
	}
	if code, _, stderr := runBinary(t, "pretooluse-gate", askQuestionPayload(t, "tu-ask-2", cp.Nonce, page2.Labels), nil); code != 0 {
		t.Fatalf("gate [v] ask exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	qtext := askQuestionText(cp.Nonce)
	opts := make([]interface{}, 0, len(page2.Labels))
	for _, l := range page2.Labels {
		opts = append(opts, map[string]interface{}{"label": l, "description": "decision option"})
	}
	answer := firstLabelWithKeyword(page2.Labels, "review")
	if code, _, stderr := runBinary(t, "posttooluse", mustJSON(t, map[string]interface{}{
		"hook_event_name": "PostToolUse",
		"session_id":      contractSession,
		"tool_name":       "AskUserQuestion",
		"tool_use_id":     "tu-ask-2",
		"tool_input": map[string]interface{}{"questions": []interface{}{map[string]interface{}{
			"question": qtext, "header": "Checkpoint", "options": opts, "multiSelect": false,
		}}},
		"tool_response": map[string]interface{}{
			"answers": map[string]interface{}{qtext: answer},
		},
	}), nil); code != 0 {
		t.Fatalf("posttooluse [v] exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != shared.StatusReviewPending || run.ReviewObligation == nil {
		t.Fatalf("after [v]: status %s obligation %+v, want REVIEW_PENDING under an obligation", run.Status, run.ReviewObligation)
	}
	return runID
}

// TestEndToEndReviewSubmitRoundTrip: [v] round trip over the REAL binaries in
// the REAL event order — fix run → [v] page decision → REVIEW_PENDING →
// review-submit envelope (validated against the frozen view) → review.prepared
// → Agent(reviewer) admitted → SubagentStop verdict with a PASSING evidence
// join → REVIEW_RESULT checkpoint.
func TestEndToEndReviewSubmitRoundTrip(t *testing.T) {
	useContractEnv(t)
	runID := contractRunAtReviewPending(t, []string{"crit-ctx"}, []string{"crit-diag"})

	// review-submit: the envelope cites exactly the frozen view.
	if code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Skill", "tu-rs-1", map[string]interface{}{
		"skill": "summoner:review-submit",
		"args":  contractEnvelopeJSON(t, runID, "rev-1"),
	}), nil); code != 0 {
		t.Fatalf("review-submit exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	run, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.ReviewObligation != nil || len(run.Reviews) != 1 || run.Reviews[0].Status != shared.ReviewPrepared {
		t.Fatalf("after review-submit: obligation %+v reviews %+v, want consumed + PREPARED", run.ReviewObligation, run.Reviews)
	}

	// The reviewer delegation is admitted only with the PREPARED id.
	if code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Agent", "tu-ag-1", map[string]interface{}{
		"subagent_type": "reviewer",
		"prompt":        contractReviewerCapsule(t, runID),
		"description":   "independent review",
	}), nil); code != 0 {
		t.Fatalf("reviewer Agent exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Reviews[0].Status != shared.ReviewRunning {
		t.Fatalf("review = %s, want RUNNING after the reviewer delegation", run.Reviews[0].Status)
	}

	// SubagentStop: the transcript's real tool_use id joins the claimed
	// evidence; the PASS verdict backfills through the reducer.
	result := contractPassResult(t, runID)
	resJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	transcript := contractReviewerTranscript(t, runID, string(resJSON))
	if code, _, stderr := runBinary(t, "subagentstop", mustJSON(t, map[string]interface{}{
		"hook_event_name":       "SubagentStop",
		"session_id":            contractSession,
		"stop_hook_active":      false,
		"agent_id":              "ag-rev-1",
		"agent_type":            "reviewer",
		"agent_transcript_path": transcript,
	}), nil); code != 0 {
		t.Fatalf("subagentstop exit = %d, want 0 (stderr: %q)", code, stderr)
	}

	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	rv := run.Reviews[0]
	if rv.Status != shared.ReviewReviewed || rv.Verdict != shared.VerdictPass {
		t.Fatalf("review = %+v, want REVIEWED PASS after the evidence join", rv)
	}
	cp := run.CurrentCheckpoint()
	if cp == nil || cp.Kind != shared.CheckpointReviewResult || run.Status != shared.StatusWaitUser {
		t.Fatalf("after the verdict: status %s checkpoint %+v, want WAIT_USER at a REVIEW_RESULT checkpoint", run.Status, cp)
	}

	evs := allTraceEvents(t)
	if n := len(eventsOfType(evs, shared.EventReviewPrepared)); n != 1 {
		t.Errorf("review.prepared events = %d, want exactly 1", n)
	}
	if n := len(eventsOfType(evs, shared.EventDelegationStarted)); n != 1 {
		t.Errorf("delegation.started events = %d, want 1", n)
	}
	if n := len(eventsOfType(evs, shared.EventDelegationCompleted)); n != 1 {
		t.Errorf("delegation.completed events = %d, want 1", n)
	}
	if verdicts := eventsOfType(evs, shared.EventReviewVerdict); len(verdicts) != 1 || verdicts[0].Payload["review_id"] != "rev-1" {
		t.Errorf("review.verdict events = %+v, want exactly one for rev-1", verdicts)
	} else if ids, ok := verdicts[0].Payload["evidence_tool_call_ids"].([]interface{}); !ok || len(ids) != 1 || ids[0] != "toolu-rev-1" {
		// T12 fix-back: a REAL flow carries the reconciled evidence ids
		// end-to-end — transcript tool_use → ReconcileEvidence join →
		// reducer record → review.verdict payload.
		t.Errorf("review.verdict evidence_tool_call_ids = %#v, want [toolu-rev-1] mirrored end-to-end", verdicts[0].Payload["evidence_tool_call_ids"])
	} else if verdicts[0].Payload["artifact_digest"] != rv.ArtifactDigest {
		// T12 fix-back 2 (split ruling A): the record's digests mirror
		// end-to-end too — artifact identity exactly (the MINTED
		// ArtifactSetDigest), envelope identity present (opaque digest).
		t.Errorf("review.verdict artifact_digest = %#v, want the minted %s", verdicts[0].Payload["artifact_digest"], rv.ArtifactDigest)
	} else if env, ok := verdicts[0].Payload["envelope_digest"].(string); !ok || env == "" {
		t.Errorf("review.verdict envelope_digest = %#v, want a non-empty digest", verdicts[0].Payload["envelope_digest"])
	}
	if err := shared.VerifyChain(runID); err != nil {
		t.Errorf("VerifyChain: %v", err)
	}
}

// TestEndToEndForgedEvidenceStaysDecidable: a forged evidence id in the
// reviewer's result routes to review.failed + a FAILED REVIEW_RESULT
// checkpoint over the REAL binaries — and the run stays decidable: the routed
// checkpoint takes a normal two-turn CONTINUE afterwards.
func TestEndToEndForgedEvidenceStaysDecidable(t *testing.T) {
	useContractEnv(t)
	runID := contractRunAtReviewPending(t, []string{"crit-ctx"}, []string{"crit-diag"})

	if code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Skill", "tu-rs-1", map[string]interface{}{
		"skill": "summoner:review-submit",
		"args":  contractEnvelopeJSON(t, runID, "rev-1"),
	}), nil); code != 0 {
		t.Fatalf("review-submit exit = %d, want 0 (stderr: %q)", code, stderr)
	}

	result := shared.ReviewResult{
		ReviewID:            "rev-1",
		RunStatus:           shared.ReviewRunCompleted,
		Verdict:             shared.VerdictPass,
		EvidenceToolCallIDs: []string{"toolu-rev-1", "toolu-forged-9"},
	}
	resJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runBinary(t, "subagentstop", mustJSON(t, map[string]interface{}{
		"hook_event_name":       "SubagentStop",
		"session_id":            contractSession,
		"stop_hook_active":      false,
		"agent_id":              "ag-rev-1",
		"agent_type":            "reviewer",
		"agent_transcript_path": contractReviewerTranscript(t, runID, string(resJSON)),
	}), nil); code != 0 {
		t.Fatalf("subagentstop exit = %d, want 0 (stderr: %q)", code, stderr)
	}

	run, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Reviews[0].Status != shared.ReviewFailed {
		t.Fatalf("review = %s, want FAILED (forged evidence id)", run.Reviews[0].Status)
	}
	cp := run.CurrentCheckpoint()
	if cp == nil || cp.Kind != shared.CheckpointReviewResult || run.Status != shared.StatusWaitUser {
		t.Fatalf("after the forged result: status %s checkpoint %+v, want WAIT_USER at a REVIEW_RESULT checkpoint", run.Status, cp)
	}
	if n := len(eventsOfType(allTraceEvents(t), shared.EventReviewFailed)); n != 1 {
		t.Errorf("review.failed events = %d, want exactly 1", n)
	}

	// Decidable: the routed checkpoint takes a normal two-turn decision.
	renderAndDecide(t, runID, "tu-ask-3", cp, 0, func(labels []string) string {
		return firstLabelWithKeyword(labels, "continue")
	})
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != shared.StatusActive {
		t.Fatalf("status = %s, want ACTIVE after deciding the FAILED checkpoint — the run must stay decidable", run.Status)
	}
	if err := shared.VerifyChain(runID); err != nil {
		t.Errorf("VerifyChain: %v", err)
	}
}

// TestEndToEndEnvelopeBeforeFreezeRejected (fix-round m4): on a standalone
// review run, a [v] at the context checkpoint BEFORE any acceptance froze
// anything is REFUSED over the REAL binaries — with an empty frozen view the
// run would otherwise park in an unrecoverable REVIEW_PENDING (obligation
// consumed-adjacent, no open checkpoint, every channel refused). The refusal
// is a decision_rejected; the checkpoint stays open and decidable.
func TestEndToEndEnvelopeBeforeFreezeRejected(t *testing.T) {
	useContractEnv(t)
	if code, _, stderr := runBinary(t, "userpromptsubmit", upsPayload(t, "/summoner:review the recent change"), nil); code != 0 {
		t.Fatalf("run start exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	files := runStateFiles(t)
	if len(files) != 1 {
		t.Fatalf("run state files = %d, want 1", len(files))
	}
	runID := strings.TrimSuffix(filepath.Base(files[0]), ".json")

	submitPhaseViaGate(t, "tu-ps-1", "context", []string{"crit-correctness"})
	run, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	// [v] BEFORE any acceptance: the frozen view is empty, so the [v] itself
	// is refused (the review option lives on the LAST page of the PHASE menu).
	renderAndDecide(t, runID, "tu-ask-1", run.CurrentCheckpoint(), -1, func(labels []string) string {
		return firstLabelWithKeyword(labels, "review")
	})
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != shared.StatusWaitUser {
		t.Fatalf("status = %s, want WAIT_USER — the refused [v] must leave the checkpoint open", run.Status)
	}
	if cp := run.CurrentCheckpoint(); cp == nil || cp.Decided {
		t.Fatalf("the context checkpoint must stay open and undecided, got %+v", cp)
	}
	if run.ReviewObligation != nil {
		t.Fatal("the refused [v] must record no obligation")
	}
	rejected := eventsOfType(allTraceEvents(t), shared.EventCheckpointDecisionRejected)
	if len(rejected) != 1 || !strings.Contains(rejected[0].Payload["reason"].(string), "frozen acceptance view is empty") {
		t.Fatalf("checkpoint.decision_rejected events = %+v, want exactly one naming the empty frozen view", rejected)
	}

	// review-submit denies on the WAIT_USER discipline — the refused [v]
	// left the checkpoint open, so it still comes first.
	code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Skill", "tu-rs-1", map[string]interface{}{
		"skill": "summoner:review-submit",
		"args":  contractEnvelopeJSON(t, runID, "rev-1"),
	}), nil)
	if code != 2 || !strings.Contains(stderr, "WAIT_USER") {
		t.Fatalf("review-submit after the refused [v]: (code, stderr) = (%d, %q), want exit 2 + WAIT_USER denial", code, stderr)
	}
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Reviews) != 0 || run.ReviewObligation != nil {
		t.Fatalf("nothing may register before the freeze (reviews %+v, obligation %+v)", run.Reviews, run.ReviewObligation)
	}

	// CONTINUE at the still-open checkpoint freezes the scope; the [v] is now
	// a legal act at the next checkpoint.
	renderAndDecide(t, runID, "tu-ask-2", run.CurrentCheckpoint(), 0, func(labels []string) string {
		return firstLabelWithKeyword(labels, "continue")
	})
	submitPhaseViaGate(t, "tu-ps-2", "review", nil)
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	renderAndDecide(t, runID, "tu-ask-3", run.CurrentCheckpoint(), -1, func(labels []string) string {
		return firstLabelWithKeyword(labels, "review")
	})
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != shared.StatusReviewPending || run.ReviewObligation == nil {
		t.Fatalf("after the post-freeze [v]: status %s obligation %+v, want REVIEW_PENDING under an obligation", run.Status, run.ReviewObligation)
	}
}

// TestEndToEndTerminalDefaultReviewNoOptIn (spec §1 终局⑤默认强制 — fix
// round): over the REAL binaries, the terminal independent review needs NO
// [v]: the review-phase checkpoint's plain continue: leads into the
// registered default review and the reviewer dispatch. An explicit skip:
// closes the default.
func TestEndToEndTerminalDefaultReviewNoOptIn(t *testing.T) {
	useContractEnv(t)
	if code, _, stderr := runBinary(t, "userpromptsubmit", upsPayload(t, "/summoner:review the recent change"), nil); code != 0 {
		t.Fatalf("run start exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	files := runStateFiles(t)
	if len(files) != 1 {
		t.Fatalf("run state files = %d, want 1", len(files))
	}
	runID := strings.TrimSuffix(filepath.Base(files[0]), ".json")

	submitPhaseViaGate(t, "tu-ps-1", "context", []string{"crit-correctness"})
	run, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	renderAndDecide(t, runID, "tu-ask-1", run.CurrentCheckpoint(), 0, func(labels []string) string {
		return firstLabelWithKeyword(labels, "continue")
	})

	// The review phase's checkpoint takes a PLAIN continue: — page 1, never
	// the [v] page.
	submitPhaseViaGate(t, "tu-ps-2", "review", nil)
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	renderAndDecide(t, runID, "tu-ask-2", run.CurrentCheckpoint(), 0, func(labels []string) string {
		return firstLabelWithKeyword(labels, "continue")
	})
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != shared.StatusActive || run.ReviewObligation != nil {
		t.Fatalf("after the default accept: %s / obligation %+v — no [v] was answered anywhere", run.Status, run.ReviewObligation)
	}

	// The channel is open from ACTIVE: envelope registers, reviewer dispatches.
	if code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Skill", "tu-rs-1", map[string]interface{}{
		"skill": "summoner:review-submit",
		"args":  contractEnvelopeJSON(t, runID, "rev-1"),
	}), nil); code != 0 {
		t.Fatalf("terminal default review-submit exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Agent", "tu-ag-1", map[string]interface{}{
		"subagent_type": "summoner:reviewer",
		"prompt":        contractReviewerCapsule(t, runID),
		"description":   "independent review",
	}), nil); code != 0 {
		t.Fatalf("reviewer Agent exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Reviews) != 1 || run.Reviews[0].Status != shared.ReviewRunning || run.ReviewObligation != nil {
		t.Fatalf("after the default registration + dispatch: reviews %+v obligation %+v, want one RUNNING / nil", run.Reviews, run.ReviewObligation)
	}
	if err := shared.VerifyChain(runID); err != nil {
		t.Errorf("VerifyChain: %v", err)
	}
}

// TestEndToEndNamespacedReviewerRoundTrip (fix-round C1): the production
// dispatch form end-to-end over the REAL binaries — the reviewer is a PLUGIN
// agent invoked as `summoner:reviewer`; the gate admits the namespaced type
// (canonical short form into effects), and the namespaced SubagentStop
// backfills the verdict.
func TestEndToEndNamespacedReviewerRoundTrip(t *testing.T) {
	useContractEnv(t)
	runID := contractRunAtReviewPending(t, []string{"crit-ctx"}, []string{"crit-diag"})

	if code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Skill", "tu-rs-1", map[string]interface{}{
		"skill": "summoner:review-submit",
		"args":  contractEnvelopeJSON(t, runID, "rev-1"),
	}), nil); code != 0 {
		t.Fatalf("review-submit exit = %d, want 0 (stderr: %q)", code, stderr)
	}

	// The production dispatch: subagent_type "summoner:reviewer".
	if code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Agent", "tu-ag-1", map[string]interface{}{
		"subagent_type": "summoner:reviewer",
		"prompt":        contractReviewerCapsule(t, runID),
		"description":   "independent review",
	}), nil); code != 0 {
		t.Fatalf("summoner:reviewer Agent exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	run, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Reviews[0].Status != shared.ReviewRunning {
		t.Fatalf("review = %s, want RUNNING after the namespaced reviewer delegation", run.Reviews[0].Status)
	}
	started := eventsOfType(allTraceEvents(t), shared.EventDelegationStarted)
	if len(started) != 1 || started[0].Payload["subagent_type"] != "reviewer" {
		t.Fatalf("delegation.started = %+v, want the CANONICAL short form in effects", started)
	}

	// The namespaced SubagentStop is processed (not silently dropped): the
	// verdict backfills through the reducer.
	result := contractPassResult(t, runID)
	resJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runBinary(t, "subagentstop", mustJSON(t, map[string]interface{}{
		"hook_event_name":       "SubagentStop",
		"session_id":            contractSession,
		"stop_hook_active":      false,
		"agent_id":              "ag-rev-1",
		"agent_type":            "summoner:reviewer",
		"agent_transcript_path": contractReviewerTranscript(t, runID, string(resJSON)),
	}), nil); code != 0 {
		t.Fatalf("namespaced subagentstop exit = %d, want 0 (stderr: %q)", code, stderr)
	}

	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	rv := run.Reviews[0]
	if rv.Status != shared.ReviewReviewed || rv.Verdict != shared.VerdictPass {
		t.Fatalf("review = %+v, want REVIEWED PASS backfilled through the namespaced stop", rv)
	}
	cp := run.CurrentCheckpoint()
	if cp == nil || cp.Kind != shared.CheckpointReviewResult || run.Status != shared.StatusWaitUser {
		t.Fatalf("after the verdict: status %s checkpoint %+v, want WAIT_USER at a REVIEW_RESULT checkpoint", run.Status, cp)
	}
	if verdicts := eventsOfType(allTraceEvents(t), shared.EventReviewVerdict); len(verdicts) != 1 || verdicts[0].Payload["review_id"] != "rev-1" {
		t.Errorf("review.verdict events = %+v, want exactly one for rev-1", verdicts)
	} else if ids, ok := verdicts[0].Payload["evidence_tool_call_ids"].([]interface{}); !ok || len(ids) != 1 || ids[0] != "toolu-rev-1" {
		t.Errorf("namespaced review.verdict evidence_tool_call_ids = %#v, want [toolu-rev-1] mirrored end-to-end", verdicts[0].Payload["evidence_tool_call_ids"])
	} else if verdicts[0].Payload["artifact_digest"] != rv.ArtifactDigest {
		t.Errorf("namespaced review.verdict artifact_digest = %#v, want the minted %s", verdicts[0].Payload["artifact_digest"], rv.ArtifactDigest)
	} else if env, ok := verdicts[0].Payload["envelope_digest"].(string); !ok || env == "" {
		t.Errorf("namespaced review.verdict envelope_digest = %#v, want a non-empty digest", verdicts[0].Payload["envelope_digest"])
	}
	if err := shared.VerifyChain(runID); err != nil {
		t.Errorf("VerifyChain: %v", err)
	}
}

// TestEndToEndDropRideAlongOverBinaries: a drop:/keep: question rides the
// SAME AskUserQuestion call as the nonce decision question — the gate admits
// the call untouched (its predicate keys on the call carrying NO nonce
// question), PostToolUse records the drop observation and the decision, and
// the criterion leaves the expected envelope set.
func TestEndToEndDropRideAlongOverBinaries(t *testing.T) {
	useContractEnv(t)

	if code, _, stderr := runBinary(t, "userpromptsubmit", upsPayload(t, "/summoner:fix another bug"), nil); code != 0 {
		t.Fatalf("run start exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	files := runStateFiles(t)
	if len(files) != 1 {
		t.Fatalf("run state files = %d, want 1", len(files))
	}
	runID := strings.TrimSuffix(filepath.Base(files[0]), ".json")

	submitPhaseViaGate(t, "tu-ps-1", "context", []string{"crit-a"})
	run, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	renderAndDecide(t, runID, "tu-ask-1", run.CurrentCheckpoint(), 0, func(labels []string) string {
		return firstLabelWithKeyword(labels, "continue")
	})
	submitPhaseViaGate(t, "tu-ps-2", "diagnosis", []string{"crit-b"})
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	cp := run.CurrentCheckpoint()

	// The drop ride-along: one nonce decision question + one nonce-free
	// drop:/keep: question, ONE call.
	if code, _, stderr := runBinary(t, "stop", stopRenderPayload(t, renderMessageFor(run, cp)), nil); code != 0 {
		t.Fatalf("stop render exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	cp = run.CurrentCheckpoint()
	page := cp.MenuPages[0]
	qtext := askQuestionText(cp.Nonce)
	dtext := "drop: crit-a — remove this criterion from the acceptance view?"
	dropQ := map[string]interface{}{
		"question": dtext, "header": "Criterion crit-a",
		"options": []interface{}{
			map[string]interface{}{"label": "keep: crit-a", "description": "default"},
			map[string]interface{}{"label": "drop: crit-a", "description": "explicit drop"},
		},
		"multiSelect": false,
	}
	// The decision question must offer a persisted page EXACTLY (all labels);
	// the drop question rides along untouched.
	pageOpts := make([]interface{}, 0, len(page.Labels))
	for _, l := range page.Labels {
		pageOpts = append(pageOpts, map[string]interface{}{"label": l, "description": "decision option"})
	}
	if code, _, stderr := runBinary(t, "pretooluse-gate", mustJSON(t, map[string]interface{}{
		"session_id":  contractSession,
		"tool_name":   "AskUserQuestion",
		"tool_use_id": "tu-ask-2",
		"tool_input": map[string]interface{}{"questions": []interface{}{
			map[string]interface{}{
				"question": qtext, "header": "Checkpoint",
				"options":     pageOpts,
				"multiSelect": false,
			},
			dropQ,
		}},
	}), nil); code != 0 {
		t.Fatalf("gate ask with drop ride-along exit = %d, want 0 — nonce-free drop questions ride along untouched (stderr: %q)", code, stderr)
	}
	if code, _, stderr := runBinary(t, "posttooluse", mustJSON(t, map[string]interface{}{
		"hook_event_name": "PostToolUse",
		"session_id":      contractSession,
		"tool_name":       "AskUserQuestion",
		"tool_use_id":     "tu-ask-2",
		"tool_input": map[string]interface{}{"questions": []interface{}{
			map[string]interface{}{"question": qtext, "header": "Checkpoint",
				"options": pageOpts, "multiSelect": false},
			dropQ,
		}},
		"tool_response": map[string]interface{}{
			"answers": map[string]interface{}{
				qtext: page.Labels[0], // continue
				dtext: "drop: crit-a",
			},
		},
	}), nil); code != 0 {
		t.Fatalf("posttooluse drop ride-along exit = %d, want 0 (stderr: %q)", code, stderr)
	}

	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.CriteriaDrops) != 1 || run.CriteriaDrops[0].ID != "crit-a" {
		t.Fatalf("drop ledger = %+v, want one crit-a entry", run.CriteriaDrops)
	}
	evs := allTraceEvents(t)
	dropped := eventsOfType(evs, shared.EventCriterionDropped)
	if len(dropped) != 1 || dropped[0].Payload["criterion_id"] != "crit-a" || dropped[0].ToolUseID != "tu-ask-2" {
		t.Fatalf("criterion.dropped events = %+v, want one crit-a bound to tu-ask-2", dropped)
	}
	if run.Status != shared.StatusActive || !run.Checkpoints[1].Decided {
		t.Fatalf("the decision must record alongside the drop (status %s)", run.Status)
	}
	if err := shared.VerifyChain(runID); err != nil {
		t.Errorf("VerifyChain: %v", err)
	}
}

// --- T18: budget lifecycle + post-game closure over the REAL binaries ---------------

// soleRunID resolves the single run state file of the contract env (fatal on
// any other count — a second live run would fail every hook on ambiguity).
func soleRunID(t *testing.T) string {
	t.Helper()
	files := runStateFiles(t)
	if len(files) != 1 {
		t.Fatalf("run state files = %d, want 1", len(files))
	}
	return strings.TrimSuffix(filepath.Base(files[0]), ".json")
}

// writeContractManifest writes a minimal valid v2 manifest (with the given
// suggested_budgets YAML body) into the temp project dir the contract env
// points CLAUDE_PROJECT_DIR at — the runtime manifest loader reads it when
// the run's first checkpoint opens its budget ask. The phases map is
// REQUIRED (the loader is validate-manifest's twin: an empty/absent phases
// map fails closed since the T11 alignment) — smallest valid form.
func writeContractManifest(t *testing.T, budgetsYAML string) {
	t.Helper()
	dir := os.Getenv("CLAUDE_PROJECT_DIR")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "summoner.yaml"), []byte("version: \"2\"\nproject:\n  name: contract-probe\nphases:\n  diagnosis:\n    skill: none\n"+budgetsYAML), 0o644); err != nil {
		t.Fatal(err)
	}
}

// postGameQuestionnairePayload builds the post-game questionnaire call: one
// question per pinned marker, each carrying "[pg:qN]" AND the run id in its
// text; answer names the markers actually answered (the rest stay unanswered).
func postGameQuestionnairePayload(t *testing.T, runID, toolUseID string, answer map[string]bool) string {
	t.Helper()
	var qs []interface{}
	answers := map[string]interface{}{}
	for _, m := range shared.PostGameMarkers() {
		text := fmt.Sprintf("Post-game %s (run %s): how did this go? [pg:%s]", m, runID, m)
		qs = append(qs, map[string]interface{}{
			"question": text, "header": "Post-game",
			"options": []interface{}{
				map[string]interface{}{"label": "well: 顺利", "description": "went well"},
				map[string]interface{}{"label": "roughly: 一般", "description": "mixed"},
			},
			"multiSelect": false,
		})
		if answer[m] {
			answers[text] = "well: 顺利"
		}
	}
	return mustJSON(t, map[string]interface{}{
		"hook_event_name": "PostToolUse",
		"session_id":      contractSession,
		"tool_name":       "AskUserQuestion",
		"tool_use_id":     toolUseID,
		"tool_input":      map[string]interface{}{"questions": qs},
		"tool_response":   map[string]interface{}{"answers": answers},
	})
}

// TestEndToEndBudgetCycleExhaustExtend pins the REAL exhaustion cycle (T18):
// manifest-suggested budget → confirm ride at the first checkpoint (post-hoc
// twin: a budget-less CONTINUE there records NOTHING) → phase work until the
// counter hits the limit → everything denied except phase-submit and the
// EXACT summoner-status invocation → the exhaustion checkpoint carries the
// extension offer → extend+decision in ONE call → work permitted again.
func TestEndToEndBudgetCycleExhaustExtend(t *testing.T) {
	useContractEnv(t)
	writeContractManifest(t, "suggested_budgets:\n  phase_tool_calls: 2\n")

	if code, _, stderr := runBinary(t, "userpromptsubmit", upsPayload(t, "/summoner:fix budget cycle"), nil); code != 0 {
		t.Fatalf("run start exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	runID := soleRunID(t)
	submitPhaseViaGate(t, "tu-ps-1", "context", nil)
	run, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	cp := run.CurrentCheckpoint()
	if cp == nil {
		t.Fatalf("the first checkpoint must exist: %+v", cp)
	}
	if cp.BudgetAsk != nil {
		t.Fatalf("the first checkpoint must carry NO budget ask (0.6.1): %+v", cp.BudgetAsk)
	}
	// 0.6.1: budgets are manifest-authored — the run-start ask is gone and
	// the suggested_budgets projection applied at run start.
	if !run.BudgetConfirmed || run.BudgetSettledBy != "run-start" || run.Limits.PhaseToolCalls != 2 {
		t.Fatalf("run-start budget = confirmed %v settledBy %q limits %+v, want settled run-start with phase_tool_calls=2", run.BudgetConfirmed, run.BudgetSettledBy, run.Limits)
	}

	// A budget-less CONTINUE records plainly (no settlement ceremony exists).
	if code, _, stderr := runBinary(t, "stop", stopRenderPayload(t, renderMessageFor(run, cp)), nil); code != 0 {
		t.Fatalf("stop render exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	run, _ = shared.LoadRun(runID)
	cp = run.CurrentCheckpoint()
	renderAndDecideSettle(t, runID, "tu-ask-1", cp, 0, func(labels []string) string {
		return firstLabelWithKeyword(labels, "continue")
	}, "", "")
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if !run.Checkpoints[0].Decided || run.Status != shared.StatusActive {
		t.Fatalf("budget-less CONTINUE must record (decided=%v status %s)", run.Checkpoints[0].Decided, run.Status)
	}
	evs := allTraceEvents(t)
	if n := len(eventsOfType(evs, shared.EventBudgetConfirmed)); n != 0 {
		t.Fatalf("budget.confirmed events = %d, want 0 (no settlement ceremony exists)", n)
	}
	if n := len(eventsOfType(evs, shared.EventCheckpointDecisionRejected)); n != 0 {
		t.Fatalf("decision_rejected events = %d, want 0", n)
	}

	// Phase work until exhaustion: the skill call opens diagnosis (1/2), one
	// Bash spends the rest (2/2); the next Bash is denied 余量为零.
	if code, _, stderr := runBinary(t, "pretooluse-gate",
		gatePayload(t, "Skill", "tu-skill-1", map[string]interface{}{"skill": "example-skill"}), nil); code != 0 {
		t.Fatalf("skill open exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if code, _, stderr := runBinary(t, "pretooluse-gate",
		gatePayload(t, "Bash", "tu-bash-1", map[string]interface{}{"command": "ls"}), nil); code != 0 {
		t.Fatalf("bash within budget exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if code, _, stderr := runBinary(t, "pretooluse-gate",
		gatePayload(t, "Bash", "tu-bash-2", map[string]interface{}{"command": "ls"}), nil); code != 2 || !strings.Contains(stderr, "余量为零") {
		t.Fatalf("exhausted bash: (code, stderr) = (%d, %q), want exit 2 + 余量为零", code, stderr)
	}
	// The EXACT read-only status invocation stays runnable at exhaustion —
	// and only the exact shape is exempt.
	if code, _, stderr := runBinary(t, "pretooluse-gate",
		gatePayload(t, "Bash", "tu-bash-3", map[string]interface{}{"command": "\"${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status\" <<< '{}'"}), nil); code != 0 {
		t.Fatalf("status invocation at exhaustion exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if code, _, stderr := runBinary(t, "pretooluse-gate",
		gatePayload(t, "Bash", "tu-bash-4", map[string]interface{}{"command": "\"${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status\" --help"}), nil); code != 2 {
		t.Fatalf("non-canonical status invocation must stay budget-gated: (code, stderr) = (%d, %q), want exit 2", code, stderr)
	}

	// phase-submit is exempt: the exhausted phase closes into its checkpoint,
	// which carries the persisted extension offer (delta = the set limit).
	submitPhaseViaGate(t, "tu-ps-2", "diagnosis", nil)
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	cp = run.CurrentCheckpoint()
	if cp == nil || cp.ExtendAsk == nil || cp.ExtendAsk.Delta.PhaseToolCalls != 2 {
		t.Fatalf("the exhaustion checkpoint must carry the extension offer (delta = current limit): %+v", cp)
	}

	// F2 pin: at the exhaustion checkpoint the counter reads 3 (2 work calls
	// + 1 exempt mid-phase status render) — the phase-submit contributed
	// NOTHING (it runs after SubmitPhase closed the phase, outside any visit
	// bucket until the decision authorizes the next one).
	if run.Counters.PhaseToolCalls != 3 {
		t.Fatalf("counter at the exhaustion checkpoint = %d, want 3 (the exempt submit never ticks)", run.Counters.PhaseToolCalls)
	}

	// Extend + decision in ONE call: limits raise (2+2).
	renderAndDecideSettle(t, runID, "tu-ask-2", cp, 0, func(labels []string) string {
		return firstLabelWithKeyword(labels, "continue")
	}, "", "extend")
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Limits.PhaseToolCalls != 4 || len(run.BudgetExtensions) != 1 || run.Status != shared.StatusActive {
		t.Fatalf("after extend+continue: limits %+v extensions %d status %s, want 4/1/ACTIVE", run.Limits, len(run.BudgetExtensions), run.Status)
	}
	// I1 pin (adjudication §5): the continue decision authorized the NEXT
	// visit and reset its bucket — pre-provider work now charges 0/4 into
	// the authorized upcoming bucket, and the next OpenPhase ADOPTS the
	// accrual instead of resetting it away.
	if run.Counters.PhaseToolCalls != 0 || !run.UpcomingVisit {
		t.Fatalf("counter after extend+continue = %d upcoming %v, want 0/true (I1: the decision authorizes a fresh visit bucket awaiting adoption)", run.Counters.PhaseToolCalls, run.UpcomingVisit)
	}
	if code, _, stderr := runBinary(t, "pretooluse-gate",
		gatePayload(t, "Skill", "tu-skill-2", map[string]interface{}{"skill": "example-skill"}), nil); code != 0 {
		t.Fatalf("work after extension (skill open) exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if code, _, stderr := runBinary(t, "pretooluse-gate",
		gatePayload(t, "Bash", "tu-bash-5", map[string]interface{}{"command": "ls"}), nil); code != 0 {
		t.Fatalf("work after extension (bash) exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	evs = allTraceEvents(t)
	if ext := eventsOfType(evs, shared.EventBudgetExtended); len(ext) != 1 || ext[0].ToolUseID != "tu-ask-2" {
		t.Fatalf("budget.extended events = %+v, want one bound to tu-ask-2", ext)
	}
	if err := shared.VerifyChain(runID); err != nil {
		t.Errorf("VerifyChain: %v", err)
	}
}

// TestEndToEndPostGameClosure pins the closure cycle (T18): DONE enters
// POST_GAME_PENDING (never CLOSED directly); the pinned questionnaire (4
// marker questions, one call) records through the SAME hook — afk-resolved
// answers observe nothing; the journal Write at the fixed path records its
// observation; only then CLOSED — with the terminal pair post_game.completed
// + workflow.ended exactly once, and a closed run neither observes later
// edits nor blocks a new run.
func TestEndToEndPostGameClosure(t *testing.T) {
	useContractEnv(t)
	if code, _, stderr := runBinary(t, "userpromptsubmit", upsPayload(t, "/summoner:fix closure cycle"), nil); code != 0 {
		t.Fatalf("run start exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	runID := soleRunID(t)
	submitPhaseViaGate(t, "tu-ps-1", "context", nil)
	run, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	renderAndDecide(t, runID, "tu-ask-1", run.CurrentCheckpoint(), 0, func(labels []string) string {
		return firstLabelWithKeyword(labels, "continue")
	})

	// DONE at the next checkpoint: POST_GAME_PENDING, post_game.started
	// (kind normal), NO journal yet, run NOT closed.
	submitPhaseViaGate(t, "tu-ps-2", "diagnosis", nil)
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	renderAndDecideSettle(t, runID, "tu-ask-2", run.CurrentCheckpoint(), 0, func(labels []string) string {
		return firstLabelWithKeyword(labels, "done")
	}, "", "")
	// M4 (ruling 72): DONE on this review-phase workflow parks at the
	// REVIEW_DEBT checkpoint — discharge via the REAL hooks with the explicit
	// UNREVIEWED finish, which itself starts the normal post-game.
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if debt := run.CurrentCheckpoint(); debt != nil && debt.Kind == shared.CheckpointReviewDebt {
		renderAndDecide(t, runID, "tu-debt-1", debt, 0, func(labels []string) string {
			return firstLabelWithKeyword(labels, "skip")
		})
	}
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != shared.StatusPostGamePending || run.PostGame == nil || run.PostGame.Kind != "normal" {
		t.Fatalf("after DONE: status %s post-game %+v, want POST_GAME_PENDING kind normal", run.Status, run.PostGame)
	}
	if _, err := os.Stat(shared.JournalPath(runID)); !os.IsNotExist(err) {
		t.Fatalf("no journal may exist before the post-game writes it (stat: %v)", err)
	}
	if started := eventsOfType(allTraceEvents(t), shared.EventPostGameStarted); len(started) != 1 || started[0].Payload["kind"] != "normal" {
		t.Fatalf("post_game.started events = %+v, want one kind=normal", started)
	}

	// The questionnaire asks pass the gate at POST_GAME_PENDING.
	if code, _, stderr := runBinary(t, "pretooluse-gate", mustJSON(t, map[string]interface{}{
		"session_id":  contractSession,
		"tool_name":   "AskUserQuestion",
		"tool_use_id": "tu-pg-0",
		"tool_input": map[string]interface{}{"questions": []interface{}{map[string]interface{}{
			"question": "Post-game q1 (run " + runID + "): warm-up? [pg:q1]", "header": "Post-game",
			"options": []interface{}{
				map[string]interface{}{"label": "well: 顺利", "description": "went well"},
				map[string]interface{}{"label": "roughly: 一般", "description": "mixed"},
			},
		}}},
	}), nil); code != 0 {
		t.Fatalf("gate questionnaire ask exit = %d, want 0 (stderr: %q)", code, stderr)
	}

	// An afk-resolved questionnaire observes NOTHING.
	afk := strings.Replace(postGameQuestionnairePayload(t, runID, "tu-pg-afk", map[string]bool{"q1": true, "q2": true, "q3": true, "q4": true}),
		`"answers":`, `"afkTimeoutMs": 30000, "answers":`, 1)
	if code, _, stderr := runBinary(t, "posttooluse", afk, nil); code != 0 {
		t.Fatalf("afk questionnaire exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	run, _ = shared.LoadRun(runID)
	if len(run.PostGame.Answers) != 0 {
		t.Fatalf("an afk-resolved questionnaire must observe nothing (answers: %v)", run.PostGame.Answers)
	}

	// The real answers observe the pinned markers; the run still owes the
	// journal (markers alone never close it).
	if code, _, stderr := runBinary(t, "posttooluse",
		postGameQuestionnairePayload(t, runID, "tu-pg-1", map[string]bool{"q1": true, "q2": true, "q3": true, "q4": true}), nil); code != 0 {
		t.Fatalf("questionnaire exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	run, _ = shared.LoadRun(runID)
	if run.PostGameReady() || run.Status != shared.StatusPostGamePending {
		t.Fatalf("markers alone must not close the run (ready %v status %s)", run.PostGameReady(), run.Status)
	}

	// The journal Write at the FIXED path: the platform executed the tool (the
	// test writes the file), PostToolUse observes the content digest, and
	// closure fires — CLOSED + post_game.completed + workflow.ended, once.
	journalPath := shared.JournalPath(runID)
	if err := os.MkdirAll(filepath.Dir(journalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "# Post-game journal (run " + runID + ")\n\nWhat worked: the cycle pinned by contract.\n"
	if err := os.WriteFile(journalPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runBinary(t, "posttooluse", mustJSON(t, map[string]interface{}{
		"hook_event_name": "PostToolUse",
		"session_id":      contractSession,
		"tool_name":       "Write",
		"tool_use_id":     "tu-jr-1",
		"tool_input":      map[string]interface{}{"file_path": filepath.Join(".summoner", "journal", runID+".md"), "content": content},
		"tool_response":   map[string]interface{}{},
	}), nil); code != 0 {
		t.Fatalf("journal write exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != shared.StatusClosed || run.PostGame.CompletedAt == "" {
		t.Fatalf("after journal: status %s completed %q, want CLOSED + stamp", run.Status, run.PostGame.CompletedAt)
	}
	evs := allTraceEvents(t)
	if n := len(eventsOfType(evs, shared.EventPostGameCompleted)); n != 1 {
		t.Fatalf("post_game.completed events = %d, want 1", n)
	}
	if n := len(eventsOfType(evs, shared.EventWorkflowEnded)); n != 1 {
		t.Fatalf("workflow.ended events = %d, want 1", n)
	}

	// A later ordinary edit of the closed run's journal observes nothing —
	// CLOSED never reopens and never appends a second workflow.ended.
	if err := os.WriteFile(journalPath, []byte(content+"\nan afterthought\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runBinary(t, "posttooluse", mustJSON(t, map[string]interface{}{
		"hook_event_name": "PostToolUse",
		"session_id":      contractSession,
		"tool_name":       "Edit",
		"tool_use_id":     "tu-jr-2",
		"tool_input":      map[string]interface{}{"file_path": journalPath, "old_string": content, "new_string": content + "\nx"},
		"tool_response":   map[string]interface{}{},
	}), nil); code != 0 {
		t.Fatalf("post-closed edit exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if n := len(eventsOfType(allTraceEvents(t), shared.EventWorkflowEnded)); n != 1 {
		t.Fatalf("workflow.ended events after closed-run edit = %d, want still 1", n)
	}
	if run2, err := shared.LoadRun(runID); err != nil || run2.Status != shared.StatusClosed {
		t.Fatalf("closed run reopened or unreadable: %v %+v", err, run2)
	}

	// The closed run no longer blocks a new one.
	if code, _, stderr := runBinary(t, "userpromptsubmit", upsPayload(t, "/summoner:fix a fresh bug"), nil); code != 0 {
		t.Fatalf("new run after closure exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if files := runStateFiles(t); len(files) != 2 {
		t.Fatalf("run state files = %d, want 2 (closed + fresh)", len(files))
	}
	if err := shared.VerifyChain(runID); err != nil {
		t.Errorf("VerifyChain: %v", err)
	}
}

// TestEndToEndEmergencyStopMinimalJournal pins the STOP arm (T18, spec §6):
// an emergency STOP writes the minimal exit journal hook-side IMMEDIATELY,
// records its observation, and stays POST_GAME_PENDING (kind
// emergency_minimal) — the questionnaire is 补答-debt; closure comes later in
// the owning session through the same observations.
func TestEndToEndEmergencyStopMinimalJournal(t *testing.T) {
	useContractEnv(t)
	if code, _, stderr := runBinary(t, "userpromptsubmit", upsPayload(t, "/summoner:fix emergency stop"), nil); code != 0 {
		t.Fatalf("run start exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	runID := soleRunID(t)
	submitPhaseViaGate(t, "tu-ps-1", "context", nil)
	run, err := shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	renderAndDecideSettle(t, runID, "tu-ask-1", run.CurrentCheckpoint(), 0, func(labels []string) string {
		return firstLabelWithKeyword(labels, "stop")
	}, "unlimited", "")

	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != shared.StatusPostGamePending || run.PostGame == nil || run.PostGame.Kind != "emergency_minimal" {
		t.Fatalf("after STOP: status %s post-game %+v, want POST_GAME_PENDING kind emergency_minimal", run.Status, run.PostGame)
	}
	b, err := os.ReadFile(shared.JournalPath(runID))
	if err != nil {
		t.Fatalf("the minimal exit journal must exist immediately after STOP: %v", err)
	}
	if !strings.Contains(string(b), "kind: emergency_minimal") || !strings.Contains(string(b), runID) {
		t.Fatalf("minimal journal = %q, want emergency_minimal frontmatter naming the run", string(b))
	}
	if run.PostGame.Journal == nil {
		t.Fatal("the STOP decision must record the journal observation hook-side")
	}
	started := eventsOfType(allTraceEvents(t), shared.EventPostGameStarted)
	if len(started) != 1 || started[0].Payload["kind"] != "emergency_minimal" {
		t.Fatalf("post_game.started events = %+v, want one kind=emergency_minimal", started)
	}
	if n := len(eventsOfType(allTraceEvents(t), shared.EventWorkflowEnded)); n != 0 {
		t.Fatalf("workflow.ended events = %d, want 0 (STOP is not closure)", n)
	}

	// The 补答: questionnaire markers (the stop call answered none), then the
	// full journal rewrite — the LATEST observed version governs closure.
	if code, _, stderr := runBinary(t, "posttooluse",
		postGameQuestionnairePayload(t, runID, "tu-pg-1", map[string]bool{"q1": true, "q2": true, "q3": true, "q4": true}), nil); code != 0 {
		t.Fatalf("questionnaire exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	full := "# Full post-game journal (run " + runID + ")\n\nEmergency stop retrospective.\n"
	if err := os.WriteFile(shared.JournalPath(runID), []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runBinary(t, "posttooluse", mustJSON(t, map[string]interface{}{
		"hook_event_name": "PostToolUse",
		"session_id":      contractSession,
		"tool_name":       "Write",
		"tool_use_id":     "tu-jr-9",
		"tool_input":      map[string]interface{}{"file_path": shared.JournalPath(runID), "content": full},
		"tool_response":   map[string]interface{}{},
	}), nil); code != 0 {
		t.Fatalf("journal rewrite exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	run, err = shared.LoadRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != shared.StatusClosed {
		t.Fatalf("status = %s, want CLOSED after 补答 (post-game %+v)", run.Status, run.PostGame)
	}
	if n := len(eventsOfType(allTraceEvents(t), shared.EventWorkflowEnded)); n != 1 {
		t.Fatalf("workflow.ended events = %d, want exactly 1", n)
	}
	if err := shared.VerifyChain(runID); err != nil {
		t.Errorf("VerifyChain: %v", err)
	}
}
