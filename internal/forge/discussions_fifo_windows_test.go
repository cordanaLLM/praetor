//go:build windows

package forge

import "testing"

func TestTranscribeDiscussionToADR_Boundary_FIFOEntry(t *testing.T) {
	t.Skip("FIFOs are not supported on Windows")
}
