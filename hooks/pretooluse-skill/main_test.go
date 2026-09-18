package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// TestNestedPayloadParse — the production payload shape (tool arguments
// nested under tool_input). The flat-only parser made this hook silently
// no-op in production (found 2026-09-17); this test pins the fix.

// setenv16 — t.Setenv is go1.17+; this repo pins go1.16.
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

func TestNestedPayloadParse(t *testing.T) {
	raw := `{"session_id":"sess-1","transcript_path":"/tmp/t.jsonl","cwd":"/x","hook_event_name":"PreToolUse","tool_name":"Skill","tool_input":{"skill":"summoner","args":"fix"}}`
	var in shared.ToolInput
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatal(err)
	}
	if in.EffectiveSkill() != "summoner" {
		t.Errorf("EffectiveSkill = %q, want summoner", in.EffectiveSkill())
	}
	if in.ToolName != "Skill" || in.SessionID != "sess-1" {
		t.Errorf("ToolName=%q SessionID=%q", in.ToolName, in.SessionID)
	}
}

func TestLegacyFlatPayload(t *testing.T) {
	var in shared.ToolInput
	if err := json.Unmarshal([]byte(`{"skill":"summoner"}`), &in); err != nil {
		t.Fatal(err)
	}
	if in.EffectiveSkill() != "summoner" {
		t.Errorf("legacy flat form: EffectiveSkill = %q", in.EffectiveSkill())
	}
}

// TestIronLawLifecycle — start → gate denies Edit → phase skill → gate allows.
func TestIronLawLifecycle(t *testing.T) {
	dir := t.TempDir()
	setenv16(t, "CLAUDE_PROJECT_DIR", dir)
	os.MkdirAll(shared.StateDir(), 0755)

	// 1. workflow start (nested payload shape, all-runnable manifest → not pre-advanced)
	if err := os.WriteFile(filepath.Join(dir, "summoner.yaml"),
		[]byte("version: \"1\"\nproject:\n  name: t\nphases:\n  debug:\n    skill: my-debug\n"), 0644); err != nil {
		t.Fatal(err)
	}
	handleSkillCall(shared.ToolInput{SessionID: "s1", ToolName: "Skill", ToolInput: struct {
		Skill string `json:"skill,omitempty"`
	}{"summoner"}})

	if rc := gateEditWrite(shared.ToolInput{SessionID: "s1", ToolName: "Edit"}); rc != 2 {
		t.Errorf("pre-phase Edit: rc = %d, want 2 (deny)", rc)
	}

	// 2. phase skill runs → PhaseAdvanced
	handleSkillCall(shared.ToolInput{SessionID: "s1", ToolName: "Skill", ToolInput: struct {
		Skill string `json:"skill,omitempty"`
	}{"my-debug"}})
	if rc := gateEditWrite(shared.ToolInput{SessionID: "s1", ToolName: "Write"}); rc != 0 {
		t.Errorf("post-phase Write: rc = %d, want 0 (allow)", rc)
	}

	// 3. parallel session isolation: s2 has no state → allowed, untouched
	if rc := gateEditWrite(shared.ToolInput{SessionID: "s2", ToolName: "Edit"}); rc != 0 {
		t.Errorf("other-session Edit: rc = %d, want 0", rc)
	}
}

func TestIronLawKillSwitchAndSkilllessManifest(t *testing.T) {
	dir := t.TempDir()
	setenv16(t, "CLAUDE_PROJECT_DIR", dir)
	os.MkdirAll(shared.StateDir(), 0755)

	// kill switch
	handleSkillCall(shared.ToolInput{SessionID: "k1", ToolName: "Skill", ToolInput: struct {
		Skill string `json:"skill,omitempty"`
	}{"summoner"}})
	setenv16(t, "SUMMONER_IRON_LAW", "off")
	if rc := gateEditWrite(shared.ToolInput{SessionID: "k1", ToolName: "Edit"}); rc != 0 {
		t.Errorf("kill switch: rc = %d, want 0", rc)
	}
	setenv16(t, "SUMMONER_IRON_LAW", "")

	// all-none manifest → pre-advanced at start (gate must not deadlock)
	dir2 := t.TempDir()
	setenv16(t, "CLAUDE_PROJECT_DIR", dir2)
	if err := os.WriteFile(filepath.Join(dir2, "summoner.yaml"),
		[]byte("version: \"1\"\nproject:\n  name: t2\nphases:\n  debug:\n    skill: none\n"), 0644); err != nil {
		t.Fatal(err)
	}
	handleSkillCall(shared.ToolInput{SessionID: "k2", ToolName: "Skill", ToolInput: struct {
		Skill string `json:"skill,omitempty"`
	}{"summoner"}})
	if rc := gateEditWrite(shared.ToolInput{SessionID: "k2", ToolName: "Edit"}); rc != 0 {
		t.Errorf("all-none manifest: rc = %d, want 0 (pre-advanced)", rc)
	}
}

func TestManifestHasNoPhaseSkills(t *testing.T) {
	dir := t.TempDir()
	setenv16(t, "CLAUDE_PROJECT_DIR", dir)

	if manifestHasNoPhaseSkills() {
		t.Errorf("no manifest → false expected (No-Manifest flow runs superpowers skills)")
	}
	os.WriteFile(filepath.Join(dir, "summoner.yaml"),
		[]byte("version: \"1\"\nproject:\n  name: t\nphases:\n  a:\n    skill: none\n  b:\n    skill: none\n"), 0644)
	if !manifestHasNoPhaseSkills() {
		t.Errorf("all-none phases → true expected")
	}
	os.WriteFile(filepath.Join(dir, "summoner.yaml"),
		[]byte("version: \"1\"\nproject:\n  name: t\nphases:\n  a:\n    skill: none\n  b:\n    skill: x\n"), 0644)
	if manifestHasNoPhaseSkills() {
		t.Errorf("runnable phase present → false expected")
	}
}
