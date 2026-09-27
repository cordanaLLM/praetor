package p

// A for statement without a condition has no scalar upper bound.
func Drain(next func() bool) {
	for {
		if !next() {
			return
		}
	}
}
