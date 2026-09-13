//go:build linux

package dogfood

import (
	"context"
	"errors"
	"os"
)

func hashScheduleConfiguredFile(ctx context.Context, root *os.Root, name string) (sum string, err error) {
	before, err := root.Lstat(name)
	if err != nil {
		return "", err
	}
	if err := validateScheduleFileInfo(before, 128<<20, false); err != nil {
		return "", err
	}
	if before.Mode().Perm()&0o111 == 0 {
		return "", errors.New("configured runner must be executable")
	}
	file, err := openSuiteConfigFile(root, name)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	actual, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(before, actual) {
		return "", errors.New("configured runner changed during open")
	}
	sum, size, err := hashRunningScheduleFile(ctx, file)
	if err != nil {
		return "", err
	}
	return sum, checkPublicFileStable(root, name, file, before, size)
}
