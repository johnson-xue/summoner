package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnson-xue/summoner/hooks/hooktest"
)

func TestMain(m *testing.M) { os.Exit(hooktest.Run(m)) }

// dormantSessionStart is a payload enforcement reads: the start of a session.
const dormantSessionStart = `{"hook_event_name":"SessionStart","session_id":"sess-%s","cwd":"/tmp"}`

// TestDormantDefault: with no opt-in SessionStart injects nothing, exports
// no session id and runs no crash scan — the "not initialized" notice and
// the OUTCOME_UNKNOWN marking are gone with it.
func TestDormantDefault(t *testing.T) {
	hooktest.Dormant(t)
	stateDir, traceDir := hooktest.IsolateState(t)
	t.Cleanup(hooktest.Put("CLAUDE_PROJECT_DIR", t.TempDir()))
	envFile := filepath.Join(t.TempDir(), "claude-env")
	t.Cleanup(hooktest.Put("CLAUDE_ENV_FILE", envFile))

	var out, errW bytes.Buffer
	payload := strings.Replace(dormantSessionStart, "%s", "dormant", 1)
	if code := runSessionStart(strings.NewReader(payload), &out, &errW); code != 0 {
		t.Fatalf("dormant hook exited %d, want 0", code)
	}
	if out.String() != "" || errW.String() != "" {
		t.Errorf("dormant hook wrote stdout %q / stderr %q, want neither", out.String(), errW.String())
	}
	if _, err := os.Stat(envFile); !os.IsNotExist(err) {
		t.Errorf("dormant hook touched the session environment file %s (stat err: %v)", envFile, err)
	}
	for name, dir := range map[string]string{"state": stateDir, "trace": traceDir} {
		if n := hooktest.CountFiles(dir); n != 0 {
			t.Errorf("dormant hook wrote %d file(s) under the %s dir, want none", n, name)
		}
	}
}

// TestOptInArmsTheSessionStart: under the opt-in the same event reaches the
// session — here seen by the session id it hands to the status viewer.
func TestOptInArmsTheSessionStart(t *testing.T) {
	hooktest.OptIn(t)
	hooktest.IsolateState(t)
	t.Cleanup(hooktest.Put("CLAUDE_PROJECT_DIR", t.TempDir()))
	envFile := filepath.Join(t.TempDir(), "claude-env")
	t.Cleanup(hooktest.Put("CLAUDE_ENV_FILE", envFile))

	var out, errW bytes.Buffer
	payload := strings.Replace(dormantSessionStart, "%s", "opt-in", 1)
	if code := runSessionStart(strings.NewReader(payload), &out, &errW); code != 0 {
		t.Fatalf("opted-in hook: exit %d, want 0 (stderr %q)", code, errW.String())
	}
	got, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("opted-in hook did not write the session environment file: %v", err)
	}
	if want := "export " + sessionIDEnvVar + "='sess-opt-in'"; !strings.Contains(string(got), want) {
		t.Fatalf("session environment file = %q, want it to carry %q", got, want)
	}
}
