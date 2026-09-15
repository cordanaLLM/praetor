package p

import "os"

// The suppression names the linter it disables and says why, and it really does suppress a
// finding on that line, so it is a visible exception rather than a hidden warning.
func JustifiedSuppression(path string) {
	//nolint:errcheck // best-effort cleanup of a temporary file; a failure here is not actionable
	os.Remove(path)
}
