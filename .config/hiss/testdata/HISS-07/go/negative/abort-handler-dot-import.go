package p

import . "net/http"

// A dot import puts ErrAbortHandler into the file block, so the bare name is the net/http
// sentinel.
func Abort() {
	panic(ErrAbortHandler)
}
