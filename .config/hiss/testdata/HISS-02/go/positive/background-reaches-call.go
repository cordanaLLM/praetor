package p

import "context"

// context.Background carries no deadline, so the fetch it reaches is unbounded.
func Load(url string) ([]byte, error) {
	return fetch(context.Background(), url)
}

func fetch(ctx context.Context, url string) ([]byte, error) { return nil, ctx.Err() }
