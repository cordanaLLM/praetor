// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package admission

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

const (
	// DefaultMaxHeavy bounds simultaneous heavy steps for one user on one workstation.
	DefaultMaxHeavy = 3
	// MaxHeavySlots bounds slot files and every slot scan.
	MaxHeavySlots = 4096
	// MaxTickets bounds live waiters and every ticket scan.
	MaxTickets = 4096
	// SlotsDirEnv overrides the shared admission directory; --slots-dir takes precedence.
	SlotsDirEnv = "PRAETOR_ADMIT_SLOTS_DIR"
	// DefaultSlotPoll is the delay between nonblocking lock attempts.
	DefaultSlotPoll = 5 * time.Second
)

// Slots configures the cross-process heavy-step queue. Zero MaxHeavy explicitly disables it.
// All callers sharing Dir must use the same positive MaxHeavy.
type Slots struct {
	// Dir overrides the per-user runtime directory and SlotsDirEnv.
	Dir string
	// MaxHeavy is the agreed slot bound; zero explicitly disables counting.
	MaxHeavy int
	// Poll is the positive delay between nonblocking slot attempts.
	Poll time.Duration
	owns func(os.FileInfo) bool
}

// Lease holds a kernel slot lock until Close. Text identifies the slot or the stated skip.
type Lease struct {
	// Text names the held slot or the explicit uncounted mode.
	Text    string
	file    *os.File
	release func() error
}

// Close releases a heavy-step slot; an uncounted lease has nothing to release.
func (l *Lease) Close() error {
	if l == nil || l.release == nil {
		return nil
	}
	return l.release()
}

// Check validates the finite slot and polling bounds.
func (s Slots) Check() error {
	if s.MaxHeavy < 0 || s.MaxHeavy > MaxHeavySlots {
		return fmt.Errorf("--max-heavy must be between 0 and %d; refused", MaxHeavySlots)
	}
	if s.Poll <= 0 {
		return errors.New("--slot-poll must be positive; refused")
	}
	return nil
}

// Acquire reserves a slot under the caller's deadline without reading the host.
// The returned lease must remain open for the heavy step's entire lifetime.
func (s Slots) Acquire(ctx context.Context, label string, log io.Writer) (*Lease, error) {
	if err := s.Check(); err != nil {
		return nil, err
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("heavy-step slot wait requires a context deadline; refused")
	}
	if s.MaxHeavy == 0 {
		return &Lease{Text: "heavy-step count disabled (--max-heavy 0); admitted uncounted"}, nil
	}
	return s.acquire(ctx, label, log)
}

// Acquire takes a ticket, then a slot, then waits for host headroom while retaining the slot.
// MaxWait covers all three gates; the caller retains the lease through the entire heavy step.
func (w Waiter) Acquire(ctx context.Context, label string, slots Slots) (lease *Lease, verdict Verdict, resultErr error) {
	if err := slots.Check(); err != nil {
		return nil, Verdict{}, err
	}
	if w.MaxWait <= 0 || w.Poll <= 0 {
		return nil, Verdict{}, errors.New("admission wait and poll must be positive; refused")
	}
	waitCtx, cancel := context.WithTimeout(ctx, w.MaxWait)
	defer cancel()
	lease, err := slots.Acquire(waitCtx, label, w.Log)
	if err != nil {
		return nil, Verdict{}, err
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, lease.Close())
		}
	}()
	deadline, _ := waitCtx.Deadline()
	w.MaxWait = time.Until(deadline)
	verdict, resultErr = w.Wait(waitCtx, label)
	return lease, verdict, resultErr
}

// queueLine writes a timestamped queue or wait diagnostic and returns any writer failure so admission
// cannot silently lose the diagnostic.
func queueLine(log io.Writer, label, kind, reason string) error {
	_, err := fmt.Fprintf(log, "%s admission %s %s: %s\n", time.Now().Format("15:04:05"), kind, label, reason)
	return err
}

// heldRefusal wraps the wait failure with the last observed blocking reason and label, retaining the
// original error for deadline checks.
func heldRefusal(label, reason string, err error) error {
	return fmt.Errorf("admission %s: still held (%s), refused: %w", label, reason, err)
}
