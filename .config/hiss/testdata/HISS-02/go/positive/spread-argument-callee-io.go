package store

import (
	"context"
	"net/http"
)

// Option adjusts a connection.
type Option func(*http.Client)

// The near miss of the spread call: the fixed context argument maps to the callee's first
// parameter, and that callee uses it for a request before any deadline exists.
func Open(opts ...Option) (*http.Client, error) {
	return Connect(context.Background(), "https://store.example", opts...)
}

// Connect sends the request under the caller's context.
func Connect(ctx context.Context, url string, opts ...Option) (*http.Client, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{}
	for i := 0; i < len(opts); i++ {
		opts[i](client)
	}
	_, err = client.Do(req)
	return client, err
}
