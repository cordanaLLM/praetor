package dogfood

import (
	"context"
	"errors"
	"path/filepath"
)

func scheduleConfiguredBinarySHA(ctx context.Context, path string) (string, error) {
	root, err := openSuiteDirectory(ctx, filepath.Dir(path))
	if err != nil {
		return "", err
	}
	hash, hashErr := hashScheduleConfiguredFile(ctx, root, filepath.Base(path))
	return hash, errors.Join(hashErr, root.Close())
}

func verifyScheduleRunner(ctx context.Context, expected string) error {
	actual, err := scheduleExecutableSHA(ctx)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("running executable differs from configured runner_binary; restart with that reviewed binary")
	}
	return nil
}
