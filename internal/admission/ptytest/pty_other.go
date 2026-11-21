// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !linux

package ptytest

import "testing"

// Run states why the Linux controlling-terminal fixture cannot run on this platform.
func Run(t *testing.T, _ string) {
	t.Helper()
	t.Skip("controlling PTY fixture requires Linux /dev/ptmx and TIOCSPTLCK/TIOCGPTN ioctls")
}

// Check states why the Linux terminal-attribute child cannot run on this platform.
func Check(t *testing.T) {
	t.Helper()
	t.Skip("controlling PTY child requires Linux TCGETS/TCSETS ioctls")
}
