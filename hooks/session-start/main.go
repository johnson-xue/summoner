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
	pluginRoot := shared.PluginRoot()
	projectDir := shared.ProjectDir()

	// --- Detect project state ---
	var status []string
	manifest := filepath.Join(projectDir, "summoner.yaml")

	if _, err := os.Stat(manifest); err == nil {
		projectName := extractProjectName(manifest)
		status = append(status, fmt.Sprintf("✅ summoner.yaml found (project: %s)", projectName))

		// Check memory DB
		dbFile := filepath.Join(pluginRoot, "memory", projectName+".db")
		if _, err := os.Stat(dbFile); err == nil {
			count := countPatterns(dbFile)
			status = append(status, fmt.Sprintf("✅ Memory DB ready (%s patterns)", count))
		} else {
			status = append(status, fmt.Sprintf(
				"⚠️  Memory DB not initialized. Run: %s/scripts/init-memory-db.sh %s",
				pluginRoot, projectName,
			))
		}

		// Count configured phases
		phaseCount := countPhases(manifest)
		status = append(status, fmt.Sprintf("   %d phases configured", phaseCount))
	} else {
		status = append(status, fmt.Sprintf(
			"⚠️  No summoner.yaml found. Run: %s/scripts/summoner-init.sh", pluginRoot,
		))
	}

	// --- Build context ---
	context := fmt.Sprintf(`<SUMMONER-HOOK>
Summoner AI Agent Orchestration Framework is active. Six workflow commands are available:

  /summoner:fix    Bug fix pipeline (diagnose→reproduce→fix→verify→review)
  /summoner:new    New feature pipeline (define→plan→implement→test→review)
  /summoner:ship   Pre-launch review (adaptive fan-out 1-3 personas→merge)
  /summoner:debug  Diagnose only, no code changes
  /summoner:ops    Server operations via project ops skill
  /summoner:review Standalone code review

Project status:
%s

Rules:
- Every /summoner:* workflow has checkpoints between phases — pause and wait for user input.
- Phase 1 is iron law for /summoner:fix and /summoner:debug — no code changes before root cause.
- Post-game review is mandatory at workflow end.
- Read %s/skills/summoner/SKILL.md for full workflow definitions.
</SUMMONER-HOOK>`, strings.Join(status, "\n"), pluginRoot)

	shared.EmitOutput("SessionStart", context)
}

// extractProjectName reads the project.name field from summoner.yaml.
func extractProjectName(manifest string) string {
	out, err := exec.Command("grep", "-A1", "project:", manifest).Output()
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "name:") {
			name := strings.TrimSpace(strings.TrimPrefix(trimmed, "name:"))
			name = strings.Trim(name, `"'`)
			return name
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

// countPhases counts configured phases in summoner.yaml (only within phases: section).
func countPhases(manifest string) int {
	data, err := os.ReadFile(manifest)
	if err != nil {
		return 0
	}
	count := 0
	inPhases := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "phases:") {
			inPhases = true
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "workflows:") {
			break
		}
		if inPhases && strings.HasPrefix(strings.TrimSpace(line), "skill:") {
			count++
		}
	}
	return count
}
