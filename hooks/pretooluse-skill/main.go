// PreToolUse hook — fires before every Skill tool call.
// Detects Summoner invocation and writes a state file.
// 100% hook-driven — the AI has zero state tracking instructions.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

func main() {
	// Read tool input from stdin
	var input shared.ToolInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		os.Exit(0)
	}

	// Only track Summoner skill invocations
	if input.Skill != "summoner" {
		os.Exit(0)
	}

	// Write state file — hook does this, NOT the AI
	os.MkdirAll(shared.StateDir(), 0755)

	state := shared.State{
		Workflow:  "summoner",
		StartedAt: time.Now().Format(time.RFC3339),
	}

	data, err := json.Marshal(state)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Summoner hook: failed to marshal state: %v\n", err)
		os.Exit(0)
	}

	if err := os.WriteFile(shared.StateFile(), data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Summoner hook: failed to write state: %v\n", err)
		os.Exit(0)
	}

	os.Exit(0)
}
