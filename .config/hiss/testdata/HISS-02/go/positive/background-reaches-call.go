package p

import (
	"context"
	"net/http"
)

// context.Background carries no deadline, so the fetch it reaches is unbounded: fetch hands
// it straight to the HTTP client.
func Load(url string) (*http.Response, error) {
	return fetch(context.Background(), url)
}

func fetch(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}
