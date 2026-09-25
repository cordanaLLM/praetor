package p

import . "unsafe"

// First uses package unsafe through a dot import, so the conversion is a call on a bare
// identifier rather than a selector.
func First(b []byte) *byte {
	return (*byte)(Pointer(&b[0]))
}
