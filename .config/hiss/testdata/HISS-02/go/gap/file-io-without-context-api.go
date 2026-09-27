package p

import "os"

// os.ReadFile has no context-aware form, so no call site can attach a deadline and the
// scanner cannot demand one. A read of a FIFO or a network mount can block indefinitely.
func Load(path string) ([]byte, error) {
	return os.ReadFile(path)
}
