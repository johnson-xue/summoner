// SessionStart hook — injects Summoner context and reports project readiness.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

func main() {
	shared.EmitOutput("SessionStart", renderContext(shared.PluginRoot(), shared.ProjectDir()))
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

	// Uninitialized: the actionable next step IS the pointer — the setup
	// script. The workflow skill is irrelevant until a manifest exists
	// (auto-setup-summoner takes over from this warning).
	return fmt.Sprintf(`<SUMMONER-HOOK>
Summoner not initialized (no summoner.yaml) — say "setup summoner" or run %s/scripts/summoner-setup.sh
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
