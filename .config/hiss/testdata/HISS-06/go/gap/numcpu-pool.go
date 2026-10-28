package p

import "runtime"

func process(string) {}

// GapNumCPUPool sizes a token channel by runtime.NumCPU() without a //hiss:bound comment; the
// channel capacity depends on the host rather than a static scalar constant.
func GapNumCPUPool(items []string) {
	tokens := make(chan struct{}, runtime.NumCPU())
	for _, item := range items {
		tokens <- struct{}{}
		v := item
		go func() {
			defer func() { <-tokens }()
			process(v)
		}()
	}
}
