package drainer

import (
	"context"
	"net/http"
)

// Event is one queued message.
type Event struct{ ID string }

// The near miss: the helper selects on the context's Done channel like a channel-only helper, and
// it also hands the same context to a request, which blocks on the network.
func Forward(client *http.Client, events <-chan Event) {
	forwardOne(context.Background(), client, events)
}

func forwardOne(ctx context.Context, client *http.Client, events <-chan Event) {
	select {
	case ev := <-events:
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://sink.example/"+ev.ID, nil)
		if err != nil {
			return
		}
		_, _ = client.Do(req)
	case <-ctx.Done():
	}
}
