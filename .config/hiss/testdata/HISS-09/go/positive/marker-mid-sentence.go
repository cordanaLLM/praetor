package p

import "unsafe"

// First names the marker in prose without opening a proof with it.
func First(b []byte) *byte {
	// This line is not a SAFETY: proof, it only mentions one.
	return (*byte)(unsafe.Pointer(&b[0]))
}
