//go:build unix

package router

import (
	"os"
	"syscall"
)

// A replacement FIFO must not block before the opened-file identity/type check.
func openRoutingInput(path string) (*os.File, error) {
	// #nosec G304 -- explicit config/snapshot path; caller checks regular-file identity and size before reading. O_NONBLOCK prevents replacement FIFO hangs, O_NOFOLLOW rejects replacement leaf links.
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}
