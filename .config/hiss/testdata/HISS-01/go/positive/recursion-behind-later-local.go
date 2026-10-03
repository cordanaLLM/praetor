package p

// Direct recursion violates the axiom. The local named Drain is declared after the call, so the
// call reaches the function itself, and a shadow check that asked whether the name was declared
// anywhere in Drain let the recursion pass.
func Drain(n int) int {
	if n > 0 {
		return Drain(n - 1)
	}
	Drain := 0
	return Drain
}
