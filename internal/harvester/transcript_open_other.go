//go:build !unix

// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import (
	"fmt"
	"os"
	"runtime"
)

func openTranscriptFile(root *os.Root, name string) (*os.File, error) {
	// Windows roots reject reserved devices and cannot address Unix FIFOs. Other
	// platforms lack the required rooted/nonblocking filesystem guarantees.
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("transcript snapshots require Unix or Windows")
	}
	return root.Open(name)
}
