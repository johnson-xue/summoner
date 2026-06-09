// Stop hook — checks if Summoner was active and warns about post-game review.
// The state file is written by the PreToolUse hook, never by the AI.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

func main() {
	stateFile := shared.StateFile()

	data, err := os.ReadFile(stateFile)
	if err != nil {
		os.Exit(0) // No state file = Summoner was not used
	}

	var state shared.State
	if err := json.Unmarshal(data, &state); err != nil {
		os.Exit(0)
	}

	fmt.Fprintf(os.Stderr, "\n⚡ Summoner was active in this session (started %s).\n", state.StartedAt)
	fmt.Fprintf(os.Stderr, "   If you didn't complete the post-game review, you can still run it.\n")
	fmt.Fprintf(os.Stderr, "   Otherwise, ignore this reminder.\n\n")

	// Clean up — state is session-scoped
	os.Remove(stateFile)
}
