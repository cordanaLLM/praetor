package p

import "golang.org/x/sync/errgroup"

// process demonstrates an errgroup without a limit, which should be reported.
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
