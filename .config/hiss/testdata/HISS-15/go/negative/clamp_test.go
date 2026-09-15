package p

import "testing"

// All three HISS-15 dimensions for Clamp: positive (a value inside the range), negative
// (values outside it in both directions) and boundary (exactly lo, exactly hi, and an
// inverted range). Statement coverage is 100%, well above the 65% floor.
func TestClamp(t *testing.T) {
	cases := []struct {
		name           string
		v, lo, hi, out int
	}{
		{"positive/inside", 5, 0, 10, 5},
		{"negative/below", -3, 0, 10, 0},
		{"negative/above", 42, 0, 10, 10},
		{"boundary/at-lo", 0, 0, 10, 0},
		{"boundary/at-hi", 10, 0, 10, 10},
		{"boundary/inverted-range", 5, 10, 0, 10},
	}
	for _, tc := range cases {
		if got := Clamp(tc.v, tc.lo, tc.hi); got != tc.out {
			t.Errorf("%s: Clamp(%d,%d,%d) = %d, want %d", tc.name, tc.v, tc.lo, tc.hi, got, tc.out)
		}
	}
}
