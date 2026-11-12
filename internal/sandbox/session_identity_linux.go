// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	sessionRegistryLimit     = 65536
	sessionHandshakeTimeout  = 5 * time.Second
	sessionLockTimeout       = 5 * time.Second
	sessionMaxReservationAge = uint64((sessionHandshakeTimeout + sessionLockTimeout) / (10 * time.Millisecond))
	sessionTickHold          = uint64((sessionHandshakeTimeout + sessionLockTimeout + time.Second) / (10 * time.Millisecond))
)

var errSessionReservationLate = errors.New("session reservation too late")

type sessionReservation struct {
	Owner          int    `json:"owner"`
	OwnerStartTick uint64 `json:"owner_start_tick"`
	Released       bool   `json:"released"`
}

// Session reservations coordinate sandboxes of one user, not a rootful engine shared by users.
func reserveSessionTick(ctx context.Context, path string, tick uint64, owner int) (bool, error) {
	return updateSessionRegistry(ctx, path, tick, owner, false)
}

// reserveFreshSessionTickWithClock refuses a reservation whose start tick is too old to be safe.
func reserveFreshSessionTickWithClock(ctx context.Context, path string, tick uint64, owner int, clock func() (uint64, error)) (bool, error) {
	checkedClock := func() (uint64, error) {
		now, err := clock()
		if err == nil && now >= tick && now-tick >= sessionMaxReservationAge {
			return 0, errSessionReservationLate
		}
		return now, err
	}
	ok, err := updateSessionRegistryWithClock(ctx, path, tick, owner, false, checkedClock)
	if errors.Is(err, errSessionReservationLate) {
		return false, nil
	}
	return ok, err
}

// updateSessionRegistry updates a reservation using the system clock so stale ticks can be pruned before reuse.
func updateSessionRegistry(ctx context.Context, path string, tick uint64, owner int, remove bool) (ok bool, resultErr error) {
	return updateSessionRegistryWithClock(ctx, path, tick, owner, remove, currentSessionTick)
}

// updateSessionRegistryWithClock serializes registry updates and holds released ticks to prevent identity reuse during the retention window.
func updateSessionRegistryWithClock(ctx context.Context, path string, tick uint64, owner int, remove bool, clock func() (uint64, error)) (ok bool, resultErr error) {
	return sessionRead(ctx, func() (bool, error) {
		return updateSessionRegistryLocked(ctx, path, tick, owner, remove, clock)
	})
}

// updateSessionRegistryLocked owns the lock until all registry operations finish.
func updateSessionRegistryLocked(ctx context.Context, path string, tick uint64, owner int, remove bool, clock func() (uint64, error)) (ok bool, resultErr error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return false, err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	release, err := sessionLock(ctx, root, filepath.Base(path)+".lock")
	if err != nil {
		return false, err
	}
	defer func() { resultErr = errors.Join(resultErr, release()) }()
	entries, err := sessionRead(ctx, func() (map[string]sessionReservation, error) {
		return loadSessionRegistry(path, clock)
	})
	if err != nil {
		return false, err
	}
	ok, err = sessionRead(ctx, func() (bool, error) { return mutateSessionRegistry(entries, tick, owner, remove) })
	if err != nil || !ok {
		return ok, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err = writeSessionRegistryWithContext(ctx, path, entries, func() {}); err != nil {
		return false, err
	}
	return true, nil
}

// sessionRead returns on cancellation even if a filesystem operation has stalled.
func sessionRead[T any](ctx context.Context, read func() (T, error)) (T, error) {
	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	go func() { value, err := read(); done <- result{value, err} }()
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case got := <-done:
		return got.value, got.err
	}
}

// loadSessionRegistry validates and prunes registry entries using the supplied clock.
func loadSessionRegistry(path string, clock func() (uint64, error)) (map[string]sessionReservation, error) {
	entries, err := readSessionRegistry(path)
	if err != nil {
		return nil, err
	}
	now, err := clock()
	if err != nil {
		return nil, err
	}
	if err := pruneSessionRegistry(entries, now); err != nil {
		return nil, err
	}
	return entries, nil
}

// mutateSessionRegistry applies a release or creates a reservation with the owner's process identity.
func mutateSessionRegistry(entries map[string]sessionReservation, tick uint64, owner int, remove bool) (bool, error) {
	key := strconv.FormatUint(tick, 10)
	if remove {
		if entry, found := entries[key]; found && entry.Owner == owner {
			entry.Released = true
			entries[key] = entry
		}
		return true, nil
	}
	if _, found := entries[key]; found {
		return false, nil
	}
	start, err := procStartTick("/proc", owner)
	if err != nil {
		return false, fmt.Errorf("session owner identity: %w", err)
	}
	entries[key] = sessionReservation{Owner: owner, OwnerStartTick: start}
	return true, nil
}

// readSessionRegistry loads a size-limited registry and rejects malformed entries so reservations are not silently lost.
func readSessionRegistry(path string) (map[string]sessionReservation, error) {
	entries := map[string]sessionReservation{}
	data, err := util.ReadFileLimited(path, sessionRegistryLimit)
	if errors.Is(err, os.ErrNotExist) {
		return entries, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read session registry: %w", err)
	}
	var raw map[string]struct {
		Owner          *int   `json:"owner"`
		OwnerStartTick uint64 `json:"owner_start_tick"`
		Released       *bool  `json:"released"`
	}
	if err = json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid session registry: %w", err)
	}
	if raw == nil {
		return nil, errors.New("invalid null session registry")
	}
	for key, value := range raw {
		if value.Owner == nil || value.Released == nil {
			return nil, fmt.Errorf("invalid session registry entry %q", key)
		}
		entries[key] = sessionReservation{Owner: *value.Owner, OwnerStartTick: value.OwnerStartTick, Released: *value.Released}
	}
	return entries, nil
}

// currentSessionTick reads uptime in hundredths of a second and rejects malformed or overflowing values used for retention.
func currentSessionTick() (uint64, error) {
	data, err := util.ReadFileLimited("/proc/uptime", 128)
	if err != nil {
		return 0, fmt.Errorf("read session uptime: %w", err)
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 {
		return 0, errors.New("invalid session uptime")
	}
	parts := strings.Split(fields[0], ".")
	if len(parts) != 2 || len(parts[1]) != 2 {
		return 0, errors.New("invalid session uptime")
	}
	seconds, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || seconds > (^uint64(0)-99)/100 {
		return 0, errors.New("invalid session uptime seconds")
	}
	fraction, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || fraction > 99 {
		return 0, errors.New("invalid session uptime fraction")
	}
	return seconds*100 + fraction, nil
}

// pruneSessionRegistry removes expired released or dead-owner entries while preserving recent ticks and live owners.
func pruneSessionRegistry(entries map[string]sessionReservation, now uint64) error {
	for key, entry := range entries {
		tick, err := strconv.ParseUint(key, 10, 64)
		if err != nil || strconv.FormatUint(tick, 10) != key || entry.Owner <= 0 {
			return fmt.Errorf("invalid session registry entry %q", key)
		}
		if now <= tick || now-tick <= sessionTickHold {
			continue
		}
		if entry.Released {
			delete(entries, key)
			continue
		}
		stale, err := staleSessionOwner(entry)
		if err != nil {
			return err
		}
		if stale {
			delete(entries, key)
		}
	}
	return nil
}

// staleSessionOwner checks whether the recorded owner has exited or its PID was reused.
func staleSessionOwner(entry sessionReservation) (bool, error) {
	start, err := procStartTick("/proc", entry.Owner)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("session owner: %w", err)
	}
	return entry.OwnerStartTick != 0 && start != entry.OwnerStartTick, nil
}

// writeSessionRegistry replaces the registry through a temporary file so readers do not observe a partial update.
func writeSessionRegistry(path string, entries map[string]sessionReservation) (resultErr error) {
	return writeSessionRegistryWithContext(context.Background(), path, entries, func() {})
}

// writeSessionRegistryWithContext checks cancellation under the registry lock before replacing the file.
func writeSessionRegistryWithContext(ctx context.Context, path string, entries map[string]sessionReservation, beforeReplace func()) (resultErr error) {
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	if len(data) > sessionRegistryLimit {
		return errors.New("session registry exceeds limit")
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".session-registry-")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.Remove(temp.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	_, writeErr := temp.Write(data)
	closeErr := temp.Close()
	if err = errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	beforeReplace()
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

// sessionLock acquires the registry lock with a five-second deadline to bound contention.
func sessionLock(ctx context.Context, root *os.Root, name string) (func() error, error) {
	ctx, cancel := context.WithTimeout(ctx, sessionLockTimeout)
	defer cancel()
	return sessionLockWithWait(ctx, root, name, sessionWait)
}

// sessionLockWithWait retries a busy registry lock at most 500 times and respects cancellation to bound waiting.
func sessionLockWithWait(ctx context.Context, root *os.Root, name string, wait func(context.Context) error) (func() error, error) {
	for i := 0; i < 500; i++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("session registry lock: %w", err)
		}
		release, busy, err := util.LockPrivateFile(root, name, os.O_RDWR|os.O_CREATE, "session registry")
		if err != nil {
			return nil, err
		}
		if !busy {
			return release, nil
		}
		if err := wait(ctx); err != nil {
			return nil, fmt.Errorf("session registry lock: %w", err)
		}
	}
	return nil, errors.New("session registry lock: wait limit exceeded")
}

// sessionWait waits between lock attempts or returns on cancellation to avoid busy polling.
func sessionWait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Millisecond):
		return nil
	}
}
