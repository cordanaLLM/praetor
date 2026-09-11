//go:build windows

package sentinel

import (
	"testing"
)

func TestMockGetDiskFreeSpace_Boundary(t *testing.T) {
	oldFunc := getDiskFreeSpaceFunc
	defer func() { getDiskFreeSpaceFunc = oldFunc }()

	getDiskFreeSpaceFunc = func(directoryName string) (uint64, uint64, error) {
		return 1000000 * 4096, 500000 * 4096, nil
	}

	total, free, err := readHostDisk("dummy")
	if err != nil {
		t.Fatalf("unexpected readHostDisk error: %v", err)
	}
	if total != 1000000*4096 {
		t.Errorf("expected total %d, got %d", 1000000*4096, total)
	}
	if free != 500000*4096 {
		t.Errorf("expected free %d, got %d", 500000*4096, free)
	}
}
