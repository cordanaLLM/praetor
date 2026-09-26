package lib

import "os"

// A receiver, parameter, named result or closure parameter named os hides the package for
// the whole function, so os.Exit there calls the local value's method, not the process exit.

var _ = os.Args

type exiter struct{}

func (exiter) Exit(int) {}

func Param(os exiter) { os.Exit(1) }

func Result() (os exiter) {
	os.Exit(1)
	return os
}

func (os exiter) Receiver() { os.Exit(1) }

func Closure() func(exiter) {
	return func(os exiter) { os.Exit(1) }
}
