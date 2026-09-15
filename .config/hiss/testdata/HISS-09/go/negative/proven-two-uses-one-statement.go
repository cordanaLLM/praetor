package p

import "unsafe"

// Offset advances a pointer; one proof covers both unsafe references in the statement.
func Offset(p *byte, n int) *byte {
	// SAFETY: n is bounded by the allocation length checked by the caller.
	return (*byte)(unsafe.Add(unsafe.Pointer(p), n))
}
