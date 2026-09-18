// Package shared provides common utilities for Summoner hooks.
package shared

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// PluginRoot returns the Summoner plugin root directory.
func PluginRoot() string {
	if dir := os.Getenv("CLAUDE_PLUGIN_ROOT"); dir != "" {
		return dir
	}
	// Fallback: relative to the binary location
	exe, _ := os.Executable()
	return filepath.Dir(filepath.Dir(filepath.Dir(exe)))
}

// ProjectDir returns the current project directory.
func ProjectDir() string {
	if dir := os.Getenv("CLAUDE_PROJECT_DIR"); dir != "" {
		return dir
	}
	dir, err := os.Getwd()
	if err != nil || dir == "" {
		// hooks run as external processes — cwd may be unreliable;
		// fall back to a stable sentinel so StateFile is still unique
		return filepath.Join(os.TempDir(), "summoner-unknown-project")
	}
	return dir
}

// ProjectKey returns a stable hash of the project directory.
func ProjectKey() string {
	hash := sha256.Sum256([]byte(ProjectDir()))
	return fmt.Sprintf("%x", hash[:8])
}

// MemoryDir returns the canonical, VERSION-INDEPENDENT memory root:
// $SUMMONER_MEMORY_DIR, else ~/.summoner/memory. Plugin-cache memory/ dirs
// die with each version bump (orphaning every DB); the stable root survives
// upgrades. Legacy DBs migrate by copy on init (scripts/init-memory-db.sh)
// or first open (internal/context).
func MemoryDir() string {
	if d := os.Getenv("SUMMONER_MEMORY_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "summoner-memory")
	}
	return filepath.Join(home, ".summoner", "memory")
}

// StateDir returns the path to the Summoner state directory.
func StateDir() string {
	return filepath.Join(os.TempDir(), "summoner-state")
}

// StateFile returns the LEGACY project-scoped state path (pre-session-keying
// form; kept only so the Stop hook can clean up files older sessions left).
func StateFile() string {
	return filepath.Join(StateDir(), ProjectKey()+".json")
}

// SessionStateFile keys state by project AND session id — two parallel
// sessions on one project used to overwrite each other's state (the Stop
// reminder of session A silently deleted session B's state).
func SessionStateFile(sessionID string) string {
	return filepath.Join(StateDir(), ProjectKey()+"-"+sessionID+".json")
}

// State represents the Summoner workflow state.
type State struct {
	Workflow      string `json:"workflow"`
	StartedAt     string `json:"started_at"`
	SessionID     string `json:"session_id,omitempty"`
	PhaseAdvanced bool   `json:"phase_advanced,omitempty"` // a phase-skill ran (Phase 1+ reached)
}

// ToolInput represents a Claude Code PreToolUse hook input (stdin).
// Real payload shape (verified 2026-09-17): the tool's arguments live in a
// NESTED tool_input object — a flat top-level "skill" only exists in legacy
// payloads, and parsing only the flat form made this hook silently no-op in
// production.
type ToolInput struct {
	SessionID string `json:"session_id,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	// Legacy flat form (kept for back-compat)
	Skill string `json:"skill,omitempty"`
	// Current nested form
	ToolInput struct {
		Skill string `json:"skill,omitempty"`
	} `json:"tool_input,omitempty"`
}

// EffectiveSkill returns the skill argument from whichever shape carried it.
func (t ToolInput) EffectiveSkill() string {
	if t.ToolInput.Skill != "" {
		return t.ToolInput.Skill
	}
	return t.Skill
}

// Output represents a hook response.
type Output struct {
	AdditionalContext    string              `json:"additionalContext,omitempty"`
	HookSpecificOutput   *HookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

// HookSpecificOutput is the Claude Code-specific hook output wrapper.
type HookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// EmitOutput prints the hook response to stdout as JSON.
// hookEventName is required — it identifies which hook is emitting (e.g. "SessionStart").
func EmitOutput(hookEventName, ctx string) {
	out := Output{AdditionalContext: ctx}
	if os.Getenv("CLAUDE_PLUGIN_ROOT") != "" && os.Getenv("COPILOT_CLI") == "" {
		out = Output{
			HookSpecificOutput: &HookSpecificOutput{
				HookEventName:     hookEventName,
				AdditionalContext: ctx,
			},
		}
	}
	json.NewEncoder(os.Stdout).Encode(out)
}
