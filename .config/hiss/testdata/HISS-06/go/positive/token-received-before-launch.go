package p

// process demonstrates fanout after draining the sent token prior to launching the goroutine.
func process(string) {}

// TokenReceivedBeforeLaunch receives from the token channel before starting the goroutine,
// freeing the slot before the worker runs.
func TokenReceivedBeforeLaunch(items []string) {
	tokens := make(chan struct{}, 4)
	for _, item := range items {
		tokens <- struct{}{}
		<-tokens
		v := item
		go func() {
			defer func() { <-tokens }()
			process(v)
		}()
	}
}
