package p

import "context"

// WithoutCancel drops the caller's deadline along with its cancellation, so the cleanup
// can hang after the caller has given up.
func Upload(ctx context.Context, path string) error {
	if err := send(ctx, path); err != nil {
		return remove(context.WithoutCancel(ctx), path)
	}
	return nil
}

func send(ctx context.Context, path string) error   { return ctx.Err() }
func remove(ctx context.Context, path string) error { return ctx.Err() }
