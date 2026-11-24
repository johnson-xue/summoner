package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/johnson-xue/summoner/hooks/hooktest"
)

func TestMain(m *testing.M) { os.Exit(hooktest.Run(m)) }

// A payload enforcement ACTS on: a typed Summoner verb starts a run.
func summonerVerb(session string) string {
	return `{"hook_event_name":"UserPromptSubmit","session_id":"sess-` + session + `","prompt":"/summoner:fix the loader nil deref"}`
}

// TestDormantDefault: with no opt-in no run is ever started, so nothing can
// ever be gated — and unreadable input, which the enforcing path fails
// closed on, is not even read.
func TestDormantDefault(t *testing.T) {
	hooktest.Dormant(t)
	stateDir, traceDir := hooktest.IsolateState(t)
	t.Cleanup(hooktest.Put("CLAUDE_PROJECT_DIR", t.TempDir()))
	payloads := map[string]string{
		"enforcement starts a run for it: a Summoner verb": summonerVerb("dormant"),
		"enforcement fails closed on it: unreadable input": `not a hook payload`,
		"nothing on stdin": ``,
	}
	var errW bytes.Buffer
	for name, payload := range payloads {
		errW.Reset()
		if code := runHook(strings.NewReader(payload), &errW); code != 0 {
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

// TestOptInArmsTheRunStart: the same prompt records a run under the opt-in.
func TestOptInArmsTheRunStart(t *testing.T) {
	hooktest.OptIn(t)
	stateDir, _ := hooktest.IsolateState(t)
	t.Cleanup(hooktest.Put("CLAUDE_PROJECT_DIR", t.TempDir()))
	var errW bytes.Buffer
	if code := runHook(strings.NewReader(summonerVerb("opt-in")), &errW); code != 0 {
		t.Fatalf("opted-in hook: exit %d, stderr %q — want 0", code, errW.String())
	}
	if hooktest.CountFiles(stateDir) == 0 {
		t.Fatal("opted-in hook recorded no run under the state dir")
	}
}
