// Package textutil provides bounded string helpers for display text and
// for the fixed-size wire protocol.
//
// Contract (README 行为契约): Truncate is RUNE-safe for user-visible
// text; TruncateBytes is BYTE-budgeted for wire frames, where cutting
// mid-rune is in-contract and callers re-encode with
// strings.ToValidUTF8.
package textutil

// Truncate shortens s to at most maxRunes Unicode code points, appending
// an ellipsis ("…") when content was cut. maxRunes <= 0 yields "".
// Rune safety matters: callers pass CJK titles and names.
func Truncate(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	if maxRunes == 1 {
		return "…"
	}
	return string(r[:maxRunes-1]) + "…"
}

// TruncateBytes cuts s to at most maxBytes bytes for the fixed-size wire
// frame header. BYTE semantics are the contract: the shelf device
// hard-rejects over-length frames and callers re-encode the result with
// strings.ToValidUTF8. Do not "fix" this to runes — that changes frame
// sizes and breaks the device.
func TruncateBytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes]
}
