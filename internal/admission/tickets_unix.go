// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package admission

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// ticketName uses fixed-width decimal numbering so each positive ticket has one canonical
// directory-entry name.
func ticketName(ticket uint64) string { return fmt.Sprintf("ticket-%020d", ticket) }

// entries reads at most MaxTickets plus one entries from the pinned ticket directory and refuses
// overflow before scanning. The caller holds queue.lock to serialize the snapshot with ticket creation
// and removal.
func (q *slotQueue) entries() (entries []os.DirEntry, resultErr error) {
	dir, err := q.tickets.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, dir.Close()) }()
	entries, err = dir.ReadDir(MaxTickets + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > MaxTickets {
		return nil, errors.New("heavy-step ticket limit 4096 reached; refused")
	}
	return entries, nil
}

// liveTickets validates canonical positive ticket names, reaps unlocked tickets, and returns locked
// tickets in numeric order while the caller holds queue.lock. Both the directory snapshot and
// resulting scan are bounded by MaxTickets.
func (q *slotQueue) liveTickets(ctx context.Context) ([]uint64, error) {
	entries, err := q.entries()
	if err != nil {
		return nil, err
	}
	live := make([]uint64, 0)
	count := 0
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "ticket-") {
			continue
		}
		count++
		if count > MaxTickets {
			return nil, errors.New("heavy-step ticket limit 4096 reached; refused")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		number, err := strconv.ParseUint(strings.TrimPrefix(entry.Name(), "ticket-"), 10, 64)
		if err != nil || number == 0 || entry.Name() != ticketName(number) {
			return nil, fmt.Errorf("invalid heavy-step ticket %q; refused", entry.Name())
		}
		alive, err := q.ticketAlive(entry.Name())
		if err != nil {
			return nil, err
		}
		if alive {
			live = append(live, number)
		}
	}
	slices.Sort(live)
	return live, nil
}

// ticketAlive treats a busy kernel lock as a live waiter and removes an unlocked ticket as dead,
// regardless of its file contents. The caller holds queue.lock across the lock check and unlink so
// ticket creation and readers cannot race removal.
func (q *slotQueue) ticketAlive(name string) (alive bool, resultErr error) {
	_, release, busy, err := util.OpenPrivateLock(q.tickets, name, os.O_RDWR, "heavy-step ticket")
	if err != nil || busy {
		return busy, err
	}
	defer func() { resultErr = errors.Join(resultErr, release()) }()
	// The directory lock serializes unlink with ticket creation and every reader.
	if err := q.tickets.Remove(name); err != nil {
		return false, err
	}
	return false, nil
}

// takeTicket creates and retains the next ticket lock after the greatest sorted live number while the
// caller holds queue.lock. Full capacity, numbering exhaustion, or an unexpectedly busy new ticket
// refuses admission.
func (q *slotQueue) takeTicket(live []uint64) error {
	if len(live) >= MaxTickets {
		return errors.New("heavy-step ticket limit 4096 reached; refused")
	}
	next := uint64(1)
	if len(live) > 0 {
		last := live[len(live)-1]
		if last == math.MaxUint64 {
			return errors.New("heavy-step ticket numbering exhausted; refused")
		}
		next = last + 1
	}
	_, release, busy, err := util.OpenPrivateLock(q.tickets, ticketName(next), os.O_RDWR|os.O_CREATE|os.O_EXCL, "heavy-step ticket")
	if err != nil {
		return err
	}
	if busy {
		return errors.New("new heavy-step ticket unexpectedly locked; refused")
	}
	q.ticket, q.ticketRelease = next, release
	return nil
}
