package operator

import (
	"context"

	"go.uber.org/fx"
)

// Worker keeps the cancel function of its drain loop in a struct field.
type Worker struct {
	drainCancel context.CancelFunc
	otherCancel context.CancelFunc
}

// The near misses of the stored forms. The first context is stored in a variable that OnStop
// never calls; the second is stored in a field while OnStop calls another field; the third is
// called in OnStop after a return that an error can take, so the cancel is skipped on that path;
// the fourth is called after a guard that returns when the cancel function was stored.
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
			w.otherCancel()
			return mgr.Wait(stopCtx)
		},
	})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			lateCtx, lateCancel := context.WithCancel(context.Background())
			cancel = lateCancel
			go mgr.Start(lateCtx)
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			if err := mgr.Wait(stopCtx); err != nil {
				return err
			}
			cancel()
			return nil
		},
	})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			guardCtx, guardCancel := context.WithCancel(context.Background())
			cancel = guardCancel
			go mgr.Start(guardCtx)
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			if cancel != nil {
				return mgr.Wait(stopCtx)
			}
			cancel()
			return nil
		},
	})
}

// Manager runs until its context is cancelled.
type Manager interface {
	Start(ctx context.Context) error
	Drain(ctx context.Context) error
	Wait(ctx context.Context) error
}
