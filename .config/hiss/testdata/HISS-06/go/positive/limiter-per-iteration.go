package p

// process demonstrates fanout with a per-iteration channel, which should be reported.
func process(string) {}

// LimiterPerIteration allocates a fresh buffered channel on every iteration instead of sharing one bound.
func LimiterPerIteration(items []string) {
	for _, item := range items {
		tokens := make(chan struct{}, 4)
		tokens <- struct{}{}
		v := item
		go func() {
			defer func() { <-tokens }()
			process(v)
		}()
	}
}
