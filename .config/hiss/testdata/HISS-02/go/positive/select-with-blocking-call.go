package drainer

import (
	"context"
	"net/http"
)

// The near miss: the select reads the context through Done like a channel sink, but one case
// sends the result of a call that takes the context and blocks on the network.
func Forward(client *http.Client, out chan<- *http.Response, url string) {
	ctx := context.Background()
	select {
	case out <- fetch(ctx, client, url):
	case <-ctx.Done():
	}
}

func fetch(ctx context.Context, client *http.Client, url string) *http.Response {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	return resp
}
