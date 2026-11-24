package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/johnson-xue/summoner/hooks/hooktest"
)

func TestMain(m *testing.M) { os.Exit(hooktest.Run(m)) }

// dormantAgentStop is a payload enforcement reads: one delegation ending.
const dormantAgentStop = `{"hook_event_name":"SubagentStop","session_id":"sess-dormant","stop_hook_active":false,"agent_id":"agent-1"}`

// TestDormantDefault: with no opt-in the SubagentStop hook settles no
// delegation — it does not even read the input.
func TestDormantDefault(t *testing.T) {
	hooktest.Dormant(t)
	stateDir, traceDir := hooktest.IsolateState(t)
	payloads := map[string]string{
		"enforcement reads it: a delegation ending":        dormantAgentStop,
		"enforcement fails closed on it: unreadable input": "not a hook payload",
		"nothing on stdin": ``,
	}
	var errW bytes.Buffer
	for name, payload := range payloads {
		errW.Reset()
		if code := runSubagentStop(strings.NewReader(payload), &errW); code != 0 {
			t.Errorf("%s: dormant hook exited %d, want 0 (stderr: %q)", name, code, errW.String())
		}
		if errW.String() != "" {
			t.Errorf("%s: dormant hook wrote stderr %q, want none", name, errW.String())
		}
	}
	for name, dir := range map[string]string{"state": stateDir, "trace": traceDir} {
		if n := hooktest.CountFiles(dir); n != 0 {
			t.Errorf("dormant hook wrote %d file(s) under the %s dir, want none", n, name)
		}
	}
}

// TestOptInArmsTheSubagentStopHook: the input the default waves through is
// read — and rejected — once the opt-in is set.
func TestOptInArmsTheSubagentStopHook(t *testing.T) {
	hooktest.OptIn(t)
	hooktest.IsolateState(t)
	var errW bytes.Buffer
	if code := runSubagentStop(strings.NewReader("not a hook payload"), &errW); code != 2 {
		t.Fatalf("opted-in hook: exit %d for unreadable input, want 2 (stderr %q)", code, errW.String())
	}
}
