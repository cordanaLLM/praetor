package p

// Mutual recursion violates the axiom. The per-file scanner cannot see it, because no single
// file's AST shows the loop closing; the package call-graph pass reports it.
func IsEven(n int) bool {
	if n == 0 {
		return true
	}
	return IsOdd(n - 1)
}

func IsOdd(n int) bool {
	if n == 0 {
		return false
	}
	return IsEven(n - 1)
}
