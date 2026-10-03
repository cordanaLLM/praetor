package p

// No cycle: the local named Refill is declared before the call in an enclosing block, so the
// call inside the loop reaches the local, not the function Refill that calls back into Pump.
func Pump(n int) int {
	Refill := func(k int) int { return k }
	total := 0
	for i := 0; i < n; i++ {
		total += Refill(i)
	}
	return total
}

func Refill(n int) int {
	return Pump(n)
}
