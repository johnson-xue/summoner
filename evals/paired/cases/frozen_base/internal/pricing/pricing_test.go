package pricing

import "testing"

func TestTotalNoDiscountBelowTenUnits(t *testing.T) {
	if got := Total([]Line{{PriceCents: 100, Qty: 9}}); got != 900 {
		t.Fatalf("Total(9 units) = %d, want 900", got)
	}
}

// TestTotalTierRoundsDown pins the integer math: 5% of 9990 cents is
// 499.5, floored to 499.
func TestTotalTierRoundsDown(t *testing.T) {
	if got := Total([]Line{{PriceCents: 999, Qty: 10}}); got != 9491 {
		t.Fatalf("Total(10x999) = %d, want 9491", got)
	}
}

func TestTotalHighestTierWins(t *testing.T) {
	if got := Total([]Line{{PriceCents: 1, Qty: 100}}); got != 88 {
		t.Fatalf("Total(100x1) = %d, want 88", got)
	}
}

func TestTotalMultiLine(t *testing.T) {
	got := Total([]Line{{PriceCents: 500, Qty: 6}, {PriceCents: 200, Qty: 6}})
	if got != 3990 { // 12 units → 5% off 4200 = 4200-210
		t.Fatalf("Total(multi) = %d, want 3990", got)
	}
}
