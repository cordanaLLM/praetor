package p

import . "unsafe"

// Hold passes a raw pointer through without converting anything. A dot-imported unsafe
// name is checked only in call position, so this use is not reported, while the same
// signature spelled unsafe.Pointer is.
func Hold(p Pointer) Pointer {
	return p
}
