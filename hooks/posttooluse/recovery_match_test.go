package main

// recovery_match_test.go — recovery-round I3, posttooluse half: the mechanical
// probe matcher. A Bash completion whose gate admission consumed an unknown's
// one-shot ticket (probe_of on its tool.started) gets the matcher's verdict
// recorded on its OWN tool.result — judged solely against the entry's frozen
// declaration over the observed stdout: expect → accepted, preimage → failed,
// anything else / lost / unsupported → indeterminate. An unbound call never
// records a match.

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

func pshaHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// matcherFixture parks one declared unknown and admits one bound probe call.
func matcherFixture(t *testing.T, decl *shared.MutationDecl) string {
	t.Helper()
	useHookEnv(t)
	r := startRun(t)
	r.MarkOutcomeUnknownWithDecl("tu-crash-1", decl)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	appendPostEvent(t, shared.TraceEvent{
		Type:      shared.EventToolStarted,
		ToolUseID: "tu-p1",
		Payload: map[string]interface{}{
			"tool_name": "Bash", "read_class": true, "probe_of": "tu-crash-1",
		},
	})
	return "tu-p1"
}

// probeMatchOf reads the matcher's record off the probe's tool.result.
func probeMatchOf(t *testing.T, probeID string) map[string]interface{} {
	t.Helper()
	for _, ev := range readHookTrace(t, "run-1") {
		if ev.Type == shared.EventToolResult && ev.ToolUseID == probeID {
			m, _ := ev.Payload["probe_match"].(map[string]interface{})
			return m
		}
	}
	return nil
}

// TestMatcherVerdictsComeFromObservedStdoutOnly — R7: the verdict is computed
// from the observed stdout against the frozen declaration, nothing else.
func TestMatcherVerdictsComeFromObservedStdoutOnly(t *testing.T) {
	decl := &shared.MutationDecl{
		Capability: shared.CapabilityFileWrite, Target: "w1.md",
		Preimage: pshaHex("old\n"), Expect: pshaHex("new\n"),
	}
	for name, tc := range map[string]struct {
		stdout       string
		wantVerdict  string
		wantObserved string
	}{
		"observed == expect ⇒ accepted":     {decl.Expect + "  w1.md\n", shared.OutcomeAccepted, decl.Expect},
		"observed == preimage ⇒ failed":     {decl.Preimage + "  w1.md\n", shared.OutcomeFailed, decl.Preimage},
		"observed other ⇒ indeterminate":    {pshaHex("something else") + "  w1.md\n", shared.OutcomeIndeterminate, pshaHex("something else")},
		"no hash in stdout ⇒ indeterminate": {"cat: w1.md: No such file\n", shared.OutcomeIndeterminate, ""},
	} {
		t.Run(name, func(t *testing.T) {
			probeID := matcherFixture(t, decl)
			if code, stderr := callHook(t, postUsePayload(t, "Bash", probeID,
				map[string]interface{}{"command": decl.ExactProbeCommand()},
				map[string]interface{}{"stdout": tc.stdout})); code != 0 {
				t.Fatalf("probe result exit = %d (stderr %q)", code, stderr)
			}
			m := probeMatchOf(t, probeID)
			if m == nil {
				t.Fatal("bound probe's tool.result carries no probe_match")
			}
			if m["verdict"] != tc.wantVerdict {
				t.Fatalf("verdict = %v, want %s (match %v)", m["verdict"], tc.wantVerdict, m)
			}
			if got, _ := m["observed"].(string); got != tc.wantObserved {
				t.Fatalf("observed = %q, want %q", got, tc.wantObserved)
			}
		})
	}
}

// TestMatcherUnsupportedNeverConcludesBetterThanIndeterminate — R7: an entry
// with no supported declaration (undeclared write-class Bash, legacy intent)
// records indeterminate regardless of what the stdout happens to show.
func TestMatcherUnsupportedNeverConcludesBetterThanIndeterminate(t *testing.T) {
	for name, decl := range map[string]*shared.MutationDecl{
		"nil decl":               nil,
		"unsupported capability": {Capability: shared.CapabilityUnsupported},
		"missing expect":         {Capability: shared.CapabilityFileWrite, Target: "w1.md", Preimage: pshaHex("old\n")},
	} {
		t.Run(name, func(t *testing.T) {
			probeID := matcherFixture(t, decl)
			// Even a stdout that LOOKS like the expected landing proves nothing.
			if code, stderr := callHook(t, postUsePayload(t, "Bash", probeID,
				map[string]interface{}{"command": "ls -la"},
				map[string]interface{}{"stdout": pshaHex("new\n") + "  w1.md\n"})); code != 0 {
				t.Fatalf("probe result exit = %d (stderr %q)", code, stderr)
			}
			m := probeMatchOf(t, probeID)
			if m == nil {
				t.Fatal("bound probe's tool.result carries no probe_match")
			}
			if m["verdict"] != shared.OutcomeIndeterminate {
				t.Fatalf("verdict = %v, want indeterminate (match %v)", m["verdict"], m)
			}
		})
	}
}

// TestUnboundCallRecordsNoMatch: a Bash completion whose admission did not
// consume a ticket never records a probe_match — the authority chain is the
// gate's probe_of binding, not the stdout's shape.
func TestUnboundCallRecordsNoMatch(t *testing.T) {
	useHookEnv(t)
	r := startRun(t)
	r.MarkOutcomeUnknownWithDecl("tu-crash-1", &shared.MutationDecl{
		Capability: shared.CapabilityFileWrite, Target: "w1.md",
		Preimage: pshaHex("old\n"), Expect: pshaHex("new\n"),
	})
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	appendPostEvent(t, shared.TraceEvent{
		Type:      shared.EventToolStarted,
		ToolUseID: "tu-loose",
		Payload:   map[string]interface{}{"tool_name": "Bash", "read_class": true, "command": "shasum -a 256 'w1.md'"},
	})
	if code, stderr := callHook(t, postUsePayload(t, "Bash", "tu-loose",
		map[string]interface{}{"command": "shasum -a 256 'w1.md'"},
		map[string]interface{}{"stdout": pshaHex("new\n") + "  w1.md\n"})); code != 0 {
		t.Fatalf("loose probe exit = %d (stderr %q)", code, stderr)
	}
	if m := probeMatchOf(t, "tu-loose"); m != nil {
		t.Fatalf("unbound call recorded a probe_match: %v", m)
	}
}
