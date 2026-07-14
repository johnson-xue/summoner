package context

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/johnson-xue/summoner/internal/llm"
)

// extractSummary extracts summary from full output using LLM
// FIX: Multi-level fallback strategy for better quality
func (m *Memory) extractSummary(fullOutput, projectGuide string) (*llm.ExtractionResult, error) {
	// Level 1: Try primary LLM client
	if m.llmClient != nil {
		result, err := m.llmClient.Extract(fullOutput, projectGuide)
		if err == nil {
			return result, nil
		}
		log.Printf("Primary LLM extraction failed: %v", err)
	}

	// Level 2: Could try backup provider here (future enhancement)
	// if m.backupClient != nil { ... }

	// Level 3: Smart fallback with keyword extraction
	return nil, fmt.Errorf("LLM extraction failed, will use fallback")
}

// fallbackSummary generates a fallback summary when LLM is unavailable
// FIX: Multi-level fallback for better quality
func fallbackSummary(fullOutput string) string {
	// Level 1: Try smart extraction (keyword-based)
	if smart := smartFallback(fullOutput); smart != "" {
		return fmt.Sprintf("[降级摘要 - 关键词提取]\n\n%s\n\n⚠️ 建议：使用 'summoner-ctx edit --id <phase_id>' 手动补充完整信息", smart)
	}

	// Level 2: Simple head + tail extraction
	lines := strings.Split(fullOutput, "\n")

	if len(lines) <= 20 {
		return fmt.Sprintf("[降级摘要 - LLM 不可用]\n\n%s\n\n⚠️ 建议：使用 'summoner-ctx edit --id <phase_id>' 手动补充关键信息", fullOutput)
	}

	head := strings.Join(lines[:15], "\n")
	tail := strings.Join(lines[len(lines)-5:], "\n")

	return fmt.Sprintf(`[降级摘要 - LLM 不可用]

前 15 行：
%s

...（省略 %d 行）...

最后 5 行：
%s

⚠️ 建议：使用 'summoner-ctx edit --id <phase_id>' 手动补充关键信息`,
		head, len(lines)-20, tail)
}

// smartFallback extracts important lines using keywords
// FIX: Improved fallback quality
func smartFallback(fullOutput string) string {
	// Common conclusive keywords
	keywords := []struct {
		pattern string
		weight  int
	}{
		{"error:", 10},
		{"failed:", 10},
		{"root cause:", 15},
		{"根因:", 15},
		{"结论:", 15},
		{"summary:", 12},
		{"摘要:", 12},
		{"✓", 8},
		{"✗", 8},
		{"warning:", 7},
		{"警告:", 7},
		{"success:", 8},
		{"成功:", 8},
		{"result:", 9},
		{"结果:", 9},
	}

	type scoredLine struct {
		line  string
		score int
		index int
	}

	var scored []scoredLine
	lines := strings.Split(fullOutput, "\n")

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) < 10 { // Skip very short lines
			continue
		}

		score := 0
		lowerLine := strings.ToLower(line)

		for _, kw := range keywords {
			if strings.Contains(lowerLine, kw.pattern) {
				score += kw.weight
			}
		}

		if score > 0 {
			scored = append(scored, scoredLine{line, score, i})
		}
	}

	// Sort by score (descending)
	for i := 0; i < len(scored); i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[j].score > scored[i].score {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}

	// Take top 10 lines
	maxLines := 10
	if len(scored) > maxLines {
		scored = scored[:maxLines]
	}

	// Sort by original index to maintain order
	for i := 0; i < len(scored); i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[j].index < scored[i].index {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}

	if len(scored) == 0 {
		return ""
	}

	var result strings.Builder
	for _, s := range scored {
		result.WriteString("- ")
		result.WriteString(s.line)
		result.WriteString("\n")
	}

	return result.String()
}

// BuildContextBundle formats context chain for transmission
// FIX: Supports both text and JSON formats
func (m *Memory) BuildContextBundle(workflowID string, upToSequence int, format string) (string, error) {
	phases, err := m.GetContextChain(workflowID, upToSequence)
	if err != nil {
		return "", fmt.Errorf("get context chain: %w", err)
	}

	if len(phases) == 0 {
		return "", nil
	}

	switch format {
	case "json":
		return formatContextJSON(phases)
	case "text", "":
		return formatContextText(phases)
	default:
		return "", fmt.Errorf("unknown format: %s (use 'text' or 'json')", format)
	}
}

// formatContextText formats context as human-readable text
func formatContextText(phases []Phase) (string, error) {
	var builder strings.Builder

	builder.WriteString("[前序阶段关键信息]:\n")

	for _, p := range phases {
		editedMarker := ""
		if p.SummaryEdited {
			editedMarker = " ✏️"
		}

		builder.WriteString(fmt.Sprintf("\n▸ Phase %d: %s (%s)%s\n",
			p.Sequence, p.PhaseName, p.SkillName, editedMarker))
		builder.WriteString(fmt.Sprintf("  质量评分: %d/5", p.SummaryScore))
		if p.TokenCost > 0 {
			builder.WriteString(fmt.Sprintf(" | Token 消耗: %d", p.TokenCost))
		}
		builder.WriteString("\n")

		// Indent summary
		summaryLines := strings.Split(p.Summary, "\n")
		for _, line := range summaryLines {
			if strings.TrimSpace(line) != "" {
				builder.WriteString("  ")
				builder.WriteString(line)
				builder.WriteString("\n")
			}
		}
	}

	return builder.String(), nil
}

// formatContextJSON formats context as JSON
func formatContextJSON(phases []Phase) (string, error) {
	type JSONPhase struct {
		Sequence      int    `json:"sequence"`
		PhaseName     string `json:"phase_name"`
		SkillName     string `json:"skill_name"`
		Summary       string `json:"summary"`
		Score         int    `json:"score"`
		Edited        bool   `json:"edited"`
		TokenCost     int    `json:"token_cost,omitempty"`
		FullOutputSize int   `json:"full_output_size"`
	}

	result := make([]JSONPhase, len(phases))
	for i, p := range phases {
		result[i] = JSONPhase{
			Sequence:      p.Sequence,
			PhaseName:     p.PhaseName,
			SkillName:     p.SkillName,
			Summary:       p.Summary,
			Score:         p.SummaryScore,
			Edited:        p.SummaryEdited,
			TokenCost:     p.TokenCost,
			FullOutputSize: p.FullOutputSize,
		}
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal json: %w", err)
	}

	return string(data), nil
}

// GetInterventions retrieves all interventions for a phase
func (m *Memory) GetInterventions(phaseID int64) ([]Intervention, error) {
	rows, err := m.db.Query(`
		SELECT id, phase_id, intervention_type, field_name,
		       before_value, after_value, reason, created_at
		FROM interventions
		WHERE phase_id = ?
		ORDER BY created_at DESC
	`, phaseID)
	if err != nil {
		return nil, fmt.Errorf("query interventions: %w", err)
	}
	defer rows.Close()

	var interventions []Intervention
	for rows.Next() {
		var i Intervention
		err := rows.Scan(&i.ID, &i.PhaseID, &i.InterventionType, &i.FieldName,
			&i.BeforeValue, &i.AfterValue, &i.Reason, &i.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan intervention: %w", err)
		}
		interventions = append(interventions, i)
	}

	return interventions, rows.Err()
}
