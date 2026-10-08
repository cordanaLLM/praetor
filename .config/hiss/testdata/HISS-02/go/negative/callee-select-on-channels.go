package drainer

import "context"

// Event is one queued message.
type Event struct{ ID string }

// The caller passes context.Background() to a helper of the same module that only selects on
// the context's Done channel and on channels of its own: reading a context through Done performs
// no I/O, so the helper needs no deadline.
func Forward(events <-chan Event, out chan<- Event) {
	forwardOne(context.Background(), events, out)
}

func forwardOne(ctx context.Context, events <-chan Event, out chan<- Event) {
	select {
	case ev := <-events:
		select {
		case out <- ev:
		case <-ctx.Done():
		}
	case <-ctx.Done():
	}
}
