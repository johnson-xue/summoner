package shared

import (
	"strings"
	"testing"
)

// display_test.go — the display binding of 0.7.0 D2: the Stop hook binds the
// checkpoint when the assistant's message CONTAINS the canonical block
// (the render minus its tail line), instead of demanding whole-message
// equality. The incident (2026-11-23) lost a 62-minute wait to equality: the
// model wrote a one-line explanation before a byte-perfect block and was
// blocked five times in a row.

// withTail re-ends a message with a different ╚ rule (the tail is the one
// line a paste may get wrong without changing what the user was shown).
func withTail(block string, rule int) string {
	return block + "╚" + strings.Repeat("═", rule) + "\n"
}

// TestDisplayContainmentAcceptsExplanationAndAnyTail: the block binds on its
// own, behind an explanation, before trailing prose, with a shorter or
// longer tail, with no tail at all, and across CRLF line endings.
func TestDisplayContainmentAcceptsExplanationAndAnyTail(t *testing.T) {
	run, cp := layoutRun()
	block := DisplayBlock(run, cp)
	canonical := RenderCheckpointMessage(run, cp)
	if got := strings.Count(canonical, "\n") - strings.Count(block, "\n"); got != 1 {
		t.Fatalf("DisplayBlock must be the render minus exactly its tail line, difference %d", got)
	}

	cases := map[string]string{
		"canonical message":           canonical,
		"explanation before":          "根因已定位：handle() 解引用前未判空。证据如下，请裁决。\n\n" + canonical,
		"explanation and a stop word": "复现步骤：先停止服务再启动，错误出现。\n\n" + canonical,
		"prose after":                 canonical + "（等待你的决定）",
		"shorter tail":                withTail(block, 3),
		"longer tail":                 withTail(block, 60),
		"no tail":                     block,
		"in a code fence":             "```\n" + canonical + "```",
		"the whole status view":       "⚡ SUMMONER STATUS — run run-1 · workflow fix · WAIT_USER\nroute: a → b\n" + CheckpointDetail(run, cp) + "\n" + canonical,
		"CRLF line endings":           strings.ReplaceAll("说明\r\n\r\n"+canonical, "\n", "\r\n"),
	}
	for name, msg := range cases {
		if !VerifyDisplayContainment(run, cp, msg) {
			t.Errorf("%s: the message carries the canonical block and must bind:\n%s", name, msg)
		}
	}
}

// TestDisplayContainmentRejectsEverythingElse: an explanation alone, a
// truncated block, an altered line, a render of ANOTHER checkpoint (stale
// nonce) and a hand-written frame never bind — only the real block does.
func TestDisplayContainmentRejectsEverythingElse(t *testing.T) {
	run, cp := layoutRun()
	block := DisplayBlock(run, cp)
	canonical := RenderCheckpointMessage(run, cp)

	// lines are the block's real lines (the trailing newline dropped), so
	// dropping the last one really removes the navigation-hint line.
	lines := strings.Split(strings.TrimSuffix(block, "\n"), "\n")
	if n := len(lines); n < 3 || !strings.Contains(lines[n-1], "nonce "+cp.Nonce) {
		t.Fatalf("fixture: the block's last line must be the nonce hint, got %q", lines[len(lines)-1])
	}
	truncated := strings.Join(lines[:len(lines)-1], "\n") // hint line missing
	altered := strings.Replace(block, cp.Conclusion.Summary, cp.Conclusion.Summary+"?", 1)

	stale := cp
	stale.Nonce = "0ldn0nc3"
	stale.ID = "cp-1"
	staleRender := RenderCheckpointMessage(run, stale)

	cases := map[string]string{
		"explanation only":        "根因已定位，请裁决。",
		"empty":                   "",
		"truncated block":         "说明\n\n" + truncated + "\n╚═══\n",
		"one altered line":        altered,
		"stale checkpoint":        staleRender,
		"hand-written frame":      "╔═ ⚡ SUMMONER CHECKPOINT " + cp.ID + " — nonce " + cp.Nonce + " — PHASE ══════════\n║ 结论: 大概修好了\n╚═══\n",
		"two halves out of order": strings.Join(append(lines[len(lines)/2:], lines[:len(lines)/2]...), "\n"),
	}
	for name, msg := range cases {
		if VerifyDisplayContainment(run, cp, msg) {
			t.Errorf("%s: must NOT bind:\n%s", name, msg)
		}
	}

	// Old and new block side by side: the CURRENT one binds — and the stale
	// one alone (above) does not.
	if !VerifyDisplayContainment(run, cp, staleRender+"\n"+canonical) {
		t.Fatal("a message carrying a stale block AND the current block must bind the current one")
	}
}
