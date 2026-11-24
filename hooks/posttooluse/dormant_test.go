package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/johnson-xue/summoner/hooks/hooktest"
)

func TestMain(m *testing.M) { os.Exit(hooktest.Run(m)) }

// dormantToolResult is a payload enforcement reads: the result of one tool
// call.
const dormantToolResult = `{"hook_event_name":"PostToolUse","session_id":"sess-dormant","tool_name":"Read","tool_use_id":"tu-1","tool_input":{"file_path":"/etc/hosts"},"tool_response":"readable"}`

// TestDormantDefault: with no opt-in the PostToolUse hook records no
// outcome and answers no decision — it does not even read the input.
func TestDormantDefault(t *testing.T) {
	hooktest.Dormant(t)
	stateDir, traceDir := hooktest.IsolateState(t)
	payloads := map[string]string{
		"enforcement reads it: a tool result":              dormantToolResult,
		"enforcement fails closed on it: unreadable input": "not a hook payload",
		"nothing on stdin":                                 ``,
	}
	var out, errW bytes.Buffer
	for name, payload := range payloads {
		out.Reset()
		errW.Reset()
		if code := runHook(strings.NewReader(payload), &out, &errW); code != 0 {
			t.Errorf("%s: dormant hook exited %d, want 0", name, code)
		}
		if out.String() != "" || errW.String() != "" {
			t.Errorf("%s: dormant hook wrote stdout %q / stderr %q, want neither", name, out.String(), errW.String())
		}
	}
	for name, dir := range map[string]string{"state": stateDir, "trace": traceDir} {
		if n := hooktest.CountFiles(dir); n != 0 {
			t.Errorf("dormant hook wrote %d file(s) under the %s dir, want none", n, name)
		}
	}
}

// TestOptInArmsThePostToolUseHook: the input the default waves through is
// read — and refused — once the opt-in is set.
func TestOptInArmsThePostToolUseHook(t *testing.T) {
	hooktest.OptIn(t)
	hooktest.IsolateState(t)
	var out, errW bytes.Buffer
	payload := `{"hook_event_name":"Nonsense","session_id":"sess-opt-in","tool_name":"Read","tool_use_id":"tu-1"}`
	if code := runHook(strings.NewReader(payload), &out, &errW); code != 2 {
		t.Fatalf("opted-in hook: exit %d for an unknown event, want 2 (stderr %q)", code, errW.String())
	}
}
