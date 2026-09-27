package p

import "context"

// A context kept in a field is not followed; the field's value at the call depends on
// every writer of the struct.
type loader struct{ ctx context.Context }

func (l *loader) Load(url string) ([]byte, error) {
	l.ctx = context.Background()
	return fetch(l.ctx, url)
}

func fetch(ctx context.Context, url string) ([]byte, error) { return nil, ctx.Err() }
