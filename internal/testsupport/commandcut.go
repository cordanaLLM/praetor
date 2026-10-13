// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// FixtureCommandError returns err, naming the bound when the fixture command it came from was cut
// by its context deadline. exec.CommandContext kills a command whose deadline passed, and on
// Windows the kill (TerminateProcess with code 1) surfaces as a bare "exit status 1" with no
// output: a slow git on a loaded runner then reads like git failing silently (#1275). The
// gate's own worktree stage met the same effect (internal/gating, createStageWorktree). A nil
// err, or a failure the deadline did not cause, is returned unchanged.
func FixtureCommandError(ctx context.Context, bound, elapsed time.Duration, err error) error {
	if err == nil || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("cut by the %s fixture bound after %s, so the command was killed "+
		"(a killed process reports exit status 1 and no output on Windows): %w", bound, elapsed.Round(time.Millisecond), err)
}
