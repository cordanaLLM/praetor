package p

import "unsafe"

// First defers its proof to later, which is not a proof.
func First(b []byte) *byte {
	// SAFETY: TODO
	return (*byte)(unsafe.Pointer(&b[0]))
}
