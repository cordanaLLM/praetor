package p

import (
	"fmt"
	"net/http"
)

// Only the exact sentinel is accepted; a wrapped error is a new value in an ordinary panic.
func Fail() {
	panic(fmt.Errorf("abort: %w", http.ErrAbortHandler))
}
