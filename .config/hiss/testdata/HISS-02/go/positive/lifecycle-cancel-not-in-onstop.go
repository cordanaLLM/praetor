package operator

import (
	"context"

	"go.uber.org/fx"
)

// The near miss: the context comes from WithCancel and runs in OnStart, but OnStop never calls
// its cancel function, so nothing ends the loop and nothing bounds the context.
func Register(lc fx.Lifecycle, mgr Manager) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go mgr.Start(ctx)
			return nil
		},
		OnStop: func(context.Context) error {
			return nil
		},
	})
}

// Manager runs until its context is cancelled.
type Manager interface {
	Start(ctx context.Context) error
}
