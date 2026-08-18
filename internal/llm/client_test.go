package llm

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestParseExtractionResponse_ClampsOutOfRangeScore proves A2: an LLM that
// ignores the "1-5" instruction and returns [SCORE: 9] or [SCORE: 99] must be
// clamped, never passed through. The unclamped value made
// checkpoint.go's strings.Repeat("░", 5-score) panic on a negative count.
func TestParseExtractionResponse_ClampsOutOfRangeScore(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantSc  int
		wantFB  bool
	}{
		{"valid 3", "[SCORE: 3]\n- finding a", 3, false},
		{"valid 5 max", "[SCORE: 5]\n- finding b", 5, false},
		{"valid 0 min", "[SCORE: 0]\n- finding c", 0, false},
		{"over 5 clamps to 5", "[SCORE: 9]\n- finding d", 5, true},
		{"way over clamps to 5", "[SCORE: 99]\n- finding e", 5, true},
		{"negative clamps to 0", "[SCORE: -3]\n- finding f", 0, true},
		{"unparseable score defaults+fallback", "[SCORE: abc]\n- finding g", 3, true},
		{"no score line defaults+fallback", "- finding h", 3, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			summary, score, fallback, err := parseExtractionResponse(c.content)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if score != c.wantSc {
				t.Errorf("score = %d, want %d", score, c.wantSc)
			}
			if fallback != c.wantFB {
				t.Errorf("fallback = %v, want %v", fallback, c.wantFB)
			}
			if summary == "" {
				t.Error("summary empty")
			}
		})
	}
}

// TestParseExtractionResponse_ClampGuaranteesSafeRender is the exact panic
// reproduction: before the clamp, score=9 → strings.Repeat("░", 5-9=-4)
// panicked. After the clamp, the same render arithmetic is always ≥0.
func TestParseExtractionResponse_ClampGuaranteesSafeRender(t *testing.T) {
	_, score, _, err := parseExtractionResponse("[SCORE: 9]\ncontent")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Simulate checkpoint.go:70 render arithmetic.
	bar := repeat("█", score) + repeat("░", 5-score)
	if bar != "█████" {
		t.Errorf("clamped render bar = %q, want %q (5 filled, 0 empty)", bar, "█████")
	}
}

// repeat is a local mirror of strings.Repeat to keep this test hermetic and
// document the panic surface: strings.Repeat(x, n) panics if n < 0.
func repeat(s string, n int) string {
	if n < 0 {
		n = 0
	}
	out := make([]byte, 0, n*len(s))
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

// TestTruncateValidUTF8 reproduces the B5 bug surface: a CJK-heavy output
// whose byte length exceeds 20000. The OLD code did fullOutput[:20000],
// splitting a 3-byte CJK char and producing invalid UTF-8. The fix slices
// on a rune boundary. We can't call Extract() without an HTTP server, so we
// exercise the exact same truncation logic on the same input and assert the
// invariants the fix guarantees: valid UTF-8, no split multi-byte sequence,
// rune count ≤ 20000, truncation marker present.
func TestTruncateValidUTF8(t *testing.T) {
	// 20001 bytes of CJK: 20001/3 = 6667 chars exactly, +1 byte over.
	// Build 6667 世 chars (= 20001 bytes) then 1 ASCII byte → 20002 bytes total,
	// byte index 20000 lands mid-char (byte 1 of the 6668th 世).
	const cjk = "世" // 3 bytes: 0xe4 0xb8 0x96
	var sb strings.Builder
	sb.Grow(20002)
	for i := 0; i < 6667; i++ {
		sb.WriteString(cjk)
	}
	sb.WriteByte('X') // now 20002 bytes; byte[20000] is mid-世
	input := sb.String()

	// Mirror the Extract() truncation block: cap at a byte budget on a rune
	// boundary. This input (20002 bytes, 6668 runes) is INSIDE the S8 gap
	// window (20001–60000 bytes ↔ 6667–20000 runes) where the old
	// byte-guard/rune-cap mismatch appended the marker WITHOUT truncating.
	maxInputLen := 20000
	var got string
	if len(input) > maxInputLen {
		got = truncateOnRuneBoundary(input, maxInputLen) + "\n\n[... 输出过长，已截断 ...]"
	}

	if !utf8.ValidString(got) {
		t.Fatal("truncated output is not valid UTF-8 — CJK char was split at byte boundary")
	}
	if !strings.Contains(got, "[... 输出过长，已截断 ...]") {
		t.Error("truncation marker missing")
	}
	body := strings.TrimSuffix(got, "\n\n[... 输出过长，已截断 ...]")
	// S8 fix assertion: ACTUAL truncation occurred — body byte length must be
	// ≤ maxInputLen AND strictly less than the input. The old code produced a
	// body of 20002 bytes (the full input) + marker (20037 total) — longer
	// than input, falsely labelled "已截断".
	if len(body) > maxInputLen {
		t.Errorf("S8 gap: body byte length %d > limit %d — no actual truncation occurred (the byte-guard/rune-cap mismatch)", len(body), maxInputLen)
	}
	if len(body) >= len(input) {
		t.Errorf("S8 gap: body (%d bytes) not shorter than input (%d) — truncation was a no-op", len(body), len(input))
	}
	// The rune count of the body is ≤ the limit (and ≤ 6667 for this input).
	if rc := utf8.RuneCountInString(body); rc > maxInputLen {
		t.Errorf("rune count %d > limit %d", rc, maxInputLen)
	}
	// Prove the bug surface: a raw byte slice at 20000 is invalid UTF-8.
	rawBad := input[:maxInputLen]
	if utf8.ValidString(rawBad) {
		t.Log("note: byte[20000] happened to be a boundary for this input (rare); bug still real for other CJK lengths")
	} else {
		t.Log("confirmed: raw byte slice at 20000 is invalid UTF-8 (the bug surface); rune-boundary fix avoids it")
	}
}

// TestTruncateOnRuneBoundary_DenseCJK (S8 fix): pure-CJK input at 30000 bytes
// (10000 runes) — squarely inside the old gap window. The old code entered the
// branch (30000 > 20000 bytes) but the rune cap (10000 > 20000) did NOT fire,
// so it appended the marker to the full 30000 bytes. The fix must truncate to
// ≤20000 bytes (6666 世 = 19998 bytes), strictly shorter than input.
func TestTruncateOnRuneBoundary_DenseCJK(t *testing.T) {
	const cjk = "世" // 3 bytes
	var sb strings.Builder
	for i := 0; i < 10000; i++ {
		sb.WriteString(cjk)
	}
	input := sb.String() // 30000 bytes, 10000 runes
	if len(input) != 30000 {
		t.Fatalf("setup: expected 30000 bytes, got %d", len(input))
	}
	got := truncateOnRuneBoundary(input, 20000)
	if !utf8.ValidString(got) {
		t.Fatal("not valid UTF-8")
	}
	if len(got) > 20000 {
		t.Errorf("got %d bytes > 20000 budget — byte budget not enforced", len(got))
	}
	if len(got) >= len(input) {
		t.Errorf("got %d bytes, not shorter than input %d — truncation was a no-op (the S8 gap)", len(got), len(input))
	}
	// Must end on a rune boundary: the last 3 bytes form a complete 世.
	if utf8.RuneCountInString(got) != len(got)/3 {
		t.Errorf("output not rune-aligned: %d runes over %d bytes", utf8.RuneCountInString(got), len(got))
	}
}
