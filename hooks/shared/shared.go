// Package shared provides common utilities for Summoner hooks.
package shared

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
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

// ProjectKey returns a stable hash of the project directory. The directory is
// NORMALIZED first — filepath.Abs, then EvalSymlinks (task-2.5 m4) — so two
// spellings of one checkout (a relative path, a symlinked path such as /var
// vs /private/var, or a user-level link to the repo) hash to the same key
// instead of forking the state and trace trees. Any normalization failure
// falls back to the best value obtained so far.
func ProjectKey() string {
	dir := ProjectDir()
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			dir = real
		}
	}
	hash := sha256.Sum256([]byte(dir))
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
	AdditionalContext  string              `json:"additionalContext,omitempty"`
	HookSpecificOutput *HookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

// HookSpecificOutput is the Claude Code-specific hook output wrapper.
type HookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// EmitOutputTo prints one hook response to w as JSON. The platform parses a
// hook's stdout as a SINGLE JSON object — hooks with several context blocks
// must join them into one payload and call this once (the 0.5.2 session-start
// emitted one object per block and broke every session start in manifest
// projects; user report 2026-11-20). There is deliberately no multi-call
// wrapper: the stdout-permission this API gives is the single-object contract.
func EmitOutputTo(w io.Writer, hookEventName, ctx string) {
	out := Output{AdditionalContext: ctx}
	if os.Getenv("CLAUDE_PLUGIN_ROOT") != "" && os.Getenv("COPILOT_CLI") == "" {
		out = Output{
			HookSpecificOutput: &HookSpecificOutput{
				HookEventName:     hookEventName,
				AdditionalContext: ctx,
			},
		}
	}
	json.NewEncoder(w).Encode(out)
}
