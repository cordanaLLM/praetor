package store

import (
	"context"
	"net/http"
	"time"
)

// Option adjusts a connection.
type Option func(*http.Client)

// The caller passes context.Background() with the options spread into a variadic parameter. The
// fixed context argument still maps to the callee's first parameter, which derives its own
// timeout before any other use; the spread options carry no context.
func Open(opts ...Option) (*http.Client, error) {
	return Connect(context.Background(), "https://store.example", opts...)
}

// Connect bounds the dial by a fixed timeout.
func Connect(ctx context.Context, url string, opts ...Option) (*http.Client, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
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
