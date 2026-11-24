package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/johnson-xue/summoner/hooks/hooktest"
	shared "github.com/johnson-xue/summoner/hooks/shared"
)

func TestMain(m *testing.M) { os.Exit(hooktest.Run(m)) }

// seedRun starts one live run in the isolated state dir.
func seedRun(t *testing.T, runID, sessionID string) {
	t.Helper()
	limits, err := shared.ResolveStartLimits()
	if err != nil {
		t.Fatalf("ResolveStartLimits: %v", err)
	}
	if _, err := shared.StartRun(runID, sessionID, "fix", limits); err != nil {
		t.Fatalf("StartRun(%s): %v", runID, err)
	}
}

// TestDormantDefaultSaysSoAndRendersNoRun: with no opt-in the viewer names
// the dormant state instead of rendering a run — even one an earlier version
// left non-CLOSED, whose decisions could no longer be recorded.
func TestDormantDefaultSaysSoAndRendersNoRun(t *testing.T) {
	hooktest.Dormant(t)
	hooktest.IsolateState(t)
	t.Cleanup(hooktest.Put("CLAUDE_PROJECT_DIR", t.TempDir()))
	seedRun(t, "run-stale", "sess-stale")

	var out, errW bytes.Buffer
	if code := runStatus(strings.NewReader(`{"session_id":"sess-stale"}`), &out, &errW); code != 0 {
		t.Fatalf("dormant viewer exited %d, want 0 (stderr %q)", code, errW.String())
	}
	got := out.String()
	if !strings.Contains(got, "dormant") || !strings.Contains(got, shared.EnvHooksEnforce) {
		t.Fatalf("dormant viewer output does not name the state and the opt-in: %q", got)
	}
	if strings.Contains(got, "SUMMONER CHECKPOINT") || strings.Contains(got, "SUMMONER STATUS") {
		t.Errorf("dormant viewer rendered run state: %q", got)
	}
}

// TestHatchKeepsTheViewer: SUMMONER_HOOKS_DISABLE=1 is the un-brick hatch,
// not dormancy. An opted-in project that had to open the hatch keeps a
// wedged session inspectable (commands/status.md).
func TestHatchKeepsTheViewer(t *testing.T) {
	hooktest.Hatch(t) // TestMain armed the opt-in; the hatch outranks it
	hooktest.IsolateState(t)
	t.Cleanup(hooktest.Put("CLAUDE_PROJECT_DIR", t.TempDir()))
	seedRun(t, "run-wedged", "sess-wedged")

	var out, errW bytes.Buffer
	if code := runStatus(strings.NewReader(`{"session_id":"sess-wedged"}`), &out, &errW); code != 0 {
		t.Fatalf("viewer under the hatch exited %d, want 0 (stderr %q)", code, errW.String())
	}
	if got := out.String(); !strings.Contains(got, "SUMMONER STATUS") || strings.Contains(got, "dormant") {
		t.Fatalf("viewer under the hatch must render the run, got %q", got)
	}
}
