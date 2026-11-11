package p

// process demonstrates fanout in a constant-header loop whose body rewrites the loop counter.
func process(int) {}

// CounterWrittenInBody decrements the counter inside the loop body, defeating the header upper bound.
func CounterWrittenInBody(keepGoing func(int) bool) {
	for i := 0; i < 8; i++ {
		if keepGoing(i) {
			i--
		}
		go process(i)
	}
}
