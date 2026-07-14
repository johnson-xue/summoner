package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/johnson-xue/summoner/internal/cli"
	"github.com/johnson-xue/summoner/internal/context"
	"github.com/johnson-xue/summoner/internal/llm"
	"github.com/spf13/cobra"
)

var (
	projectName string
	jsonOutput  bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "summoner-ctx",
		Short: "Summoner context memory management tool",
		Long:  "Manage workflow context, view history, edit summaries, and build context bundles for Summoner workflows",
	}

	// Project flag is NOT persistent - each command that needs it will require it
	rootCmd.PersistentFlags().StringVarP(&projectName, "project", "p", "", "Project name")
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")

	// Add commands
	rootCmd.AddCommand(saveCmd())
	rootCmd.AddCommand(viewCmd())
	rootCmd.AddCommand(editCmd())
	rootCmd.AddCommand(listCmd())
	rootCmd.AddCommand(searchCmd())
	rootCmd.AddCommand(getContextBundleCmd())
	rootCmd.AddCommand(checkpointCmd())
	rootCmd.AddCommand(configCmd())
	rootCmd.AddCommand(maintenanceCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// saveCmd saves a phase output
func saveCmd() *cobra.Command {
	var (
		workflowID   string
		phaseName    string
		skillName    string
		inputFile    string
		projectGuide string
	)

	cmd := &cobra.Command{
		Use:   "save",
		Short: "Save phase output to context memory",
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, err := context.NewMemory(projectName)
			if err != nil {
				return err
			}
			defer mem.Close()

			// Read input file
			// Security: Validate input file path to prevent path traversal (S4)
			if err := validateInputPath(inputFile); err != nil {
				return fmt.Errorf("invalid input file: %w", err)
			}

			fullOutput, err := os.ReadFile(inputFile)
			if err != nil {
				return fmt.Errorf("read input file: %w", err)
			}

			// Save phase
			phaseID, err := mem.SavePhase(context.SavePhaseRequest{
				WorkflowID:   workflowID,
				PhaseName:    phaseName,
				SkillName:    skillName,
				FullOutput:   string(fullOutput),
				ProjectGuide: projectGuide,
			})
			if err != nil {
				return err
			}

			// Get saved phase
			phase, err := mem.GetPhase(phaseID)
			if err != nil {
				return err
			}

			if jsonOutput {
				output := map[string]interface{}{
					"success":  true,
					"phase_id": phaseID,
					"summary":  phase.Summary,
					"score":    phase.SummaryScore,
				}
				return json.NewEncoder(os.Stdout).Encode(output)
			}

			fmt.Printf("✓ Phase saved (ID: %d)\n", phaseID)
			fmt.Printf("  Summary score: %d/5\n", phase.SummaryScore)
			if phase.TokenCost > 0 {
				fmt.Printf("  Token cost: %d\n", phase.TokenCost)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&workflowID, "workflow", "w", "", "Workflow ID")
	cmd.Flags().StringVarP(&phaseName, "phase", "n", "", "Phase name")
	cmd.Flags().StringVarP(&skillName, "skill", "s", "", "Skill name")
	cmd.Flags().StringVarP(&inputFile, "input", "i", "", "Input file (phase output)")
	cmd.Flags().StringVarP(&projectGuide, "guide", "g", "", "Project-specific extraction guide")

	cmd.MarkFlagRequired("project")
	cmd.MarkFlagRequired("workflow")
	cmd.MarkFlagRequired("phase")
	cmd.MarkFlagRequired("skill")
	cmd.MarkFlagRequired("input")

	return cmd
}

// viewCmd views a phase
func viewCmd() *cobra.Command {
	var (
		phaseID int64
		full    bool
	)

	cmd := &cobra.Command{
		Use:   "view",
		Short: "View phase context",
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, err := context.NewMemory(projectName)
			if err != nil {
				return err
			}
			defer mem.Close()

			phase, err := mem.GetPhase(phaseID)
			if err != nil {
				return err
			}

			if jsonOutput {
				return json.NewEncoder(os.Stdout).Encode(phase)
			}

			fmt.Print(cli.FormatPhase(phase, full))

			if full {
				fullOutput, err := mem.GetFullOutput(phaseID)
				if err != nil {
					return err
				}
				fmt.Println("\n" + strings.Repeat("─", 50))
				fmt.Println("Complete Output:")
				fmt.Println(strings.Repeat("─", 50))
				fmt.Println(fullOutput)
			}

			return nil
		},
	}

	cmd.Flags().Int64VarP(&phaseID, "id", "d", 0, "Phase ID")
	cmd.Flags().BoolVarP(&full, "full", "f", false, "Show full output")
	cmd.MarkFlagRequired("id")

	return cmd
}

// editCmd edits a phase summary
func editCmd() *cobra.Command {
	var (
		phaseID int64
		reason  string
	)

	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Edit phase summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, err := context.NewMemory(projectName)
			if err != nil {
				return err
			}
			defer mem.Close()

			// Get current summary
			currentSummary, err := mem.GetSummary(phaseID)
			if err != nil {
				return err
			}

			fmt.Println("Current summary:")
			fmt.Println(strings.Repeat("─", 50))
			fmt.Println(currentSummary)
			fmt.Println(strings.Repeat("─", 50))
			fmt.Println("\nOpening editor...")

			// Open editor
			newSummary, err := editInEditor(currentSummary)
			if err != nil {
				return err
			}

			// Save
			if err := mem.EditSummary(phaseID, newSummary, reason); err != nil {
				return err
			}

			fmt.Println("✓ Summary updated")
			return nil
		},
	}

	cmd.Flags().Int64VarP(&phaseID, "id", "d", 0, "Phase ID")
	cmd.Flags().StringVarP(&reason, "reason", "r", "manual edit", "Edit reason")
	cmd.MarkFlagRequired("id")

	return cmd
}

// listCmd lists all phases in a workflow
func listCmd() *cobra.Command {
	var workflowID string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all phases in workflow",
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, err := context.NewMemory(projectName)
			if err != nil {
				return err
			}
			defer mem.Close()

			phases, err := mem.GetContextChain(workflowID, 0)
			if err != nil {
				return err
			}

			if jsonOutput {
				return json.NewEncoder(os.Stdout).Encode(phases)
			}

			fmt.Printf("Workflow: %s\n\n", workflowID)
			fmt.Print(cli.FormatPhaseList(phases))

			return nil
		},
	}

	cmd.Flags().StringVarP(&workflowID, "workflow", "w", "", "Workflow ID")
	cmd.MarkFlagRequired("workflow")

	return cmd
}

// searchCmd searches context by keyword
func searchCmd() *cobra.Command {
	var (
		keyword    string
		workflowID string
	)

	cmd := &cobra.Command{
		Use:   "search",
		Short: "Search context by keyword",
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, err := context.NewMemory(projectName)
			if err != nil {
				return err
			}
			defer mem.Close()

			results, err := mem.GetDB().SearchPhases(keyword, workflowID)
			if err != nil {
				return err
			}

			if jsonOutput {
				return json.NewEncoder(os.Stdout).Encode(results)
			}

			fmt.Printf("🔍 Found %d results for \"%s\"\n\n", len(results), keyword)

			for _, r := range results {
				fmt.Printf("Phase %d: %s — %s\n", r.Sequence, r.PhaseName, r.SkillName)
				fmt.Printf("  Score: %d/5 | Size: %s\n", r.SummaryScore, formatSize(r.FullOutputSize))

				// Show first line of summary
				lines := strings.Split(r.Summary, "\n")
				if len(lines) > 0 {
					fmt.Printf("  %s\n", truncate(lines[0], 80))
				}
				fmt.Println()
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&keyword, "keyword", "k", "", "Search keyword")
	cmd.Flags().StringVarP(&workflowID, "workflow", "w", "", "Limit to workflow (optional)")
	cmd.MarkFlagRequired("keyword")

	return cmd
}

// getContextBundleCmd builds context bundle for next phase
func getContextBundleCmd() *cobra.Command {
	var (
		workflowID  string
		upToSeq     int
		format      string
		previewMode bool
	)

	cmd := &cobra.Command{
		Use:   "get-context-bundle",
		Short: "Get context bundle for next phase",
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, err := context.NewMemory(projectName)
			if err != nil {
				return err
			}
			defer mem.Close()

			bundle, err := mem.BuildContextBundle(workflowID, upToSeq, format)
			if err != nil {
				return err
			}

			if format == "json" || !previewMode {
				fmt.Println(bundle)
			} else {
				fmt.Println(cli.FormatContextBundle(bundle, previewMode))
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&workflowID, "workflow", "w", "", "Workflow ID")
	cmd.Flags().IntVarP(&upToSeq, "up-to-sequence", "s", 0, "Up to sequence number (0 = all)")
	cmd.Flags().StringVarP(&format, "format", "f", "text", "Output format (text|json)")
	cmd.Flags().BoolVar(&previewMode, "preview", true, "Show preview (first 20 lines)")

	cmd.MarkFlagRequired("workflow")

	return cmd
}

// checkpointCmd shows interactive checkpoint
func checkpointCmd() *cobra.Command {
	var phaseID int64

	cmd := &cobra.Command{
		Use:   "checkpoint",
		Short: "Show interactive checkpoint for a phase",
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, err := context.NewMemory(projectName)
			if err != nil {
				return err
			}
			defer mem.Close()

			action, err := cli.ShowCheckpoint(mem, phaseID)
			if err != nil {
				return err
			}

			if jsonOutput {
				output := map[string]string{"action": action}
				return json.NewEncoder(os.Stdout).Encode(output)
			}

			fmt.Printf("\nAction: %s\n", action)
			return nil
		},
	}

	cmd.Flags().Int64VarP(&phaseID, "id", "d", 0, "Phase ID")
	cmd.MarkFlagRequired("id")

	return cmd
}

// configCmd manages LLM configuration
func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage LLM configuration",
	}

	cmd.AddCommand(configShowCmd())
	cmd.AddCommand(configTestCmd())

	return cmd
}

func configShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show current LLM configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := llm.NewClient()
			if err != nil {
				return err
			}

			fmt.Println(client.GetInfo())
			return nil
		},
	}
}

func configTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test",
		Short: "Test LLM connection",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := llm.NewClient()
			if err != nil {
				return fmt.Errorf("create client: %w", err)
			}

			fmt.Println("Testing LLM connection...")
			fmt.Println(client.GetInfo())
			fmt.Println()

			// Validate
			if err := client.Validate(); err != nil {
				return err
			}

			// Simple test
			result, err := client.Extract("Test output: successful", "")
			if err != nil {
				return fmt.Errorf("extraction failed: %w", err)
			}

			fmt.Println("✓ Connection successful")
			fmt.Printf("  Summary: %s\n", result.Summary)
			fmt.Printf("  Score: %d/5\n", result.Score)
			if result.TokenUsage != nil {
				fmt.Printf("  Tokens: %d\n", result.TokenUsage.Total)
			}

			return nil
		},
	}
}

// maintenanceCmd provides database maintenance operations
func maintenanceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "maintenance",
		Short: "Database maintenance operations",
	}

	cmd.AddCommand(cleanupCmd())
	cmd.AddCommand(vacuumCmd())
	cmd.AddCommand(statsCmd())

	return cmd
}

func cleanupCmd() *cobra.Command {
	var days int

	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Clean up old workflows",
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, err := context.NewMemory(projectName)
			if err != nil {
				return err
			}
			defer mem.Close()

			count, err := mem.GetDB().CleanupOldWorkflows(days)
			if err != nil {
				return err
			}

			fmt.Printf("✓ Deleted %d old workflows\n", count)

			// Vacuum
			if err := mem.GetDB().Vacuum(); err != nil {
				return err
			}

			fmt.Println("✓ Database vacuumed")
			return nil
		},
	}

	cmd.Flags().IntVarP(&days, "days", "d", 30, "Delete workflows older than N days")
	return cmd
}

func vacuumCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "vacuum",
		Short: "Compact database",
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, err := context.NewMemory(projectName)
			if err != nil {
				return err
			}
			defer mem.Close()

			if err := mem.GetDB().Vacuum(); err != nil {
				return err
			}

			fmt.Println("✓ Database vacuumed")
			return nil
		},
	}
}

func statsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Show database statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, err := context.NewMemory(projectName)
			if err != nil {
				return err
			}
			defer mem.Close()

			stats, err := mem.GetDB().GetStats()
			if err != nil {
				return err
			}

			if jsonOutput {
				return json.NewEncoder(os.Stdout).Encode(stats)
			}

			fmt.Println("Database Statistics:")
			fmt.Printf("  Workflows: %d\n", stats.TotalWorkflows)
			fmt.Printf("  Phases: %d\n", stats.TotalPhases)
			fmt.Printf("  Chunks: %d\n", stats.TotalChunks)
			fmt.Printf("  Size: %s\n", formatSize(int(stats.DatabaseSize)))
			if stats.OldestWorkflow != nil {
				fmt.Printf("  Oldest: %s\n", stats.OldestWorkflow.Format("2006-01-02"))
			}

			return nil
		},
	}
}

// Helper functions

func editInEditor(currentContent string) (string, error) {
	tmpFile, err := os.CreateTemp("", "summoner-edit-*.md")
	if err != nil {
		return "", err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.WriteString(currentContent); err != nil {
		return "", err
	}
	tmpFile.Close()

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim"
	}

	// Security: validate editor to prevent command injection
	if err := validateEditor(editor); err != nil {
		return "", fmt.Errorf("unsafe EDITOR value: %w", err)
	}

	cmd := exec.Command(editor, tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return "", err
	}

	newContent, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", err
	}

	return string(newContent), nil
}

// validateEditor validates the EDITOR environment variable to prevent command injection
func validateEditor(editor string) error {
	// List of known safe editors
	allowedEditors := []string{"vim", "vi", "nano", "emacs", "code", "subl", "nvim", "gedit", "kate"}

	editorBase := filepath.Base(editor)

	// Check if it's a known safe editor
	for _, allowed := range allowedEditors {
		if editorBase == allowed {
			return nil
		}
	}

	// If not in allowed list, must be an absolute path to be safe
	if !filepath.IsAbs(editor) {
		return fmt.Errorf("unknown editor '%s' must be specified as absolute path", editor)
	}

	// Verify the executable exists
	if _, err := os.Stat(editor); err != nil {
		return fmt.Errorf("editor executable not found: %w", err)
	}

	return nil
}

// validateInputPath validates the input file path to prevent path traversal
// Addresses S4: Path Traversal in File Operations [MEDIUM]
func validateInputPath(path string) error {
	// Clean and resolve path
	cleanPath := filepath.Clean(path)
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	// Check for traversal attempts
	if strings.Contains(cleanPath, "..") {
		return fmt.Errorf("path traversal detected: %s", path)
	}

	// Ensure file exists and is readable
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("cannot access file: %w", err)
	}

	// Prevent reading directories or devices
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", path)
	}

	// Size limit to prevent OOM (100MB)
	if info.Size() > 100*1024*1024 {
		return fmt.Errorf("file too large: %d bytes (max 100MB)", info.Size())
	}

	return nil
}

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

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
