package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"inventoryd/internal/store"
)

func TestBuildTitleRuneSafe(t *testing.T) {
	// 25 runes > TitleMaxRunes(24): cut after 23 runes, rune-safe.
	const title = "季度库存报告——华东区（2026年秋季盘点附录）终"
	sum := Build(title, []store.Item{{ID: "A", Name: "油壶", PriceCents: 100, Stock: 2}})
	if sum.Title != "季度库存报告——华东区（2026年秋季盘点附录…" {
		t.Fatalf("Title = %q, want rune-safe truncation", sum.Title)
	}
}

func TestBuildWireBudget(t *testing.T) {
	sum := Build("t", []store.Item{
		{ID: "A", Name: "short", PriceCents: 1, Stock: 1},
		{ID: "B", Name: strings.Repeat("季度库存报告", 10), PriceCents: 1, Stock: 2},
	})
	for i, line := range sum.Wire {
		if len(line) > WireLineBytes {
			t.Fatalf("wire[%d] len %d exceeds budget", i, len(line))
		}
		if !utf8.ValidString(line) {
			t.Fatalf("wire[%d] not valid UTF-8 after re-encode: %q", i, line)
		}
	}
	if !strings.Contains(sum.Wire[1], "?") {
		t.Fatalf("forced cut must re-encode the mid-rune tail, got %q", sum.Wire[1])
	}
}

func TestSharePctDisplayOnly(t *testing.T) {
	for _, tc := range []struct {
		part, total int
		want        string
	}{
		{1, 3, "33.3%"},
		{0, 5, "0.0%"},
		{7, 0, "0%"},
		{7, -2, "0%"},
	} {
		if got := SharePct(tc.part, tc.total); got != tc.want {
			t.Errorf("SharePct(%d,%d) = %q, want %q", tc.part, tc.total, got, tc.want)
		}
	}
}

// TestExportSanitizes pins C1: no path escape, base-name reduction only.
func TestExportSanitizes(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"../evil.txt", "a/b.txt", "..", "."} {
		if _, err := Export(dir, bad, "x"); err == nil {
			t.Fatalf("Export(%q) accepted", bad)
		}
	}
	p, err := Export(dir, "ok.txt", "hello")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "hello" {
		t.Fatalf("roundtrip = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.txt")); !os.IsNotExist(err) {
		t.Fatalf("file escaped the export dir")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("dir has %d entries, want 1", len(ents))
	}
}
