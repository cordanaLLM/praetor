package p

import "errors"

// A local named ErrAbortHandler is not the net/http sentinel; nothing recovers this panic.
func Fail() {
	ErrAbortHandler := errors.New("abort")
	panic(ErrAbortHandler)
}
