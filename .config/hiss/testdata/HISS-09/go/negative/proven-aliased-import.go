package p

import us "unsafe"

// First reaches unsafe through a renamed import under a stated proof.
func First(b []byte) *byte {
	// SAFETY: every caller checks len(b) > 0, so &b[0] is within the allocation.
	return (*byte)(us.Pointer(&b[0]))
}
