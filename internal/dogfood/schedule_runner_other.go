//go:build !linux

package dogfood

import (
	"context"
	"errors"
	"os"
)

func hashScheduleConfiguredFile(_ context.Context, _ *os.Root, _ string) (string, error) {
	return "", errors.New("dogfood scheduling requires Linux procfs executable identity")
}
