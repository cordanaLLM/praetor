//go:build !linux

package dogfood

import (
	"context"
	"errors"
)

// errSchedulingUnsupported is why scheduling cannot run where procfs is absent.
var errSchedulingUnsupported = errors.New("dogfood scheduling requires Linux procfs executable identity")

func scheduleExecutableSHA(_ context.Context) (string, error) {
	return "", errSchedulingUnsupported
}

// SchedulingSupported reports whether dogfood scheduling can run on this platform, and why
// not. The error is the one scheduling itself returns.
func SchedulingSupported() error {
	return errSchedulingUnsupported
}
