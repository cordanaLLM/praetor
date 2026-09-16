//go:build !linux

package dogfood

import (
	"context"
	"errors"
)

func scheduleExecutableSHA(_ context.Context) (string, error) {
	return "", errors.New("dogfood scheduling requires Linux procfs executable identity")
}

// SchedulingSupported reports whether dogfood scheduling can run on this platform, and why
// not. The error is the one scheduling itself returns.
func SchedulingSupported() error {
	_, err := scheduleExecutableSHA(context.Background())
	return err
}
