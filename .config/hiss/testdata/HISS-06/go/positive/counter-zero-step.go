package p

// process demonstrates a counter loop with a zero step, which should be reported.
func process(int) {}

// CounterZeroStep never advances the loop variable toward the constant upper bound.
func CounterZeroStep() {
	for i := 0; i < 8; i += 0 {
		go process(i)
	}
}
