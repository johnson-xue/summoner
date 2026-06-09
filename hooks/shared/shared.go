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
	dir, _ := os.Getwd()
	return dir
}

// ProjectKey returns a stable hash of the project directory.
func ProjectKey() string {
	hash := sha256.Sum256([]byte(ProjectDir()))
	return fmt.Sprintf("%x", hash[:8])
}

// StateDir returns the path to the Summoner state directory.
func StateDir() string {
	return filepath.Join(os.TempDir(), "summoner-state")
}

// StateFile returns the path to the state file for the current project.
func StateFile() string {
	return filepath.Join(StateDir(), ProjectKey()+".json")
}

// State represents the Summoner workflow state.
type State struct {
	Workflow  string `json:"workflow"`
	StartedAt string `json:"started_at"`
}

// ToolInput represents a Claude Code tool call input (stdin).
type ToolInput struct {
	Skill string `json:"skill,omitempty"`
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
