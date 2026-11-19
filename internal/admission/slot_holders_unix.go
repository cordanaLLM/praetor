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
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// trySlots scans at most MaxHeavy locks while the caller holds queue.lock and permits only the lowest
// live ticket to retain a free slot. A dead holder has released its kernel lock, so stale metadata
// cannot reserve capacity.
func (q *slotQueue) trySlots(ctx context.Context, label string, tickets []uint64) (*Lease, string, error) {
	labels := make([]string, 0, q.slots.MaxHeavy)
	position := slices.Index(tickets, q.ticket) + 1
	first := position == 1
	for slot := 1; slot <= q.slots.MaxHeavy; slot++ {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		name := fmt.Sprintf("slot-%04d", slot)
		file, release, busy, err := util.OpenPrivateLock(q.root, name, os.O_RDWR|os.O_CREATE, "heavy-step slot")
		if err != nil {
			return nil, "", err
		}
		if busy {
			label, err := q.holder(name)
			if err != nil {
				return nil, "", err
			}
			labels = append(labels, label)
			continue
		}
		if first {
			return q.claimSlot(file, release, label, slot)
		}
		if err := release(); err != nil {
			return nil, "", err
		}
	}
	reason := fmt.Sprintf("%d of %d heavy steps running: %s; position %d of %d", len(labels), q.slots.MaxHeavy, strings.Join(labels, "; "), position, len(tickets))
	return nil, reason, nil
}

// claimSlot replaces holder metadata and removes the owning ticket while the caller holds queue.lock
// and the slot lock. Failure releases the slot; success transfers its lock to the returned lease for
// the heavy step.
func (q *slotQueue) claimSlot(file *os.File, release func() error, label string, slot int) (*Lease, string, error) {
	text := fmt.Sprintf("%s %q", time.Now().UTC().Format(time.RFC3339Nano), label[:min(len(label), 256)])
	if err := file.Truncate(0); err != nil {
		return nil, "", errors.Join(err, release())
	}
	if _, err := file.WriteString(text); err != nil {
		return nil, "", errors.Join(err, release())
	}
	if err := q.tickets.Remove(ticketName(q.ticket)); err != nil {
		return nil, "", errors.Join(err, release())
	}
	return &Lease{Text: fmt.Sprintf("slot %d of %d", slot, q.slots.MaxHeavy), file: file, release: release}, "", nil
}

// holder reads at most 1024 bytes of regular, non-symlink slot metadata while the caller holds
// queue.lock after observing a busy slot. Empty metadata is reported explicitly and never establishes
// lock ownership.
func (q *slotQueue) holder(name string) (label string, resultErr error) {
	file, err := q.root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("slot %s is not regular", name)
	}
	data, err := io.ReadAll(io.LimitReader(file, 1024))
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "holder metadata unavailable", nil
	}
	return string(data), nil
}
