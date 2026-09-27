package p

import "context"

// WithCancel keeps the parent's deadline, and context.TODO has none to keep.
func Load(url string) ([]byte, error) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	return fetch(ctx, url)
}

func fetch(ctx context.Context, url string) ([]byte, error) { return nil, ctx.Err() }
