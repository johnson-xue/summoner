// Stop hook — checks if Summoner was active in THIS session and warns about
// post-game review. The state file is written by the PreToolUse hook, never
// by the AI. Session-keyed (2026-09): parallel sessions on one project no
// longer overwrite/delete each other's state; stale files (>24h, crashed
// sessions) are swept opportunistically.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

func main() {
	var input struct {
		SessionID string `json:"session_id,omitempty"`
	}
	_ = json.NewDecoder(os.Stdin).Decode(&input)

	stateFile := ""
	var state shared.State
	if input.SessionID != "" {
		f := shared.SessionStateFile(input.SessionID)
		if data, err := os.ReadFile(f); err == nil && json.Unmarshal(data, &state) == nil {
			stateFile = f
		}
	}
	if stateFile == "" {
		// legacy project-scoped file (older plugin version) — still honor it
		f := shared.StateFile()
		if data, err := os.ReadFile(f); err == nil && json.Unmarshal(data, &state) == nil {
			stateFile = f
		}
	}
	if stateFile == "" {
		sweepStale()
		os.Exit(0) // No state = Summoner was not used
	}

	fmt.Fprintf(os.Stderr, "\n⚡ Summoner was active in this session (started %s).\n", state.StartedAt)
	fmt.Fprintf(os.Stderr, "   If you didn't complete the post-game review, you can still run it.\n")
	fmt.Fprintf(os.Stderr, "   Otherwise, ignore this reminder.\n\n")

	os.Remove(stateFile)
	sweepStale()
}

// sweepStale removes this project's state files older than 24h (sessions
// that crashed without firing Stop would otherwise litter the state dir —
// and a leftover file would keep the iron-law gate armed for a dead session).
func sweepStale() {
	entries, err := os.ReadDir(shared.StateDir())
	if err != nil {
		return
	}
	prefix := shared.ProjectKey()
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || !strings.HasPrefix(name, prefix) {
			continue
		}
		path := filepath.Join(shared.StateDir(), name)
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			os.Remove(path)
		}
	}
}
