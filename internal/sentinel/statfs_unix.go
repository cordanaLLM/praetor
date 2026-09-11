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
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	return total, free, nil
}
