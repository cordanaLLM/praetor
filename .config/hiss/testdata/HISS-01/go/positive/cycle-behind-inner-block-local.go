package p

// The cycle Settle -> Retry -> Settle violates the axiom. The local named Retry lives only in
// the if block, so the later call reaches the function Retry, and a shadow check that asked
// whether the name was declared anywhere in Settle hid the cycle.
func Settle(n int) int {
	if n > 9 {
		Retry := n / 2
		_ = Retry
	}
	return Retry(n - 1)
}

func Retry(n int) int {
	if n <= 0 {
		return 0
	}
	return Settle(n)
}
