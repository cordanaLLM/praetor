package p

import "context"

// The caller's context is the caller's to bound; a parameter is passed on as it came.
func Load(ctx context.Context, url string) ([]byte, error) {
	return fetch(ctx, url)
}

func fetch(ctx context.Context, url string) ([]byte, error) { return nil, ctx.Err() }
