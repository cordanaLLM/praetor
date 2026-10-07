// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package contextopt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DirectoryLockBudget is how long LockDirectory waits, in total, for another process to
// release a directory before it fails and names that process. A cooperating writer holds the
// lock for one snapshot publish, a few milliseconds. The budget also absorbs a process that
// takes the same advisory lock for a reason of its own, for example systemd-tmpfiles, which
// takes an exclusive flock on every directory its age-based cleanup descends into (#820).
const DirectoryLockBudget = 5 * time.Second

const (
	// lockRetryFirst and lockRetryCeiling pace the retries inside the budget: the delay
	// doubles from the first value and stays at the ceiling.
	lockRetryFirst   = 5 * time.Millisecond
	lockRetryCeiling = 200 * time.Millisecond
	// maxLockAttempts is the scalar bound of the retry loop (HISS-02). The budget ends the
	// loop long before it: at the ceiling, MaxDuration allows fewer than 200 attempts.
	maxLockAttempts = 4096
)

// lockBudgetKey carries a budget WithDirectoryLockBudget set.
type lockBudgetKey struct{}

// lockPauseKey carries the wait between two attempts; only this package's tests set it, to
// act at the moment the budget ends.
type lockPauseKey struct{}

// lockPause waits delay or until ctx ends, and reports whether the whole delay passed.
type lockPause func(ctx context.Context, delay time.Duration) bool

// WithDirectoryLockBudget returns ctx carrying budget as the whole wait LockDirectory grants
// for this directory's other writers, in this process or another, in place of the defaults:
// a writer in this process is waited for until ctx ends, at most MaxDuration, and another
// process for DirectoryLockBudget. A zero budget makes one attempt and fails at once while
// another writer holds the lock. A negative budget is refused; one above MaxDuration, the
// bound of every snapshot operation, is cut to it.
func WithDirectoryLockBudget(ctx context.Context, budget time.Duration) (context.Context, error) {
	if ctx == nil {
		return nil, errors.New("directory lock budget requires a context")
	}
	if budget < 0 {
		return nil, fmt.Errorf("directory lock budget %s is negative", budget)
	}
	return context.WithValue(ctx, lockBudgetKey{}, min(budget, MaxDuration)), nil
}

// lockWait is the wait one LockDirectory call grants: the budget, whether
// WithDirectoryLockBudget set it, and when that explicit budget ends.
type lockWait struct {
	budget   time.Duration
	explicit bool
	deadline time.Time
}

// directoryLockWait returns the wait ctx carries, starting now, or DirectoryLockBudget.
func directoryLockWait(ctx context.Context) lockWait {
	if budget, ok := ctx.Value(lockBudgetKey{}).(time.Duration); ok {
		return lockWait{budget: budget, explicit: true, deadline: time.Now().Add(budget)}
	}
	return lockWait{budget: DirectoryLockBudget}
}

// bound returns ctx ending with the explicit budget, which both waits share, or without one
// DirectoryLockBudget from now.
func (w lockWait) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	if w.explicit {
		return context.WithDeadline(ctx, w.deadline)
	}
	return context.WithTimeout(ctx, w.budget)
}

// lockPauseFor returns the pause ctx carries, or sleepUntil.
func lockPauseFor(ctx context.Context) lockPause {
	if pause, ok := ctx.Value(lockPauseKey{}).(lockPause); ok {
		return pause
	}
	return sleepUntil
}

// sleepUntil waits delay or until ctx ends, and reports whether the whole delay passed.
func sleepUntil(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// LockDirectory serializes cooperating operations on an already pinned directory and returns
// the release, which must be called once; a second call changes nothing and reports
// os.ErrClosed.
//
// It takes two locks, in this order (#820). Writers in this process take turns per directory,
// identified by os.SameFile, so two writers of one run never refuse each other: a writer waits
// for the one ahead of it until ctx ends, at most MaxDuration. The turn then takes the
// platform's lock on the directory (tryLockSnapshotDirectory), which writers in other
// processes share. While another process holds it, the writer retries until
// DirectoryLockBudget has passed, makes one last attempt as the budget ends, and fails naming
// the holder, by process id where the platform reports it, and the budget. A budget set with
// WithDirectoryLockBudget bounds both waits together.
func LockDirectory(ctx context.Context, root *os.Root) (func() error, error) {
	if ctx == nil || root == nil {
		return nil, errors.New("directory lock requires a context and pinned root")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	info, err := root.Stat(".")
	if err != nil {
		return nil, fmt.Errorf("identify directory %s to lock it: %w", root.Name(), err)
	}
	wait := directoryLockWait(ctx)
	leave, err := takeProcessTurn(ctx, root, info, wait)
	if err != nil {
		return nil, err
	}
	unlock, err := waitForDirectoryLock(ctx, root, wait)
	if err != nil {
		leave()
		return nil, err
	}
	var released atomic.Bool
	return func() error {
		if !released.CompareAndSwap(false, true) {
			return fmt.Errorf("release snapshot directory %s lock: already released: %w", root.Name(), os.ErrClosed)
		}
		// The platform lock goes first, so the next writer of this process finds it free.
		defer leave()
		return unlock()
	}, nil
}

// takeProcessTurn waits for this process's turn on the directory info identifies, until ctx
// ends or an explicit budget passes, and returns the function that ends the turn.
func takeProcessTurn(ctx context.Context, root *os.Root, info os.FileInfo, wait lockWait) (func(), error) {
	if wait.explicit {
		var cancel context.CancelFunc
		ctx, cancel = wait.bound(ctx)
		defer cancel()
	}
	leave, err := processDirectoryTurns.take(ctx, info)
	if err == nil {
		return leave, nil
	}
	holder := fmt.Sprintf("another writer in this process (pid %d)", os.Getpid())
	if wait.explicit && errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("snapshot directory busy: %s held the lock on %s past the %s wait budget", holder, root.Name(), wait.budget)
	}
	return nil, fmt.Errorf("snapshot directory busy: %s holds the lock on %s: %w", holder, root.Name(), err)
}

// waitForDirectoryLock takes the platform lock on root, retrying while another process holds
// it until the budget has passed, and then makes one last attempt. ctx ending first ends the
// wait as well.
func waitForDirectoryLock(ctx context.Context, root *os.Root, wait lockWait) (func() error, error) {
	waitCtx, cancel := wait.bound(ctx)
	defer cancel()
	pause := lockPauseFor(ctx)
	delay := lockRetryFirst
	for attempt := 0; attempt < maxLockAttempts; attempt++ {
		release, busy, err := tryLockSnapshotDirectory(root)
		if err != nil || !busy {
			return release, err
		}
		if !pause(waitCtx, delay) {
			break
		}
		delay = min(2*delay, lockRetryCeiling)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("snapshot directory busy: %s holds the lock on %s: %w", directoryLockHolder(root), root.Name(), err)
	}
	release, busy, err := tryLockSnapshotDirectory(root)
	if err != nil || !busy {
		return release, err
	}
	return nil, fmt.Errorf("snapshot directory busy: %s held the lock on %s past the %s wait budget",
		directoryLockHolder(root), root.Name(), wait.budget)
}

// describeLockHolders names the processes pids lists for a busy-lock error, marking this
// process when it is one of them; with none listed it is "another process" and unknown, the
// reason none is.
func describeLockHolders(pids []int, unknown string) string {
	names := make([]string, 0, len(pids))
	for _, pid := range pids {
		name := fmt.Sprintf("process %d", pid)
		if pid == os.Getpid() {
			name += " (this process)"
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	switch len(names) {
	case 0:
		return "another process (" + unknown + ")"
	case 1:
		return names[0]
	}
	last := len(names) - 1
	return strings.Join(names[:last], ", ") + " and " + names[last]
}

// processDirectoryTurns serializes this process's writers per directory, ahead of the
// platform lock, which does not tell two writers of one process apart on every platform and
// refuses the second instead of queueing it.
var processDirectoryTurns directoryTurns

// directoryTurns holds one turn per directory some writer of this process is using.
type directoryTurns struct {
	mu      sync.Mutex
	entries []*directoryTurn
}

// directoryTurn is one directory's turn: the writer that sent into token holds it; users
// counts the writers holding or waiting for it.
type directoryTurn struct {
	info  os.FileInfo
	token chan struct{}
	users int
}

// take waits for the turn on the directory info identifies until ctx ends, and returns the
// function that ends the turn; that function changes nothing after its first call. A free
// turn is taken even when ctx has ended.
func (d *directoryTurns) take(ctx context.Context, info os.FileInfo) (func(), error) {
	entry := d.join(info)
	select {
	case entry.token <- struct{}{}:
	default:
		select {
		case entry.token <- struct{}{}:
		case <-ctx.Done():
			d.leave(entry)
			return nil, context.Cause(ctx)
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-entry.token
			d.leave(entry)
		})
	}, nil
}

// join returns the turn of the directory info identifies, creating it for its first user.
func (d *directoryTurns) join(info os.FileInfo) *directoryTurn {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, entry := range d.entries {
		if os.SameFile(entry.info, info) {
			entry.users++
			return entry
		}
	}
	entry := &directoryTurn{info: info, token: make(chan struct{}, 1), users: 1}
	d.entries = append(d.entries, entry)
	return entry
}

// leave drops one user of entry and forgets the turn when its last user leaves.
func (d *directoryTurns) leave(entry *directoryTurn) {
	d.mu.Lock()
	defer d.mu.Unlock()
	entry.users--
	if entry.users == 0 {
		d.entries = slices.DeleteFunc(d.entries, func(other *directoryTurn) bool { return other == entry })
	}
}
