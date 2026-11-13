package p

// process demonstrates fanout inside a function literal, which should be reported.
func process(string) {}

// FanoutInClosure launches a goroutine inside a function literal invoked per loop iteration.
func FanoutInClosure(items []string) {
	for _, item := range items {
		func(v string) {
			go process(v)
		}(item)
	}
}
