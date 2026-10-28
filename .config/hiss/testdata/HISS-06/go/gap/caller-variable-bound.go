package p

import "golang.org/x/sync/errgroup"

func process(string) {}

// GapCallerVariableBound passes a caller variable to SetLimit; without interprocedural constant
// propagation the per-function syntax check cannot prove the caller's value is positive and
// bounded.
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
