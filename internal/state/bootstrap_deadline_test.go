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

// bootstrapDeadlineCase parameterizes table-driven bootstrap deadline tests.
type bootstrapDeadlineCase struct {
	name       string
	seeded     bool
	shorter    bool
	noDeadline bool
	want       BootstrapOutcome
	wantErr    error
}

// TestBootstrapDeadlineWithSlowLedgerWrites verifies that ledger bootstrap
// respects the caller's context deadline when ledger writes stall past the
// fallback budget. A caller deadline longer than the fallback survives slow
// ledger writes, a shorter one fails with context.DeadlineExceeded, and a
// context without one gets the fallback.
func TestBootstrapDeadlineWithSlowLedgerWrites(t *testing.T) {
	t.Parallel()
	for _, tc := range []bootstrapDeadlineCase{
		{name: "longer caller deadline", want: BootstrapCreated},
		{name: "longer caller deadline seeds existing directory", seeded: true, want: BootstrapSeeded},
		{name: "shorter caller deadline", shorter: true, wantErr: context.DeadlineExceeded},
		{name: "no caller deadline", noDeadline: true, wantErr: context.DeadlineExceeded},
	} {
		t.Run(tc.name, tc.run)
	}
}

// run executes one bootstrap deadline test case against a temporary directory,
// proving that a caller deadline longer than the fallback survives slow ledger
// writes, a shorter one fails with context.DeadlineExceeded, and a context
// without one gets the fallback.
func (tc bootstrapDeadlineCase) run(t *testing.T) {
	t.Parallel()
	t.Helper()
	dir := t.TempDir()
	if tc.seeded {
		// a directory needs the owner execute bit; 0700 is least privilege for a directory the test writes into
		// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission
		if err := os.Mkdir(filepath.Join(dir, WorkingDirName), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx := tc.callerContext(t)
	deps := slowBootstrapDeps(ctx, t)
	outcome, err := bootstrapWorkingDir(ctx, dir, deps)
	if tc.wantErr != nil {
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("blocked writes: outcome=%v, err=%v; want %v", outcome, err, tc.wantErr)
		}
		return
	}
	if err != nil || outcome != tc.want {
		t.Fatalf("slow bootstrap: outcome=%v, err=%v; want %v", outcome, err, tc.want)
	}
	if report, err := AuditWorkingDirContext(ctx, dir); err != nil || !report.Valid {
		t.Fatalf("slow bootstrap left invalid ledger: %+v, %v", report, err)
	}
}

// callerContext builds the context for the test case, configuring whether a
// caller deadline longer than the fallback survives slow ledger writes, a
// shorter one fails with context.DeadlineExceeded, or a context without one
// gets the fallback.
func (tc bootstrapDeadlineCase) callerContext(t *testing.T) context.Context {
	t.Helper()
	if tc.noDeadline {
		return t.Context()
	}
	ctx := testsupport.DeadlineContext(t)
	if tc.shorter {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Millisecond)
		t.Cleanup(cancel)
	}
	return ctx
}

// slowBootstrapDeps builds a bootstrapDeps value whose initialize function delays
// real ledger writes past the fallback budget; no package globals are modified.
func slowBootstrapDeps(caller context.Context, t *testing.T) bootstrapDeps {
	t.Helper()
	const fallback = 20 * time.Millisecond
	parent := testsupport.DeadlineContext(t)
	return bootstrapDeps{
		fallback: fallback,
		initialize: func(ctx context.Context, working *os.Root) error {
			stalled, cancel := context.WithTimeout(parent, 2*fallback)
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
			return initializeWorkingFiles(ctx, working)
		},
	}
}
