package p

// process demonstrates fanout with an input-sized token channel, which bounds nothing.
func process(string) {}

// LenSizedChanLimiter sizes the token channel by len(items), allowing one goroutine per input item.
func LenSizedChanLimiter(items []string) {
	tokens := make(chan struct{}, len(items))
	for _, item := range items {
		tokens <- struct{}{}
		v := item
		go func() {
			defer func() { <-tokens }()
			process(v)
		}()
	}
}
