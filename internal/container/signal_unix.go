// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !windows

package container

import (
	"syscall"
)

// raiseTermination delivers SIGTERM to the current process.
//
// The drain path is signal-driven, so proving it requires a process able to signal itself.
// syscall.Kill exists only on POSIX platforms; see signal_windows.go for what the Windows
// build reports instead (HISS-21: a platform that cannot run a check says so).
func raiseTermination() error {
	return syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
}
