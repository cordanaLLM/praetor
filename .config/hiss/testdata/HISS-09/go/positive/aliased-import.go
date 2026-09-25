package p

import us "unsafe"

// First does exactly what unproven-pointer.go does, through a renamed import. The
// selector's package identifier resolves to unsafe through the import, whatever it is
// spelled.
func First(b []byte) *byte {
	return (*byte)(us.Pointer(&b[0]))
}
