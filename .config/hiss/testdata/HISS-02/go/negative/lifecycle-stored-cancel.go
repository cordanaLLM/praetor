package operator

import (
	"context"

	"go.uber.org/fx"
)

// Worker keeps the cancel function of its drain loop in a struct field.
type Worker struct {
	drainCancel context.CancelFunc
}

// The cancel function is stored where OnStop can reach it: in a variable of the constructor
// (cancel = runCancel) and in a field of the receiver (w.drainCancel = drainCancel). OnStop calls
// each as its own top-level statement. The only return before a call is the guard on the very
// variable it calls, which returns when nothing was stored, so every context OnStart launches is
// cancelled when the lifecycle stops.
func (w *Worker) Register(lc fx.Lifecycle, mgr Manager) {
	var cancel context.CancelFunc
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			runCtx, runCancel := context.WithCancel(context.Background())
			cancel = runCancel
			go mgr.Start(runCtx)
			drainCtx, drainCancel := context.WithCancel(context.Background())
			w.drainCancel = drainCancel
			go mgr.Drain(drainCtx)
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			w.drainCancel()
			if cancel == nil {
				return nil
			}
			cancel()
			return mgr.Wait(stopCtx)
		},
	})
}

// Manager runs until its context is cancelled.
type Manager interface {
	Start(ctx context.Context) error
	Drain(ctx context.Context) error
	Wait(ctx context.Context) error
}
