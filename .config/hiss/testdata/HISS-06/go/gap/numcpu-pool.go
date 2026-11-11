package p

import (
	"runtime"

	"golang.org/x/sync/errgroup"
)

// process demonstrates an errgroup worker pool whose runtime.NumCPU() limit is not statically constant.
func process(string) {}

// GapNumCPUPool launches goroutines through errgroup.Group.Go under a runtime.NumCPU() limit
// rather than a raw go statement, which the per-function *ast.GoStmt check does not inspect.
func GapNumCPUPool(items []string) error {
	var g errgroup.Group
	g.SetLimit(runtime.NumCPU())
	for _, item := range items {
		v := item
		g.Go(func() error {
			process(v)
			return nil
		})
	}
	return g.Wait()
}
