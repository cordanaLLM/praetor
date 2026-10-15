// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !linux

package dogfood

import (
	"context"
	"errors"
	"os"
)

// errScheduleTranscriptsUnsupported is why the schedule transcript store cannot run here: it
// judges its files by POSIX mode, which decides nothing off Linux, where scheduling runs
// (#132).
var errScheduleTranscriptsUnsupported = errors.New("the dogfood schedule transcript store requires Linux file modes")

func openPrivateScheduleDirectory(_ *os.Root, _, _ string) (*os.Root, error) {
	return nil, errScheduleTranscriptsUnsupported
}

func readStoredScheduleTranscript(_ context.Context, _ *os.Root, _ string, _ SuiteTranscript, _ string) ([]byte, bool, error) {
	return nil, false, errScheduleTranscriptsUnsupported
}
