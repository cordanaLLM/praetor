package p

// Real HISS-04 debt: McCabe cyclomatic complexity 12, past both the declared cap of 10 and
// the linter's effective threshold, so golangci-lint reports it. It stays under the
// scanner's 60-LOC rule, so no scanner violation exists, nothing enters the baseline and
// V_total does not move. The ratchet cannot ratchet what it does not count.
func Grade(n int) string {
	if n < 0 {
		return "negative"
	}
	if n == 0 {
		return "zero"
	}
	if n < 10 {
		return "ones"
	}
	if n < 100 {
		return "tens"
	}
	if n < 1000 {
		return "hundreds"
	}
	if n < 10000 {
		return "thousands"
	}
	if n < 100000 {
		return "ten-thousands"
	}
	if n < 1000000 {
		return "hundred-thousands"
	}
	if n < 10000000 {
		return "millions"
	}
	if n < 100000000 {
		return "ten-millions"
	}
	if n%2 == 0 {
		return "big even"
	}
	return "big odd"
}
