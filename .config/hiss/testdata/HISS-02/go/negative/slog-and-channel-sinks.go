package drainer

import (
	"context"
	"log/slog"
)

// Drainer forwards events and logs what it drops.
type Drainer struct {
	log *slog.Logger
	out chan<- Event
}

// Event is one queued message.
type Event struct{ ID string }

// log/slog takes a context only to hand its values to the handler, and a select whose cases
// only send to or receive from channels reads the context through Done: neither performs I/O
// on it, so neither needs a deadline.
func (d *Drainer) Forward(logger *slog.Logger, events <-chan Event) {
	ctx := context.Background()
	slog.InfoContext(ctx, "drainer started")
	logger.DebugContext(ctx, "draining")
	for i := 0; i < 1024; i++ {
		select {
		case ev := <-events:
			select {
			case d.out <- ev:
			case <-ctx.Done():
				return
			}
		case <-ctx.Done():
			d.log.ErrorContext(ctx, "drainer stopped", "err", ctx.Err())
			return
		}
	}
}
