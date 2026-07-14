package cli

import (
	"fmt"
	"strings"

	"github.com/johnson-xue/summoner/internal/context"
)

// FormatPhase formats a phase for display
func FormatPhase(phase *context.Phase, showFull bool) string {
	var builder strings.Builder

	// Header
	builder.WriteString(fmt.Sprintf("Phase %d: %s — %s\n",
		phase.Sequence, phase.PhaseName, phase.SkillName))

	// Status
	statusIcon := "⏳"
	switch phase.Status {
	case "completed":
		statusIcon = "✓"
	case "failed":
		statusIcon = "✗"
	case "skipped":
		statusIcon = "⊘"
	}
	builder.WriteString(fmt.Sprintf("Status: %s %s\n", statusIcon, phase.Status))

	// Metadata
	editedMarker := ""
	if phase.SummaryEdited {
		editedMarker = " ✏️"
	}
	builder.WriteString(fmt.Sprintf("Summary Score: %d/5%s\n", phase.SummaryScore, editedMarker))

	if phase.TokenCost > 0 {
		builder.WriteString(fmt.Sprintf("Token Cost: %d\n", phase.TokenCost))
	}

	builder.WriteString(fmt.Sprintf("Output Size: %s (%d chunks)\n",
		formatSize(phase.FullOutputSize), phase.FullOutputChunks))

	// Timestamps
	builder.WriteString(fmt.Sprintf("Started: %s\n", phase.StartedAt.Format("2006-01-02 15:04:05")))
	if phase.CompletedAt != nil {
		builder.WriteString(fmt.Sprintf("Completed: %s\n", phase.CompletedAt.Format("2006-01-02 15:04:05")))
	}

	// Summary
	builder.WriteString("\nSummary:\n")
	summaryLines := strings.Split(phase.Summary, "\n")
	for _, line := range summaryLines {
		if strings.TrimSpace(line) != "" {
			builder.WriteString("  ")
			builder.WriteString(line)
			builder.WriteString("\n")
		}
	}

	return builder.String()
}

// FormatPhaseList formats a list of phases
func FormatPhaseList(phases []context.Phase) string {
	var builder strings.Builder

	builder.WriteString(fmt.Sprintf("Total phases: %d\n\n", len(phases)))

	for _, p := range phases {
		editedMarker := ""
		if p.SummaryEdited {
			editedMarker = " ✏️"
		}

		statusIcon := "⏳"
		switch p.Status {
		case "completed":
			statusIcon = "✓"
		case "failed":
			statusIcon = "✗"
		case "skipped":
			statusIcon = "⊘"
		}

		builder.WriteString(fmt.Sprintf("%s Phase %d: %s — %s%s\n",
			statusIcon, p.Sequence, p.PhaseName, p.SkillName, editedMarker))
		builder.WriteString(fmt.Sprintf("   Score: %d/5 | Size: %s",
			p.SummaryScore, formatSize(p.FullOutputSize)))

		if p.TokenCost > 0 {
			builder.WriteString(fmt.Sprintf(" | Tokens: %d", p.TokenCost))
		}
		builder.WriteString("\n")

		// First line of summary
		summaryLines := strings.Split(p.Summary, "\n")
		if len(summaryLines) > 0 && strings.TrimSpace(summaryLines[0]) != "" {
			builder.WriteString("   ")
			builder.WriteString(truncate(summaryLines[0], 80))
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
	}

	return builder.String()
}

// FormatContextBundle formats context bundle for display
func FormatContextBundle(bundle string, previewMode bool) string {
	if !previewMode {
		return bundle
	}

	// Show only first 500 chars for preview
	lines := strings.Split(bundle, "\n")
	if len(lines) <= 20 {
		return bundle
	}

	previewText := strings.Join(lines[:20], "\n")
	return fmt.Sprintf("%s\n\n... (%d more lines, use --full to see all)",
		previewText, len(lines)-20)
}

// FormatInterventions formats intervention history
func FormatInterventions(interventions []context.Intervention) string {
	var builder strings.Builder

	builder.WriteString(fmt.Sprintf("Total interventions: %d\n\n", len(interventions)))

	for i, intervention := range interventions {
		builder.WriteString(fmt.Sprintf("[%d] %s at %s\n",
			i+1, intervention.InterventionType, intervention.CreatedAt.Format("2006-01-02 15:04:05")))

		if intervention.Reason != "" {
			builder.WriteString(fmt.Sprintf("    Reason: %s\n", intervention.Reason))
		}

		if intervention.FieldName != "" {
			builder.WriteString(fmt.Sprintf("    Field: %s\n", intervention.FieldName))
		}

		// Show diff
		if intervention.BeforeValue != "" && intervention.AfterValue != "" {
			builder.WriteString("    Before:\n")
			beforeLines := strings.Split(intervention.BeforeValue, "\n")
			for _, line := range beforeLines[:min(3, len(beforeLines))] {
				builder.WriteString(fmt.Sprintf("      - %s\n", line))
			}
			if len(beforeLines) > 3 {
				builder.WriteString(fmt.Sprintf("      ... (%d more lines)\n", len(beforeLines)-3))
			}

			builder.WriteString("    After:\n")
			afterLines := strings.Split(intervention.AfterValue, "\n")
			for _, line := range afterLines[:min(3, len(afterLines))] {
				builder.WriteString(fmt.Sprintf("      + %s\n", line))
			}
			if len(afterLines) > 3 {
				builder.WriteString(fmt.Sprintf("      ... (%d more lines)\n", len(afterLines)-3))
			}
		}

		builder.WriteString("\n")
	}

	return builder.String()
}

// formatSize formats bytes to human readable size
func formatSize(bytes int) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// truncate truncates a string to max length
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
