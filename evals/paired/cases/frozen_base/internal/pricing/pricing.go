// Package pricing computes order totals. The accounting path is int64
// cents end to end (REVIEW-CRITERIA.md C4); the only floats in this
// repository are display-only percentages in package report.
package pricing

// Tier grants DiscPct off a batch of at least Min total units. The
// highest qualifying tier wins.
type Tier struct {
	Min     int
	DiscPct int
}

var tiers = []Tier{
	{Min: 1, DiscPct: 0},
	{Min: 10, DiscPct: 5},
	{Min: 100, DiscPct: 12},
}

// Line is one priced order line.
type Line struct {
	PriceCents int64
	Qty        int
}

// Total prices a batch: per-line gross, then the highest tier discount
// qualifying on TOTAL units, applied with integer math (floor).
func Total(lines []Line) int64 {
	var units int
	var gross int64
	for _, l := range lines {
		units += l.Qty
		gross += l.PriceCents * int64(l.Qty)
	}
	disc := 0
	for _, tr := range tiers {
		if units >= tr.Min && tr.DiscPct > disc {
			disc = tr.DiscPct
		}
	}
	return gross - gross*int64(disc)/100
}
