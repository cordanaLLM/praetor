package p

import (
	"context"
	"time"
)

// WithTimeout bounds the root, and WithCancel on the result keeps that deadline.
func Load(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	inner, stop := context.WithCancel(ctx)
	defer stop()
	return fetch(inner, url)
}

func fetch(ctx context.Context, url string) ([]byte, error) { return nil, ctx.Err() }
