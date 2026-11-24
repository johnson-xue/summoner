package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/johnson-xue/summoner/hooks/hooktest"
)

func TestMain(m *testing.M) { os.Exit(hooktest.Run(m)) }

// dormantStopEvent is a payload enforcement reads: the Stop event of a
// session.
const dormantStopEvent = `{"hook_event_name":"Stop","session_id":"sess-dormant","stop_hook_active":false}`

// TestDormantDefault: with no opt-in the Stop hook hands back no question
// and blocks no stop — it does not even read the input.
func TestDormantDefault(t *testing.T) {
	hooktest.Dormant(t)
	hooktest.IsolateState(t)
	payloads := map[string]string{
		"enforcement reads it: a Stop event":               dormantStopEvent,
		"enforcement fails closed on it: unreadable input": `not a hook payload`,
		"nothing on stdin":                                 ``,
	}
	var out, errW bytes.Buffer
	for name, payload := range payloads {
		out.Reset()
		errW.Reset()
		if code := runStop(strings.NewReader(payload), &out, &errW); code != 0 {
			t.Errorf("%s: dormant hook exited %d, want 0", name, code)
		}
		if out.String() != "" || errW.String() != "" {
			t.Errorf("%s: dormant hook wrote stdout %q / stderr %q, want neither", name, out.String(), errW.String())
		}
	}
}

// TestOptInArmsTheStopHook: the input the default waves through is read —
// and rejected — once the opt-in is set.
func TestOptInArmsTheStopHook(t *testing.T) {
	hooktest.OptIn(t)
	hooktest.IsolateState(t)
	var out, errW bytes.Buffer
	if code := runStop(strings.NewReader("not a hook payload"), &out, &errW); code != 2 {
		t.Fatalf("opted-in hook: exit %d for unreadable input, want 2 (stderr %q)", code, errW.String())
	}
}
