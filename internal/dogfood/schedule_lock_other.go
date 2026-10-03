//go:build !unix

package dogfood

import (
	"errors"
	"os"
)

func lockSchedule(_ *os.Root, _ bool) (func() error, bool, error) {
	return nil, false, errors.New("dogfood scheduling requires Unix advisory file locks")
}
