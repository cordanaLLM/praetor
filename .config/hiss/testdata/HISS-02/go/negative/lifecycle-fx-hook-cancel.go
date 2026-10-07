package operator

import (
	"context"

	"go.uber.org/fx"
)

// The fx lifecycle owns this loop's lifetime: OnStart launches it under a context that OnStop
// cancels, exactly as main.main owns a server's root context. A deadline here would stop a
// leader-election or controller loop that must run for the life of the process.
func Register(lc fx.Lifecycle, mgr Manager) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				defer close(done)
				if err := mgr.Start(ctx); err != nil {
					mgr.Fail(err)
				}
			}()
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			cancel()
			select {
			case <-done:
				return nil
			case <-stopCtx.Done():
				return stopCtx.Err()
			}
		},
	})
}

// Manager runs until its context is cancelled.
type Manager interface {
	Start(ctx context.Context) error
	Fail(err error)
}
