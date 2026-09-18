package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// marketplaceRoot mimics the longest realistic plugin root:
// ~/.claude/plugins/cache/summoner-marketplace/summoner/0.2.0
const marketplaceRoot = "/home/some-user-with-a-long-name/.claude/plugins/cache/summoner-marketplace/summoner/0.2.0"

func writeManifest(t *testing.T, dir, name string, phases int) {
	t.Helper()
	var b strings.Builder
	b.WriteString("version: \"1\"\nproject:\n  name: " + name + "\nphases:\n")
	for i := 0; i < phases; i++ {
		b.WriteString("  phase" + string(rune('a'+i)) + ":\n    skill: none\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "summoner.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setenvS(t *testing.T, key, val string) func() {
	t.Helper()
	old, had := os.LookupEnv(key)
	os.Setenv(key, val)
	return func() {
		if had {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	}
}

func TestRenderContextUninitialized(t *testing.T) {
	dir := t.TempDir()
	got := renderContext(marketplaceRoot, dir)
	if !strings.Contains(got, "not initialized") {
		t.Errorf("uninitialized branch missing warning: %q", got)
	}
	if !strings.Contains(got, "summoner-setup.sh") {
		t.Errorf("uninitialized branch missing setup pointer: %q", got)
	}
}

func TestRenderContextInitialized(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "demo-project", 3)
	got := renderContext(marketplaceRoot, dir)
	for _, want := range []string{"Summoner project: demo-project", "3 phases", "skills/summoner/SKILL.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("initialized branch missing %q in: %q", want, got)
		}
	}
}

func TestRenderContextPathTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "summoner.yaml"),
		[]byte("version: \"1\"\nproject:\n  name: ../../etc\nphases:\n  debug:\n    skill: none\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := renderContext(marketplaceRoot, dir)
	if strings.Contains(got, "../../etc") {
		t.Errorf("path traversal leaked into context: %q", got)
	}
	if !strings.Contains(got, "project: unknown") {
		t.Errorf("traversal should fall back to unknown: %q", got)
	}
}

// TestRenderContextByteBudget — the M-C1/M-C2 invariant: the whole injected
// block stays within 300 bytes in the worst case (marketplace-length root,
// no memory DB, many phases, AND an extreme project name — audit found
// names >~46 chars pushed past 300 before display truncation).
func TestRenderContextByteBudget(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "a-very-long-project-name-here", 16)
	if got := renderContext(marketplaceRoot, dir); len(got) > 300 {
		t.Errorf("initialized block = %d bytes (budget 300):\n%s", len(got), got)
	}
	long := t.TempDir()
	writeManifest(t, long, strings.Repeat("very-long-project-name-", 4)+"x", 16)
	if got := renderContext(marketplaceRoot, long); len(got) > 300 {
		t.Errorf("initialized block (extreme name) = %d bytes (budget 300):\n%s", len(got), got)
	}
	dir2 := t.TempDir()
	if got := renderContext(marketplaceRoot, dir2); len(got) > 300 {
		t.Errorf("uninitialized block = %d bytes (budget 300):\n%s", len(got), got)
	}
}

func TestRenderContextDottedNamePreserved(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "my.project", 2)
	got := renderContext(marketplaceRoot, dir)
	if !strings.Contains(got, "project: my.project") {
		t.Errorf("dotted project name should be preserved, got: %q", got)
	}
}

func TestCountPhasesInlineValues(t *testing.T) {
	dir := t.TempDir()
	yaml := "version: \"1\"\nproject:\n  name: inline\nphases:\n  debug: none\n  ops: my-ops\n"
	if err := os.WriteFile(filepath.Join(dir, "summoner.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := countPhases(filepath.Join(dir, "summoner.yaml")); got != 2 {
		t.Errorf("countPhases(inline values) = %d, want 2", got)
	}
}

func TestRenderContextMemoryLocations(t *testing.T) {
	proj := t.TempDir()
	writeManifest(t, proj, "memtest", 1)
	mem := t.TempDir()

	// (a) canonical DB present → pattern count shown
	db := filepath.Join(mem, "memtest.db")
	os.WriteFile(db, []byte{}, 0o644)
	restore := setenvS(t, "SUMMONER_MEMORY_DIR", mem)
	got := renderContext(marketplaceRoot, proj)
	if !strings.Contains(got, "memory: ? patterns)") { // "?" = sqlite3 can't read empty file
		t.Errorf("(a) canonical DB: %q", got)
	}
	restore()

	// (b) canonical absent + legacy present → migration hint
	plug := t.TempDir()
	os.MkdirAll(filepath.Join(plug, "memory"), 0755)
	os.WriteFile(filepath.Join(plug, "memory", "memtest.db"), []byte{}, 0o644)
	restore = setenvS(t, "SUMMONER_MEMORY_DIR", filepath.Join(t.TempDir(), "empty"))
	got = renderContext(plug, proj)
	if !strings.Contains(got, "legacy DB found") {
		t.Errorf("(b) legacy hint: %q", got)
	}
	restore()

	// (c) both absent → init hint (clean plugin root — no legacy file)
	plugClean := t.TempDir()
	restore = setenvS(t, "SUMMONER_MEMORY_DIR", filepath.Join(t.TempDir(), "empty"))
	got = renderContext(plugClean, proj)
	if !strings.Contains(got, "DB not initialized") {
		t.Errorf("(c) init hint: %q", got)
	}
	restore()
}
