// SessionStart hook — injects Summoner context and reports project readiness.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

func main() {
	sessionID := parseSessionID(os.Stdin)
	shared.EmitOutput("SessionStart", renderContext(shared.PluginRoot(), shared.ProjectDir()))
	emitPendingPostGame()
	if sessionID != "" {
		scanDanglingIntents(sessionID)
	}
}

// parseSessionID reads the SessionStart payload for its session_id — the only
// field this hook consumes, and tolerantly: a missing/invalid payload skips
// the crash scan (the hook's other duties still run; SessionStart must never
// brick a session over its own input).
func parseSessionID(r io.Reader) string {
	var raw struct {
		SessionID string `json:"session_id"`
	}
	dec := json.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return ""
	}
	return raw.SessionID
}

// scanDanglingIntents is the SessionStart crash-window recovery scan (T19,
// spec §5 OUTCOME_UNKNOWN 与副作用恢复): a restarted session inherits the
// previous session's runs, and a MUTATING intent whose result never landed —
// a write-class tool call with no tool.result/tool.failed, or a worker
// delegation that never completed (the previous session's worker processes are
// dead; their side effects are uncertain) — is an outcome-unknown side effect.
// Each found id is marked OUTCOME_UNKNOWN (idempotently: already-marked ids
// and terminal-state runs are skipped by the reducer) and its marking is
// traced as tool.outcome_unknown with detected_by session-start. Read-only
// calls, Ask/decision bookkeeping, Skill channels and reviewer delegations
// (the §3 review lifecycle owns those) are never crash candidates.
//
// NEVER exits non-zero and never blocks the session: every failure is a stderr
// warning — a session must always be able to start (the Stop hook re-runs this
// scan's tool half, so a missed mark here is caught at the next turn end).
func scanDanglingIntents(sessionID string) {
	runs, err := shared.SessionRuns(sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "summoner session-start: crash scan skipped — session run-state unreadable: %v\n", err)
		return
	}
	for _, run := range runs {
		if run.Status == shared.StatusClosed || run.Status == shared.StatusPostGamePending {
			continue // terminal: nothing may be marked there
		}
		evs, err := shared.LoadRunEvents(run.RunID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "summoner session-start: crash scan skipped for run %s — trace unreadable: %v\n", run.RunID, err)
			continue
		}
		before := len(run.OutcomeUnknowns)
		for _, d := range shared.MutatingDanglingIntents(evs, true) {
			// Recovery round I3: freeze the intent's pre-effect declaration.
			run.MarkOutcomeUnknownWithDecl(d.ToolUseID, d.Decl)
		}
		if len(run.OutcomeUnknowns) == before {
			continue
		}
		if err := run.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "summoner session-start: crash scan could not persist run %s (state unchanged, next Stop rescans): %v\n", run.RunID, err)
			continue
		}
		for _, e := range run.OutcomeUnknowns[before:] {
			if err := shared.AppendEvent(run.RunID, sessionID, shared.TraceEvent{
				Type:      shared.EventToolOutcomeUnknown,
				Source:    "session-start",
				ToolUseID: e.ToolUseID,
				Payload:   map[string]interface{}{"tool_use_id": e.ToolUseID, "detected_by": "session-start"},
			}); err != nil {
				fmt.Fprintf(os.Stderr, "summoner session-start: tool.outcome_unknown trace append failed for %s: %v\n", e.ToolUseID, err)
			}
		}
	}
}

// emitPendingPostGame surfaces this project's owed post-games (T18): runs
// stuck POST_GAME_PENDING in ANY session. Display-only — completion happens
// in the OWNING session (its hooks observe the questionnaire answers and the
// journal write); this pointer exists so a fresh session knows the debt
// exists and names it. Errors stay silent: a state-read problem must never
// break session start (the viewer path reports it properly).
func emitPendingPostGame() {
	runs, err := shared.PendingPostGameRuns()
	if err != nil || len(runs) == 0 {
		return
	}
	ids := make([]string, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.RunID)
	}
	shared.EmitOutput("SessionStart", fmt.Sprintf(
		"Summoner post-game owed: run(s) %s are POST_GAME_PENDING (questionnaire q1..q4 + journal .summoner/journal/<run_id>.md) — resume the OWNING session to complete and CLOSE them.",
		strings.Join(ids, ", ")))
}

// renderContext builds the SessionStart injection: project status + one
// pointer. (M-C1) The old block carried a hand-maintained command catalog —
// which went stale once: it said "Six" while seven commands existed — plus a
// Rules section duplicating skills/summoner/SKILL.md: ~30 lines injected into
// EVERY session, Summoner used or not. The host UI already lists plugin
// commands; the hook's legitimate job is status + pointer. Budget: the whole
// block stays ≤300 bytes even with marketplace-length plugin roots
// (enforced by TestRenderContextByteBudget).
func renderContext(pluginRoot, projectDir string) string {
	manifest := filepath.Join(projectDir, "summoner.yaml")

	if _, err := os.Stat(manifest); err == nil {
		projectName := extractProjectName(manifest)
		// Reject path-SHAPED names (traversal), not dotted names: "my.project"
		// is a legal project name (found in review — the old guard mapped it
		// to "unknown" and then reported the wrong DB state).
		if projectName != filepath.Base(projectName) || projectName == "." || projectName == ".." ||
			strings.ContainsAny(projectName, "/\\") {
			projectName = "unknown"
		}
		// Display-only truncation keeps the 300B byte budget deterministic for
		// long project names (the DB lookup below uses the full name).
		display := projectName
		if len(display) > 40 {
			display = display[:37] + "…"
		}
		status := fmt.Sprintf("project: %s (%d phases, memory: ", display, countPhases(manifest))
		dbFile := filepath.Join(shared.MemoryDir(), projectName+".db")
		legacyFile := filepath.Join(pluginRoot, "memory", projectName+".db")
		if _, err := os.Stat(dbFile); err == nil {
			status += countPatterns(dbFile) + " patterns)"
		} else if _, err := os.Stat(legacyFile); err == nil {
			status += "legacy DB found — scripts/init-memory-db.sh migrates it)"
		} else {
			status += "DB not initialized — scripts/init-memory-db.sh)"
		}
		return fmt.Sprintf(`<SUMMONER-HOOK>
Summoner %s. Workflows: read %s/skills/summoner/SKILL.md
</SUMMONER-HOOK>`, status, pluginRoot)
	}

	// No project capabilities: built-in defaults still enter the SAME reducer.
	// Setup is optional and explicit; the pointer does not start a second flow.
	return fmt.Sprintf(`<SUMMONER-HOOK>
Summoner not initialized (no summoner.yaml): 未配置项目专属能力. Built-in defaults use the same reducer. Setup: %s/scripts/summoner-setup.sh
</SUMMONER-HOOK>`, pluginRoot)
}

// extractProjectName reads the project.name field from summoner.yaml.
// Parses YAML directly instead of shelling out to grep — avoids matching
// "name:" keys outside the project: section.
func extractProjectName(manifest string) string {
	data, err := os.ReadFile(manifest)
	if err != nil {
		return "unknown"
	}
	inProject := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Enter project: section
		if strings.HasPrefix(trimmed, "project:") {
			inProject = true
			continue
		}
		// Exit project: section at next top-level key
		if inProject && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "" {
			break
		}
		// Match "  name: my-project" inside project: section
		if inProject && strings.HasPrefix(trimmed, "name:") {
			name := strings.TrimSpace(strings.TrimPrefix(trimmed, "name:"))
			name = strings.Trim(name, `"'`)
			if name != "" {
				return name
			}
		}
	}
	return "unknown"
}

// countPatterns returns the count of rows in the patterns table.
func countPatterns(dbFile string) string {
	out, err := exec.Command("sqlite3", dbFile, "SELECT COUNT(*) FROM patterns;").Output()
	if err != nil {
		return "?"
	}
	return strings.TrimSpace(string(out))
}

// countPhases counts actual phase entries in summoner.yaml (only within phases: section).
// Each phase is a YAML key at 2-space indent under phases: — not counting comments or blank lines.
func countPhases(manifest string) int {
	data, err := os.ReadFile(manifest)
	if err != nil {
		return 0
	}
	count := 0
	inPhases := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "phases:") {
			inPhases = true
			continue
		}
		if inPhases {
			// Exit phases section when we hit a non-indented top-level key
			if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "" {
				break
			}
			// Count phase entries: 2-space indent, key, colon, optional inline
			// value. Matches "  debug:", "  debug: none" (inline values are
			// legal YAML — found in review: the space check undercounted them
			// to "0 phases"), but not "    skill: ..." (3+ spaces = sub-key).
			if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "  #") {
				key := trimmed
				if i := strings.Index(key, ":"); i > 0 && key[:i] != "" && !strings.ContainsAny(key[:i], " \t") {
					count++
				}
			}
		}
	}
	return count
}
