package p

import "golang.org/x/sync/errgroup"

func run(items []int) error {
	var g errgroup.Group
	g.SetLimit(8)
	for range items {
		g.Go(func() error { return nil })
	}
	return g.Wait()
}
