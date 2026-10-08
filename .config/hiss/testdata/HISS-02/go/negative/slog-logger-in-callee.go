package drainer

import (
	"context"
	"log/slog"
)

// Params is the dependency struct a constructor receives.
type Params struct {
	Logger *slog.Logger
}

// The caller passes context.Background() to helpers of the same module. Each helper hands the
// context only to a log/slog sink, on a logger it holds as a parameter, a local or a field of
// its parameter struct: log/slog uses the context for its values, never for I/O.
func Start(p Params, logger *slog.Logger) {
	ctx := context.Background()
	viaParam(logger, ctx)
	viaLocal(ctx)
	viaField(p, ctx)
}

func viaParam(logger *slog.Logger, ctx context.Context) {
	logger.ErrorContext(ctx, "drainer failed")
}

func viaLocal(ctx context.Context) {
	logger := slog.Default().With("component", "drainer")
	logger.InfoContext(ctx, "drainer started")
}

func viaField(p Params, ctx context.Context) {
	p.Logger.WarnContext(ctx, "drainer slow")
}
