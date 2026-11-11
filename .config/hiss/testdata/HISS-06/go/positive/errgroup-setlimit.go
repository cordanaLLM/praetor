package p

import "golang.org/x/sync/errgroup"

// process demonstrates an errgroup with an explicit limit, which does not bound a raw go statement.
func process(string) {}

// BoundedByErrgroup does not bound raw goroutines using errgroup.Group.SetLimit.
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
