package p

func process(string) {}

// FanoutInClosure launches a goroutine inside a function literal invoked per loop iteration.
func FanoutInClosure(items []string) {
	for _, item := range items {
		func(v string) {
			go process(v)
		}(item)
	}
}
