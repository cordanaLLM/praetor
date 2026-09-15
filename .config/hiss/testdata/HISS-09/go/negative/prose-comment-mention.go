package p

// Count is pure Go. The prose below mentions unsafe.Pointer only to explain why this
// implementation does not use it.
func Count(b []byte) int {
	return len(b)
}
