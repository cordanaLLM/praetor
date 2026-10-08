package drainer

import (
	"context"
	"log/slog"
)

// Params holds one logger and one shipper whose methods look like the sink methods.
type Params struct {
	Logger  *slog.Logger
	Shipper Shipper
}

// Shipper sends entries over the network.
type Shipper interface {
	ErrorContext(ctx context.Context, msg string)
}

// The near misses of the logger proof in a callee: the receiver is a parameter of another type,
// a field that is not a *slog.Logger, and a range variable that shadows a logger local. None is proven a *slog.Logger, so the context reaches a call that may do I/O.
func Start(p Params, s Shipper) {
	ctx := context.Background()
	viaParam(s, ctx)
	viaField(p, ctx)
	viaRebound(s, ctx)
}

func viaParam(s Shipper, ctx context.Context) {
	s.ErrorContext(ctx, "drainer failed")
}

func viaField(p Params, ctx context.Context) {
	p.Shipper.ErrorContext(ctx, "drainer slow")
}

func viaRebound(s Shipper, ctx context.Context) {
	logger := slog.Default()
	for _, logger := range []Shipper{s} {
		logger.ErrorContext(ctx, "drainer failed")
	}
	_ = logger
}
