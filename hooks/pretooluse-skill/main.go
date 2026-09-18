// PreToolUse hook — fires before every Skill / Edit / Write tool call.
//
// Two jobs, both 100% hook-driven (the AI has zero state-tracking instructions):
//
//  1. Workflow tracking: a Skill("summoner") invocation writes session state
//     (keyed by session_id — parallel sessions no longer clobber each other);
//     any subsequent phase-skill invocation marks PhaseAdvanced (Phase 1+).
//
//  2. Chain-mode iron law (2026-09, peer proposal #1): within an active
//     Summoner workflow, Edit/Write are DENIED until a phase skill has run —
//     "no code changes before the early phases (diagnosis/spec)" was prompt-
//     only until now; graph mode had the walker, chain mode had nothing.
//     Fail-open by design: no state, no manifest, kill-switch, or an
//     all-none manifest ⇒ allow. Deny = exit 2 (stderr is shown to the model).
//
// Payload note: the tool's arguments arrive NESTED in tool_input (verified
// 2026-09-17); the legacy flat "skill" form is still honored.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

func main() {
	var input shared.ToolInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		os.Exit(0)
	}

	switch input.ToolName {
	case "", "Skill":
		handleSkillCall(input)
	case "Edit", "Write":
		if rc := gateEditWrite(input); rc != 0 {
			os.Exit(rc)
		}
	}
	os.Exit(0)
}

// handleSkillCall — workflow-start detection + phase-advance marking.
func handleSkillCall(input shared.ToolInput) {
	if err := os.MkdirAll(shared.StateDir(), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Summoner hook: failed to create state dir: %v\n", err)
		os.Exit(0)
	}

	skill := input.EffectiveSkill()
	stateFile := shared.SessionStateFile(input.SessionID)

	switch {
	case skill == "summoner":
		// Workflow start. If the project manifest declares NO runnable phase
		// skills (all none/absent), pre-advance: no Skill call will ever fire
		// and the iron-law gate would otherwise deadlock the workflow.
		state := shared.State{
			Workflow:      "summoner",
			StartedAt:     time.Now().Format(time.RFC3339),
			SessionID:     input.SessionID,
			PhaseAdvanced: manifestHasNoPhaseSkills(),
		}
		writeState(stateFile, state)

	case skill != "":
		// A phase skill is running ⇒ Phase 1+ reached. Mark on existing state.
		if data, err := os.ReadFile(stateFile); err == nil {
			var state shared.State
			if json.Unmarshal(data, &state) == nil && !state.PhaseAdvanced {
				state.PhaseAdvanced = true
				writeState(stateFile, state)
			}
		}
	}
}

// gateEditWrite — chain-mode iron law. Returns the exit code (0 = allow,
// 2 = deny). Fail-open everywhere: this gate must never brick normal work.
func gateEditWrite(input shared.ToolInput) int {
	if os.Getenv("SUMMONER_IRON_LAW") == "off" {
		return 0
	}
	data, err := os.ReadFile(shared.SessionStateFile(input.SessionID))
	if err != nil {
		return 0 // no active Summoner workflow in this session → not our business
	}
	var state shared.State
	if json.Unmarshal(data, &state) != nil {
		return 0 // unreadable state → fail open
	}
	if state.PhaseAdvanced {
		return 0
	}
	fmt.Fprintf(os.Stderr, `⛔ Summoner iron law: this session has an active Summoner workflow whose early phases (Phase 0-1: memory retrieval + diagnosis/spec) have not run yet — no phase-skill invocation recorded. Code edits are blocked until then.
- Complete the workflow's early phases first (the checkpoint flow will guide you).
- If this workflow's phases are deliberately skill:none and this block is wrong, export SUMMONER_IRON_LAW=off.
`)
	return 2
}

// manifestHasNoPhaseSkills — light scan of the project's summoner.yaml:
// true iff the phases: section declares no runnable skill (every entry
// "none", empty, or no phases at all). Used to pre-advance skill-less
// manifests so the gate never deadlocks them.
func manifestHasNoPhaseSkills() bool {
	data, err := os.ReadFile(shared.ProjectDir() + string(os.PathSeparator) + "summoner.yaml")
	if err != nil {
		return false // no manifest → No-Manifest flow → superpowers skills WILL run
	}
	inPhases, runnable := false, false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "phases:") {
			inPhases = true
			continue
		}
		if inPhases && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			break // next top-level key
		}
		if inPhases {
			v := trimmed
			if i := strings.Index(v, ":"); i >= 0 {
				v = strings.TrimSpace(v[i+1:])
			}
			if v != "" && v != "none" && v != `"none"` {
				runnable = true
			}
		}
	}
	return inPhases && !runnable
}

func writeState(path string, state shared.State) {
	data, err := json.Marshal(state)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Summoner hook: failed to marshal state: %v\n", err)
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Summoner hook: failed to write state: %v\n", err)
	}
}
