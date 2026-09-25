//go:build windows

package hiss

import "testing"

// TestScan_FIFOIsRefusedAndCounted states why the FIFO fixture does not run on Windows
// (HISS-21): the platform has no mkfifo and no FIFO entry in its filesystem namespace, so
// the fixture cannot be built there. The guard under test, os.FileMode.IsRegular, is
// platform-neutral, and the walk's file bound is covered on every platform by
// TestScan_FileBoundMarksTruncation.
func TestScan_FIFOIsRefusedAndCounted(t *testing.T) {
	t.Skip("windows has no mkfifo: a FIFO fixture cannot be created, and named pipes do not appear as filesystem entries a walk visits")
}
