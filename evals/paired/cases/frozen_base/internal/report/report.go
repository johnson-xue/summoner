// Package report renders inventory summaries: human-readable display
// lines and fixed-size wire frames for the legacy shelf device.
//
// Contract notes reviewers must respect (README 行为契约,
// REVIEW-CRITERIA.md):
//   - Display text is rune-safe (textutil.Truncate).
//   - Wire frames are BYTE-budgeted; mid-rune cuts are in-contract and
//     are re-encoded with strings.ToValidUTF8 here.
//   - Accounting stays int64 cents; the only floats are display-only
//     percentages (SharePct).
package report

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"inventoryd/internal/pricing"
	"inventoryd/internal/store"
	"inventoryd/internal/textutil"
)

// TitleMaxRunes bounds report titles for display.
const TitleMaxRunes = 24

// WireLineBytes is the byte budget of one shelf-device frame line. The
// device hard-rejects over-length frames.
const WireLineBytes = 40

// Summary is one rendered report.
type Summary struct {
	Title      string
	Display    []string
	Wire       []string
	TotalCents int64
}

// Build renders title, display lines, wire frames and the batch total.
func Build(title string, items []store.Item) Summary {
	sum := Summary{Title: textutil.Truncate(title, TitleMaxRunes)}
	var lines []pricing.Line
	for _, it := range items {
		lines = append(lines, pricing.Line{PriceCents: it.PriceCents, Qty: it.Stock})
		sum.Display = append(sum.Display, fmt.Sprintf("%-6s %-14s %5d", it.ID, it.Name, it.Stock))
		wire := fmt.Sprintf("%s|%s|%d", it.ID, it.Name, it.Stock)
		sum.Wire = append(sum.Wire, strings.ToValidUTF8(textutil.TruncateBytes(wire, WireLineBytes), "?"))
	}
	sum.TotalCents = pricing.Total(lines)
	return sum
}

// SharePct formats a stock share as a display percentage. Float is
// deliberate and safe here: display-only output, never fed back into
// money math — the accounting path stays int64 (REVIEW-CRITERIA.md C4
// scopes the no-float rule to accounting).
func SharePct(part, total int) string {
	if total <= 0 {
		return "0%"
	}
	return fmt.Sprintf("%.1f%%", float64(part)/float64(total)*100)
}

// Export writes text into a file named filename under dir and returns
// the written path. Filenames are accepted ONLY as plain base names —
// anything carrying a separator or ".." is rejected outright, never
// silently reduced (REVIEW-CRITERIA.md C1 — all file exports must go
// through this function).
func Export(dir, filename, text string) (string, error) {
	base := filepath.Base(filename)
	if filename != base || base == "." || base == ".." || strings.ContainsAny(base, `/\`) {
		return "", fmt.Errorf("report: unsafe filename %q", filename)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, base)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
