package p

// Clamp is a public interface with no test of any dimension. Measured on its own, the
// package reports 0.0% statement coverage, which is below the HISS-15 floor of 65%.
func Clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
