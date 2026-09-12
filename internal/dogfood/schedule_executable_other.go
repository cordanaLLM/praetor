//go:build !linux

package dogfood

import (
	"context"
	"errors"
)

func scheduleExecutableSHA(_ context.Context) (string, error) {
	return "", errors.New("dogfood scheduling requires Linux procfs executable identity")
}
