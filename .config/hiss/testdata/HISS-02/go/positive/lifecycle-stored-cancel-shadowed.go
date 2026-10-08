package operator

import (
	"context"

	"go.uber.org/fx"
)

// Worker keeps the cancel function of its drain loop in a struct field.
type Worker struct {
	drainCancel context.CancelFunc
}

// The near misses of the binding rule. The first OnStart declares its own cancel variable, which
// is not the one OnStop calls; the second stores the function only on a branch, so the nil guard
// in OnStop returns silently on the other; the third stores it in a closure nobody calls; the
// fourth stores it in a holder the start function declares itself.
func (w *Worker) Register(lc fx.Lifecycle, mgr Manager) {
	var cancel context.CancelFunc
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			shadowCtx, cancel := context.WithCancel(context.Background())
			go mgr.Start(shadowCtx)
			_ = cancel
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			if cancel == nil {
				return nil
			}
			cancel()
			return mgr.Wait(stopCtx)
		},
	})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			branchCtx, branchCancel := context.WithCancel(context.Background())
			if mgr == nil {
				cancel = branchCancel
			}
			go mgr.Start(branchCtx)
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			if cancel == nil {
				return nil
			}
			cancel()
			return mgr.Wait(stopCtx)
		},
	})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			closureCtx, closureCancel := context.WithCancel(context.Background())
			store := func() { cancel = closureCancel }
			_ = store
			go mgr.Start(closureCtx)
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			if cancel == nil {
				return nil
			}
			cancel()
			return mgr.Wait(stopCtx)
		},
	})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			holderCtx, holderCancel := context.WithCancel(context.Background())
			w := &Worker{}
			w.drainCancel = holderCancel
			go mgr.Start(holderCtx)
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			w.drainCancel()
			return mgr.Wait(stopCtx)
		},
	})
}

// Manager runs until its context is cancelled.
type Manager interface {
	Start(ctx context.Context) error
	Wait(ctx context.Context) error
}
