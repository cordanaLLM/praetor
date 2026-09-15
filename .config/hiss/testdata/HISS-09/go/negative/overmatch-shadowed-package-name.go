package p

// shim has a Pointer field and the local variable below is named unsafe. No package
// unsafe is imported and nothing is dereferenced, yet the rule matches the identifier
// spelling rather than the resolved package, so this legitimate file IS reported today.
type shim struct{ Pointer int }

// Read returns the shim's field.
func Read() int {
	var unsafe shim
	return unsafe.Pointer
}
