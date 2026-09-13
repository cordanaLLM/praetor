// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import (
	"errors"
	"fmt"
	"os"
)

// Verify the identity after the nonblocking open so concurrent replacement by a
// FIFO cannot stall source or cache readback before validation runs.
func openStableTranscriptFile(root *os.Root, name string, before os.FileInfo) (*os.File, error) {
	file, err := openTranscriptFile(root, name)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err == nil && (!opened.Mode().IsRegular() || !os.SameFile(before, opened)) {
		err = fmt.Errorf("transcript file changed while opening")
	}
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}
