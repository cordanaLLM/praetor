package p

// process demonstrates fanout when a token send lies under a condition that does not guard the launch.
func process(string) {}

// ConditionalTokenSend sends to the buffered channel only when throttle holds, leaving other iterations unbounded.
func ConditionalTokenSend(items []string, throttle bool) {
	tokens := make(chan struct{}, 4)
	for _, item := range items {
		if throttle {
			tokens <- struct{}{}
		}
		v := item
		go func() {
			defer func() { <-tokens }()
			process(v)
		}()
	}
}
