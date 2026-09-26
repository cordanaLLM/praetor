package p

import "unsafe"

// First carries the marker with nothing after it, which states no proof.
func First(b []byte) *byte {
	// SAFETY:
	return (*byte)(unsafe.Pointer(&b[0]))
}
