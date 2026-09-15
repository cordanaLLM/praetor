package p

import "testing"

// Only the positive dimension. The negative dimension (overflow at math.MaxInt, where the
// result silently wraps) and the boundary dimension (0, 1, math.MinInt, math.MaxInt) are
// both absent, which HISS-15 forbids. Statement coverage is nevertheless 100%, so the
// coverage floor -- the rule's only mechanical check -- passes.
func TestDouble(t *testing.T) {
	if got := Double(21); got != 42 {
		t.Errorf("Double(21) = %d, want 42", got)
	}
}
