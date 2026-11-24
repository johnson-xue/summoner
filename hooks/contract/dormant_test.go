package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnson-xue/summoner/hooks/hooktest"
	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// dormantPayloads names, for every wired hook, a payload enforcement ACTS on
// — so the exit 0 asserted below is a wave-through of real work, never of
// garbage.
var dormantPayloads = map[string]string{
	// denied: a protocol skill driven with no live run
	"pretooluse-gate": `{"session_id":"sess-dormant","tool_name":"Skill","tool_use_id":"tu-1","tool_input":{"skill":"summoner:phase-submit"}}`,
	// starts a run
	"userpromptsubmit": `{"hook_event_name":"UserPromptSubmit","session_id":"sess-dormant","prompt":"/summoner:fix the loader nil deref"}`,
	// injects context, exports the session id, scans for dangling intents
	"session-start": `{"hook_event_name":"SessionStart","session_id":"sess-dormant","cwd":"/tmp"}`,
	"stop":          `{"hook_event_name":"Stop","session_id":"sess-dormant","stop_hook_active":false}`,
	"posttooluse":   `{"hook_event_name":"PostToolUse","session_id":"sess-dormant","tool_name":"Read","tool_use_id":"tu-1","tool_input":{"file_path":"/etc/hosts"},"tool_response":"readable"}`,
	"subagentstop":  `{"hook_event_name":"SubagentStop","session_id":"sess-dormant","stop_hook_active":false,"agent_id":"agent-1"}`,
}

// TestDormantByDefault pins the production default at the binary seam — the
// artifacts users actually run. With no opt-in anywhere in the environment
// every wired hook exits 0 and writes nothing: not a byte of output, not a
// file of state or trace, not the session environment file. Incident
// 2026-11-24: an always-on gate denied a session's everyday tools for as
// long as one of its runs stayed non-CLOSED.
func TestDormantByDefault(t *testing.T) {
	useContractEnv(t)
	envFile := filepath.Join(t.TempDir(), "claude-env")
	t.Cleanup(hooktest.Put("CLAUDE_ENV_FILE", envFile))
	for hook, acts := range dormantPayloads {
		for _, payload := range []string{acts, "not a hook payload", ""} {
			code, out, errOut := runBinaryEnv(t, hook, payload, dormantSubprocessEnv(t))
			if code != 0 || out != "" || errOut != "" {
				t.Errorf("%s, payload %q: exit %d, stdout %q, stderr %q — want 0 and no output",
					hook, payload, code, out, errOut)
			}
		}
	}
	for name, key := range map[string]string{"state": "SUMMONER_STATE_DIR", "trace": "SUMMONER_TRACE_DIR"} {
		if n := hooktest.CountFiles(os.Getenv(key)); n != 0 {
			t.Errorf("dormant hooks left %d file(s) in the %s dir, want none", n, name)
		}
	}
	if _, err := os.Stat(envFile); !os.IsNotExist(err) {
		t.Errorf("dormant session-start touched the session environment file (stat err: %v)", err)
	}
}

// TestOptInArmsTheGate: the same bytes the default waves through are denied
// once the opt-in is in the environment.
func TestOptInArmsTheGate(t *testing.T) {
	useContractEnv(t)
	payload := dormantPayloads["pretooluse-gate"]
	if code, _, errOut := runBinaryEnv(t, "pretooluse-gate", payload, dormantSubprocessEnv(t)); code != 0 {
		t.Fatalf("default: gate exited %d, want 0 (stderr %q)", code, errOut)
	}
	code, _, errOut := runBinary(t, "pretooluse-gate", payload, nil)
	if code != 2 || !strings.Contains(errOut, "requires an active run") {
		t.Fatalf("opt-in: gate exited %d, stderr %q — want 2 denying the run-less protocol skill", code, errOut)
	}
}

// TestOptInArmsTheRunStart: a typed Summoner verb records a run only under
// the opt-in — the default starts nothing, so nothing can ever be gated.
func TestOptInArmsTheRunStart(t *testing.T) {
	useContractEnv(t)
	payload := dormantPayloads["userpromptsubmit"]
	if code, _, errOut := runBinaryEnv(t, "userpromptsubmit", payload, dormantSubprocessEnv(t)); code != 0 {
		t.Fatalf("default: userpromptsubmit exited %d, want 0 (stderr %q)", code, errOut)
	}
	if n := hooktest.CountFiles(os.Getenv("SUMMONER_STATE_DIR")); n != 0 {
		t.Fatalf("default: the prompt recorded %d file(s) of run state, want none", n)
	}
	if code, _, errOut := runBinary(t, "userpromptsubmit", payload, nil); code != 0 {
		t.Fatalf("opt-in: userpromptsubmit exited %d, want 0 (stderr %q)", code, errOut)
	}
	if n := hooktest.CountFiles(os.Getenv("SUMMONER_STATE_DIR")); n == 0 {
		t.Fatal("opt-in: the prompt recorded no run")
	}
}

// TestHatchOutranksTheOptIn: with both switches set the hatch wins — the
// gate enforces nothing (existing hatch contracts cover what it records).
func TestHatchOutranksTheOptIn(t *testing.T) {
	useContractEnv(t)
	env := append(dormantSubprocessEnv(t),
		shared.EnvHooksEnforce+"=1",
		shared.EnvHooksDisable+"=1",
	)
	code, _, errOut := runBinaryEnv(t, "pretooluse-gate", dormantPayloads["pretooluse-gate"], env)
	if code != 0 {
		t.Fatalf("gate under hatch + opt-in exited %d, want 0 (stderr %q)", code, errOut)
	}
}
