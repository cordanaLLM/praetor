package p

import "unsafe"

// Window builds a slice over raw memory without a proof of its extent.
func Window(p *byte, n int) []byte {
	return unsafe.Slice(p, n)
}
