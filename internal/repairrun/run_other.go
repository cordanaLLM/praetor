//go:build !linux

package repairrun

import (
	"errors"
	"os"
)

func openRegular(_ *os.Root, _ string) (*os.File, error) {
	return nil, errors.New("repair execution requires Linux file isolation")
}
func lockState(_ *os.Root, _ bool) (*os.File, bool, error) {
	return nil, false, errors.New("repair execution requires Linux locking")
}
