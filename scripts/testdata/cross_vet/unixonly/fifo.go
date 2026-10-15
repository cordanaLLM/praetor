// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package fifo is cross_vet's planted defect (#1026): it calls a Unix-only syscall without a
// build constraint, so it compiles for Linux and macOS and fails for Windows.
package fifo

import "syscall"

// Make creates a named pipe at path.
func Make(path string) error {
	return syscall.Mkfifo(path, 0o600)
}
