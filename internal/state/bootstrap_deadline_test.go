// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

func TestBootstrapDeadlineWithSlowLedgerWrites(t *testing.T) {
	for _, tc := range []struct {
		name       string
		seeded     bool
		shorter    bool
		noDeadline bool
	}{
		{name: "longer caller deadline"},
		{name: "longer caller deadline seeds existing directory", seeded: true},
		{name: "shorter caller deadline", shorter: true},
		{name: "no caller deadline", noDeadline: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.seeded {
				if err := os.Mkdir(filepath.Join(dir, WorkingDirName), 0700); err != nil {
					t.Fatal(err)
				}
			}
			ctx := testsupport.DeadlineContext(t)
			if tc.noDeadline {
				ctx = t.Context()
			}
			if tc.shorter {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 10*time.Millisecond)
				defer cancel()
			}
			slowBootstrapWrites(t, ctx)
			outcome, err := InitWorkingDirIfAbsentContext(ctx, dir)
			if tc.shorter || tc.noDeadline {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("blocked writes: outcome=%v, err=%v; want DeadlineExceeded", outcome, err)
				}
				return
			}
			want := BootstrapCreated
			if tc.seeded {
				want = BootstrapSeeded
			}
			if err != nil || outcome != want {
				t.Fatalf("slow bootstrap: outcome=%v, err=%v; want %v", outcome, err, want)
			}
			if report, err := AuditWorkingDir(dir); err != nil || !report.Valid {
				t.Fatalf("slow bootstrap left invalid ledger: %+v, %v", report, err)
			}
		})
	}
}

// slowBootstrapWrites replaces package seams only in serial tests. Its deadline
// channel delays real ledger writes past the fallback; no elapsed-time assertion
// or scheduler-dependent sleep decides whether the bootstrap succeeded.
func slowBootstrapWrites(t *testing.T, caller context.Context) {
	t.Helper()
	budget, initialize := bootstrapFallbackTimeout, bootstrapInitializeFiles
	t.Cleanup(func() {
		bootstrapFallbackTimeout, bootstrapInitializeFiles = budget, initialize
	})
	bootstrapFallbackTimeout = 20 * time.Millisecond
	parent := testsupport.DeadlineContext(t)
	bootstrapInitializeFiles = func(ctx context.Context, working *os.Root) error {
		stalled, cancel := context.WithTimeout(parent, 2*bootstrapFallbackTimeout)
		defer cancel()
		select {
		case <-ctx.Done():
		case <-stalled.Done():
		}
		deadline, bounded := ctx.Deadline()
		if !bounded {
			return errors.New("bootstrap writes have no deadline")
		}
		if want, ok := caller.Deadline(); ok && !deadline.Equal(want) {
			return fmt.Errorf("bootstrap deadline %v differs from caller deadline %v", deadline, want)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return initialize(ctx, working)
	}
}
