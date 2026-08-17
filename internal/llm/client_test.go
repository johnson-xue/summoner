package llm

import "testing"

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
