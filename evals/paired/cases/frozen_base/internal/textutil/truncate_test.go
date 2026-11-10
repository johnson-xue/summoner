package textutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncatePassthrough(t *testing.T) {
	for _, tc := range []struct {
		in   string
		max  int
		want string
	}{
		{"widget", 10, "widget"},
		{"ab", 2, "ab"},
		{"abc", 3, "abc"},
		{"abc", 2, "a…"},
		{"abc", 1, "…"},
		{"anything", 0, ""},
		{"anything", -3, ""},
	} {
		if got := Truncate(tc.in, tc.max); got != tc.want {
			t.Errorf("Truncate(%q,%d) = %q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
}

// TestTruncateCJKSafety pins the display contract: CJK text is cut on
// rune boundaries and never leaves a half character.
func TestTruncateCJKSafety(t *testing.T) {
	const s = "季度库存报告——华东区"                       // 11 runes
	if got := Truncate(s, 8); got != "季度库存报告—…" { // 7 runes + ellipsis
		t.Fatalf("Truncate(s,8) = %q, want %q", got, "季度库存报告—…")
	}
	if got := Truncate(s, 11); got != s {
		t.Fatalf("Truncate(s,11) = %q, want passthrough", got)
	}
}

// TestTruncateBytesContract pins the WIRE contract: byte budget is
// exact, mid-rune cuts are expected, and the caller-side re-encode
// (strings.ToValidUTF8) yields valid UTF-8.
func TestTruncateBytesContract(t *testing.T) {
	const s = "季度库存报告" // 15 bytes
	got := TruncateBytes(s, 10)
	if len(got) != 10 {
		t.Fatalf("len(TruncateBytes(s,10)) = %d, want 10", len(got))
	}
	if utf8.ValidString(got) {
		t.Fatalf("mid-rune cut expected at 10 bytes for CJK payload")
	}
	if fixed := strings.ToValidUTF8(got, "?"); !utf8.ValidString(fixed) {
		t.Fatalf("re-encode must produce valid UTF-8, got %q", fixed)
	}
	if got := TruncateBytes("plain", 40); got != "plain" {
		t.Fatalf("short input must pass through, got %q", got)
	}
}
