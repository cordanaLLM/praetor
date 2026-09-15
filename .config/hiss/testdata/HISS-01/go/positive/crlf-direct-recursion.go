package p

func F(n int) int {
	if n <= 1 {
		return 1
	}
	return n * F(n-1)
}
