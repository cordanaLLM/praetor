package p

import "unsafe"

// First opens the proof with the marker and states it on the following line.
func First(b []byte) *byte {
	// SAFETY:
	// every caller checks len(b) > 0, so &b[0] is within the allocation.
	return (*byte)(unsafe.Pointer(&b[0]))
}
