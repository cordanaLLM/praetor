package p

import "context"

// The inner ctx lives only in the if block; the call after it passes the parameter.
func Load(ctx context.Context, fresh bool, url string) ([]byte, error) {
	if fresh {
		ctx := context.Background()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return fetch(ctx, url)
}

func fetch(ctx context.Context, url string) ([]byte, error) { return nil, ctx.Err() }
