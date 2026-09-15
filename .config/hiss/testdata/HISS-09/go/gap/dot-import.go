package p

import . "unsafe"

// First uses package unsafe through a dot import, so the call is a bare identifier
// rather than a selector and the SelectorExpr check never sees it.
func First(b []byte) *byte {
	return (*byte)(Pointer(&b[0]))
}
