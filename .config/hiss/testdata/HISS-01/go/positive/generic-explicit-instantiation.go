package p

// Count calls itself with an explicit type argument. The generic instantiation must not
// hide the cycle.
func Count[T any](k int) int {
	if k <= 0 {
		return 0
	}
	return Count[T](k - 1)
}
