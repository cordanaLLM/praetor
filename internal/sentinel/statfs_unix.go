//go:build !windows

package sentinel

import (
	"fmt"
	"syscall"
)

var statfsFunc = syscall.Statfs

func readHostDisk(path string) (uint64, uint64, error) {
	var stat syscall.Statfs_t
	if err := statfsFunc(path, &stat); err != nil {
		return 0, 0, fmt.Errorf("statfs failed for path %q: %w", path, err)
	}
	if stat.Bsize <= 0 {
		return 0, 0, fmt.Errorf("invalid statfs block size: %d", stat.Bsize)
	}
	bsize := uint64(stat.Bsize)
	total := stat.Blocks * bsize
	free := stat.Bavail * bsize
	return total, free, nil
}
