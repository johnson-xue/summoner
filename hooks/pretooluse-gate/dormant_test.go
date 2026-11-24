package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/johnson-xue/summoner/hooks/hooktest"
)

func TestMain(m *testing.M) { os.Exit(hooktest.Run(m)) }

// A payload enforcement DENIES: a protocol skill driven with no live run.
const runLessProtocolSkill = `{"session_id":"sess-%s","tool_name":"Skill","tool_use_id":"tu-1","tool_input":{"skill":"summoner:phase-submit"}}`

// TestDormantDefault pins the production default on the function the binary
// runs: with no opt-in the gate is not a pass-through for SOME calls — it
// reads nothing and denies nothing for EVERY call, input enforcement would
// fail closed on included.
func TestDormantDefault(t *testing.T) {
	hooktest.Dormant(t)
	stateDir, traceDir := hooktest.IsolateState(t)
	payloads := map[string]string{
		"enforcement denies it: a protocol skill with no live run": strings.Replace(runLessProtocolSkill, "%s", "dormant", 1),
		"enforcement fails closed on it: unreadable input":         `not a hook payload`,
		"nothing on stdin": ``,
	}
	var errW bytes.Buffer
	for name, payload := range payloads {
		errW.Reset()
		if code := runGate(strings.NewReader(payload), &errW); code != 0 {
			t.Errorf("%s: dormant gate exited %d, want 0 (stderr: %q)", name, code, errW.String())
		}
		if errW.String() != "" {
			t.Errorf("%s: dormant gate wrote stderr %q, want none", name, errW.String())
		}
	}
	for name, dir := range map[string]string{"state": stateDir, "trace": traceDir} {
		if n := hooktest.CountFiles(dir); n != 0 {
			t.Errorf("dormant gate wrote %d file(s) under the %s dir, want none", n, name)
		}
	}
}

// TestOptInArmsTheGate is the other half of the contract: the call the
// default waves through is denied once the opt-in is set.
func TestOptInArmsTheGate(t *testing.T) {
	hooktest.OptIn(t)
	hooktest.IsolateState(t)
	var errW bytes.Buffer
	payload := strings.Replace(runLessProtocolSkill, "%s", "opt-in", 1)
	if code := runGate(strings.NewReader(payload), &errW); code != 2 || !strings.Contains(errW.String(), "requires an active run") {
		t.Fatalf("opted-in gate: exit %d, stderr %q — want exit 2 denying the run-less protocol skill", code, errW.String())
	}
}
