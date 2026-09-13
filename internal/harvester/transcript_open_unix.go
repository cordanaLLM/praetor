//go:build unix

// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import (
	"os"
	"syscall"
)

// Nonblocking open prevents a concurrent regular-file-to-FIFO replacement from
// hanging before the identity/type check in transcript source/cache reader. Regular files ignore it.
func openTranscriptFile(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
