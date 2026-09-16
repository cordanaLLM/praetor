//go:build linux

package dogfood

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

// Hash the running executable descriptor, not os.Executable's path: Go strips
// procfs's deleted suffix, so replacement could otherwise identify a newer binary.
func scheduleExecutableSHA(ctx context.Context) (sum string, err error) {
	file, err := os.Open("/proc/self/exe")
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	before, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() || before.Size() > 128<<20 {
		return "", errors.New("running executable must be at most 128 MiB")
	}
	sum, size, err := hashRunningScheduleFile(ctx, file)
	if err != nil {
		return "", err
	}
	after, err := file.Stat()
	if err != nil {
		return "", err
	}
	if before.Size() != int64(size) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return "", errors.New("running executable changed while hashing")
	}
	return sum, nil
}

func hashRunningScheduleFile(ctx context.Context, file *os.File) (string, int, error) {
	hash := sha256.New()
	buffer := make([]byte, 32768)
	size := 0
	for i := 0; i <= 4096; i++ {
		if err := ctx.Err(); err != nil {
			return "", 0, err
		}
		n, readErr := file.Read(buffer)
		size += n
		if size > 128<<20 {
			return "", 0, errors.New("running executable exceeds 128 MiB")
		}
		if _, err := hash.Write(buffer[:n]); err != nil {
			return "", 0, err
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", 0, readErr
		}
	}

	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

// SchedulingSupported reports whether dogfood scheduling can run on this platform. It
// identifies its own executable through Linux procfs, which this build provides.
func SchedulingSupported() error { return nil }
