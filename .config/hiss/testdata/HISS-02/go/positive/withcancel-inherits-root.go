package p

import (
	"context"
	"net/http"
)

// WithCancel keeps the parent's deadline, and context.TODO has none to keep.
func Load(url string) (*http.Response, error) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	return fetch(ctx, url)
}

func fetch(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}
