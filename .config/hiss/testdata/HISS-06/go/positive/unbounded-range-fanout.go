package p

func process(string) {}

// Fanout launches one goroutine per slice element with no concurrency bound.
func Fanout(items []string) {
	for _, item := range items {
		go process(item)
	}
}
