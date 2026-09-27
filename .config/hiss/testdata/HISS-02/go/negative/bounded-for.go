package p

// A counted loop has a scalar upper bound.
func Sum(values []int) int {
	total := 0
	for i := 0; i < len(values); i++ {
		total += values[i]
	}
	return total
}
