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
	for _, name := range []string{"session-start", "pretooluse-gate", "posttooluse", "subagentstop", "stop", "userpromptsubmit"} {
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
// env overrides the subprocesses get).
func seedLiveRun(t *testing.T, runID, workflow string) *shared.RunState {
	t.Helper()
	r, err := shared.StartRun(runID, contractSession, workflow, shared.BudgetLimits{})
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
// checkpoint header (id + nonce — what display verification binds) plus the
// FULL menu and paging note the status render carries.
func renderMessageFor(cp *shared.CheckpointState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "⚡ SUMMONER CHECKPOINT %s — nonce %s — %s\n", cp.ID, cp.Nonce, cp.Kind)
	b.WriteString("phase: " + cp.PhaseID + "\nmenu:\n")
	for _, opt := range shared.DecisionMenuLabels(cp.Kind) {
		b.WriteString("  " + opt + "\n")
	}
	if len(cp.MenuPages) > 1 {
		b.WriteString("paged ask (one page per decision question, ≤4 options):\n")
		for i, pg := range cp.MenuPages {
			fmt.Fprintf(&b, "  page %d/%d: %s — %s — %s\n", i+1, len(cp.MenuPages),
				strings.Join(pg.Labels, " · "), pg.NavNext, pg.NavPrev)
		}
	}
	return b.String()
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
	code, _, stderr = runBinary(t, "stop", stopRenderPayload(t, renderMessageFor(cp)), nil)
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

	// Next-turn ask with the persisted page's exact labels: allowed.
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
	code, _, stderr = runBinary(t, "posttooluse", mustJSON(t, map[string]interface{}{
		"hook_event_name": "PostToolUse",
		"session_id":      contractSession,
		"tool_name":       "AskUserQuestion",
		"tool_use_id":     "tu-ask-1",
		"tool_input": map[string]interface{}{"questions": []interface{}{map[string]interface{}{
			"question": qtext, "header": "Checkpoint", "options": opts, "multiSelect": false,
		}}},
		"tool_response": map[string]interface{}{
			"questions": []interface{}{map[string]interface{}{"question": qtext}},
			"answers":   map[string]interface{}{qtext: answer},
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
