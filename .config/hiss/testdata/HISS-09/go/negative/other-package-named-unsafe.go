package p

import unsafe "example.com/shim"

// Read calls a function of a package that is only named unsafe in this file. The
// selector resolves to example.com/shim, not to package unsafe.
func Read(v int) int {
	return unsafe.Pointer(v)
}
