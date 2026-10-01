package p

import "net/http"

// The sentinel is only recovered on the goroutine net/http runs the handler on. Panicking
// with it on a goroutine the code started itself ends the process, but which goroutine runs
// a function is not visible in syntax, so the scanner accepts the sentinel everywhere.
func Start() {
	go func() {
		panic(http.ErrAbortHandler)
	}()
}
