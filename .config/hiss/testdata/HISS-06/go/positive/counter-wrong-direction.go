package p

// process demonstrates a counter loop stepping away from its bound, which should be reported.
func process(int) {}

// CounterWrongDirection decrements a counter compared with < against an upper bound.
func CounterWrongDirection() {
	for i := 0; i < 8; i -= 1 {
		go process(i)
	}
}
