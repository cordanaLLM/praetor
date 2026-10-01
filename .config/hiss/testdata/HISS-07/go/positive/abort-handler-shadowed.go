package p

import "net/http"

type carrier struct{ ErrAbortHandler error }

var _ = http.MethodGet

// The parameter named http hides the package, so this panics with the parameter's field.
func Fail(http carrier) {
	panic(http.ErrAbortHandler)
}
