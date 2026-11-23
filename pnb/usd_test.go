package pnb

import (
	"math"
	"testing"
)

// price × size past int64: the depth of a long event market (2026-11-22, 38 of 1,412 tokens went
// negative on the 11-21 19:00 hour).
func TestUSDIsExactPastInt64(t *testing.T) {
	for _, c := range []struct{ price, size, want int64 }{
		{420_000_000, 120_500_000, 50_610_000},                        // 0.42 × 120.5 = $50.61
		{999_000_000, 20_000_000 * SizeScale, 19_980_000 * SizeScale}, // 0.999 × 20M shares, int64 product overflows
		{1_000_000, 900_000_000 * SizeScale, 900_000 * SizeScale},     // 0.001 × 900M shares
		{0, 5, 0}, {-1, 5, 0}, {5, -1, 0},
	} {
		if got := USD(c.price, c.size); got != c.want {
			t.Fatalf("USD(%d, %d) = %d, want %d", c.price, c.size, got, c.want)
		}
	}
	if USD(math.MaxInt64, math.MaxInt64) != math.MaxInt64 {
		t.Fatal("beyond any book: saturate")
	}
}
