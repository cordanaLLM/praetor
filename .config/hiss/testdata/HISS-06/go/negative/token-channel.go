package p

func process(string) {}

// BoundedByTokenChannel sends on a buffered token channel before launching each goroutine and
// receives the token when the goroutine finishes.
func BoundedByTokenChannel(items []string) {
	tokens := make(chan struct{}, 8)
	for _, item := range items {
		tokens <- struct{}{}
		v := item
		go func() {
			defer func() { <-tokens }()
			process(v)
		}()
	}
}
