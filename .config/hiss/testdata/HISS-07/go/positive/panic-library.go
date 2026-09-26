package p

// panic in library code aborts instead of returning an error.
func Must(ok bool) {
	if !ok {
		panic("not ok")
	}
}
