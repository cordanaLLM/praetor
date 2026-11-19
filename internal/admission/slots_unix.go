// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package admission

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/cordanaLLM/praetor/internal/util"
)

// HeavyCountAvailable reports whether this build has kernel-held heavy slots.
const HeavyCountAvailable = true

type slotQueue struct {
	root          *os.Root
	tickets       *os.Root
	slots         Slots
	ticket        uint64
	ticketRelease func() error
}

func defaultSlotsDir(ctx context.Context) (string, error) {
	if path := os.Getenv(SlotsDirEnv); path != "" {
		return path, nil
	}
	base, err := util.UserRuntimeDirectory(ctx, fmt.Sprintf("praetor-%d", os.Getuid()))
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "praetor-admit"), nil
}

func (s Slots) acquire(ctx context.Context, label string, log io.Writer) (lease *Lease, resultErr error) {
	path := s.Dir
	if path == "" {
		var err error
		path, err = defaultSlotsDir(ctx)
		if err != nil {
			return nil, fmt.Errorf("heavy-step slots directory refused: %w", err)
		}
	}
	root, err := util.PrivateDirectory(ctx, path, s.owns)
	if err != nil {
		return nil, fmt.Errorf("heavy-step slots directory %q refused: %w", path, err)
	}
	q := &slotQueue{root: root, slots: s}
	if err := q.openTickets(ctx, path); err != nil {
		return nil, errors.Join(fmt.Errorf("heavy-step ticket directory refused: %w", err), root.Close())
	}
	defer func() {
		resultErr = errors.Join(resultErr, q.close())
		if resultErr != nil && lease != nil {
			resultErr = errors.Join(resultErr, lease.Close())
		}
	}()
	return q.wait(ctx, label, log)
}

func (q *slotQueue) close() error {
	var err error
	if q.ticketRelease != nil {
		err = q.ticketRelease()
	}
	if q.tickets != nil {
		err = errors.Join(err, q.tickets.Close())
	}
	return errors.Join(err, q.root.Close())
}

func (q *slotQueue) wait(ctx context.Context, label string, log io.Writer) (*Lease, error) {
	last := "ticket directory lock busy"
	queued := false
	for attempt := 0; attempt < maxPolls; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, heldRefusal(label, last, err)
		}
		lease, reason, err := q.attempt(ctx, label)
		if ended := ctx.Err(); ended != nil {
			return nil, errors.Join(heldRefusal(label, last, ended), lease.Close())
		}
		if err != nil {
			return nil, fmt.Errorf("admission %s: heavy-step slots refused: %w", label, err)
		}
		if lease != nil {
			return lease, nil
		}
		last = reason
		kind := "wait"
		if !queued {
			kind, queued = "queued", true
		}
		if err := queueLine(log, label, kind, reason); err != nil {
			return nil, err
		}
		if err := SleepContext(ctx, q.slots.Poll); err != nil {
			return nil, heldRefusal(label, last, err)
		}
	}
	return nil, fmt.Errorf("admission %s: more than %d slot attempts (%s), refused", label, maxPolls, last)
}

func (q *slotQueue) attempt(ctx context.Context, label string) (lease *Lease, reason string, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	file, release, busy, err := util.OpenPrivateLock(q.root, "queue.lock", os.O_RDWR|os.O_CREATE, "heavy-step directory")
	if err != nil || busy {
		return nil, "ticket directory lock busy", err
	}
	defer func() {
		resultErr = errors.Join(resultErr, release())
		if resultErr != nil && lease != nil {
			resultErr = errors.Join(resultErr, lease.Close())
		}
	}()
	if err := q.checkBound(ctx, file); err != nil {
		return nil, "", err
	}
	tickets, err := q.liveTickets(ctx)
	if err != nil {
		return nil, "", err
	}
	if q.ticket == 0 {
		if err := q.takeTicket(tickets); err != nil {
			return nil, "", err
		}
		tickets = append(tickets, q.ticket)
	}
	return q.trySlots(ctx, label, tickets)
}

func (q *slotQueue) checkBound(ctx context.Context, file *os.File) error {
	data, err := io.ReadAll(io.LimitReader(file, 32))
	if err != nil {
		return err
	}
	if len(data) == 0 {
		if err := q.initializeSlots(ctx); err != nil {
			return err
		}
		_, err := fmt.Fprintf(file, "%d", q.slots.MaxHeavy)
		return err
	}
	bound, err := strconv.Atoi(string(data))
	if err != nil {
		return fmt.Errorf("invalid heavy-step directory bound: %w", err)
	}
	if bound != q.slots.MaxHeavy {
		return fmt.Errorf("slots directory bound is %d, requested %d; use the same --max-heavy for every caller", bound, q.slots.MaxHeavy)
	}
	return nil
}

func (q *slotQueue) initializeSlots(ctx context.Context) error {
	for slot := 1; slot <= q.slots.MaxHeavy; slot++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, release, busy, err := util.OpenPrivateLock(q.root, fmt.Sprintf("slot-%04d", slot), os.O_RDWR|os.O_CREATE, "heavy-step slot")
		if err != nil {
			return err
		}
		if busy {
			return errors.New("uninitialized slots directory contains a held slot; refused")
		}
		if err := release(); err != nil {
			return err
		}
	}
	return nil
}

func (q *slotQueue) openTickets(ctx context.Context, path string) (resultErr error) {
	tickets, err := util.PrivateDirectory(ctx, filepath.Join(path, "tickets"), q.slots.owns)
	if err != nil {
		return err
	}
	named, err := q.root.Stat("tickets")
	if err != nil {
		return errors.Join(err, tickets.Close())
	}
	opened, err := tickets.Stat(".")
	if err == nil && !os.SameFile(named, opened) {
		err = errors.New("ticket directory parent changed during open")
	}
	if err != nil {
		return errors.Join(err, tickets.Close())
	}
	q.tickets = tickets
	return nil
}
