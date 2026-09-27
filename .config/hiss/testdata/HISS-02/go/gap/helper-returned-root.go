package p

import "context"

// A context returned by a helper is not followed: deciding what rootContext returns needs
// the callee's body, which a per-function syntax scan does not read.
func Load(url string) ([]byte, error) {
	ctx := rootContext()
	return fetch(ctx, url)
}

func rootContext() context.Context { return context.Background() }

func fetch(ctx context.Context, url string) ([]byte, error) { return nil, ctx.Err() }
