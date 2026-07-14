package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/johnson-xue/summoner/internal/context"
)

// ShowCheckpoint displays an interactive checkpoint
func ShowCheckpoint(mem *context.Memory, phaseID int64) (string, error) {
	phase, err := mem.GetPhase(phaseID)
	if err != nil {
		return "", fmt.Errorf("get phase: %w", err)
	}

	for {
		// Clear screen and show checkpoint
		clearScreen()
		printCheckpoint(phase)

		// Read user choice
		choice := readUserInput("Your choice [continue/edit/view/skip]: ")

		switch strings.ToLower(strings.TrimSpace(choice)) {
		case "continue", "c", "":
			return "continue", nil

		case "edit", "e":
			if err := editSummaryInteractive(mem, phaseID); err != nil {
				fmt.Printf("\n❌ Edit failed: %v\n", err)
				pressEnterToContinue()
				continue
			}
			// Reload phase to show updated summary
			phase, _ = mem.GetPhase(phaseID)
			fmt.Println("\n✓ Summary updated")
			pressEnterToContinue()

		case "view", "v":
			if err := viewFullOutput(mem, phaseID); err != nil {
				fmt.Printf("\n❌ View failed: %v\n", err)
			}
			pressEnterToContinue()

		case "skip", "s":
			return "skip", nil

		case "stop":
			return "stop", nil

		default:
			fmt.Printf("\n❌ Invalid choice: %s\n", choice)
			pressEnterToContinue()
		}
	}
}

// printCheckpoint prints the checkpoint UI
func printCheckpoint(phase *context.Phase) {
	fmt.Println("┌────────────────────────────────────────────────────┐")
	fmt.Printf("│ ✓ Phase %d (%s) — %s\n", phase.Sequence, phase.PhaseName, phase.SkillName)
	fmt.Println("│")

	// Score indicator
	scoreBar := strings.Repeat("█", phase.SummaryScore) + strings.Repeat("░", 5-phase.SummaryScore)
	editedMarker := ""
	if phase.SummaryEdited {
		editedMarker = " ✏️"
	}
	fmt.Printf("│ 📋 摘要质量: [%s] %d/5%s\n", scoreBar, phase.SummaryScore, editedMarker)
	fmt.Println("│")

	// Summary
	summaryLines := strings.Split(phase.Summary, "\n")
	lineCount := 0
	for _, line := range summaryLines {
		if strings.TrimSpace(line) != "" && lineCount < 15 {
			fmt.Printf("│   %s\n", line)
			lineCount++
		}
	}

	if lineCount >= 15 {
		remainingLines := len(summaryLines) - 15
		fmt.Printf("│   ... (%d more lines)\n", remainingLines)
	}

	fmt.Println("│")
	fmt.Printf("│ 📂 完整输出: %s (%d chunks)\n", formatSize(phase.FullOutputSize), phase.FullOutputChunks)
	if phase.TokenCost > 0 {
		fmt.Printf("│ 💰 Token 消耗: %d\n", phase.TokenCost)
	}
	fmt.Println("│")
	fmt.Println("├────────────────────────────────────────────────────┤")
	fmt.Println("│ [continue]  继续下一阶段                            │")
	fmt.Println("│ [edit]      编辑摘要（补充或修正关键信息）           │")
	fmt.Println("│ [view]      查看完整输出                            │")
	fmt.Println("│ [skip]      跳过下一阶段                            │")
	fmt.Println("│ [stop]      停止 workflow                           │")
	fmt.Println("└────────────────────────────────────────────────────┘")
	fmt.Println()
}

// editSummaryInteractive opens editor for summary editing
func editSummaryInteractive(mem *context.Memory, phaseID int64) error {
	// Get current summary
	currentSummary, err := mem.GetSummary(phaseID)
	if err != nil {
		return fmt.Errorf("get current summary: %w", err)
	}

	// Show edit mode selection
	fmt.Println("\n编辑方式:")
	fmt.Println("[1] 在编辑器中编辑（推荐，适合大改）")
	fmt.Println("[2] 在终端输入新摘要（适合小改）")
	fmt.Println("[3] 追加信息（在现有摘要后补充）")
	fmt.Println("[4] 取消")
	fmt.Println()

	mode := readUserInput("选择 [1-4]: ")

	var newSummary string
	var err2 error

	switch strings.TrimSpace(mode) {
	case "1", "":
		newSummary, err2 = editInEditor(currentSummary)
	case "2":
		newSummary, err2 = editInTerminal(currentSummary)
	case "3":
		newSummary, err2 = appendInfo(currentSummary)
	case "4":
		return nil
	default:
		return fmt.Errorf("invalid mode: %s", mode)
	}

	if err2 != nil {
		return err2
	}

	// Confirm changes
	fmt.Println("\n修改预览:")
	fmt.Printf("原摘要: %d 字符\n", len(currentSummary))
	fmt.Printf("新摘要: %d 字符\n", len(newSummary))
	fmt.Println()

	confirm := readUserInput("确认修改? [y/N]: ")
	if strings.ToLower(strings.TrimSpace(confirm)) != "y" {
		return fmt.Errorf("cancelled by user")
	}

	// Get reason
	reason := readUserInput("修改原因（可选）: ")
	if reason == "" {
		reason = "manual edit"
	}

	// Save changes
	return mem.EditSummary(phaseID, newSummary, reason)
}

// editInEditor opens user's preferred editor
func editInEditor(currentContent string) (string, error) {
	// Create temp file
	tmpFile, err := os.CreateTemp("", "summoner-edit-*.md")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	// Write current content
	if _, err := tmpFile.WriteString(currentContent); err != nil {
		return "", fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()

	// Get editor
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim" // fallback
	}

	fmt.Printf("\n打开编辑器: %s %s\n", editor, tmpPath)
	fmt.Println("编辑完成后保存并退出...")

	// Launch editor
	cmd := exec.Command(editor, tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("editor failed: %w", err)
	}

	// Read modified content
	newContent, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", fmt.Errorf("read modified file: %w", err)
	}

	return string(newContent), nil
}

// editInTerminal prompts user to enter new summary in terminal
func editInTerminal(currentContent string) (string, error) {
	fmt.Println("\n当前摘要:")
	fmt.Println("─────────────────────────────────────")
	fmt.Println(currentContent)
	fmt.Println("─────────────────────────────────────")
	fmt.Println("\n输入新摘要（输入 END 结束）:")

	var lines []string
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "END" {
			break
		}
		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}

	return strings.Join(lines, "\n"), nil
}

// appendInfo appends additional information to current summary
func appendInfo(currentContent string) (string, error) {
	fmt.Println("\n输入要追加的信息（输入 END 结束）:")

	var lines []string
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "END" {
			break
		}
		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}

	additional := strings.Join(lines, "\n")
	return fmt.Sprintf("%s\n\n[用户补充]:\n%s", currentContent, additional), nil
}

// viewFullOutput displays the full phase output
func viewFullOutput(mem *context.Memory, phaseID int64) error {
	fmt.Println("\n正在加载完整输出...")

	fullOutput, err := mem.GetFullOutput(phaseID)
	if err != nil {
		return fmt.Errorf("get full output: %w", err)
	}

	clearScreen()
	fmt.Println("┌────────────────────────────────────────────────────┐")
	fmt.Printf("│ Phase %d - 完整输出 (%s)\n", phaseID, formatSize(len(fullOutput)))
	fmt.Println("└────────────────────────────────────────────────────┘")
	fmt.Println()
	fmt.Println(fullOutput)
	fmt.Println()

	return nil
}

// readUserInput reads a line from stdin
func readUserInput(prompt string) string {
	fmt.Print(prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return scanner.Text()
	}
	return ""
}

// clearScreen clears the terminal screen
func clearScreen() {
	fmt.Print("\033[H\033[2J")
}

// pressEnterToContinue waits for user to press Enter
func pressEnterToContinue() {
	fmt.Print("\nPress Enter to continue...")
	bufio.NewReader(os.Stdin).ReadBytes('\n')
}
