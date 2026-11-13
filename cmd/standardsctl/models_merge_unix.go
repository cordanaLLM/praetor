//go:build unix

package main

import (
	"os"
	"syscall"
)

// openMergeInput refuses leaf links and prevents replacement FIFOs from blocking the open.
func openMergeInput(path string) (*os.File, error) {
	// #nosec G304 -- explicit review path; readMergeFile checks the descriptor and byte bounds.
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}
