package p

import http "example.com/fake/http"

// The package named http here is not net/http, so its ErrAbortHandler is an ordinary value.
func Fail() {
	panic(http.ErrAbortHandler)
}
