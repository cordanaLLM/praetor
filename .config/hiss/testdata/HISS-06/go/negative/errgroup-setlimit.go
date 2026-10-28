package p

import "golang.org/x/sync/errgroup"

func process(string) {}

// BoundedByErrgroup caps concurrent goroutines with errgroup.Group.SetLimit before the loop.
func BoundedByErrgroup(items []string) error {
	var g errgroup.Group
	g.SetLimit(8)
	for _, item := range items {
		v := item
		go func() {
			process(v)
		}()
	}
	return g.Wait()
}
