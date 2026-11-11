package p

import "golang.org/x/sync/errgroup"

func process(string) {}

// FanoutWithoutSetLimit declares an errgroup.Group without calling SetLimit before the loop.
func FanoutWithoutSetLimit(items []string) error {
	var g errgroup.Group
	for _, item := range items {
		v := item
		go func() {
			process(v)
		}()
	}
	return g.Wait()
}
