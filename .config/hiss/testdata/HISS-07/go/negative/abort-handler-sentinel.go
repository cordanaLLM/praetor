package p

import (
	"errors"
	"net/http"
)

// net/http documents panic(http.ErrAbortHandler) as the way a handler aborts its response;
// the server recovers it and drops the connection. Recovery middleware re-panics the exact
// sentinel instead of completing a response it can no longer replace.
func Guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(http.ErrAbortHandler)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// Abort is the bare form.
func Abort() {
	panic(http.ErrAbortHandler)
}

// A plain reference to the sentinel is not a panic at all.
var sentinel = http.ErrAbortHandler
