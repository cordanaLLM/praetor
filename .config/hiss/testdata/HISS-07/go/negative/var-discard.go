package p

// Discarding a variable cannot discard an error.
func G(v int) {
	_ = v
}
