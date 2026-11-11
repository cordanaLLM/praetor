// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package secrets_test

import "testing"

// TestGate_EventPipeRejectsPromptly is skipped on Windows: syscall.Mkfifo does not exist there, so no
// named pipe can stand in for the event payload. The deadline path is covered on unix platforms
// (gate_fifo_unix_test.go); the regular-file and symlink refusals run everywhere.
func TestGate_EventPipeRejectsPromptly(t *testing.T) {
	t.Skip("named pipes via syscall.Mkfifo are unavailable on Windows; the payload deadline is tested on unix")
}
