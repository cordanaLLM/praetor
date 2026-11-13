package p

import "golang.org/x/sync/errgroup"

// process demonstrates a caller-provided fanout bound, which does not bound raw goroutines.
func process(string) {}

// GapCallerVariableBound passes a caller variable to SetLimit; which does not bound a raw go statement.
func GapCallerVariableBound(items []string, limit int) error {
	var g errgroup.Group
	g.SetLimit(limit)
	for _, item := range items {
		v := item
		go func() {
			process(v)
		}()
	}
	return g.Wait()
}
